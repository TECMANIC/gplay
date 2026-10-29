package cli_test

import (
	"strings"
	"testing"

	"github.com/TECMANIC/gplay/internal/cli"
	"github.com/TECMANIC/gplay/internal/fakeserver"
)

func TestAPIGET(t *testing.T) {
	isolateEnv(t)
	key := writeKey(t, 0o600)
	srv := fakeserver.New()
	t.Cleanup(srv.Close)
	streams := cli.IO{Endpoint: srv.URL(), StaticToken: "ya29.supersecrettoken"}
	path := "/applications/com.example/tracks/internal/releases?pageSize=1"

	stdout, stderr, code := runStreams(t, streams, "--credentials", key, "api", "GET", path)
	if code != 0 {
		t.Fatalf("get %d %s", code, stderr)
	}
	if !strings.Contains(stdout, "RELEASE_LIFECYCLE_STATE_PUBLISHED") {
		t.Fatalf("%s", stdout)
	}
	if !saw(srv, "GET", "pageSize=1") {
		t.Fatalf("hits %+v", srv.Hits())
	}
	for _, h := range srv.Hits() {
		if strings.Contains(h.Path, "/edits") {
			t.Fatal("api GET opened an edit")
		}
	}

	srv.Reset()
	stdout, stderr, code = runStreams(t, streams, "--credentials", key, "api", "POST", "/applications/com.example/tracks/production")
	if code != 2 || stdout != "" || len(srv.Hits()) != 0 {
		t.Fatalf("post %d hits %d %s", code, len(srv.Hits()), stderr)
	}

	stdout, stderr, code = runStreams(t, streams, "--credentials", key, "api", "GET", "https://evil.example/x")
	if code != 2 || len(srv.Hits()) != 0 {
		t.Fatalf("url %d hits %d %s", code, len(srv.Hits()), stderr)
	}
	stdout, stderr, code = runStreams(t, streams, "--credentials", key, "api", "GET", "/applications/../secret")
	if code != 2 || len(srv.Hits()) != 0 {
		t.Fatalf("dotdot %d hits %d %s", code, len(srv.Hits()), stderr)
	}

	stdout, stderr, code = runStreams(t, streams, "--output", "table", "--credentials", key, "api", "GET", path)
	if code != 2 || stdout != "" || len(srv.Hits()) != 0 {
		t.Fatalf("table %d hits %d %s", code, len(srv.Hits()), stderr)
	}

	srv.Reset()
	stdout, stderr, code = runStreams(t, streams, "--credentials", key, "api", "GET", "/applications/com.example/tracks/missing/releases")
	if code != 4 || stdout != "" {
		t.Fatalf("missing %d %s", code, stderr)
	}

	schema, _, code := runStreams(t, cli.IO{}, "schema")
	if code != 0 || !strings.Contains(schema, `"name": "api"`) || !strings.Contains(schema, "GET-only") && !strings.Contains(schema, "GET a path") {
		t.Fatalf("schema missing api: %s", schema)
	}
}
