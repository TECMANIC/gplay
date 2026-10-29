package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/TECMANIC/gplay/internal/cli"
	"github.com/TECMANIC/gplay/internal/fakeserver"
)

func TestReads(t *testing.T) {
	isolateEnv(t)
	key := writeKey(t, 0o600)
	srv := fakeserver.New()
	t.Cleanup(srv.Close)
	srv.Paginate = true
	streams := cli.IO{Endpoint: srv.URL(), StaticToken: "ya29.supersecrettoken"}

	stdout, stderr, code := runStreams(t, streams, "--credentials", key, "apps", "list")
	if code != 0 {
		t.Fatalf("apps %d %s", code, stderr)
	}
	var apps struct {
		Apps []struct {
			PackageName string `json:"packageName"`
			DisplayName string `json:"displayName"`
		} `json:"apps"`
	}
	if err := json.Unmarshal([]byte(stdout), &apps); err != nil {
		t.Fatal(err)
	}
	if len(apps.Apps) != 2 || apps.Apps[0].PackageName != "com.example" || apps.Apps[1].PackageName != "com.example.two" {
		t.Fatalf("%+v", apps.Apps)
	}
	if strings.Contains(stdout+stderr, "ya29.") || strings.Contains(stdout+stderr, "PRIVATE KEY") {
		t.Fatal("leaked")
	}

	srv.Reset()
	stdout, stderr, code = runStreams(t, streams, "--credentials", key, "tracks", "list", "--package", "com.example")
	if code != 0 {
		t.Fatalf("tracks %d %s", code, stderr)
	}
	var tracks struct {
		Tracks []struct {
			Track    string            `json:"track"`
			Releases []json.RawMessage `json:"releases"`
		} `json:"tracks"`
	}
	if err := json.Unmarshal([]byte(stdout), &tracks); err != nil {
		t.Fatal(err)
	}
	if len(tracks.Tracks) == 0 || tracks.Tracks[0].Track != "internal" {
		t.Fatalf("%s", stdout)
	}
	var rel struct {
		VersionCodes []json.RawMessage `json:"versionCodes"`
	}
	if err := json.Unmarshal(tracks.Tracks[0].Releases[0], &rel); err != nil {
		t.Fatal(err)
	}
	if len(rel.VersionCodes) != 1 || string(rel.VersionCodes[0]) != `"1"` {
		t.Fatalf("version codes not strings: %s", rel.VersionCodes)
	}
	assertOrder(t, srv, "POST", "/edits", "GET", "/tracks", "DELETE", "/edits/")
	for _, h := range srv.Hits() {
		if strings.Contains(h.Path, ":commit") {
			t.Fatalf("commit on a read: %+v", srv.Hits())
		}
	}

	srv.Reset()
	stdout, stderr, code = runStreams(t, streams, "--credentials", key, "tracks", "get", "--package", "com.example", "--track", "Internal")
	if code != 0 {
		t.Fatalf("get %d %s", code, stderr)
	}
	if !strings.Contains(stdout, `"track": "internal"`) {
		t.Fatalf("%s", stdout)
	}
	var sawList, sawGet bool
	for _, h := range srv.Hits() {
		if h.Method == "GET" && strings.HasSuffix(h.Path, "/tracks") {
			sawList = true
		}
		if h.Method == "GET" && strings.Contains(h.Path, "/tracks/internal") {
			sawGet = true
		}
	}
	if !sawList || !sawGet {
		t.Fatalf("hits %+v", srv.Hits())
	}

	srv.Reset()
	stdout, stderr, code = runStreams(t, streams, "--credentials", key, "tracks", "get", "--package", "com.example", "--track", "bogus")
	if code != 4 || stdout != "" {
		t.Fatalf("bogus %d %q %s", code, stdout, stderr)
	}
	assertOneJSON(t, stderr, 4, "not_found")
	if !strings.Contains(stderr, "internal") {
		t.Fatalf("hint %s", stderr)
	}
	if !deletedOnce(srv) {
		t.Fatalf("hits %+v", srv.Hits())
	}

	srv.Reset()
	stdout, stderr, code = runStreams(t, streams, "--credentials", key, "release", "status", "--package", "com.example", "--track", "internal")
	if code != 0 {
		t.Fatalf("status %d %s", code, stderr)
	}
	if !strings.Contains(stdout, "RELEASE_LIFECYCLE_STATE_PUBLISHED") {
		t.Fatalf("%s", stdout)
	}
	for _, h := range srv.Hits() {
		if strings.Contains(h.Path, "/edits") {
			t.Fatalf("release status opened an edit: %+v", srv.Hits())
		}
	}

	srv.Reset()
	stdout, stderr, code = runStreams(t, streams, "--credentials", key, "release", "status", "--package", "com.example", "--track", "nope")
	if code != 4 || stdout != "" {
		t.Fatalf("missing track %d %s", code, stderr)
	}
	for _, h := range srv.Hits() {
		if strings.Contains(h.Path, "/edits") {
			t.Fatal("edit opened")
		}
	}
}

func assertOrder(t *testing.T, srv *fakeserver.Server, parts ...string) {
	t.Helper()
	hits := srv.Hits()
	at := 0
	for i := 0; i < len(parts); i += 2 {
		method, piece := parts[i], parts[i+1]
		found := false
		for ; at < len(hits); at++ {
			if hits[at].Method == method && strings.Contains(hits[at].Path, piece) {
				found = true
				at++
				break
			}
		}
		if !found {
			t.Fatalf("missing %s %s in %+v", method, piece, hits)
		}
	}
}

func deletedOnce(srv *fakeserver.Server) bool {
	n := 0
	for _, h := range srv.Hits() {
		if h.Method == "DELETE" && strings.Contains(h.Path, "/edits/") {
			n++
		}
	}
	return n == 1
}
