package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/TECMANIC/gplay/internal/fakeserver"
	"github.com/TECMANIC/gplay/internal/testkey"
)

func TestEveryCommandRedacts(t *testing.T) {
	srv := fakeserver.New()
	t.Cleanup(srv.Close)
	srv.Tracks = []fakeserver.Track{
		{Track: "internal", Releases: []fakeserver.Release{{Name: "1", Status: "completed", VersionCodes: []string{"1"}}}},
		{Track: "beta", Releases: []fakeserver.Release{{Name: "2", Status: "inProgress", UserFraction: 0.2, VersionCodes: []string{"2"}}}},
		{Track: "alpha", Releases: []fakeserver.Release{{Name: "4", Status: "halted", UserFraction: 0.2, VersionCodes: []string{"4"}}}},
	}
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	unset(t, "GPLAY_CREDENTIALS")
	unset(t, "GPLAY_CREDENTIALS_JSON")
	key := filepath.Join(t.TempDir(), "key.json")
	if err := os.WriteFile(key, testkey.ServiceAccountJSON("keyid-SECRET-12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	aab := filepath.Join(t.TempDir(), "app.aab")
	const bodySentinel = "BODY-SHOULD-NOT-LOG"
	if err := os.WriteFile(aab, []byte(bodySentinel), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		args  []string
		debug bool
	}{
		"gplay version":          {debug: false},
		"gplay schema":           {debug: false},
		"gplay auth login":       {debug: false},
		"gplay auth status":      {debug: false},
		"gplay auth doctor":      {debug: true},
		"gplay apps list":        {debug: true},
		"gplay tracks list":      {args: []string{"--package", "com.example"}, debug: true},
		"gplay tracks get":       {args: []string{"--package", "com.example", "--track", "internal"}, debug: true},
		"gplay release status":   {args: []string{"--package", "com.example", "--track", "internal"}, debug: true},
		"gplay release create":   {args: []string{"--package", "com.example", "--track", "internal", "--version-code", "1"}, debug: true},
		"gplay release rollout":  {args: []string{"--package", "com.example", "--track", "beta", "--fraction", "0.5"}, debug: true},
		"gplay release halt":     {args: []string{"--package", "com.example", "--track", "beta"}, debug: true},
		"gplay release resume":   {args: []string{"--package", "com.example", "--track", "alpha"}, debug: true},
		"gplay release complete": {args: []string{"--package", "com.example", "--track", "beta"}, debug: true},
		"gplay release promote":  {args: []string{"--package", "com.example", "--from", "internal", "--to", "beta", "--version-code", "1"}, debug: true},
		"gplay upload":           {args: []string{"--package", "com.example", "--aab", aab, "--dry-run"}, debug: true},
		"gplay api":              {args: []string{"GET", "/applications/com.example/tracks/internal/releases", "--output", "json"}, debug: true},
	}

	root := newRoot(IO{})
	var commands []string
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Runnable() && !c.HasSubCommands() {
			commands = append(commands, c.CommandPath())
		}
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(root)
	if len(commands) != len(cases) {
		t.Fatalf("command set changed: got %v", commands)
	}

	for _, path := range commands {
		tc, ok := cases[path]
		if !ok {
			t.Fatalf("no redaction case for %s", path)
		}
		args := append([]string{"--debug", "--credentials", key}, strings.Fields(path)[1:]...)
		args = append(args, tc.args...)
		var stdout, stderr bytes.Buffer
		code := Execute(t.Context(), args, IO{
			Stdout:      &stdout,
			Stderr:      &stderr,
			Endpoint:    srv.URL(),
			StaticToken: "ya29.supersecrettoken",
		})
		if code != 0 {
			t.Fatalf("%s exited %d\n%s", path, code, stderr.String())
		}
		blob := stdout.String() + stderr.String()
		for _, secret := range []string{"PRIVATE KEY", "ya29.", "keyid-SECRET-12345", "private_key"} {
			if strings.Contains(blob, secret) {
				t.Fatalf("%s leaked %s\n%s", path, secret, blob)
			}
		}
		debugs := 0
		for _, line := range strings.Split(stderr.String(), "\n") {
			if !strings.HasPrefix(line, "debug:") {
				continue
			}
			debugs++
			if strings.Contains(line, "{") || strings.Contains(line, bodySentinel) || strings.Contains(line, "displayName") {
				t.Fatalf("%s debug line has a body: %s", path, line)
			}
		}
		if tc.debug && debugs == 0 {
			t.Fatalf("%s produced no debug line\n%s", path, stderr.String())
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
