package server

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/auth"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/google"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/httpx"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/store"
)

type userDTO struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

type sessionDTO struct {
	User      userDTO   `json:"user"`
	ExpiresAt time.Time `json:"expires_at"`
}

func toUserDTO(u *store.User) userDTO {
	return userDTO{ID: u.ID, Email: u.Email, Name: u.Name, AvatarURL: u.AvatarURL}
}

// handleAuthStart redirects the browser to Google's consent screen.
//
//	GET /api/v1/auth/google/start?intent=login|link|reconnect|upgrade
//	                             &scope=drive.file|drive
//	                             &account_id=...   (reconnect, upgrade)
//	                             &next=/some/path
func (s *Server) handleAuthStart(c *fiber.Ctx) error {
	intent := auth.Intent(strings.TrimSpace(c.Query("intent", string(auth.IntentLogin))))
	if !intent.Valid() {
		return apperr.BadRequest("Unknown sign-in intent %q.", intent)
	}

	scope := store.Scope(strings.TrimSpace(c.Query("scope", string(store.ScopeDriveFile))))
	if !scope.Valid() {
		return apperr.BadRequest("Unknown permission level %q.", scope)
	}
	if intent == auth.IntentUpgrade && scope != store.ScopeDriveFull {
		return apperr.BadRequest("Upgrading permissions requires the full Drive scope.")
	}

	accountID := strings.TrimSpace(c.Query("account_id"))
	if intent.RequiresAccount() && accountID == "" {
		return apperr.BadRequest("account_id is required to %s an account.", intent)
	}

	if intent.RequiresSession() {
		user, _, err := s.resolveSession(c)
		if err != nil {
			return err
		}
		// fail before bouncing the user through Google if the target is not theirs
		if accountID != "" {
			if _, err := s.deps.Store.GetAccount(c.Context(), user.ID, accountID); err != nil {
				return mapAccountLookupError(err)
			}
		}
	}

	state, nonce, err := s.deps.Auth.SignState(auth.State{
		Intent:    intent,
		Scope:     string(scope),
		AccountID: accountID,
		Next:      auth.SafeNext(c.Query("next")),
	})
	if err != nil {
		return err
	}
	s.deps.Auth.SetStateCookie(c, nonce)

	scopes := append(google.IdentityScopes(), driveScopeURL(scope))
	return c.Redirect(s.deps.Google.AuthCodeURL(state, scopes), fiber.StatusFound)
}

// handleAuthCallback completes the OAuth round trip.
//
// Every failure path redirects back to the web app with an `auth_error` code
// rather than rendering JSON: the browser is mid-navigation, and a raw JSON body
// would be a dead end for the user.
func (s *Server) handleAuthCallback(c *fiber.Ctx) error {
	nonce := c.Cookies(auth.StateCookie)
	// clear immediately so a replayed callback cannot reuse this nonce
	s.deps.Auth.ClearStateCookie(c)

	if googleErr := c.Query("error"); googleErr != "" {
		return s.redirectAuthFailure(c, "/", apperr.Forbidden(
			"Google did not grant access: %s.", googleErr,
		))
	}

	state, err := s.deps.Auth.VerifyState(c.Query("state"), nonce)
	if err != nil {
		return s.redirectAuthFailure(c, "/", err)
	}

	next := auth.SafeNext(state.Next)

	code := c.Query("code")
	if code == "" {
		return s.redirectAuthFailure(c, next, apperr.BadRequest("Google did not return an authorisation code."))
	}

	result, err := s.completeOAuth(c, state, code)
	if err != nil {
		return s.redirectAuthFailure(c, next, err)
	}

	return c.Redirect(s.appURL(next, url.Values{
		"auth":    {string(state.Intent)},
		"account": {result.Email},
	}), fiber.StatusFound)
}

type oauthResult struct {
	Email string
}

// completeOAuth performs the intent-specific work after a successful exchange.
func (s *Server) completeOAuth(c *fiber.Ctx, state *auth.State, code string) (*oauthResult, error) {
	ctx := c.Context()

	token, err := s.deps.Google.Exchange(ctx, code)
	if err != nil {
		return nil, err
	}
	if token.RefreshToken == "" {
		// prompt=consent should always yield one; if not, a stale grant is in the way
		return nil, apperr.BadRequest(
			"Google did not return a refresh token. Remove SangamDrive at " +
				"myaccount.google.com/permissions and connect the account again.",
		)
	}

	profile, err := s.deps.Google.Profile(ctx, token.AccessToken)
	if err != nil {
		return nil, err
	}
	if !profile.EmailVerified {
		return nil, apperr.Forbidden("This Google account's email address is not verified.")
	}

	scope := store.Scope(state.Scope)
	if !token.HasScope(driveScopeURL(scope)) {
		return nil, apperr.InsufficientScope(
			"The requested Drive permission was not granted. Please approve it to continue.",
		)
	}

	sealed, err := s.deps.Auth.SealRefreshToken(token.RefreshToken)
	if err != nil {
		return nil, err
	}

	switch state.Intent {
	case auth.IntentLogin:
		return s.completeLogin(c, profile, scope, sealed)
	case auth.IntentLink:
		return s.completeLink(c, profile, scope, sealed)
	case auth.IntentReconnect, auth.IntentUpgrade:
		return s.completeReconnect(c, state, profile, scope, sealed)
	default:
		return nil, apperr.BadRequest("Unknown sign-in intent %q.", state.Intent)
	}
}

// completeLogin signs the user in, creating the SangamDrive user on first use.
//
// Identity is the Google account's email address: signing in with a different
// Google account deliberately creates a separate SangamDrive user rather than
// silently joining an existing one.
func (s *Server) completeLogin(
	c *fiber.Ctx, profile *google.Profile, scope store.Scope, sealed string,
) (*oauthResult, error) {
	ctx := c.Context()

	user, err := s.deps.Store.GetUserByEmail(ctx, profile.Email)
	switch {
	case err == nil:
		// keep the display name and avatar current on every sign-in
		if user.Name != profile.Name || user.AvatarURL != profile.PictureURL {
			if err := s.deps.Store.UpdateUserProfile(ctx, user.ID, profile.Name, profile.PictureURL); err != nil {
				return nil, apperr.Internal("Could not update your profile.").WithCause(err)
			}
		}
	case errors.Is(err, store.ErrNotFound):
		now := time.Now().UTC()
		user = &store.User{
			ID:        uuid.NewString(),
			Email:     profile.Email,
			Name:      profile.Name,
			AvatarURL: profile.PictureURL,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := s.deps.Store.CreateUser(ctx, user); err != nil {
			return nil, apperr.Internal("Could not create your account.").WithCause(err)
		}
	default:
		return nil, apperr.Internal("Could not look up your account.").WithCause(err)
	}

	if _, err := s.upsertAccount(ctx, user.ID, profile, scope, sealed); err != nil {
		return nil, err
	}
	if err := s.startSession(c, user.ID); err != nil {
		return nil, err
	}
	return &oauthResult{Email: profile.Email}, nil
}

// completeLink adds another Google account to the signed-in user. Re-linking an
// account that is already connected refreshes it instead of failing.
func (s *Server) completeLink(
	c *fiber.Ctx, profile *google.Profile, scope store.Scope, sealed string,
) (*oauthResult, error) {
	user, _, err := s.resolveSession(c)
	if err != nil {
		return nil, err
	}
	if _, err := s.upsertAccount(c.Context(), user.ID, profile, scope, sealed); err != nil {
		return nil, err
	}
	return &oauthResult{Email: profile.Email}, nil
}

// completeReconnect replaces the credentials on an existing account, optionally
// widening its scope.
func (s *Server) completeReconnect(
	c *fiber.Ctx, state *auth.State, profile *google.Profile, scope store.Scope, sealed string,
) (*oauthResult, error) {
	ctx := c.Context()

	user, _, err := s.resolveSession(c)
	if err != nil {
		return nil, err
	}

	account, err := s.deps.Store.GetAccount(ctx, user.ID, state.AccountID)
	if err != nil {
		return nil, mapAccountLookupError(err)
	}
	if account.GoogleUserID != profile.Sub {
		return nil, apperr.Conflict(
			"You signed in as %s, but this card belongs to %s. Choose the matching Google account.",
			profile.Email, account.Email,
		).WithAccount(account.ID)
	}

	account.Email = profile.Email
	account.Name = profile.Name
	account.AvatarURL = profile.PictureURL
	account.Scope = scope
	account.Status = store.StatusConnected
	account.RefreshTokenEnc = sealed

	if err := s.deps.Store.UpdateAccount(ctx, account); err != nil {
		return nil, apperr.Internal("Could not update the connected account.").WithCause(err)
	}
	return &oauthResult{Email: profile.Email}, nil
}

// upsertAccount creates or refreshes the account row for one Google identity.
func (s *Server) upsertAccount(
	ctx context.Context, userID string, profile *google.Profile, scope store.Scope, sealed string,
) (*store.Account, error) {
	existing, err := s.deps.Store.GetAccountByGoogleID(ctx, userID, profile.Sub)
	switch {
	case err == nil:
		existing.Email = profile.Email
		existing.Name = profile.Name
		existing.AvatarURL = profile.PictureURL
		existing.Status = store.StatusConnected
		existing.RefreshTokenEnc = sealed
		// never silently narrow a scope the user already granted
		if scope == store.ScopeDriveFull || existing.Scope == store.ScopeDriveFile {
			existing.Scope = scope
		}
		if err := s.deps.Store.UpdateAccount(ctx, existing); err != nil {
			return nil, apperr.Internal("Could not update the connected account.").WithCause(err)
		}
		return existing, nil

	case errors.Is(err, store.ErrNotFound):
		siblings, err := s.deps.Store.ListAccounts(ctx, userID)
		if err != nil {
			return nil, apperr.Internal("Could not read your connected accounts.").WithCause(err)
		}
		if len(siblings) >= maxAccountsPerUser {
			return nil, apperr.Conflict(
				"You have reached the limit of %d connected accounts.", maxAccountsPerUser,
			)
		}

		now := time.Now().UTC()
		account := &store.Account{
			ID:              uuid.NewString(),
			UserID:          userID,
			GoogleUserID:    profile.Sub,
			Email:           profile.Email,
			Name:            profile.Name,
			AvatarURL:       profile.PictureURL,
			Scope:           scope,
			Status:          store.StatusConnected,
			RefreshTokenEnc: sealed,
			SortOrder:       len(siblings),
			ConnectedAt:     now,
			UpdatedAt:       now,
		}
		if err := s.deps.Store.CreateAccount(ctx, account); err != nil {
			if errors.Is(err, store.ErrConflict) {
				return nil, apperr.Conflict("That Google account is already connected.")
			}
			return nil, apperr.Internal("Could not connect the Google account.").WithCause(err)
		}
		return account, nil

	default:
		return nil, apperr.Internal("Could not read your connected accounts.").WithCause(err)
	}
}

// startSession issues a session and writes both cookies.
func (s *Server) startSession(c *fiber.Ctx, userID string) error {
	token, sess, err := s.deps.Auth.Issue(c.Context(), userID, c.Get(fiber.HeaderUserAgent), c.IP())
	if err != nil {
		return err
	}
	s.deps.Auth.SetSessionCookies(c, token, sess.ExpiresAt)
	return nil
}

// handleAuthSession returns the signed-in user.
func (s *Server) handleAuthSession(c *fiber.Ctx) error {
	return httpx.OK(c, sessionDTO{
		User:      toUserDTO(currentUser(c)),
		ExpiresAt: currentSession(c).ExpiresAt,
	})
}

// handleLogout ends the current session.
func (s *Server) handleLogout(c *fiber.Ctx) error {
	if err := s.deps.Auth.Revoke(c.Context(), currentSession(c).ID); err != nil {
		return err
	}
	s.deps.Auth.ClearSessionCookies(c)
	return httpx.NoContent(c)
}

// handleLogoutAll ends every session for the current user.
func (s *Server) handleLogoutAll(c *fiber.Ctx) error {
	if err := s.deps.Auth.RevokeAll(c.Context(), currentUser(c).ID); err != nil {
		return err
	}
	s.deps.Auth.ClearSessionCookies(c)
	return httpx.NoContent(c)
}

// --- helpers ---------------------------------------------------------------

func driveScopeURL(scope store.Scope) string {
	if scope == store.ScopeDriveFull {
		return google.ScopeDriveFull
	}
	return google.ScopeDriveFile
}

func mapAccountLookupError(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return apperr.NotFound("That connected account does not exist.")
	}
	return apperr.Internal("Could not read the connected account.").WithCause(err)
}

// appURL builds an absolute URL back into the web app.
func (s *Server) appURL(path string, query url.Values) string {
	target := strings.TrimRight(s.deps.Config.AppBaseURL, "/") + path
	if len(query) == 0 {
		return target
	}
	separator := "?"
	if strings.Contains(target, "?") {
		separator = "&"
	}
	return target + separator + query.Encode()
}

// redirectAuthFailure sends the browser back to the app with a machine-readable
// error code, and logs the underlying cause server-side.
func (s *Server) redirectAuthFailure(c *fiber.Ctx, next string, err error) error {
	appErr := apperr.From(err)

	s.deps.Logger.Warn("oauth callback failed",
		slog.String("request_id", httpx.RequestIDOf(c)),
		slog.String("code", string(appErr.Code)),
		slog.String("error", appErr.Error()),
	)

	return c.Redirect(s.appURL(auth.SafeNext(next), url.Values{
		"auth_error":   {string(appErr.Code)},
		"auth_message": {appErr.Message},
	}), fiber.StatusFound)
}
