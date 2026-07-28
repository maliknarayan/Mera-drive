package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
	"github.com/sangamdrive/sangamdrive/apps/api/internal/cryptobox"
)

// Intent is what the user is trying to achieve by running the OAuth flow.
type Intent string

const (
	// IntentLogin signs in, creating the SangamDrive user if needed.
	IntentLogin Intent = "login"
	// IntentLink adds another Google account to the signed-in user.
	IntentLink Intent = "link"
	// IntentReconnect replaces a rejected refresh token on an existing account.
	IntentReconnect Intent = "reconnect"
	// IntentUpgrade re-consents an account with a broader Drive scope.
	IntentUpgrade Intent = "upgrade"
)

// Valid reports whether i is a recognised intent.
func (i Intent) Valid() bool {
	switch i {
	case IntentLogin, IntentLink, IntentReconnect, IntentUpgrade:
		return true
	default:
		return false
	}
}

// RequiresSession reports whether the intent may only run for a signed-in user.
func (i Intent) RequiresSession() bool { return i != IntentLogin }

// RequiresAccount reports whether the intent targets an existing account row.
func (i Intent) RequiresAccount() bool {
	return i == IntentReconnect || i == IntentUpgrade
}

// stateTTL bounds how long a consent screen may sit open before its state is
// refused. Long enough for a slow account chooser, short enough to matter.
const stateTTL = 15 * time.Minute

// stateNonceBytes is the entropy in the nonce that binds state to one browser.
const stateNonceBytes = 24

// State is the payload carried through the OAuth round trip.
//
// It is signed rather than stored: a database table of pending logins would be
// state we would then have to expire, and it buys nothing over an HMAC.
type State struct {
	Nonce     string `json:"n"`
	Intent    Intent `json:"i"`
	Scope     string `json:"s"`
	AccountID string `json:"a,omitempty"`
	// Next is a site-relative path to return the browser to.
	Next     string `json:"r,omitempty"`
	IssuedAt int64  `json:"t"`
}

// SignState encodes and signs state, returning the opaque token for the `state`
// query parameter plus the nonce to store in the browser's state cookie.
func (s *Service) SignState(state State) (token, nonce string, err error) {
	nonce, err = cryptobox.RandomToken(stateNonceBytes)
	if err != nil {
		return "", "", apperr.Internal("Could not start the Google sign-in.").WithCause(err)
	}

	state.Nonce = nonce
	state.IssuedAt = time.Now().Unix()

	payload, err := json.Marshal(state)
	if err != nil {
		return "", "", apperr.Internal("Could not start the Google sign-in.").WithCause(err)
	}

	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + s.box.SignHMAC(encoded), nonce, nil
}

// VerifyState authenticates a returned state token.
//
// Three things must hold: the HMAC proves this instance minted it, the nonce
// proves the same browser started the flow, and the age proves it is not a
// replay of an old link.
func (s *Service) VerifyState(token, cookieNonce string) (*State, error) {
	invalid := apperr.BadRequest(
		"This sign-in link is no longer valid. Please start again.",
	)

	encoded, signature, found := strings.Cut(token, ".")
	if !found || encoded == "" || signature == "" {
		return nil, invalid
	}
	if !s.box.VerifyHMAC(encoded, signature) {
		return nil, invalid
	}

	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, invalid
	}

	var state State
	if err := json.Unmarshal(payload, &state); err != nil {
		return nil, invalid
	}

	if cookieNonce == "" || !constantTimeEqual(state.Nonce, cookieNonce) {
		return nil, invalid
	}
	if !state.Intent.Valid() {
		return nil, invalid
	}

	age := time.Since(time.Unix(state.IssuedAt, 0))
	if age < -time.Minute || age > stateTTL {
		return nil, apperr.BadRequest("This sign-in took too long to complete. Please start again.")
	}

	return &state, nil
}

// SafeNext validates a caller-supplied return path, defending against open
// redirects. Only site-relative paths are ever accepted.
func SafeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") {
		return "/"
	}
	// "//host" and "/\host" are protocol-relative URLs, not local paths
	if strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	if strings.ContainsAny(next, "\r\n") {
		return "/"
	}
	return next
}
