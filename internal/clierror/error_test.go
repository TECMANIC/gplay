package clierror_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/TECMANIC/gplay/internal/clierror"
)

func TestWriteOneJSONLine(t *testing.T) {
	var buf bytes.Buffer
	clierror.Write(&buf, clierror.Usage("nope", "run gplay --help"))
	raw := buf.Bytes()
	if bytes.Count(raw, []byte("\n")) != 1 {
		t.Fatalf("want one line, got %q", raw)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["code"] != "usage" || got["exit_code"] != float64(2) || got["retryable"] != false {
		t.Fatalf("%v", got)
	}
	if _, ok := got["http_status"]; ok {
		t.Fatalf("optional zero field was present: %v", got)
	}
	if got["hint"] != "run gplay --help" {
		t.Fatalf("hint: %v", got["hint"])
	}
}

func TestFromPassThroughAndNetwork(t *testing.T) {
	orig := clierror.PolicyBlocked("blocked", "add the policy file")
	if clierror.From(orig) != orig {
		t.Fatal("passthrough")
	}
	if clierror.ExitCode(nil) != 0 {
		t.Fatal("nil")
	}
	if got := clierror.From(context.Canceled); got.ExitCode != 130 || got.Code != clierror.CodeInterrupted {
		t.Fatalf("%+v", got)
	}
	if got := clierror.From(context.DeadlineExceeded); got.ExitCode != 1 || !got.Retryable {
		t.Fatalf("%+v", got)
	}
	if got := clierror.From(errors.New("boom")); got.Code != clierror.CodeUnexpected || got.Retryable {
		t.Fatalf("%+v", got)
	}
}

func TestCodesCoverContract(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range clierror.Codes() {
		if c.Code == "" || c.ExitCode == 0 || c.Meaning == "" {
			t.Fatalf("incomplete %+v", c)
		}
		if seen[c.Code] {
			t.Fatalf("duplicate %s", c.Code)
		}
		if c.Code == clierror.CodeUnexpected && c.Retryable {
			t.Fatal("unexpected is not always retryable")
		}
		seen[c.Code] = true
	}
	for _, code := range []string{
		clierror.CodeUsage, clierror.CodeConfigInvalid, clierror.CodePolicyBlocked,
		clierror.CodeChangesInReview, clierror.CodeValidationFailed, clierror.CodeRateLimited,
	} {
		if !seen[code] {
			t.Fatalf("missing %s", code)
		}
	}
}
