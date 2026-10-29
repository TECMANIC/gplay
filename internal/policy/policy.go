// Package policy is the pure production lock.
// It does no I/O. Load reads the policy file; Decide does not.
package policy

import (
	"strconv"
	"strings"

	"github.com/TECMANIC/gplay/internal/clierror"
)

const (
	// DefaultMaxFraction is the cap when the policy file omits the key.
	DefaultMaxFraction = 0.2

	OpCreate   = "create"
	OpPromote  = "promote"
	OpRollout  = "rollout"
	OpHalt     = "halt"
	OpResume   = "resume"
	OpComplete = "complete"
	OpUpload   = "upload"
)

// Policy is the parsed .gplay/policy.json, or the defaults when the file is absent.
type Policy struct {
	AllowProduction           bool
	AllowCancelInReview       bool
	MaxProductionUserFraction float64
}

// Default is the policy used when no file is found.
func Default() Policy {
	return Policy{MaxProductionUserFraction: DefaultMaxFraction}
}

// Flags are the acknowledgement flags and the status inputs.
// DryRun is an input so a caller cannot forget it. It never permits a write.
type Flags struct {
	Status          string
	FractionSet     bool
	Fraction        float64
	AllowProduction bool
	// AllowFraction is the raw --allow-production-fraction value.
	// Empty means the flag was not passed.
	AllowFraction  string
	CancelInReview bool
	DryRun         bool
}

// State is the release that would be written.
// FractionKnown is false for halt and resume until tracks.get has been read.
type State struct {
	Status        string
	Fraction      float64
	FractionKnown bool
}

// IsProduction reports whether track is production or <prefix>:production.
// Comparison is case-insensitive after trimming. Exactly one colon is required
// for the prefixed form, and the prefix must be non-empty.
func IsProduction(track string) bool {
	t := strings.ToLower(strings.TrimSpace(track))
	if t == "production" {
		return true
	}
	prefix, rest, ok := strings.Cut(t, ":")
	if !ok || prefix == "" || strings.Contains(rest, ":") {
		return false
	}
	return rest == "production"
}

// Decide returns the resulting release state, or the first applicable error.
// Order is usage, then the production lock, then the fraction cap, then
// cancel-in-review. A non-nil known state is the resulting state for halt
// and resume after the track has been read. Other operations derive the
// state from flags.
func Decide(p Policy, op, track string, flags Flags, known *State) (State, *clierror.Error) {
	if err := validateAck(flags); err != nil {
		return State{}, err
	}
	state, err := resulting(op, track, flags, known)
	if err != nil {
		return State{}, err
	}
	if err := lock(p, op, track, flags); err != nil {
		return state, noteDryRun(err, flags.DryRun)
	}
	if err := acknowledge(p, op, track, flags, state); err != nil {
		return state, noteDryRun(err, flags.DryRun)
	}
	if err := cancel(p, flags); err != nil {
		return state, noteDryRun(err, flags.DryRun)
	}
	return state, nil
}

func noteDryRun(err *clierror.Error, dry bool) *clierror.Error {
	if err == nil || !dry {
		return err
	}
	cp := *err
	if cp.Hint != "" {
		cp.Hint += "; --dry-run does not bypass this check"
	} else {
		cp.Hint = "--dry-run does not bypass this check"
	}
	return &cp
}

func validateAck(flags Flags) *clierror.Error {
	if flags.AllowFraction == "" {
		return nil
	}
	_, err := parseAck(flags.AllowFraction)
	return err
}

type ack struct {
	completed bool
	value     float64
}

func parseAck(s string) (ack, *clierror.Error) {
	if s == "completed" {
		return ack{completed: true}, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return ack{}, clierror.Usage("--allow-production-fraction must be a number or 'completed'", "example: --allow-production-fraction 0.2")
	}
	return ack{value: v}, nil
}

func resulting(op, track string, flags Flags, known *State) (State, *clierror.Error) {
	switch op {
	case OpCreate, OpPromote:
		return resolveCreate(track, flags)
	case OpRollout:
		if !flags.FractionSet {
			return State{}, clierror.Usage("rollout requires --fraction", "full rollout is release complete")
		}
		if !openFraction(flags.Fraction) {
			return State{}, clierror.Usage("--fraction must be in (0, 1)", "full rollout is release complete")
		}
		return State{Status: "inProgress", Fraction: flags.Fraction, FractionKnown: true}, nil
	case OpHalt:
		if known == nil {
			return State{Status: "halted"}, nil
		}
		return *known, nil
	case OpResume:
		if known == nil {
			return State{Status: "inProgress"}, nil
		}
		return *known, nil
	case OpComplete:
		return State{Status: "completed", FractionKnown: true}, nil
	case OpUpload:
		return State{}, nil
	default:
		return State{}, clierror.Usage("unknown operation "+op, "")
	}
}

func resolveCreate(track string, flags Flags) (State, *clierror.Error) {
	switch {
	case flags.FractionSet && flags.Status == "":
		if !openFraction(flags.Fraction) {
			return State{}, clierror.Usage("--fraction must be in (0, 1)", "full rollout is release complete")
		}
		return State{Status: "inProgress", Fraction: flags.Fraction, FractionKnown: true}, nil
	case flags.Status == "inProgress" || flags.Status == "halted":
		if !flags.FractionSet {
			return State{}, clierror.Usage("--status "+flags.Status+" requires --fraction", "")
		}
		if !openFraction(flags.Fraction) {
			return State{}, clierror.Usage("--fraction must be in (0, 1)", "full rollout is release complete")
		}
		return State{Status: flags.Status, Fraction: flags.Fraction, FractionKnown: true}, nil
	case flags.Status == "completed" || flags.Status == "draft":
		if flags.FractionSet {
			return State{}, clierror.Usage("--fraction cannot be combined with --status "+flags.Status, "")
		}
		return State{Status: flags.Status, FractionKnown: true}, nil
	case flags.Status == "" && !flags.FractionSet:
		if IsProduction(track) {
			return State{}, clierror.Usage("production requires --fraction or --status", "testing tracks default to completed")
		}
		return State{Status: "completed", FractionKnown: true}, nil
	default:
		return State{}, clierror.Usage("--status must be draft, inProgress, halted, or completed", "")
	}
}

func openFraction(f float64) bool {
	return f > 0 && f < 1
}

func lock(p Policy, op, track string, flags Flags) *clierror.Error {
	if op == OpUpload || !IsProduction(track) {
		return nil
	}
	if flags.AllowProduction && p.AllowProduction {
		return nil
	}
	var hint string
	switch {
	case flags.AllowProduction && !p.AllowProduction:
		hint = "set allowProduction to true in .gplay/policy.json"
	case !flags.AllowProduction && p.AllowProduction:
		hint = "pass --allow-production"
	default:
		hint = "set allowProduction to true in .gplay/policy.json and pass --allow-production"
	}
	return clierror.PolicyBlocked("refusing write to production track "+strings.TrimSpace(track), hint)
}

func acknowledge(p Policy, op, track string, flags Flags, state State) *clierror.Error {
	if op == OpUpload || !IsProduction(track) {
		return nil
	}
	if state.Status != "completed" && state.Status != "draft" && !state.FractionKnown {
		return nil
	}
	if state.Status == "draft" {
		if flags.AllowFraction != "" {
			return clierror.PolicyBlocked("--allow-production-fraction does not apply to a draft release", "omit the flag")
		}
		return nil
	}
	if state.Status == "completed" {
		return capCompleted(p, flags)
	}
	return capFraction(p, flags, state.Fraction)
}

func capCompleted(p Policy, flags Flags) *clierror.Error {
	if p.MaxProductionUserFraction >= 1 {
		if flags.AllowFraction == "" || flags.AllowFraction == "completed" {
			return nil
		}
		return clierror.PolicyBlocked(
			"--allow-production-fraction must equal 'completed' for a completed release",
			"pass --allow-production-fraction completed, or omit it when maxProductionUserFraction is 1",
		)
	}
	if flags.AllowFraction != "completed" {
		return clierror.PolicyBlocked(
			"a completed production release is above maxProductionUserFraction",
			"pass --allow-production-fraction completed",
		)
	}
	return nil
}

func capFraction(p Policy, flags Flags, fraction float64) *clierror.Error {
	formatted := FormatFraction(fraction)
	if flags.AllowFraction == "" {
		if fraction > p.MaxProductionUserFraction {
			return clierror.PolicyBlocked(
				"resulting fraction "+formatted+" is above maxProductionUserFraction",
				"pass --allow-production-fraction "+formatted,
			)
		}
		return nil
	}
	parsed, err := parseAck(flags.AllowFraction)
	if err != nil {
		return err
	}
	if parsed.completed || parsed.value != fraction {
		return clierror.PolicyBlocked(
			"--allow-production-fraction must equal the resulting fraction "+formatted,
			"pass --allow-production-fraction "+formatted,
		)
	}
	return nil
}

func cancel(p Policy, flags Flags) *clierror.Error {
	if !flags.CancelInReview || p.AllowCancelInReview {
		return nil
	}
	return clierror.PolicyBlocked(
		"--cancel-in-review is not allowed by policy",
		"set allowCancelInReview to true in .gplay/policy.json",
	)
}

// FormatFraction renders a fraction the same way the cap hint does.
func FormatFraction(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}
