package cryptobox

import (
	"strings"
	"testing"
)

func newTestBox(t *testing.T) *Box {
	t.Helper()
	enc := make([]byte, KeySize)
	mac := make([]byte, KeySize)
	for i := range enc {
		enc[i] = byte(i)
		mac[i] = byte(255 - i)
	}
	box, err := New(enc, mac)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return box
}

func TestNewRejectsShortKeys(t *testing.T) {
	short := make([]byte, KeySize-1)
	full := make([]byte, KeySize)

	if _, err := New(short, full); err == nil {
		t.Error("expected error for short encryption key")
	}
	if _, err := New(full, short); err == nil {
		t.Error("expected error for short mac key")
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	box := newTestBox(t)

	cases := []string{
		"",
		"1//0abcdefghijklmnop-refresh-token",
		strings.Repeat("x", 4096),
		"unicode ✓ ñ 日本語",
	}
	for _, plaintext := range cases {
		sealed, err := box.Seal(plaintext)
		if err != nil {
			t.Fatalf("Seal(%q): %v", plaintext, err)
		}
		if strings.Contains(sealed, plaintext) && plaintext != "" {
			t.Errorf("sealed value leaks plaintext: %q", sealed)
		}
		opened, err := box.Open(sealed)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if opened != plaintext {
			t.Errorf("round trip mismatch: got %q want %q", opened, plaintext)
		}
	}
}

func TestSealIsNonDeterministic(t *testing.T) {
	box := newTestBox(t)

	first, err := box.Seal("same-input")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	second, err := box.Seal("same-input")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if first == second {
		t.Error("expected a fresh nonce to produce different ciphertexts")
	}
}

func TestOpenRejectsTamperedInput(t *testing.T) {
	box := newTestBox(t)

	sealed, err := box.Seal("secret")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	tampered := []string{
		"",
		"not-versioned",
		"v2:" + strings.TrimPrefix(sealed, "v1:"),
		"v1:!!!not-base64!!!",
		"v1:AAAA",
		sealed[:len(sealed)-1] + flipLast(sealed),
	}
	for _, input := range tampered {
		if _, err := box.Open(input); err == nil {
			t.Errorf("expected Open(%q) to fail", input)
		}
	}
}

func TestOpenRejectsForeignKey(t *testing.T) {
	box := newTestBox(t)
	sealed, err := box.Seal("secret")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	other, err := New(make([]byte, KeySize), make([]byte, KeySize))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := other.Open(sealed); err == nil {
		t.Error("expected decryption with a different key to fail")
	}
}

func TestHMACSignAndVerify(t *testing.T) {
	box := newTestBox(t)

	sig := box.SignHMAC("session-id")
	if !box.VerifyHMAC("session-id", sig) {
		t.Error("valid signature rejected")
	}
	if box.VerifyHMAC("other-id", sig) {
		t.Error("signature accepted for a different message")
	}
	if box.VerifyHMAC("session-id", "deadbeef") {
		t.Error("forged signature accepted")
	}
}

func TestRandomTokenIsUniqueAndURLSafe(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		token, err := RandomToken(32)
		if err != nil {
			t.Fatalf("RandomToken: %v", err)
		}
		if seen[token] {
			t.Fatal("RandomToken produced a duplicate")
		}
		seen[token] = true
		if strings.ContainsAny(token, "+/=") {
			t.Errorf("token is not URL-safe: %q", token)
		}
	}
}

func TestHashTokenIsStable(t *testing.T) {
	if HashToken("abc") != HashToken("abc") {
		t.Error("HashToken is not deterministic")
	}
	if HashToken("abc") == HashToken("abd") {
		t.Error("HashToken collided on different inputs")
	}
	if len(HashToken("abc")) != 64 {
		t.Error("expected a 64-character hex digest")
	}
}

func flipLast(s string) string {
	last := s[len(s)-1]
	if last == 'A' {
		return "B"
	}
	return "A"
}
