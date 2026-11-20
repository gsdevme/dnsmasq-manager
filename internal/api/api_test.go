package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/gsdevme/dnsmasq-manager/internal/api"
	"github.com/gsdevme/dnsmasq-manager/internal/health"
	"github.com/gsdevme/dnsmasq-manager/internal/history"
	"github.com/gsdevme/dnsmasq-manager/internal/records"
)

var token = strings.Repeat("t", 32)

const appName = "app.home.example.test"

// fakeSaver records every state it saves and fails while err is set.
type fakeSaver struct {
	mu    sync.Mutex
	err   error
	saves int
}

func (f *fakeSaver) Save(records.State) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.saves++
	return nil
}

func (f *fakeSaver) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// fakeChecker returns a fixed health result.
type fakeChecker struct{ result health.Result }

func (f fakeChecker) Check(context.Context) health.Result { return f.result }

// testAPI is a handler over a real store with a fake saver and checker.
type testAPI struct {
	t       *testing.T
	store   *records.Store
	saver   *fakeSaver
	checker *fakeChecker
	hist    *history.Log
	logs    *bytes.Buffer
	handler http.Handler
}

func policy() records.Policy {
	return records.Policy{
		Zones:    []string{"home.example.test"},
		Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24"), netip.MustParsePrefix("fd00:10::/64")},
	}
}

func newTestAPI(t *testing.T, initial records.State) *testAPI {
	t.Helper()
	a := &testAPI{t: t, saver: &fakeSaver{}, checker: &fakeChecker{}, hist: history.New(history.Size), logs: &bytes.Buffer{}}
	a.store = records.NewStore(initial, policy(), a.saver)
	logger := slog.New(slog.NewJSONHandler(a.logs, nil))
	a.handler = api.NewHandler(a.store, a.checker, token, a.hist, logger)
	return a
}

// do serves one request, sending the right token when auth is true.
func (a *testAPI) do(method, target, body string, auth bool) *httptest.ResponseRecorder {
	a.t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	if auth {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	a.handler.ServeHTTP(w, r)
	return w
}

func wantStatus(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status = %d, want %d; body %s", w.Code, want, w.Body.String())
	}
}

// wantError checks w is status with a JSON {"error": "..."} body.
func wantError(t *testing.T, w *httptest.ResponseRecorder, status int) string {
	t.Helper()
	wantStatus(t, w, status)
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not JSON: %v", w.Body.String(), err)
	}
	if len(body) != 1 || body["error"] == "" {
		t.Fatalf("body = %q, want only a non-empty error", w.Body.String())
	}
	return body["error"]
}

func wantBody(t *testing.T, w *httptest.ResponseRecorder, want string) {
	t.Helper()
	if got := strings.TrimSpace(w.Body.String()); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func (a *testAPI) wantList(want string) {
	a.t.Helper()
	w := a.do(http.MethodGet, "/v1/records", "", true)
	wantStatus(a.t, w, http.StatusOK)
	wantBody(a.t, w, want)
}

func addrs(s ...string) []netip.Addr {
	out := make([]netip.Addr, len(s))
	for i, v := range s {
		out[i] = netip.MustParseAddr(v)
	}
	return out
}

func externalDNSState() records.State {
	d := records.State{}.Edit()
	d.Set(appName, records.TypeA, addrs("10.0.0.80"), records.SourceExternalDNS)
	d.SetTXT(records.TXT{Name: "a-app.home.example.test", Targets: []string{"heritage=external-dns"}, OwnedRecord: appName})
	d.SetTXT(records.TXT{Name: "a-other.home.example.test", Targets: []string{"heritage=external-dns"}, OwnedRecord: "other.home.example.test"})
	return d.State()
}

const putA = `{"type":"A","values":["10.0.0.80"]}`

const appRecordA = `{"name":"app.home.example.test","type":"A","values":["10.0.0.80"],"source":"api"}`

func TestAuth(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   int
	}{
		{name: "no header", header: "", want: http.StatusUnauthorized},
		{name: "wrong token", header: "Bearer " + strings.Repeat("u", 32), want: http.StatusUnauthorized},
		{name: "empty token", header: "Bearer ", want: http.StatusUnauthorized},
		{name: "basic scheme", header: "Basic " + token, want: http.StatusUnauthorized},
		{name: "right token", header: "Bearer " + token, want: http.StatusOK},
		{name: "scheme case-insensitive", header: "bEARER " + token, want: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestAPI(t, records.State{})
			r := httptest.NewRequest(http.MethodGet, "/v1/records", nil)
			if tt.header != "" {
				r.Header.Set("Authorization", tt.header)
			}
			w := httptest.NewRecorder()
			a.handler.ServeHTTP(w, r)
			if tt.want == http.StatusOK {
				wantStatus(t, w, http.StatusOK)
				return
			}
			wantError(t, w, http.StatusUnauthorized)
			if got := w.Header().Get("WWW-Authenticate"); got != "Bearer" {
				t.Errorf("WWW-Authenticate = %q, want Bearer", got)
			}
		})
	}
}

func TestHealthzNeedsNoToken(t *testing.T) {
	a := newTestAPI(t, records.State{})
	w := a.do(http.MethodGet, "/healthz", "", false)
	wantStatus(t, w, http.StatusOK)
	wantBody(t, w, `{"status":"ok","checks":{"hosts_dir":"ok","dnsmasq":"ok"}}`)

	a.checker.result = health.Result{Dnsmasq: errors.New("no reply from 127.0.0.1:53")}
	w = a.do(http.MethodGet, "/healthz", "", false)
	wantStatus(t, w, http.StatusServiceUnavailable)
	wantBody(t, w, `{"status":"unhealthy","checks":{"hosts_dir":"ok","dnsmasq":"no reply from 127.0.0.1:53"}}`)
}

func TestListEmpty(t *testing.T) {
	a := newTestAPI(t, records.State{})
	a.wantList(`{"records":[]}`)
}

func TestPutCreatesThenLists(t *testing.T) {
	a := newTestAPI(t, records.State{})
	w := a.do(http.MethodPut, "/v1/records/"+appName, putA, true)
	wantStatus(t, w, http.StatusOK)
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	wantBody(t, w, appRecordA)
	a.wantList(`{"records":[` + appRecordA + `]}`)
}

func TestPutIsIdempotent(t *testing.T) {
	a := newTestAPI(t, records.State{})
	for range 2 {
		w := a.do(http.MethodPut, "/v1/records/"+appName, putA, true)
		wantStatus(t, w, http.StatusOK)
		wantBody(t, w, appRecordA)
	}
	if a.saver.saves != 1 {
		t.Errorf("saves = %d, want 1", a.saver.saves)
	}
	a.wantList(`{"records":[` + appRecordA + `]}`)
}

func TestPutLeavesOtherType(t *testing.T) {
	a := newTestAPI(t, records.State{})
	wantStatus(t, a.do(http.MethodPut, "/v1/records/"+appName, putA, true), http.StatusOK)
	w := a.do(http.MethodPut, "/v1/records/"+appName, `{"type":"AAAA","values":["fd00:10::80"]}`, true)
	wantStatus(t, w, http.StatusOK)
	wantBody(t, w, `{"name":"app.home.example.test","type":"AAAA","values":["fd00:10::80"],"source":"api"}`)
	a.wantList(`{"records":[` + appRecordA + `,{"name":"app.home.example.test","type":"AAAA","values":["fd00:10::80"],"source":"api"}]}`)
}

func TestPutAcceptsAndIgnoresTTL(t *testing.T) {
	for _, body := range []string{
		`{"type":"A","values":["10.0.0.80"],"ttl":300}`,
		`{"type":"A","values":["10.0.0.80"],"ttl":0}`,
		`{"type":"A","values":["10.0.0.80"],"ttl":null}`,
	} {
		t.Run(body, func(t *testing.T) {
			a := newTestAPI(t, records.State{})
			w := a.do(http.MethodPut, "/v1/records/"+appName, body, true)
			wantStatus(t, w, http.StatusOK)
			wantBody(t, w, appRecordA)
		})
	}
}

func TestPutValidation(t *testing.T) {
	tests := []struct {
		name, path, body string
	}{
		{name: "CNAME", path: appName, body: `{"type":"CNAME","values":["10.0.0.80"]}`},
		{name: "lowercase type", path: appName, body: `{"type":"a","values":["10.0.0.80"]}`},
		{name: "missing type", path: appName, body: `{"values":["10.0.0.80"]}`},
		{name: "outside zone", path: "app.example.test", body: putA},
		{name: "wildcard", path: "*.home.example.test", body: putA},
		{name: "apex", path: "home.example.test", body: putA},
		{name: "outside prefixes", path: appName, body: `{"type":"A","values":["8.8.8.8"]}`},
		{name: "empty values", path: appName, body: `{"type":"A","values":[]}`},
		{name: "missing values", path: appName, body: `{"type":"A"}`},
		{name: "negative ttl", path: appName, body: `{"type":"A","values":["10.0.0.80"],"ttl":-1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestAPI(t, records.State{})
			wantError(t, a.do(http.MethodPut, "/v1/records/"+tt.path, tt.body, true), http.StatusUnprocessableEntity)
			a.wantList(`{"records":[]}`)
		})
	}
}

func TestPutBadJSON(t *testing.T) {
	tests := []struct{ name, body string }{
		{name: "malformed", body: `{"type":"A",`},
		{name: "empty body", body: ``},
		{name: "unknown field", body: `{"type":"A","values":["10.0.0.80"],"name":"x"}`},
		{name: "trailing data", body: putA + `{}`},
		{name: "wrong JSON type", body: `{"type":"A","values":"10.0.0.80"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestAPI(t, records.State{})
			wantError(t, a.do(http.MethodPut, "/v1/records/"+appName, tt.body, true), http.StatusBadRequest)
			a.wantList(`{"records":[]}`)
		})
	}
}

func TestPutBodyTooLarge(t *testing.T) {
	a := newTestAPI(t, records.State{})
	body := `{"type":"A","values":["10.0.0.80"]` + strings.Repeat(" ", 64<<10) + `}`
	wantError(t, a.do(http.MethodPut, "/v1/records/"+appName, body, true), http.StatusRequestEntityTooLarge)

	fits := `{"type":"A","values":["10.0.0.80"]}`
	fits = fits[:len(fits)-1] + strings.Repeat(" ", 64<<10-len(fits)) + "}"
	wantStatus(t, a.do(http.MethodPut, "/v1/records/"+appName, fits, true), http.StatusOK)
}

func TestPutNormalisesName(t *testing.T) {
	a := newTestAPI(t, records.State{})
	w := a.do(http.MethodPut, "/v1/records/App.Home.Example.Test.", putA, true)
	wantStatus(t, w, http.StatusOK)
	wantBody(t, w, appRecordA)
	a.wantList(`{"records":[` + appRecordA + `]}`)
}

func apiState() records.State {
	d := records.State{}.Edit()
	d.Set(appName, records.TypeA, addrs("10.0.0.80"), records.SourceAPI)
	d.Set(appName, records.TypeAAAA, addrs("fd00:10::80"), records.SourceAPI)
	d.Set("nas.home.example.test", records.TypeA, addrs("10.0.0.40"), records.SourceAPI)
	return d.State()
}

const nasRecord = `{"name":"nas.home.example.test","type":"A","values":["10.0.0.40"],"source":"api"}`

func TestDeleteWholeName(t *testing.T) {
	a := newTestAPI(t, apiState())
	w := a.do(http.MethodDelete, "/v1/records/"+appName, "", true)
	wantStatus(t, w, http.StatusNoContent)
	if w.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", w.Body.String())
	}
	a.wantList(`{"records":[` + nasRecord + `]}`)
}

func TestDeleteOneType(t *testing.T) {
	a := newTestAPI(t, apiState())
	wantStatus(t, a.do(http.MethodDelete, "/v1/records/"+appName+"?type=AAAA", "", true), http.StatusNoContent)
	a.wantList(`{"records":[` + appRecordA + `,` + nasRecord + `]}`)
}

func TestDeleteAbsentIs204(t *testing.T) {
	for _, target := range []string{
		"/v1/records/gone.home.example.test",
		"/v1/records/gone.home.example.test?type=A",
		"/v1/records/" + appName + "?type=A",
	} {
		t.Run(target, func(t *testing.T) {
			a := newTestAPI(t, records.State{})
			wantStatus(t, a.do(http.MethodDelete, target, "", true), http.StatusNoContent)
			if a.saver.saves != 0 {
				t.Errorf("saves = %d, want 0", a.saver.saves)
			}
		})
	}
}

func TestDeleteValidation(t *testing.T) {
	for _, target := range []string{
		"/v1/records/" + appName + "?type=CNAME",
		"/v1/records/home.example.test",
		"/v1/records/app.example.test",
	} {
		t.Run(target, func(t *testing.T) {
			a := newTestAPI(t, apiState())
			wantError(t, a.do(http.MethodDelete, target, "", true), http.StatusUnprocessableEntity)
		})
	}
}

func TestExternalDNSNameNeedsForce(t *testing.T) {
	const otherTXT = `a-other.home.example.test`
	checkTXTs := func(t *testing.T, s records.State) {
		t.Helper()
		if len(s.TXTs) != 1 || s.TXTs[0].Name != otherTXT {
			t.Errorf("TXTs = %+v, want only %s", s.TXTs, otherTXT)
		}
	}

	t.Run("PUT", func(t *testing.T) {
		a := newTestAPI(t, externalDNSState())
		const body = `{"type":"A","values":["10.0.0.81"]}`
		wantError(t, a.do(http.MethodPut, "/v1/records/"+appName, body, true), http.StatusConflict)
		wantError(t, a.do(http.MethodPut, "/v1/records/"+appName+"?force=false", body, true), http.StatusConflict)
		if !a.store.State().Equal(externalDNSState()) {
			t.Fatalf("state changed after 409: %+v", a.store.State())
		}

		w := a.do(http.MethodPut, "/v1/records/"+appName+"?force=true", body, true)
		wantStatus(t, w, http.StatusOK)
		wantBody(t, w, `{"name":"app.home.example.test","type":"A","values":["10.0.0.81"],"source":"api"}`)
		checkTXTs(t, a.store.State())
	})

	t.Run("PUT other type keeps the name together", func(t *testing.T) {
		a := newTestAPI(t, externalDNSState())
		w := a.do(http.MethodPut, "/v1/records/"+appName+"?force=true", `{"type":"AAAA","values":["fd00:10::80"]}`, true)
		wantStatus(t, w, http.StatusOK)
		a.wantList(`{"records":[` + appRecordA + `,{"name":"app.home.example.test","type":"AAAA","values":["fd00:10::80"],"source":"api"}]}`)
	})

	t.Run("DELETE", func(t *testing.T) {
		a := newTestAPI(t, externalDNSState())
		wantError(t, a.do(http.MethodDelete, "/v1/records/"+appName, "", true), http.StatusConflict)
		if !a.store.State().Equal(externalDNSState()) {
			t.Fatalf("state changed after 409: %+v", a.store.State())
		}
		wantStatus(t, a.do(http.MethodDelete, "/v1/records/"+appName+"?force=true", "", true), http.StatusNoContent)
		s := a.store.State()
		if len(s.Records) != 0 {
			t.Errorf("Records = %+v, want none", s.Records)
		}
		checkTXTs(t, s)
	})

	t.Run("DELETE one type takes over the rest", func(t *testing.T) {
		d := externalDNSState().Edit()
		d.Set(appName, records.TypeAAAA, addrs("fd00:10::80"), records.SourceExternalDNS)
		a := newTestAPI(t, d.State())
		wantError(t, a.do(http.MethodDelete, "/v1/records/"+appName+"?type=AAAA", "", true), http.StatusConflict)
		wantStatus(t, a.do(http.MethodDelete, "/v1/records/"+appName+"?type=AAAA&force=true", "", true), http.StatusNoContent)
		a.wantList(`{"records":[` + appRecordA + `]}`)
		checkTXTs(t, a.store.State())
	})
}

func TestBadQuery(t *testing.T) {
	tests := []struct{ method, target string }{
		{http.MethodPut, "/v1/records/" + appName + "?force=yes"},
		{http.MethodPut, "/v1/records/" + appName + "?force=TRUE"},
		{http.MethodPut, "/v1/records/" + appName + "?force=true&force=false"},
		{http.MethodDelete, "/v1/records/" + appName + "?force=yes"},
		{http.MethodDelete, "/v1/records/" + appName + "?type=x"},
		{http.MethodDelete, "/v1/records/" + appName + "?type=a"},
		{http.MethodDelete, "/v1/records/" + appName + "?type="},
		{http.MethodDelete, "/v1/records/" + appName + "?type=A&type=AAAA"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.target, func(t *testing.T) {
			a := newTestAPI(t, apiState())
			wantError(t, a.do(tt.method, tt.target, putA, true), http.StatusBadRequest)
			if !a.store.State().Equal(apiState()) {
				t.Errorf("state changed: %+v", a.store.State())
			}
		})
	}
}

func TestPutReturns500WhenSaveFails(t *testing.T) {
	a := newTestAPI(t, records.State{})
	a.saver.setErr(errors.New("hosts directory is gone"))
	wantError(t, a.do(http.MethodPut, "/v1/records/"+appName, putA, true), http.StatusInternalServerError)
	a.wantList(`{"records":[]}`)

	a.saver.setErr(nil)
	wantStatus(t, a.do(http.MethodPut, "/v1/records/"+appName, putA, true), http.StatusOK)
	a.wantList(`{"records":[` + appRecordA + `]}`)
}

func TestMethodNotAllowed(t *testing.T) {
	tests := []struct{ method, target string }{
		{http.MethodPost, "/v1/records"},
		{http.MethodGet, "/v1/records/" + appName},
		{http.MethodPost, "/healthz"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.target, func(t *testing.T) {
			a := newTestAPI(t, records.State{})
			w := a.do(tt.method, tt.target, "", true)
			wantError(t, w, http.StatusMethodNotAllowed)
			if w.Header().Get("Allow") == "" {
				t.Error("no Allow header")
			}
		})
	}
}

func TestNotFound(t *testing.T) {
	a := newTestAPI(t, records.State{})
	wantError(t, a.do(http.MethodGet, "/v2/records", "", true), http.StatusNotFound)
	wantError(t, a.do(http.MethodGet, "/v2/records", "", false), http.StatusUnauthorized)
}

func TestHistory(t *testing.T) {
	a := newTestAPI(t, records.State{})
	wantStatus(t, a.do(http.MethodPut, "/v1/records/"+appName+"?force=false", putA, true), http.StatusOK)
	wantStatus(t, a.do(http.MethodGet, "/v1/records", "", false), http.StatusUnauthorized)
	wantStatus(t, a.do(http.MethodGet, "/healthz", "", false), http.StatusOK)

	got := a.hist.Entries()
	if len(got) != 2 {
		t.Fatalf("len(Entries()) = %d, want 2: %+v", len(got), got)
	}
	want := []history.Entry{
		{Listener: "api", Method: http.MethodGet, Path: "/v1/records", Status: http.StatusUnauthorized, Remote: "192.0.2.1", Summary: "missing bearer token"},
		{Listener: "api", Method: http.MethodPut, Path: "/v1/records/" + appName + "?force=false", Status: http.StatusOK, Remote: "192.0.2.1", Summary: "set app.home.example.test A"},
	}
	for i, w := range want {
		g := got[i]
		g.Time, g.Duration = w.Time, w.Duration
		if g != w {
			t.Errorf("Entries()[%d] = %+v, want %+v", i, g, w)
		}
	}
}

func TestHistorySummaries(t *testing.T) {
	tests := []struct {
		name, method, target, body, want string
	}{
		{name: "list", method: http.MethodGet, target: "/v1/records", want: "list"},
		{name: "set", method: http.MethodPut, target: "/v1/records/App.Home.Example.Test", body: `{"type":"AAAA","values":["fd00:10::80"]}`, want: "set app.home.example.test AAAA"},
		{name: "delete name", method: http.MethodDelete, target: "/v1/records/" + appName, want: "delete app.home.example.test"},
		{name: "delete type", method: http.MethodDelete, target: "/v1/records/" + appName + "?type=A", want: "delete app.home.example.test A"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestAPI(t, apiState())
			a.do(tt.method, tt.target, tt.body, true)
			if got := a.hist.Entries()[0].Summary; got != tt.want {
				t.Errorf("Summary = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("failure carries the error", func(t *testing.T) {
		a := newTestAPI(t, records.State{})
		msg := wantError(t, a.do(http.MethodPut, "/v1/records/"+appName, `{"type":"CNAME","values":["10.0.0.80"]}`, true), http.StatusUnprocessableEntity)
		if got := a.hist.Entries()[0].Summary; got != msg {
			t.Errorf("Summary = %q, want the error %q", got, msg)
		}
	})
}

func TestMutationIsLogged(t *testing.T) {
	a := newTestAPI(t, records.State{})
	wantStatus(t, a.do(http.MethodPut, "/v1/records/"+appName, putA, true), http.StatusOK)

	var line map[string]any
	if err := json.Unmarshal(a.logs.Bytes(), &line); err != nil {
		t.Fatalf("log %q is not one JSON line: %v", a.logs.String(), err)
	}
	want := map[string]string{"msg": "record changed", "listener": "api", "remote": "192.0.2.1", "name": appName, "type": "A", "before": "", "after": "10.0.0.80"}
	for k, v := range want {
		if line[k] != v {
			t.Errorf("log %s = %v, want %q", k, line[k], v)
		}
	}

	a.logs.Reset()
	wantStatus(t, a.do(http.MethodPut, "/v1/records/"+appName, putA, true), http.StatusOK)
	if a.logs.Len() != 0 {
		t.Errorf("unchanged PUT logged %q", a.logs.String())
	}
}
