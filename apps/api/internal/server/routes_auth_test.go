package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/auth"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/google"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/store"
)

// fakeGoogle stands in for the Google OAuth provider at the interface boundary.
// The HTTP-level behaviour is covered in internal/google.
type fakeGoogle struct {
	token      *google.Token
	tokenErr   error
	profile    *google.Profile
	profileErr error

	lastState  string
	lastScopes []string
	revoked    []string
}

func newFakeGoogle(_ *testing.T) *fakeGoogle {
	return &fakeGoogle{
		token: &google.Token{
			AccessToken:   "access-token",
			RefreshToken:  "refresh-token",
			GrantedScopes: append(google.IdentityScopes(), google.ScopeDriveFile),
		},
		profile: &google.Profile{
			Sub:           "google-sub-1",
			Email:         "owner@example.test",
			EmailVerified: true,
			Name:          "Owner",
			PictureURL:    "https://lh3.googleusercontent.test/o.png",
		},
	}
}

func (f *fakeGoogle) AuthCodeURL(state string, scopes []string) string {
	f.lastState = state
	f.lastScopes = scopes
	return "https://accounts.google.test/o/oauth2/v2/auth?state=" + url.QueryEscape(state)
}

func (f *fakeGoogle) Exchange(_ context.Context, _ string) (*google.Token, error) {
	if f.tokenErr != nil {
		return nil, f.tokenErr
	}
	return f.token, nil
}

func (f *fakeGoogle) Profile(_ context.Context, _ string) (*google.Profile, error) {
	if f.profileErr != nil {
		return nil, f.profileErr
	}
	return f.profile, nil
}

func (f *fakeGoogle) Revoke(_ context.Context, token string) error {
	f.revoked = append(f.revoked, token)
	return nil
}

// grantScope makes the next exchange report the given Drive scope as granted.
func (f *fakeGoogle) grantScope(scope string) {
	f.token.GrantedScopes = append(google.IdentityScopes(), scope)
}

// asGoogleAccount switches the identity the next flow will return.
func (f *fakeGoogle) asGoogleAccount(sub, email string) {
	f.profile = &google.Profile{
		Sub: sub, Email: email, EmailVerified: true,
		Name: email, PictureURL: "https://lh3.googleusercontent.test/x.png",
	}
}

// --- flow helpers ----------------------------------------------------------

// oauthFlow is what the browser carries between /start and /callback.
type oauthFlow struct {
	state string
	nonce string
}

// start runs /auth/google/start and captures the signed state plus the nonce
// cookie the browser would hold.
func (h *harness) start(t *testing.T, query string, sessionToken string) (*http.Response, *oauthFlow) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/google/start?"+query, nil)
	if sessionToken != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: sessionToken})
	}
	resp := h.send(t, req)

	if resp.StatusCode != http.StatusFound {
		return resp, nil
	}

	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	return resp, &oauthFlow{
		state: location.Query().Get("state"),
		nonce: cookieValue(resp, auth.StateCookie),
	}
}

// callback replays Google's redirect back to the API.
func (h *harness) callback(t *testing.T, flow *oauthFlow, extra url.Values, sessionToken string) *http.Response {
	t.Helper()

	query := url.Values{"code": {"the-code"}, "state": {flow.state}}
	for key, values := range extra {
		query[key] = values
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/google/callback?"+query.Encode(), nil)
	if flow.nonce != "" {
		req.AddCookie(&http.Cookie{Name: auth.StateCookie, Value: flow.nonce})
	}
	if sessionToken != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: sessionToken})
	}
	return h.send(t, req)
}

// login runs a complete login flow and returns the session token.
func (h *harness) login(t *testing.T) string {
	t.Helper()

	_, flow := h.start(t, "intent=login&scope=drive.file", "")
	if flow == nil {
		t.Fatal("start did not redirect")
	}
	resp := h.callback(t, flow, nil, "")

	token := cookieValue(resp, auth.SessionCookie)
	if token == "" {
		t.Fatalf("login did not set a session cookie (redirect: %s)", resp.Header.Get("Location"))
	}
	return token
}

// authError extracts the auth_error code from a callback redirect.
func authError(t *testing.T, resp *http.Response) string {
	t.Helper()

	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	return location.Query().Get("auth_error")
}

// --- start -----------------------------------------------------------------

func TestStartRedirectsToGoogleWithSignedState(t *testing.T) {
	h := newHarness(t)

	resp, flow := h.start(t, "intent=login&scope=drive&next=/dashboard", "")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("got status %d, want 302", resp.StatusCode)
	}
	if flow.state == "" {
		t.Fatal("no state in the redirect")
	}
	if flow.nonce == "" {
		t.Fatal("no state nonce cookie was set")
	}
	if !strings.Contains(flow.state, ".") {
		t.Errorf("state does not look signed: %q", flow.state)
	}
	if !strings.HasPrefix(resp.Header.Get("Location"), "https://accounts.google.test/") {
		t.Errorf("unexpected redirect target: %q", resp.Header.Get("Location"))
	}

	// the identity scopes plus exactly one Drive scope
	if got := h.google.lastScopes; len(got) != 4 || got[3] != google.ScopeDriveFull {
		t.Errorf("unexpected scopes: %v", got)
	}
}

func TestStartRejectsBadInput(t *testing.T) {
	cases := map[string]struct {
		query      string
		withSession bool
		wantStatus int
	}{
		"unknown intent":          {"intent=exfiltrate", false, http.StatusBadRequest},
		"unknown scope":           {"intent=login&scope=everything", false, http.StatusBadRequest},
		"upgrade without full":    {"intent=upgrade&scope=drive.file&account_id=x", true, http.StatusBadRequest},
		"reconnect without id":    {"intent=reconnect", true, http.StatusBadRequest},
		"link without session":    {"intent=link", false, http.StatusUnauthorized},
		"reconnect without session": {"intent=reconnect&account_id=x", false, http.StatusUnauthorized},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)

			var token string
			if tc.withSession {
				token = h.login(t)
			}
			resp, _ := h.start(t, tc.query, token)
			if resp.StatusCode != tc.wantStatus {
				t.Errorf("got status %d want %d", resp.StatusCode, tc.wantStatus)
			}
		})
	}
}

func TestStartRejectsAccountOwnedByAnotherUser(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)

	resp, _ := h.start(t, "intent=reconnect&account_id=does-not-exist", token)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("got status %d, want 404", resp.StatusCode)
	}
}

// --- login -----------------------------------------------------------------

func TestLoginCreatesUserAccountAndSession(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	_, flow := h.start(t, "intent=login&scope=drive.file", "")
	resp := h.callback(t, flow, nil, "")

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("got status %d, want 302", resp.StatusCode)
	}
	if location := resp.Header.Get("Location"); !strings.HasPrefix(location, testAppBaseURL+"/?") {
		t.Errorf("unexpected redirect: %q", location)
	}
	if cookieValue(resp, auth.SessionCookie) == "" {
		t.Error("no session cookie")
	}
	if cookieValue(resp, auth.CSRFCookie) == "" {
		t.Error("no CSRF cookie")
	}
	if !cookieCleared(resp, auth.StateCookie) {
		t.Error("state cookie was not cleared, so the state stays replayable")
	}

	user, err := h.store.GetUserByEmail(ctx, "owner@example.test")
	if err != nil {
		t.Fatalf("user was not created: %v", err)
	}
	if user.Name != "Owner" {
		t.Errorf("profile not copied: %+v", user)
	}

	accounts, err := h.store.ListAccounts(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("expected 1 linked account, got %d", len(accounts))
	}

	account := accounts[0]
	if account.GoogleUserID != "google-sub-1" || account.Scope != store.ScopeDriveFile {
		t.Errorf("unexpected account: %+v", account)
	}
	if account.Status != store.StatusConnected {
		t.Errorf("unexpected status: %v", account.Status)
	}
	if account.RefreshTokenEnc == "refresh-token" {
		t.Fatal("refresh token was stored in plaintext")
	}
	opened, err := h.auth.OpenRefreshToken(account.RefreshTokenEnc)
	if err != nil || opened != "refresh-token" {
		t.Errorf("sealed refresh token did not round-trip: %q %v", opened, err)
	}
}

func TestLoginTwiceReusesTheSameUser(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	first := h.login(t)
	second := h.login(t)
	if first == second {
		t.Error("expected a fresh session token on re-login")
	}

	user, err := h.store.GetUserByEmail(ctx, "owner@example.test")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	accounts, _ := h.store.ListAccounts(ctx, user.ID)
	if len(accounts) != 1 {
		t.Errorf("re-login duplicated the account: %d rows", len(accounts))
	}
}

func TestLoginWithUnverifiedEmailIsRejected(t *testing.T) {
	h := newHarness(t)
	h.google.profile.EmailVerified = false

	_, flow := h.start(t, "intent=login", "")
	resp := h.callback(t, flow, nil, "")

	if got := authError(t, resp); got != "forbidden" {
		t.Errorf("got auth_error %q, want forbidden", got)
	}
	if cookieValue(resp, auth.SessionCookie) != "" {
		t.Error("a session was issued despite the failure")
	}
}

func TestLoginRequiresARefreshToken(t *testing.T) {
	h := newHarness(t)
	h.google.token.RefreshToken = ""

	_, flow := h.start(t, "intent=login", "")
	resp := h.callback(t, flow, nil, "")

	if got := authError(t, resp); got != "bad_request" {
		t.Errorf("got auth_error %q", got)
	}
}

func TestLoginRequiresTheRequestedScopeToBeGranted(t *testing.T) {
	h := newHarness(t)
	// user asked for full drive but only approved drive.file
	h.google.grantScope(google.ScopeDriveFile)

	_, flow := h.start(t, "intent=login&scope=drive", "")
	resp := h.callback(t, flow, nil, "")

	if got := authError(t, resp); got != "insufficient_scope" {
		t.Errorf("got auth_error %q", got)
	}
}

// --- link ------------------------------------------------------------------

func TestLinkAddsASecondAccount(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	token := h.login(t)

	h.google.asGoogleAccount("google-sub-2", "second@example.test")

	_, flow := h.start(t, "intent=link&scope=drive", token)
	if flow == nil {
		t.Fatal("start did not redirect")
	}
	resp := h.callback(t, flow, nil, token)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("got status %d", resp.StatusCode)
	}
	if got := authError(t, resp); got != "" {
		t.Fatalf("unexpected auth_error %q", got)
	}

	user, _ := h.store.GetUserByEmail(ctx, "owner@example.test")
	accounts, _ := h.store.ListAccounts(ctx, user.ID)
	if len(accounts) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(accounts))
	}
	if accounts[1].SortOrder != 1 {
		t.Errorf("sort order not assigned: %+v", accounts[1])
	}
	// linking must not create a second SangamDrive user for the second Drive
	if _, err := h.store.GetUserByEmail(ctx, "second@example.test"); err == nil {
		t.Error("linking created a separate user")
	}
}

func TestRelinkingAnExistingAccountRefreshesIt(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	token := h.login(t)

	h.google.token.RefreshToken = "rotated-refresh-token"
	_, flow := h.start(t, "intent=link&scope=drive.file", token)
	h.callback(t, flow, nil, token)

	user, _ := h.store.GetUserByEmail(ctx, "owner@example.test")
	accounts, _ := h.store.ListAccounts(ctx, user.ID)
	if len(accounts) != 1 {
		t.Fatalf("expected the account to be refreshed, got %d rows", len(accounts))
	}
	opened, _ := h.auth.OpenRefreshToken(accounts[0].RefreshTokenEnc)
	if opened != "rotated-refresh-token" {
		t.Errorf("refresh token not rotated: %q", opened)
	}
}

// --- reconnect and upgrade -------------------------------------------------

func TestReconnectRestoresARejectedAccount(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	token := h.login(t)

	user, _ := h.store.GetUserByEmail(ctx, "owner@example.test")
	accounts, _ := h.store.ListAccounts(ctx, user.ID)
	account := accounts[0]

	if err := h.store.SetAccountStatus(ctx, account.ID, store.StatusReauthRequired); err != nil {
		t.Fatalf("SetAccountStatus: %v", err)
	}

	h.google.token.RefreshToken = "fresh-refresh-token"
	_, flow := h.start(t, "intent=reconnect&scope=drive.file&account_id="+account.ID, token)
	if flow == nil {
		t.Fatal("start did not redirect")
	}
	resp := h.callback(t, flow, nil, token)
	if got := authError(t, resp); got != "" {
		t.Fatalf("unexpected auth_error %q", got)
	}

	updated, err := h.store.GetAccount(ctx, user.ID, account.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if updated.Status != store.StatusConnected {
		t.Errorf("status not restored: %v", updated.Status)
	}
	opened, _ := h.auth.OpenRefreshToken(updated.RefreshTokenEnc)
	if opened != "fresh-refresh-token" {
		t.Errorf("refresh token not replaced: %q", opened)
	}
}

func TestReconnectRejectsTheWrongGoogleAccount(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	token := h.login(t)

	user, _ := h.store.GetUserByEmail(ctx, "owner@example.test")
	accounts, _ := h.store.ListAccounts(ctx, user.ID)

	_, flow := h.start(t, "intent=reconnect&scope=drive.file&account_id="+accounts[0].ID, token)
	// the user picked a different Google account at the chooser
	h.google.asGoogleAccount("google-sub-999", "someone-else@example.test")

	resp := h.callback(t, flow, nil, token)
	if got := authError(t, resp); got != "conflict" {
		t.Errorf("got auth_error %q, want conflict", got)
	}

	unchanged, _ := h.store.GetAccount(ctx, user.ID, accounts[0].ID)
	if unchanged.GoogleUserID != "google-sub-1" {
		t.Error("the account was rebound to a different Google identity")
	}
}

func TestUpgradeWidensTheScope(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	token := h.login(t)

	user, _ := h.store.GetUserByEmail(ctx, "owner@example.test")
	accounts, _ := h.store.ListAccounts(ctx, user.ID)
	if accounts[0].Scope != store.ScopeDriveFile {
		t.Fatalf("precondition failed: %v", accounts[0].Scope)
	}

	h.google.grantScope(google.ScopeDriveFull)
	_, flow := h.start(t, "intent=upgrade&scope=drive&account_id="+accounts[0].ID, token)
	if flow == nil {
		t.Fatal("start did not redirect")
	}
	if got := authError(t, h.callback(t, flow, nil, token)); got != "" {
		t.Fatalf("unexpected auth_error %q", got)
	}

	upgraded, _ := h.store.GetAccount(ctx, user.ID, accounts[0].ID)
	if upgraded.Scope != store.ScopeDriveFull {
		t.Errorf("scope not upgraded: %v", upgraded.Scope)
	}
}

func TestReLinkingDoesNotNarrowAnExistingScope(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	token := h.login(t)

	user, _ := h.store.GetUserByEmail(ctx, "owner@example.test")
	accounts, _ := h.store.ListAccounts(ctx, user.ID)

	h.google.grantScope(google.ScopeDriveFull)
	_, flow := h.start(t, "intent=upgrade&scope=drive&account_id="+accounts[0].ID, token)
	h.callback(t, flow, nil, token)

	// a later drive.file link must not silently downgrade the granted access
	h.google.grantScope(google.ScopeDriveFile)
	_, flow = h.start(t, "intent=link&scope=drive.file", token)
	h.callback(t, flow, nil, token)

	current, _ := h.store.GetAccount(ctx, user.ID, accounts[0].ID)
	if current.Scope != store.ScopeDriveFull {
		t.Errorf("scope was narrowed to %v", current.Scope)
	}
}

// --- callback hardening ----------------------------------------------------

func TestCallbackRejectsMissingOrForgedState(t *testing.T) {
	h := newHarness(t)
	_, valid := h.start(t, "intent=login", "")

	cases := map[string]*oauthFlow{
		"no nonce cookie": {state: valid.state, nonce: ""},
		"wrong nonce":     {state: valid.state, nonce: "not-the-nonce"},
		"forged state":    {state: "forged.signature", nonce: valid.nonce},
		"empty state":     {state: "", nonce: valid.nonce},
	}
	for name, flow := range cases {
		t.Run(name, func(t *testing.T) {
			resp := h.callback(t, flow, nil, "")
			if got := authError(t, resp); got != "bad_request" {
				t.Errorf("got auth_error %q, want bad_request", got)
			}
			if cookieValue(resp, auth.SessionCookie) != "" {
				t.Error("a session was issued for an invalid state")
			}
		})
	}
}

func TestCallbackSurfacesGoogleDenial(t *testing.T) {
	h := newHarness(t)
	_, flow := h.start(t, "intent=login", "")

	resp := h.callback(t, flow, url.Values{"error": {"access_denied"}}, "")
	if got := authError(t, resp); got != "forbidden" {
		t.Errorf("got auth_error %q", got)
	}
}

func TestCallbackRequiresACode(t *testing.T) {
	h := newHarness(t)
	_, flow := h.start(t, "intent=login", "")

	query := url.Values{"state": {flow.state}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/google/callback?"+query.Encode(), nil)
	req.AddCookie(&http.Cookie{Name: auth.StateCookie, Value: flow.nonce})

	if got := authError(t, h.send(t, req)); got != "bad_request" {
		t.Errorf("got auth_error %q", got)
	}
}

func TestCallbackNextCannotRedirectOffSite(t *testing.T) {
	h := newHarness(t)

	_, flow := h.start(t, "intent=login&next=https://evil.example/steal", "")
	resp := h.callback(t, flow, nil, "")

	location := resp.Header.Get("Location")
	if !strings.HasPrefix(location, testAppBaseURL+"/") {
		t.Errorf("redirected off-site: %q", location)
	}
}

func TestCallbackNextIsHonouredWhenLocal(t *testing.T) {
	h := newHarness(t)

	_, flow := h.start(t, "intent=login&next=/dashboard", "")
	resp := h.callback(t, flow, nil, "")

	if location := resp.Header.Get("Location"); !strings.HasPrefix(location, testAppBaseURL+"/dashboard?") {
		t.Errorf("unexpected redirect: %q", location)
	}
}

// --- session, logout -------------------------------------------------------

func TestSessionEndpointReturnsTheCurrentUser(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
	resp, env := h.sendJSON(t, req)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d: %#v", resp.StatusCode, env.Error)
	}
	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected data: %#v", env.Data)
	}
	user, ok := data["user"].(map[string]any)
	if !ok || user["email"] != "owner@example.test" {
		t.Errorf("unexpected user: %#v", data["user"])
	}
	if data["expires_at"] == nil {
		t.Error("expected expires_at")
	}
}

func TestSessionEndpointRequiresACookie(t *testing.T) {
	h := newHarness(t)

	resp, env := h.sendJSON(t, httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got status %d", resp.StatusCode)
	}
	if env.Error == nil || env.Error.Code != "unauthorized" {
		t.Errorf("unexpected error: %#v", env.Error)
	}
}

func TestLogoutRevokesTheSession(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
	req.Header.Set(auth.CSRFHeader, h.auth.CSRFToken(token))

	resp := h.send(t, req)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("got status %d", resp.StatusCode)
	}
	if !cookieCleared(resp, auth.SessionCookie) || !cookieCleared(resp, auth.CSRFCookie) {
		t.Error("cookies were not cleared")
	}

	// the token must be dead server-side, not merely dropped by the browser
	check := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	check.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
	if resp := h.send(t, check); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("session still valid after logout: %d", resp.StatusCode)
	}
}

func TestWritesRequireACSRFToken(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)

	cases := map[string]string{
		"missing":         "",
		"from nowhere":    "deadbeef",
		"another session": h.auth.CSRFToken("some-other-session-token"),
	}
	for name, csrf := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
			req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
			if csrf != "" {
				req.Header.Set(auth.CSRFHeader, csrf)
			}

			resp, env := h.sendJSON(t, req)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("got status %d, want 403", resp.StatusCode)
			}
			if env.Error == nil || env.Error.Code != "csrf_invalid" {
				t.Errorf("unexpected error: %#v", env.Error)
			}
		})
	}
}

func TestLogoutAllEndsEverySession(t *testing.T) {
	h := newHarness(t)
	first := h.login(t)
	second := h.login(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout-all", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: second})
	req.Header.Set(auth.CSRFHeader, h.auth.CSRFToken(second))

	if resp := h.send(t, req); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("got status %d", resp.StatusCode)
	}

	for name, token := range map[string]string{"first": first, "second": second} {
		check := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
		check.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
		if resp := h.send(t, check); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s session survived logout-all: %d", name, resp.StatusCode)
		}
	}
}
