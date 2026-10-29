package play

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/auth"

	"github.com/TECMANIC/gplay/internal/clierror"
	"github.com/TECMANIC/gplay/internal/transport"
)

func TestADCExternalAccountURLExchange(t *testing.T) {
	for _, host := range []string{"actions.githubusercontent.com", "pipelines.actions.githubusercontent.com"} {
		t.Run(host, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			var mu sync.Mutex
			var sawSTS, sawImp, sawSubject bool
			var apiAuth string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/token"):
					mu.Lock()
					sawSTS = true
					mu.Unlock()
					_, _ = io.WriteString(w, `{"access_token":"sts-token","expires_in":3600,"token_type":"Bearer"}`)
				case strings.Contains(r.URL.Path, "generateAccessToken"):
					mu.Lock()
					sawImp = true
					mu.Unlock()
					if r.Header.Get("Authorization") != "Bearer sts-token" {
						t.Errorf("impersonation auth %q", r.Header.Get("Authorization"))
					}
					_, _ = io.WriteString(w, `{"accessToken":"wif-access","expireTime":"`+time.Now().Add(time.Hour).UTC().Format(time.RFC3339)+`"}`)
				default:
					mu.Lock()
					apiAuth = r.Header.Get("Authorization")
					mu.Unlock()
					_, _ = io.WriteString(w, `{"apps":[]}`)
				}
			}))
			t.Cleanup(srv.Close)
			base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Hostname() == host {
					mu.Lock()
					sawSubject = true
					mu.Unlock()
					return &http.Response{
						StatusCode: http.StatusOK,
						Status:     "200 OK",
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader("header.payload.sig")),
						Request:    r,
					}, nil
				}
				return http.DefaultTransport.RoundTrip(r)
			})
			writeADC(t, map[string]any{
				"type":                              "external_account",
				"audience":                          "//iam.googleapis.com/projects/123/locations/global/workloadIdentityPools/pool/providers/gh",
				"subject_token_type":                "urn:ietf:params:oauth:token-type:jwt",
				"token_url":                         srv.URL + "/token",
				"service_account_impersonation_url": srv.URL + "/v1/projects/-/serviceAccounts/app@proj.iam.gserviceaccount.com:generateAccessToken",
				"credential_source":                 map[string]string{"url": "https://" + host + "/oidc"},
			})
			client, err := New(context.Background(), Options{ADC: true, Endpoint: srv.URL, Base: base})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.SearchApps(context.Background(), 1); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !sawSubject || !sawSTS || !sawImp || apiAuth != "Bearer wif-access" {
				t.Fatalf("subject %v sts %v impersonate %v api %q", sawSubject, sawSTS, sawImp, apiAuth)
			}
		})
	}
}

func TestADCSubjectTokenURLRejected(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://evil.example/token", "not allowlisted"},
		{"https://actions.githubusercontent.com.evil.com/token", "not allowlisted"},
		{"https://notactions.githubusercontent.com/token", "not allowlisted"},
		{"http://pipelines.actions.githubusercontent.com/token", "must be https"},
		{"https://user:pass@pipelines.actions.githubusercontent.com/token", "must be https"},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			writeADC(t, map[string]any{
				"type":              "external_account",
				"credential_source": map[string]string{"url": tc.url},
			})
			_, err := New(context.Background(), Options{ADC: true})
			var ce *clierror.Error
			if !errors.As(err, &ce) || ce == nil || ce.ExitCode != 3 {
				t.Fatalf("%v", err)
			}
			blob := ce.Message + "\n" + ce.Hint
			if !strings.Contains(blob, tc.want) || strings.Contains(blob, tc.url) {
				t.Fatalf("%+v", ce)
			}
		})
	}
}

func TestActionsHostRefusedWithoutADC(t *testing.T) {
	pemKey := rsaPEM(t)
	called := false
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		return nil, fmt.Errorf("base called for %s", r.URL.Host)
	})
	raw := serviceAccountJSON(t, pemKey, "https://pipelines.actions.githubusercontent.com/token")
	client, err := New(context.Background(), Options{JSON: raw, Endpoint: "http://127.0.0.1:9", Base: base})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.SearchApps(context.Background(), 1)
	assertAuthClean(t, err, "pipelines.actions.githubusercontent.com", "actions.githubusercontent.com", pemKey)
	var ce *clierror.Error
	if !errors.As(err, &ce) || !strings.Contains(ce.Message, "not allowlisted") {
		t.Fatalf("%+v", ce)
	}
	if called {
		t.Fatal("refused host reached the transport")
	}
}

func TestCanceledTokenIsInterrupted(t *testing.T) {
	pemKey := rsaPEM(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(entered)
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-time.After(2 * time.Second):
			return nil, fmt.Errorf("token request context was not canceled")
		}
	})
	raw := serviceAccountJSON(t, pemKey, "https://oauth2.googleapis.com/token")
	client, err := New(context.Background(), Options{JSON: raw, Endpoint: "http://127.0.0.1:9", Base: base})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		<-entered
		cancel()
	}()
	_, err = client.SearchApps(ctx, 1)
	var ce *clierror.Error
	if !errors.As(err, &ce) || ce == nil || ce.ExitCode != 130 || ce.Code != clierror.CodeInterrupted {
		t.Fatalf("%v", err)
	}
	if strings.Contains(ce.Message, pemKey) || strings.Contains(ce.Message, "oauth2.googleapis.com") {
		t.Fatalf("%+v", ce)
	}
}

func TestConnectionRefusedTokenIsRetryable(t *testing.T) {
	pemKey := rsaPEM(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("api called after a refused token endpoint")
	}))
	t.Cleanup(api.Close)
	raw := serviceAccountJSON(t, pemKey, "http://"+addr+"/token")
	err = callWithJSON(t, raw, api.URL)
	var ce *clierror.Error
	if !errors.As(err, &ce) || ce == nil || ce.ExitCode != 1 || !ce.Retryable || ce.Message != "connection failed" {
		t.Fatalf("%+v", err)
	}
	if strings.Contains(ce.Message+ce.Hint, addr) || strings.Contains(ce.Message, pemKey) {
		t.Fatalf("%+v", ce)
	}
}

func TestTokenEndpoint5xxIsRetryable(t *testing.T) {
	pemKey := rsaPEM(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "token") {
			t.Errorf("api call after a token 500: %s", r.URL.Path)
		}
		http.Error(w, "SECRET-TOKEN-BODY", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	raw := serviceAccountJSON(t, pemKey, srv.URL+"/token")
	err := callWithJSON(t, raw, srv.URL)
	var ce *clierror.Error
	if !errors.As(err, &ce) || ce == nil || ce.ExitCode != 1 || !ce.Retryable || ce.Message != "token endpoint failed" {
		t.Fatalf("%+v", err)
	}
	if strings.Contains(ce.Message+ce.Hint, "SECRET-TOKEN-BODY") || strings.Contains(ce.Message+ce.Hint, srv.URL) {
		t.Fatalf("%+v", ce)
	}
}

func TestOAuthCodeInAuthMessage(t *testing.T) {
	for _, code := range []string{"invalid_grant", "invalid_client", "unauthorized_client"} {
		body := []byte(`{"error":"` + code + `","error_description":"LEAKED-DESC"}`)
		err := MapAPI(transport.Rejected(&auth.Error{
			Response: &http.Response{StatusCode: http.StatusBadRequest},
			Body:     body,
		}))
		var ce *clierror.Error
		if !errors.As(err, &ce) || ce.ExitCode != 3 || ce.Message != "credentials were rejected: "+code {
			t.Fatalf("%s: %+v", code, err)
		}
		if strings.Contains(ce.Message+ce.Hint, "LEAKED-DESC") {
			t.Fatalf("%+v", ce)
		}
	}
}

func rsaPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func writeADC(t *testing.T, doc map[string]any) {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "adc.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
