package webhook_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gsdevme/dnsmasq-manager/internal/history"
	"github.com/gsdevme/dnsmasq-manager/internal/records"
	"github.com/gsdevme/dnsmasq-manager/internal/webhook"
)

// allowedRemote is the source the test handlers accept; newFixtureAllowing
// accepts others.
const allowedRemote = "10.0.0.5:40000"

// fakeSaver records every state it is asked to save, and fails when err is set.
type fakeSaver struct {
	mu    sync.Mutex
	saved []records.State
	err   error
}

func (f *fakeSaver) Save(s records.State) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.saved = append(f.saved, s)
	return nil
}

func (f *fakeSaver) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.saved)
}

// fixture is a webhook handler over a store holding initial.
type fixture struct {
	handler http.Handler
	store   *records.Store
	saver   *fakeSaver
	hist    *history.Log
	logs    *bytes.Buffer
}

func newFixture(t *testing.T, initial records.State) *fixture {
	t.Helper()
	return newFixtureAllowing(t, initial, netip.MustParsePrefix("10.0.0.5/32"))
}

// newFixtureAllowing is newFixture with the allowlist set to allowed.
func newFixtureAllowing(t *testing.T, initial records.State, allowed ...netip.Prefix) *fixture {
	t.Helper()
	saver := &fakeSaver{}
	store := records.NewStore(initial, testPolicy(), saver)
	hist := history.New(history.Size)
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	return &fixture{handler: webhook.NewHandler(store, allowed, hist, logger), store: store, saver: saver, hist: hist, logs: logs}
}

// do sends a request from allowedRemote and returns the response.
func (f *fixture) do(t *testing.T, method, path, body string) *http.Response {
	t.Helper()
	return f.doFrom(t, allowedRemote, method, path, body)
}

// doFrom sends a request from remote with the headers external-dns v0.24.0
// sends: Accept always, and Content-Type on a POST.
func (f *fixture) doFrom(t *testing.T, remote, method, path, body string) *http.Response {
	t.Helper()
	contentType := ""
	if method == http.MethodPost {
		contentType = webhook.MediaType
	}
	return f.doWith(t, remote, contentType, method, path, body)
}

// doWith sends a request from remote with Content-Type set to contentType,
// or without one when it is empty.
func (f *fixture) doWith(t *testing.T, remote, contentType, method, path, body string) *http.Response {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = remote
	r.Header.Set("Accept", webhook.MediaType)
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w.Result()
}

// readBody returns the whole response body.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// checkResponse fails the test unless resp has status and the webhook media type.
func checkResponse(t *testing.T, resp *http.Response, status int) string {
	t.Helper()
	body := readBody(t, resp)
	if resp.StatusCode != status {
		t.Errorf("status = %d, want %d (body %q)", resp.StatusCode, status, body)
	}
	if got := resp.Header.Get("Content-Type"); got != webhook.MediaType {
		t.Errorf("Content-Type = %q, want %q", got, webhook.MediaType)
	}
	return body
}

func TestAllowlist(t *testing.T) {
	f := newFixture(t, records.State{})

	resp := f.doFrom(t, "10.0.0.9:40000", http.MethodGet, "/", "")
	checkResponse(t, resp, http.StatusForbidden)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.9:40000"
	r.Header.Set("X-Forwarded-For", "10.0.0.5")
	r.Header.Set("X-Real-IP", "10.0.0.5")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	checkResponse(t, w.Result(), http.StatusForbidden)

	checkResponse(t, f.do(t, http.MethodGet, "/", ""), http.StatusOK)
	checkResponse(t, f.doFrom(t, "[::ffff:10.0.0.5]:40000", http.MethodGet, "/", ""), http.StatusOK)
}

func TestNegotiation(t *testing.T) {
	f := newFixture(t, records.State{})
	body := checkResponse(t, f.do(t, http.MethodGet, "/", ""), http.StatusOK)
	if want := `{"include":["home.example.test"]}`; strings.TrimSpace(body) != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

func TestUnknownPathIs404(t *testing.T) {
	f := newFixture(t, records.State{})
	checkResponse(t, f.do(t, http.MethodGet, "/nope", ""), http.StatusNotFound)
	checkResponse(t, f.do(t, http.MethodGet, "/records/x", ""), http.StatusNotFound)
}

func TestUnknownMethodIs405(t *testing.T) {
	f := newFixture(t, records.State{})
	resp := f.do(t, http.MethodDelete, "/records", "")
	checkResponse(t, resp, http.StatusMethodNotAllowed)
	if resp.Header.Get("Allow") == "" {
		t.Error("Allow header is empty")
	}
}

func TestGetRecordsEmptyIsArray(t *testing.T) {
	f := newFixture(t, records.State{})
	body := checkResponse(t, f.do(t, http.MethodGet, "/records", ""), http.StatusOK)
	if strings.TrimSpace(body) != "[]" {
		t.Errorf("body = %q, want []", body)
	}
}

func TestPostRecordsReturns204(t *testing.T) {
	f := newFixture(t, records.State{})
	sent := []webhook.Endpoint{
		{DNSName: "app.home.example.test", Targets: []string{"10.0.0.80"}, RecordType: "A"},
		{DNSName: "a-app.home.example.test", Targets: []string{ownership}, RecordType: "TXT"},
	}
	create, err := json.Marshal(webhook.Changes{Create: []webhook.Endpoint{
		{DNSName: "app.home.example.test", Targets: []string{"10.0.0.80"}, RecordType: "A", RecordTTL: 300},
		{DNSName: "a-app.home.example.test", Targets: []string{ownership}, RecordType: "TXT", Labels: map[string]string{"ownedRecord": "app.home.example.test"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	checkResponse(t, f.do(t, http.MethodPost, "/records", string(create)), http.StatusNoContent)

	body := checkResponse(t, f.do(t, http.MethodGet, "/records", ""), http.StatusOK)
	var got []webhook.Endpoint
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", body, err)
	}
	want, _ := json.Marshal(sent)
	gotJSON, _ := json.Marshal(got)
	if string(gotJSON) != string(want) {
		t.Errorf("GET /records = %s, want %s", gotJSON, want)
	}
	if f.saver.calls() != 1 {
		t.Errorf("saver called %d times, want 1", f.saver.calls())
	}
}

func TestPostRecordsEmptyChangeSet(t *testing.T) {
	f := newFixture(t, apiState())
	checkResponse(t, f.do(t, http.MethodPost, "/records", "{}"), http.StatusNoContent)
	if f.saver.calls() != 0 {
		t.Errorf("saver called %d times, want 0", f.saver.calls())
	}
}

func TestPostRecordsAllOrNothing(t *testing.T) {
	f := newFixture(t, apiState())
	body := `{"create":[{"dnsName":"new.home.example.test","targets":["10.0.0.70"],"recordType":"A"},` +
		`{"dnsName":"api.home.example.test","targets":["10.0.0.11"],"recordType":"A"}]}`
	got := checkResponse(t, f.do(t, http.MethodPost, "/records", body), http.StatusInternalServerError)
	var e struct{ Error string }
	if err := json.Unmarshal([]byte(got), &e); err != nil || !strings.Contains(e.Error, "api.home.example.test") {
		t.Errorf("body = %q, want an error naming api.home.example.test", got)
	}
	if !f.store.State().Equal(apiState()) {
		t.Errorf("state = %+v, want it unchanged", f.store.State())
	}
	if f.saver.calls() != 0 {
		t.Errorf("saver called %d times, want 0", f.saver.calls())
	}
}

func TestPostRecordsRefusesTXTOfAnAPIName(t *testing.T) {
	for name, body := range map[string]string{
		"set labelled with an api name":                        `{"create":[{"dnsName":"a-api.home.example.test","targets":["t"],"recordType":"TXT","labels":{"ownedRecord":"api.home.example.test"}}]}`,
		"unlabelled delete of a stored TXT owning an api name": `{"delete":[{"dnsName":"a-nas.home.example.test","targets":["t"],"recordType":"TXT"}]}`,
		"set over a stored TXT owning an api name":             `{"create":[{"dnsName":"a-nas.home.example.test","targets":["t"],"recordType":"TXT","labels":{"ownedRecord":"app.home.example.test"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, orphanState())
			got := checkResponse(t, f.do(t, http.MethodPost, "/records", body), http.StatusInternalServerError)
			if !strings.Contains(got, "belongs to the api") {
				t.Errorf("body = %q, want a conflict naming the api", got)
			}
			if !f.store.State().Equal(orphanState()) {
				t.Errorf("state = %+v, want it unchanged", f.store.State())
			}
		})
	}
}

func TestPostRecordsSaveFails(t *testing.T) {
	f := newFixture(t, records.State{})
	f.saver.err = errors.New("disk gone")
	body := `{"create":[{"dnsName":"new.home.example.test","targets":["10.0.0.70"],"recordType":"A"}]}`
	checkResponse(t, f.do(t, http.MethodPost, "/records", body), http.StatusInternalServerError)
}

func TestPostRecordsMalformedIs500(t *testing.T) {
	for _, body := range []string{"", "{", `{"create":"x"}`, "[]"} {
		f := newFixture(t, records.State{})
		got := checkResponse(t, f.do(t, http.MethodPost, "/records", body), http.StatusInternalServerError)
		if !strings.Contains(got, `"error"`) {
			t.Errorf("body for %q = %q, want an error object", body, got)
		}
	}
}

func TestWebhookTrailingDataIs500(t *testing.T) {
	create := `{"create":[{"dnsName":"new.home.example.test","targets":["10.0.0.70"],"recordType":"A"}]}`
	endpoints := `[{"dnsName":"new.home.example.test","targets":["10.0.0.70"],"recordType":"A"}]`
	for _, tt := range []struct{ path, body string }{
		{"/records", create + "}"},
		{"/records", create + "]"},
		{"/records", create + " {}"},
		{"/adjustendpoints", endpoints + "]"},
		{"/adjustendpoints", endpoints + "}"},
	} {
		f := newFixture(t, records.State{})
		got := checkResponse(t, f.do(t, http.MethodPost, tt.path, tt.body), http.StatusInternalServerError)
		if !strings.Contains(got, `"error"`) {
			t.Errorf("%s %q: body = %q, want an error object", tt.path, tt.body, got)
		}
		if f.saver.calls() != 0 || len(f.store.State().Records) != 0 {
			t.Errorf("%s %q: saver called %d times, state %+v; want nothing written", tt.path, tt.body, f.saver.calls(), f.store.State())
		}
	}
	f := newFixture(t, records.State{})
	checkResponse(t, f.do(t, http.MethodPost, "/records", create+"\n"), http.StatusNoContent)
}

// TestWebhookBodyTooLarge sends bodies that would be accepted but for their
// size, so only the 1 MiB cap can refuse them.
func TestWebhookBodyTooLarge(t *testing.T) {
	f := newFixture(t, records.State{})
	padded := `{"dnsName":"new.home.example.test","targets":["10.0.0.70"],"recordType":"A","labels":{"pad":"` + strings.Repeat("a", 1<<20) + `"}}`
	for path, body := range map[string]string{
		"/records":         `{"create":[` + padded + `]}`,
		"/adjustendpoints": "[" + padded + "]",
	} {
		got := checkResponse(t, f.do(t, http.MethodPost, path, body), http.StatusInternalServerError)
		if !strings.Contains(got, "too large") {
			t.Errorf("%s body = %.200q, want a too-large error", path, got)
		}
	}
	if f.saver.calls() != 0 {
		t.Errorf("saver called %d times, want 0", f.saver.calls())
	}
}

func TestAdjustEndpointsRoute(t *testing.T) {
	f := newFixture(t, records.State{})
	body := checkResponse(t, f.do(t, http.MethodPost, "/adjustendpoints",
		`[{"dnsName":"App.home.example.test","targets":["10.0.0.80"],"recordType":"A","recordTTL":60}]`), http.StatusOK)
	if want := `[{"dnsName":"app.home.example.test","targets":["10.0.0.80"],"recordType":"A"}]`; strings.TrimSpace(body) != want {
		t.Errorf("body = %q, want %q", body, want)
	}
	for _, in := range []string{"[]", "null"} {
		body = checkResponse(t, f.do(t, http.MethodPost, "/adjustendpoints", in), http.StatusOK)
		if strings.TrimSpace(body) != "[]" {
			t.Errorf("body for %s = %q, want []", in, body)
		}
	}
	checkResponse(t, f.do(t, http.MethodPost, "/adjustendpoints", "{"), http.StatusInternalServerError)
}

func TestHistorySelection(t *testing.T) {
	f := newFixture(t, records.State{})
	checkResponse(t, f.do(t, http.MethodGet, "/records", ""), http.StatusOK)
	if n := len(f.hist.Entries()); n != 0 {
		t.Fatalf("history has %d entries after GET /records, want 0", n)
	}

	body := `{"create":[{"dnsName":"app.home.example.test","targets":["10.0.0.80"],"recordType":"A"},` +
		`{"dnsName":"a-app.home.example.test","targets":["x"],"recordType":"TXT"}]}`
	checkResponse(t, f.do(t, http.MethodPost, "/records", body), http.StatusNoContent)
	entries := f.hist.Entries()
	if len(entries) != 1 {
		t.Fatalf("history has %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Listener != "webhook" || e.Method != http.MethodPost || e.Path != "/records" || e.Status != http.StatusNoContent || e.Summary != "create 2, update 0, delete 0" || e.Remote != "10.0.0.5" {
		t.Errorf("entry = %+v", e)
	}

	checkResponse(t, f.doFrom(t, "10.0.0.9:40000", http.MethodGet, "/records", ""), http.StatusForbidden)
	entries = f.hist.Entries()
	if len(entries) != 2 || entries[0].Status != http.StatusForbidden || entries[0].Remote != "10.0.0.9" {
		t.Errorf("entries = %+v, want the 403 recorded first", entries)
	}

	checkResponse(t, f.do(t, http.MethodPost, "/records", `{"create":[{"dnsName":"c.home.example.test","targets":["x"],"recordType":"CNAME"}]}`), http.StatusInternalServerError)
	entries = f.hist.Entries()
	if len(entries) != 3 || entries[0].Status != http.StatusInternalServerError || !strings.Contains(entries[0].Summary, "c.home.example.test") {
		t.Errorf("entries = %+v, want the failed POST recorded with its error", entries)
	}
}

func TestDroppedReportOnlyOnChange(t *testing.T) {
	f := newFixture(t, records.State{})
	withCNAME := `[{"dnsName":"app.home.example.test","targets":["10.0.0.80"],"recordType":"A"},` +
		`{"dnsName":"c.home.example.test","targets":["app.home.example.test"],"recordType":"CNAME"}]`
	for range 3 {
		checkResponse(t, f.do(t, http.MethodPost, "/adjustendpoints", withCNAME), http.StatusOK)
	}
	entries := f.hist.Entries()
	if len(entries) != 1 {
		t.Fatalf("history has %d entries, want 1: %+v", len(entries), entries)
	}
	e := entries[0]
	if e.Listener != "webhook" || e.Path != "/adjustendpoints" || e.Status != http.StatusOK || e.Remote != "10.0.0.5" ||
		!strings.HasPrefix(e.Summary, "dropped: ") || !strings.Contains(e.Summary, "c.home.example.test") {
		t.Errorf("entry = %+v", e)
	}

	checkResponse(t, f.do(t, http.MethodPost, "/adjustendpoints", `[{"dnsName":"app.home.example.test","targets":["10.0.0.80"],"recordType":"A"}]`), http.StatusOK)
	entries = f.hist.Entries()
	if len(entries) != 2 || entries[0].Summary != "dropped: none" {
		t.Errorf("entries = %+v, want a second entry \"dropped: none\"", entries)
	}
}

func TestDroppedReportPerCaller(t *testing.T) {
	f := newFixtureAllowing(t, records.State{}, netip.MustParsePrefix("10.0.0.0/24"))
	cname := func(name string) string {
		return `[{"dnsName":"app.home.example.test","targets":["10.0.0.80"],"recordType":"A"},` +
			`{"dnsName":"` + name + `","targets":["app.home.example.test"],"recordType":"CNAME"}]`
	}
	for range 3 {
		checkResponse(t, f.doFrom(t, "10.0.0.5:40000", http.MethodPost, "/adjustendpoints", cname("one.home.example.test")), http.StatusOK)
		checkResponse(t, f.doFrom(t, "10.0.0.6:40000", http.MethodPost, "/adjustendpoints", cname("two.home.example.test")), http.StatusOK)
	}
	entries := f.hist.Entries()
	if len(entries) != 2 {
		t.Fatalf("history has %d entries, want one per caller: %+v", len(entries), entries)
	}
	for _, want := range []struct{ remote, name string }{{"10.0.0.6", "two."}, {"10.0.0.5", "one."}} {
		e := entries[0]
		entries = entries[1:]
		if e.Remote != want.remote || !strings.Contains(e.Summary, want.name) {
			t.Errorf("entry = %+v, want %s reporting %s", e, want.remote, want.name)
		}
	}
	logs := f.logs.String()
	if n := strings.Count(logs, "adjustendpoints dropped endpoints changed"); n != 2 {
		t.Errorf("logged %d dropped reports, want 2:\n%s", n, logs)
	}
	for _, remote := range []string{"remote=10.0.0.5", "remote=10.0.0.6"} {
		if !strings.Contains(logs, remote) {
			t.Errorf("logs lack %s:\n%s", remote, logs)
		}
	}
}

// TestDroppedReportCallersAreBounded pins that the per-caller memory is
// bounded at 64 callers and forgets the least recently seen one first.
func TestDroppedReportCallersAreBounded(t *testing.T) {
	f := newFixtureAllowing(t, records.State{}, netip.MustParsePrefix("10.0.0.0/24"))
	body := `[{"dnsName":"c.home.example.test","targets":["app.home.example.test"],"recordType":"CNAME"}]`
	post := func(host int) {
		t.Helper()
		checkResponse(t, f.doFrom(t, "10.0.0."+strconv.Itoa(host)+":40000", http.MethodPost, "/adjustendpoints", body), http.StatusOK)
	}
	post(1)
	post(1)
	if n := len(f.hist.Entries()); n != 1 {
		t.Fatalf("history has %d entries after two calls from one caller, want 1", n)
	}
	for host := 2; host <= 64; host++ {
		post(host)
	}
	post(1)
	if n := len(f.hist.Entries()); n != 50 {
		t.Fatalf("history has %d entries, want the ring full at 50", n)
	}
	if e := f.hist.Entries()[0]; e.Remote != "10.0.0.64" {
		t.Errorf("newest entry from %s, want 10.0.0.64: caller 1 is still remembered", e.Remote)
	}
	post(65)
	post(1)
	if e := f.hist.Entries()[0]; e.Remote != "10.0.0.65" {
		t.Errorf("newest entry from %s, want 10.0.0.65: caller 1, seen recently, was forgotten", e.Remote)
	}
	post(2)
	if e := f.hist.Entries()[0]; e.Remote != "10.0.0.2" {
		t.Errorf("newest entry from %s, want 10.0.0.2 reported again once forgotten", e.Remote)
	}
}

func TestPostRoutesRequireWebhookMediaType(t *testing.T) {
	create := `{"create":[{"dnsName":"new.home.example.test","targets":["10.0.0.70"],"recordType":"A"}]}`
	endpoints := `[{"dnsName":"new.home.example.test","targets":["10.0.0.70"],"recordType":"A"}]`
	refused := []string{
		"",
		"text/plain",
		"text/plain;charset=utf-8",
		"application/json",
		"application/external.dns.webhook+json",
		"application/external.dns.webhook+json;version=2",
		"application/external.dns.webhook+json;version=1;charset=utf-8",
		"not a media type;;",
	}
	for _, tt := range []struct{ path, body string }{{"/records", create}, {"/adjustendpoints", endpoints}} {
		for _, ct := range refused {
			t.Run(tt.path+" "+ct, func(t *testing.T) {
				f := newFixture(t, records.State{})
				got := checkResponse(t, f.doWith(t, allowedRemote, ct, http.MethodPost, tt.path, tt.body), http.StatusUnsupportedMediaType)
				if !strings.Contains(got, `"error"`) {
					t.Errorf("body = %q, want an error object", got)
				}
				if f.saver.calls() != 0 || len(f.store.State().Records) != 0 {
					t.Errorf("saver called %d times, state %+v; want nothing written", f.saver.calls(), f.store.State())
				}
				if n := len(f.hist.Entries()); n != 1 || f.hist.Entries()[0].Status != http.StatusUnsupportedMediaType {
					t.Errorf("history = %+v, want the 415 recorded", f.hist.Entries())
				}
			})
		}
	}
	for _, ct := range []string{
		webhook.MediaType,
		"application/external.dns.webhook+json; version=1",
		"Application/External.DNS.Webhook+JSON;Version=1",
	} {
		t.Run("accepted "+ct, func(t *testing.T) {
			f := newFixture(t, records.State{})
			checkResponse(t, f.doWith(t, allowedRemote, ct, http.MethodPost, "/records", create), http.StatusNoContent)
			checkResponse(t, f.doWith(t, allowedRemote, ct, http.MethodPost, "/adjustendpoints", endpoints), http.StatusOK)
		})
	}
}
