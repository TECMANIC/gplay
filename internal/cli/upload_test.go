package cli_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TECMANIC/gplay/internal/cli"
	"github.com/TECMANIC/gplay/internal/fakeserver"
)

func TestUpload(t *testing.T) {
	isolateEnv(t)
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	key := writeKey(t, 0o600)
	srv := fakeserver.New()
	t.Cleanup(srv.Close)
	base := cli.IO{Endpoint: srv.URL(), StaticToken: "ya29.supersecrettoken"}

	small := writeBytes(t, "app.aab", []byte("hello aab"))
	sum := sha256.Sum256([]byte("hello aab"))
	wantSHA := hex.EncodeToString(sum[:])

	stdout, stderr, code := runStreams(t, base, "--credentials", key, "upload", "--package", "com.example", "--aab", small, "--dry-run")
	if code != 0 {
		t.Fatalf("dry-run %d %s", code, stderr)
	}
	var doc struct {
		DryRun      bool   `json:"dry_run"`
		Committed   bool   `json:"committed"`
		VersionCode int64  `json:"version_code"`
		SHA256      string `json:"sha256"`
		Plan        []struct {
			Method string `json:"method"`
			Path   string `json:"path"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.DryRun || doc.Committed || doc.VersionCode != 42 || doc.SHA256 != wantSHA {
		t.Fatalf("%+v", doc)
	}
	if !saw(srv, "POST", "uploadType=multipart") || saw(srv, "POST", ":commit") || !deletedOnce(srv) {
		t.Fatalf("hits %+v", srv.Hits())
	}
	if !strings.Contains(stdout, ":validate") || !strings.Contains(stdout, "DELETE") {
		t.Fatalf("plan %s", stdout)
	}

	srv.Reset()
	stdout, stderr, code = runStreams(t, base, "--credentials", key, "upload", "--package", "com.example", "--aab", small, "--commit")
	if code != 0 {
		t.Fatalf("commit %d %s", code, stderr)
	}
	q := commitQuery(t, srv)
	if !strings.Contains(q, "changesInReviewBehavior=ERROR_IF_IN_REVIEW") || strings.Contains(q, "changesNotSentForReview") {
		t.Fatalf("query %s", q)
	}
	if deletedOnce(srv) {
		t.Fatal("committed edit was deleted")
	}

	srv.Reset()
	_, stderr, code = runStreams(t, base, "--credentials", key, "upload", "--package", "com.example", "--aab", small, "--commit", "--changes-not-sent-for-review")
	if code != 0 {
		t.Fatalf("not sent %d %s", code, stderr)
	}
	q = commitQuery(t, srv)
	if !strings.Contains(q, "changesNotSentForReview=true") || !strings.Contains(q, "ERROR_IF_IN_REVIEW") {
		t.Fatalf("query %s", q)
	}

	srv.Reset()
	stdout, stderr, code = runStreams(t, base, "--credentials", key, "upload", "--package", "com.example", "--aab", small, "--commit", "--dry-run")
	if code != 2 || stdout != "" || len(srv.Hits()) != 0 {
		t.Fatalf("conflict %d %q hits %d %s", code, stdout, len(srv.Hits()), stderr)
	}

	srv.Reset()
	stdout, stderr, code = runStreams(t, base, "--credentials", key, "upload", "--package", "com.example", "--aab", small, "--commit", "--cancel-in-review")
	if code != 8 || stdout != "" || len(srv.Hits()) != 0 {
		t.Fatalf("cancel %d hits %d %s", code, len(srv.Hits()), stderr)
	}

	if err := os.MkdirAll(filepath.Join(repo, ".gplay"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gplay", "policy.json"), []byte(`{"allowCancelInReview":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.Reset()
	_, stderr, code = runStreams(t, base, "--credentials", key, "upload", "--package", "com.example", "--aab", small, "--commit", "--cancel-in-review")
	if code != 0 {
		t.Fatalf("cancel allowed %d %s", code, stderr)
	}
	if !strings.Contains(commitQuery(t, srv), "CANCEL_IN_REVIEW_AND_SUBMIT") {
		t.Fatalf("%s", commitQuery(t, srv))
	}
	if err := os.Remove(filepath.Join(repo, ".gplay", "policy.json")); err != nil {
		t.Fatal(err)
	}

	mapPath := writeBytes(t, "mapping.txt", []byte("map"))
	srv.Reset()
	_, stderr, code = runStreams(t, base, "--credentials", key, "upload", "--package", "com.example", "--aab", small, "--mapping", mapPath, "--commit")
	if code != 0 {
		t.Fatalf("mapping %d %s", code, stderr)
	}
	if !saw(srv, "POST", "/deobfuscationFiles/proguard") {
		t.Fatalf("hits %+v", srv.Hits())
	}

	srv.Reset()
	srv.BundleSHA256 = "deadbeef"
	stdout, stderr, code = runStreams(t, base, "--credentials", key, "upload", "--package", "com.example", "--aab", small, "--commit")
	if code != 6 || stdout != "" || saw(srv, "POST", ":commit") || !deletedOnce(srv) {
		t.Fatalf("mismatch %d commit %v %s", code, saw(srv, "POST", ":commit"), stderr)
	}
	srv.BundleSHA256 = ""

	srv.Reset()
	srv.OmitSHA256 = true
	stdout, stderr, code = runStreams(t, base, "--credentials", key, "upload", "--package", "com.example", "--aab", small, "--dry-run")
	if code != 6 || stdout != "" || saw(srv, "POST", ":commit") {
		t.Fatalf("empty hash %d %s", code, stderr)
	}
	srv.OmitSHA256 = false

	srv.Reset()
	srv.ValidateStatus = 400
	srv.ValidateBody = `{"error":{"code":400,"message":"APK signature is invalid","status":"INVALID_ARGUMENT","errors":[{"reason":"invalid"}]}}`
	stdout, stderr, code = runStreams(t, base, "--credentials", key, "upload", "--package", "com.example", "--aab", small, "--commit")
	if code != 6 || stdout != "" || saw(srv, "POST", ":commit") || !deletedOnce(srv) {
		t.Fatalf("validate %d %s", code, stderr)
	}
	if !strings.Contains(stderr, "upload key") {
		t.Fatalf("hint %s", stderr)
	}
	srv.ValidateStatus = 0

	srv.Reset()
	srv.CommitStatus = 400
	srv.CommitBody = `{"error":{"code":400,"message":"changes already in review","status":"FAILED_PRECONDITION","details":[{"reason":"CHANGES_ALREADY_IN_REVIEW"}]}}`
	stdout, stderr, code = runStreams(t, base, "--credentials", key, "upload", "--package", "com.example", "--aab", small, "--commit")
	if code != 5 || stdout != "" || !deletedOnce(srv) {
		t.Fatalf("in review %d deletes %v %s", code, deletedOnce(srv), stderr)
	}
	assertOneJSON(t, lastLine(stderr), 5, "changes_in_review")
	srv.CommitStatus = 0

	chunk := 256 * 1024
	big := make([]byte, chunk+1024)
	for i := range big {
		big[i] = byte(i)
	}
	bigPath := writeBytes(t, "big.aab", big)
	srv.Reset()
	streams := base
	streams.ChunkSize = chunk
	stdout, stderr, code = runStreams(t, streams, "--credentials", key, "upload", "--package", "com.example", "--aab", bigPath, "--dry-run")
	if code != 0 {
		t.Fatalf("resumable %d %s", code, stderr)
	}
	if !saw(srv, "POST", "uploadType=resumable") {
		t.Fatalf("hits %+v", srv.Hits())
	}
	var incomplete, complete bool
	for _, h := range srv.Hits() {
		if strings.Contains(h.ContentRange, "/*") {
			incomplete = true
		}
		if h.ContentRange != "" && !strings.Contains(h.ContentRange, "/*") {
			complete = true
		}
	}
	if !incomplete || !complete {
		t.Fatalf("ranges %+v", srv.Hits())
	}
	var got struct {
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	bigSum := sha256.Sum256(big)
	if got.SHA256 != hex.EncodeToString(bigSum[:]) {
		t.Fatalf("sha %s", got.SHA256)
	}

	srv.Reset()
	stdout, stderr, code = runStreams(t, base, "--credentials", key, "upload", "--package", "com.example")
	if code != 2 || stdout != "" || len(srv.Hits()) != 0 {
		t.Fatalf("default path %d hits %d %s", code, len(srv.Hits()), stderr)
	}
}

func writeBytes(t *testing.T, name string, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func saw(srv *fakeserver.Server, method, piece string) bool {
	for _, h := range srv.Hits() {
		if h.Method == method && (strings.Contains(h.Path, piece) || strings.Contains(h.RawQuery, piece) || strings.Contains(h.UploadType, piece)) {
			return true
		}
	}
	return false
}

func commitQuery(t *testing.T, srv *fakeserver.Server) string {
	t.Helper()
	for _, h := range srv.Hits() {
		if strings.Contains(h.Path, ":commit") {
			return h.RawQuery
		}
	}
	t.Fatal("no commit")
	return ""
}

func lastLine(s string) string {
	s = strings.TrimSuffix(s, "\n")
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}
