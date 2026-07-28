// Package auth owns sessions, CSRF tokens, OAuth state and the cookies that
// carry them. Route handlers call into it; it never calls into them.
package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/cryptobox"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/store"
)

// Cookie names.
const (
	// SessionCookie holds the opaque session token. HttpOnly.
	SessionCookie = "sangam_session"
	// CSRFCookie holds the double-submit token. Readable by JavaScript by design.
	CSRFCookie = "sangam_csrf"
	// StateCookie holds the OAuth nonce for the duration of one flow. HttpOnly.
	StateCookie = "sangam_oauth"
)

// CSRFHeader is where the client echoes the CSRF cookie.
const CSRFHeader = "X-CSRF-Token"

const (
	// sessionTokenBytes is the entropy in a session token.
	sessionTokenBytes = 32
	// stateCookieTTL bounds how long a half-finished OAuth flow stays valid.
	stateCookieTTL = stateTTL
	// touchInterval throttles last_seen_at writes so a busy tab does not cause
	// one UPDATE per request.
	touchInterval = 5 * time.Minute
)

// CookieOptions describes how cookies are scoped for this deployment.
type CookieOptions struct {
	Domain string
	Secure bool
}

// Service issues and validates everything a signed-in request depends on.
type Service struct {
	store   store.Store
	box     *cryptobox.Box
	cookies CookieOptions
	ttl     time.Duration
}

// NewService builds an auth service. ttl is the session lifetime.
func NewService(st store.Store, box *cryptobox.Box, cookies CookieOptions, ttl time.Duration) *Service {
	return &Service{store: st, box: box, cookies: cookies, ttl: ttl}
}

// --- sessions --------------------------------------------------------------

// Issue creates a session for userID and returns the raw token. Only the token's
// hash is persisted, so a database leak yields no usable sessions.
func (s *Service) Issue(ctx context.Context, userID, userAgent, ip string) (string, *store.Session, error) {
	token, err := cryptobox.RandomToken(sessionTokenBytes)
	if err != nil {
		return "", nil, apperr.Internal("Could not start your session.").WithCause(err)
	}

	now := time.Now().UTC()
	sess := &store.Session{
		ID:         uuid.NewString(),
		UserID:     userID,
		TokenHash:  cryptobox.HashToken(token),
		UserAgent:  truncate(userAgent, 512),
		IPAddress:  truncate(ip, 64),
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(s.ttl),
	}
	if err := s.store.CreateSession(ctx, sess); err != nil {
		return "", nil, apperr.Internal("Could not start your session.").WithCause(err)
	}
	return token, sess, nil
}

// Resolve validates a session token and returns its user.
//
// An expired row is deleted on the way out rather than left for the sweeper, so
// a stolen-but-expired token is useless immediately.
func (s *Service) Resolve(ctx context.Context, token string) (*store.User, *store.Session, error) {
	if token == "" {
		return nil, nil, apperr.Unauthorized("You are not signed in.")
	}

	sess, err := s.store.GetSessionByTokenHash(ctx, cryptobox.HashToken(token))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil, apperr.Unauthorized("Your session is no longer valid. Please sign in again.")
		}
		return nil, nil, apperr.Internal("Could not read your session.").WithCause(err)
	}

	now := time.Now().UTC()
	if sess.Expired(now) {
		_ = s.store.DeleteSession(ctx, sess.ID)
		return nil, nil, apperr.Unauthorized("Your session has expired. Please sign in again.")
	}

	user, err := s.store.GetUserByID(ctx, sess.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// the user was deleted while the session lived on
			_ = s.store.DeleteSession(ctx, sess.ID)
			return nil, nil, apperr.Unauthorized("Your account no longer exists.")
		}
		return nil, nil, apperr.Internal("Could not read your account.").WithCause(err)
	}

	if now.Sub(sess.LastSeenAt) > touchInterval {
		if err := s.store.TouchSession(ctx, sess.ID, now); err != nil {
			return nil, nil, apperr.Internal("Could not update your session.").WithCause(err)
		}
		sess.LastSeenAt = now
	}

	return user, sess, nil
}

// Revoke ends one session.
func (s *Service) Revoke(ctx context.Context, sessionID string) error {
	if err := s.store.DeleteSession(ctx, sessionID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return apperr.Internal("Could not sign you out.").WithCause(err)
	}
	return nil
}

// RevokeAll ends every session for a user.
func (s *Service) RevokeAll(ctx context.Context, userID string) error {
	if err := s.store.DeleteUserSessions(ctx, userID); err != nil {
		return apperr.Internal("Could not sign you out everywhere.").WithCause(err)
	}
	return nil
}

// --- CSRF ------------------------------------------------------------------

// CSRFToken derives the double-submit token for a session token.
//
// Deriving rather than storing means the token is bound to exactly one session:
// a value minted for a different session cannot validate here, and there is no
// extra row to expire.
func (s *Service) CSRFToken(sessionToken string) string {
	return s.box.SignHMAC("csrf:" + cryptobox.HashToken(sessionToken))
}

// VerifyCSRF checks the header against the session cookie.
func (s *Service) VerifyCSRF(sessionToken, presented string) bool {
	if sessionToken == "" || presented == "" {
		return false
	}
	return constantTimeEqual(s.CSRFToken(sessionToken), presented)
}

// --- cookies ---------------------------------------------------------------

// SetSessionCookies writes the session and CSRF cookies.
//
// SameSite=Lax rather than Strict: the OAuth callback is a top-level cross-site
// navigation, and Strict would withhold the cookie on arrival back from Google.
func (s *Service) SetSessionCookies(c *fiber.Ctx, token string, expiresAt time.Time) {
	c.Cookie(&fiber.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		Domain:   s.cookies.Domain,
		Expires:  expiresAt,
		Secure:   s.cookies.Secure,
		HTTPOnly: true,
		SameSite: fiber.CookieSameSiteLaxMode,
	})
	c.Cookie(&fiber.Cookie{
		Name:    CSRFCookie,
		Value:   s.CSRFToken(token),
		Path:    "/",
		Domain:  s.cookies.Domain,
		Expires: expiresAt,
		Secure:  s.cookies.Secure,
		// the client must read this to echo it back
		HTTPOnly: false,
		SameSite: fiber.CookieSameSiteLaxMode,
	})
}

// ClearSessionCookies removes the session and CSRF cookies.
func (s *Service) ClearSessionCookies(c *fiber.Ctx) {
	s.expire(c, SessionCookie, true)
	s.expire(c, CSRFCookie, false)
}

// SetStateCookie stores the OAuth nonce for the duration of one flow.
func (s *Service) SetStateCookie(c *fiber.Ctx, nonce string) {
	c.Cookie(&fiber.Cookie{
		Name:     StateCookie,
		Value:    nonce,
		Path:     "/",
		Domain:   s.cookies.Domain,
		Expires:  time.Now().Add(stateCookieTTL),
		Secure:   s.cookies.Secure,
		HTTPOnly: true,
		SameSite: fiber.CookieSameSiteLaxMode,
	})
}

// ClearStateCookie removes the OAuth nonce, making the state single-use.
func (s *Service) ClearStateCookie(c *fiber.Ctx) { s.expire(c, StateCookie, true) }

func (s *Service) expire(c *fiber.Ctx, name string, httpOnly bool) {
	c.Cookie(&fiber.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		Domain:   s.cookies.Domain,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		Secure:   s.cookies.Secure,
		HTTPOnly: httpOnly,
		SameSite: fiber.CookieSameSiteLaxMode,
	})
}

// --- helpers ---------------------------------------------------------------

// SealRefreshToken encrypts a Google refresh token for storage.
func (s *Service) SealRefreshToken(refreshToken string) (string, error) {
	sealed, err := s.box.Seal(refreshToken)
	if err != nil {
		return "", apperr.Internal("Could not securely store the Google credentials.").WithCause(err)
	}
	return sealed, nil
}

// OpenRefreshToken decrypts a stored Google refresh token.
//
// Failure here means the ENCRYPTION_KEY changed, so the account must be
// reconnected rather than retried.
func (s *Service) OpenRefreshToken(sealed string) (string, error) {
	token, err := s.box.Open(sealed)
	if err != nil {
		return "", apperr.ReauthRequired(
			"This account's stored credentials could not be read. Please reconnect it.",
		).WithCause(err)
	}
	return token, nil
}

// SessionTTL is the configured session lifetime.
func (s *Service) SessionTTL() time.Duration { return s.ttl }

func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
