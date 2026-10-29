package play

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"google.golang.org/api/googleapi"

	"github.com/TECMANIC/gplay/internal/clierror"
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

func mapGoogle(code int, status, reason, msg, allReasons string) *clierror.Error {
	blob := strings.ToLower(reason + " " + allReasons + " " + msg)
	if strings.Contains(strings.ToUpper(reason), "CHANGES_ALREADY_IN_REVIEW") || strings.Contains(strings.ToUpper(allReasons), "CHANGES_ALREADY_IN_REVIEW") {
		return clierror.ChangesInReview(msg, changesInReviewHint)
	}
	if editInvalid(reason, msg) {
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

func editInvalid(reason, msg string) bool {
	r := strings.ToUpper(reason)
	if strings.Contains(r, "EDIT_EXPIRED") || strings.Contains(r, "EDIT_NOT_FOUND") || strings.Contains(r, "EDIT_INVALID") {
		return true
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
