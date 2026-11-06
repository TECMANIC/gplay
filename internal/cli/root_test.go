package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/TECMANIC/gplay/internal/cli"
)

func run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, err bytes.Buffer
	code = cli.Execute(t.Context(), args, cli.IO{Stdout: &out, Stderr: &err})
	return out.String(), err.String(), code
}

func TestVersionPrintsModule(t *testing.T) {
	stdout, stderr, code := run(t, "version")
	if code != 0 {
		t.Fatalf("exit %d stderr %s", code, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr %q", stderr)
	}
	want := "gplay (github.com/TECMANIC/gplay) "
	if !strings.HasPrefix(stdout, want) {
		t.Fatalf("stdout %q", stdout)
	}
	if strings.Count(stdout, "\n") != 1 {
		t.Fatalf("stdout %q", stdout)
	}
}

func TestUnknownCommandOneJSONLine(t *testing.T) {
	stdout, stderr, code := run(t, "nope")
	if code != 2 {
		t.Fatalf("exit %d", code)
	}
	if stdout != "" {
		t.Fatalf("stdout should be empty, got %q", stdout)
	}
	lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("want one stderr line, got %q", stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatal(err)
	}
	if got["code"] != "usage" || got["exit_code"] != float64(2) || got["retryable"] != false {
		t.Fatalf("%v", got)
	}
	if _, ok := got["message"].(string); !ok || got["message"] == "" {
		t.Fatalf("message: %v", got)
	}
}

func TestSchemaToolAndExitCodes(t *testing.T) {
	stdout, stderr, code := run(t, "schema")
	if code != 0 {
		t.Fatalf("exit %d %s", code, stderr)
	}
	var doc struct {
		Tool     string `json:"tool"`
		Module   string `json:"module"`
		Commands []struct {
			Name string `json:"name"`
		} `json:"commands"`
		ExitCodes []struct {
			Code     string `json:"code"`
			ExitCode int    `json:"exit_code"`
		} `json:"exit_codes"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Tool != "TECMANIC/gplay" || doc.Module != "github.com/TECMANIC/gplay" {
		t.Fatalf("%+v", doc)
	}
	names := map[string]bool{}
	for _, c := range doc.Commands {
		names[c.Name] = true
	}
	if !names["version"] || !names["schema"] {
		t.Fatalf("commands %+v", doc.Commands)
	}
	foundUsage := false
	for _, c := range doc.ExitCodes {
		if c.Code == "usage" && c.ExitCode == 2 {
			foundUsage = true
		}
	}
	if !foundUsage {
		t.Fatal("schema missing usage exit 2")
	}
}

func TestSchemaUnknown(t *testing.T) {
	stdout, _, code := run(t, "schema", "nope")
	if code != 2 || stdout != "" {
		t.Fatalf("code %d stdout %q", code, stdout)
	}
}

func TestBadOutputFlag(t *testing.T) {
	stdout, stderr, code := run(t, "--output", "yaml", "version")
	if code != 2 || stdout != "" {
		t.Fatalf("code %d stdout %q stderr %q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, `"code":"usage"`) || !strings.Contains(stderr, `"exit_code":2`) {
		t.Fatalf("stderr %s", stderr)
	}
}
