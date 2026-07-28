package server

import (
	"github.com/gofiber/fiber/v2"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/auth"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/store"
)

// Locals keys for the authenticated request context.
const (
	localUser         = "sangam.user"
	localSession      = "sangam.session"
	localSessionToken = "sangam.session_token"
	// localSessionID is read by the rate limiter to key on session rather than IP.
	localSessionID = "sangam.session_id"
)

// requireSession rejects the request unless a valid session cookie is present.
func (s *Server) requireSession(c *fiber.Ctx) error {
	user, sess, err := s.resolveSession(c)
	if err != nil {
		return err
	}
	storeSession(c, user, sess, c.Cookies(auth.SessionCookie))
	return c.Next()
}

// resolveSession validates the session cookie without mounting middleware, for
// routes where a session is optional.
func (s *Server) resolveSession(c *fiber.Ctx) (*store.User, *store.Session, error) {
	return s.deps.Auth.Resolve(c.Context(), c.Cookies(auth.SessionCookie))
}

func storeSession(c *fiber.Ctx, user *store.User, sess *store.Session, token string) {
	c.Locals(localUser, user)
	c.Locals(localSession, sess)
	c.Locals(localSessionToken, token)
	c.Locals(localSessionID, sess.ID)
}

// requireCSRF enforces the double-submit token on state-changing requests.
//
// Mounted before requireSession so a forged write is rejected without a database
// read, and so the failure is reported as CSRF rather than as an auth problem.
func (s *Server) requireCSRF(c *fiber.Ctx) error {
	switch c.Method() {
	case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
		return c.Next()
	}

	sessionToken := c.Cookies(auth.SessionCookie)
	if sessionToken == "" {
		return apperr.Unauthorized("You are not signed in.")
	}
	if !s.deps.Auth.VerifyCSRF(sessionToken, c.Get(auth.CSRFHeader)) {
		return apperr.CSRF("This request could not be verified. Please reload the page and try again.")
	}
	return c.Next()
}

// currentUser returns the authenticated user. Only valid behind requireSession.
func currentUser(c *fiber.Ctx) *store.User {
	user, _ := c.Locals(localUser).(*store.User)
	return user
}

// currentSession returns the active session. Only valid behind requireSession.
func currentSession(c *fiber.Ctx) *store.Session {
	sess, _ := c.Locals(localSession).(*store.Session)
	return sess
}
