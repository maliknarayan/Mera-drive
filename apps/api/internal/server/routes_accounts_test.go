package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/auth"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/google"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/store"
)

// authedRequest builds a request carrying the session cookie and CSRF header.
func (h *harness) authedRequest(t *testing.T, method, path, sessionToken string, body any) *http.Request {
	t.Helper()

	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, path, reader)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: sessionToken})
	req.Header.Set(auth.CSRFHeader, h.auth.CSRFToken(sessionToken))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// linkAccount runs a link flow for an additional Google identity.
func (h *harness) linkAccount(t *testing.T, sessionToken, sub, email string) {
	t.Helper()

	h.google.asGoogleAccount(sub, email)
	_, flow := h.start(t, "intent=link&scope=drive.file", sessionToken)
	if flow == nil {
		t.Fatal("link start did not redirect")
	}
	if got := authError(t, h.callback(t, flow, nil, sessionToken)); got != "" {
		t.Fatalf("link failed: %s", got)
	}
}

// accountsOf decodes the /accounts payload.
func accountsOf(t *testing.T, data any) []map[string]any {
	t.Helper()

	raw, ok := data.([]any)
	if !ok {
		t.Fatalf("unexpected accounts payload: %#v", data)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		account, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("unexpected account entry: %#v", item)
		}
		out = append(out, account)
	}
	return out
}

func metaOf(t *testing.T, meta any) map[string]any {
	t.Helper()

	value, ok := meta.(map[string]any)
	if !ok {
		t.Fatalf("unexpected meta: %#v", meta)
	}
	return value
}

// --- GET /accounts ---------------------------------------------------------

func TestAccountsRequireASession(t *testing.T) {
	h := newHarness(t)

	for _, path := range []string{"/api/v1/accounts", "/api/v1/storage"} {
		resp, env := h.sendJSON(t, httptest.NewRequest(http.MethodGet, path, nil))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: got status %d", path, resp.StatusCode)
		}
		if env.Error == nil || env.Error.Code != apperr.CodeUnauthorized {
			t.Errorf("%s: unexpected error %#v", path, env.Error)
		}
	}
}

func TestListAccountsReturnsLiveQuota(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)
	h.linkAccount(t, token, "google-sub-2", "second@example.test")

	limit := int64(1000)
	h.drive.quotaFor["access-for-refresh-token"] = google.StorageQuota{Limit: &limit, Usage: 250}

	resp, env := h.sendJSON(t, h.authedRequest(t, http.MethodGet, "/api/v1/accounts", token, nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d: %#v", resp.StatusCode, env.Error)
	}

	list := accountsOf(t, env.Data)
	if len(list) != 2 {
		t.Fatalf("got %d accounts, want 2", len(list))
	}
	if meta := metaOf(t, env.Meta); meta["count"] != float64(2) {
		t.Errorf("meta count: %v", meta["count"])
	}

	first := list[0]
	if first["email"] != "owner@example.test" {
		t.Errorf("unexpected first account: %v", first["email"])
	}
	if first["quota"] == nil {
		t.Error("expected live quota on a healthy account")
	}
	// credentials must never appear in a response
	for _, key := range []string{"refresh_token", "refresh_token_enc", "google_user_id"} {
		if _, present := first[key]; present {
			t.Errorf("account payload leaks %q", key)
		}
	}
}

func TestListAccountsReportsPartialFailure(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	token := h.login(t)
	h.linkAccount(t, token, "google-sub-2", "second@example.test")

	user, err := h.store.GetUserByEmail(ctx, "owner@example.test")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	stored, err := h.store.ListAccounts(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	broken := stored[1]

	h.tokens.errFor[broken.ID] = apperr.ReauthRequired("Google rejected the stored credentials.")

	resp, env := h.sendJSON(t, h.authedRequest(t, http.MethodGet, "/api/v1/accounts", token, nil))
	// one unhealthy Drive must not fail the page
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d: %#v", resp.StatusCode, env.Error)
	}

	meta := metaOf(t, env.Meta)
	failures, ok := meta["errors"].([]any)
	if !ok || len(failures) != 1 {
		t.Fatalf("expected one per-account error, got %#v", meta["errors"])
	}
	failure, ok := failures[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected failure entry: %#v", failures[0])
	}
	if failure["code"] != string(apperr.CodeReauthRequired) {
		t.Errorf("unexpected code: %v", failure["code"])
	}
	// the UI needs to know which card to put the Reconnect button on
	if failure["account_id"] != broken.ID {
		t.Errorf("failure not tagged with its account: %v", failure["account_id"])
	}

	list := accountsOf(t, env.Data)
	if len(list) != 2 {
		t.Fatalf("got %d accounts", len(list))
	}
	if list[0]["quota"] == nil {
		t.Error("the healthy account lost its data")
	}
	if list[1]["status"] != string(store.StatusReauthRequired) {
		t.Errorf("unhealthy account status: %v", list[1]["status"])
	}
}

// --- GET /storage ----------------------------------------------------------

func TestStorageReturnsTheAggregate(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)
	h.linkAccount(t, token, "google-sub-2", "second@example.test")

	resp, env := h.sendJSON(t, h.authedRequest(t, http.MethodGet, "/api/v1/storage", token, nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d: %#v", resp.StatusCode, env.Error)
	}

	summary, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected payload: %#v", env.Data)
	}
	if summary["account_count"] != float64(2) {
		t.Errorf("account_count: %v", summary["account_count"])
	}
	if summary["connected_count"] != float64(2) {
		t.Errorf("connected_count: %v", summary["connected_count"])
	}
	// both stub accounts share the default 15 GiB quota
	if summary["total_usage"] != float64(2048) {
		t.Errorf("total_usage: %v", summary["total_usage"])
	}
	if summary["total_limit"] == nil {
		t.Error("expected a total limit")
	}
}

// --- DELETE /accounts/:id --------------------------------------------------

func TestDisconnectAccountRemovesItAndRevokesAtGoogle(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	token := h.login(t)

	user, _ := h.store.GetUserByEmail(ctx, "owner@example.test")
	stored, _ := h.store.ListAccounts(ctx, user.ID)
	target := stored[0]

	resp := h.send(t, h.authedRequest(t, http.MethodDelete, "/api/v1/accounts/"+target.ID, token, nil))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("got status %d", resp.StatusCode)
	}

	remaining, _ := h.store.ListAccounts(ctx, user.ID)
	if len(remaining) != 0 {
		t.Errorf("account was not removed: %d rows", len(remaining))
	}
	if len(h.google.revoked) != 1 {
		t.Errorf("grant not revoked at Google: %v", h.google.revoked)
	}
}

func TestDisconnectUnknownAccountIs404(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)

	resp, env := h.sendJSON(t,
		h.authedRequest(t, http.MethodDelete, "/api/v1/accounts/not-a-real-id", token, nil))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got status %d", resp.StatusCode)
	}
	if env.Error == nil || env.Error.Code != apperr.CodeNotFound {
		t.Errorf("unexpected error: %#v", env.Error)
	}
}

func TestDisconnectCannotTouchAnotherUsersAccount(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.login(t)

	victim, _ := h.store.GetUserByEmail(ctx, "owner@example.test")
	victimAccounts, _ := h.store.ListAccounts(ctx, victim.ID)

	// a second SangamDrive user, created by signing in with another Google account
	h.google.asGoogleAccount("google-sub-attacker", "attacker@example.test")
	attackerToken := h.login(t)

	resp := h.send(t, h.authedRequest(t, http.MethodDelete,
		"/api/v1/accounts/"+victimAccounts[0].ID, attackerToken, nil))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got status %d, want 404", resp.StatusCode)
	}

	still, _ := h.store.ListAccounts(ctx, victim.ID)
	if len(still) != 1 {
		t.Error("another user was able to disconnect the account")
	}
}

func TestDisconnectRequiresCSRF(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	token := h.login(t)

	user, _ := h.store.GetUserByEmail(ctx, "owner@example.test")
	stored, _ := h.store.ListAccounts(ctx, user.ID)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/"+stored[0].ID, nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})

	resp, env := h.sendJSON(t, req)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("got status %d", resp.StatusCode)
	}
	if env.Error == nil || env.Error.Code != apperr.CodeCSRF {
		t.Errorf("unexpected error: %#v", env.Error)
	}
}

// --- PATCH /accounts/order -------------------------------------------------

func TestReorderAccounts(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	token := h.login(t)
	h.linkAccount(t, token, "google-sub-2", "second@example.test")
	h.linkAccount(t, token, "google-sub-3", "third@example.test")

	user, _ := h.store.GetUserByEmail(ctx, "owner@example.test")
	stored, _ := h.store.ListAccounts(ctx, user.ID)

	reversed := []string{stored[2].ID, stored[1].ID, stored[0].ID}
	resp := h.send(t, h.authedRequest(t, http.MethodPatch, "/api/v1/accounts/order", token,
		map[string]any{"account_ids": reversed}))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("got status %d", resp.StatusCode)
	}

	reordered, _ := h.store.ListAccounts(ctx, user.ID)
	for i, id := range reversed {
		if reordered[i].ID != id {
			t.Fatalf("position %d is %s, want %s", i, reordered[i].ID, id)
		}
	}
}

func TestReorderRejectsBadBodies(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)

	cases := map[string]struct {
		body       any
		wantStatus int
	}{
		"empty list":   {map[string]any{"account_ids": []string{}}, http.StatusUnprocessableEntity},
		"wrong length": {map[string]any{"account_ids": []string{"a", "b"}}, http.StatusUnprocessableEntity},
		"unknown id":   {map[string]any{"account_ids": []string{"nope"}}, http.StatusUnprocessableEntity},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			resp, _ := h.sendJSON(t,
				h.authedRequest(t, http.MethodPatch, "/api/v1/accounts/order", token, tc.body))
			if resp.StatusCode != tc.wantStatus {
				t.Errorf("got status %d want %d", resp.StatusCode, tc.wantStatus)
			}
		})
	}
}

func TestReorderRejectsNonJSON(t *testing.T) {
	h := newHarness(t)
	token := h.login(t)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/order",
		bytes.NewReader([]byte("not json")))
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
	req.Header.Set(auth.CSRFHeader, h.auth.CSRFToken(token))
	req.Header.Set("Content-Type", "application/json")

	resp, env := h.sendJSON(t, req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("got status %d", resp.StatusCode)
	}
	if env.Error == nil || env.Error.Code != apperr.CodeBadRequest {
		t.Errorf("unexpected error: %#v", env.Error)
	}
}
