package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/auth"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/config"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/cryptobox"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/httpx"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/store/sqlite"
)

const testAppBaseURL = "http://localhost:3000"

// harness is a fully wired server with a fake Google behind it.
type harness struct {
	server *Server
	store  *sqlite.Store
	auth   *auth.Service
	google *fakeGoogle
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	st, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	key := make([]byte, cryptobox.KeySize)
	for i := range key {
		key[i] = byte(i + 1)
	}
	box, err := cryptobox.New(key, key)
	if err != nil {
		t.Fatalf("cryptobox: %v", err)
	}

	authService := auth.NewService(st, box, auth.CookieOptions{}, time.Hour)
	fake := newFakeGoogle(t)

	srv := New(Deps{
		Config: &config.Config{
			Env:             config.EnvDevelopment,
			AppBaseURL:      testAppBaseURL,
			CORSOrigins:     []string{testAppBaseURL},
			RateLimitMax:    1000,
			RateLimitWindow: time.Minute,
			SessionTTL:      time.Hour,
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Store:  st,
		Crypto: box,
		Auth:   authService,
		Google: fake,
		Build:  BuildInfo{Version: "test"},
	})

	return &harness{server: srv, store: st, auth: authService, google: fake}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	return newHarness(t).server
}

// send issues a request and returns the raw response.
func (h *harness) send(t *testing.T, req *http.Request) *http.Response {
	t.Helper()

	resp, err := h.server.App().Test(req, -1)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// sendJSON issues a request and decodes the response envelope.
func (h *harness) sendJSON(t *testing.T, req *http.Request) (*http.Response, httpx.Envelope) {
	t.Helper()

	resp := h.send(t, req)

	var env httpx.Envelope
	if resp.StatusCode != http.StatusNoContent && (resp.StatusCode < 300 || resp.StatusCode >= 400) {
		if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
			t.Fatalf("decode body (status %d): %v", resp.StatusCode, err)
		}
	}
	return resp, env
}

// cookieValue returns a Set-Cookie value from a response, or "" if absent.
func cookieValue(resp *http.Response, name string) string {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// cookieCleared reports whether the response expires the named cookie.
func cookieCleared(resp *http.Response, name string) bool {
	for _, c := range resp.Cookies() {
		if c.Name == name && (c.MaxAge < 0 || c.Value == "") {
			return true
		}
	}
	return false
}

func do(t *testing.T, s *Server, req *http.Request) (*http.Response, httpx.Envelope) {
	t.Helper()

	resp, err := s.App().Test(req, -1)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	var env httpx.Envelope
	if resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
			t.Fatalf("decode body: %v", err)
		}
	}
	return resp, env
}

func TestHealthAndReady(t *testing.T) {
	s := newTestServer(t)

	for _, path := range []string{"/healthz", "/readyz"} {
		resp, env := do(t, s, httptest.NewRequest(http.MethodGet, path, nil))
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: got status %d", path, resp.StatusCode)
		}
		if env.Error != nil {
			t.Errorf("%s: unexpected error %v", path, env.Error)
		}
		if resp.Header.Get(httpx.HeaderRequestID) == "" {
			t.Errorf("%s: missing request id header", path)
		}
	}
}

func TestMetaReportsBuildInfo(t *testing.T) {
	s := newTestServer(t)

	_, env := do(t, s, httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil))
	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected data shape: %#v", env.Data)
	}
	if data["name"] != "SangamDrive" {
		t.Errorf("unexpected name: %v", data["name"])
	}
	build, ok := data["build"].(map[string]any)
	if !ok || build["version"] != "test" {
		t.Errorf("unexpected build info: %#v", data["build"])
	}
}

func TestUnknownRouteReturnsStructuredError(t *testing.T) {
	s := newTestServer(t)

	resp, env := do(t, s, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got status %d", resp.StatusCode)
	}
	if env.Error == nil || env.Error.Code != "not_found" {
		t.Fatalf("unexpected error payload: %#v", env.Error)
	}
	if env.RequestID == "" {
		t.Error("expected request id in error envelope")
	}
}

func TestSecurityHeadersArePresent(t *testing.T) {
	s := newTestServer(t)

	resp, _ := do(t, s, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for header, value := range want {
		if got := resp.Header.Get(header); got != value {
			t.Errorf("%s: got %q want %q", header, got, value)
		}
	}
}

func TestCORSAllowlist(t *testing.T) {
	s := newTestServer(t)

	allowed := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	allowed.Header.Set("Origin", testAppBaseURL)
	resp, _ := do(t, s, allowed)
	if resp.Header.Get("Access-Control-Allow-Origin") != testAppBaseURL {
		t.Error("allowed origin was not echoed")
	}
	if resp.Header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("credentials not allowed for an allowlisted origin")
	}

	denied := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	denied.Header.Set("Origin", "https://evil.example")
	resp, _ = do(t, s, denied)
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("unlisted origin was allowed")
	}
}

func TestPreflightShortCircuits(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/meta", nil)
	req.Header.Set("Origin", testAppBaseURL)
	resp, _ := do(t, s, req)

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("got status %d, want 204", resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Methods") == "" {
		t.Error("preflight did not advertise allowed methods")
	}
}
