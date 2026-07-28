// Package apperr defines the single error type that crosses the HTTP boundary.
//
// Handlers return *apperr.Error; the error middleware renders it. Every error
// carries a stable machine-readable Code so the web client can branch on
// behaviour ("this account needs reconnecting") without parsing prose.
package apperr

import (
	"errors"
	"fmt"
	"net/http"
)

// Code is a stable, machine-readable error identifier. Codes are part of the
// public API contract — rename them only with a version bump.
type Code string

const (
	CodeBadRequest      Code = "bad_request"
	CodeValidation      Code = "validation_failed"
	CodeUnauthorized    Code = "unauthorized"
	CodeCSRF            Code = "csrf_invalid"
	CodeForbidden       Code = "forbidden"
	CodeNotFound        Code = "not_found"
	CodeConflict        Code = "conflict"
	CodePayloadTooLarge Code = "payload_too_large"
	CodeRateLimited     Code = "rate_limited"
	CodeInternal        Code = "internal_error"

	// Google-specific conditions the UI renders as dedicated states.

	// CodeReauthRequired means the stored refresh token no longer works and the
	// user must reconnect that Google account.
	CodeReauthRequired Code = "reauth_required"
	// CodeInsufficientScope means the account is connected with drive.file but
	// the requested operation needs full drive scope.
	CodeInsufficientScope Code = "insufficient_scope"
	// CodeQuotaExceeded means the target Drive has no free space left.
	CodeQuotaExceeded Code = "quota_exceeded"
	// CodeUpstreamUnavailable means Google returned 5xx or the call timed out.
	CodeUpstreamUnavailable Code = "upstream_unavailable"
)

// Error is an application error with an HTTP status and a stable code.
type Error struct {
	Code    Code           `json:"code"`
	Message string         `json:"message"`
	Status  int            `json:"-"`
	Details map[string]any `json:"details,omitempty"`

	// AccountID identifies the connected Google account a per-account failure
	// belongs to, so the UI can highlight the right card.
	AccountID string `json:"account_id,omitempty"`

	// cause is never serialised; it is logged server-side only.
	cause error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.cause }

// WithCause attaches an internal error for logging. Returns e for chaining.
func (e *Error) WithCause(err error) *Error {
	e.cause = err
	return e
}

// WithDetail adds a structured field to the client-visible payload.
func (e *Error) WithDetail(key string, value any) *Error {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	e.Details[key] = value
	return e
}

// WithAccount tags the error with the connected account it originated from.
func (e *Error) WithAccount(id string) *Error {
	e.AccountID = id
	return e
}

func newf(code Code, status int, format string, args ...any) *Error {
	return &Error{Code: code, Status: status, Message: fmt.Sprintf(format, args...)}
}

// Constructors. Messages are user-facing — keep them plain and actionable.

func BadRequest(format string, args ...any) *Error {
	return newf(CodeBadRequest, http.StatusBadRequest, format, args...)
}

func Validation(format string, args ...any) *Error {
	return newf(CodeValidation, http.StatusUnprocessableEntity, format, args...)
}

func Unauthorized(format string, args ...any) *Error {
	return newf(CodeUnauthorized, http.StatusUnauthorized, format, args...)
}

func CSRF(format string, args ...any) *Error {
	return newf(CodeCSRF, http.StatusForbidden, format, args...)
}

func Forbidden(format string, args ...any) *Error {
	return newf(CodeForbidden, http.StatusForbidden, format, args...)
}

func NotFound(format string, args ...any) *Error {
	return newf(CodeNotFound, http.StatusNotFound, format, args...)
}

func Conflict(format string, args ...any) *Error {
	return newf(CodeConflict, http.StatusConflict, format, args...)
}

func PayloadTooLarge(format string, args ...any) *Error {
	return newf(CodePayloadTooLarge, http.StatusRequestEntityTooLarge, format, args...)
}

func RateLimited(format string, args ...any) *Error {
	return newf(CodeRateLimited, http.StatusTooManyRequests, format, args...)
}

func Internal(format string, args ...any) *Error {
	return newf(CodeInternal, http.StatusInternalServerError, format, args...)
}

func ReauthRequired(format string, args ...any) *Error {
	return newf(CodeReauthRequired, http.StatusUnauthorized, format, args...)
}

func InsufficientScope(format string, args ...any) *Error {
	return newf(CodeInsufficientScope, http.StatusForbidden, format, args...)
}

func QuotaExceeded(format string, args ...any) *Error {
	return newf(CodeQuotaExceeded, http.StatusInsufficientStorage, format, args...)
}

func UpstreamUnavailable(format string, args ...any) *Error {
	return newf(CodeUpstreamUnavailable, http.StatusBadGateway, format, args...)
}

// From normalises any error into an *Error. Unrecognised errors become a
// generic internal error so handler bugs never leak implementation detail.
func From(err error) *Error {
	if err == nil {
		return nil
	}
	var appErr *Error
	if errors.As(err, &appErr) {
		return appErr
	}
	return Internal("Something went wrong on our side.").WithCause(err)
}

// IsRetryable reports whether retrying the same operation could plausibly
// succeed. Used by the Google client's backoff loop.
func IsRetryable(err error) bool {
	e := From(err)
	if e == nil {
		return false
	}
	switch e.Code {
	case CodeRateLimited, CodeUpstreamUnavailable:
		return true
	default:
		return false
	}
}
