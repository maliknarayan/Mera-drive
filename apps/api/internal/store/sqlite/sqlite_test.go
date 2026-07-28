package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/store"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()

	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func seedUser(t *testing.T, s *Store, id, email string) *store.User {
	t.Helper()

	now := time.Now().UTC().Truncate(time.Millisecond)
	u := &store.User{
		ID: id, Email: email, Name: "Test User",
		AvatarURL: "https://example.test/a.png",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return u
}

func seedAccount(t *testing.T, s *Store, id, userID, googleID string) *store.Account {
	t.Helper()

	now := time.Now().UTC().Truncate(time.Millisecond)
	a := &store.Account{
		ID: id, UserID: userID, GoogleUserID: googleID,
		Email: googleID + "@example.test", Name: "Drive Account",
		Scope: store.ScopeDriveFile, Status: store.StatusConnected,
		RefreshTokenEnc: "v1:sealed", ConnectedAt: now, UpdatedAt: now,
	}
	if err := s.CreateAccount(context.Background(), a); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	return a
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := newTestStore(t)

	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}

func TestUserCRUD(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	want := seedUser(t, s, "user-1", "a@example.test")

	got, err := s.GetUserByID(ctx, want.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.Email != want.Email || got.Name != want.Name {
		t.Errorf("round trip mismatch: %+v", got)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CreatedAt not preserved: got %v want %v", got.CreatedAt, want.CreatedAt)
	}

	if _, err := s.GetUserByEmail(ctx, "a@example.test"); err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}

	if err := s.UpdateUserProfile(ctx, want.ID, "Renamed", "https://example.test/b.png"); err != nil {
		t.Fatalf("UpdateUserProfile: %v", err)
	}
	got, _ = s.GetUserByID(ctx, want.ID)
	if got.Name != "Renamed" {
		t.Errorf("profile not updated: %q", got.Name)
	}

	if err := s.DeleteUser(ctx, want.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := s.GetUserByID(ctx, want.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestUserNotFoundAndConflict(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedUser(t, s, "user-1", "a@example.test")

	if _, err := s.GetUserByID(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	if err := s.UpdateUserProfile(ctx, "missing", "x", ""); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}

	dup := &store.User{
		ID: "user-2", Email: "a@example.test",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.CreateUser(ctx, dup); !errors.Is(err, store.ErrConflict) {
		t.Errorf("expected ErrConflict on duplicate email, got %v", err)
	}
}

func TestAccountCRUD(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedUser(t, s, "user-1", "a@example.test")
	a := seedAccount(t, s, "acct-1", "user-1", "google-1")

	got, err := s.GetAccount(ctx, "user-1", "acct-1")
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if got.Scope != store.ScopeDriveFile || got.Status != store.StatusConnected {
		t.Errorf("unexpected account state: %+v", got)
	}
	if got.LastUsedAt != nil {
		t.Errorf("expected nil LastUsedAt, got %v", got.LastUsedAt)
	}

	if _, err := s.GetAccountByGoogleID(ctx, "user-1", "google-1"); err != nil {
		t.Fatalf("GetAccountByGoogleID: %v", err)
	}

	// scope upgrade
	a.Scope = store.ScopeDriveFull
	a.RefreshTokenEnc = "v1:rotated"
	if err := s.UpdateAccount(ctx, a); err != nil {
		t.Fatalf("UpdateAccount: %v", err)
	}
	got, _ = s.GetAccount(ctx, "user-1", "acct-1")
	if got.Scope != store.ScopeDriveFull || got.RefreshTokenEnc != "v1:rotated" {
		t.Errorf("update not applied: %+v", got)
	}

	if err := s.SetAccountStatus(ctx, "acct-1", store.StatusReauthRequired); err != nil {
		t.Fatalf("SetAccountStatus: %v", err)
	}
	got, _ = s.GetAccount(ctx, "user-1", "acct-1")
	if got.Status != store.StatusReauthRequired {
		t.Errorf("status not applied: %v", got.Status)
	}

	used := time.Now().UTC().Truncate(time.Millisecond)
	if err := s.TouchAccount(ctx, "acct-1", used); err != nil {
		t.Fatalf("TouchAccount: %v", err)
	}
	got, _ = s.GetAccount(ctx, "user-1", "acct-1")
	if got.LastUsedAt == nil || !got.LastUsedAt.Equal(used) {
		t.Errorf("LastUsedAt not persisted: %v", got.LastUsedAt)
	}

	if err := s.DeleteAccount(ctx, "user-1", "acct-1"); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	if _, err := s.GetAccount(ctx, "user-1", "acct-1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestAccountIsolatedPerUser(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedUser(t, s, "user-1", "a@example.test")
	seedUser(t, s, "user-2", "b@example.test")
	seedAccount(t, s, "acct-1", "user-1", "google-1")

	if _, err := s.GetAccount(ctx, "user-2", "acct-1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another user could read the account: %v", err)
	}
	if err := s.DeleteAccount(ctx, "user-2", "acct-1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another user could delete the account: %v", err)
	}

	// the same Google account may be linked by a different SangamDrive user
	if _, err := s.GetAccountByGoogleID(ctx, "user-2", "google-1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	seedAccount(t, s, "acct-2", "user-2", "google-1")
}

func TestAccountDuplicateGoogleIDConflicts(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedUser(t, s, "user-1", "a@example.test")
	seedAccount(t, s, "acct-1", "user-1", "google-1")

	now := time.Now().UTC()
	dup := &store.Account{
		ID: "acct-dup", UserID: "user-1", GoogleUserID: "google-1",
		Email: "x@example.test", Scope: store.ScopeDriveFile,
		Status: store.StatusConnected, RefreshTokenEnc: "v1:x",
		ConnectedAt: now, UpdatedAt: now,
	}
	if err := s.CreateAccount(ctx, dup); !errors.Is(err, store.ErrConflict) {
		t.Errorf("expected ErrConflict, got %v", err)
	}
}

func TestListAccountsRespectsSortOrder(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedUser(t, s, "user-1", "a@example.test")

	for i, id := range []string{"acct-c", "acct-a", "acct-b"} {
		a := seedAccount(t, s, id, "user-1", "google-"+id)
		a.SortOrder = 3 - i
		if err := s.UpdateAccount(ctx, a); err != nil {
			t.Fatalf("UpdateAccount: %v", err)
		}
	}

	accounts, err := s.ListAccounts(ctx, "user-1")
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(accounts) != 3 {
		t.Fatalf("expected 3 accounts, got %d", len(accounts))
	}
	if accounts[0].ID != "acct-b" || accounts[2].ID != "acct-c" {
		t.Errorf("unexpected order: %s, %s, %s", accounts[0].ID, accounts[1].ID, accounts[2].ID)
	}

	empty, err := s.ListAccounts(ctx, "nobody")
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected no accounts, got %d", len(empty))
	}
}

func TestDeletingUserCascades(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedUser(t, s, "user-1", "a@example.test")
	seedAccount(t, s, "acct-1", "user-1", "google-1")

	if err := s.SetPreference(ctx, "user-1", "theme", `"dark"`); err != nil {
		t.Fatalf("SetPreference: %v", err)
	}
	if err := s.DeleteUser(ctx, "user-1"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	accounts, _ := s.ListAccounts(ctx, "user-1")
	if len(accounts) != 0 {
		t.Errorf("accounts survived user deletion: %d", len(accounts))
	}
	prefs, _ := s.GetPreferences(ctx, "user-1")
	if len(prefs) != 0 {
		t.Errorf("preferences survived user deletion: %d", len(prefs))
	}
}

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedUser(t, s, "user-1", "a@example.test")

	now := time.Now().UTC().Truncate(time.Millisecond)
	sess := &store.Session{
		ID: "sess-1", UserID: "user-1", TokenHash: "hash-1",
		UserAgent: "test-agent", IPAddress: "127.0.0.1",
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := s.GetSessionByTokenHash(ctx, "hash-1")
	if err != nil {
		t.Fatalf("GetSessionByTokenHash: %v", err)
	}
	if got.UserID != "user-1" || got.Expired(now) {
		t.Errorf("unexpected session: %+v", got)
	}
	if !got.Expired(now.Add(2 * time.Hour)) {
		t.Error("session should report expired past its expiry")
	}

	later := now.Add(time.Minute)
	if err := s.TouchSession(ctx, "sess-1", later); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}
	got, _ = s.GetSessionByTokenHash(ctx, "hash-1")
	if !got.LastSeenAt.Equal(later) {
		t.Errorf("LastSeenAt not updated: %v", got.LastSeenAt)
	}

	if err := s.DeleteSession(ctx, "sess-1"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := s.GetSessionByTokenHash(ctx, "hash-1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDeleteExpiredSessions(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedUser(t, s, "user-1", "a@example.test")

	now := time.Now().UTC()
	for i, exp := range []time.Duration{-time.Hour, -time.Minute, time.Hour} {
		sess := &store.Session{
			ID: string(rune('a' + i)), UserID: "user-1",
			TokenHash: string(rune('a'+i)) + "-hash",
			CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(exp),
		}
		if err := s.CreateSession(ctx, sess); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
	}

	n, err := s.DeleteExpiredSessions(ctx, now)
	if err != nil {
		t.Fatalf("DeleteExpiredSessions: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 pruned sessions, got %d", n)
	}
	if _, err := s.GetSessionByTokenHash(ctx, "c-hash"); err != nil {
		t.Errorf("live session was pruned: %v", err)
	}
}

func TestDeleteUserSessions(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedUser(t, s, "user-1", "a@example.test")

	now := time.Now().UTC()
	for _, id := range []string{"s1", "s2"} {
		if err := s.CreateSession(ctx, &store.Session{
			ID: id, UserID: "user-1", TokenHash: id + "-hash",
			CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
		}); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
	}

	if err := s.DeleteUserSessions(ctx, "user-1"); err != nil {
		t.Fatalf("DeleteUserSessions: %v", err)
	}
	if _, err := s.GetSessionByTokenHash(ctx, "s1-hash"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected all sessions gone, got %v", err)
	}
}

func TestPreferencesUpsert(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedUser(t, s, "user-1", "a@example.test")

	if err := s.SetPreference(ctx, "user-1", "theme", `"dark"`); err != nil {
		t.Fatalf("SetPreference: %v", err)
	}
	if err := s.SetPreference(ctx, "user-1", "view", `"grid"`); err != nil {
		t.Fatalf("SetPreference: %v", err)
	}
	if err := s.SetPreference(ctx, "user-1", "theme", `"light"`); err != nil {
		t.Fatalf("SetPreference (upsert): %v", err)
	}

	prefs, err := s.GetPreferences(ctx, "user-1")
	if err != nil {
		t.Fatalf("GetPreferences: %v", err)
	}
	if len(prefs) != 2 || prefs["theme"] != `"light"` {
		t.Errorf("unexpected preferences: %v", prefs)
	}

	if err := s.DeletePreference(ctx, "user-1", "theme"); err != nil {
		t.Fatalf("DeletePreference: %v", err)
	}
	prefs, _ = s.GetPreferences(ctx, "user-1")
	if _, ok := prefs["theme"]; ok {
		t.Error("preference not deleted")
	}
}
