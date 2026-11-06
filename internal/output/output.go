// Package output writes success payloads. Failures never go through it:
// stdout is empty when the process exits non-zero.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
)

// JSON writes an indented JSON document and a trailing newline.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Table writes a header and rows as aligned columns.
func Table(w io.Writer, header []string, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if err := writeRow(tw, header); err != nil {
		return err
	}
	for _, row := range rows {
		if err := writeRow(tw, row); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeRow(w io.Writer, cols []string) error {
	for i, c := range cols {
		if i > 0 {
			if _, err := io.WriteString(w, "\t"); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, c); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// IsTerminal reports whether w is a character device.
// Anything that is not an *os.File is not a terminal, so tests and pipes get JSON.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// WantJSON is true for --output json, false for --output table, and
// true when --output is empty and stdout is not a terminal.
func WantJSON(stdout io.Writer, flag string) (bool, error) {
	switch flag {
	case "":
		return !IsTerminal(stdout), nil
	case "json":
		return true, nil
	case "table":
		return false, nil
	default:
		return false, fmt.Errorf("--output must be json or table, got %q", flag)
	}
}
