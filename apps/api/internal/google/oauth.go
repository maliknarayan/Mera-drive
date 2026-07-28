// Package google wraps the Google OAuth2 endpoints SangamDrive needs.
//
// Endpoint URLs are injectable so tests can point the real exchange and profile
// code at an httptest server rather than at a hand-written fake that would drift
// from the production path.
package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
)

// OAuth scope URLs. The identity scopes are always requested; exactly one Drive
// scope is requested per connection.
const (
	ScopeOpenID    = "openid"
	ScopeEmail     = "https://www.googleapis.com/auth/userinfo.email"
	ScopeProfile   = "https://www.googleapis.com/auth/userinfo.profile"
	ScopeDriveFile = "https://www.googleapis.com/auth/drive.file"
	ScopeDriveFull = "https://www.googleapis.com/auth/drive"
)

// IdentityScopes are requested on every connection so we can label the account.
func IdentityScopes() []string { return []string{ScopeOpenID, ScopeEmail, ScopeProfile} }

// Endpoints are the Google URLs the authenticator talks to.
type Endpoints struct {
	Auth     string
	Token    string
	UserInfo string
	Revoke   string
}

// DefaultEndpoints returns the live Google endpoints.
//
// These are hardcoded rather than taken from golang.org/x/oauth2/google, which
// would drag in the GCE metadata client for no benefit here.
func DefaultEndpoints() Endpoints {
	return Endpoints{
		Auth:     "https://accounts.google.com/o/oauth2/v2/auth",
		Token:    "https://oauth2.googleapis.com/token",
		UserInfo: "https://openidconnect.googleapis.com/v1/userinfo",
		Revoke:   "https://oauth2.googleapis.com/revoke",
	}
}

// Token is the result of a successful authorisation code exchange.
type Token struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
	// GrantedScopes is what the user actually consented to, which may be less
	// than what was requested.
	GrantedScopes []string
}

// HasScope reports whether the user granted the given scope.
func (t *Token) HasScope(scope string) bool {
	for _, granted := range t.GrantedScopes {
		if granted == scope {
			return true
		}
	}
	return false
}

// Profile is the subset of the OpenID userinfo response we display.
type Profile struct {
	// Sub is Google's stable account identifier. Email can change; this cannot.
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	PictureURL    string `json:"picture"`
}

// Provider is the surface handlers depend on.
type Provider interface {
	AuthCodeURL(state string, scopes []string) string
	Exchange(ctx context.Context, code string) (*Token, error)
	Profile(ctx context.Context, accessToken string) (*Profile, error)
	Revoke(ctx context.Context, token string) error
}

// Authenticator implements Provider against Google.
type Authenticator struct {
	clientID     string
	clientSecret string
	redirectURL  string
	endpoints    Endpoints
	client       *http.Client
}

var _ Provider = (*Authenticator)(nil)

// Option customises an Authenticator.
type Option func(*Authenticator)

// WithEndpoints overrides the Google URLs. Tests use this.
func WithEndpoints(e Endpoints) Option {
	return func(a *Authenticator) { a.endpoints = e }
}

// WithHTTPClient overrides the HTTP client.
func WithHTTPClient(c *http.Client) Option {
	return func(a *Authenticator) { a.client = c }
}

// NewAuthenticator builds an Authenticator for one OAuth client.
func NewAuthenticator(clientID, clientSecret, redirectURL string, opts ...Option) *Authenticator {
	a := &Authenticator{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURL:  redirectURL,
		endpoints:    DefaultEndpoints(),
		client:       &http.Client{Timeout: 20 * time.Second},
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

func (a *Authenticator) oauthConfig(scopes []string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     a.clientID,
		ClientSecret: a.clientSecret,
		RedirectURL:  a.redirectURL,
		Scopes:       scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:   a.endpoints.Auth,
			TokenURL:  a.endpoints.Token,
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
}

// AuthCodeURL builds the consent screen URL.
//
// access_type=offline plus prompt=consent is what makes Google return a refresh
// token; without prompt=consent it only issues one on the very first grant, so a
// user who reconnects would silently get no token at all.
func (a *Authenticator) AuthCodeURL(state string, scopes []string) string {
	return a.oauthConfig(scopes).AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.ApprovalForce,
		// incremental auth: upgrading to full drive keeps previously granted scopes
		oauth2.SetAuthURLParam("include_granted_scopes", "true"),
	)
}

// Exchange trades an authorisation code for tokens.
func (a *Authenticator) Exchange(ctx context.Context, code string) (*Token, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, a.client)

	tok, err := a.oauthConfig(nil).Exchange(ctx, code)
	if err != nil {
		return nil, mapOAuthError(err)
	}

	granted := strings.Fields(stringExtra(tok, "scope"))

	return &Token{
		AccessToken:   tok.AccessToken,
		RefreshToken:  tok.RefreshToken,
		Expiry:        tok.Expiry,
		GrantedScopes: granted,
	}, nil
}

// Profile fetches the OpenID userinfo for an access token.
func (a *Authenticator) Profile(ctx context.Context, accessToken string) (*Profile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.endpoints.UserInfo, nil)
	if err != nil {
		return nil, apperr.Internal("Could not build the Google profile request.").WithCause(err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, apperr.UpstreamUnavailable("Could not reach Google to read your profile.").WithCause(err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, apperr.UpstreamUnavailable("Google's profile response could not be read.").WithCause(err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, mapProfileStatus(resp.StatusCode, body)
	}

	var profile Profile
	if err := json.Unmarshal(body, &profile); err != nil {
		return nil, apperr.UpstreamUnavailable("Google returned an unexpected profile response.").WithCause(err)
	}
	if profile.Sub == "" || profile.Email == "" {
		return nil, apperr.UpstreamUnavailable("Google did not return an account identifier.")
	}
	return &profile, nil
}

// Revoke asks Google to invalidate a refresh or access token. Best-effort: a
// failure here must not block disconnecting an account locally.
func (a *Authenticator) Revoke(ctx context.Context, token string) error {
	form := url.Values{"token": {token}}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoints.Revoke,
		strings.NewReader(form.Encode()))
	if err != nil {
		return apperr.Internal("Could not build the Google revoke request.").WithCause(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.client.Do(req)
	if err != nil {
		return apperr.UpstreamUnavailable("Could not reach Google to revoke the token.").WithCause(err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))

	// Google answers 400 invalid_token for an already-revoked token, which is
	// the outcome we wanted anyway
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		return apperr.UpstreamUnavailable("Google refused to revoke the token (HTTP %d).", resp.StatusCode)
	}
	return nil
}

// mapOAuthError translates a token endpoint failure into a stable app error.
func mapOAuthError(err error) error {
	var retrieve *oauth2.RetrieveError
	if !errors.As(err, &retrieve) {
		return apperr.UpstreamUnavailable("Could not reach Google to complete sign-in.").WithCause(err)
	}

	switch retrieve.ErrorCode {
	case "invalid_grant":
		return apperr.ReauthRequired(
			"Google rejected the sign-in. The authorisation may have expired — please try again.",
		).WithCause(err)
	case "invalid_client", "unauthorized_client":
		return apperr.Internal(
			"This SangamDrive instance is misconfigured: Google rejected its OAuth client credentials.",
		).WithCause(err)
	case "invalid_scope":
		return apperr.BadRequest("Google rejected the requested permissions.").WithCause(err)
	}

	if retrieve.Response != nil && retrieve.Response.StatusCode >= 500 {
		return apperr.UpstreamUnavailable("Google is having trouble. Please try again shortly.").WithCause(err)
	}
	return apperr.BadRequest("Google could not complete the sign-in.").WithCause(err)
}

func mapProfileStatus(status int, body []byte) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return apperr.ReauthRequired("Google rejected the access token while reading your profile.")
	case status == http.StatusTooManyRequests:
		return apperr.RateLimited("Google is rate limiting this instance. Please try again shortly.")
	case status >= 500:
		return apperr.UpstreamUnavailable("Google is having trouble. Please try again shortly.")
	default:
		return apperr.UpstreamUnavailable("Google returned HTTP %d while reading your profile.", status).
			WithCause(fmt.Errorf("userinfo body: %s", truncate(body, 256)))
	}
}

func stringExtra(tok *oauth2.Token, key string) string {
	if v, ok := tok.Extra(key).(string); ok {
		return v
	}
	return ""
}

func truncate(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "…"
}
