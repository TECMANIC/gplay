// Package redact removes credential material from strings that leave the process.
package redact

import (
	"regexp"
	"strings"
)

var (
	pemPattern = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*-----.*?-----END [A-Z0-9 ]*-----`)
	ya29       = regexp.MustCompile(`ya29\.[A-Za-z0-9_\-.]+`)
	jsonSecret = regexp.MustCompile(`(?i)"(private_key|private_key_id|client_secret|refresh_token|access_token|id_token)"\s*:\s*"(?:\\.|[^"\\])*"`)
	authHeader = regexp.MustCompile(`(?i)(authorization:\s*)(bearer\s+)?\S+`)
)

// Scrub returns s with private keys, bearer tokens, and known secret
// JSON fields replaced. It is safe to call on ordinary error text.
func Scrub(s string) string {
	if s == "" {
		return s
	}
	s = pemPattern.ReplaceAllString(s, "[redacted]")
	s = ya29.ReplaceAllString(s, "[redacted]")
	s = jsonSecret.ReplaceAllString(s, `"$1":"[redacted]"`)
	s = authHeader.ReplaceAllString(s, "${1}[redacted]")
	s = strings.ReplaceAll(s, "PRIVATE KEY", "[redacted]")
	return s
}
