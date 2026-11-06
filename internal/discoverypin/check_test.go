package discoverypin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckMatch(t *testing.T) {
	pins := Pins{
		AndroidPublisher:       APIPin{Version: "v3", Revision: "20261029"},
		PlayDeveloperReporting: APIPin{Version: "v1beta1", Revision: "20261028"},
		GoogleAPIModule:        "v0.310.0",
	}
	pub := []byte(`{"id":"androidpublisher:v3","version":"v3","revision":"20261029","extra":true}`)
	rep := []byte(`{"version":"v1beta1","revision":"20261028","resources":{}}`)
	mod := []byte("module example\n\nrequire google.golang.org/api v0.310.0\n")
	if err := Check(pins, pub, rep, mod); err != nil {
		t.Fatal(err)
	}
}

func TestCheckReportsEveryMismatch(t *testing.T) {
	pins := Pins{
		AndroidPublisher:       APIPin{Version: "v3", Revision: "20261029"},
		PlayDeveloperReporting: APIPin{Version: "v1beta1", Revision: "20261028"},
		GoogleAPIModule:        "v0.310.0",
	}
	pub := []byte(`{"version":"v3","revision":"19990101"}`)
	rep := []byte(`{"version":"v1","revision":"20261028"}`)
	mod := []byte("require google.golang.org/api v0.1.0\n")
	err := Check(pins, pub, rep, mod)
	if err == nil {
		t.Fatal("expected mismatches")
	}
	msg := err.Error()
	for _, want := range []string{"androidpublisher", "19990101", "playdeveloperreporting", "v0.1.0"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("missing %q in %s", want, msg)
		}
	}
}

func TestCheckRejectsIncompleteDocument(t *testing.T) {
	pins := Pins{
		AndroidPublisher:       APIPin{Version: "v3", Revision: "20261029"},
		PlayDeveloperReporting: APIPin{Version: "v1beta1", Revision: "20261028"},
		GoogleAPIModule:        "v0.310.0",
	}
	err := Check(pins, []byte(`{"version":"v3"}`), []byte(`{"version":"v1beta1","revision":"20261028"}`), []byte("require google.golang.org/api v0.310.0\n"))
	if err == nil || !strings.Contains(err.Error(), "missing version or revision") {
		t.Fatalf("got %v", err)
	}
}

func TestParsePinsRejectsUnknownKeys(t *testing.T) {
	_, err := ParsePins(strings.NewReader(`{"androidpublisher":{"version":"v3","revision":"20261029"},"playdeveloperreporting":{"version":"v1beta1","revision":"20261028"},"google_api_module":"v0.310.0","extra":true}`))
	if err == nil {
		t.Fatal("expected unknown key to fail")
	}
}

func TestModuleVersionDuplicate(t *testing.T) {
	_, err := ModuleVersion([]byte("google.golang.org/api v0.1.0\ngoogle.golang.org/api v0.2.0\n"), "google.golang.org/api")
	if err == nil {
		t.Fatal("expected duplicate to fail")
	}
}

func TestCommittedPinsMatchModule(t *testing.T) {
	root := moduleRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "discovery", "pins.json"))
	if err != nil {
		t.Fatal(err)
	}
	pins, err := ParsePins(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ModuleVersion(mod, "google.golang.org/api")
	if err != nil {
		t.Fatal(err)
	}
	if got != pins.GoogleAPIModule {
		t.Fatalf("go.mod has %s, pins have %s", got, pins.GoogleAPIModule)
	}
	if pins.AndroidPublisher.Revision != "20261029" || pins.PlayDeveloperReporting.Revision != "20261028" {
		t.Fatalf("unexpected revisions: %+v %+v", pins.AndroidPublisher, pins.PlayDeveloperReporting)
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
