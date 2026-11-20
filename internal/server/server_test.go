package server_test

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gsdevme/dnsmasq-manager/internal/server"
)

var loopback = netip.MustParseAddrPort("127.0.0.1:0")

// textHandler answers every request with 200 and body.
func textHandler(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	})
}

// serve runs s in the background and returns a function that cancels it and
// a channel that receives what Serve returned. The test's cleanup cancels and
// waits for Serve, so no goroutine outlives the test.
func serve(t *testing.T, s *server.Server) (cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ch <- s.Serve(ctx, slog.New(slog.DiscardHandler))
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(10 * time.Second):
			t.Error("Serve did not return after cancel")
		}
	})
	return cancel, ch
}

// get fetches url with client and returns the status and body.
func get(t *testing.T, client *http.Client, url string) (status int, body string) {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body of %s: %v", url, err)
	}
	return resp.StatusCode, string(b)
}

// selfSigned writes a self-signed certificate for 127.0.0.1 and its key to a
// temporary directory and returns their paths and the parsed certificate.
func selfSigned(t *testing.T) (certPath, keyPath string, cert *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "dnsmasq-manager test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	writePEM(t, certPath, "CERTIFICATE", der)
	writePEM(t, keyPath, "PRIVATE KEY", keyDER)
	return certPath, keyPath, cert
}

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	data := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// freePort returns a loopback address whose port was free a moment ago.
func freePort(t *testing.T) netip.AddrPort {
	t.Helper()
	ln, err := net.Listen("tcp", loopback.String())
	if err != nil {
		t.Fatal(err)
	}
	addr := netip.MustParseAddrPort(ln.Addr().String())
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// assertClosed fails the test if anything still accepts connections on addr.
func assertClosed(t *testing.T, addr net.Addr) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr.String(), time.Second)
	if err == nil {
		_ = conn.Close()
		t.Errorf("%s still accepts connections", addr)
	}
}

func TestServeAnswersOnEveryListener(t *testing.T) {
	s, err := server.Bind([]server.Listener{
		{Name: "api", Addr: loopback, Handler: textHandler("api")},
		{Name: "webhook", Addr: loopback, Handler: textHandler("webhook")},
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	serve(t, s)

	for _, name := range []string{"api", "webhook"} {
		t.Run(name, func(t *testing.T) {
			addr := s.Addr(name)
			if addr == nil {
				t.Fatalf("Addr(%q) = nil", name)
			}
			status, body := get(t, http.DefaultClient, "http://"+addr.String()+"/")
			if status != http.StatusOK || body != name {
				t.Errorf("GET = %d %q, want 200 %q", status, body, name)
			}
		})
	}
}

func TestAddrUnknownName(t *testing.T) {
	s, err := server.Bind([]server.Listener{{Name: "api", Addr: loopback, Handler: textHandler("api")}})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	serve(t, s)
	if addr := s.Addr("bogus"); addr != nil {
		t.Errorf("Addr(bogus) = %v, want nil", addr)
	}
}

func TestServeTLS(t *testing.T) {
	certPath, keyPath, cert := selfSigned(t)
	s, err := server.Bind([]server.Listener{
		{Name: "webhook", Addr: loopback, Handler: textHandler("secure"), TLSCert: certPath, TLSKey: keyPath},
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	serve(t, s)
	addr := s.Addr("webhook").String()

	pool := x509.NewCertPool()
	pool.AddCert(cert)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	status, body := get(t, client, "https://"+addr+"/")
	if status != http.StatusOK || body != "secure" {
		t.Errorf("HTTPS GET = %d %q, want 200 %q", status, body, "secure")
	}

	resp, err := http.Get("http://" + addr + "/")
	if err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("plain HTTP GET to a TLS listener = 200, want a failure")
		}
	}

	tls11 := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs: pool, MinVersion: tls.VersionTLS11, MaxVersion: tls.VersionTLS11,
	}}}
	if resp, err := tls11.Get("https://" + addr + "/"); err == nil {
		_ = resp.Body.Close()
		t.Errorf("TLS 1.1 GET succeeded, want a handshake failure")
	}
}

func TestBindFailsWhenPortTaken(t *testing.T) {
	taken, err := net.Listen("tcp", loopback.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = taken.Close() })
	free := freePort(t)

	_, err = server.Bind([]server.Listener{
		{Name: "api", Addr: free, Handler: textHandler("api")},
		{Name: "webhook", Addr: netip.MustParseAddrPort(taken.Addr().String()), Handler: textHandler("webhook")},
	})
	if err == nil {
		t.Fatal("Bind succeeded on a taken port")
	}
	if !strings.Contains(err.Error(), "webhook") {
		t.Errorf("error %q does not name the listener %q", err, "webhook")
	}

	again, err := net.Listen("tcp", free.String())
	if err != nil {
		t.Fatalf("first listener's port %s not released: %v", free, err)
	}
	_ = again.Close()
}

func TestBindRejectsDuplicateName(t *testing.T) {
	_, err := server.Bind([]server.Listener{
		{Name: "api", Addr: loopback, Handler: textHandler("a")},
		{Name: "api", Addr: loopback, Handler: textHandler("b")},
	})
	if err == nil || !strings.Contains(err.Error(), "api") {
		t.Fatalf("Bind error = %v, want one naming %q", err, "api")
	}
}

func TestBindBadCertificate(t *testing.T) {
	certPath, keyPath, _ := selfSigned(t)
	garbage := filepath.Join(t.TempDir(), "garbage.pem")
	if err := os.WriteFile(garbage, []byte("not a certificate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing.pem")

	tests := []struct {
		name      string
		cert, key string
	}{
		{name: "garbage certificate", cert: garbage, key: keyPath},
		{name: "garbage key", cert: certPath, key: garbage},
		{name: "missing certificate", cert: missing, key: keyPath},
		{name: "missing key", cert: certPath, key: missing},
		{name: "certificate without key", cert: certPath},
		{name: "key without certificate", key: keyPath},
		{name: "key and certificate swapped", cert: keyPath, key: certPath},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			free := freePort(t)
			_, err := server.Bind([]server.Listener{
				{Name: "plain", Addr: free, Handler: textHandler("plain")},
				{Name: "webhook", Addr: loopback, Handler: textHandler("w"), TLSCert: tt.cert, TLSKey: tt.key},
			})
			if err == nil {
				t.Fatal("Bind succeeded with a bad certificate")
			}
			if !strings.Contains(err.Error(), "webhook") {
				t.Errorf("error %q does not name the listener %q", err, "webhook")
			}
			again, err := net.Listen("tcp", free.String())
			if err != nil {
				t.Fatalf("plain listener's port %s not released: %v", free, err)
			}
			_ = again.Close()
		})
	}
}

func TestServeStopsOnCancel(t *testing.T) {
	s, err := server.Bind([]server.Listener{
		{Name: "api", Addr: loopback, Handler: textHandler("api")},
		{Name: "web", Addr: loopback, Handler: textHandler("web")},
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	api, web := s.Addr("api"), s.Addr("web")
	cancel, done := serve(t, s)
	get(t, http.DefaultClient, "http://"+api.String()+"/")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not return within a second of cancel")
	}
	assertClosed(t, api)
	assertClosed(t, web)
}

func TestServeFinishesInFlightRequest(t *testing.T) {
	started := make(chan struct{})
	slow := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(200 * time.Millisecond)
		_, _ = io.WriteString(w, "done")
	})
	s, err := server.Bind([]server.Listener{{Name: "api", Addr: loopback, Handler: slow}})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	cancel, done := serve(t, s)

	type result struct {
		status int
		body   string
		err    error
	}
	resCh := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + s.Addr("api").String() + "/")
		if err != nil {
			resCh <- result{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		b, err := io.ReadAll(resp.Body)
		resCh <- result{status: resp.StatusCode, body: string(b), err: err}
	}()

	<-started
	cancel()
	res := <-resCh
	if res.err != nil || res.status != http.StatusOK || res.body != "done" {
		t.Errorf("in-flight request = %d %q %v, want 200 %q", res.status, res.body, res.err, "done")
	}
	if err := <-done; err != nil {
		t.Errorf("Serve = %v, want nil", err)
	}
}

func TestServerClosesStalledConnection(t *testing.T) {
	t.Cleanup(server.SetReadHeaderTimeout(100 * time.Millisecond))
	s, err := server.Bind([]server.Listener{{Name: "api", Addr: loopback, Handler: textHandler("api")}})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	serve(t, s)

	conn, err := net.Dial("tcp", s.Addr("api").String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(conn)
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatal("server did not close a stalled connection within a second")
	}
}

func TestServeReturnsListenerFailure(t *testing.T) {
	s, err := server.Bind([]server.Listener{
		{Name: "api", Addr: loopback, Handler: textHandler("api")},
		{Name: "web", Addr: loopback, Handler: textHandler("web")},
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	web := s.Addr("web")
	breakListener(t, s, "api")
	_, done := serve(t, s)

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "api") {
			t.Errorf("Serve = %v, want an error naming %q", err, "api")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after a listener failed")
	}
	assertClosed(t, web)
}

// breakListener closes the named listener's socket underneath s, so its
// Accept fails as soon as Serve starts.
func breakListener(t *testing.T, s *server.Server, name string) {
	t.Helper()
	if err := server.CloseListener(s, name); err != nil {
		t.Fatal(err)
	}
}

func TestServeRefusesOversizedHeaders(t *testing.T) {
	reached := make(chan struct{}, 1)
	s, err := server.Bind([]server.Listener{{Name: "api", Addr: loopback, Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached <- struct{}{}
	})}})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	serve(t, s)

	conn, err := net.Dial("tcp", s.Addr("api").String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, "GET /"+strings.Repeat("a", 20<<10)+" HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
		t.Logf("write cut short: %v", err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err == nil && !strings.Contains(line, "431") {
		t.Errorf("response = %q, want 431 or a closed connection", line)
	}
	select {
	case <-reached:
		t.Error("handler reached with a 20 KiB request line")
	default:
	}
}
