package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/cryptobox"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/store"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/store/sqlite"
)

func newTestService(t *testing.T, ttl time.Duration) (*Service, store.Store) {
	t.Helper()

	st, err := sqlite.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	enc := make([]byte, cryptobox.KeySize)
	mac := make([]byte, cryptobox.KeySize)
	for i := range enc {
		enc[i] = byte(i)
		mac[i] = byte(255 - i)
	}
	box, err := cryptobox.New(enc, mac)
	if err != nil {
		t.Fatalf("cryptobox: %v", err)
	}

	return NewService(st, box, CookieOptions{Secure: true}, ttl), st
}

func seedUser(t *testing.T, st store.Store) *store.User {
	t.Helper()

	now := time.Now().UTC()
	u := &store.User{
		ID: uuid.NewString(), Email: "user@example.test", Name: "User",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return u
}

// --- state -----------------------------------------------------------------

func TestSignAndVerifyState(t *testing.T) {
	svc, _ := newTestService(t, time.Hour)

	token, nonce, err := svc.SignState(State{
		Intent: IntentUpgrade, Scope: "drive", AccountID: "acc-1", Next: "/dashboard",
	})
	if err != nil {
		t.Fatalf("SignState: %v", err)
	}

	got, err := svc.VerifyState(token, nonce)
	if err != nil {
		t.Fatalf("VerifyState: %v", err)
	}
	if got.Intent != IntentUpgrade || got.Scope != "drive" || got.AccountID != "acc-1" {
		t.Errorf("state not round-tripped: %+v", got)
	}
	if got.Next != "/dashboard" {
		t.Errorf("Next not preserved: %q", got.Next)
	}
}

func TestVerifyStateRejectsBadInput(t *testing.T) {
	svc, _ := newTestService(t, time.Hour)

	token, nonce, err := svc.SignState(State{Intent: IntentLogin, Scope: "drive.file"})
	if err != nil {
		t.Fatalf("SignState: %v", err)
	}

	cases := map[string]struct{ token, nonce string }{
		"empty token":       {"", nonce},
		"no signature":      {"payload", nonce},
		"forged signature":  {splitPayload(token) + ".deadbeef", nonce},
		"missing nonce":     {token, ""},
		"wrong nonce":       {token, "some-other-nonce"},
		"corrupt payload":   {"!!!not-base64!!!.sig", nonce},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.VerifyState(tc.token, tc.nonce); err == nil {
				t.Error("expected verification to fail")
			}
		})
	}
}

func TestVerifyStateRejectsStaleState(t *testing.T) {
	svc, _ := newTestService(t, time.Hour)

	// forge a correctly signed but old state, which is what a bookmarked or
	// replayed consent link looks like
	stale := State{
		Nonce:    "nonce-123",
		Intent:   IntentLogin,
		Scope:    "drive.file",
		IssuedAt: time.Now().Add(-2 * stateTTL).Unix(),
	}
	payload, err := json.Marshal(stale)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	token := encoded + "." + svc.box.SignHMAC(encoded)

	_, err = svc.VerifyState(token, "nonce-123")
	if err == nil {
		t.Fatal("expected stale state to be rejected")
	}
	if got := apperr.From(err).Code; got != apperr.CodeBadRequest {
		t.Errorf("got code %q", got)
	}
}

func TestVerifyStateRejectsUnknownIntent(t *testing.T) {
	svc, _ := newTestService(t, time.Hour)

	forged := State{Nonce: "n", Intent: Intent("exfiltrate"), IssuedAt: time.Now().Unix()}
	payload, _ := json.Marshal(forged)
	encoded := base64.RawURLEncoding.EncodeToString(payload)

	if _, err := svc.VerifyState(encoded+"."+svc.box.SignHMAC(encoded), "n"); err == nil {
		t.Error("expected unknown intent to be rejected")
	}
}

func TestSignStateProducesFreshNonces(t *testing.T) {
	svc, _ := newTestService(t, time.Hour)

	_, first, _ := svc.SignState(State{Intent: IntentLogin})
	_, second, _ := svc.SignState(State{Intent: IntentLogin})
	if first == second {
		t.Error("expected a fresh nonce per flow")
	}
}

func TestSafeNextBlocksOpenRedirects(t *testing.T) {
	cases := map[string]string{
		"/dashboard":            "/dashboard",
		"/files?parent=abc":     "/files?parent=abc",
		"":                      "/",
		"https://evil.example":  "/",
		"//evil.example":        "/",
		"/\\evil.example":       "/",
		"dashboard":             "/",
		"/ok\r\nSet-Cookie: x":  "/",
	}
	for input, want := range cases {
		if got := SafeNext(input); got != want {
			t.Errorf("SafeNext(%q) = %q, want %q", input, got, want)
		}
	}
}

// --- sessions --------------------------------------------------------------

func TestIssueAndResolveSession(t *testing.T) {
	ctx := context.Background()
	svc, st := newTestService(t, time.Hour)
	user := seedUser(t, st)

	token, sess, err := svc.Issue(ctx, user.ID, "test-agent", "10.0.0.1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if token == "" {
		t.Fatal("expected a session token")
	}

	// the raw token must never be persisted
	stored, err := st.GetSessionByTokenHash(ctx, cryptobox.HashToken(token))
	if err != nil {
		t.Fatalf("GetSessionByTokenHash: %v", err)
	}
	if stored.TokenHash == token {
		t.Error("session token was stored in the clear")
	}

	gotUser, gotSess, err := svc.Resolve(ctx, token)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if gotUser.ID != user.ID || gotSess.ID != sess.ID {
		t.Errorf("resolved the wrong session: %+v", gotSess)
	}
}

func TestResolveRejectsBadTokens(t *testing.T) {
	ctx := context.Background()
	svc, st := newTestService(t, time.Hour)
	seedUser(t, st)

	for name, token := range map[string]string{
		"empty":   "",
		"unknown": "not-a-real-token",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := svc.Resolve(ctx, token); apperr.From(err).Code != apperr.CodeUnauthorized {
				t.Errorf("expected unauthorized, got %v", err)
			}
		})
	}
}

func TestResolveDeletesExpiredSession(t *testing.T) {
	ctx := context.Background()
	// negative TTL means the session is born expired
	svc, st := newTestService(t, -time.Minute)
	user := seedUser(t, st)

	token, _, err := svc.Issue(ctx, user.ID, "", "")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if _, _, err := svc.Resolve(ctx, token); apperr.From(err).Code != apperr.CodeUnauthorized {
		t.Fatalf("expected unauthorized, got %v", err)
	}
	// the row must be gone, not merely rejected
	if _, err := st.GetSessionByTokenHash(ctx, cryptobox.HashToken(token)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expired session row survived: %v", err)
	}
}

func TestResolveRejectsSessionOfDeletedUser(t *testing.T) {
	ctx := context.Background()
	svc, st := newTestService(t, time.Hour)
	user := seedUser(t, st)

	token, _, err := svc.Issue(ctx, user.ID, "", "")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := st.DeleteUser(ctx, user.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	if _, _, err := svc.Resolve(ctx, token); apperr.From(err).Code != apperr.CodeUnauthorized {
		t.Errorf("expected unauthorized, got %v", err)
	}
}

func TestRevokeAndRevokeAll(t *testing.T) {
	ctx := context.Background()
	svc, st := newTestService(t, time.Hour)
	user := seedUser(t, st)

	first, sess, _ := svc.Issue(ctx, user.ID, "", "")
	second, _, _ := svc.Issue(ctx, user.ID, "", "")

	if err := svc.Revoke(ctx, sess.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, _, err := svc.Resolve(ctx, first); err == nil {
		t.Error("revoked session still resolves")
	}
	if _, _, err := svc.Resolve(ctx, second); err != nil {
		t.Errorf("unrelated session was revoked: %v", err)
	}

	// revoking an already-gone session is not an error
	if err := svc.Revoke(ctx, sess.ID); err != nil {
		t.Errorf("second Revoke: %v", err)
	}

	if err := svc.RevokeAll(ctx, user.ID); err != nil {
		t.Fatalf("RevokeAll: %v", err)
	}
	if _, _, err := svc.Resolve(ctx, second); err == nil {
		t.Error("RevokeAll left a session alive")
	}
}

// --- CSRF ------------------------------------------------------------------

func TestCSRFTokenIsBoundToItsSession(t *testing.T) {
	svc, _ := newTestService(t, time.Hour)

	tokenA := svc.CSRFToken("session-token-a")
	tokenB := svc.CSRFToken("session-token-b")

	if tokenA == tokenB {
		t.Fatal("two sessions produced the same CSRF token")
	}
	if !svc.VerifyCSRF("session-token-a", tokenA) {
		t.Error("valid CSRF token rejected")
	}
	// the core property: a token minted elsewhere must not validate here
	if svc.VerifyCSRF("session-token-a", tokenB) {
		t.Error("CSRF token from another session was accepted")
	}
	if svc.VerifyCSRF("session-token-a", "") || svc.VerifyCSRF("", tokenA) {
		t.Error("empty input was accepted")
	}
}

// --- refresh tokens --------------------------------------------------------

func TestRefreshTokenSealRoundTrip(t *testing.T) {
	svc, _ := newTestService(t, time.Hour)

	sealed, err := svc.SealRefreshToken("1//google-refresh-token")
	if err != nil {
		t.Fatalf("SealRefreshToken: %v", err)
	}
	if sealed == "1//google-refresh-token" {
		t.Fatal("refresh token was not encrypted")
	}

	opened, err := svc.OpenRefreshToken(sealed)
	if err != nil {
		t.Fatalf("OpenRefreshToken: %v", err)
	}
	if opened != "1//google-refresh-token" {
		t.Errorf("got %q", opened)
	}
}

func TestOpenRefreshTokenAsksForReconnect(t *testing.T) {
	svc, _ := newTestService(t, time.Hour)

	// what a rotated ENCRYPTION_KEY looks like from here
	if _, err := svc.OpenRefreshToken("v1:AAAAAAAAAAAAAAAAAAAA"); apperr.From(err).Code != apperr.CodeReauthRequired {
		t.Errorf("expected reauth_required, got %v", err)
	}
}

func splitPayload(token string) string {
	for i := len(token) - 1; i >= 0; i-- {
		if token[i] == '.' {
			return token[:i]
		}
	}
	return token
}
