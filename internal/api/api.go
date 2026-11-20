// Package api is the REST listener's HTTP handler: bearer-token auth, the
// unauthenticated health check, and listing, setting and deleting A and AAAA
// records in the shared records.Store.
//
// Every response body is JSON. Errors are {"error":"<message>"} with the
// status the spec assigns: 400 bad JSON, a bad or unknown query parameter, 401 bad token, 409 a name
// owned by external-dns without force=true, 413 a body over 64 KiB, 422 a
// validation failure, 500 a failed save.
package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/gsdevme/dnsmasq-manager/internal/health"
	"github.com/gsdevme/dnsmasq-manager/internal/history"
	"github.com/gsdevme/dnsmasq-manager/internal/records"
)

// maxBodyBytes is the largest request body accepted; larger is a 413.
const maxBodyBytes = 64 << 10

// listener names this handler in history entries and change logs.
const listener = "api"

// healthPath is the one route that needs no token and is not kept in history.
const healthPath = "/healthz"

// typeWord matches a query type that names a DNS record type the service
// does not support, such as CNAME; those are 422 rather than 400.
var typeWord = regexp.MustCompile(`^[A-Z][A-Z0-9]*$`)

// HealthChecker runs the health checks behind GET /healthz.
type HealthChecker interface {
	Check(ctx context.Context) health.Result
}

// handler holds what the routes share.
type handler struct {
	store   *records.Store
	checker HealthChecker
	token   [sha256.Size]byte
	logger  *slog.Logger
}

// NewHandler returns the REST listener's handler, already wrapped in the
// history middleware (listener "api", every request except /healthz).
//
// token is the bearer token every route except /healthz requires; an empty
// presented token is always refused. Successful changes are logged to logger
// with records.LogChanges.
func NewHandler(store *records.Store, checker HealthChecker, token string, hist *history.Log, logger *slog.Logger) http.Handler {
	h := &handler{store: store, checker: checker, token: sha256.Sum256([]byte(token)), logger: logger}

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+healthPath, h.healthz)
	mux.HandleFunc(healthPath, methodNotAllowed("GET, HEAD"))
	mux.HandleFunc("GET /v1/records", h.list)
	mux.HandleFunc("/v1/records", methodNotAllowed("GET, HEAD"))
	mux.HandleFunc("PUT /v1/records/{name}", h.put)
	mux.HandleFunc("DELETE /v1/records/{name}", h.delete)
	mux.HandleFunc("/v1/records/{name}", methodNotAllowed("PUT, DELETE"))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusNotFound, "not found")
	})

	keep := func(r *http.Request, _ int) bool { return r.URL.Path != healthPath }
	return hist.Middleware(listener, keep, h.authenticate(mux))
}

// authenticate refuses every request except /healthz that lacks the right
// bearer token. The scheme word is matched case-insensitively and the tokens
// are compared as SHA-256 digests in constant time.
func (h *handler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == healthPath {
			next.ServeHTTP(w, r)
			return
		}
		scheme, presented, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		presented = strings.TrimSpace(presented)
		if !strings.EqualFold(scheme, "Bearer") || presented == "" {
			unauthorized(w, r, "missing bearer token")
			return
		}
		digest := sha256.Sum256([]byte(presented))
		if subtle.ConstantTimeCompare(digest[:], h.token[:]) != 1 {
			unauthorized(w, r, "wrong bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// unauthorized writes a 401 with the challenge header.
func unauthorized(w http.ResponseWriter, r *http.Request, msg string) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	writeError(w, r, http.StatusUnauthorized, msg)
}

// methodNotAllowed returns a handler writing a JSON 405 with allow as the
// Allow header.
func methodNotAllowed(allow string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allow)
		writeError(w, r, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// healthResponse is the GET /healthz body.
type healthResponse struct {
	Status string       `json:"status"`
	Checks healthChecks `json:"checks"`
}

// healthChecks holds "ok" or the error text of each check.
type healthChecks struct {
	HostsDir string `json:"hosts_dir"`
	Dnsmasq  string `json:"dnsmasq"`
}

// healthz reports the health checks: 200 when all pass, else 503.
func (h *handler) healthz(w http.ResponseWriter, r *http.Request) {
	if !onlyQuery(w, r) {
		return
	}
	res := h.checker.Check(r.Context())
	body := healthResponse{Status: "ok", Checks: healthChecks{HostsDir: checkText(res.HostsDir), Dnsmasq: checkText(res.Dnsmasq)}}
	status := http.StatusOK
	if !res.OK() {
		body.Status = "unhealthy"
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, body)
}

// checkText is "ok" for a passing check, else its error text.
func checkText(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

// recordJSON is one record as the API shows it.
type recordJSON struct {
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	Values []string `json:"values"`
	Source string   `json:"source"`
}

// toJSON converts r to its API form, addresses in canonical text.
func toJSON(r records.Record) recordJSON {
	values := make([]string, len(r.Values))
	for i, v := range r.Values {
		values[i] = v.String()
	}
	return recordJSON{Name: r.Name, Type: string(r.Type), Values: values, Source: string(r.Source)}
}

// list writes every A and AAAA record, in the state's canonical order.
func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	if !onlyQuery(w, r) {
		return
	}
	state := h.store.State()
	out := make([]recordJSON, 0, len(state.Records))
	for _, rec := range state.Records {
		out = append(out, toJSON(rec))
	}
	history.SetSummary(r, "list")
	writeJSON(w, http.StatusOK, struct {
		Records []recordJSON `json:"records"`
	}{out})
}

// putBody is the PUT /v1/records/{name} request body. TTL is checked and
// otherwise ignored.
type putBody struct {
	Type   string   `json:"type"`
	Values []string `json:"values"`
	TTL    *int64   `json:"ttl"`
}

// put sets one name's record of one type, taking the name over from
// external-dns when force=true, and drops every TXT ownership record naming it.
func (h *handler) put(w http.ResponseWriter, r *http.Request) {
	if !onlyQuery(w, r, "force") {
		return
	}
	force, err := boolQuery(r, "force")
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	var body putBody
	if status, err := decode(w, r, &body); err != nil {
		writeError(w, r, status, err.Error())
		return
	}
	policy := h.store.Policy()
	name, err := policy.NormalizeName(r.PathValue("name"))
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, err.Error())
		return
	}
	typ, err := records.ParseType(body.Type)
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, err.Error())
		return
	}
	values, err := policy.ParseValues(typ, body.Values)
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if body.TTL != nil && *body.TTL < 0 {
		writeError(w, r, http.StatusUnprocessableEntity, fmt.Sprintf("%v: ttl %d is negative", records.ErrInvalid, *body.TTL))
		return
	}

	after, ok := h.update(w, r, func(d *records.Draft) error {
		if err := takeOver(d, name, force); err != nil {
			return err
		}
		d.DeleteTXTOwnedBy(name)
		d.Set(name, typ, values, records.SourceAPI)
		return nil
	})
	if !ok {
		return
	}
	history.SetSummary(r, fmt.Sprintf("set %s %s", name, typ))
	for _, rec := range after.Records {
		if rec.Name == name && rec.Type == typ {
			writeJSON(w, http.StatusOK, toJSON(rec))
			return
		}
	}
	writeError(w, r, http.StatusInternalServerError, "record missing after update")
}

// delete removes a name's records, or only one type with ?type=, and drops
// every TXT ownership record naming it. An absent name or type is a 204 like
// any other.
func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	if !onlyQuery(w, r, "force", "type") {
		return
	}
	force, err := boolQuery(r, "force")
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	typ, status, err := typeQuery(r)
	if err != nil {
		writeError(w, r, status, err.Error())
		return
	}
	name, err := h.store.Policy().NormalizeName(r.PathValue("name"))
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, err.Error())
		return
	}

	_, ok := h.update(w, r, func(d *records.Draft) error {
		if err := takeOver(d, name, force); err != nil {
			return err
		}
		d.DeleteTXTOwnedBy(name)
		if typ == "" {
			d.DeleteName(name)
			return nil
		}
		d.Delete(name, typ)
		for _, rec := range d.State().Records {
			if rec.Name == name {
				d.Set(name, rec.Type, rec.Values, records.SourceAPI)
			}
		}
		return nil
	})
	if !ok {
		return
	}
	summary := "delete " + name
	if typ != "" {
		summary += " " + string(typ)
	}
	history.SetSummary(r, summary)
	w.WriteHeader(http.StatusNoContent)
}

// takeOver refuses a change to an external-dns name unless force is set.
// Callers then drop the TXT ownership records of the name on every write,
// forced or not, so that external-dns never sees a TXT claiming a name the
// api holds or has just removed.
func takeOver(d *records.Draft, name string, force bool) error {
	src, ok := d.Source(name)
	if ok && src == records.SourceExternalDNS && !force {
		return fmt.Errorf("%w: %s is managed by external-dns; add ?force=true to take it over", records.ErrConflict, name)
	}
	return nil
}

// update applies fn through the store and logs the change. On failure it
// writes the error response and returns false.
func (h *handler) update(w http.ResponseWriter, r *http.Request, fn func(*records.Draft) error) (records.State, bool) {
	before, after, err := h.store.Update(fn)
	switch {
	case err == nil:
		records.LogChanges(h.logger, listener, history.RemoteIP(r), before, after)
		return after, true
	case errors.Is(err, records.ErrConflict):
		writeError(w, r, http.StatusConflict, err.Error())
	case errors.Is(err, records.ErrInvalid):
		writeError(w, r, http.StatusUnprocessableEntity, err.Error())
	default:
		h.logger.Error("saving records failed", slog.String("listener", listener), slog.String("error", err.Error()))
		writeError(w, r, http.StatusInternalServerError, "could not write the hosts file: "+err.Error())
	}
	return records.State{}, false
}

// onlyQuery refuses with a 400 a request carrying a query parameter outside
// defined, naming the first such parameter in name order, and returns false.
// Names are case-sensitive, so a misspelling such as ?Type=AAAA is refused
// rather than ignored, which on DELETE would remove every type of the name.
func onlyQuery(w http.ResponseWriter, r *http.Request, defined ...string) bool {
	var unknown []string
	for key := range r.URL.Query() {
		if !slices.Contains(defined, key) {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return true
	}
	slices.Sort(unknown)
	writeError(w, r, http.StatusBadRequest, fmt.Sprintf("unknown query parameter %q", unknown[0]))
	return false
}

// boolQuery parses the optional query parameter key, which must appear at
// most once and be exactly "true" or "false".
func boolQuery(r *http.Request, key string) (bool, error) {
	v, ok, err := singleQuery(r, key)
	if err != nil || !ok {
		return false, err
	}
	switch v {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("query parameter %s=%q must be true or false", key, v)
	}
}

// typeQuery parses the optional type query parameter. It returns "" when it
// is absent. An unsupported record type such as CNAME is a 422; any other
// value that is not A or AAAA is a 400.
func typeQuery(r *http.Request) (records.Type, int, error) {
	v, ok, err := singleQuery(r, "type")
	if err != nil {
		return "", http.StatusBadRequest, err
	}
	if !ok {
		return "", 0, nil
	}
	t, err := records.ParseType(v)
	if err == nil {
		return t, 0, nil
	}
	if typeWord.MatchString(v) {
		return "", http.StatusUnprocessableEntity, err
	}
	return "", http.StatusBadRequest, fmt.Errorf("query parameter type=%q must be A or AAAA", v)
}

// singleQuery returns the one value of query parameter key and whether it
// was given. Giving it more than once is an error.
func singleQuery(r *http.Request, key string) (string, bool, error) {
	values, ok := r.URL.Query()[key]
	if !ok {
		return "", false, nil
	}
	if len(values) != 1 {
		return "", false, fmt.Errorf("query parameter %s given %d times", key, len(values))
	}
	return values[0], true, nil
}

// decode reads exactly one JSON object from the body into v, refusing
// unknown fields and trailing data. On failure it returns the status to send:
// 413 for a body over maxBodyBytes, else 400.
func decode(w http.ResponseWriter, r *http.Request, v any) (int, error) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil {
		var extra json.RawMessage
		if err = dec.Decode(&extra); errors.Is(err, io.EOF) {
			return 0, nil
		} else if err == nil {
			err = errors.New("unexpected data after the JSON object")
		}
	}
	if maxErr, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return http.StatusRequestEntityTooLarge, fmt.Errorf("request body is over %d bytes", maxErr.Limit)
	}
	if errors.Is(err, io.EOF) {
		err = errors.New("empty request body")
	}
	return http.StatusBadRequest, fmt.Errorf("malformed JSON: %w", err)
}

// writeError writes {"error":msg} with status and makes msg the request's
// history summary.
func writeError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	history.SetSummary(r, msg)
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{msg})
}

// writeJSON writes v as a JSON body with status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, `{"error":"encoding the response failed"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
