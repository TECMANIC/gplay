package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TECMANIC/gplay/internal/cli"
	"github.com/TECMANIC/gplay/internal/fakeserver"
	"github.com/TECMANIC/gplay/internal/testkey"
)

func isolateEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	for _, key := range []string{"GPLAY_CREDENTIALS", "GPLAY_CREDENTIALS_JSON"} {
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
	return dir
}

func writeKey(t *testing.T, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "key.json")
	if err := os.WriteFile(path, testkey.ServiceAccountJSON("keyid-SECRET-12345"), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func runStreams(t *testing.T, streams cli.IO, args ...string) (string, string, int) {
	t.Helper()
	var out, err strings.Builder
	if streams.Stdout == nil {
		streams.Stdout = &out
	}
	if streams.Stderr == nil {
		streams.Stderr = &err
	}
	code := cli.Execute(t.Context(), args, streams)
	stdout := ""
	stderr := ""
	if w, ok := streams.Stdout.(*strings.Builder); ok {
		stdout = w.String()
	}
	if w, ok := streams.Stderr.(*strings.Builder); ok {
		stderr = w.String()
	}
	return stdout, stderr, code
}

func TestAuthStatusAndRejection(t *testing.T) {
	isolateEnv(t)
	key := writeKey(t, 0o600)

	stdout, stderr, code := runStreams(t, cli.IO{}, "auth", "status", "--credentials", key)
	if code != 0 {
		t.Fatalf("status %d %s", code, stderr)
	}
	if strings.Contains(stdout+stderr, "PRIVATE KEY") || strings.Contains(stdout+stderr, "ya29.") || strings.Contains(stdout+stderr, "keyid-SECRET-12345") {
		t.Fatalf("leaked\n%s\n%s", stdout, stderr)
	}
	var doc struct {
		Source      string `json:"source"`
		ClientEmail string `json:"client_email"`
		Validated   bool   `json:"validated"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Source != "flag" || doc.ClientEmail == "" || doc.Validated {
		t.Fatalf("%+v", doc)
	}

	ext := filepath.Join(t.TempDir(), "ext.json")
	os.WriteFile(ext, []byte(`{"type":"external_account","token_uri":"https://oauth2.googleapis.com/token"}`), 0o600)
	stdout, stderr, code = runStreams(t, cli.IO{}, "auth", "status", "--credentials", ext)
	if code != 3 || stdout != "" {
		t.Fatalf("code %d stdout %q stderr %s", code, stdout, stderr)
	}
	assertOneJSON(t, stderr, 3, "auth_failed")
	if strings.Contains(stderr, "PRIVATE KEY") {
		t.Fatal(stderr)
	}
}

func TestAuthStatusValidateAndDoctor(t *testing.T) {
	isolateEnv(t)
	key := writeKey(t, 0o644)
	srv := fakeserver.New()
	t.Cleanup(srv.Close)
	srv.Date = time.Now().Add(-2 * time.Hour)
	streams := cli.IO{Endpoint: srv.URL(), StaticToken: "ya29.supersecrettoken"}

	stdout, stderr, code := runStreams(t, streams, "--debug", "--credentials", key, "auth", "status", "--validate")
	if code != 0 {
		t.Fatalf("validate %d %s", code, stderr)
	}
	if strings.Contains(stdout+stderr, "ya29.") || strings.Contains(stdout+stderr, "PRIVATE KEY") || strings.Contains(stdout+stderr, "keyid-SECRET-12345") {
		t.Fatalf("leaked\nstdout %s\nstderr %s", stdout, stderr)
	}
	if !strings.Contains(stderr, "warning: credential file is readable") {
		t.Fatalf("missing perm warning: %s", stderr)
	}
	if !strings.Contains(stderr, "debug: GET") || strings.Contains(stderr, "Authorization") {
		t.Fatalf("debug log: %s", stderr)
	}
	var status struct {
		Validated bool `json:"validated"`
	}
	if err := json.Unmarshal([]byte(stdout), &status); err != nil || !status.Validated {
		t.Fatalf("%s %v", stdout, err)
	}

	stdout, stderr, code = runStreams(t, streams, "--debug", "--credentials", key, "auth", "doctor", "--package", "com.example")
	if code != 0 {
		t.Fatalf("doctor %d %s", code, stderr)
	}
	if strings.Contains(stdout+stderr, "ya29.") || strings.Contains(stdout+stderr, "keyid-SECRET-12345") || strings.Contains(stdout+stderr, "PRIVATE KEY") {
		t.Fatalf("leaked\n%s\n%s", stdout, stderr)
	}
	var doctor struct {
		Findings []struct {
			Message string `json:"message"`
		} `json:"findings"`
		Probes []struct {
			Name string `json:"name"`
			OK   bool   `json:"ok"`
		} `json:"probes"`
	}
	if err := json.Unmarshal([]byte(stdout), &doctor); err != nil {
		t.Fatal(err)
	}
	if len(doctor.Probes) != 2 || !doctor.Probes[0].OK || !doctor.Probes[1].OK {
		t.Fatalf("probes %+v", doctor.Probes)
	}
	foundSkew := false
	for _, f := range doctor.Findings {
		if strings.Contains(f.Message, "clock skew") {
			foundSkew = true
		}
	}
	if !foundSkew {
		t.Fatalf("findings %+v", doctor.Findings)
	}
	var sawAuth, sawInsert, sawDelete bool
	for _, h := range srv.Hits() {
		if !strings.HasPrefix(h.Auth, "Bearer ") {
			t.Fatalf("missing auth on %+v", h)
		}
		if strings.Contains(h.Path, "apps:search") {
			sawAuth = true
		}
		if h.Method == "POST" && strings.HasSuffix(h.Path, "/edits") {
			sawInsert = true
		}
		if h.Method == "DELETE" && strings.Contains(h.Path, "/edits/") {
			sawDelete = true
		}
	}
	if !sawAuth || !sawInsert || !sawDelete {
		t.Fatalf("hits %+v", srv.Hits())
	}
}

func TestDoctorSkipsEditWithoutPackage(t *testing.T) {
	isolateEnv(t)
	key := writeKey(t, 0o600)
	srv := fakeserver.New()
	t.Cleanup(srv.Close)
	stdout, stderr, code := runStreams(t, cli.IO{Endpoint: srv.URL(), StaticToken: "ya29.supersecrettoken"}, "--credentials", key, "auth", "doctor")
	if code != 0 {
		t.Fatalf("%d %s", code, stderr)
	}
	var skipped struct {
		Probes []struct {
			Name    string `json:"name"`
			Skipped bool   `json:"skipped"`
		} `json:"probes"`
	}
	if err := json.Unmarshal([]byte(stdout), &skipped); err != nil {
		t.Fatal(err)
	}
	var editSkipped bool
	for _, p := range skipped.Probes {
		if p.Name == "edit" && p.Skipped {
			editSkipped = true
		}
	}
	if !editSkipped {
		t.Fatalf("%s", stdout)
	}
	for _, h := range srv.Hits() {
		if strings.Contains(h.Path, "/edits") {
			t.Fatalf("edit was opened: %+v", srv.Hits())
		}
	}
}

func TestADCStatusDoesNotReadAFile(t *testing.T) {
	isolateEnv(t)
	stdout, stderr, code := runStreams(t, cli.IO{}, "auth", "status", "--credentials", "adc")
	if code != 0 {
		t.Fatalf("%d %s", code, stderr)
	}
	if !strings.Contains(stdout, `"source": "adc"`) {
		t.Fatalf("%s", stdout)
	}
	if strings.Contains(stdout, "client_email") || strings.Contains(stdout, "private") {
		t.Fatalf("%s", stdout)
	}
}

func assertOneJSON(t *testing.T, stderr string, exit int, code string) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("stderr lines %q", stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatal(err)
	}
	if got["code"] != code || got["exit_code"] != float64(exit) {
		t.Fatalf("%v", got)
	}
}
