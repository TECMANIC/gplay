package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TECMANIC/gplay/internal/auth"
	"github.com/TECMANIC/gplay/internal/clierror"
	"github.com/TECMANIC/gplay/internal/testkey"
)

func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	unset(t, "GPLAY_CREDENTIALS")
	unset(t, "GPLAY_CREDENTIALS_JSON")
}

func unset(t *testing.T, key string) {
	t.Helper()
	prev, ok := os.LookupEnv(key)
	os.Unsetenv(key)
	t.Cleanup(func() {
		if ok {
			os.Setenv(key, prev)
		} else {
			os.Unsetenv(key)
		}
	})
}

func writeKey(t *testing.T, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "key.json")
	if err := os.WriteFile(path, testkey.ServiceAccountJSON("keyid-SECRET-12345"), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseRejectsExternalAccountWithoutEchoingIt(t *testing.T) {
	raw := []byte(`{"type":"external_account","token_uri":"https://evil.example/MARKER123","credential_source":{"executable":{"command":"steal"}}}`)
	_, err := auth.ParseServiceAccount(raw)
	if err == nil || err.ExitCode != 3 || err.Code != clierror.CodeAuthFailed {
		t.Fatalf("%v", err)
	}
	if strings.Contains(err.Error(), "MARKER123") || strings.Contains(err.Error(), "steal") || strings.Contains(err.Hint, "MARKER123") {
		t.Fatalf("echoed credential material: %+v", err)
	}
	if !strings.Contains(err.Message, "external_account") {
		t.Fatalf("message %s", err.Message)
	}
}

func TestParseRejectsBadTokenURIWithoutEcho(t *testing.T) {
	raw := []byte(`{"type":"service_account","token_uri":"https://evil.example/MARKER123","client_email":"a@b.c","private_key":"k","project_id":"p"}`)
	_, err := auth.ParseServiceAccount(raw)
	if err == nil || err.ExitCode != 3 {
		t.Fatalf("%v", err)
	}
	if strings.Contains(err.Message+err.Hint, "MARKER123") {
		t.Fatalf("echoed token uri: %+v", err)
	}
}

func TestParseRejectsInvalidJSONWithoutSnippet(t *testing.T) {
	raw := []byte(`{"type": NOT JSON -----BEGIN PRIVATE KEY----- ya29.supersecrettoken`)
	_, err := auth.ParseServiceAccount(raw)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Message+err.Hint, "PRIVATE KEY") || strings.Contains(err.Message, "ya29.") {
		t.Fatalf("parse error echoed input: %+v", err)
	}
}

func TestNoFallthrough(t *testing.T) {
	isolate(t)
	good := writeKey(t, 0o600)
	t.Setenv("GPLAY_CREDENTIALS", good)
	_, err := auth.Resolve("/no/such/key.json", true)
	if err == nil || err.ExitCode != 3 {
		t.Fatalf("flag should win and fail, got %v", err)
	}
}

func TestEmptyEnvDoesNotFallThrough(t *testing.T) {
	isolate(t)
	t.Setenv("GPLAY_CREDENTIALS", "")
	t.Setenv("GPLAY_CREDENTIALS_JSON", string(testkey.ServiceAccountJSON("keyid-SECRET-12345")))
	_, err := auth.Resolve("", false)
	if err == nil || err.ExitCode != 3 {
		t.Fatalf("%v", err)
	}
	if strings.Contains(err.Message+err.Hint, "ya29.") || strings.Contains(err.Message, "PRIVATE KEY") {
		t.Fatalf("leaked: %+v", err)
	}
}

func TestJSONEnvSizeCap(t *testing.T) {
	isolate(t)
	huge := "MARKER123" + strings.Repeat("a", auth.MaxCredentialJSON)
	t.Setenv("GPLAY_CREDENTIALS_JSON", huge)
	_, err := auth.Resolve("", false)
	if err == nil || err.ExitCode != 3 {
		t.Fatalf("%v", err)
	}
	if strings.Contains(err.Message+err.Hint, "MARKER123") {
		t.Fatalf("leaked payload: %+v", err)
	}
}

func TestADCOptIn(t *testing.T) {
	isolate(t)
	res, err := auth.Resolve("adc", true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.ADC || res.Source != auth.SourceADC || res.Account.PrivateKey != "" {
		t.Fatalf("%+v", res)
	}
}

func TestProfileRejectsForeignTool(t *testing.T) {
	isolate(t)
	path, err := auth.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"tool":"other/gplay","credentials":"/tmp/x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, got := auth.Resolve("", false)
	if got == nil || got.Code != clierror.CodeConfigInvalid || got.ExitCode != 2 {
		t.Fatalf("%v", got)
	}
}

func TestProfileRejectsUnknownKey(t *testing.T) {
	isolate(t)
	path, _ := auth.ConfigPath()
	os.MkdirAll(filepath.Dir(path), 0o700)
	key := writeKey(t, 0o600)
	body := `{"tool":"TECMANIC/gplay","credentials":"` + key + `","extra":true}`
	os.WriteFile(path, []byte(body), 0o600)
	_, got := auth.Resolve("", false)
	if got == nil || got.ExitCode != 2 {
		t.Fatalf("%v", got)
	}
}

func TestLoginStoresAbsolutePath(t *testing.T) {
	isolate(t)
	key := writeKey(t, 0o600)
	saved, err := auth.SaveLogin(key)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(saved) {
		t.Fatalf("saved %s", saved)
	}
	res, err := auth.Resolve("", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != auth.SourceProfile || res.Account.ClientEmail == "" {
		t.Fatalf("%+v", res)
	}
	if res.Account.Fingerprint() != "" {
		t.Fatalf("unparsable fixture fingerprint %s", res.Account.Fingerprint())
	}
	st, _ := os.Stat(mustConfig(t))
	if st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("config mode %o", st.Mode().Perm())
	}
}

func mustConfig(t *testing.T) string {
	t.Helper()
	p, err := auth.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFingerprintIsPKIXPublicKey(t *testing.T) {
	first := rsaPEMAccount(t)
	second := rsaPEMAccount(t)
	if first.Fingerprint() == "" || first.Fingerprint() == second.Fingerprint() {
		t.Fatalf("fingerprints %q %q", first.Fingerprint(), second.Fingerprint())
	}
	if !strings.HasPrefix(first.Fingerprint(), "sha256:") || strings.Contains(first.Fingerprint(), "PRIVATE") {
		t.Fatalf("%s", first.Fingerprint())
	}
	block, _ := pem.Decode([]byte(first.PrivateKey))
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.(*rsa.PrivateKey).PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	if first.Fingerprint() != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("got %s", first.Fingerprint())
	}
	privSum := sha256.Sum256(block.Bytes)
	if first.Fingerprint() == "sha256:"+hex.EncodeToString(privSum[:]) {
		t.Fatal("fingerprint hashed the private key")
	}
	if (auth.Account{PrivateKey: "not a key", ClientEmail: "a@b.c"}).Fingerprint() != "" {
		t.Fatal("unparsable PEM produced a fingerprint")
	}
}

func rsaPEMAccount(t *testing.T) auth.Account {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	raw, err := json.Marshal(map[string]string{
		"type":         "service_account",
		"token_uri":    "https://oauth2.googleapis.com/token",
		"client_email": "app@proj.iam.gserviceaccount.com",
		"private_key":  pemKey,
		"project_id":   "proj",
	})
	if err != nil {
		t.Fatal(err)
	}
	acc, aerr := auth.ParseServiceAccount(raw)
	if aerr != nil {
		t.Fatal(aerr)
	}
	return acc
}
