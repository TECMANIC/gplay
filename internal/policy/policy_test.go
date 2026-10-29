package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TECMANIC/gplay/internal/clierror"
)

func TestIsProduction(t *testing.T) {
	yes := []string{"production", " Production ", "wear:production", "TV:Production", "android_xr:production"}
	no := []string{"", "internal", "beta", "alpha", "qa", "wear:internal", ":production", "production:extra", "a:b:production", "prod"}
	for _, s := range yes {
		if !IsProduction(s) {
			t.Fatalf("want production: %q", s)
		}
	}
	for _, s := range no {
		if IsProduction(s) {
			t.Fatalf("want not production: %q", s)
		}
	}
}

func TestDecideTable(t *testing.T) {
	open := Default()
	open.AllowProduction = true
	open.AllowCancelInReview = true
	full := open
	full.MaxProductionUserFraction = 1

	cases := []struct {
		name  string
		p     Policy
		op    string
		track string
		flags Flags
		known *State
		code  int
		err   string
		state State
	}{
		{
			name: "testing default completed",
			p:    Default(), op: OpCreate, track: "internal",
			state: State{Status: "completed", FractionKnown: true},
		},
		{
			name: "beta is not locked",
			p:    Default(), op: OpCreate, track: "beta",
			flags: Flags{DryRun: true},
			state: State{Status: "completed", FractionKnown: true},
		},
		{
			name: "qa is not locked",
			p:    Default(), op: OpPromote, track: "qa",
			state: State{Status: "completed", FractionKnown: true},
		},
		{
			name: "alpha fraction",
			p:    Default(), op: OpCreate, track: "alpha",
			flags: Flags{FractionSet: true, Fraction: 0.1},
			state: State{Status: "inProgress", Fraction: 0.1, FractionKnown: true},
		},
		{
			name: "wear production dry-run blocked",
			p:    Default(), op: OpCreate, track: "wear:production",
			flags: Flags{Status: "completed", DryRun: true},
			code:  8, err: clierror.CodePolicyBlocked,
		},
		{
			name: "TV production blocked without flag",
			p:    open, op: OpRollout, track: "TV:Production",
			flags: Flags{FractionSet: true, Fraction: 0.1},
			code:  8, err: clierror.CodePolicyBlocked,
		},
		{
			name: "production blocked with flag and no policy",
			p:    Default(), op: OpHalt, track: "production",
			flags: Flags{AllowProduction: true, DryRun: true},
			code:  8, err: clierror.CodePolicyBlocked,
		},
		{
			name: "production create missing status is usage before policy",
			p:    Default(), op: OpCreate, track: "production",
			code: 2, err: clierror.CodeUsage,
		},
		{
			name: "production create completed needs acknowledgement",
			p:    open, op: OpCreate, track: "production",
			flags: Flags{Status: "completed", AllowProduction: true},
			code:  8, err: clierror.CodePolicyBlocked,
		},
		{
			name: "production create completed acknowledged",
			p:    open, op: OpCreate, track: " production ",
			flags: Flags{Status: "completed", AllowProduction: true, AllowFraction: "completed"},
			state: State{Status: "completed", FractionKnown: true},
		},
		{
			name: "cap 1 allows completed without the flag",
			p:    full, op: OpComplete, track: "production",
			flags: Flags{AllowProduction: true},
			state: State{Status: "completed", FractionKnown: true},
		},
		{
			name: "cap 1 allows the completed token",
			p:    full, op: OpComplete, track: "production",
			flags: Flags{AllowProduction: true, AllowFraction: "completed"},
			state: State{Status: "completed", FractionKnown: true},
		},
		{
			name: "completed rejects a numeric acknowledgement under the cap",
			p:    open, op: OpCreate, track: "production",
			flags: Flags{Status: "completed", AllowProduction: true, AllowFraction: "0.2"},
			code:  8, err: clierror.CodePolicyBlocked,
		},
		{
			name: "completed rejects --fraction",
			p:    Default(), op: OpCreate, track: "internal",
			flags: Flags{Status: "completed", FractionSet: true, Fraction: 0.2},
			code:  2, err: clierror.CodeUsage,
		},
		{
			name: "resume preflight defers the cap",
			p:    open, op: OpResume, track: "production",
			flags: Flags{AllowProduction: true, AllowFraction: "0.2"},
			state: State{Status: "inProgress"},
		},
		{
			name: "cap 1 rejects a fraction token on completed",
			p:    full, op: OpComplete, track: "production",
			flags: Flags{AllowProduction: true, AllowFraction: "0.2"},
			code:  8, err: clierror.CodePolicyBlocked,
		},
		{
			name: "rollout under cap",
			p:    open, op: OpRollout, track: "production",
			flags: Flags{FractionSet: true, Fraction: 0.2, AllowProduction: true},
			state: State{Status: "inProgress", Fraction: 0.2, FractionKnown: true},
		},
		{
			name: "rollout above cap",
			p:    open, op: OpRollout, track: "production",
			flags: Flags{FractionSet: true, Fraction: 0.5, AllowProduction: true},
			code:  8, err: clierror.CodePolicyBlocked,
		},
		{
			name: "rollout above cap with exact flag",
			p:    open, op: OpRollout, track: "production",
			flags: Flags{FractionSet: true, Fraction: 0.5, AllowProduction: true, AllowFraction: "0.5"},
			state: State{Status: "inProgress", Fraction: 0.5, FractionKnown: true},
		},
		{
			name: "flag that does not equal is blocked even under the cap",
			p:    open, op: OpRollout, track: "production",
			flags: Flags{FractionSet: true, Fraction: 0.1, AllowProduction: true, AllowFraction: "0.2"},
			code:  8, err: clierror.CodePolicyBlocked,
		},
		{
			name: "fraction 1 rejected",
			p:    open, op: OpRollout, track: "internal",
			flags: Flags{FractionSet: true, Fraction: 1},
			code:  2, err: clierror.CodeUsage,
		},
		{
			name: "fraction 0 rejected",
			p:    Default(), op: OpCreate, track: "internal",
			flags: Flags{FractionSet: true, Fraction: 0},
			code:  2, err: clierror.CodeUsage,
		},
		{
			name: "halt preflight defers the cap",
			p:    open, op: OpHalt, track: "production",
			flags: Flags{AllowProduction: true},
			state: State{Status: "halted"},
		},
		{
			name: "halt after read above cap",
			p:    open, op: OpHalt, track: "production",
			flags: Flags{AllowProduction: true},
			known: &State{Status: "halted", Fraction: 0.5, FractionKnown: true},
			code:  8, err: clierror.CodePolicyBlocked,
		},
		{
			name: "resume after read under cap",
			p:    open, op: OpResume, track: "wear:production",
			flags: Flags{AllowProduction: true, DryRun: true},
			known: &State{Status: "inProgress", Fraction: 0.2, FractionKnown: true},
			state: State{Status: "inProgress", Fraction: 0.2, FractionKnown: true},
		},
		{
			name: "resume after read above cap",
			p:    open, op: OpResume, track: "production",
			flags: Flags{AllowProduction: true},
			known: &State{Status: "inProgress", Fraction: 0.21, FractionKnown: true},
			code:  8, err: clierror.CodePolicyBlocked,
		},
		{
			name: "complete on production needs completed token",
			p:    open, op: OpComplete, track: "production",
			flags: Flags{AllowProduction: true, DryRun: true},
			code:  8, err: clierror.CodePolicyBlocked,
		},
		{
			name: "complete acknowledged",
			p:    open, op: OpComplete, track: "production",
			flags: Flags{AllowProduction: true, AllowFraction: "completed"},
			state: State{Status: "completed", FractionKnown: true},
		},
		{
			name: "draft production allowed without fraction flag",
			p:    open, op: OpCreate, track: "production",
			flags: Flags{Status: "draft", AllowProduction: true},
			state: State{Status: "draft", FractionKnown: true},
		},
		{
			name: "draft rejects fraction flag",
			p:    open, op: OpCreate, track: "production",
			flags: Flags{Status: "draft", AllowProduction: true, AllowFraction: "0.1"},
			code:  8, err: clierror.CodePolicyBlocked,
		},
		{
			name: "draft rejects --fraction",
			p:    Default(), op: OpCreate, track: "internal",
			flags: Flags{Status: "draft", FractionSet: true, Fraction: 0.1},
			code:  2, err: clierror.CodeUsage,
		},
		{
			name: "inProgress requires fraction",
			p:    Default(), op: OpCreate, track: "internal",
			flags: Flags{Status: "inProgress"},
			code:  2, err: clierror.CodeUsage,
		},
		{
			name: "inProgress rejects fraction 1",
			p:    Default(), op: OpCreate, track: "internal",
			flags: Flags{Status: "inProgress", FractionSet: true, Fraction: 1},
			code:  2, err: clierror.CodeUsage,
		},
		{
			name: "halted status with fraction",
			p:    Default(), op: OpPromote, track: "internal",
			flags: Flags{Status: "halted", FractionSet: true, Fraction: 0.4},
			state: State{Status: "halted", Fraction: 0.4, FractionKnown: true},
		},
		{
			name: "bad status",
			p:    Default(), op: OpCreate, track: "internal",
			flags: Flags{Status: "live"},
			code:  2, err: clierror.CodeUsage,
		},
		{
			name: "rollout missing fraction",
			p:    Default(), op: OpRollout, track: "internal",
			code: 2, err: clierror.CodeUsage,
		},
		{
			name: "unknown operation",
			p:    Default(), op: "explode", track: "internal",
			code: 2, err: clierror.CodeUsage,
		},
		{
			name: "bad acknowledgement token is usage",
			p:    open, op: OpCreate, track: "production",
			flags: Flags{Status: "completed", AllowProduction: true, AllowFraction: "nope"},
			code:  2, err: clierror.CodeUsage,
		},
		{
			name: "cancel blocked",
			p:    Default(), op: OpUpload, track: "",
			flags: Flags{CancelInReview: true, DryRun: true},
			code:  8, err: clierror.CodePolicyBlocked,
		},
		{
			name: "cancel allowed",
			p:    open, op: OpUpload,
			flags: Flags{CancelInReview: true},
		},
		{
			name: "upload ignores a production-looking absence",
			p:    Default(), op: OpUpload, track: "production",
		},
		{
			name: "completed token on a fraction is not equal",
			p:    open, op: OpRollout, track: "production",
			flags: Flags{FractionSet: true, Fraction: 0.5, AllowProduction: true, AllowFraction: "completed"},
			code:  8, err: clierror.CodePolicyBlocked,
		},
		{
			name: "allow-production on internal is usage",
			p:    Default(), op: OpCreate, track: "internal",
			flags: Flags{AllowProduction: true},
			code:  2, err: clierror.CodeUsage,
		},
		{
			name: "allow-production-fraction on beta is usage",
			p:    Default(), op: OpCreate, track: "beta",
			flags: Flags{AllowFraction: "0.2"},
			code:  2, err: clierror.CodeUsage,
		},
		{
			name: "production flags on an empty track are usage",
			p:    Default(), op: OpUpload, track: "",
			flags: Flags{AllowProduction: true, AllowFraction: "completed"},
			code:  2, err: clierror.CodeUsage,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decide(tc.p, tc.op, tc.track, tc.flags, tc.known)
			if tc.code == 0 {
				if err != nil {
					t.Fatalf("unexpected %s: %s", err.Code, err.Message)
				}
				if got != tc.state {
					t.Fatalf("state %+v want %+v", got, tc.state)
				}
				return
			}
			if err == nil || err.ExitCode != tc.code || err.Code != tc.err {
				t.Fatalf("got %+v", err)
			}
			if tc.flags.DryRun && err.Code == clierror.CodePolicyBlocked && !strings.Contains(err.Hint, "--dry-run does not bypass") {
				t.Fatalf("dry-run hint missing: %s", err.Hint)
			}
		})
	}
}

func TestDecisionEdges(t *testing.T) {
	if noteDryRun(nil, true) != nil {
		t.Fatal("nil error")
	}
	hinted := noteDryRun(clierror.PolicyBlocked("blocked", ""), true)
	if hinted == nil || hinted.Hint != "--dry-run does not bypass this check" {
		t.Fatalf("%+v", hinted)
	}
	err := capFraction(Default(), Flags{AllowFraction: "nope"}, 0.1)
	if err == nil || err.ExitCode != 2 {
		t.Fatalf("%+v", err)
	}
}

func TestLoadUsesWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	got, err := Load()
	if err != nil || got != Default() {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestLoadDiscovery(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	outside := filepath.Join(root, "outside")
	proj := filepath.Join(home, "proj")
	sub := filepath.Join(proj, "sub")
	for _, d := range []string{home, outside, proj, sub} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writePolicy(t, outside, `{"allowProduction":true}`)
	got, err := LoadFrom(sub, home)
	if err != nil {
		t.Fatal(err)
	}
	if got.AllowProduction || got.MaxProductionUserFraction != DefaultMaxFraction {
		t.Fatalf("walked past home: %+v", got)
	}

	writePolicy(t, home, `{"allowProduction":true,"maxProductionUserFraction":0.5}`)
	got, err = LoadFrom(sub, home)
	if err != nil {
		t.Fatal(err)
	}
	if !got.AllowProduction || got.MaxProductionUserFraction != 0.5 {
		t.Fatalf("home file: %+v", got)
	}

	if err := os.Mkdir(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = LoadFrom(sub, home)
	if err != nil {
		t.Fatal(err)
	}
	if got.AllowProduction {
		t.Fatal("did not stop at .git")
	}

	writePolicy(t, sub, `{"allowCancelInReview":true}`)
	got, err = LoadFrom(sub, home)
	if err != nil {
		t.Fatal(err)
	}
	if !got.AllowCancelInReview || got.AllowProduction {
		t.Fatalf("closest file: %+v", got)
	}
}

func TestLoadRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()
	cases := []string{
		`{"allowProduction":true,"lockedTracks":["beta"]}`,
		`{"maxProductionUserFraction":0}`,
		`{"maxProductionUserFraction":1.1}`,
		`{"allowProduction":"yes"}`,
		`{}{}`,
		`not json`,
	}
	for _, body := range cases {
		writePolicy(t, dir, body)
		_, err := LoadFrom(dir, filepath.Join(dir, "no-such-home"))
		if err == nil || err.Code != clierror.CodeConfigInvalid || err.ExitCode != 2 {
			t.Fatalf("body %s → %+v", body, err)
		}
	}
	if err := os.Remove(filepath.Join(dir, ".gplay", "policy.json")); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFrom(dir, filepath.Join(dir, "missing-home"))
	if err != nil || got != Default() {
		t.Fatalf("missing: %+v %v", got, err)
	}
}

func TestLoadUnreadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gplay", "policy.json")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFrom(dir, dir)
	if err == nil || err.Code != clierror.CodeConfigInvalid {
		t.Fatalf("%+v", err)
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(`{"allowProduction":true,"allowCancelInReview":false,"maxProductionUserFraction":0.2}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"maxProductionUserFraction":1}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		got, err := parse(body)
		if err != nil {
			if err.ExitCode != 2 || err.Code != clierror.CodeConfigInvalid {
				t.Fatalf("%+v", err)
			}
			return
		}
		if !(got.MaxProductionUserFraction > 0 && got.MaxProductionUserFraction <= 1) {
			t.Fatalf("%+v", got)
		}
	})
}

func writePolicy(t *testing.T, dir, body string) {
	t.Helper()
	path := filepath.Join(dir, ".gplay")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "policy.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
