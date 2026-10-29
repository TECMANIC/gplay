package play

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/api/googleapi"

	"github.com/TECMANIC/gplay/internal/clierror"
)

func TestMapAPI(t *testing.T) {
	cases := []struct {
		name     string
		err      *googleapi.Error
		code     string
		exit     int
		retry    bool
		hintPart string
	}{
		{
			name: "in review",
			err:  &googleapi.Error{Code: 400, Message: "already in review", Body: `{"error":{"code":400,"message":"already in review","status":"FAILED_PRECONDITION","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"CHANGES_ALREADY_IN_REVIEW"}]}}`, Header: http.Header{"X-Goog-Request-Id": []string{"r1"}}},
			code: clierror.CodeChangesInReview, exit: 5, hintPart: "--cancel-in-review",
		},
		{
			name: "not sent",
			err:  &googleapi.Error{Code: 400, Message: "Please set the query parameter changesNotSentForReview to true."},
			code: clierror.CodeValidationFailed, exit: 6, hintPart: "--changes-not-sent-for-review",
		},
		{
			name: "draft app",
			err:  &googleapi.Error{Code: 400, Message: "Only releases with status draft may be created on a draft app"},
			code: clierror.CodeValidationFailed, exit: 6, hintPart: "--status draft",
		},
		{
			name: "signature",
			err:  &googleapi.Error{Code: 400, Message: "APK signature is invalid"},
			code: clierror.CodeValidationFailed, exit: 6, hintPart: "upload key",
		},
		{
			name: "version used",
			err:  &googleapi.Error{Code: 400, Message: "Version code 1 has already been used"},
			code: clierror.CodeValidationFailed, exit: 6, hintPart: "versionCode",
		},
		{
			name: "edit expired 404",
			err:  &googleapi.Error{Code: 404, Message: "The edit is no longer valid because it has expired"},
			code: clierror.CodeEditInvalidated, exit: 5,
		},
		{
			name: "edit words on 400 stay validation",
			err:  &googleapi.Error{Code: 400, Message: "The edit is no longer valid because it has expired"},
			code: clierror.CodeValidationFailed, exit: 6,
		},
		{
			name: "edit reason on 400",
			err:  &googleapi.Error{Code: 400, Message: "rejected", Errors: []googleapi.ErrorItem{{Reason: "EDIT_EXPIRED"}}},
			code: clierror.CodeEditInvalidated, exit: 5,
		},
		{
			name: "edit conflict message",
			err:  &googleapi.Error{Code: 409, Message: "The edit is no longer valid"},
			code: clierror.CodeEditInvalidated, exit: 5,
		},
		{
			name: "service disabled",
			err:  &googleapi.Error{Code: 403, Message: "Play Developer Reporting API has not been used", Errors: []googleapi.ErrorItem{{Reason: "accessNotConfigured"}}},
			code: clierror.CodeAPINotEnabled, exit: 3, hintPart: "playdeveloperreporting",
		},
		{
			name: "publisher disabled",
			err:  &googleapi.Error{Code: 403, Message: "androidpublisher is disabled", Errors: []googleapi.ErrorItem{{Reason: "SERVICE_DISABLED"}}},
			code: clierror.CodeAPINotEnabled, exit: 3, hintPart: "androidpublisher",
		},
		{
			name: "quota 403",
			err:  &googleapi.Error{Code: 403, Message: "quota", Errors: []googleapi.ErrorItem{{Reason: "quotaExceeded"}}},
			code: clierror.CodeRateLimited, exit: 7, retry: true,
		},
		{
			name: "rate 403",
			err:  &googleapi.Error{Code: 403, Message: "slow down", Errors: []googleapi.ErrorItem{{Reason: "rateLimitExceeded"}}},
			code: clierror.CodeRateLimited, exit: 7, retry: true,
		},
		{
			name: "plain 403",
			err:  &googleapi.Error{Code: 403, Message: "forbidden"},
			code: clierror.CodeAuthFailed, exit: 3, hintPart: "24 hours",
		},
		{
			name: "404",
			err:  &googleapi.Error{Code: 404, Message: "missing"},
			code: clierror.CodeNotFound,
			exit: 4,
		},
		{
			name:  "429",
			err:   &googleapi.Error{Code: 429, Message: "slow"},
			code:  clierror.CodeRateLimited,
			exit:  7,
			retry: true,
		},
		{
			name:  "500",
			err:   &googleapi.Error{Code: 500, Message: "boom"},
			code:  clierror.CodeUnexpected,
			exit:  1,
			retry: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MapAPI(tc.err)
			var ce *clierror.Error
			if !errors.As(got, &ce) {
				t.Fatalf("%T %v", got, got)
			}
			if ce.Code != tc.code || ce.ExitCode != tc.exit || ce.Retryable != tc.retry {
				t.Fatalf("%+v", ce)
			}
			if tc.hintPart != "" && !strings.Contains(ce.Hint, tc.hintPart) {
				t.Fatalf("hint %q", ce.Hint)
			}
		})
	}
}

func TestExternalAccountRejectedByTypedLoader(t *testing.T) {
	raw := []byte(`{"type":"external_account","audience":"x","subject_token_type":"urn:ietf:params:oauth:token-type:jwt","token_url":"https://example.invalid/token"}`)
	_, err := New(context.Background(), Options{JSON: raw})
	var ce *clierror.Error
	if !errors.As(err, &ce) || ce.ExitCode != 3 {
		t.Fatalf("%v", err)
	}
}
