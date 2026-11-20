package history_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/gsdevme/dnsmasq-manager/internal/history"
)

func keepAll(*http.Request, int) bool { return true }

func serve(t *testing.T, h http.Handler, method, target, remote string) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = remote
	h.ServeHTTP(httptest.NewRecorder(), req)
}

func TestLogKeepsNewestFirst(t *testing.T) {
	l := history.New(history.Size)
	for i := range 3 {
		l.Add(history.Entry{Summary: fmt.Sprint(i)})
	}

	got := l.Entries()
	want := []string{"2", "1", "0"}
	if len(got) != len(want) {
		t.Fatalf("len(Entries()) = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Summary != w {
			t.Errorf("Entries()[%d].Summary = %q, want %q", i, got[i].Summary, w)
		}
	}
}

func TestLogDropsOldest(t *testing.T) {
	l := history.New(history.Size)
	for i := range history.Size + 1 {
		l.Add(history.Entry{Summary: fmt.Sprint(i)})
	}

	got := l.Entries()
	if len(got) != history.Size {
		t.Fatalf("len(Entries()) = %d, want %d", len(got), history.Size)
	}
	if got[0].Summary != fmt.Sprint(history.Size) {
		t.Errorf("newest = %q, want %q", got[0].Summary, fmt.Sprint(history.Size))
	}
	if got[len(got)-1].Summary != "1" {
		t.Errorf("oldest = %q, want %q", got[len(got)-1].Summary, "1")
	}
	for _, e := range got {
		if e.Summary == "0" {
			t.Errorf("first entry still present")
		}
	}
}

func TestEntriesIsACopy(t *testing.T) {
	l := history.New(history.Size)
	l.Add(history.Entry{Summary: "kept"})

	got := l.Entries()
	got[0].Summary = "changed"

	if again := l.Entries()[0].Summary; again != "kept" {
		t.Errorf("Entries()[0].Summary = %q after mutating a copy, want %q", again, "kept")
	}
}

func TestMiddlewareRecords(t *testing.T) {
	l := history.New(history.Size)
	h := l.Middleware("rest", keepAll, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		history.SetSummary(r, "set app.home.example.test A")
		w.WriteHeader(http.StatusUnprocessableEntity)
	}))

	serve(t, h, http.MethodPut, "/v1/records/app.home.example.test", "10.0.0.5:4242")

	got := l.Entries()
	if len(got) != 1 {
		t.Fatalf("len(Entries()) = %d, want 1", len(got))
	}
	e := got[0]
	if e.Listener != "rest" {
		t.Errorf("Listener = %q, want %q", e.Listener, "rest")
	}
	if e.Method != http.MethodPut {
		t.Errorf("Method = %q, want %q", e.Method, http.MethodPut)
	}
	if e.Path != "/v1/records/app.home.example.test" {
		t.Errorf("Path = %q, want %q", e.Path, "/v1/records/app.home.example.test")
	}
	if e.Status != http.StatusUnprocessableEntity {
		t.Errorf("Status = %d, want %d", e.Status, http.StatusUnprocessableEntity)
	}
	if e.Remote != "10.0.0.5" {
		t.Errorf("Remote = %q, want %q", e.Remote, "10.0.0.5")
	}
	if e.Summary != "set app.home.example.test A" {
		t.Errorf("Summary = %q, want %q", e.Summary, "set app.home.example.test A")
	}
	if e.Time.IsZero() {
		t.Errorf("Time is zero")
	}
	if e.Duration < 0 {
		t.Errorf("Duration = %v, want >= 0", e.Duration)
	}
}

func TestMiddlewareKeepFalse(t *testing.T) {
	l := history.New(history.Size)
	var gotStatus int
	keep := func(_ *http.Request, status int) bool {
		gotStatus = status
		return false
	}
	h := l.Middleware("webhook", keep, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	serve(t, h, http.MethodGet, "/", "10.0.0.5:4242")

	if n := len(l.Entries()); n != 0 {
		t.Errorf("len(Entries()) = %d, want 0", n)
	}
	if gotStatus != http.StatusNoContent {
		t.Errorf("keep saw status %d, want %d", gotStatus, http.StatusNoContent)
	}
}

func TestMiddlewarePathDropsOtherQuery(t *testing.T) {
	tests := []struct {
		name   string
		target string
		want   string
	}{
		{name: "token dropped, force and type kept", target: "/v1/records/x?token=s&force=true&type=A", want: "/v1/records/x?force=true&type=A"},
		{name: "type before force reordered", target: "/v1/records/x?type=A&force=true", want: "/v1/records/x?force=true&type=A"},
		{name: "only type", target: "/v1/records/x?type=AAAA", want: "/v1/records/x?type=AAAA"},
		{name: "only other params", target: "/v1/records/x?token=s", want: "/v1/records/x"},
		{name: "no query", target: "/v1/records", want: "/v1/records"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := history.New(history.Size)
			h := l.Middleware("rest", keepAll, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

			serve(t, h, http.MethodGet, tt.target, "10.0.0.5:4242")

			if got := l.Entries()[0].Path; got != tt.want {
				t.Errorf("Path = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMiddlewareDefaultStatus200(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{name: "writes nothing", handler: func(http.ResponseWriter, *http.Request) {}},
		{name: "writes body only", handler: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := history.New(history.Size)
			h := l.Middleware("rest", keepAll, tt.handler)

			serve(t, h, http.MethodGet, "/v1/records", "10.0.0.5:4242")

			if got := l.Entries()[0].Status; got != http.StatusOK {
				t.Errorf("Status = %d, want %d", got, http.StatusOK)
			}
		})
	}
}

func TestSetSummaryWithoutMiddleware(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	history.SetSummary(req, "ignored")
	if req.Context().Value(struct{}{}) != nil {
		t.Errorf("SetSummary changed the request context")
	}
}

func TestRemoteIP(t *testing.T) {
	tests := []struct {
		remote string
		want   string
	}{
		{remote: "10.0.0.5:4242", want: "10.0.0.5"},
		{remote: "[fd00:10::5]:4242", want: "fd00:10::5"},
		{remote: "10.0.0.5", want: "10.0.0.5"},
		{remote: "@", want: "@"},
	}

	for _, tt := range tests {
		t.Run(tt.remote, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remote
			if got := history.RemoteIP(req); got != tt.want {
				t.Errorf("RemoteIP(%q) = %q, want %q", tt.remote, got, tt.want)
			}
		})
	}
}

func TestLogConcurrent(t *testing.T) {
	l := history.New(history.Size)
	h := l.Middleware("rest", keepAll, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		history.SetSummary(r, "concurrent")
	}))

	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			serve(t, h, http.MethodGet, "/v1/records", "10.0.0.5:4242")
			_ = l.Entries()
		})
	}
	wg.Wait()

	if n := len(l.Entries()); n != history.Size {
		t.Errorf("len(Entries()) = %d, want %d", n, history.Size)
	}
}

func TestAddBoundsPathAndSummary(t *testing.T) {
	l := history.New(history.Size)
	long := strings.Repeat("é", 5000)
	l.Add(history.Entry{Path: "/" + long, Summary: long})

	e := l.Entries()[0]
	for field, got := range map[string]string{"Path": e.Path, "Summary": e.Summary} {
		if !utf8.ValidString(got) {
			t.Errorf("%s is not valid UTF-8", field)
		}
		body, ok := strings.CutSuffix(got, "…")
		if !ok {
			t.Errorf("%s = %.20q…, want it to end with an ellipsis", field, got)
		}
		if len(body) > 256 || len(body) < 250 {
			t.Errorf("len(%s) without the ellipsis = %d, want at most 256 and close to it", field, len(body))
		}
	}

	l.Add(history.Entry{Path: "/v1/records/nas.home.example.test", Summary: "set nas.home.example.test A"})
	if e := l.Entries()[0]; e.Path != "/v1/records/nas.home.example.test" || e.Summary != "set nas.home.example.test A" {
		t.Errorf("short entry changed to %q %q", e.Path, e.Summary)
	}
}
