package cli_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsdevme/dnsmasq-manager/internal/cli"
	"github.com/gsdevme/dnsmasq-manager/internal/webhook"
)

var token = strings.Repeat("t", 32)

// syncBuffer is a bytes.Buffer safe for the concurrent writes of the logger
// and the reads of the test.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p under the lock.
func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns everything written so far.
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// serveEnv returns an environment for serve with a temporary or given hosts
// directory, plain HTTP and every listener on a port the kernel picks,
// followed by extra, which takes precedence.
func serveEnv(dir string, extra ...string) []string {
	env := append([]string{}, extra...)
	return append(env,
		"DNSMASQ_MANAGER_ZONES=home.example.test",
		"DNSMASQ_MANAGER_API_TOKEN="+token,
		"DNSMASQ_MANAGER_API_PLAIN_HTTP=true",
		"DNSMASQ_MANAGER_HOSTS_DIR="+dir,
		"DNSMASQ_MANAGER_DNS_PROBE_ADDR=127.0.0.1:1",
		"DNSMASQ_MANAGER_API_ADDR=127.0.0.1:0",
		"DNSMASQ_MANAGER_WEBHOOK_ADDR=127.0.0.1:0",
		"DNSMASQ_MANAGER_WEB_ADDR=127.0.0.1:0",
	)
}

// running is a serve command started in the background.
type running struct {
	stdout *syncBuffer
	cancel context.CancelFunc
	done   chan error
}

// startServe runs "serve" with env in the background. The test's cleanup
// stops it if the test has not.
func startServe(t *testing.T, env []string) *running {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{stdout: &syncBuffer{}, cancel: cancel, done: make(chan error, 1)}
	cmd := cli.NewRootCommandWithEnv(r.stdout, io.Discard, func() []string { return env })
	cmd.SetArgs([]string{"serve"})
	go func() { r.done <- cmd.ExecuteContext(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-r.done; err != nil {
			t.Logf("serve exited with: %v", err)
		}
	})
	return r
}

// stop cancels the command and returns the error it exited with, failing
// the test if it does not exit within ten seconds.
func (r *running) stop(t *testing.T) error {
	t.Helper()
	r.cancel()
	select {
	case err := <-r.done:
		r.done <- err
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop within ten seconds of cancel")
		return nil
	}
}

var listeningLine = regexp.MustCompile(`msg=listening listener=(\S+) addr=(\S+)`)

// listeners parses the listening log lines in out into listener name and
// address.
func listeners(out string) map[string]string {
	found := map[string]string{}
	for _, m := range listeningLine.FindAllStringSubmatch(out, -1) {
		found[m[1]] = m[2]
	}
	return found
}

// waitListening polls the command's stdout until every named listener has
// logged its address, and returns the addresses by name.
func (r *running) waitListening(t *testing.T, names ...string) map[string]string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		found := listeners(r.stdout.String())
		missing := false
		for _, n := range names {
			if found[n] == "" {
				missing = true
			}
		}
		if !missing {
			return found
		}
		select {
		case err := <-r.done:
			r.done <- err
			t.Fatalf("serve exited before listening: %v\nstdout:\n%s", err, r.stdout.String())
		case <-time.After(10 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("listeners %v not logged within ten seconds; stdout:\n%s", names, r.stdout.String())
		}
	}
}

// do sends a request, with a JSON Content-Type when it has a body, and
// returns the status and body, failing the test on a transport error.
func do(t *testing.T, method, url, auth, body string) (int, string) {
	t.Helper()
	contentType := ""
	if body != "" {
		contentType = "application/json"
	}
	return doWith(t, method, url, auth, contentType, body)
}

// doWith is do with Content-Type set to contentType, or none when it is empty.
func doWith(t *testing.T, method, url, auth, contentType, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(data)
}

// putApp sets app.home.example.test A 10.0.0.80 over REST at apiAddr.
func putApp(t *testing.T, apiAddr string) {
	t.Helper()
	status, body := do(t, http.MethodPut, "http://"+apiAddr+"/v1/records/app.home.example.test", token,
		`{"type":"A","values":["10.0.0.80"]}`)
	if status != http.StatusOK {
		t.Fatalf("PUT status = %d, body %s; want 200", status, body)
	}
}

func TestServeConfigError(t *testing.T) {
	var stdout bytes.Buffer
	cmd := cli.NewRootCommandWithEnv(&stdout, io.Discard, func() []string { return nil })
	cmd.SetArgs([]string{"serve"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("err = nil, want a config error")
	}
	if !strings.Contains(err.Error(), "DNSMASQ_MANAGER_ZONES") {
		t.Errorf("err = %q, want it to mention DNSMASQ_MANAGER_ZONES", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
}

func TestServeRefusesBadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dnsmasq-manager.hosts")
	content := []byte("10.0.0.80 app.home.example.test\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := cli.NewRootCommandWithEnv(io.Discard, io.Discard, func() []string { return serveEnv(dir) })
	cmd.SetArgs([]string{"serve"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("err = nil, want the managed file refused")
	}
	if !regexp.MustCompile(`^` + regexp.QuoteMeta(path) + `:\d+: `).MatchString(err.Error()) {
		t.Errorf("err = %q, want it to start with %q, a line number and a colon", err, path)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("managed file = %q, want it unchanged %q", got, content)
	}
}

func TestServeEndToEnd(t *testing.T) {
	dir := t.TempDir()
	r := startServe(t, serveEnv(dir))
	addrs := r.waitListening(t, "api", "webhook", "web")

	putApp(t, addrs["api"])

	data, err := os.ReadFile(filepath.Join(dir, "dnsmasq-manager.hosts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "10.0.0.80 app.home.example.test") {
		t.Errorf("managed file = %q, want it to contain the record", data)
	}

	status, body := do(t, http.MethodGet, "http://"+addrs["webhook"]+"/records", "", "")
	if status != http.StatusOK || !strings.Contains(body, `"dnsName":"app.home.example.test"`) {
		t.Errorf("webhook GET /records = %d %s; want 200 listing app.home.example.test", status, body)
	}

	status, body = do(t, http.MethodGet, "http://"+addrs["web"]+"/", "", "")
	if status != http.StatusOK || !strings.Contains(body, "app.home.example.test") {
		t.Errorf("web GET / = %d; want 200 containing app.home.example.test; body:\n%s", status, body)
	}

	if err := r.stop(t); err != nil {
		t.Errorf("serve after cancel: err = %v, want nil", err)
	}
}

// TestServeForcedPutDropsWebhookTXT pins that a TXT external-dns created with
// an ownedRecord label in another case or with a trailing dot is dropped when
// the REST API takes its name over.
func TestServeForcedPutDropsWebhookTXT(t *testing.T) {
	for _, label := range []string{"App.home.example.test", "app.home.example.test."} {
		t.Run(label, func(t *testing.T) {
			dir := t.TempDir()
			r := startServe(t, serveEnv(dir))
			addrs := r.waitListening(t, "api", "webhook")

			changes := `{"create":[{"dnsName":"app.home.example.test","targets":["10.0.0.80"],"recordType":"A"},` +
				`{"dnsName":"a-app.home.example.test","targets":["\"heritage=external-dns,external-dns/owner=default\""],` +
				`"recordType":"TXT","labels":{"ownedRecord":"` + label + `"}}]}`
			if status, body := doWith(t, http.MethodPost, "http://"+addrs["webhook"]+"/records", "", webhook.MediaType, changes); status != http.StatusNoContent {
				t.Fatalf("webhook POST /records = %d %s; want 204", status, body)
			}

			status, body := do(t, http.MethodPut, "http://"+addrs["api"]+"/v1/records/app.home.example.test?force=true", token,
				`{"type":"A","values":["10.0.0.81"]}`)
			if status != http.StatusOK {
				t.Fatalf("forced PUT = %d %s; want 200", status, body)
			}

			status, body = do(t, http.MethodGet, "http://"+addrs["webhook"]+"/records", "", "")
			if status != http.StatusOK || strings.Contains(body, "a-app.home.example.test") {
				t.Errorf("webhook GET /records = %d %s; want 200 without the TXT", status, body)
			}
			data, err := os.ReadFile(filepath.Join(dir, "dnsmasq-manager.hosts"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "#@ txt") {
				t.Errorf("managed file = %q, want no TXT line", data)
			}
			if err := r.stop(t); err != nil {
				t.Errorf("serve: err = %v, want nil", err)
			}
		})
	}
}

func TestServeRestartKeepsState(t *testing.T) {
	dir := t.TempDir()
	first := startServe(t, serveEnv(dir))
	putApp(t, first.waitListening(t, "api")["api"])
	if err := first.stop(t); err != nil {
		t.Fatalf("first serve: err = %v, want nil", err)
	}

	second := startServe(t, serveEnv(dir))
	api := second.waitListening(t, "api")["api"]
	status, body := do(t, http.MethodGet, "http://"+api+"/v1/records", token, "")
	if status != http.StatusOK || !strings.Contains(body, "app.home.example.test") || !strings.Contains(body, "10.0.0.80") {
		t.Errorf("GET /v1/records after restart = %d %s; want 200 listing app.home.example.test 10.0.0.80", status, body)
	}
	if err := second.stop(t); err != nil {
		t.Errorf("second serve: err = %v, want nil", err)
	}
}

func TestServeWebOff(t *testing.T) {
	r := startServe(t, serveEnv(t.TempDir(), "DNSMASQ_MANAGER_WEB_ADDR=off"))
	r.waitListening(t, "api", "webhook")
	if err := r.stop(t); err != nil {
		t.Fatalf("serve: err = %v, want nil", err)
	}

	found := listeners(r.stdout.String())
	if _, ok := found["web"]; ok {
		t.Errorf("web listener bound at %s, want none with WEB_ADDR=off", found["web"])
	}
	if len(found) != 2 {
		t.Errorf("listeners = %v, want only api and webhook", found)
	}
}
