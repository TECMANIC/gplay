// Package transport is the only HTTP path gplay uses.
// Token refresh and API calls share one allowlisting RoundTripper.
package transport

import (
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/TECMANIC/gplay/internal/redact"
)

// DefaultHosts are the only Google hosts a production client may dial.
var DefaultHosts = []string{
	"androidpublisher.googleapis.com",
	"playdeveloperreporting.googleapis.com",
	"oauth2.googleapis.com",
}

// Call is one request that was allowed to leave the process.
type Call struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// TokenFunc returns a bearer token value, without the "Bearer " prefix.
type TokenFunc func(*http.Request) (string, error)

// Guard is the allowlist, debug log, and GET retry layer.
type Guard struct {
	hosts map[string]struct{}
	debug io.Writer
	base  http.RoundTripper
	sleep func(time.Duration)

	mu    sync.Mutex
	calls []Call
}

// NewGuard builds the shared dial path. extra hosts (a test server) are added
// to the allowlist. base nil uses http.DefaultTransport.
func NewGuard(extra []string, debug io.Writer, base http.RoundTripper) *Guard {
	if base == nil {
		base = http.DefaultTransport
	}
	hosts := map[string]struct{}{}
	for _, h := range DefaultHosts {
		hosts[strings.ToLower(h)] = struct{}{}
	}
	for _, h := range extra {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			hosts[h] = struct{}{}
		}
	}
	return &Guard{hosts: hosts, debug: debug, base: base, sleep: time.Sleep}
}

// SetSleep replaces the retry delay. Tests pass a no-op.
func (g *Guard) SetSleep(fn func(time.Duration)) {
	if fn == nil {
		fn = time.Sleep
	}
	g.sleep = fn
}

// Calls returns the requests that passed the allowlist, in order.
func (g *Guard) Calls() []Call {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]Call, len(g.calls))
	copy(out, g.calls)
	return out
}

func (g *Guard) RoundTrip(req *http.Request) (*http.Response, error) {
	host := strings.ToLower(req.URL.Hostname())
	if _, ok := g.hosts[host]; !ok {
		return nil, fmt.Errorf("refusing request to %s: host is not allowlisted", host)
	}
	var resp *http.Response
	var err error
	attempts := 1
	if req.Method == http.MethodGet && req.Body == nil {
		attempts = 4 // one try plus at most three retries
	}
	backoffs := []time.Duration{200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond}
	for i := 0; i < attempts; i++ {
		if i > 0 {
			d := backoffs[i-1]
			jitter := time.Duration(rand.Int64N(int64(d / 5)))
			g.sleep(d + jitter)
		}
		start := time.Now()
		g.record(req)
		resp, err = g.base.RoundTrip(req)
		g.log(req, resp, time.Since(start), err)
		if err != nil || resp == nil || !retryable(resp.StatusCode) || i == attempts-1 {
			return resp, err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	return resp, err
}

func retryable(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

func (g *Guard) record(req *http.Request) {
	g.mu.Lock()
	g.calls = append(g.calls, Call{Method: req.Method, Path: req.URL.Path})
	g.mu.Unlock()
}

func (g *Guard) log(req *http.Request, resp *http.Response, latency time.Duration, err error) {
	if g.debug == nil {
		return
	}
	status := "error"
	reqID, uploadID := "", ""
	if resp != nil {
		status = fmt.Sprintf("%d", resp.StatusCode)
		reqID = resp.Header.Get("x-goog-request-id")
		uploadID = resp.Header.Get("x-guploader-uploadid")
	}
	if err != nil && resp == nil {
		status = "error"
	}
	u := redact.Scrub(req.URL.String())
	fmt.Fprintf(g.debug, "debug: %s %s status=%s latency=%s request_id=%s upload_id=%s\n",
		req.Method, u, status, latency.Truncate(time.Millisecond), redact.Scrub(reqID), redact.Scrub(uploadID))
}

// Authed adds a bearer token and then calls Guard.
type Authed struct {
	Guard *Guard
	Token TokenFunc
}

func (a Authed) RoundTrip(req *http.Request) (*http.Response, error) {
	if a.Token != nil {
		tok, err := a.Token(req)
		if err != nil {
			return nil, fmt.Errorf("authorization token: %s", redact.Scrub(err.Error()))
		}
		req = req.Clone(req.Context())
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return a.Guard.RoundTrip(req)
}
