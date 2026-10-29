// Package clierror is the process-wide error contract.
// Every non-zero exit writes exactly one JSON object as the last line of stderr.
package clierror

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"syscall"
)

const (
	CodeUnexpected       = "unexpected"
	CodeUsage            = "usage"
	CodeConfigInvalid    = "config_invalid"
	CodeAuthFailed       = "auth_failed"
	CodeAPINotEnabled    = "api_not_enabled"
	CodeNotFound         = "not_found"
	CodeChangesInReview  = "changes_in_review"
	CodeEditInvalidated  = "edit_invalidated"
	CodeValidationFailed = "validation_failed"
	CodeRateLimited      = "rate_limited"
	CodePolicyBlocked    = "policy_blocked"
	CodeInterrupted      = "interrupted"
)

// Error is the JSON object agents parse. Optional fields are omitted, never null.
type Error struct {
	Code         string `json:"code"`
	ExitCode     int    `json:"exit_code"`
	Message      string `json:"message"`
	Hint         string `json:"hint,omitempty"`
	Retryable    bool   `json:"retryable"`
	HTTPStatus   int    `json:"http_status,omitempty"`
	GoogleStatus string `json:"google_status,omitempty"`
	GoogleReason string `json:"google_reason,omitempty"`
	EditID       string `json:"edit_id,omitempty"`
	RequestID    string `json:"request_id,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func newError(code string, exit int, message, hint string, retryable bool) *Error {
	return &Error{
		Code:      code,
		ExitCode:  exit,
		Message:   message,
		Hint:      hint,
		Retryable: retryable,
	}
}

func Usage(message, hint string) *Error {
	return newError(CodeUsage, 2, message, hint, false)
}

func Config(message, hint string) *Error {
	return newError(CodeConfigInvalid, 2, message, hint, false)
}

func Auth(message, hint string) *Error {
	return newError(CodeAuthFailed, 3, message, hint, false)
}

func APINotEnabled(message, hint string) *Error {
	return newError(CodeAPINotEnabled, 3, message, hint, false)
}

func NotFound(message, hint string) *Error {
	return newError(CodeNotFound, 4, message, hint, false)
}

func ChangesInReview(message, hint string) *Error {
	return newError(CodeChangesInReview, 5, message, hint, false)
}

func EditInvalidated(message, hint string) *Error {
	return newError(CodeEditInvalidated, 5, message, hint, false)
}

func Validation(message, hint string) *Error {
	return newError(CodeValidationFailed, 6, message, hint, false)
}

func RateLimited(message, hint string) *Error {
	return newError(CodeRateLimited, 7, message, hint, true)
}

func PolicyBlocked(message, hint string) *Error {
	return newError(CodePolicyBlocked, 8, message, hint, false)
}

func Interrupted() *Error {
	return newError(CodeInterrupted, 130, "interrupted", "the open edit was discarded when this process had created one", false)
}

func Unexpected(message string, retryable bool) *Error {
	return newError(CodeUnexpected, 1, message, "", retryable)
}

// WithGoogle returns a copy with API details attached.
func (e *Error) WithGoogle(httpStatus int, googleStatus, reason, requestID, editID string) *Error {
	if e == nil {
		return nil
	}
	cp := *e
	cp.HTTPStatus = httpStatus
	cp.GoogleStatus = googleStatus
	cp.GoogleReason = reason
	cp.RequestID = requestID
	cp.EditID = editID
	return &cp
}

// WithHint returns a copy with hint set when the receiver's hint is empty.
func (e *Error) WithHint(hint string) *Error {
	if e == nil || hint == "" || e.Hint != "" {
		return e
	}
	cp := *e
	cp.Hint = hint
	return &cp
}

// From maps an arbitrary error onto the contract. A *Error passes through.
func From(err error) *Error {
	if err == nil {
		return nil
	}
	var ce *Error
	if errors.As(err, &ce) && ce != nil {
		return ce
	}
	if errors.Is(err, context.Canceled) {
		return Interrupted()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Unexpected("request timed out", true)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return Unexpected("request timed out", true)
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return Unexpected("name lookup failed", true)
	}
	if connectFailed(err) {
		return Unexpected("connection failed", true)
	}
	return Unexpected(err.Error(), false)
}

func connectFailed(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) {
		return true
	}
	var op *net.OpError
	if !errors.As(err, &op) {
		return false
	}
	if op.Op != "dial" && op.Op != "connect" {
		return false
	}
	var dnsErr *net.DNSError
	return !errors.As(err, &dnsErr)
}

// ExitCode is 0 for a nil error, the contract code for *Error, and 1 otherwise.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	return From(err).ExitCode
}

// Write encodes e as one JSON line. It is the last thing a failing command writes.
func Write(w io.Writer, e *Error) {
	if e == nil {
		e = Unexpected("unknown error", false)
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(e)
}

// CodeInfo is one stable code advertised by `gplay schema`.
type CodeInfo struct {
	Code      string `json:"code"`
	ExitCode  int    `json:"exit_code"`
	Retryable bool   `json:"retryable"`
	Meaning   string `json:"meaning"`
}

// Codes is the stable enum, in exit-code order.
func Codes() []CodeInfo {
	return []CodeInfo{
		{Code: CodeUnexpected, ExitCode: 1, Retryable: false, Meaning: "5xx, timeout, DNS, or a connect failure set retryable on that error; other unexpected failures do not"},
		{Code: CodeUsage, ExitCode: 2, Meaning: "unknown command, missing flag, or conflicting flags"},
		{Code: CodeConfigInvalid, ExitCode: 2, Meaning: "policy or config file is unreadable, has unknown keys, or has the wrong tool id"},
		{Code: CodeAuthFailed, ExitCode: 3, Meaning: "credentials missing or rejected, or Play returned 401/403"},
		{Code: CodeAPINotEnabled, ExitCode: 3, Meaning: "403 SERVICE_DISABLED or accessNotConfigured"},
		{Code: CodeNotFound, ExitCode: 4, Meaning: "HTTP 404, unknown track, or version code not on the track"},
		{Code: CodeChangesInReview, ExitCode: 5, Meaning: "commit refused because a change is already in review"},
		{Code: CodeEditInvalidated, ExitCode: 5, Meaning: "the edit expired or was invalidated"},
		{Code: CodeValidationFailed, ExitCode: 6, Meaning: "Play or a local check rejected the upload or release"},
		{Code: CodeRateLimited, ExitCode: 7, Retryable: true, Meaning: "HTTP 429 or a quota 403"},
		{Code: CodePolicyBlocked, ExitCode: 8, Meaning: "refused by the production lock, the rollout cap, or cancel-in-review policy"},
		{Code: CodeInterrupted, ExitCode: 130, Meaning: "SIGINT, SIGTERM, or a canceled context"},
	}
}
