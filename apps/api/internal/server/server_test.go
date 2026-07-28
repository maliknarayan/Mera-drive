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

	"github.com/sangamdrive/sangamdrive/apps/api/internal/config"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/cryptobox"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/httpx"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/store/sqlite"
)

func newTestServer(t *testing.T) *Server {
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
	box, err := cryptobox.New(key, key)
	if err != nil {
		t.Fatalf("cryptobox: %v", err)
	}

	return New(Deps{
		Config: &config.Config{
			Env:             config.EnvDevelopment,
			CORSOrigins:     []string{"http://localhost:3000"},
			RateLimitMax:    1000,
			RateLimitWindow: time.Minute,
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Store:  st,
		Crypto: box,
		Build:  BuildInfo{Version: "test"},
	})
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
	allowed.Header.Set("Origin", "http://localhost:3000")
	resp, _ := do(t, s, allowed)
	if resp.Header.Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
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
	req.Header.Set("Origin", "http://localhost:3000")
	resp, _ := do(t, s, req)

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("got status %d, want 204", resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Methods") == "" {
		t.Error("preflight did not advertise allowed methods")
	}
}
