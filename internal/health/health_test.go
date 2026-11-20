package health_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gsdevme/dnsmasq-manager/internal/health"
)

// question is the exact question section the probe must send: version.bind,
// type TXT (16), class CH (3).
var question = []byte("\x07version\x04bind\x00\x00\x10\x00\x03")

// fakeDNS listens on a loopback UDP port and answers each query with what
// reply returns; a nil reply sends nothing. Received queries arrive on the
// returned channel.
func fakeDNS(t *testing.T, reply func(query []byte) []byte) (string, <-chan []byte) {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	queries := make(chan []byte, 8)
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			q := bytes.Clone(buf[:n])
			select {
			case queries <- q:
			default:
			}
			if r := reply(q); r != nil {
				_, _ = conn.WriteTo(r, addr)
			}
		}
	}()
	return conn.LocalAddr().String(), queries
}

// answer builds a reply header for query with the QR bit set and rcode, followed
// by the query's question section.
func answer(query []byte, rcode byte) []byte {
	if len(query) < 12 {
		return nil
	}
	r := bytes.Clone(query)
	r[2] |= 0x80
	r[3] = (r[3] &^ 0x0f) | rcode
	return r
}

// probe runs only the dnsmasq check against addr: the hosts directory is a
// fresh temporary directory, so its check always passes.
func probe(t *testing.T, addr string, timeout time.Duration) error {
	t.Helper()
	c := health.Checker{HostsDir: t.TempDir(), ProbeAddr: addr, Timeout: timeout}
	r := c.Check(context.Background())
	if r.HostsDir != nil {
		t.Fatalf("HostsDir = %v, want nil", r.HostsDir)
	}
	return r.Dnsmasq
}

func TestProbeOK(t *testing.T) {
	addr, queries := fakeDNS(t, func(q []byte) []byte { return answer(q, 0) })
	if err := probe(t, addr, 0); err != nil {
		t.Fatalf("Dnsmasq = %v, want nil", err)
	}

	q := <-queries
	if len(q) != 12+len(question) {
		t.Fatalf("query length = %d, want %d", len(q), 12+len(question))
	}
	flags := binary.BigEndian.Uint16(q[2:4])
	if flags != 0 {
		t.Errorf("flags = %#04x, want 0 (standard query, RD clear)", flags)
	}
	counts := [4]uint16{
		binary.BigEndian.Uint16(q[4:6]),
		binary.BigEndian.Uint16(q[6:8]),
		binary.BigEndian.Uint16(q[8:10]),
		binary.BigEndian.Uint16(q[10:12]),
	}
	if counts != [4]uint16{1, 0, 0, 0} {
		t.Errorf("QD/AN/NS/AR counts = %v, want [1 0 0 0]", counts)
	}
	if !bytes.Equal(q[12:], question) {
		t.Errorf("question = %q, want %q", q[12:], question)
	}
}

func TestProbeUsesRandomIDs(t *testing.T) {
	addr, queries := fakeDNS(t, func(q []byte) []byte { return answer(q, 0) })
	seen := map[uint16]bool{}
	for range 8 {
		if err := probe(t, addr, 0); err != nil {
			t.Fatal(err)
		}
		seen[binary.BigEndian.Uint16((<-queries)[0:2])] = true
	}
	if len(seen) < 2 {
		t.Errorf("8 probes used %d distinct IDs, want more than one", len(seen))
	}
}

func TestProbeAcceptsAnyRcode(t *testing.T) {
	tests := []struct {
		name  string
		rcode byte
	}{
		{name: "servfail", rcode: 2},
		{name: "nxdomain", rcode: 3},
		{name: "refused", rcode: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, _ := fakeDNS(t, func(q []byte) []byte { return answer(q, tt.rcode) })
			if err := probe(t, addr, 0); err != nil {
				t.Fatalf("Dnsmasq = %v, want nil", err)
			}
		})
	}
}

func TestProbeWrongID(t *testing.T) {
	addr, _ := fakeDNS(t, func(q []byte) []byte {
		r := answer(q, 0)
		r[0] ^= 0xff
		return r
	})
	if err := probe(t, addr, 200*time.Millisecond); err == nil {
		t.Fatal("Dnsmasq = nil, want an error for a mismatched ID")
	}
}

func TestProbeNotAResponse(t *testing.T) {
	addr, _ := fakeDNS(t, bytes.Clone)
	if err := probe(t, addr, 200*time.Millisecond); err == nil {
		t.Fatal("Dnsmasq = nil, want an error for a reply without the QR bit")
	}
}

func TestProbeShortReply(t *testing.T) {
	tests := []struct {
		name string
		n    int
	}{
		{name: "two bytes", n: 2},
		{name: "eleven bytes", n: 11},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, _ := fakeDNS(t, func(q []byte) []byte { return answer(q, 0)[:tt.n] })
			if err := probe(t, addr, 200*time.Millisecond); err == nil {
				t.Fatal("Dnsmasq = nil, want an error for a short reply")
			}
		})
	}
}

func TestProbeTimeout(t *testing.T) {
	addr, _ := fakeDNS(t, func([]byte) []byte { return nil })
	start := time.Now()
	err := probe(t, addr, 100*time.Millisecond)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("probe took %v, want under 500ms", elapsed)
	}
	if err == nil {
		t.Fatal("Dnsmasq = nil, want a timeout error")
	}
}

func TestProbeHonoursContext(t *testing.T) {
	addr, _ := fakeDNS(t, func([]byte) []byte { return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	r := health.Checker{HostsDir: t.TempDir(), ProbeAddr: addr}.Check(ctx)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("probe took %v, want under 500ms", elapsed)
	}
	if r.Dnsmasq == nil {
		t.Fatal("Dnsmasq = nil, want an error once the context ends")
	}
}

func TestProbeNothingListening(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := conn.LocalAddr().String()
	_ = conn.Close()
	if err := probe(t, addr, 200*time.Millisecond); err == nil {
		t.Fatal("Dnsmasq = nil, want an error with nothing listening")
	}
}

func TestProbeBadAddress(t *testing.T) {
	if err := probe(t, "", 200*time.Millisecond); err == nil {
		t.Fatal("Dnsmasq = nil, want an error for an empty address")
	}
}

// hostsDir runs only the hosts directory check: the probe goes to a fake
// server that always answers.
func hostsDir(t *testing.T, dir string) error {
	t.Helper()
	addr, _ := fakeDNS(t, func(q []byte) []byte { return answer(q, 0) })
	r := health.Checker{HostsDir: dir, ProbeAddr: addr}.Check(context.Background())
	if r.Dnsmasq != nil {
		t.Fatalf("Dnsmasq = %v, want nil", r.Dnsmasq)
	}
	return r.HostsDir
}

func TestHostsDirOK(t *testing.T) {
	dir := t.TempDir()
	if err := hostsDir(t, dir); err != nil {
		t.Fatalf("HostsDir = %v, want nil", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("directory holds %d entries after the check, want none", len(entries))
	}
}

func TestHostsDirMissing(t *testing.T) {
	if err := hostsDir(t, filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("HostsDir = nil, want an error for a missing directory")
	}
}

func TestHostsDirNotADirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := hostsDir(t, file); err == nil {
		t.Fatal("HostsDir = nil, want an error for a regular file")
	}
}

func TestHostsDirReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to a read-only directory")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if err := hostsDir(t, dir); err == nil {
		t.Fatal("HostsDir = nil, want an error for a read-only directory")
	}
}

func TestResultOK(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name   string
		result health.Result
		want   bool
	}{
		{name: "both ok", result: health.Result{}, want: true},
		{name: "hosts dir failed", result: health.Result{HostsDir: boom}, want: false},
		{name: "dnsmasq failed", result: health.Result{Dnsmasq: boom}, want: false},
		{name: "both failed", result: health.Result{HostsDir: boom, Dnsmasq: boom}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.result.OK(); got != tt.want {
				t.Errorf("OK() = %v, want %v", got, tt.want)
			}
		})
	}
}
