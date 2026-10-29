package notes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TECMANIC/gplay/internal/clierror"
)

func TestParseInlineAndFile(t *testing.T) {
	got, err := Parse("en-US", "hello", "")
	if err != nil || len(got) != 1 || got[0].Text != "hello" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := Parse("", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse("en-US", "a", "b"); err == nil || err.(*clierror.Error).ExitCode != 2 {
		t.Fatal(err)
	}
	if _, err := Parse("not a tag", "hi", ""); err == nil {
		t.Fatal("bad tag")
	}
	long := strings.Repeat("é", 500)
	if _, err := Parse("en-US", long, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse("en-US", long+"x", ""); err == nil {
		t.Fatal("501 runes")
	}
	if _, err := Parse("en-US", string([]byte{0xff}), ""); err == nil {
		t.Fatal("utf-8")
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(file, []byte("from file"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = Parse("fr", "", file)
	if err != nil || got[0].Language != "fr" || got[0].Text != "from file" {
		t.Fatalf("%+v %v", got, err)
	}

	notes := filepath.Join(dir, "notes")
	if err := os.Mkdir(notes, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notes, "en-US.txt"), []byte("en"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notes, "pt-BR.txt"), []byte("pt"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = Parse("en-US", "", notes)
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}

	if err := os.WriteFile(filepath.Join(notes, "readme.md"), []byte("no"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse("en-US", "", notes); err == nil {
		t.Fatal("extra file")
	}
	if err := os.Remove(filepath.Join(notes, "readme.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(notes, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse("en-US", "", notes); err == nil {
		t.Fatal("subdir")
	}
}

func TestNotesContainment(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "note.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse("en-US", "", link); err == nil {
		t.Fatal("file symlink")
	}
	notes := filepath.Join(dir, "notes")
	if err := os.Mkdir(notes, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(notes, "en-US.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse("en-US", "", notes); err == nil {
		t.Fatal("dir symlink")
	}
	if _, err := Parse("en-US", "", filepath.Join(dir, "..", "secret.txt")); err == nil {
		t.Fatal("dotdot")
	}
}

func FuzzParse(f *testing.F) {
	f.Add("en-US", "hello", false)
	f.Add("fr", "", true)
	f.Fuzz(func(t *testing.T, language, text string, asFile bool) {
		if asFile {
			dir := t.TempDir()
			path := filepath.Join(dir, "n.txt")
			if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			_, _ = Parse(language, "", path)
			return
		}
		_, _ = Parse(language, text, "")
	})
}
