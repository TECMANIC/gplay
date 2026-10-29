// Package fakeserver is a Play API stand-in for tests. It never dials out.
package fakeserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Hit is one request the server accepted.
type Hit struct {
	Method   string
	Path     string
	RawQuery string
	Auth     string
}

// Server records requests and answers the routes gplay uses.
type Server struct {
	http *httptest.Server
	mu   sync.Mutex
	hits []Hit
	// Date is sent as the Date header. Zero means the current time.
	Date time.Time
	// Status, when non-zero, replaces the normal route response.
	Status int
	// Body is the replacement body used with Status.
	Body string
}

// New starts a loopback server.
func New() *Server {
	s := &Server{}
	s.http = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// Close shuts the server down.
func (s *Server) Close() { s.http.Close() }

// URL is the server base URL, without a trailing slash.
func (s *Server) URL() string { return s.http.URL }

// Hits returns a copy of the recorded requests.
func (s *Server) Hits() []Hit {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Hit, len(s.hits))
	copy(out, s.hits)
	return out
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.hits = append(s.hits, Hit{
		Method:   r.Method,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
		Auth:     r.Header.Get("Authorization"),
	})
	status, body, date := s.Status, s.Body, s.Date
	s.mu.Unlock()

	if date.IsZero() {
		date = time.Now().UTC()
	}
	w.Header().Set("Date", date.Format(http.TimeFormat))
	w.Header().Set("X-Goog-Request-Id", "req-test")
	if status != 0 {
		if body == "" {
			body = `{"error":{"code":` + strconv.Itoa(status) + `,"message":"forced","status":"UNKNOWN"}}`
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1beta1/apps:search":
		writeJSON(w, http.StatusOK, map[string]any{
			"apps": []map[string]string{{
				"name":        "apps/1",
				"packageName": "com.example",
				"displayName": "Example",
			}},
		})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/edits"):
		writeJSON(w, http.StatusOK, map[string]string{
			"id":                "edit-1",
			"expiryTimeSeconds": "1893456000",
		})
	case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/edits/"):
		w.WriteHeader(http.StatusNoContent)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]any{"code": 404, "message": "not found", "status": "NOT_FOUND"},
		})
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
