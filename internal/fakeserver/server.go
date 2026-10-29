// Package fakeserver is a Play API stand-in for tests. It never dials out.
package fakeserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Hit is one request the server accepted.
type Hit struct {
	Method       string
	Path         string
	RawQuery     string
	Auth         string
	ContentRange string
	UploadType   string
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
	// Paginate makes apps.search return a second page.
	Paginate bool
	// Tracks, when non-nil, replaces the default track catalogue.
	// An empty slice means the app has no tracks.
	Tracks []Track
	// Summaries, when non-nil, replaces release-status payloads, keyed by track.
	Summaries map[string][]Summary
	// BundleSHA256, when set, is returned instead of the hash of the uploaded bytes.
	BundleSHA256 string
	// OmitSHA256 leaves sha256 out of the bundle response.
	OmitSHA256 bool
	// VersionCode is returned from bundle upload. Zero means 42.
	VersionCode int64
	// ValidateStatus, when non-zero, is the status of edits.validate.
	ValidateStatus int
	// ValidateBody is the validate error body. Empty selects a generic error.
	ValidateBody string
	// CommitStatus, when non-zero, is the status of edits.commit.
	CommitStatus int
	// CommitBody is the commit error body.
	CommitBody string

	sessions map[string]*bytes.Buffer
}

// Track is one edits.tracks entry.
type Track struct {
	Track    string    `json:"track"`
	Releases []Release `json:"releases,omitempty"`
}

// Release is one track release. VersionCodes are JSON strings, as Play sends them.
type Release struct {
	Name                string   `json:"name,omitempty"`
	Status              string   `json:"status,omitempty"`
	UserFraction        float64  `json:"userFraction,omitempty"`
	VersionCodes        []string `json:"versionCodes,omitempty"`
	InAppUpdatePriority int64    `json:"inAppUpdatePriority,omitempty"`
}

// Summary is one applications.tracks.releases.list entry.
type Summary struct {
	ReleaseName           string `json:"releaseName,omitempty"`
	ReleaseLifecycleState string `json:"releaseLifecycleState,omitempty"`
	Track                 string `json:"track,omitempty"`
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

// Reset drops recorded requests.
func (s *Server) Reset() {
	s.mu.Lock()
	s.hits = nil
	s.mu.Unlock()
}

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
		Method:       r.Method,
		Path:         r.URL.Path,
		RawQuery:     r.URL.RawQuery,
		Auth:         r.Header.Get("Authorization"),
		ContentRange: r.Header.Get("Content-Range"),
		UploadType:   r.URL.Query().Get("uploadType"),
	})
	status, body, date := s.Status, s.Body, s.Date
	paginate := s.Paginate
	tracks := append([]Track(nil), s.Tracks...)
	tracksSet := s.Tracks != nil
	summaries := s.Summaries
	bundleSHA, omitSHA, versionCode := s.BundleSHA256, s.OmitSHA256, s.VersionCode
	validateStatus, validateBody := s.ValidateStatus, s.ValidateBody
	commitStatus, commitBody := s.CommitStatus, s.CommitBody
	s.mu.Unlock()
	if !tracksSet {
		tracks = defaultTracks()
	}

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
	path := unescape(r.URL.Path)
	switch {
	case r.Method == http.MethodGet && path == "/v1beta1/apps:search":
		s.serveApps(w, r, paginate)
	case r.Method == http.MethodGet && strings.Contains(path, "/tracks/") && strings.HasSuffix(path, "/releases") && !strings.Contains(path, "/edits/"):
		s.serveSummaries(w, path, tracks, summaries)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/tracks") && strings.Contains(path, "/edits/"):
		writeJSON(w, http.StatusOK, map[string]any{"tracks": tracks})
	case r.Method == http.MethodGet && strings.Contains(path, "/edits/") && strings.Contains(path, "/tracks/"):
		s.serveTrack(w, path, tracks)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/edits"):
		writeJSON(w, http.StatusOK, map[string]string{
			"id":                "edit-1",
			"expiryTimeSeconds": "1893456000",
		})
	case r.Method == http.MethodPost && (strings.Contains(path, "/upload/") || strings.HasPrefix(path, "/upload/")):
		s.serveUpload(w, r, path, bundleSHA, omitSHA, versionCode)
	case r.Method == http.MethodPost && strings.Contains(path, ":validate"):
		if validateStatus != 0 {
			writeForced(w, validateStatus, validateBody)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": "edit-1", "expiryTimeSeconds": "1893456000"})
	case r.Method == http.MethodPost && strings.Contains(path, ":commit"):
		if commitStatus != 0 {
			writeForced(w, commitStatus, commitBody)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": "edit-1", "expiryTimeSeconds": "1893456000"})
	case r.Method == http.MethodDelete && strings.Contains(path, "/edits/"):
		w.WriteHeader(http.StatusNoContent)
	default:
		notFound(w)
	}
}

func writeForced(w http.ResponseWriter, status int, body string) {
	if body == "" {
		body = `{"error":{"code":` + strconv.Itoa(status) + `,"message":"forced","status":"UNKNOWN"}}`
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func (s *Server) serveApps(w http.ResponseWriter, r *http.Request, paginate bool) {
	if paginate && r.URL.Query().Get("pageToken") == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"apps":          []map[string]string{{"name": "apps/1", "packageName": "com.example", "displayName": "Example"}},
			"nextPageToken": "p2",
		})
		return
	}
	if r.URL.Query().Get("pageToken") == "p2" {
		writeJSON(w, http.StatusOK, map[string]any{
			"apps": []map[string]string{{"name": "apps/2", "packageName": "com.example.two", "displayName": "Two"}},
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"apps": []map[string]string{{"name": "apps/1", "packageName": "com.example", "displayName": "Example"}},
	})
}

func (s *Server) serveTrack(w http.ResponseWriter, path string, tracks []Track) {
	name := path[strings.LastIndex(path, "/tracks/")+len("/tracks/"):]
	for _, tr := range tracks {
		if tr.Track == name {
			writeJSON(w, http.StatusOK, tr)
			return
		}
	}
	notFound(w)
}

func (s *Server) serveSummaries(w http.ResponseWriter, path string, tracks []Track, summaries map[string][]Summary) {
	rest := strings.TrimSuffix(path, "/releases")
	name := rest[strings.LastIndex(rest, "/tracks/")+len("/tracks/"):]
	if summaries != nil {
		if rows, ok := summaries[name]; ok {
			writeJSON(w, http.StatusOK, map[string]any{"releases": rows})
			return
		}
		notFound(w)
		return
	}
	for _, tr := range tracks {
		if tr.Track == name {
			writeJSON(w, http.StatusOK, map[string]any{"releases": []Summary{{
				ReleaseName:           "1",
				ReleaseLifecycleState: "RELEASE_LIFECYCLE_STATE_PUBLISHED",
				Track:                 name,
			}}})
			return
		}
	}
	notFound(w)
}

func defaultTracks() []Track {
	return []Track{
		{Track: "internal", Releases: []Release{{Name: "1", Status: "completed", VersionCodes: []string{"1"}}}},
		{Track: "beta", Releases: []Release{{Name: "2", Status: "inProgress", UserFraction: 0.2, VersionCodes: []string{"2"}}}},
		{Track: "production", Releases: []Release{{Name: "3", Status: "completed", VersionCodes: []string{"3"}}}},
	}
}

func unescape(path string) string {
	if u, err := url.PathUnescape(path); err == nil {
		return u
	}
	return path
}

func notFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]any{
		"error": map[string]any{"code": 404, "message": "not found", "status": "NOT_FOUND"},
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
