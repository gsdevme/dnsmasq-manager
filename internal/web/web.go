// Package web serves the read-only status page: the health checks, the
// managed records and the recent request history. The page has no
// JavaScript and changes nothing; it reloads itself every 30 seconds.
//
// The template and stylesheet are embedded in the binary. Responses carry a
// Content-Security-Policy that allows only the page's own stylesheet.
package web

import (
	"bytes"
	"context"
	_ "embed"
	"html/template"
	"net/http"
	"net/netip"
	"time"

	"github.com/gsdevme/dnsmasq-manager/internal/health"
	"github.com/gsdevme/dnsmasq-manager/internal/history"
	"github.com/gsdevme/dnsmasq-manager/internal/records"
)

// contentSecurityPolicy forbids everything but the page's own stylesheet.
const contentSecurityPolicy = "default-src 'none'; style-src 'self'"

//go:embed page.html.tmpl
var pageSource string

//go:embed style.css
var stylesheet []byte

var page = template.Must(template.New("page").Parse(pageSource))

// HealthChecker runs the health checks shown at the top of the page.
// *health.Checker and health.Checker satisfy it.
type HealthChecker interface {
	Check(ctx context.Context) health.Result
}

// NewHandler returns the handler for the web listener. It serves GET / and
// GET /style.css only (HEAD too); any other path is 404 and any other method
// on those paths is 405. Each page request runs checker once with the
// request's context and reads the current state of store and hist.
func NewHandler(store *records.Store, checker HealthChecker, hist *history.Log) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		data := newView(checker.Check(r.Context()), store.State(), hist.Entries())
		var buf bytes.Buffer
		if err := page.Execute(&buf, data); err != nil {
			http.Error(w, "cannot render the page", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(buf.Bytes())
	})
	mux.HandleFunc("GET /style.css", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		_, _ = w.Write(stylesheet)
	})
	return mux
}

// view is the data the page template renders.
type view struct {
	Healthy  bool
	Checks   []checkView
	Records  []recordView
	TXTCount int
	History  []entryView
	Rendered time.Time
}

// checkView is one health check: OK, or the error that failed it.
type checkView struct {
	Name  string
	Label string
	OK    bool
	Error string
}

// recordView is one published record.
type recordView struct {
	Name   string
	Type   records.Type
	Values []netip.Addr
	Source records.Source
}

// entryView is one history entry, with its status class for colouring.
type entryView struct {
	history.Entry
	StatusClass string
}

// newView builds the page data from one health result, a state and the
// history entries, which are already newest first.
func newView(result health.Result, state records.State, entries []history.Entry) view {
	v := view{
		Healthy: result.OK(),
		Checks: []checkView{
			newCheck("hosts_dir", "Hosts directory writable", result.HostsDir),
			newCheck("dnsmasq", "dnsmasq answering queries", result.Dnsmasq),
		},
		TXTCount: len(state.TXTs),
		Rendered: time.Now(),
	}
	for _, r := range state.Records {
		v.Records = append(v.Records, recordView(r))
	}
	for _, e := range entries {
		v.History = append(v.History, entryView{Entry: e, StatusClass: statusClass(e.Status)})
	}
	return v
}

// newCheck returns the view of one check, which passed when err is nil.
func newCheck(name, label string, err error) checkView {
	c := checkView{Name: name, Label: label, OK: err == nil}
	if err != nil {
		c.Error = err.Error()
	}
	return c
}

// statusClass names the CSS class for an HTTP status code's class.
func statusClass(code int) string {
	switch {
	case code >= 500:
		return "s5"
	case code >= 400:
		return "s4"
	case code >= 300:
		return "s3"
	default:
		return "s2"
	}
}

// DurationText formats the entry's duration at a precision that suits it.
func (e entryView) DurationText() string {
	switch {
	case e.Duration < time.Millisecond:
		return e.Duration.Round(time.Microsecond).String()
	case e.Duration < time.Second:
		return e.Duration.Round(100 * time.Microsecond).String()
	default:
		return e.Duration.Round(10 * time.Millisecond).String()
	}
}
