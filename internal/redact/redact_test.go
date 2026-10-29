package redact

import (
	"strings"
	"testing"
)

func TestScrub(t *testing.T) {
	in := `-----BEGIN PRIVATE KEY-----
ya29.supersecret
-----END PRIVATE KEY-----
authorization: Bearer ya29.other
"private_key_id":"keyid-SECRET-12345"`
	out := Scrub(in)
	if strings.Contains(out, "PRIVATE KEY") || strings.Contains(out, "ya29.") || strings.Contains(out, "keyid-SECRET-12345") {
		t.Fatalf("leaked: %s", out)
	}
}
