// Package history keeps a small in-memory ring of recent HTTP requests for the
// web page. It is lost on restart and never holds bodies, headers or query
// values other than force and type.
package history

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
	"unicode/utf8"
)

// Size is the number of entries the service keeps.
const Size = 50

// maxFieldBytes bounds an entry's Path and Summary, so a request with a huge
// path cannot make every entry, and the web page, as large as the request.
const maxFieldBytes = 256

// Entry is one recorded request.
type Entry struct {
	Time     time.Time
	Listener string
	Method   string
	Path     string
	Status   int
	Remote   string
	Duration time.Duration
	Summary  string
}

// Log is a fixed-size ring of entries, safe for concurrent use. Once full,
// each Add drops the oldest entry.
type Log struct {
	mu      sync.Mutex
	entries []Entry
	next    int
	full    bool
}

// New returns an empty Log that keeps the last size entries. A size below 1
// is treated as 1.
func New(size int) *Log {
	return &Log{entries: make([]Entry, max(size, 1))}
}

// Add records e, dropping the oldest entry when the log is full. A Path or
// Summary over 256 bytes is cut to at most 256 bytes on a UTF-8 boundary and
// "…" is appended.
func (l *Log) Add(e Entry) {
	e.Path = truncate(e.Path)
	e.Summary = truncate(e.Summary)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries[l.next] = e
	l.next = (l.next + 1) % len(l.entries)
	if l.next == 0 {
		l.full = true
	}
}

// truncate returns s unchanged when it fits in maxFieldBytes, else its
// longest prefix of whole runes within maxFieldBytes followed by "…".
func truncate(s string) string {
	if len(s) <= maxFieldBytes {
		return s
	}
	cut := maxFieldBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// Entries returns the recorded entries, newest first. The slice is a copy the
// caller may change freely.
func (l *Log) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := l.next
	if l.full {
		n = len(l.entries)
	}
	out := make([]Entry, n)
	for i := range n {
		out[i] = l.entries[(l.next-1-i+len(l.entries))%len(l.entries)]
	}
	return out
}

type summaryKey struct{}

// summary is the per-request slot SetSummary writes and Middleware reads.
type summary struct {
	mu   sync.Mutex
	text string
}

// SetSummary sets the summary of the entry Middleware will record for r. It
// is a no-op when r did not pass through Middleware.
func SetSummary(r *http.Request, text string) {
	s, ok := r.Context().Value(summaryKey{}).(*summary)
	if !ok {
		return
	}
	s.mu.Lock()
	s.text = text
	s.mu.Unlock()
}

// Middleware records one Entry per request for which keep returns true. keep
// is called after next returns, with the status next wrote, or 200 when it
// never called WriteHeader. The entry's Path is the URL path followed by only
// the force and type query parameters, in that order, when present.
func (l *Log) Middleware(listener string, keep func(r *http.Request, status int) bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		s := &summary{}
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), summaryKey{}, s)))

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		if !keep(r, status) {
			return
		}
		s.mu.Lock()
		text := s.text
		s.mu.Unlock()
		l.Add(Entry{
			Time:     start,
			Listener: listener,
			Method:   r.Method,
			Path:     recordedPath(r.URL),
			Status:   status,
			Remote:   RemoteIP(r),
			Duration: time.Since(start),
			Summary:  text,
		})
	})
}

// RemoteIP returns the IP of r.RemoteAddr, or the whole string if it has no port.
func RemoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// recordedPath returns u's path with only the force and type query
// parameters kept, so tokens or other values never reach the log.
func recordedPath(u *url.URL) string {
	q := u.Query()
	kept := url.Values{}
	for _, name := range []string{"force", "type"} {
		if v, ok := q[name]; ok {
			kept[name] = v
		}
	}
	if len(kept) == 0 {
		return u.Path
	}
	return u.Path + "?" + kept.Encode()
}

// statusRecorder captures the first status code written through it.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}
