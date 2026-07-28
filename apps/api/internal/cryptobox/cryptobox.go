// Package cryptobox provides the symmetric primitives SangamDrive needs:
// authenticated encryption for refresh tokens, and one-way hashing plus HMAC
// for session and CSRF tokens.
//
// Refresh tokens are the only genuinely sensitive data the server persists.
// They are sealed with AES-256-GCM and stored as a self-describing string so a
// future key rotation can identify which key sealed which value.
package cryptobox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// KeySize is the required key length in bytes (AES-256).
const KeySize = 32

// versionPrefix tags every ciphertext so the format can evolve.
const versionPrefix = "v1"

var (
	// ErrKeySize is returned when a key is not exactly KeySize bytes.
	ErrKeySize = fmt.Errorf("key must be %d bytes", KeySize)
	// ErrCiphertext is returned for malformed or tampered ciphertext. It is
	// deliberately opaque — callers must not learn which check failed.
	ErrCiphertext = errors.New("ciphertext is invalid or was encrypted with a different key")
)

// Box seals and opens secrets with a single AES-256-GCM key.
type Box struct {
	aead cipher.AEAD
	mac  []byte
}

// New builds a Box. encryptionKey seals refresh tokens; macKey signs CSRF
// tokens. Both must be exactly KeySize bytes.
func New(encryptionKey, macKey []byte) (*Box, error) {
	if len(encryptionKey) != KeySize || len(macKey) != KeySize {
		return nil, ErrKeySize
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}
	// defensive copy so a caller mutating its slice cannot change our key
	mac := make([]byte, len(macKey))
	copy(mac, macKey)

	return &Box{aead: aead, mac: mac}, nil
}

// Seal encrypts plaintext and returns "v1:<base64(nonce||ciphertext)>".
func (b *Box) Seal(plaintext string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	sealed := b.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return versionPrefix + ":" + base64.RawStdEncoding.EncodeToString(sealed), nil
}

// Open reverses Seal. Any tampering fails the GCM tag check.
func (b *Box) Open(encoded string) (string, error) {
	version, payload, found := strings.Cut(encoded, ":")
	if !found || version != versionPrefix {
		return "", ErrCiphertext
	}
	raw, err := base64.RawStdEncoding.DecodeString(payload)
	if err != nil {
		return "", ErrCiphertext
	}
	nonceSize := b.aead.NonceSize()
	if len(raw) < nonceSize {
		return "", ErrCiphertext
	}
	plaintext, err := b.aead.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return "", ErrCiphertext
	}
	return string(plaintext), nil
}

// SignHMAC returns a hex HMAC-SHA256 of message under the MAC key.
func (b *Box) SignHMAC(message string) string {
	h := hmac.New(sha256.New, b.mac)
	h.Write([]byte(message))
	return hex.EncodeToString(h.Sum(nil))
}

// VerifyHMAC checks a signature in constant time.
func (b *Box) VerifyHMAC(message, signature string) bool {
	return subtle.ConstantTimeCompare([]byte(b.SignHMAC(message)), []byte(signature)) == 1
}

// RandomToken returns a URL-safe random string carrying n bytes of entropy.
// Used for session tokens, CSRF tokens and OAuth state.
func RandomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashToken returns the hex SHA-256 of a token. Session tokens are stored
// hashed so a database leak does not hand out live sessions.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
