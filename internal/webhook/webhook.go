// Package webhook is the external-dns v0.24.0 webhook provider: an HTTP
// handler for the negotiation, records and adjustendpoints calls, and the
// logic that turns external-dns change sets into edits of the record store.
//
// external-dns exits on any 4xx during a sync and accepts only exact success
// codes, so every failure inside a route is a 500 and every reply carries
// MediaType. The only 4xx replies are 403 from the source allowlist, 404 or
// 405 for unknown routes and methods, and 415 for a POST without MediaType as
// its Content-Type: external-dns always sends it, so such a request is not
// external-dns, and refusing it stops a browser on an allowlisted address
// from posting a change set cross-origin as text/plain.
package webhook

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gsdevme/dnsmasq-manager/internal/history"
	"github.com/gsdevme/dnsmasq-manager/internal/records"
)

// MediaType is the Content-Type of every webhook reply. external-dns requires
// it exactly on negotiation.
const MediaType = "application/external.dns.webhook+json;version=1"

// maxBody is the largest request body read; a larger one is a 500.
const maxBody = 1 << 20

// maxCallers bounds how many callers' dropped-endpoint lists are remembered;
// the least recently seen caller is forgotten first.
const maxCallers = 64

// listener names the webhook listener in history and logs.
const listener = "webhook"

// handler serves the webhook routes over one store.
type handler struct {
	store  *records.Store
	hist   *history.Log
	logger *slog.Logger

	mu      sync.Mutex
	callers []callerDropped
}

// callerDropped is the dropped-endpoint list one caller's last
// /adjustendpoints call produced. remote is the caller's IP as the allowlist
// checked it.
type callerDropped struct {
	remote  string
	dropped []string
}

// NewHandler returns the webhook listener's handler: history outermost
// (listener "webhook": every POST /records, anything with status >= 400),
// then the allowlist, then the routes.
//
// Only a request whose connection address is inside allowed is served; any
// other gets 403, and forwarding headers are ignored. History wraps the
// allowlist so that those 403 replies are recorded. hist and logger must not
// be nil.
func NewHandler(store *records.Store, allowed []netip.Prefix, hist *history.Log, logger *slog.Logger) http.Handler {
	h := &handler{store: store, hist: hist, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.negotiate)
	mux.HandleFunc("GET /records", h.getRecords)
	mux.HandleFunc("POST /records", h.requireMediaType(h.postRecords))
	mux.HandleFunc("POST /adjustendpoints", h.requireMediaType(h.adjustEndpoints))
	return hist.Middleware(listener, keep, allowlist(allowed, routes(mux)))
}

// keep selects the webhook requests history records.
func keep(r *http.Request, status int) bool {
	return status >= http.StatusBadRequest || (r.Method == http.MethodPost && r.URL.Path == "/records")
}

// allowlist refuses with 403 any request whose remote address is not inside
// one of allowed.
func allowlist(allowed []netip.Prefix, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowedSource(allowed, r.RemoteAddr) {
			writeError(w, http.StatusForbidden, "source address not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// allowedSource reports whether the IP of remoteAddr, unmapped from IPv6 and
// without a zone, lies inside one of allowed.
func allowedSource(allowed []netip.Prefix, remoteAddr string) bool {
	ap, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		return false
	}
	addr := ap.Addr().Unmap().WithZone("")
	return slices.ContainsFunc(allowed, func(p netip.Prefix) bool { return p.Contains(addr) })
}

// routes serves mux, answering unknown routes and methods itself so that the
// 404 and 405 replies carry MediaType and a JSON body.
func routes(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, pattern := mux.Handler(r)
		if pattern != "" {
			h.ServeHTTP(w, r)
			return
		}
		probe := &statusProbe{header: http.Header{}}
		h.ServeHTTP(probe, r)
		if allow := probe.header.Get("Allow"); allow != "" {
			w.Header().Set("Allow", allow)
		}
		status := probe.status
		if status == 0 {
			status = http.StatusNotFound
		}
		writeError(w, status, strings.ToLower(http.StatusText(status)))
	})
}

// statusProbe is a ResponseWriter that keeps only the status and headers the
// mux's own 404 and 405 handlers write.
type statusProbe struct {
	header http.Header
	status int
}

func (p *statusProbe) Header() http.Header { return p.header }

func (p *statusProbe) Write(b []byte) (int, error) { return len(b), nil }

func (p *statusProbe) WriteHeader(status int) {
	if p.status == 0 {
		p.status = status
	}
}

// requireMediaType refuses with 415, before reading the body, a request whose
// Content-Type is not MediaType. The media type and parameter names compare
// case-insensitively and optional whitespace after ';' is allowed, but the
// only parameter must be version=1.
func (h *handler) requireMediaType(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Content-Type")
		typ, params, err := mime.ParseMediaType(got)
		if err == nil && typ == "application/external.dns.webhook+json" && len(params) == 1 && params["version"] == "1" {
			next(w, r)
			return
		}
		msg := fmt.Sprintf("Content-Type %q is not %s", got, MediaType)
		h.logger.Warn("webhook request refused",
			slog.String("listener", listener),
			slog.String("remote", history.RemoteIP(r)),
			slog.String("path", r.URL.Path),
			slog.String("error", msg),
		)
		history.SetSummary(r, msg)
		writeError(w, http.StatusUnsupportedMediaType, msg)
	}
}

// negotiate answers GET / with the configured zones.
func (h *handler) negotiate(w http.ResponseWriter, _ *http.Request) {
	zones := h.store.Policy().Zones
	if zones == nil {
		zones = []string{}
	}
	writeJSON(w, http.StatusOK, struct {
		Include []string `json:"include"`
	}{zones})
}

// getRecords answers GET /records with every record and TXT.
func (h *handler) getRecords(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, Endpoints(h.store.State()))
}

// postRecords applies a change set as one store update: 204 when it is
// applied, 500 and no write when any entry is refused or saving fails.
func (h *handler) postRecords(w http.ResponseWriter, r *http.Request) {
	var c Changes
	if err := decode(w, r, &c); err != nil {
		h.fail(w, r, "malformed change set: "+err.Error())
		return
	}
	summary := fmt.Sprintf("create %d, update %d, delete %d", len(c.Create), len(c.UpdateNew), len(c.Delete))
	if c.empty() {
		history.SetSummary(r, summary)
		writeStatus(w, http.StatusNoContent)
		return
	}
	policy := h.store.Policy()
	before, after, err := h.store.Update(func(d *records.Draft) error {
		return Apply(policy, d, c)
	})
	if err != nil {
		h.fail(w, r, err.Error())
		return
	}
	history.SetSummary(r, summary)
	records.LogChanges(h.logger, listener, history.RemoteIP(r), before, after)
	writeStatus(w, http.StatusNoContent)
}

// adjustEndpoints answers POST /adjustendpoints with the endpoints the
// service would accept, and reports the dropped ones when they change.
func (h *handler) adjustEndpoints(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var in []Endpoint
	if err := decode(w, r, &in); err != nil {
		h.fail(w, r, "malformed endpoints: "+err.Error())
		return
	}
	kept, dropped := Adjust(h.store.Policy(), in)
	slices.Sort(dropped)
	h.reportDropped(r, start, dropped)
	writeJSON(w, http.StatusOK, kept)
}

// reportDropped logs and records dropped when it differs from the previous
// list from the same caller, so callers with different standing lists, such
// as external-dns in two clusters, each report only their own changes.
func (h *handler) reportDropped(r *http.Request, start time.Time, dropped []string) {
	remote := history.RemoteIP(r)
	if !h.droppedChanged(remote, dropped) {
		return
	}
	summary := "dropped: none"
	if len(dropped) > 0 {
		summary = "dropped: " + strings.Join(dropped, "; ")
	}
	h.logger.Warn("adjustendpoints dropped endpoints changed",
		slog.String("listener", listener),
		slog.String("remote", remote),
		slog.String("dropped", summary),
	)
	h.hist.Add(history.Entry{
		Time:     start,
		Listener: listener,
		Method:   r.Method,
		Path:     r.URL.Path,
		Status:   http.StatusOK,
		Remote:   remote,
		Duration: time.Since(start),
		Summary:  summary,
	})
}

// droppedChanged remembers dropped as remote's latest list and reports
// whether it differs from the one before. A caller not remembered had none.
// At most maxCallers callers are kept, the most recently seen last.
func (h *handler) droppedChanged(remote string, dropped []string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	var previous []string
	if i := slices.IndexFunc(h.callers, func(c callerDropped) bool { return c.remote == remote }); i >= 0 {
		previous = h.callers[i].dropped
		h.callers = slices.Delete(h.callers, i, i+1)
	}
	if len(h.callers) >= maxCallers {
		h.callers = slices.Delete(h.callers, 0, len(h.callers)-maxCallers+1)
	}
	h.callers = append(h.callers, callerDropped{remote: remote, dropped: dropped})
	return !slices.Equal(previous, dropped)
}

// fail logs msg, sets it as the history summary and replies 500 with it.
func (h *handler) fail(w http.ResponseWriter, r *http.Request, msg string) {
	h.logger.Warn("webhook request failed",
		slog.String("listener", listener),
		slog.String("remote", history.RemoteIP(r)),
		slog.String("path", r.URL.Path),
		slog.String("error", msg),
	)
	history.SetSummary(r, msg)
	writeError(w, http.StatusInternalServerError, msg)
}

// decode reads r's body, at most maxBody bytes, as one JSON value into v.
// Anything but whitespace after that value is an error, including a stray
// closing bracket or brace.
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(v); err != nil {
		return err
	}
	var extra json.RawMessage
	switch err := dec.Decode(&extra); {
	case errors.Is(err, io.EOF):
		return nil
	case err == nil:
		return errors.New("unexpected data after the JSON value")
	default:
		return fmt.Errorf("unexpected data after the JSON value: %w", err)
	}
}

// writeStatus replies with status and no body.
func writeStatus(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", MediaType)
	w.WriteHeader(status)
}

// writeError replies with status and {"error": msg}.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{msg})
}

// writeJSON replies with status and v encoded as JSON.
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(`{"error":"encoding the reply failed"}`)
	}
	w.Header().Set("Content-Type", MediaType)
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}
