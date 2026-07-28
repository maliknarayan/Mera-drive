package accounts

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/google"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/store"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/store/sqlite"
)

// --- fakes -----------------------------------------------------------------

// plainOpener treats the stored value as the refresh token, except for a
// sentinel that stands in for a ciphertext sealed with a rotated key.
type plainOpener struct{}

func (plainOpener) OpenRefreshToken(sealed string) (string, error) {
	if sealed == "undecryptable" {
		return "", apperr.ReauthRequired("This account's stored credentials could not be read.")
	}
	return sealed, nil
}

type fakeTokens struct {
	mu       sync.Mutex
	forgot   []string
	errFor   map[string]error
	minted   atomic.Int32
}

func newFakeTokens() *fakeTokens {
	return &fakeTokens{errFor: map[string]error{}}
}

func (f *fakeTokens) AccessToken(_ context.Context, accountID, refreshToken string) (string, error) {
	f.minted.Add(1)
	f.mu.Lock()
	err := f.errFor[accountID]
	f.mu.Unlock()

	if err != nil {
		return "", err
	}
	return "access-for-" + refreshToken, nil
}

func (f *fakeTokens) Forget(accountID string) {
	f.mu.Lock()
	f.forgot = append(f.forgot, accountID)
	f.mu.Unlock()
}

func (f *fakeTokens) didForget(accountID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range f.forgot {
		if id == accountID {
			return true
		}
	}
	return false
}

type fakeDrive struct {
	mu sync.Mutex
	// quotaFor is keyed by access token.
	quotaFor map[string]google.StorageQuota
	errFor   map[string]error

	inFlight atomic.Int32
	peak      atomic.Int32
	hold      time.Duration
}

func newFakeDrive() *fakeDrive {
	return &fakeDrive{
		quotaFor: map[string]google.StorageQuota{},
		errFor:   map[string]error{},
	}
}

func (f *fakeDrive) About(_ context.Context, accessToken string) (*google.About, error) {
	current := f.inFlight.Add(1)
	for {
		peak := f.peak.Load()
		if current <= peak || f.peak.CompareAndSwap(peak, current) {
			break
		}
	}
	defer f.inFlight.Add(-1)

	if f.hold > 0 {
		time.Sleep(f.hold)
	}

	f.mu.Lock()
	err := f.errFor[accessToken]
	quota, ok := f.quotaFor[accessToken]
	f.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if !ok {
		quota = google.StorageQuota{Usage: 1}
	}
	return &google.About{Quota: quota}, nil
}

type fakeRevoker struct {
	mu      sync.Mutex
	revoked []string
	err     error
}

func (f *fakeRevoker) Revoke(_ context.Context, token string) error {
	f.mu.Lock()
	f.revoked = append(f.revoked, token)
	f.mu.Unlock()
	return f.err
}

// --- harness ---------------------------------------------------------------

type fixture struct {
	service *Service
	store   *sqlite.Store
	tokens  *fakeTokens
	drive   *fakeDrive
	revoker *fakeRevoker
	userID  string
}

func newFixture(t *testing.T, concurrency int) *fixture {
	t.Helper()

	st, err := sqlite.Open(filepath.Join(t.TempDir(), "accounts.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Now().UTC()
	user := &store.User{ID: "user-1", Email: "owner@example.test", CreatedAt: now, UpdatedAt: now}
	if err := st.CreateUser(context.Background(), user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	tokens := newFakeTokens()
	drive := newFakeDrive()
	revoker := &fakeRevoker{}

	service := NewService(st, plainOpener{}, tokens, drive, revoker,
		Config{Concurrency: concurrency, Timeout: 2 * time.Second},
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	return &fixture{
		service: service, store: st, tokens: tokens,
		drive: drive, revoker: revoker, userID: user.ID,
	}
}

// addAccount inserts an account whose refresh token is the plaintext sentinel.
func (f *fixture) addAccount(t *testing.T, id, sealed string, status store.AccountStatus) *store.Account {
	t.Helper()

	now := time.Now().UTC()
	account := &store.Account{
		ID: id, UserID: f.userID, GoogleUserID: "google-" + id,
		Email: id + "@example.test", Name: id,
		Scope: store.ScopeDriveFile, Status: status,
		RefreshTokenEnc: sealed, ConnectedAt: now, UpdatedAt: now,
	}
	if err := f.store.CreateAccount(context.Background(), account); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	return account
}

func quotaOf(limit *int64, usage int64) google.StorageQuota {
	return google.StorageQuota{Limit: limit, Usage: usage, UsageInDrive: usage}
}

func int64p(v int64) *int64 { return &v }

// --- List ------------------------------------------------------------------

func TestListReturnsLiveQuotaPerAccount(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 4)

	f.addAccount(t, "acc-a", "refresh-a", store.StatusConnected)
	f.addAccount(t, "acc-b", "refresh-b", store.StatusConnected)

	f.drive.quotaFor["access-for-refresh-a"] = quotaOf(int64p(1000), 400)
	f.drive.quotaFor["access-for-refresh-b"] = quotaOf(int64p(2000), 500)

	views, failures, err := f.service.List(ctx, f.userID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
	if len(views) != 2 {
		t.Fatalf("got %d views", len(views))
	}
	if views[0].Quota == nil || views[0].Quota.Usage != 400 {
		t.Errorf("first account quota wrong: %+v", views[0].Quota)
	}
	if views[1].Quota == nil || views[1].Quota.Usage != 500 {
		t.Errorf("second account quota wrong: %+v", views[1].Quota)
	}
	// credentials must never reach a view
	for _, view := range views {
		if view.StatusReason != "" {
			t.Errorf("unexpected status reason: %q", view.StatusReason)
		}
	}
}

func TestListSurvivesOneUnhealthyAccount(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 4)

	f.addAccount(t, "acc-good", "refresh-good", store.StatusConnected)
	f.addAccount(t, "acc-bad", "refresh-bad", store.StatusConnected)

	f.drive.quotaFor["access-for-refresh-good"] = quotaOf(int64p(1000), 100)
	f.drive.errFor["access-for-refresh-bad"] = apperr.UpstreamUnavailable("Google Drive is having trouble.")

	views, failures, err := f.service.List(ctx, f.userID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("got %d views", len(views))
	}
	if len(failures) != 1 {
		t.Fatalf("got %d failures, want 1", len(failures))
	}

	// the failure must say which card to flag
	if failures[0].AccountID != "acc-bad" {
		t.Errorf("failure not tagged with its account: %+v", failures[0])
	}
	// and the healthy Drive must still render
	good := viewByID(views, "acc-good")
	if good.Quota == nil || good.Quota.Usage != 100 {
		t.Errorf("healthy account lost its data: %+v", good)
	}

	bad := viewByID(views, "acc-bad")
	if bad.Quota != nil {
		t.Error("failed account should carry no quota")
	}
	if bad.StatusReason == "" {
		t.Error("failed account should explain itself")
	}
	// a transient outage is not a credentials problem
	if bad.Status != store.StatusConnected {
		t.Errorf("status changed on a transient failure: %v", bad.Status)
	}
}

func TestListMarksAccountsWhoseCredentialsWereRejected(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 2)

	account := f.addAccount(t, "acc-1", "refresh-1", store.StatusConnected)
	f.tokens.errFor["acc-1"] = apperr.ReauthRequired("Google rejected the stored credentials.")

	views, failures, err := f.service.List(ctx, f.userID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(failures) != 1 || failures[0].Code != apperr.CodeReauthRequired {
		t.Fatalf("unexpected failures: %+v", failures)
	}
	if views[0].Status != store.StatusReauthRequired {
		t.Errorf("view status not updated: %v", views[0].Status)
	}

	// the state change must be persisted so the dashboard is correct on reload
	stored, err := f.store.GetAccount(ctx, f.userID, account.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if stored.Status != store.StatusReauthRequired {
		t.Errorf("status not persisted: %v", stored.Status)
	}
	if !f.tokens.didForget(account.ID) {
		t.Error("cached access token was not dropped")
	}
}

func TestListReportsUndecryptableCredentials(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 2)

	// what a rotated ENCRYPTION_KEY looks like from here
	f.addAccount(t, "acc-1", "undecryptable", store.StatusConnected)

	views, failures, err := f.service.List(ctx, f.userID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(failures) != 1 || failures[0].Code != apperr.CodeReauthRequired {
		t.Fatalf("unexpected failures: %+v", failures)
	}
	if views[0].Status != store.StatusReauthRequired {
		t.Errorf("view status not updated: %v", views[0].Status)
	}
	if f.tokens.minted.Load() != 0 {
		t.Error("should not have attempted a token refresh")
	}
}

func TestListSkipsDisconnectedAccounts(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 2)

	f.addAccount(t, "acc-1", "refresh-1", store.StatusDisconnected)

	views, failures, err := f.service.List(ctx, f.userID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("unexpected failures: %+v", failures)
	}
	if views[0].Quota != nil {
		t.Error("disconnected account should have no quota")
	}
	if f.tokens.minted.Load() != 0 {
		t.Error("a disconnected account must not cost a Google call")
	}
}

func TestListRespectsTheConcurrencyLimit(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 2)
	f.drive.hold = 40 * time.Millisecond

	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		f.addAccount(t, "acc-"+id, "refresh-"+id, store.StatusConnected)
	}

	if _, _, err := f.service.List(ctx, f.userID); err != nil {
		t.Fatalf("List: %v", err)
	}
	// raising concurrency makes 429s more likely, not throughput higher
	if peak := f.drive.peak.Load(); peak > 2 {
		t.Errorf("ran %d concurrent Google calls, limit is 2", peak)
	}
}

func TestListOfNoAccounts(t *testing.T) {
	views, failures, err := newFixture(t, 2).service.List(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(views) != 0 || len(failures) != 0 {
		t.Errorf("got %d views and %d failures", len(views), len(failures))
	}
}

// --- Summarise -------------------------------------------------------------

func TestSummariseAddsUpLimitedDrives(t *testing.T) {
	views := []*View{
		{Status: store.StatusConnected, Quota: &google.StorageQuota{Limit: int64p(1000), Usage: 400}},
		{Status: store.StatusConnected, Quota: &google.StorageQuota{Limit: int64p(2000), Usage: 500}},
	}

	summary := Summarise(views)
	if summary.TotalLimit == nil || *summary.TotalLimit != 3000 {
		t.Errorf("total limit: %v", summary.TotalLimit)
	}
	if summary.TotalUsage != 900 {
		t.Errorf("total usage: %d", summary.TotalUsage)
	}
	if summary.TotalFree == nil || *summary.TotalFree != 2100 {
		t.Errorf("total free: %v", summary.TotalFree)
	}
	if summary.AccountCount != 2 || summary.ConnectedCount != 2 {
		t.Errorf("counts wrong: %+v", summary)
	}
}

func TestSummariseKeepsUnlimitedDrivesOutOfTheLimit(t *testing.T) {
	views := []*View{
		{Status: store.StatusConnected, Quota: &google.StorageQuota{Limit: int64p(1000), Usage: 200}},
		{Status: store.StatusConnected, Quota: &google.StorageQuota{Usage: 9_000_000}},
	}

	summary := Summarise(views)
	if summary.UnlimitedCount != 1 {
		t.Errorf("unlimited count: %d", summary.UnlimitedCount)
	}
	// folding an unlimited Drive into the cap would badly understate free space
	if summary.TotalLimit == nil || *summary.TotalLimit != 1000 {
		t.Errorf("total limit: %v", summary.TotalLimit)
	}
	// usage still counts, so the number matches what the per-drive cards show
	if summary.TotalUsage != 9_000_200 {
		t.Errorf("total usage: %d", summary.TotalUsage)
	}
	if summary.TotalFree == nil || *summary.TotalFree != 0 {
		t.Errorf("total free should clamp at zero, got %v", summary.TotalFree)
	}
}

func TestSummariseWithNoQuotaAtAll(t *testing.T) {
	summary := Summarise([]*View{
		{Status: store.StatusReauthRequired},
		{Status: store.StatusDisconnected},
	})

	if summary.TotalLimit != nil || summary.TotalFree != nil {
		t.Errorf("expected no totals: %+v", summary)
	}
	if summary.AccountCount != 2 || summary.ConnectedCount != 0 {
		t.Errorf("counts wrong: %+v", summary)
	}
}

// --- Disconnect ------------------------------------------------------------

func TestDisconnectRevokesAndDeletes(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 2)
	account := f.addAccount(t, "acc-1", "refresh-1", store.StatusConnected)

	if err := f.service.Disconnect(ctx, f.userID, account.ID); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	if len(f.revoker.revoked) != 1 || f.revoker.revoked[0] != "refresh-1" {
		t.Errorf("token not revoked at Google: %v", f.revoker.revoked)
	}
	if !f.tokens.didForget(account.ID) {
		t.Error("cached access token not dropped")
	}
	if _, err := f.store.GetAccount(ctx, f.userID, account.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("account row survived: %v", err)
	}
}

func TestDisconnectProceedsWhenGoogleRefusesToRevoke(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 2)
	f.revoker.err = errors.New("google is down")
	account := f.addAccount(t, "acc-1", "refresh-1", store.StatusConnected)

	// a Google outage must not trap the user's credentials on this server
	if err := f.service.Disconnect(ctx, f.userID, account.ID); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if _, err := f.store.GetAccount(ctx, f.userID, account.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("account row survived: %v", err)
	}
}

func TestDisconnectDeletesEvenIfCredentialsCannotBeRead(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 2)
	account := f.addAccount(t, "acc-1", "undecryptable", store.StatusConnected)

	if err := f.service.Disconnect(ctx, f.userID, account.ID); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if len(f.revoker.revoked) != 0 {
		t.Error("should not have called Google with an unreadable token")
	}
	if _, err := f.store.GetAccount(ctx, f.userID, account.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("account row survived: %v", err)
	}
}

func TestDisconnectUnknownAccount(t *testing.T) {
	f := newFixture(t, 2)

	err := f.service.Disconnect(context.Background(), f.userID, "nope")
	if apperr.From(err).Code != apperr.CodeNotFound {
		t.Errorf("got %v", err)
	}
}

// --- Reorder ---------------------------------------------------------------

func TestReorderSetsSortOrder(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 2)
	f.addAccount(t, "acc-a", "refresh-a", store.StatusConnected)
	f.addAccount(t, "acc-b", "refresh-b", store.StatusConnected)
	f.addAccount(t, "acc-c", "refresh-c", store.StatusConnected)

	if err := f.service.Reorder(ctx, f.userID, []string{"acc-c", "acc-a", "acc-b"}); err != nil {
		t.Fatalf("Reorder: %v", err)
	}

	accounts, err := f.store.ListAccounts(ctx, f.userID)
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	got := []string{accounts[0].ID, accounts[1].ID, accounts[2].ID}
	want := []string{"acc-c", "acc-a", "acc-b"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order is %v, want %v", got, want)
		}
	}
}

func TestReorderRejectsIncompleteOrUnknownLists(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 2)
	f.addAccount(t, "acc-a", "refresh-a", store.StatusConnected)
	f.addAccount(t, "acc-b", "refresh-b", store.StatusConnected)

	cases := map[string][]string{
		"too short":  {"acc-a"},
		"unknown id": {"acc-a", "acc-z"},
		"duplicate":  {"acc-a", "acc-a"},
		"too long":   {"acc-a", "acc-b", "acc-a"},
	}
	for name, order := range cases {
		t.Run(name, func(t *testing.T) {
			err := f.service.Reorder(ctx, f.userID, order)
			if apperr.From(err).Code != apperr.CodeValidation {
				t.Errorf("got %v, want a validation error", err)
			}
		})
	}
}

func TestReorderIgnoresAnotherUsersAccounts(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 2)
	f.addAccount(t, "acc-a", "refresh-a", store.StatusConnected)

	now := time.Now().UTC()
	other := &store.User{ID: "user-2", Email: "other@example.test", CreatedAt: now, UpdatedAt: now}
	if err := f.store.CreateUser(ctx, other); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := f.store.CreateAccount(ctx, &store.Account{
		ID: "acc-other", UserID: "user-2", GoogleUserID: "google-other",
		Email: "other@example.test", Scope: store.ScopeDriveFile,
		Status: store.StatusConnected, RefreshTokenEnc: "x",
		ConnectedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	err := f.service.Reorder(ctx, f.userID, []string{"acc-other"})
	if apperr.From(err).Code != apperr.CodeValidation {
		t.Errorf("got %v, want a validation error", err)
	}
}

func viewByID(views []*View, id string) *View {
	for _, view := range views {
		if view.ID == id {
			return view
		}
	}
	return &View{}
}
