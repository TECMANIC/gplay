package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TECMANIC/gplay/internal/cli"
	"github.com/TECMANIC/gplay/internal/fakeserver"
)

func TestReleaseCreate(t *testing.T) {
	isolateEnv(t)
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	key := writeKey(t, 0o600)
	srv := fakeserver.New()
	t.Cleanup(srv.Close)
	streams := cli.IO{Endpoint: srv.URL(), StaticToken: "ya29.supersecrettoken"}
	args := []string{"--credentials", key, "release", "create", "--package", "com.example"}

	stdout, stderr, code := runStreams(t, streams, append(args, "--track", "production", "--dry-run")...)
	if code != 2 || stdout != "" || len(srv.Hits()) != 0 {
		t.Fatalf("missing status %d hits %d %s", code, len(srv.Hits()), stderr)
	}

	srv.Reset()
	stdout, stderr, code = runStreams(t, streams, append(args, "--track", "production", "--status", "completed", "--allow-production", "--dry-run", "--version-code", "3")...)
	if code != 8 || stdout != "" || len(srv.Hits()) != 0 {
		t.Fatalf("lock %d hits %d %s", code, len(srv.Hits()), stderr)
	}

	stdout, stderr, code = runStreams(t, streams, append(args, "--track", "internal", "--version-code", "7", "--notes", "ship it")...)
	if code != 0 {
		t.Fatalf("create %d %s", code, stderr)
	}
	body := onlyTrack(t, srv)
	if body.Track != "internal" || len(body.Releases) != 1 {
		t.Fatalf("%+v", body)
	}
	rel := body.Releases[0]
	if rel.Status != "completed" || rel.Name != "7" || string(rel.VersionCodes[0]) != `"7"` {
		t.Fatalf("%+v", rel)
	}
	if len(rel.Notes) != 1 || rel.Notes[0].Language != "en-US" || rel.Notes[0].Text != "ship it" {
		t.Fatalf("notes %+v", rel.Notes)
	}
	if rel.UserFraction != nil {
		t.Fatal("completed sent a fraction")
	}
	q := commitQuery(t, srv)
	if !strings.Contains(q, "ERROR_IF_IN_REVIEW") || strings.Contains(q, "changesNotSentForReview") {
		t.Fatalf("query %s", q)
	}

	srv.Reset()
	stdout, stderr, code = runStreams(t, streams, append(args, "--track", "internal", "--version-code", "7", "--fraction", "0.25")...)
	if code != 0 {
		t.Fatalf("fraction %d %s", code, stderr)
	}
	body = onlyTrack(t, srv)
	if body.Releases[0].Status != "inProgress" || body.Releases[0].UserFraction == nil || *body.Releases[0].UserFraction != 0.25 {
		t.Fatalf("%+v", body.Releases[0])
	}

	aab := writeBytes(t, "app.aab", []byte("bundle-bytes"))
	srv.Reset()
	stdout, stderr, code = runStreams(t, streams, append(args, "--track", "internal", "--aab", aab, "--release-name", "Ship")...)
	if code != 0 {
		t.Fatalf("aab %d %s", code, stderr)
	}
	if !saw(srv, "POST", "/bundles") || !saw(srv, "PUT", "/tracks/internal") || !saw(srv, "POST", ":commit") {
		t.Fatalf("hits %+v", srv.Hits())
	}
	body = onlyTrack(t, srv)
	if body.Releases[0].Name != "Ship" || string(body.Releases[0].VersionCodes[0]) != `"42"` {
		t.Fatalf("%+v", body.Releases[0])
	}

	srv.Reset()
	stdout, stderr, code = runStreams(t, streams, append(args, "--track", "internal", "--aab", aab, "--version-code", "7")...)
	if code != 2 || stdout != "" || saw(srv, "POST", ":commit") || !deletedOnce(srv) {
		t.Fatalf("mismatch %d %s", code, stderr)
	}

	notes := filepath.Join(t.TempDir(), "notes")
	if err := os.Mkdir(notes, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notes, "en-US.txt"), []byte("english"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notes, "fr.txt"), []byte("french"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.Reset()
	_, stderr, code = runStreams(t, streams, append(args, "--track", "internal", "--version-code", "7", "--notes-file", notes)...)
	if code != 0 {
		t.Fatalf("notes %d %s", code, stderr)
	}
	body = onlyTrack(t, srv)
	if len(body.Releases[0].Notes) != 2 {
		t.Fatalf("%+v", body.Releases[0].Notes)
	}

	srv.Reset()
	srv.ValidateStatus = 400
	srv.ValidateBody = `{"error":{"code":400,"message":"Version code 7 has already been used","status":"INVALID_ARGUMENT"}}`
	stdout, stderr, code = runStreams(t, streams, append(args, "--track", "internal", "--version-code", "7")...)
	if code != 6 || stdout != "" || saw(srv, "POST", ":commit") || !deletedOnce(srv) {
		t.Fatalf("validate %d %s", code, stderr)
	}
	if !strings.Contains(stderr, "versionCode") && !strings.Contains(stderr, "version code") {
		t.Fatalf("hint %s", stderr)
	}
	srv.ValidateStatus = 0

	srv.Reset()
	srv.CommitStatus = 400
	srv.CommitBody = `{"error":{"code":400,"message":"changes already in review","status":"FAILED_PRECONDITION","details":[{"reason":"CHANGES_ALREADY_IN_REVIEW"}]}}`
	stdout, stderr, code = runStreams(t, streams, append(args, "--track", "internal", "--version-code", "8", "--dry-run")...)
	if code != 0 {
		t.Fatalf("dry %d %s", code, stderr)
	}
	if saw(srv, "POST", ":commit") || !deletedOnce(srv) {
		t.Fatal("dry-run committed")
	}
	srv.Reset()
	stdout, stderr, code = runStreams(t, streams, append(args, "--track", "internal", "--version-code", "8")...)
	if code != 5 || stdout != "" || !deletedOnce(srv) {
		t.Fatalf("review %d %s", code, stderr)
	}
}

type trackBody struct {
	Track    string `json:"track"`
	Releases []struct {
		Name         string            `json:"name"`
		Status       string            `json:"status"`
		VersionCodes []json.RawMessage `json:"versionCodes"`
		UserFraction *float64          `json:"userFraction"`
		Notes        []struct {
			Language string `json:"language"`
			Text     string `json:"text"`
		} `json:"releaseNotes"`
	} `json:"releases"`
}

func onlyTrack(t *testing.T, srv *fakeserver.Server) trackBody {
	t.Helper()
	bodies := srv.TrackBodies()
	if len(bodies) != 1 {
		t.Fatalf("updates %d", len(bodies))
	}
	var body trackBody
	if err := json.Unmarshal(bodies[0], &body); err != nil {
		t.Fatal(err)
	}
	return body
}
