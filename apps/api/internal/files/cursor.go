package files

import (
	"encoding/base64"
	"encoding/json"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/apperr"
)

// cursor maps an account id to its Drive page token.
//
// Google paginates per account, so a unified listing needs one token per source.
// Encoding them together keeps the API surface a single opaque `page` parameter,
// and an account missing from a non-empty cursor has no more pages.
type cursor map[string]string

// encodeCursor renders a cursor as an opaque string. An empty cursor encodes to
// "" so the client can treat "no more pages" as an absent field.
func encodeCursor(c cursor) string {
	if len(c) == 0 {
		return ""
	}
	payload, err := json.Marshal(c)
	if err != nil {
		// a map[string]string cannot fail to marshal
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

// decodeCursor parses a client-supplied cursor.
//
// The cursor is not signed: it carries no authority. Account ids inside it are
// re-checked against the caller's own accounts before use, so a forged cursor
// reaches nothing that a plain request could not.
func decodeCursor(raw string) (cursor, error) {
	if raw == "" {
		return cursor{}, nil
	}

	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, apperr.BadRequest("That page reference is not valid. Reload the folder.")
	}

	var decoded cursor
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, apperr.BadRequest("That page reference is not valid. Reload the folder.")
	}
	if len(decoded) > maxAccountsPerListing {
		return nil, apperr.BadRequest("That page reference is not valid. Reload the folder.")
	}
	return decoded, nil
}
