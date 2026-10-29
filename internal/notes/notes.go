// Package notes loads release notes from a string or from a file tree.
package notes

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/TECMANIC/gplay/internal/clierror"
)

// MaxRunes is Play's per-note limit.
const MaxRunes = 500

// bcp47 is the tag shape v0.1 accepts. It is not a full BCP-47 parser.
var bcp47 = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

// Note is one localized release note.
type Note struct {
	Language string
	Text     string
}

// Parse returns notes from inline text or from path.
// An empty text and an empty path means there are no notes.
// path is a regular file in language, or a directory of <bcp47>.txt files.
func Parse(language, text, path string) ([]Note, error) {
	if text != "" && path != "" {
		return nil, clierror.Usage("--notes and --notes-file cannot be combined", "pass one of them")
	}
	if language == "" {
		language = "en-US"
	}
	if !bcp47.MatchString(language) {
		return nil, clierror.Usage("--language is not a BCP-47 tag", "example: en-US")
	}
	if text == "" && path == "" {
		return nil, nil
	}
	if text != "" {
		if err := checkText(text); err != nil {
			return nil, err
		}
		return []Note{{Language: language, Text: text}}, nil
	}
	if hasDotDot(path) {
		return nil, clierror.Usage("notes path must not contain ..", "")
	}
	return fromPath(language, path)
}

func hasDotDot(path string) bool {
	for _, seg := range strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == os.PathSeparator
	}) {
		if seg == ".." {
			return true
		}
	}
	return false
}

func checkText(s string) error {
	if !utf8.ValidString(s) {
		return clierror.Usage("release notes are not valid UTF-8", "")
	}
	if utf8.RuneCountInString(s) > MaxRunes {
		return clierror.Usage("release notes exceed 500 characters", "shorten the note")
	}
	return nil
}

func fromPath(language, path string) ([]Note, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, clierror.Usage("cannot resolve notes path", "")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, clierror.Usage("cannot read notes path", "check that the path exists")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, clierror.Usage("cannot read notes path", "check that the path exists")
	}
	if info.IsDir() {
		if resolved != abs {
			return nil, clierror.Usage("notes directory must not be a symlink", "")
		}
		return fromDir(resolved)
	}
	if resolved != abs {
		return nil, clierror.Usage("notes file must not be a symlink", "")
	}
	body, err := os.ReadFile(resolved)
	if err != nil {
		return nil, clierror.Usage("cannot read notes file", "")
	}
	if err := checkText(string(body)); err != nil {
		return nil, err
	}
	return []Note{{Language: language, Text: string(body)}}, nil
}

func fromDir(dir string) ([]Note, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, clierror.Usage("cannot read notes directory", "")
	}
	if len(entries) == 0 {
		return nil, clierror.Usage("notes directory is empty", "add <bcp47>.txt files")
	}
	var out []Note
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".txt") {
			return nil, clierror.Usage("unexpected notes entry "+name, "the directory may contain only <bcp47>.txt files")
		}
		tag := strings.TrimSuffix(name, ".txt")
		if !bcp47.MatchString(tag) {
			return nil, clierror.Usage("notes file name is not a BCP-47 tag: "+name, "")
		}
		full := filepath.Join(dir, name)
		resolved, err := filepath.EvalSymlinks(full)
		if err != nil {
			return nil, clierror.Usage("cannot read "+name, "")
		}
		rel, err := filepath.Rel(dir, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return nil, clierror.Usage("notes file escapes the directory: "+name, "")
		}
		body, err := os.ReadFile(resolved)
		if err != nil {
			return nil, clierror.Usage("cannot read "+name, "")
		}
		if err := checkText(string(body)); err != nil {
			return nil, err
		}
		out = append(out, Note{Language: tag, Text: string(body)})
	}
	return out, nil
}
