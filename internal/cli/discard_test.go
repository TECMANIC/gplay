package cli_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/TECMANIC/gplay/internal/cli"
)

func TestCancelDuringReadStillDiscardsOnce(t *testing.T) {
	isolateEnv(t)
	key := writeKey(t, 0o600)
	var mu sync.Mutex
	deletes := 0
	started := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/edits"):
			_, _ = io.WriteString(w, `{"id":"edit-1","expiryTimeSeconds":"1893456000"}`)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/tracks"):
			once.Do(func() { close(started) })
			<-r.Context().Done()
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/edits/"):
			mu.Lock()
			deletes++
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-started
		cancel()
	}()
	var stdout, stderr strings.Builder
	code := cli.Execute(ctx, []string{"--credentials", key, "tracks", "list", "--package", "com.example"}, cli.IO{
		Stdout:      &stdout,
		Stderr:      &stderr,
		Endpoint:    srv.URL,
		StaticToken: "ya29.supersecrettoken",
	})
	if code != 130 || stdout.String() != "" {
		t.Fatalf("code %d stdout %q stderr %s", code, stdout.String(), stderr.String())
	}
	var got struct {
		Code     string `json:"code"`
		ExitCode int    `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stderr.String())), &got); err != nil {
		t.Fatal(err)
	}
	if got.Code != "interrupted" || got.ExitCode != 130 {
		t.Fatalf("%+v stderr %s", got, stderr.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if deletes != 1 {
		t.Fatalf("deletes %d", deletes)
	}
}
