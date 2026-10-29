package redact

import "testing"

func TestScrub(t *testing.T) {
	in := `-----BEGIN PRIVATE KEY-----
ya29.supersecret
-----END PRIVATE KEY-----
authorization: Bearer ya29.other
"private_key_id":"keyid-SECRET-12345"`
	out := Scrub(in)
	if ContainsSecret(out, "keyid-SECRET-12345") {
		t.Fatalf("leaked: %s", out)
	}
}
