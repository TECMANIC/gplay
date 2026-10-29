// Package transport is the only HTTP path gplay uses.
// Token refresh and API calls share one allowlisting RoundTripper.
package transport

import (
	crand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/TECMANIC/gplay/internal/redact"
)

// DefaultHosts are the only Google hosts a service-account client may dial.
var DefaultHosts = []string{
	"androidpublisher.googleapis.com",
	"playdeveloperreporting.googleapis.com",
	"oauth2.googleapis.com",
}

// ADCHosts are the workload-identity hosts added only when ADC is selected.
// A service-account key never needs them.
var ADCHosts = []string{
	"sts.googleapis.com",
	"iamcredentials.googleapis.com",
}

// TokenError is a credential failure. Error is a fixed sentence so a
// token-endpoint URL, a response body, or a private-key parse dump cannot
// leave the process. Unwrap returns the cause for errors.As.
type TokenError struct {
	cause error
}

func (e *TokenError) Error() string { return "credentials were rejected" }

func (e *TokenError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Rejected wraps err as a TokenError. A nil err stays nil. An error that
// already wraps TokenError is returned as-is.
func Rejected(err error) error {
	if err == nil {
		return nil
	}
	var te *TokenError
	if errors.As(err, &te) {
		return err
	}
	return &TokenError{cause: err}
}

// TokenFunc returns a bearer token value, without the "Bearer " prefix.
type TokenFunc func(*http.Request) (string, error)

// Guard is the allowlist, debug log, and GET retry layer.
type Guard struct {
	hosts map[string]struct{}
	debug io.Writer
	base  http.RoundTripper
	sleep func(time.Duration)
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
	for i := 0; i < attempts; i++ {
		if i > 0 {
			wait, jerr := withJitter(backoff(i))
			if jerr != nil {
				return nil, jerr
			}
			g.sleep(wait)
		}
		start := time.Now()
		resp, err = g.base.RoundTrip(req)
		g.log(req, resp, time.Since(start), err)
		if err != nil || resp == nil || !retryable(resp.StatusCode) || i == attempts-1 {
			return resp, err
		}
		_, copyErr := io.Copy(io.Discard, resp.Body)
		closeErr := resp.Body.Close()
		if copyErr != nil && closeErr != nil {
			return nil, fmt.Errorf("%w; closing body: %v", copyErr, closeErr)
		}
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	return resp, err
}

func backoff(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 200 * time.Millisecond
	case 2:
		return 400 * time.Millisecond
	case 3:
		return 800 * time.Millisecond
	default:
		return 0
	}
}

func withJitter(d time.Duration) (time.Duration, error) {
	span := int64(d / 5)
	if span <= 0 {
		return d, nil
	}
	n, err := crand.Int(crand.Reader, big.NewInt(span))
	if err != nil {
		return 0, fmt.Errorf("retry jitter: %w", err)
	}
	return d + time.Duration(n.Int64()), nil
}

func retryable(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
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
			return nil, Rejected(err)
		}
		req = req.Clone(req.Context())
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return a.Guard.RoundTrip(req)
}
