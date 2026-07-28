package google

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
)

// fakeGoogle is a stand-in for Google's OAuth and userinfo endpoints. Tests point
// the real Authenticator at it so the production exchange path is exercised.
type fakeGoogle struct {
	server *httptest.Server

	tokenStatus   int
	tokenBody     any
	profileStatus int
	profileBody   any
	revokeStatus  int

	lastTokenForm  url.Values
	lastAuthHeader string
	lastRevokeForm url.Values
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()

	f := &fakeGoogle{
		tokenStatus: http.StatusOK,
		tokenBody: map[string]any{
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
			"token_type":    "Bearer",
			"expires_in":    3599,
			"scope":         strings.Join(append(IdentityScopes(), ScopeDriveFile), " "),
		},
		profileStatus: http.StatusOK,
		profileBody: map[string]any{
			"sub":            "google-sub-1",
			"email":          "user@example.test",
			"email_verified": true,
			"name":           "Test User",
			"picture":        "https://lh3.googleusercontent.test/a.png",
		},
		revokeStatus: http.StatusOK,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.lastTokenForm = r.PostForm
		writeJSON(w, f.tokenStatus, f.tokenBody)
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		f.lastAuthHeader = r.Header.Get("Authorization")
		writeJSON(w, f.profileStatus, f.profileBody)
	})
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.lastRevokeForm = r.PostForm
		w.WriteHeader(f.revokeStatus)
	})

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeGoogle) endpoints() Endpoints {
	return Endpoints{
		Auth:     f.server.URL + "/auth",
		Token:    f.server.URL + "/token",
		UserInfo: f.server.URL + "/userinfo",
		Revoke:   f.server.URL + "/revoke",
	}
}

func (f *fakeGoogle) authenticator() *Authenticator {
	return NewAuthenticator("client-id", "client-secret", "http://localhost:8080/cb",
		WithEndpoints(f.endpoints()))
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func TestAuthCodeURLForcesOfflineConsent(t *testing.T) {
	fake := newFakeGoogle(t)
	auth := fake.authenticator()

	raw := auth.AuthCodeURL("state-123", append(IdentityScopes(), ScopeDriveFull))
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	q := parsed.Query()

	want := map[string]string{
		"state":                  "state-123",
		"client_id":              "client-id",
		"redirect_uri":           "http://localhost:8080/cb",
		"response_type":          "code",
		"access_type":            "offline",
		"prompt":                 "consent",
		"include_granted_scopes": "true",
	}
	for key, value := range want {
		if got := q.Get(key); got != value {
			t.Errorf("%s: got %q want %q", key, got, value)
		}
	}
	if !strings.Contains(q.Get("scope"), ScopeDriveFull) {
		t.Errorf("scope missing full drive: %q", q.Get("scope"))
	}
}

func TestExchangeReturnsTokenAndGrantedScopes(t *testing.T) {
	fake := newFakeGoogle(t)

	token, err := fake.authenticator().Exchange(context.Background(), "the-code")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	if token.AccessToken != "access-token" || token.RefreshToken != "refresh-token" {
		t.Errorf("unexpected token: %+v", token)
	}
	if !token.HasScope(ScopeDriveFile) {
		t.Error("expected drive.file in granted scopes")
	}
	if token.HasScope(ScopeDriveFull) {
		t.Error("did not expect full drive in granted scopes")
	}
	if fake.lastTokenForm.Get("code") != "the-code" {
		t.Errorf("code not forwarded: %v", fake.lastTokenForm)
	}
	if fake.lastTokenForm.Get("grant_type") != "authorization_code" {
		t.Errorf("unexpected grant_type: %q", fake.lastTokenForm.Get("grant_type"))
	}
}

func TestExchangeMapsGoogleErrors(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     any
		wantCode apperr.Code
	}{
		{"invalid grant", http.StatusBadRequest,
			map[string]any{"error": "invalid_grant"}, apperr.CodeReauthRequired},
		{"invalid client", http.StatusUnauthorized,
			map[string]any{"error": "invalid_client"}, apperr.CodeInternal},
		{"invalid scope", http.StatusBadRequest,
			map[string]any{"error": "invalid_scope"}, apperr.CodeBadRequest},
		{"google outage", http.StatusInternalServerError,
			map[string]any{"error": "backend_error"}, apperr.CodeUpstreamUnavailable},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeGoogle(t)
			fake.tokenStatus = tc.status
			fake.tokenBody = tc.body

			_, err := fake.authenticator().Exchange(context.Background(), "code")
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := apperr.From(err).Code; got != tc.wantCode {
				t.Errorf("got code %q want %q (%v)", got, tc.wantCode, err)
			}
		})
	}
}

func TestRefreshMintsANewAccessToken(t *testing.T) {
	fake := newFakeGoogle(t)

	token, err := fake.authenticator().Refresh(context.Background(), "stored-refresh-token")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if token.AccessToken != "access-token" {
		t.Errorf("unexpected access token: %q", token.AccessToken)
	}
	// the caller's stored token is echoed back so it can be reused
	if token.RefreshToken != "stored-refresh-token" {
		t.Errorf("unexpected refresh token: %q", token.RefreshToken)
	}
	if fake.lastTokenForm.Get("grant_type") != "refresh_token" {
		t.Errorf("unexpected grant_type: %q", fake.lastTokenForm.Get("grant_type"))
	}
}

func TestRefreshWithARevokedTokenAsksForReconnect(t *testing.T) {
	fake := newFakeGoogle(t)
	fake.tokenStatus = http.StatusBadRequest
	fake.tokenBody = map[string]any{"error": "invalid_grant"}

	_, err := fake.authenticator().Refresh(context.Background(), "revoked")
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := apperr.From(err).Code; got != apperr.CodeReauthRequired {
		t.Errorf("got %q want %q", got, apperr.CodeReauthRequired)
	}
}

func TestRefreshRejectsAnEmptyToken(t *testing.T) {
	fake := newFakeGoogle(t)

	if _, err := fake.authenticator().Refresh(context.Background(), ""); err == nil {
		t.Fatal("expected an error")
	}
	if fake.lastTokenForm != nil {
		t.Error("expected no call to Google")
	}
}

func TestProfileReadsUserInfo(t *testing.T) {
	fake := newFakeGoogle(t)

	profile, err := fake.authenticator().Profile(context.Background(), "access-token")
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}
	if profile.Sub != "google-sub-1" || profile.Email != "user@example.test" {
		t.Errorf("unexpected profile: %+v", profile)
	}
	if !profile.EmailVerified {
		t.Error("expected email_verified true")
	}
	if fake.lastAuthHeader != "Bearer access-token" {
		t.Errorf("unexpected authorization header: %q", fake.lastAuthHeader)
	}
}

func TestProfileMapsFailures(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     any
		wantCode apperr.Code
	}{
		{"rejected token", http.StatusUnauthorized, map[string]any{}, apperr.CodeReauthRequired},
		{"throttled", http.StatusTooManyRequests, map[string]any{}, apperr.CodeRateLimited},
		{"outage", http.StatusBadGateway, map[string]any{}, apperr.CodeUpstreamUnavailable},
		{"missing sub", http.StatusOK,
			map[string]any{"email": "a@example.test"}, apperr.CodeUpstreamUnavailable},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeGoogle(t)
			fake.profileStatus = tc.status
			fake.profileBody = tc.body

			_, err := fake.authenticator().Profile(context.Background(), "at")
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := apperr.From(err).Code; got != tc.wantCode {
				t.Errorf("got code %q want %q", got, tc.wantCode)
			}
		})
	}
}

func TestRevokeTreatsAlreadyRevokedAsSuccess(t *testing.T) {
	fake := newFakeGoogle(t)
	auth := fake.authenticator()

	if err := auth.Revoke(context.Background(), "refresh-token"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if fake.lastRevokeForm.Get("token") != "refresh-token" {
		t.Errorf("token not forwarded: %v", fake.lastRevokeForm)
	}

	// Google answers 400 invalid_token for a token that is already gone
	fake.revokeStatus = http.StatusBadRequest
	if err := auth.Revoke(context.Background(), "refresh-token"); err != nil {
		t.Errorf("already-revoked token should not be an error: %v", err)
	}

	fake.revokeStatus = http.StatusInternalServerError
	if err := auth.Revoke(context.Background(), "refresh-token"); err == nil {
		t.Error("expected an error when Google fails")
	}
}
