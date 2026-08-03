package files

import (
	"strings"
	"testing"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
)

func TestCursorRoundTrips(t *testing.T) {
	original := cursor{"acc-a": "token-a", "acc-b": "token-b"}

	decoded, err := decodeCursor(encodeCursor(original))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(decoded) != len(original) {
		t.Fatalf("got %d entries, want %d", len(decoded), len(original))
	}
	for id, token := range original {
		if decoded[id] != token {
			t.Errorf("%s: got %q want %q", id, decoded[id], token)
		}
	}
}

func TestEmptyCursorEncodesToNothing(t *testing.T) {
	if got := encodeCursor(cursor{}); got != "" {
		t.Errorf("got %q, want empty", got)
	}

	decoded, err := decodeCursor("")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded) != 0 {
		t.Errorf("got %d entries, want 0", len(decoded))
	}
}

func TestDecodeCursorRejectsGarbage(t *testing.T) {
	for _, raw := range []string{"not-base64!!", "bm90LWpzb24"} {
		_, err := decodeCursor(raw)
		if apperr.From(err).Code != apperr.CodeBadRequest {
			t.Errorf("%q: unexpected error %v", raw, err)
		}
	}
}

func TestDecodeCursorRejectsAnOversizedFanOut(t *testing.T) {
	oversized := cursor{}
	for i := 0; i <= maxAccountsPerListing; i++ {
		oversized[strings.Repeat("a", i+1)] = "token"
	}

	if _, err := decodeCursor(encodeCursor(oversized)); apperr.From(err).Code != apperr.CodeBadRequest {
		t.Fatalf("unexpected error: %v", err)
	}
}
