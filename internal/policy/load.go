package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/TECMANIC/gplay/internal/clierror"
)

// Load reads .gplay/policy.json starting at the working directory.
// A missing file is Default. A file that exists and is invalid is exit 2.
func Load() (Policy, *clierror.Error) {
	wd, err := os.Getwd()
	if err != nil {
		return Policy{}, clierror.Config("cannot determine the working directory", "")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Policy{}, clierror.Config("cannot determine the home directory", "")
	}
	return LoadFrom(wd, home)
}

// LoadFrom walks dir, then each parent, and stops after checking the first
// directory that contains .git, and after checking home and the filesystem root.
// The closest file wins.
func LoadFrom(dir, home string) (Policy, *clierror.Error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Policy{}, clierror.Config("cannot resolve the working directory", "")
	}
	homeAbs := ""
	if home != "" {
		homeAbs, err = filepath.Abs(home)
		if err != nil {
			return Policy{}, clierror.Config("cannot resolve the home directory", "")
		}
	}
	for {
		path := filepath.Join(abs, ".gplay", "policy.json")
		body, readErr := os.ReadFile(path)
		if readErr == nil {
			return parse(body)
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			return Policy{}, clierror.Config("cannot read "+path, "")
		}
		stop := false
		if homeAbs != "" && abs == homeAbs {
			stop = true
		}
		if _, err := os.Stat(filepath.Join(abs, ".git")); err == nil {
			stop = true
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			stop = true
		}
		if stop {
			return Default(), nil
		}
		abs = parent
	}
}

func parse(body []byte) (Policy, *clierror.Error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var raw struct {
		AllowProduction           *bool    `json:"allowProduction"`
		AllowCancelInReview       *bool    `json:"allowCancelInReview"`
		MaxProductionUserFraction *float64 `json:"maxProductionUserFraction"`
	}
	if err := dec.Decode(&raw); err != nil {
		return Policy{}, clierror.Config("policy file is not valid JSON", "expected allowProduction, allowCancelInReview, and maxProductionUserFraction")
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Policy{}, clierror.Config("policy file contains extra data", "")
	}
	out := Default()
	if raw.AllowProduction != nil {
		out.AllowProduction = *raw.AllowProduction
	}
	if raw.AllowCancelInReview != nil {
		out.AllowCancelInReview = *raw.AllowCancelInReview
	}
	if raw.MaxProductionUserFraction != nil {
		v := *raw.MaxProductionUserFraction
		if !(v > 0 && v <= 1) {
			return Policy{}, clierror.Config("maxProductionUserFraction must be in (0, 1]", "")
		}
		out.MaxProductionUserFraction = v
	}
	return out, nil
}
