//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	binEnv        = "DNSMASQ_MANAGER_BIN"
	serviceToken  = "integration-test-token-not-a-secret-0123456789"
	serviceZone   = "home.example.test"
	managedFile   = "dnsmasq-manager.hosts"
	webhookMedia  = "application/external.dns.webhook+json;version=1"
	stopTimeout   = 10 * time.Second
	appName       = "app.home.example.test"
	appTXTName    = "a-app.home.example.test"
	appAAAATXT    = "aaaa-app.home.example.test"
	wildcardAddr  = "10.0.0.17"
	ownerTXTValue = `"heritage=external-dns,external-dns/owner=default,external-dns/resource=ingress/default/app"`
)

var (
	buildOnce sync.Once
	builtBin  string
	buildDir  string
	buildErr  error
)

// TestMain runs the tests and then removes the binary serviceBinary built,
// if it built one.
func TestMain(m *testing.M) {
	code := m.Run()
	if buildDir != "" {
		_ = os.RemoveAll(buildDir)
	}
	os.Exit(code)
}

// serviceBinary returns the path of the dnsmasq-manager binary under test:
// DNSMASQ_MANAGER_BIN when it is set, otherwise a binary built once with go
// build into a temporary directory that TestMain removes. It fails the test
// when the build fails.
func serviceBinary(t *testing.T) string {
	t.Helper()
	if bin := os.Getenv(binEnv); bin != "" {
		return bin
	}
	buildOnce.Do(func() {
		buildDir, buildErr = os.MkdirTemp("", "dnsmasq-manager-integration-")
		if buildErr != nil {
			return
		}
		builtBin = filepath.Join(buildDir, "dnsmasq-manager")
		out, err := exec.Command("go", "build", "-o", builtBin,
			"github.com/gsdevme/dnsmasq-manager/cmd/dnsmasq-manager").CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("go build: %w: %s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatalf("build the service (or set %s): %v", binEnv, buildErr)
	}
	return builtBin
}

// lockedBuffer is an io.Writer safe for the process's output goroutines and
// the test reading it at the same time.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p to the buffer.
func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns everything written so far.
func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// service is a running dnsmasq-manager serve process started by
// startService, with the addresses its listeners reported.
type service struct {
	cmd     *exec.Cmd
	output  *lockedBuffer
	done    chan struct{}
	waitErr error
	api     string
	webhook string
	web     string
}

// startService runs `dnsmasq-manager serve` beside s, managing s.hostsDir and
// probing s, with every listener on a kernel-chosen port of 127.0.0.1. The
// process gets only the DNSMASQ_MANAGER_* variables it needs, so neither the
// caller's environment nor DNSMASQ_MANAGER_BIN reaches it. It returns once
// every listener has logged its address and /healthz answers 200, and fails
// the test if the process exits first or that takes longer than five
// seconds. The process is killed in t.Cleanup, which also logs its output if
// the test failed.
func startService(t *testing.T, s *server) *service {
	t.Helper()
	bin := serviceBinary(t)
	svc := &service{output: &lockedBuffer{}, done: make(chan struct{})}
	svc.cmd = exec.Command(bin, "serve")
	svc.cmd.Env = []string{
		"DNSMASQ_MANAGER_ZONES=" + serviceZone,
		"DNSMASQ_MANAGER_ALLOWED_CIDRS=10.0.0.0/24,fd00:10::/64",
		"DNSMASQ_MANAGER_HOSTS_DIR=" + s.hostsDir,
		fmt.Sprintf("DNSMASQ_MANAGER_DNS_PROBE_ADDR=127.0.0.1:%d", s.port),
		"DNSMASQ_MANAGER_API_ADDR=127.0.0.1:0",
		"DNSMASQ_MANAGER_API_TOKEN=" + serviceToken,
		"DNSMASQ_MANAGER_API_PLAIN_HTTP=true",
		"DNSMASQ_MANAGER_WEBHOOK_ADDR=127.0.0.1:0",
		"DNSMASQ_MANAGER_WEBHOOK_ALLOWED_SOURCES=127.0.0.1/32",
		"DNSMASQ_MANAGER_WEB_ADDR=127.0.0.1:0",
	}
	svc.cmd.Stdout = svc.output
	svc.cmd.Stderr = svc.output
	if err := svc.cmd.Start(); err != nil {
		t.Fatalf("start the service: %v", err)
	}
	go func() {
		svc.waitErr = svc.cmd.Wait()
		close(svc.done)
	}()
	t.Cleanup(func() {
		_ = svc.cmd.Process.Kill()
		<-svc.done
		if t.Failed() {
			t.Logf("service output:\n%s", svc.output.String())
		}
	})

	svc.awaitListeners(t)
	deadline := time.Now().Add(pollTimeout)
	for {
		status, body, err := svc.healthz()
		if err == nil && status == http.StatusOK {
			return svc
		}
		if time.Now().After(deadline) {
			t.Fatalf("/healthz: want 200 within %v, last status %d, body %s, error %v", pollTimeout, status, body, err)
		}
		time.Sleep(pollInterval)
	}
}

// awaitListeners waits for the api, webhook and web listeners' "listening"
// log lines and records their addresses. It fails the test if the process
// exits first or the lines do not all appear within five seconds.
func (svc *service) awaitListeners(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(pollTimeout)
	for {
		addrs := listeningAddrs(svc.output.String())
		if addrs["api"] != "" && addrs["webhook"] != "" && addrs["web"] != "" {
			svc.api = "http://" + addrs["api"]
			svc.webhook = "http://" + addrs["webhook"]
			svc.web = "http://" + addrs["web"]
			return
		}
		select {
		case <-svc.done:
			t.Fatalf("the service exited before listening: %v", svc.waitErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the service did not report all listeners within %v, got %v", pollTimeout, addrs)
		}
		time.Sleep(pollInterval)
	}
}

// listeningAddrs returns, by listener name, the addr of every slog text line
// in output whose msg is "listening".
func listeningAddrs(output string) map[string]string {
	addrs := map[string]string{}
	for line := range strings.Lines(output) {
		fields := map[string]string{}
		for _, f := range strings.Fields(line) {
			if k, v, ok := strings.Cut(f, "="); ok {
				fields[k] = v
			}
		}
		if fields["msg"] == "listening" {
			addrs[fields["listener"]] = fields["addr"]
		}
	}
	return addrs
}

// stop sends the service SIGTERM and fails the test unless it exits with
// status 0 within ten seconds.
func (svc *service) stop(t *testing.T) {
	t.Helper()
	if err := svc.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM to the service: %v", err)
	}
	select {
	case <-svc.done:
	case <-time.After(stopTimeout):
		t.Fatalf("the service did not exit within %v of SIGTERM", stopTimeout)
	}
	if svc.waitErr != nil {
		t.Fatalf("the service exited with %v", svc.waitErr)
	}
}

// do sends a request with an optional JSON body and returns the status and
// body of the response.
func do(method, url, contentType string, header http.Header, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return 0, nil, err
	}
	for k, vs := range header {
		req.Header[k] = vs
	}
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	data, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	return resp.StatusCode, data, errors.Join(readErr, closeErr)
}

// healthz returns the status and body of GET /healthz, which needs no token.
func (svc *service) healthz() (int, []byte, error) {
	return do(http.MethodGet, svc.api+"/healthz", "", nil, nil)
}

// rest sends an authenticated REST request to path and fails the test on a
// transport error or a status other than want. It returns the body.
func (svc *service) rest(t *testing.T, method, path string, body any, want int) []byte {
	t.Helper()
	header := http.Header{"Authorization": {"Bearer " + serviceToken}}
	status, data, err := do(method, svc.api+path, "application/json", header, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	if status != want {
		t.Fatalf("%s %s: status %d, want %d, body %s", method, path, status, want, data)
	}
	return data
}

// hook sends a webhook request to path the way external-dns v0.24.0 does,
// with the webhook media type in Accept and Content-Type, and fails the test
// on a transport error or a status other than want. It returns the body.
func (svc *service) hook(t *testing.T, method, path string, body any, want int) []byte {
	t.Helper()
	header := http.Header{"Accept": {webhookMedia}}
	status, data, err := do(method, svc.webhook+path, webhookMedia, header, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	if status != want {
		t.Fatalf("%s %s: status %d, want %d, body %s", method, path, status, want, data)
	}
	return data
}

// putRecord sets name's record of rtype to values over REST.
func (svc *service) putRecord(t *testing.T, name, rtype string, values ...string) {
	t.Helper()
	svc.rest(t, http.MethodPut, "/v1/records/"+name,
		map[string]any{"type": rtype, "values": values, "ttl": 300}, http.StatusOK)
}

// managedContent returns the managed file in s.hostsDir.
func (s *server) managedContent(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.hostsDir, managedFile))
	if err != nil {
		t.Fatalf("read the managed file: %v", err)
	}
	return string(data)
}

// stop kills dnsmasq and waits for it to exit.
func (s *server) stop(t *testing.T) {
	t.Helper()
	if err := s.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill dnsmasq: %v", err)
	}
	_ = s.cmd.Wait()
}

// endpoint is one external-dns endpoint as v0.24.0 sends it.
type endpoint struct {
	DNSName    string            `json:"dnsName"`
	Targets    []string          `json:"targets"`
	RecordType string            `json:"recordType"`
	Labels     map[string]string `json:"labels,omitempty"`
}

// addressEndpoint returns the endpoint external-dns plans for an Ingress
// address, with the empty labels it carries.
func addressEndpoint(name, rtype string, targets ...string) endpoint {
	return endpoint{DNSName: name, Targets: targets, RecordType: rtype, Labels: map[string]string{}}
}

// ownerEndpoint returns the TXT registry's ownership record txtName for
// owned, as external-dns v0.24.0 sends it beside the record it owns.
func ownerEndpoint(txtName, owned string) endpoint {
	return endpoint{
		DNSName:    txtName,
		Targets:    []string{ownerTXTValue},
		RecordType: "TXT",
		Labels:     map[string]string{"ownedRecord": owned},
	}
}

// TestServeRESTAddUpdateDelete checks that a record added, changed and
// removed over REST is served by dnsmasq from the managed file, which holds
// `#@` lines, without any SIGHUP.
func TestServeRESTAddUpdateDelete(t *testing.T) {
	s := startDnsmasq(t)
	svc := startService(t, s)
	s.eventually(t, appName, wildcardAddr)

	svc.putRecord(t, appName, "A", "10.0.0.80")
	s.eventually(t, appName, "10.0.0.80")
	if got := s.managedContent(t); !strings.Contains(got, "\n#@ name "+appName+" api\n10.0.0.80 "+appName+"\n") {
		t.Fatalf("managed file lacks the record's #@ block:\n%s", got)
	}

	svc.putRecord(t, appName, "A", "10.0.0.81")
	s.eventually(t, appName, "10.0.0.81")

	svc.rest(t, http.MethodDelete, "/v1/records/"+appName, nil, http.StatusNoContent)
	s.eventually(t, appName, wildcardAddr)
}

// TestServeAAAA checks that an AAAA record set over REST is served, that it
// leaves the name's A record as it was, and that removing only the AAAA
// record leaves the A record served.
func TestServeAAAA(t *testing.T) {
	s := startDnsmasq(t)
	svc := startService(t, s)

	svc.putRecord(t, appName, "A", "10.0.0.80")
	s.eventually(t, appName, "10.0.0.80")
	svc.putRecord(t, appName, "AAAA", "fd00:10::80")
	s.eventuallyType(t, appName, "AAAA", "fd00:10::80")
	s.eventually(t, appName, "10.0.0.80")

	svc.putRecord(t, appName, "AAAA", "fd00:10::81")
	s.eventuallyType(t, appName, "AAAA", "fd00:10::81")

	svc.rest(t, http.MethodDelete, "/v1/records/"+appName+"?type=AAAA", nil, http.StatusNoContent)
	s.eventuallyNotLocal(t, appName, "AAAA")
	s.eventually(t, appName, "10.0.0.80")
}

// TestServeWebhookChangeSet replays the change sets external-dns v0.24.0
// sends for an Ingress with an IPv4 and an IPv6 address being created,
// readdressed and deleted, and checks dnsmasq serves each result. The TXT
// ownership records live only in the file's `#@` lines, which dnsmasq
// ignores, so it never answers them locally.
func TestServeWebhookChangeSet(t *testing.T) {
	s := startDnsmasq(t)
	svc := startService(t, s)

	svc.hook(t, http.MethodGet, "/", nil, http.StatusOK)
	svc.hook(t, http.MethodPost, "/records", map[string]any{
		"create": []endpoint{
			addressEndpoint(appName, "A", "10.0.0.80"),
			addressEndpoint(appName, "AAAA", "fd00:10::80"),
			ownerEndpoint(appTXTName, appName),
			ownerEndpoint(appAAAATXT, appName),
		},
	}, http.StatusNoContent)
	s.eventually(t, appName, "10.0.0.80")
	s.eventuallyType(t, appName, "AAAA", "fd00:10::80")
	if got := s.managedContent(t); !strings.Contains(got, "\n#@ txt ") {
		t.Fatalf("managed file lacks the TXT ownership lines:\n%s", got)
	}
	s.eventuallyNotLocal(t, appTXTName, "TXT")

	svc.hook(t, http.MethodPost, "/records", map[string]any{
		"updateOld": []endpoint{
			addressEndpoint(appName, "A", "10.0.0.80"),
			ownerEndpoint(appTXTName, appName),
		},
		"updateNew": []endpoint{
			addressEndpoint(appName, "A", "10.0.0.81"),
			ownerEndpoint(appTXTName, appName),
		},
	}, http.StatusNoContent)
	s.eventually(t, appName, "10.0.0.81")
	s.eventuallyType(t, appName, "AAAA", "fd00:10::80")
	s.eventuallyNotLocal(t, appTXTName, "TXT")

	svc.hook(t, http.MethodPost, "/records", map[string]any{
		"delete": []endpoint{
			addressEndpoint(appName, "A", "10.0.0.81"),
			addressEndpoint(appName, "AAAA", "fd00:10::80"),
			ownerEndpoint(appTXTName, appName),
			ownerEndpoint(appAAAATXT, appName),
		},
	}, http.StatusNoContent)
	s.eventually(t, appName, wildcardAddr)
	s.eventuallyNotLocal(t, appName, "AAAA")
	if got := string(svc.hook(t, http.MethodGet, "/records", nil, http.StatusOK)); strings.TrimSpace(got) != "[]" {
		t.Fatalf("GET /records after the delete: got %s, want []", got)
	}
	s.eventuallyNotLocal(t, appTXTName, "TXT")
}

// eventuallyNotLocal polls name's records of qtype until dnsmasq has no
// local answer for them, then checks dnsmasq is still alive by a positive
// query for readyName, and fails the test if either has not happened within
// five seconds. dnsmasq forwards a query of a type it holds nothing for, even
// for a name it serves another type of, to the mock config's upstreams,
// which never answer; so a timed-out query, like a NOERROR reply with no
// answer, means nothing local. Any other outcome, such as a refused query or
// another response code, does not.
func (s *server) eventuallyNotLocal(t *testing.T, name, qtype string) {
	t.Helper()
	deadline := time.Now().Add(pollTimeout)
	for {
		err := s.notLocal(name, qtype)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s %s: want no local answer within %v: %v", name, qtype, pollTimeout, err)
		}
		time.Sleep(pollInterval)
	}
	s.eventually(t, readyName, "10.0.0.40")
}

// rcodePattern finds the response code in dig's header comment.
var rcodePattern = regexp.MustCompile(`status: ([A-Z]+)`)

// notLocal returns nil when one dig query for name's records of qtype either
// times out or gets a NOERROR reply with no answer records, and otherwise an
// error describing what dig saw.
func (s *server) notLocal(name, qtype string) error {
	out, err := exec.Command("dig", "+noall", "+comments", "+answer", "+time=1", "+tries=1",
		"@127.0.0.1", "-p", strconv.Itoa(s.port), name, qtype).CombinedOutput()
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && exitErr.ExitCode() == 9 && bytes.Contains(out, []byte("timed out")) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("dig %s %s: %w: %s", name, qtype, err, bytes.TrimSpace(out))
	}
	m := rcodePattern.FindSubmatch(out)
	if m == nil {
		return fmt.Errorf("dig %s %s: no response code in %q", name, qtype, out)
	}
	if rcode := string(m[1]); rcode != "NOERROR" {
		return fmt.Errorf("dig %s %s: status %s, want NOERROR or no reply", name, qtype, rcode)
	}
	for line := range strings.Lines(string(out)) {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, ";") {
			return fmt.Errorf("dig %s %s: local answer %q", name, qtype, line)
		}
	}
	return nil
}

// TestServeStateSurvivesRestart checks that records added over REST and over
// the webhook are listed the same by both APIs after the service restarts,
// and are still served by dnsmasq.
func TestServeStateSurvivesRestart(t *testing.T) {
	s := startDnsmasq(t)
	svc := startService(t, s)

	svc.putRecord(t, "db.home.example.test", "A", "10.0.0.90")
	svc.hook(t, http.MethodPost, "/records", map[string]any{
		"create": []endpoint{
			addressEndpoint(appName, "A", "10.0.0.80"),
			ownerEndpoint(appTXTName, appName),
		},
	}, http.StatusNoContent)
	restBefore := svc.rest(t, http.MethodGet, "/v1/records", nil, http.StatusOK)
	hookBefore := svc.hook(t, http.MethodGet, "/records", nil, http.StatusOK)
	fileBefore := s.managedContent(t)
	svc.stop(t)

	svc = startService(t, s)
	if got := svc.rest(t, http.MethodGet, "/v1/records", nil, http.StatusOK); !bytes.Equal(got, restBefore) {
		t.Fatalf("GET /v1/records after restart:\n got %s\nwant %s", got, restBefore)
	}
	if got := svc.hook(t, http.MethodGet, "/records", nil, http.StatusOK); !bytes.Equal(got, hookBefore) {
		t.Fatalf("GET /records after restart:\n got %s\nwant %s", got, hookBefore)
	}
	if got := s.managedContent(t); got != fileBefore {
		t.Fatalf("managed file changed across restart:\n got %s\nwant %s", got, fileBefore)
	}
	s.eventually(t, "db.home.example.test", "10.0.0.90")
	s.eventually(t, appName, "10.0.0.80")

	status, page, err := do(http.MethodGet, svc.web+"/", "", nil, nil)
	if err != nil || status != http.StatusOK || !bytes.Contains(page, []byte(appName)) {
		t.Fatalf("web page after restart: status %d, error %v, want 200 listing %s:\n%s", status, err, appName, page)
	}
}

// TestServeHealthzReportsDnsmasqDown checks that /healthz turns 503 with a
// failing dnsmasq check once dnsmasq stops, while the hosts_dir check stays
// ok.
func TestServeHealthzReportsDnsmasqDown(t *testing.T) {
	s := startDnsmasq(t)
	svc := startService(t, s)
	s.stop(t)

	deadline := time.Now().Add(pollTimeout)
	for {
		status, body, err := svc.healthz()
		if err == nil && status == http.StatusServiceUnavailable {
			var got struct {
				Status string            `json:"status"`
				Checks map[string]string `json:"checks"`
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decode /healthz body %s: %v", body, err)
			}
			if got.Status != "unhealthy" || got.Checks["dnsmasq"] == "ok" || got.Checks["hosts_dir"] != "ok" {
				t.Fatalf("/healthz body %s: want unhealthy with dnsmasq failing and hosts_dir ok", body)
			}
			t.Logf("/healthz with dnsmasq down: %s", body)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("/healthz: want 503 within %v, last status %d, body %s, error %v", pollTimeout, status, body, err)
		}
		time.Sleep(pollInterval)
	}
}
