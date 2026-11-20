package web_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/gsdevme/dnsmasq-manager/internal/health"
	"github.com/gsdevme/dnsmasq-manager/internal/history"
	"github.com/gsdevme/dnsmasq-manager/internal/records"
	"github.com/gsdevme/dnsmasq-manager/internal/web"
)

// fakeChecker returns a fixed health.Result.
type fakeChecker struct {
	result health.Result
}

func (f fakeChecker) Check(context.Context) health.Result {
	return f.result
}

// sampleState holds one record from each source and two TXT ownership records.
func sampleState() records.State {
	return records.State{
		Records: []records.Record{
			{Name: "app.home.example.test", Type: records.TypeA, Values: []netip.Addr{netip.MustParseAddr("10.0.0.80")}, Source: records.SourceExternalDNS},
			{Name: "nas.home.example.test", Type: records.TypeA, Values: []netip.Addr{netip.MustParseAddr("10.0.0.20"), netip.MustParseAddr("10.0.0.21")}, Source: records.SourceAPI},
		},
		TXTs: []records.TXT{
			{Name: "a-app.home.example.test", Targets: []string{"heritage=external-dns"}, OwnedRecord: "app.home.example.test"},
			{Name: "app.home.example.test", Targets: []string{"heritage=external-dns"}, OwnedRecord: "app.home.example.test"},
		},
	}
}

// serve sends one request to a handler built from state, checker and hist,
// and returns the response and its body.
func serve(t *testing.T, state records.State, checker web.HealthChecker, hist *history.Log, method, target string) (*http.Response, string) {
	t.Helper()
	store := records.NewStore(state, records.Policy{}, nil)
	h := web.NewHandler(store, checker, hist)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	res := rec.Result()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return res, string(body)
}

// getPage fetches GET / and fails the test unless it answers 200.
func getPage(t *testing.T, state records.State, checker web.HealthChecker, hist *history.Log) string {
	t.Helper()
	res, body := serve(t, state, checker, hist, http.MethodGet, "/")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	return body
}

func assertContains(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestPageShowsRecords(t *testing.T) {
	body := getPage(t, sampleState(), fakeChecker{}, history.New(history.Size))
	assertContains(t, body,
		"app.home.example.test",
		"nas.home.example.test",
		"10.0.0.80",
		"10.0.0.20",
		"10.0.0.21",
		">external-dns<",
		">api<",
		"2 TXT ownership records",
	)
}

func TestPageShowsHealth(t *testing.T) {
	tests := []struct {
		name   string
		result health.Result
		wants  []string
	}{
		{name: "all ok", result: health.Result{}, wants: []string{"hosts_dir", "dnsmasq", ">ok<"}},
		{
			name:   "dnsmasq down",
			result: health.Result{Dnsmasq: errors.New("read udp 10.0.0.1:53: i/o timeout")},
			wants:  []string{"hosts_dir", ">ok<", "dnsmasq", "read udp 10.0.0.1:53: i/o timeout", "Unhealthy"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := getPage(t, sampleState(), fakeChecker{result: tt.result}, history.New(history.Size))
			assertContains(t, body, tt.wants...)
		})
	}
}

func TestPageShowsHistoryNewestFirst(t *testing.T) {
	hist := history.New(history.Size)
	start := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	for i, summary := range []string{"set first.home.example.test A", "set second.home.example.test A", "create 2, update 0, delete 1"} {
		hist.Add(history.Entry{
			Time:     start.Add(time.Duration(i) * time.Minute),
			Listener: "rest",
			Method:   http.MethodPut,
			Path:     "/records/x?force=true",
			Status:   http.StatusOK,
			Remote:   "10.0.0.5",
			Duration: 3 * time.Millisecond,
			Summary:  summary,
		})
	}
	body := getPage(t, sampleState(), fakeChecker{}, hist)
	assertContains(t, body, "rest", "PUT", "/records/x?force=true", "10.0.0.5", "200")

	newest := strings.Index(body, "create 2, update 0, delete 1")
	middle := strings.Index(body, "set second.home.example.test A")
	oldest := strings.Index(body, "set first.home.example.test A")
	if newest < 0 || middle < 0 || oldest < 0 {
		t.Fatalf("summaries missing: newest=%d middle=%d oldest=%d", newest, middle, oldest)
	}
	if newest >= middle || middle >= oldest {
		t.Errorf("order = %d, %d, %d; want newest first", newest, middle, oldest)
	}
}

func TestPageEscapes(t *testing.T) {
	hist := history.New(history.Size)
	hist.Add(history.Entry{Time: time.Now(), Listener: "webhook", Method: http.MethodPost, Path: "/records", Status: http.StatusBadRequest, Summary: "<script>alert(1)</script>"})
	body := getPage(t, records.State{}, fakeChecker{}, hist)
	if strings.Contains(body, "<script") {
		t.Error("body contains an unescaped <script")
	}
	assertContains(t, body, "&lt;script&gt;alert(1)&lt;/script&gt;")
}

func TestPageEmptyState(t *testing.T) {
	body := getPage(t, records.State{}, fakeChecker{}, history.New(history.Size))
	assertContains(t, body, "No records yet", "No requests yet", "0 TXT ownership records")
}

func TestPageTXTCountPlural(t *testing.T) {
	txt := records.TXT{Name: "a-app.home.example.test", Targets: []string{"heritage=external-dns"}, OwnedRecord: "app.home.example.test"}
	tests := []struct {
		count int
		want  string
	}{
		{count: 0, want: "0 TXT ownership records kept"},
		{count: 1, want: "1 TXT ownership record kept"},
		{count: 2, want: "2 TXT ownership records kept"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			state := records.State{}
			for range tt.count {
				state.TXTs = append(state.TXTs, txt)
			}
			body := getPage(t, state, fakeChecker{}, history.New(history.Size))
			assertContains(t, body, tt.want)
		})
	}
}

// TestPageCellsCarryLabels checks that every table cell names its column in
// data-label, which the stylesheet shows when rows stack at phone width.
func TestPageCellsCarryLabels(t *testing.T) {
	hist := history.New(history.Size)
	hist.Add(history.Entry{Time: time.Now(), Listener: "api", Method: http.MethodGet, Path: "/v1/records", Status: http.StatusOK, Remote: "10.0.0.5", Duration: time.Millisecond, Summary: "list"})
	body := getPage(t, sampleState(), fakeChecker{}, hist)

	cells := strings.Count(body, "<td")
	labelled := strings.Count(body, `<td data-label="`)
	if cells == 0 || labelled != cells {
		t.Errorf("%d of %d cells carry a data-label", labelled, cells)
	}
	for _, label := range []string{"Name", "Type", "Addresses", "Source", "Time", "Listener", "Request", "Status", "Remote", "Took", "Summary"} {
		assertContains(t, body, `data-label="`+label+`"`)
	}
}

func TestHeaders(t *testing.T) {
	res, body := serve(t, sampleState(), fakeChecker{}, history.New(history.Size), http.MethodGet, "/")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	headers := map[string]string{
		"Content-Type":            "text/html; charset=utf-8",
		"Content-Security-Policy": "default-src 'none'; style-src 'self'",
	}
	for name, want := range headers {
		if got := res.Header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	assertContains(t, body, `<meta http-equiv="refresh" content="30">`, `<link rel="stylesheet" href="/style.css">`)
	for _, banned := range []string{"<script", "style="} {
		if strings.Contains(body, banned) {
			t.Errorf("body contains %q", banned)
		}
	}
}

func TestStylesheet(t *testing.T) {
	res, body := serve(t, records.State{}, fakeChecker{}, history.New(history.Size), http.MethodGet, "/style.css")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got, want := res.Header.Get("Content-Type"), "text/css; charset=utf-8"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	assertContains(t, body, "prefers-color-scheme")
}

func TestOtherPaths404(t *testing.T) {
	for _, path := range []string{"/index.html", "/records", "/style.css/x", "/healthz", "/page.html.tmpl"} {
		t.Run(path, func(t *testing.T) {
			res, _ := serve(t, records.State{}, fakeChecker{}, history.New(history.Size), http.MethodGet, path)
			if res.StatusCode != http.StatusNotFound {
				t.Errorf("status = %d, want 404", res.StatusCode)
			}
		})
	}
}

func TestPostIs405(t *testing.T) {
	for _, path := range []string{"/", "/style.css"} {
		t.Run(path, func(t *testing.T) {
			res, _ := serve(t, records.State{}, fakeChecker{}, history.New(history.Size), http.MethodPost, path)
			if res.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want 405", res.StatusCode)
			}
		})
	}
}
