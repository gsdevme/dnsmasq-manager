//go:build integration

// Package integration runs a real dnsmasq against the mock config in
// testdata/dnsmasq and queries it with dig, so tests observe how dnsmasq
// itself behaves rather than how we assume it behaves.
//
// The tests need Linux with dnsmasq and dig on the path. Run them with
// `make integration` on such a host, or `make integration-docker` elsewhere,
// which pins the dnsmasq version the production host runs.
package integration

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	mockConfig   = "../../testdata/dnsmasq/dnsmasq.conf"
	managedHosts = "managed.hosts"
	readyName    = "nas.home.example.test"
	pollInterval = 100 * time.Millisecond
	pollTimeout  = 5 * time.Second
)

// server is a dnsmasq process started by startDnsmasq. It listens on
// 127.0.0.1 at port and reads hosts files from hostsDir. The process is
// killed when the test that started it finishes.
type server struct {
	port     int
	hostsDir string
	cmd      *exec.Cmd
}

// startDnsmasq starts dnsmasq in the foreground with the mock config, an
// empty per-test hosts directory and a free port on 127.0.0.1, and returns
// once it answers queries. It fails the test when dnsmasq or dig is not on
// the path or dnsmasq does not answer within five seconds. The process is
// killed in t.Cleanup, which also logs its output if the test failed.
func startDnsmasq(t *testing.T) *server {
	t.Helper()
	for _, bin := range []string{"dnsmasq", "dig"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("%s is not on the path: %v", bin, err)
		}
	}
	t.Log(dnsmasqVersion(t))

	conf, err := filepath.Abs(mockConfig)
	if err != nil {
		t.Fatalf("resolve mock config: %v", err)
	}
	s := &server{port: freePort(t), hostsDir: t.TempDir()}
	args := []string{
		"--keep-in-foreground",
		"--conf-file=" + conf,
		"--hostsdir=" + s.hostsDir,
		"--port=" + strconv.Itoa(s.port),
		"--listen-address=127.0.0.1",
		"--bind-interfaces",
		"--no-hosts",
		"--pid-file=",
		"--log-facility=-",
	}
	if os.Geteuid() == 0 {
		args = append(args, "--user=root")
	}

	var output bytes.Buffer
	s.cmd = exec.Command("dnsmasq", args...)
	s.cmd.Stdout = &output
	s.cmd.Stderr = &output
	if err := s.cmd.Start(); err != nil {
		t.Fatalf("start dnsmasq: %v", err)
	}
	t.Cleanup(func() {
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
		if t.Failed() {
			t.Logf("dnsmasq output:\n%s", output.String())
		}
	})

	s.eventually(t, readyName, "10.0.0.40")
	return s
}

// freePort returns a UDP port on 127.0.0.1 that was free a moment ago, found
// by binding port 0 and closing the socket.
func freePort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find a free port: %v", err)
	}
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if err := conn.Close(); err != nil {
		t.Fatalf("release free port: %v", err)
	}
	if !ok {
		t.Fatalf("unexpected local address type %T", conn.LocalAddr())
	}
	return addr.Port
}

// dnsmasqVersion returns the first line of `dnsmasq --version`, for example
// "Dnsmasq version 2.85  Copyright (c) 2000-2021 Simon Kelley".
func dnsmasqVersion(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("dnsmasq", "--version").Output()
	if err != nil {
		t.Fatalf("dnsmasq --version: %v", err)
	}
	line, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(line)
}

// query asks the server for name's A records with a single one-second dig
// attempt and returns the answers sorted. A query that gets no reply is an
// error, as is any other dig failure.
func (s *server) query(name string) ([]string, error) {
	out, err := exec.Command("dig", "+short", "+time=1", "+tries=1",
		"@127.0.0.1", "-p", strconv.Itoa(s.port), name, "A").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("dig %s: %w: %s", name, err, bytes.TrimSpace(out))
	}
	var answers []string
	for line := range strings.Lines(string(out)) {
		if line = strings.TrimSpace(line); line != "" {
			answers = append(answers, line)
		}
	}
	slices.Sort(answers)
	return answers, nil
}

// lookup returns the sorted A records the server answers for name. It fails
// the test when dig fails or gets no reply.
func (s *server) lookup(t *testing.T, name string) []string {
	t.Helper()
	answers, err := s.query(name)
	if err != nil {
		t.Fatal(err)
	}
	return answers
}

// writeHosts replaces the hosts directory's single managed file with content.
// It writes a dot-prefixed temporary file in the same directory, which
// dnsmasq ignores, and renames it over managed.hosts so dnsmasq never reads
// a partial file.
func (s *server) writeHosts(t *testing.T, content string) {
	t.Helper()
	tmp, err := os.CreateTemp(s.hostsDir, "."+managedHosts+".*")
	if err != nil {
		t.Fatalf("create temporary hosts file: %v", err)
	}
	_, writeErr := tmp.WriteString(content)
	if err := errors.Join(writeErr, tmp.Close()); err != nil {
		t.Fatalf("write temporary hosts file: %v", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(s.hostsDir, managedHosts)); err != nil {
		t.Fatalf("replace %s: %v", managedHosts, err)
	}
}

// reload sends dnsmasq SIGHUP, which clears its cache and rereads the hosts
// directory.
func (s *server) reload(t *testing.T) {
	t.Helper()
	if err := s.cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatalf("send SIGHUP to dnsmasq: %v", err)
	}
}

// eventually polls name every 100ms until the server answers exactly want, in
// any order, and fails the test with the last answer or error if that has
// not happened within five seconds.
func (s *server) eventually(t *testing.T, name string, want ...string) {
	t.Helper()
	want = slices.Sorted(slices.Values(want))
	deadline := time.Now().Add(pollTimeout)
	for {
		got, err := s.query(name)
		if err == nil && slices.Equal(got, want) {
			return
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("%s: want %v within %v, last error: %v", name, want, pollTimeout, err)
			}
			t.Fatalf("%s: want %v within %v, last answer %v", name, want, pollTimeout, got)
		}
		time.Sleep(pollInterval)
	}
}
