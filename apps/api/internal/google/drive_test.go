package google

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
)

// driveStub is a scriptable stand-in for the Drive v3 REST API.
type driveStub struct {
	server *httptest.Server

	// responses are served in order; the last one repeats.
	responses []stubResponse
	calls     atomic.Int32
	lastToken string
	lastQuery string
}

type stubResponse struct {
	status     int
	body       string
	retryAfter string
}

func newDriveStub(t *testing.T, responses ...stubResponse) *driveStub {
	t.Helper()

	stub := &driveStub{responses: responses}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := int(stub.calls.Add(1)) - 1
		stub.lastToken = r.Header.Get("Authorization")
		stub.lastQuery = r.URL.RawQuery

		response := stub.responses[len(stub.responses)-1]
		if index < len(stub.responses) {
			response = stub.responses[index]
		}

		if response.retryAfter != "" {
			w.Header().Set("Retry-After", response.retryAfter)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.status)
		_, _ = w.Write([]byte(response.body))
	}))
	t.Cleanup(stub.server.Close)

	return stub
}

func (s *driveStub) drive() *Drive {
	return NewDrive(WithDriveEndpoints(DriveEndpoints{
		BaseURL:   s.server.URL,
		UploadURL: s.server.URL + "/upload",
	}))
}

const aboutBody = `{
	"user": {
		"displayName": "Test User",
		"emailAddress": "user@example.test",
		"photoLink": "https://lh3.googleusercontent.test/a.png"
	},
	"storageQuota": {
		"limit": "16106127360",
		"usage": "5368709120",
		"usageInDrive": "5000000000",
		"usageInDriveTrash": "368709120"
	}
}`

func TestAboutParsesStringEncodedByteCounts(t *testing.T) {
	stub := newDriveStub(t, stubResponse{status: http.StatusOK, body: aboutBody})

	about, err := stub.drive().About(context.Background(), "access-token")
	if err != nil {
		t.Fatalf("About: %v", err)
	}

	if about.User.EmailAddress != "user@example.test" {
		t.Errorf("unexpected user: %+v", about.User)
	}
	if about.Quota.Limit == nil || *about.Quota.Limit != 16106127360 {
		t.Errorf("limit not parsed: %v", about.Quota.Limit)
	}
	if about.Quota.Usage != 5368709120 {
		t.Errorf("usage not parsed: %d", about.Quota.Usage)
	}
	if about.Quota.UsageInTrash != 368709120 {
		t.Errorf("trash usage not parsed: %d", about.Quota.UsageInTrash)
	}

	if stub.lastToken != "Bearer access-token" {
		t.Errorf("unexpected authorization header: %q", stub.lastToken)
	}
	// asking for everything would waste bandwidth on every account, every request
	if stub.lastQuery == "" {
		t.Error("expected a fields mask on the request")
	}
}

func TestAboutTreatsMissingLimitAsUnlimited(t *testing.T) {
	stub := newDriveStub(t, stubResponse{
		status: http.StatusOK,
		body:   `{"user":{},"storageQuota":{"usage":"1024"}}`,
	})

	about, err := stub.drive().About(context.Background(), "at")
	if err != nil {
		t.Fatalf("About: %v", err)
	}
	if !about.Quota.Unlimited() {
		t.Error("expected unlimited storage")
	}
	if about.Quota.Free() != nil {
		t.Error("unlimited storage has no free figure")
	}
	if about.Quota.Usage != 1024 {
		t.Errorf("usage not parsed: %d", about.Quota.Usage)
	}
}

func TestStorageQuotaFreeNeverGoesNegative(t *testing.T) {
	limit := int64(100)
	quota := StorageQuota{Limit: &limit, Usage: 150}

	free := quota.Free()
	if free == nil || *free != 0 {
		t.Errorf("got %v, want 0", free)
	}
}

func TestAboutMapsDriveErrorsByReason(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantCode apperr.Code
	}{
		{
			"quota exceeded", http.StatusForbidden,
			`{"error":{"code":403,"errors":[{"reason":"storageQuotaExceeded"}]}}`,
			apperr.CodeQuotaExceeded,
		},
		{
			"insufficient permissions", http.StatusForbidden,
			`{"error":{"code":403,"errors":[{"reason":"insufficientFilePermissions"}]}}`,
			apperr.CodeInsufficientScope,
		},
		{
			"credentials rejected", http.StatusUnauthorized,
			`{"error":{"code":401,"errors":[{"reason":"authError"}]}}`,
			apperr.CodeReauthRequired,
		},
		{
			"not found", http.StatusNotFound,
			`{"error":{"code":404,"errors":[{"reason":"fileNotFound"}]}}`,
			apperr.CodeNotFound,
		},
		{
			"plain 403 without a reason", http.StatusForbidden,
			`{"error":{"code":403,"message":"The user does not have sufficient permissions."}}`,
			apperr.CodeForbidden,
		},
		{
			"unparseable body", http.StatusBadRequest,
			`not json at all`,
			apperr.CodeBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := newDriveStub(t, stubResponse{status: tc.status, body: tc.body})

			_, err := stub.drive().About(context.Background(), "at")
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := apperr.From(err).Code; got != tc.wantCode {
				t.Errorf("got %q want %q", got, tc.wantCode)
			}
			// non-retryable failures must not be retried
			if calls := stub.calls.Load(); calls != 1 {
				t.Errorf("made %d calls, want 1", calls)
			}
		})
	}
}

func TestAboutRetriesTransientFailures(t *testing.T) {
	stub := newDriveStub(t,
		stubResponse{status: http.StatusServiceUnavailable, body: `{"error":{"code":503}}`},
		stubResponse{status: http.StatusTooManyRequests,
			body: `{"error":{"code":429,"errors":[{"reason":"rateLimitExceeded"}]}}`},
		stubResponse{status: http.StatusOK, body: aboutBody},
	)

	about, err := stub.drive().About(context.Background(), "at")
	if err != nil {
		t.Fatalf("About: %v", err)
	}
	if about.Quota.Usage == 0 {
		t.Error("succeeded but returned no data")
	}
	if calls := stub.calls.Load(); calls != 3 {
		t.Errorf("made %d calls, want 3", calls)
	}
}

func TestAboutGivesUpAfterMaxAttempts(t *testing.T) {
	stub := newDriveStub(t, stubResponse{status: http.StatusBadGateway, body: `{}`})

	_, err := stub.drive().About(context.Background(), "at")
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := apperr.From(err).Code; got != apperr.CodeUpstreamUnavailable {
		t.Errorf("got %q", got)
	}
	if calls := stub.calls.Load(); calls != maxAttempts {
		t.Errorf("made %d calls, want %d", calls, maxAttempts)
	}
}

func TestAboutStopsWhenTheContextIsCancelled(t *testing.T) {
	stub := newDriveStub(t, stubResponse{status: http.StatusServiceUnavailable, body: `{}`})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := stub.drive().About(ctx, "at"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestParseRetryAfter(t *testing.T) {
	if got := parseRetryAfter("7"); got != 7*time.Second {
		t.Errorf("seconds form: got %v", got)
	}
	if got := parseRetryAfter(""); got != 0 {
		t.Errorf("empty: got %v", got)
	}
	if got := parseRetryAfter("not-a-number"); got != 0 {
		t.Errorf("garbage: got %v", got)
	}
	// a date in the past must not produce a negative delay
	if got := parseRetryAfter("Mon, 01 Jan 2001 00:00:00 GMT"); got != 0 {
		t.Errorf("past date: got %v", got)
	}
	if got := parseRetryAfter(time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)); got <= 0 {
		t.Errorf("future date: got %v", got)
	}
}

func TestRateLimitErrorCarriesRetryAfter(t *testing.T) {
	err := mapAPIError(http.StatusTooManyRequests,
		[]byte(`{"error":{"errors":[{"reason":"rateLimitExceeded"}]}}`), 5*time.Second)

	if err.Code != apperr.CodeRateLimited {
		t.Fatalf("got %q", err.Code)
	}
	seconds, ok := err.Details["retry_after_seconds"].(int)
	if !ok || seconds != 5 {
		t.Errorf("retry_after_seconds not surfaced: %#v", err.Details)
	}
}

func TestBackoffHonoursRetryAfter(t *testing.T) {
	err := apperr.RateLimited("slow down").WithDetail("retry_after_seconds", 2)

	if got := backoffFor(1, err); got != 2*time.Second {
		t.Errorf("got %v, want 2s", got)
	}
	// and never waits longer than the ceiling
	tooLong := apperr.RateLimited("slow down").WithDetail("retry_after_seconds", 3600)
	if got := backoffFor(1, tooLong); got != maxBackoff {
		t.Errorf("got %v, want %v", got, maxBackoff)
	}
}
