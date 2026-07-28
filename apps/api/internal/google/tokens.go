package google

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
)

// refreshSkew renews an access token slightly early so a request never starts
// with a token that expires mid-flight.
const refreshSkew = 60 * time.Second

// defaultTokenLifetime is assumed when Google omits an expiry.
const defaultTokenLifetime = 55 * time.Minute

// Refresher mints access tokens from refresh tokens.
type Refresher interface {
	Refresh(ctx context.Context, refreshToken string) (*Token, error)
}

// TokenManager caches access tokens in memory for the life of the process.
//
// Access tokens are never persisted: they are short-lived bearer credentials, and
// writing them to disk would widen the blast radius of a database leak for no
// benefit. The cache is keyed by account and fingerprinted by the refresh token,
// so rotating stored credentials invalidates the entry automatically.
type TokenManager struct {
	refresher Refresher
	flight    singleflight.Group

	mu      sync.RWMutex
	entries map[string]tokenEntry
}

type tokenEntry struct {
	accessToken string
	expiry      time.Time
	fingerprint string
}

// NewTokenManager builds a token manager over a refresher.
func NewTokenManager(refresher Refresher) *TokenManager {
	return &TokenManager{refresher: refresher, entries: map[string]tokenEntry{}}
}

// AccessToken returns a usable access token for an account, refreshing only when
// the cached one is missing, stale, or was minted from different credentials.
//
// Concurrent callers for the same account share one refresh: a fan-out across
// eight workers must not fire eight token requests at Google.
func (m *TokenManager) AccessToken(ctx context.Context, accountID, refreshToken string) (string, error) {
	fingerprint := fingerprintOf(refreshToken)

	if token, ok := m.cached(accountID, fingerprint); ok {
		return token, nil
	}

	value, err, _ := m.flight.Do(accountID, func() (any, error) {
		// another goroutine may have refreshed while this one queued
		if token, ok := m.cached(accountID, fingerprint); ok {
			return token, nil
		}

		token, err := m.refresher.Refresh(ctx, refreshToken)
		if err != nil {
			return nil, err
		}

		expiry := token.Expiry
		if expiry.IsZero() {
			expiry = time.Now().Add(defaultTokenLifetime)
		}

		m.mu.Lock()
		m.entries[accountID] = tokenEntry{
			accessToken: token.AccessToken,
			expiry:      expiry,
			fingerprint: fingerprint,
		}
		m.mu.Unlock()

		return token.AccessToken, nil
	})
	if err != nil {
		return "", err
	}

	accessToken, ok := value.(string)
	if !ok || accessToken == "" {
		return "", apperr.Internal("Google returned an empty access token.")
	}
	return accessToken, nil
}

// Forget drops any cached token for an account. Called when an account is
// disconnected or its credentials are rejected.
func (m *TokenManager) Forget(accountID string) {
	m.mu.Lock()
	delete(m.entries, accountID)
	m.mu.Unlock()
}

func (m *TokenManager) cached(accountID, fingerprint string) (string, bool) {
	m.mu.RLock()
	entry, ok := m.entries[accountID]
	m.mu.RUnlock()

	if !ok || entry.fingerprint != fingerprint {
		return "", false
	}
	if time.Until(entry.expiry) <= refreshSkew {
		return "", false
	}
	return entry.accessToken, true
}

// fingerprintOf identifies a refresh token without holding it in the cache key.
func fingerprintOf(refreshToken string) string {
	sum := sha256.Sum256([]byte(refreshToken))
	return hex.EncodeToString(sum[:8])
}
