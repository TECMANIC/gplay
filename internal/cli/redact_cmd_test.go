package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/TECMANIC/gplay/internal/fakeserver"
	"github.com/TECMANIC/gplay/internal/redact"
	"github.com/TECMANIC/gplay/internal/testkey"
)

func TestEveryCommandRedacts(t *testing.T) {
	srv := fakeserver.New()
	t.Cleanup(srv.Close)
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	unset(t, "GPLAY_CREDENTIALS")
	unset(t, "GPLAY_CREDENTIALS_JSON")
	key := filepath.Join(t.TempDir(), "key.json")
	if err := os.WriteFile(key, testkey.ServiceAccountJSON("keyid-SECRET-12345"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := newRoot(IO{})
	var commands []string
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Runnable() {
			commands = append(commands, c.CommandPath())
		}
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(root)
	if len(commands) < 4 {
		t.Fatalf("expected the auth commands, got %v", commands)
	}

	for _, path := range commands {
		args := strings.Fields(path)[1:]
		args = append([]string{"--debug", "--credentials", key}, args...)
		var stdout, stderr bytes.Buffer
		Execute(t.Context(), args, IO{
			Stdout:      &stdout,
			Stderr:      &stderr,
			Endpoint:    srv.URL(),
			StaticToken: "ya29.supersecrettoken",
		})
		blob := stdout.String() + stderr.String()
		if redact.ContainsSecret(blob, "keyid-SECRET-12345") {
			t.Fatalf("%s leaked\n%s", path, blob)
		}
	}
	for _, h := range srv.Hits() {
		if !strings.HasPrefix(h.Auth, "Bearer ya29.") {
			t.Fatalf("server saw %+v", h)
		}
	}
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
