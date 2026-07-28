// Package store defines the persistence contract.
//
// SangamDrive persists only what Google cannot hold for us: who the user is,
// which Google accounts they linked, the sealed refresh token for each, their
// live sessions, and their UI preferences. No file metadata is ever written
// here — every listing is fetched live from the Drive API.
//
// Implementations live in subpackages (currently store/sqlite). Handlers depend
// on these interfaces only, so swapping in Postgres later touches one package.
package store

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotFound is returned when a lookup matches no row.
	ErrNotFound = errors.New("store: not found")
	// ErrConflict is returned when a write violates a uniqueness constraint,
	// for example linking a Google account that is already linked.
	ErrConflict = errors.New("store: conflict")
)

// Scope is the Drive OAuth scope an account was connected with.
type Scope string

const (
	// ScopeDriveFile grants access only to files created or opened via SangamDrive.
	ScopeDriveFile Scope = "drive.file"
	// ScopeDriveFull grants full read/write access to the Drive.
	ScopeDriveFull Scope = "drive"
)

// Valid reports whether s is a recognised scope.
func (s Scope) Valid() bool { return s == ScopeDriveFile || s == ScopeDriveFull }

// GoogleScope maps to the OAuth scope URL Google expects.
func (s Scope) GoogleScope() string {
	if s == ScopeDriveFull {
		return "https://www.googleapis.com/auth/drive"
	}
	return "https://www.googleapis.com/auth/drive.file"
}

// AccountStatus describes whether an account's stored credentials still work.
type AccountStatus string

const (
	// StatusConnected means the refresh token is believed valid.
	StatusConnected AccountStatus = "connected"
	// StatusReauthRequired means Google rejected the refresh token; the user
	// must reconnect this account before it can be used again.
	StatusReauthRequired AccountStatus = "reauth_required"
	// StatusDisconnected means the user removed the account but the row is kept
	// until it is hard-deleted, so the UI can show it greyed out.
	StatusDisconnected AccountStatus = "disconnected"
)

// User is a SangamDrive login, created from the first Google account connected.
type User struct {
	ID        string
	Email     string
	Name      string
	AvatarURL string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Account is one linked Google Drive account.
type Account struct {
	ID           string
	UserID       string
	GoogleUserID string
	Email        string
	Name         string
	AvatarURL    string
	Scope        Scope
	Status       AccountStatus

	// RefreshTokenEnc is the AES-GCM sealed refresh token. It never leaves the
	// server and is never included in an API response.
	RefreshTokenEnc string

	// SortOrder controls the display order of account cards.
	SortOrder int

	ConnectedAt time.Time
	LastUsedAt  *time.Time
	UpdatedAt   time.Time
}

// Session is a browser login. Only the hash of the session token is stored.
type Session struct {
	ID        string
	UserID    string
	TokenHash string
	UserAgent string
	IPAddress string
	CreatedAt time.Time
	LastSeenAt time.Time
	ExpiresAt time.Time
}

// Expired reports whether the session is past its expiry.
func (s *Session) Expired(now time.Time) bool { return !now.Before(s.ExpiresAt) }

// UserStore persists SangamDrive users.
type UserStore interface {
	CreateUser(ctx context.Context, u *User) error
	GetUserByID(ctx context.Context, id string) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	UpdateUserProfile(ctx context.Context, id, name, avatarURL string) error
	DeleteUser(ctx context.Context, id string) error
}

// AccountStore persists linked Google accounts.
type AccountStore interface {
	CreateAccount(ctx context.Context, a *Account) error
	GetAccount(ctx context.Context, userID, accountID string) (*Account, error)
	GetAccountByGoogleID(ctx context.Context, userID, googleUserID string) (*Account, error)
	ListAccounts(ctx context.Context, userID string) ([]*Account, error)
	UpdateAccount(ctx context.Context, a *Account) error
	SetAccountStatus(ctx context.Context, accountID string, status AccountStatus) error
	TouchAccount(ctx context.Context, accountID string, at time.Time) error
	DeleteAccount(ctx context.Context, userID, accountID string) error
}

// SessionStore persists browser sessions.
type SessionStore interface {
	CreateSession(ctx context.Context, s *Session) error
	GetSessionByTokenHash(ctx context.Context, tokenHash string) (*Session, error)
	TouchSession(ctx context.Context, id string, at time.Time) error
	DeleteSession(ctx context.Context, id string) error
	DeleteUserSessions(ctx context.Context, userID string) error
	// DeleteExpiredSessions prunes rows past their expiry and returns the count.
	DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error)
}

// PreferenceStore persists per-user UI preferences as opaque JSON values.
type PreferenceStore interface {
	GetPreferences(ctx context.Context, userID string) (map[string]string, error)
	SetPreference(ctx context.Context, userID, key, value string) error
	DeletePreference(ctx context.Context, userID, key string) error
}

// Store is the full persistence surface a running server needs.
type Store interface {
	UserStore
	AccountStore
	SessionStore
	PreferenceStore

	// Migrate brings the schema up to date. Safe to call on every boot.
	Migrate(ctx context.Context) error
	// Ping verifies the backing store is reachable.
	Ping(ctx context.Context) error
	Close() error
}
