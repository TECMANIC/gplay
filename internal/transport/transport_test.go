package transport

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAllowlistBlocksUnknownHost(t *testing.T) {
	called := false
	g := NewGuard(nil, nil, roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, nil
	}))
	req, _ := http.NewRequest(http.MethodGet, "https://evil.example/secret", nil)
	_, err := g.RoundTrip(req)
	if !errors.Is(err, ErrNotAllowlisted) {
		t.Fatalf("err %v", err)
	}
	if called {
		t.Fatal("base transport was called")
	}
}

func TestDebugOmitsAuthorization(t *testing.T) {
	var log strings.Builder
	g := NewGuard([]string{"example.test"}, &log, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") == "" {
			t.Fatal("missing authorization")
		}
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"X-Goog-Request-Id": []string{"req-1"}},
			Body:       io.NopCloser(strings.NewReader(`{"private_key":"secret"}`)),
			Request:    r,
		}, nil
	}))
	a := Authed{Guard: g, Token: func(*http.Request) (string, error) {
		return "ya29.supersecrettoken", nil
	}}
	req, _ := http.NewRequest(http.MethodGet, "https://example.test/v?changesInReviewBehavior=ERROR_IF_IN_REVIEW", nil)
	resp, err := a.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if strings.Contains(log.String(), "ya29.") || strings.Contains(log.String(), "Authorization") || strings.Contains(log.String(), "private_key") {
		t.Fatalf("log leaked: %s", log.String())
	}
	if !strings.Contains(log.String(), "changesInReviewBehavior=ERROR_IF_IN_REVIEW") {
		t.Fatalf("log %s", log.String())
	}
	if !strings.Contains(log.String(), "request_id=req-1") {
		t.Fatalf("log %s", log.String())
	}
}

func TestGETRetries(t *testing.T) {
	n := 0
	g := NewGuard([]string{"example.test"}, nil, roundTripFunc(func(*http.Request) (*http.Response, error) {
		n++
		code := 500
		if n == 3 {
			code = 200
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader("x")), Header: http.Header{}}, nil
	}))
	g.SetSleep(func(time.Duration) {})
	req, _ := http.NewRequest(http.MethodGet, "https://example.test/read", nil)
	resp, err := g.RoundTrip(req)
	if err != nil || resp.StatusCode != 200 || n != 3 {
		t.Fatalf("status %v n %d err %v", resp, n, err)
	}
}

func TestADCHostsAreOptIn(t *testing.T) {
	for _, raw := range []string{
		"https://sts.googleapis.com/v1/token",
		"https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/a@b.iam.gserviceaccount.com:generateAccessToken",
	} {
		called := false
		g := NewGuard(nil, nil, roundTripFunc(func(*http.Request) (*http.Response, error) {
			called = true
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: http.Header{}}, nil
		}))
		req, _ := http.NewRequest(http.MethodPost, raw, nil)
		if _, err := g.RoundTrip(req); !errors.Is(err, ErrNotAllowlisted) || called {
			t.Fatalf("%s err %v called %v", raw, err, called)
		}
		called = false
		allowed := NewGuard(ADCHosts, nil, roundTripFunc(func(*http.Request) (*http.Response, error) {
			called = true
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: http.Header{}}, nil
		}))
		req, _ = http.NewRequest(http.MethodPost, raw, nil)
		resp, err := allowed.RoundTrip(req)
		if err != nil || !called || resp.StatusCode != 200 {
			t.Fatalf("%s err %v called %v", raw, err, called)
		}
	}
}

func TestHTTPOnAllowlistedHostIsRefusedBeforeDial(t *testing.T) {
	for _, raw := range []string{
		"http://oauth2.googleapis.com/token",
		"http://androidpublisher.googleapis.com/androidpublisher/v3/applications",
	} {
		called := false
		g := NewGuard(nil, nil, roundTripFunc(func(*http.Request) (*http.Response, error) {
			called = true
			return nil, nil
		}))
		req, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = g.RoundTrip(req)
		if !errors.Is(err, ErrInsecureScheme) || called {
			t.Fatalf("%s err %v called %v", raw, err, called)
		}
	}
	called := false
	g := NewGuard(nil, nil, roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	}))
	req, err := http.NewRequest(http.MethodPost, "https://oauth2.googleapis.com/token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.RoundTrip(req); err != nil || !called {
		t.Fatalf("https err %v called %v", err, called)
	}
}

func TestInjectedEndpointMayUseHTTP(t *testing.T) {
	called := false
	g := NewGuard([]string{"127.0.0.1"}, nil, roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	}))
	g.AllowHTTP("127.0.0.1")
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:9/edits", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := g.RoundTrip(req)
	if err != nil || !called || resp.StatusCode != 204 {
		t.Fatalf("err %v called %v", err, called)
	}
}

func TestPOSTIsNotRetried(t *testing.T) {
	n := 0
	g := NewGuard([]string{"example.test"}, nil, roundTripFunc(func(*http.Request) (*http.Response, error) {
		n++
		return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader("x")), Header: http.Header{}}, nil
	}))
	g.SetSleep(func(time.Duration) {})
	req, _ := http.NewRequest(http.MethodPost, "https://example.test/edits/1:commit", nil)
	resp, err := g.RoundTrip(req)
	if err != nil || resp.StatusCode != 500 || n != 1 {
		t.Fatalf("n %d err %v", n, err)
	}
}
