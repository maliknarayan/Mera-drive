package google

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
)

// apiErrorEnvelope is the Google JSON error shape.
//
//	{"error":{"code":403,"message":"...","errors":[{"reason":"rateLimitExceeded"}]}}
type apiErrorEnvelope struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Errors  []struct {
			Reason  string `json:"reason"`
			Message string `json:"message"`
			Domain  string `json:"domain"`
		} `json:"errors"`
	} `json:"error"`
}

// reason returns the first machine-readable reason Google gave, if any.
func (e *apiErrorEnvelope) reason() string {
	if len(e.Error.Errors) > 0 {
		return e.Error.Errors[0].Reason
	}
	return ""
}

// mapAPIError translates a Drive API failure into a stable app error.
//
// The reason string matters more than the status code: a 403 can mean "you are
// being throttled" (retry) or "this Drive is full" (do not retry), and the two
// need completely different UI.
func mapAPIError(status int, body []byte, retryAfter time.Duration) *apperr.Error {
	var envelope apiErrorEnvelope
	_ = json.Unmarshal(body, &envelope)

	reason := envelope.reason()
	message := strings.TrimSpace(envelope.Error.Message)

	switch reason {
	case "rateLimitExceeded", "userRateLimitExceeded", "sharingRateLimitExceeded":
		err := apperr.RateLimited("Google is rate limiting requests. Retrying shortly.")
		if retryAfter > 0 {
			err = err.WithDetail("retry_after_seconds", int(retryAfter.Seconds()))
		}
		return err

	case "storageQuotaExceeded":
		return apperr.QuotaExceeded("This Google Drive is out of space.")

	case "insufficientFilePermissions", "insufficientPermissions", "forbidden":
		return apperr.InsufficientScope(
			"SangamDrive does not have permission for that. Upgrade this account to full Drive access.",
		)

	case "authError", "invalidCredentials", "invalid_grant":
		return apperr.ReauthRequired("Google rejected the stored credentials. Please reconnect this account.")

	case "notFound", "fileNotFound":
		return apperr.NotFound("That file or folder no longer exists in Google Drive.")

	case "cannotModifyInheritedTeamDrivePermission", "cannotDeleteResource":
		return apperr.Forbidden(fallback(message, "Google Drive refused that operation."))

	case "activeItemCreationLimitExceeded", "numChildrenInNonRootLimitExceeded":
		return apperr.Conflict(fallback(message, "Google Drive rejected that: a folder limit was reached."))
	}

	switch status {
	case http.StatusUnauthorized:
		return apperr.ReauthRequired("Google rejected the stored credentials. Please reconnect this account.")
	case http.StatusForbidden:
		return apperr.Forbidden(fallback(message, "Google Drive refused that request."))
	case http.StatusNotFound:
		return apperr.NotFound("That file or folder no longer exists in Google Drive.")
	case http.StatusTooManyRequests:
		err := apperr.RateLimited("Google is rate limiting requests. Retrying shortly.")
		if retryAfter > 0 {
			err = err.WithDetail("retry_after_seconds", int(retryAfter.Seconds()))
		}
		return err
	case http.StatusRequestEntityTooLarge:
		return apperr.PayloadTooLarge("Google Drive rejected the upload as too large.")
	}

	if status >= 500 {
		return apperr.UpstreamUnavailable("Google Drive is having trouble. Please try again shortly.")
	}
	return apperr.BadRequest(fallback(message, "Google Drive rejected that request."))
}

func fallback(message, def string) string {
	if message == "" {
		return def
	}
	return message
}

// parseRetryAfter reads the Retry-After header in either of its two forms.
func parseRetryAfter(header string) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(header); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(header); err == nil {
		if delay := time.Until(when); delay > 0 {
			return delay
		}
	}
	return 0
}

// mapTransportError distinguishes a cancelled request from a network failure, so
// a user navigating away is not logged as a Google outage.
func mapTransportError(ctx context.Context, err error) *apperr.Error {
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return apperr.UpstreamUnavailable("The request was cancelled.").WithCause(err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return apperr.UpstreamUnavailable("Google Drive did not respond in time.").WithCause(err)
	}
	return apperr.UpstreamUnavailable("Could not reach Google Drive.").WithCause(err)
}
