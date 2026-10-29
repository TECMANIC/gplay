package play

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TECMANIC/gplay/internal/clierror"
)

func TestUnparsablePrivateKeyIsAuthFailed(t *testing.T) {
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("not-asn1")}))
	raw := serviceAccountJSON(t, pemKey, "https://oauth2.googleapis.com/token")
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("unparsable key dialed")
	}))
	t.Cleanup(srv.Close)
	err := callWithJSON(t, raw, srv.URL)
	assertAuthClean(t, err, srv.URL, pemKey, "not-asn1")
}

func TestInvalidGrantIsAuthFailed(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "token") {
			t.Errorf("api call after a rejected token: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"LEAKED-TOKEN-BODY"}`)
	}))
	t.Cleanup(srv.Close)
	raw := serviceAccountJSON(t, pemKey, srv.URL+"/token")
	err = callWithJSON(t, raw, srv.URL)
	assertAuthClean(t, err, srv.URL, "LEAKED-TOKEN-BODY", "invalid_grant", pemKey)
}

func TestGCEMetadataADCIsRefused(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	_, err := New(context.Background(), Options{ADC: true})
	var ce *clierror.Error
	if !errors.As(err, &ce) || ce == nil || ce.ExitCode != 3 || ce.Code != clierror.CodeAuthFailed {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(ce.Message, "metadata") || !strings.Contains(ce.Hint, "allowlist") {
		t.Fatalf("%+v", ce)
	}
}

func TestGuardHostsOmitADCUnlessSelected(t *testing.T) {
	for _, host := range guardHosts(Options{Endpoint: "http://127.0.0.1:9"}) {
		if host == "sts.googleapis.com" || host == "iamcredentials.googleapis.com" {
			t.Fatalf("service-account client allowlisted %s", host)
		}
	}
	got := map[string]bool{}
	for _, host := range guardHosts(Options{ADC: true, Endpoint: "http://127.0.0.1:9"}) {
		got[host] = true
	}
	if !got["sts.googleapis.com"] || !got["iamcredentials.googleapis.com"] || !got["127.0.0.1"] {
		t.Fatalf("%v", got)
	}
}

func TestADCExternalAccountExchange(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	subject := filepath.Join(t.TempDir(), "subject.jwt")
	if err := os.WriteFile(subject, []byte("header.payload.sig\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var sawSTS, sawImp bool
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
	doc := map[string]any{
		"type":                              "external_account",
		"audience":                          "//iam.googleapis.com/projects/123/locations/global/workloadIdentityPools/pool/providers/gh",
		"subject_token_type":                "urn:ietf:params:oauth:token-type:jwt",
		"token_url":                         srv.URL + "/token",
		"service_account_impersonation_url": srv.URL + "/v1/projects/-/serviceAccounts/app@proj.iam.gserviceaccount.com:generateAccessToken",
		"credential_source":                 map[string]string{"file": subject},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "adc.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
	client, err := New(context.Background(), Options{ADC: true, Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.SearchApps(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !sawSTS || !sawImp || apiAuth != "Bearer wif-access" {
		t.Fatalf("sts %v impersonate %v api %q", sawSTS, sawImp, apiAuth)
	}
}

func serviceAccountJSON(t *testing.T, privateKey, tokenURI string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]string{
		"type":           "service_account",
		"project_id":     "proj",
		"private_key_id": "kid",
		"private_key":    privateKey,
		"client_email":   "app@proj.iam.gserviceaccount.com",
		"token_uri":      tokenURI,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func callWithJSON(t *testing.T, raw []byte, endpoint string) error {
	t.Helper()
	client, err := New(context.Background(), Options{JSON: raw, Endpoint: endpoint})
	if err != nil {
		return err
	}
	_, err = client.SearchApps(context.Background(), 1)
	return err
}

func assertAuthClean(t *testing.T, err error, forbidden ...string) {
	t.Helper()
	var ce *clierror.Error
	if !errors.As(err, &ce) || ce == nil || ce.ExitCode != 3 || ce.Code != clierror.CodeAuthFailed {
		t.Fatalf("%v", err)
	}
	blob := ce.Message + "\n" + ce.Hint
	if strings.Contains(strings.ToLower(blob), "asn1") || strings.Contains(blob, "http://") || strings.Contains(blob, "https://") {
		t.Fatalf("leaked %s", blob)
	}
	for _, s := range forbidden {
		if s != "" && strings.Contains(blob, s) {
			t.Fatalf("leaked %q in %s", s, blob)
		}
	}
}
