package play

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"cloud.google.com/go/auth"
	"google.golang.org/api/googleapi"

	"github.com/TECMANIC/gplay/internal/clierror"
	"github.com/TECMANIC/gplay/internal/transport"
)

var (
	oauthCodePattern   = regexp.MustCompile(`"error"\s*:\s*"(invalid_grant|invalid_client|unauthorized_client)"`)
	tokenStatusPattern = regexp.MustCompile(`status code (\d{3})`)
)

const (
	publisherEnableURL  = "https://console.cloud.google.com/apis/library/androidpublisher.googleapis.com"
	reportingEnableURL  = "https://console.cloud.google.com/apis/library/playdeveloperreporting.googleapis.com"
	changesInReviewHint = "wait for the in-review change to finish, or pass --cancel-in-review with allowCancelInReview set in .gplay/policy.json"
	notSentHint         = "re-run with --changes-not-sent-for-review"
	draftHint           = "re-run with --status draft"
)

// MapAPI converts a Google client error into the process contract.
// Unknown errors are returned unchanged when they are already *clierror.Error.
func MapAPI(err error) error {
	if err == nil {
		return nil
	}
	var ce *clierror.Error
	if errors.As(err, &ce) && ce != nil {
		return ce
	}
	var te *transport.TokenError
	if errors.As(err, &te) {
		return mapToken(err)
	}
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr == nil {
		return clierror.From(err)
	}
	status, reasons := googleDetails(gerr)
	reason := first(reasons)
	reqID := ""
	if gerr.Header != nil {
		reqID = gerr.Header.Get("x-goog-request-id")
	}
	msg := gerr.Message
	if msg == "" {
		msg = http.StatusText(gerr.Code)
	}
	mapped := mapGoogle(gerr.Code, status, reason, msg, strings.Join(reasons, " "))
	return mapped.WithGoogle(gerr.Code, status, reason, reqID, "")
}

// mapToken classifies a credential-provider failure.
// Cancel is exit 130. A timeout, a DNS failure, and a connect failure go
// through clierror.From, which makes them exit 1 and retryable. A
// token-endpoint 5xx is the same exit and flag with a fixed message, because
// From would copy the response body. A 4xx, an OAuth rejection, or a key
// that does not parse is exit 3. The OAuth error code is included when it is
// one of the three stable values. The description, the URL, and the body are not.
func mapToken(err error) error {
	if tokenStatus(err) >= 500 {
		return clierror.Unexpected("token endpoint failed", true)
	}
	cause := tokenCause(err)
	if strings.Contains(cause.Error(), "not allowlisted") {
		return clierror.Auth("credential host is not allowlisted", "check the credential URL")
	}
	mapped := clierror.From(cause)
	if mapped.ExitCode == 130 || (mapped.Code == clierror.CodeUnexpected && mapped.Retryable) {
		return mapped
	}
	msg := "credentials were rejected"
	if code := oauthCode(err); code != "" {
		msg += ": " + code
	}
	return clierror.Auth(msg, "check the configured credentials")
}

func tokenCause(err error) error {
	var te *transport.TokenError
	if errors.As(err, &te) && te.Unwrap() != nil {
		return te.Unwrap()
	}
	return err
}

func tokenStatus(err error) int {
	var ae *auth.Error
	if errors.As(err, &ae) {
		if ae.Response != nil {
			return ae.Response.StatusCode
		}
		return 0
	}
	m := tokenStatusPattern.FindStringSubmatch(tokenCause(err).Error())
	if m == nil {
		return 0
	}
	code, convErr := strconv.Atoi(m[1])
	if convErr != nil {
		return 0
	}
	return code
}

func oauthCode(err error) string {
	var ae *auth.Error
	if errors.As(err, &ae) && len(ae.Body) > 0 {
		if m := oauthCodePattern.FindSubmatch(ae.Body); m != nil {
			return string(m[1])
		}
	}
	if m := oauthCodePattern.FindStringSubmatch(tokenCause(err).Error()); m != nil {
		return m[1]
	}
	return ""
}

func mapGoogle(code int, status, reason, msg, allReasons string) *clierror.Error {
	blob := strings.ToLower(reason + " " + allReasons + " " + msg)
	if strings.Contains(strings.ToUpper(reason), "CHANGES_ALREADY_IN_REVIEW") || strings.Contains(strings.ToUpper(allReasons), "CHANGES_ALREADY_IN_REVIEW") {
		return clierror.ChangesInReview(msg, changesInReviewHint)
	}
	if editInvalid(code, reason, msg) {
		return clierror.EditInvalidated(msg, "the edit was discarded; run the command again")
	}
	if code == http.StatusBadRequest {
		return clierror.Validation(msg, validationHint(msg))
	}
	if code == http.StatusNotFound {
		return clierror.NotFound(msg, "")
	}
	if code == http.StatusConflict {
		return clierror.EditInvalidated(msg, "the edit was discarded; run the command again")
	}
	if code == http.StatusTooManyRequests || quota(blob) {
		return clierror.RateLimited(msg, "wait and retry")
	}
	if code == http.StatusUnauthorized {
		return clierror.Auth(msg, "check the service-account key")
	}
	if code == http.StatusForbidden {
		if strings.Contains(blob, "service_disabled") || strings.Contains(blob, "accessnotconfigured") {
			return clierror.APINotEnabled(msg, enableHint(msg))
		}
		return clierror.Auth(msg, "the service account may lack a Play permission, or a new grant may still be propagating (up to 24 hours)")
	}
	if code >= 500 {
		return clierror.Unexpected(msg, true)
	}
	return clierror.Unexpected(msg, false)
}

func editInvalid(code int, reason, msg string) bool {
	r := strings.ToUpper(reason)
	if strings.Contains(r, "EDIT_EXPIRED") || strings.Contains(r, "EDIT_NOT_FOUND") || strings.Contains(r, "EDIT_INVALID") {
		return true
	}
	if code != http.StatusNotFound && code != http.StatusConflict {
		return false
	}
	m := strings.ToLower(msg)
	return strings.Contains(m, "edit") && (strings.Contains(m, "invalid") || strings.Contains(m, "expired") || strings.Contains(m, "no longer"))
}

func quota(blob string) bool {
	return strings.Contains(blob, "quotaexceeded") || strings.Contains(blob, "ratelimitexceeded") || strings.Contains(blob, "userratelimitexceeded")
}

func validationHint(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(msg, "changesNotSentForReview"):
		return notSentHint
	case strings.Contains(m, "draft") && strings.Contains(m, "status"):
		return draftHint
	case strings.Contains(m, "apk signature is invalid") || strings.Contains(m, "upload certificate"):
		return "the AAB is signed with a different upload key than Play Console has on file"
	case strings.Contains(m, "version code") && strings.Contains(m, "already"):
		return "bump versionCode; it must be higher than every code already uploaded"
	case strings.Contains(m, "release notes") && (strings.Contains(m, "long") || strings.Contains(m, "500")):
		return "shorten the note to 500 characters"
	default:
		return ""
	}
}

func enableHint(msg string) string {
	m := strings.ToLower(msg)
	if strings.Contains(m, "playdeveloperreporting") || strings.Contains(m, "reporting") {
		return "enable the API: " + reportingEnableURL
	}
	return "enable the API: " + publisherEnableURL
}

func googleDetails(gerr *googleapi.Error) (status string, reasons []string) {
	for _, item := range gerr.Errors {
		if item.Reason != "" {
			reasons = append(reasons, item.Reason)
		}
	}
	var body struct {
		Error struct {
			Status  string           `json:"status"`
			Details []map[string]any `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(gerr.Body), &body) == nil {
		status = body.Error.Status
		for _, d := range body.Error.Details {
			if r, ok := d["reason"].(string); ok && r != "" {
				reasons = append(reasons, r)
			}
		}
	}
	for _, d := range gerr.Details {
		m, ok := d.(map[string]any)
		if !ok {
			continue
		}
		if r, ok := m["reason"].(string); ok && r != "" {
			reasons = append(reasons, r)
		}
	}
	return status, reasons
}

func first(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[0]
}
