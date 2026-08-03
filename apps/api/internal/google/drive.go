package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
)

// DriveEndpoints are the Drive API URLs, injectable for tests.
type DriveEndpoints struct {
	// BaseURL is the Drive v3 root, e.g. https://www.googleapis.com/drive/v3.
	BaseURL string
	// UploadURL is the resumable upload root, used from phase 5.
	UploadURL string
}

// DefaultDriveEndpoints returns the live Drive API URLs.
func DefaultDriveEndpoints() DriveEndpoints {
	return DriveEndpoints{
		BaseURL:   "https://www.googleapis.com/drive/v3",
		UploadURL: "https://www.googleapis.com/upload/drive/v3",
	}
}

// Retry policy. Google's own guidance is exponential backoff with jitter; the
// ceiling is deliberately low because a user is waiting on the response.
const (
	maxAttempts  = 4
	baseBackoff  = 250 * time.Millisecond
	maxBackoff   = 4 * time.Second
	maxErrorBody = 1 << 16
)

// Drive is a thin client over the Google Drive v3 REST API.
//
// It intentionally implements only what SangamDrive forwards. There is no model
// layer and no caching: responses are transformed and passed straight through.
type Drive struct {
	endpoints DriveEndpoints
	client    *http.Client
}

// DriveOption customises a Drive client.
type DriveOption func(*Drive)

// WithDriveEndpoints overrides the Drive API URLs. Tests use this.
func WithDriveEndpoints(e DriveEndpoints) DriveOption {
	return func(d *Drive) { d.endpoints = e }
}

// WithDriveHTTPClient overrides the HTTP client.
func WithDriveHTTPClient(c *http.Client) DriveOption {
	return func(d *Drive) { d.client = c }
}

// NewDrive builds a Drive client.
func NewDrive(opts ...DriveOption) *Drive {
	d := &Drive{
		endpoints: DefaultDriveEndpoints(),
		// no overall timeout: downloads are unbounded, callers pass a context
		client: &http.Client{},
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// StorageQuota is one Drive's usage, in bytes.
type StorageQuota struct {
	// Limit is nil for accounts with unlimited storage, which some Workspace
	// tiers report by omitting the field entirely.
	Limit        *int64 `json:"limit"`
	Usage        int64  `json:"usage"`
	UsageInDrive int64  `json:"usage_in_drive"`
	UsageInTrash int64  `json:"usage_in_trash"`
}

// Unlimited reports whether this Drive has no storage cap.
func (q *StorageQuota) Unlimited() bool { return q == nil || q.Limit == nil }

// Free returns the remaining bytes, or nil when storage is unlimited.
func (q *StorageQuota) Free() *int64 {
	if q == nil || q.Limit == nil {
		return nil
	}
	free := *q.Limit - q.Usage
	if free < 0 {
		free = 0
	}
	return &free
}

// DriveUser is the account identity as Drive reports it.
type DriveUser struct {
	DisplayName  string `json:"display_name"`
	EmailAddress string `json:"email_address"`
	PhotoLink    string `json:"photo_link"`
}

// About is the subset of the Drive `about` resource SangamDrive uses.
type About struct {
	User  DriveUser    `json:"user"`
	Quota StorageQuota `json:"quota"`
}

// aboutResponse mirrors Drive's wire format, where 64-bit byte counts arrive as
// JSON strings and absent fields mean "unlimited".
type aboutResponse struct {
	User struct {
		DisplayName  string `json:"displayName"`
		EmailAddress string `json:"emailAddress"`
		PhotoLink    string `json:"photoLink"`
	} `json:"user"`
	StorageQuota struct {
		Limit             *string `json:"limit"`
		Usage             *string `json:"usage"`
		UsageInDrive      *string `json:"usageInDrive"`
		UsageInDriveTrash *string `json:"usageInDriveTrash"`
	} `json:"storageQuota"`
}

// About fetches the signed-in account's identity and storage quota.
func (d *Drive) About(ctx context.Context, accessToken string) (*About, error) {
	query := url.Values{"fields": {"user(displayName,emailAddress,photoLink),storageQuota"}}

	var raw aboutResponse
	if err := d.getJSON(ctx, accessToken, "/about", query, &raw); err != nil {
		return nil, err
	}

	limit, err := parseOptionalInt64(raw.StorageQuota.Limit)
	if err != nil {
		return nil, apperr.UpstreamUnavailable("Google returned an unreadable storage quota.").WithCause(err)
	}

	return &About{
		User: DriveUser{
			DisplayName:  raw.User.DisplayName,
			EmailAddress: raw.User.EmailAddress,
			PhotoLink:    raw.User.PhotoLink,
		},
		Quota: StorageQuota{
			Limit:        limit,
			Usage:        parseInt64OrZero(raw.StorageQuota.Usage),
			UsageInDrive: parseInt64OrZero(raw.StorageQuota.UsageInDrive),
			UsageInTrash: parseInt64OrZero(raw.StorageQuota.UsageInDriveTrash),
		},
	}, nil
}

// callSpec describes one Drive API request.
type callSpec struct {
	method string
	path   string
	query  url.Values
	// body is marshalled as JSON when non-nil.
	body any
	// idempotent allows the request to be retried. Creating a resource is not
	// idempotent — retrying a folder create would make two folders.
	idempotent bool
}

// getJSON issues a retryable GET and decodes the response.
func (d *Drive) getJSON(ctx context.Context, accessToken, path string, query url.Values, out any) error {
	return d.call(ctx, accessToken, callSpec{
		method:     http.MethodGet,
		path:       path,
		query:      query,
		idempotent: true,
	}, out)
}

// call performs a Drive API request, retrying transient failures when the spec
// says it is safe to do so.
//
// out may be nil for calls whose response body is not needed.
func (d *Drive) call(ctx context.Context, accessToken string, spec callSpec, out any) error {
	target := strings.TrimRight(d.endpoints.BaseURL, "/") + spec.path
	if len(spec.query) > 0 {
		target += "?" + spec.query.Encode()
	}

	var payload []byte
	if spec.body != nil {
		encoded, err := json.Marshal(spec.body)
		if err != nil {
			return apperr.Internal("Could not encode the Google Drive request.").WithCause(err)
		}
		payload = encoded
	}

	attempts := 1
	if spec.idempotent {
		attempts = maxAttempts
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, backoffFor(attempt, lastErr)); err != nil {
				return mapTransportError(ctx, err)
			}
		}

		var reader io.Reader
		if payload != nil {
			reader = bytes.NewReader(payload)
		}

		req, err := http.NewRequestWithContext(ctx, spec.method, target, reader)
		if err != nil {
			return apperr.Internal("Could not build the Google Drive request.").WithCause(err)
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Accept", "application/json")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := d.client.Do(req)
		if err != nil {
			lastErr = mapTransportError(ctx, err)
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			decodeErr := decodeInto(resp, out)
			_ = resp.Body.Close()
			if decodeErr != nil {
				return decodeErr
			}
			return nil
		}

		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		_ = resp.Body.Close()

		apiErr := mapAPIError(resp.StatusCode, body, retryAfter)
		if !apperr.IsRetryable(apiErr) {
			return apiErr
		}
		lastErr = apiErr
	}

	if lastErr == nil {
		lastErr = apperr.UpstreamUnavailable("Google Drive did not respond.")
	}
	return lastErr
}

func decodeInto(resp *http.Response, out any) error {
	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return apperr.UpstreamUnavailable("Google Drive returned an unreadable response.").WithCause(err)
	}
	return nil
}

// backoffFor returns the delay before the given attempt, honouring Retry-After
// when Google supplied one.
func backoffFor(attempt int, lastErr error) time.Duration {
	if appErr := apperr.From(lastErr); appErr != nil {
		if seconds, ok := appErr.Details["retry_after_seconds"].(int); ok && seconds > 0 {
			delay := time.Duration(seconds) * time.Second
			if delay > maxBackoff {
				return maxBackoff
			}
			return delay
		}
	}

	delay := baseBackoff * (1 << (attempt - 1))
	if delay > maxBackoff {
		delay = maxBackoff
	}
	// full jitter, so N concurrent accounts do not retry in lockstep
	return time.Duration(rand.Int63n(int64(delay)) + int64(delay)/2)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func parseOptionalInt64(raw *string) (*int64, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseInt(*raw, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse %q: %w", *raw, err)
	}
	return &value, nil
}

func parseInt64OrZero(raw *string) int64 {
	if raw == nil {
		return 0
	}
	value, err := strconv.ParseInt(*raw, 10, 64)
	if err != nil {
		return 0
	}
	return value
}
