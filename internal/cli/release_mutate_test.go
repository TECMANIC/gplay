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

func TestMutations(t *testing.T) {
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
	base := []string{"--credentials", key, "release"}

	stdout, stderr, code := runStreams(t, streams, append(base, "rollout", "--package", "com.example", "--track", "production", "--fraction", "0.1", "--dry-run")...)
	if code != 8 || stdout != "" || len(srv.Hits()) != 0 {
		t.Fatalf("rollout lock %d hits %d %s", code, len(srv.Hits()), stderr)
	}

	srv.Reset()
	stdout, stderr, code = runStreams(t, streams, append(base, "complete", "--package", "com.example", "--track", "production", "--allow-production", "--dry-run")...)
	if code != 8 || stdout != "" || len(srv.Hits()) != 0 {
		t.Fatalf("complete cap preflight %d hits %d %s", code, len(srv.Hits()), stderr)
	}

	srv.Tracks = []fakeserver.Track{{
		Track: "beta",
		Releases: []fakeserver.Release{{
			Name: "2", Status: "inProgress", UserFraction: 0.2, VersionCodes: []string{"2"},
			InAppUpdatePriority: 4,
			ReleaseNotes:        []fakeserver.Note{{Language: "en-US", Text: "keep"}},
			CountryTargeting:    &fakeserver.Targeting{Countries: []string{"US"}},
		}},
	}, {
		Track: "production",
		Releases: []fakeserver.Release{{
			Name: "9", Status: "inProgress", UserFraction: 0.5, VersionCodes: []string{"9"},
		}},
	}, {
		Track:    "internal",
		Releases: []fakeserver.Release{{Name: "1", Status: "completed", VersionCodes: []string{"1"}, ReleaseNotes: []fakeserver.Note{{Language: "en-US", Text: "from-internal"}}}},
	}}

	if err := os.MkdirAll(filepath.Join(repo, ".gplay"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gplay", "policy.json"), []byte(`{"allowProduction":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	srv.Reset()
	stdout, stderr, code = runStreams(t, streams, append(base, "halt", "--package", "com.example", "--track", "production", "--allow-production")...)
	if code != 8 || stdout != "" || len(srv.TrackBodies()) != 0 || saw(srv, "POST", ":commit") || !deletedOnce(srv) {
		t.Fatalf("cap after read %d bodies %d %s", code, len(srv.TrackBodies()), stderr)
	}
	if !saw(srv, "GET", "/tracks/production") {
		t.Fatalf("expected a read: %+v", srv.Hits())
	}

	srv.Reset()
	_, stderr, code = runStreams(t, streams, append(base, "halt", "--package", "com.example", "--track", "production", "--allow-production", "--allow-production-fraction", "0.5")...)
	if code != 0 {
		t.Fatalf("halt %d %s", code, stderr)
	}
	body := onlyTrack(t, srv)
	rel := body.Releases[0]
	if rel.Status != "halted" || rel.UserFraction == nil || *rel.UserFraction != 0.5 || string(rel.VersionCodes[0]) != `"9"` || rel.Name != "9" {
		t.Fatalf("%+v", rel)
	}

	srv.Reset()
	_, stderr, code = runStreams(t, streams, append(base, "rollout", "--package", "com.example", "--track", "beta", "--fraction", "0.4")...)
	if code != 0 {
		t.Fatalf("rollout %d %s", code, stderr)
	}
	raw := srv.TrackBodies()[0]
	if !strings.Contains(string(raw), `"versionCodes":["2"]`) && !strings.Contains(string(raw), `"2"`) {
		t.Fatalf("version codes %s", raw)
	}
	var preserved struct {
		Releases []struct {
			Status              string   `json:"status"`
			Name                string   `json:"name"`
			InAppUpdatePriority int64    `json:"inAppUpdatePriority"`
			Countries           []string `json:"countries"`
			Notes               []struct {
				Text string `json:"text"`
			} `json:"releaseNotes"`
			Target *struct {
				Countries []string `json:"countries"`
			} `json:"countryTargeting"`
			VersionCodes []json.RawMessage `json:"versionCodes"`
			UserFraction *float64          `json:"userFraction"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(raw, &preserved); err != nil {
		t.Fatal(err)
	}
	got := preserved.Releases[0]
	if got.Status != "inProgress" || got.Name != "2" || got.InAppUpdatePriority != 4 || got.UserFraction == nil || *got.UserFraction != 0.4 {
		t.Fatalf("%+v", got)
	}
	if string(got.VersionCodes[0]) != `"2"` || got.Notes[0].Text != "keep" || got.Target == nil || got.Target.Countries[0] != "US" {
		t.Fatalf("%s", raw)
	}

	srv.Reset()
	_, stderr, code = runStreams(t, streams, append(base, "complete", "--package", "com.example", "--track", "beta")...)
	if code != 0 {
		t.Fatalf("complete %d %s", code, stderr)
	}
	body = onlyTrack(t, srv)
	if body.Releases[0].Status != "completed" || body.Releases[0].UserFraction != nil || string(body.Releases[0].VersionCodes[0]) != `"2"` {
		t.Fatalf("%+v", body.Releases[0])
	}

	srv.Tracks = []fakeserver.Track{{
		Track: "beta",
		Releases: []fakeserver.Release{
			{Name: "a", Status: "inProgress", UserFraction: 0.1, VersionCodes: []string{"1"}},
			{Name: "b", Status: "halted", UserFraction: 0.2, VersionCodes: []string{"2"}},
		},
	}}
	srv.Reset()
	stdout, stderr, code = runStreams(t, streams, append(base, "rollout", "--package", "com.example", "--track", "beta", "--fraction", "0.3")...)
	if code != 2 || stdout != "" || len(srv.TrackBodies()) != 0 || !deletedOnce(srv) {
		t.Fatalf("ambiguous %d %s", code, stderr)
	}

	srv.Tracks = []fakeserver.Track{{
		Track:    "beta",
		Releases: []fakeserver.Release{{Name: "a", Status: "completed", VersionCodes: []string{"1"}}},
	}}
	srv.Reset()
	stdout, stderr, code = runStreams(t, streams, append(base, "resume", "--package", "com.example", "--track", "beta")...)
	if code != 4 || stdout != "" || len(srv.TrackBodies()) != 0 {
		t.Fatalf("missing %d %s", code, stderr)
	}

	srv.Tracks = []fakeserver.Track{{
		Track:    "beta",
		Releases: []fakeserver.Release{{Name: "a", Status: "halted", UserFraction: 0.2, VersionCodes: []string{"5"}}},
	}, {
		Track:    "internal",
		Releases: []fakeserver.Release{{Name: "1", Status: "completed", VersionCodes: []string{"1"}, ReleaseNotes: []fakeserver.Note{{Language: "en-US", Text: "from-internal"}}}},
	}}
	srv.Reset()
	_, stderr, code = runStreams(t, streams, append(base, "resume", "--package", "com.example", "--track", "beta")...)
	if code != 0 {
		t.Fatalf("resume %d %s", code, stderr)
	}
	body = onlyTrack(t, srv)
	if body.Releases[0].Status != "inProgress" || *body.Releases[0].UserFraction != 0.2 || string(body.Releases[0].VersionCodes[0]) != `"5"` {
		t.Fatalf("%+v", body.Releases[0])
	}

	srv.Reset()
	_, stderr, code = runStreams(t, streams, append(base, "promote", "--package", "com.example", "--from", "internal", "--to", "beta", "--version-code", "1")...)
	if code != 0 {
		t.Fatalf("promote %d %s", code, stderr)
	}
	bodies := srv.TrackBodies()
	if len(bodies) != 1 || !strings.Contains(string(bodies[0]), `"track":"beta"`) && !strings.Contains(string(bodies[0]), `"track": "beta"`) {
		t.Fatalf("promote body %s", bodies)
	}
	if strings.Contains(string(bodies[0]), `"track":"internal"`) || strings.Contains(string(bodies[0]), `"track": "internal"`) {
		t.Fatalf("source track was written: %s", bodies[0])
	}
	var promoted trackBody
	if err := json.Unmarshal(bodies[0], &promoted); err != nil {
		t.Fatal(err)
	}
	if promoted.Releases[0].Status != "completed" || string(promoted.Releases[0].VersionCodes[0]) != `"1"` || promoted.Releases[0].Notes[0].Text != "from-internal" {
		t.Fatalf("%+v", promoted.Releases[0])
	}
	for _, h := range srv.Hits() {
		if h.Method == "PUT" && strings.Contains(h.Path, "/tracks/internal") {
			t.Fatal("source track updated")
		}
	}

	srv.Reset()
	stdout, stderr, code = runStreams(t, streams, append(base, "promote", "--package", "com.example", "--from", "internal", "--to", "production", "--version-code", "1", "--dry-run")...)
	if code != 2 || stdout != "" || len(srv.Hits()) != 0 {
		t.Fatalf("promote usage %d hits %d %s", code, len(srv.Hits()), stderr)
	}
	srv.Reset()
	stdout, stderr, code = runStreams(t, streams, append(base, "promote", "--package", "com.example", "--from", "internal", "--to", "production", "--version-code", "1", "--status", "completed", "--dry-run")...)
	if code != 8 || stdout != "" || len(srv.Hits()) != 0 {
		t.Fatalf("promote lock %d hits %d %s", code, len(srv.Hits()), stderr)
	}
}
