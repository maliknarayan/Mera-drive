package google

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingRefresher records how often Google's token endpoint would be hit.
type countingRefresher struct {
	calls atomic.Int32
	err   error
	// expiry applied to every minted token; zero means "let the manager decide".
	expiry time.Time
	// gate, when non-nil, blocks each refresh until closed.
	gate chan struct{}
}

func (r *countingRefresher) Refresh(_ context.Context, refreshToken string) (*Token, error) {
	if r.gate != nil {
		<-r.gate
	}
	n := r.calls.Add(1)
	if r.err != nil {
		return nil, r.err
	}
	return &Token{
		AccessToken:  "access-" + refreshToken + "-" + string(rune('0'+n)),
		RefreshToken: refreshToken,
		Expiry:       r.expiry,
	}, nil
}

func TestAccessTokenIsCachedPerAccount(t *testing.T) {
	ctx := context.Background()
	refresher := &countingRefresher{expiry: time.Now().Add(time.Hour)}
	manager := NewTokenManager(refresher)

	first, err := manager.AccessToken(ctx, "acc-1", "refresh-1")
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	second, err := manager.AccessToken(ctx, "acc-1", "refresh-1")
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}

	if first != second {
		t.Error("expected the cached token to be reused")
	}
	if calls := refresher.calls.Load(); calls != 1 {
		t.Errorf("refreshed %d times, want 1", calls)
	}

	// a different account must not share the entry
	if _, err := manager.AccessToken(ctx, "acc-2", "refresh-2"); err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if calls := refresher.calls.Load(); calls != 2 {
		t.Errorf("refreshed %d times, want 2", calls)
	}
}

func TestRotatedRefreshTokenInvalidatesTheCache(t *testing.T) {
	ctx := context.Background()
	refresher := &countingRefresher{expiry: time.Now().Add(time.Hour)}
	manager := NewTokenManager(refresher)

	if _, err := manager.AccessToken(ctx, "acc-1", "refresh-1"); err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	// reconnecting the account stores a new refresh token; the cached access
	// token was minted from the old one and must not be reused
	if _, err := manager.AccessToken(ctx, "acc-1", "refresh-2"); err != nil {
		t.Fatalf("AccessToken: %v", err)
	}

	if calls := refresher.calls.Load(); calls != 2 {
		t.Errorf("refreshed %d times, want 2", calls)
	}
}

func TestTokenIsRenewedBeforeItExpires(t *testing.T) {
	ctx := context.Background()
	// inside the skew window, so it must be treated as already stale
	refresher := &countingRefresher{expiry: time.Now().Add(refreshSkew / 2)}
	manager := NewTokenManager(refresher)

	if _, err := manager.AccessToken(ctx, "acc-1", "refresh-1"); err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if _, err := manager.AccessToken(ctx, "acc-1", "refresh-1"); err != nil {
		t.Fatalf("AccessToken: %v", err)
	}

	if calls := refresher.calls.Load(); calls != 2 {
		t.Errorf("refreshed %d times, want 2 — a token expiring within the skew must be renewed", calls)
	}
}

func TestMissingExpiryGetsADefaultLifetime(t *testing.T) {
	ctx := context.Background()
	refresher := &countingRefresher{}
	manager := NewTokenManager(refresher)

	if _, err := manager.AccessToken(ctx, "acc-1", "refresh-1"); err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if _, err := manager.AccessToken(ctx, "acc-1", "refresh-1"); err != nil {
		t.Fatalf("AccessToken: %v", err)
	}

	if calls := refresher.calls.Load(); calls != 1 {
		t.Errorf("refreshed %d times, want 1", calls)
	}
}

func TestConcurrentCallersShareOneRefresh(t *testing.T) {
	ctx := context.Background()
	refresher := &countingRefresher{
		expiry: time.Now().Add(time.Hour),
		gate:   make(chan struct{}),
	}
	manager := NewTokenManager(refresher)

	const workers = 8
	var wg sync.WaitGroup
	tokens := make([]string, workers)
	errs := make([]error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			tokens[index], errs[index] = manager.AccessToken(ctx, "acc-1", "refresh-1")
		}(i)
	}

	// let every worker reach the refresher before any completes
	time.Sleep(50 * time.Millisecond)
	close(refresher.gate)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
		if tokens[i] != tokens[0] {
			t.Errorf("worker %d got a different token", i)
		}
	}
	// the whole point: a fan-out must not stampede Google's token endpoint
	if calls := refresher.calls.Load(); calls != 1 {
		t.Errorf("refreshed %d times, want 1", calls)
	}
}

func TestForgetDropsTheCachedToken(t *testing.T) {
	ctx := context.Background()
	refresher := &countingRefresher{expiry: time.Now().Add(time.Hour)}
	manager := NewTokenManager(refresher)

	if _, err := manager.AccessToken(ctx, "acc-1", "refresh-1"); err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	manager.Forget("acc-1")
	if _, err := manager.AccessToken(ctx, "acc-1", "refresh-1"); err != nil {
		t.Fatalf("AccessToken: %v", err)
	}

	if calls := refresher.calls.Load(); calls != 2 {
		t.Errorf("refreshed %d times, want 2", calls)
	}
}

func TestRefreshFailureIsNotCached(t *testing.T) {
	ctx := context.Background()
	refresher := &countingRefresher{err: errors.New("google said no")}
	manager := NewTokenManager(refresher)

	if _, err := manager.AccessToken(ctx, "acc-1", "refresh-1"); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := manager.AccessToken(ctx, "acc-1", "refresh-1"); err == nil {
		t.Fatal("expected an error")
	}
	if calls := refresher.calls.Load(); calls != 2 {
		t.Errorf("refreshed %d times, want 2 — a failure must not be cached", calls)
	}
}
