// Package sqlite implements store.Store on top of SQLite.
//
// It uses modernc.org/sqlite, a pure-Go driver, so the binary needs no cgo and
// the container image stays small and cross-compilable.
//
// The pool is capped at a single connection. SQLite serialises writers anyway,
// and for a self-hosted dashboard the throughput cost is irrelevant next to
// eliminating an entire class of SQLITE_BUSY races.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sangamdrive/sangamdrive/apps/api/internal/store"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// timeFormat keeps timestamps sortable as text and unambiguous about zone.
const timeFormat = time.RFC3339Nano

// Store is the SQLite-backed persistence layer.
type Store struct {
	db *sql.DB
}

var _ store.Store = (*Store)(nil)

// Open connects to the SQLite database at path, creating parent directories as
// needed. Tests should pass a file inside t.TempDir() rather than ":memory:",
// which SQLite scopes per-connection and would not survive the pool.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("sqlite: path is required")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("sqlite: create data directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	// single writer — see package doc
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	s := &Store{db: db}
	if err := s.Ping(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// dsn builds the connection string with the pragmas we depend on.
func dsn(path string) string {
	pragmas := []string{
		"_pragma=busy_timeout(5000)",
		"_pragma=journal_mode(WAL)",
		"_pragma=foreign_keys(1)",
		"_pragma=synchronous(NORMAL)",
	}
	return "file:" + url.PathEscape(path) + "?" + strings.Join(pragmas, "&")
}

func (s *Store) Ping(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("sqlite: ping: %w", err)
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

// Migrate applies every embedded migration that has not run yet, in filename
// order, each inside its own transaction.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name       TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("sqlite: create schema_migrations: %w", err)
	}

	names, err := migrationNames()
	if err != nil {
		return err
	}

	for _, name := range names {
		var exists int
		err := s.db.QueryRowContext(ctx,
			`SELECT 1 FROM schema_migrations WHERE name = ?`, name).Scan(&exists)
		if err == nil {
			continue // already applied
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("sqlite: check migration %s: %w", name, err)
		}

		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("sqlite: read migration %s: %w", name, err)
		}
		if err := s.applyMigration(ctx, name, string(body)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, name, body string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin migration %s: %w", name, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, body); err != nil {
		return fmt.Errorf("sqlite: apply migration %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`,
		name, formatTime(time.Now().UTC()),
	); err != nil {
		return fmt.Errorf("sqlite: record migration %s: %w", name, err)
	}
	return tx.Commit()
}

func migrationNames() ([]string, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("sqlite: read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// --- users -----------------------------------------------------------------

func (s *Store) CreateUser(ctx context.Context, u *store.User) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (id, email, name, avatar_url, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		u.ID, u.Email, u.Name, u.AvatarURL, formatTime(u.CreatedAt), formatTime(u.UpdatedAt),
	)
	return wrapWrite("create user", err)
}

const userColumns = `id, email, name, avatar_url, created_at, updated_at`

func (s *Store) GetUserByID(ctx context.Context, id string) (*store.User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = ?`, id)
	return scanUser(row)
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*store.User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE email = ?`, email)
	return scanUser(row)
}

func (s *Store) UpdateUserProfile(ctx context.Context, id, name, avatarURL string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE users SET name = ?, avatar_url = ?, updated_at = ? WHERE id = ?`,
		name, avatarURL, formatTime(time.Now().UTC()), id,
	)
	return affectedOne("update user", res, err)
}

func (s *Store) DeleteUser(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	return affectedOne("delete user", res, err)
}

func scanUser(row *sql.Row) (*store.User, error) {
	var (
		u                    store.User
		createdAt, updatedAt string
	)
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &createdAt, &updatedAt)
	if err != nil {
		return nil, wrapRead("get user", err)
	}
	if u.CreatedAt, err = parseTime(createdAt); err != nil {
		return nil, err
	}
	if u.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return nil, err
	}
	return &u, nil
}

// --- accounts --------------------------------------------------------------

const accountColumns = `id, user_id, google_user_id, email, name, avatar_url, scope,
	status, refresh_token_enc, sort_order, connected_at, last_used_at, updated_at`

func (s *Store) CreateAccount(ctx context.Context, a *store.Account) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO accounts (`+accountColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.UserID, a.GoogleUserID, a.Email, a.Name, a.AvatarURL, string(a.Scope),
		string(a.Status), a.RefreshTokenEnc, a.SortOrder,
		formatTime(a.ConnectedAt), formatTimePtr(a.LastUsedAt), formatTime(a.UpdatedAt),
	)
	return wrapWrite("create account", err)
}

func (s *Store) GetAccount(ctx context.Context, userID, accountID string) (*store.Account, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+accountColumns+` FROM accounts WHERE user_id = ? AND id = ?`,
		userID, accountID)
	return scanAccountRow(row)
}

func (s *Store) GetAccountByGoogleID(ctx context.Context, userID, googleUserID string) (*store.Account, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+accountColumns+` FROM accounts WHERE user_id = ? AND google_user_id = ?`,
		userID, googleUserID)
	return scanAccountRow(row)
}

func (s *Store) ListAccounts(ctx context.Context, userID string) ([]*store.Account, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+accountColumns+` FROM accounts WHERE user_id = ?
		 ORDER BY sort_order ASC, connected_at ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list accounts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var accounts []*store.Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list accounts: %w", err)
	}
	return accounts, nil
}

func (s *Store) UpdateAccount(ctx context.Context, a *store.Account) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE accounts SET
			email = ?, name = ?, avatar_url = ?, scope = ?, status = ?,
			refresh_token_enc = ?, sort_order = ?, last_used_at = ?, updated_at = ?
		WHERE id = ? AND user_id = ?`,
		a.Email, a.Name, a.AvatarURL, string(a.Scope), string(a.Status),
		a.RefreshTokenEnc, a.SortOrder, formatTimePtr(a.LastUsedAt),
		formatTime(time.Now().UTC()), a.ID, a.UserID,
	)
	return affectedOne("update account", res, err)
}

func (s *Store) SetAccountStatus(ctx context.Context, accountID string, status store.AccountStatus) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE accounts SET status = ?, updated_at = ? WHERE id = ?`,
		string(status), formatTime(time.Now().UTC()), accountID)
	return affectedOne("set account status", res, err)
}

func (s *Store) TouchAccount(ctx context.Context, accountID string, at time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE accounts SET last_used_at = ? WHERE id = ?`,
		formatTime(at.UTC()), accountID)
	return affectedOne("touch account", res, err)
}

func (s *Store) DeleteAccount(ctx context.Context, userID, accountID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM accounts WHERE user_id = ? AND id = ?`, userID, accountID)
	return affectedOne("delete account", res, err)
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

func scanAccountRow(row *sql.Row) (*store.Account, error) { return scanAccount(row) }

func scanAccount(sc scanner) (*store.Account, error) {
	var (
		a                      store.Account
		scope, status          string
		connectedAt, updatedAt string
		lastUsedAt             sql.NullString
	)
	err := sc.Scan(&a.ID, &a.UserID, &a.GoogleUserID, &a.Email, &a.Name, &a.AvatarURL,
		&scope, &status, &a.RefreshTokenEnc, &a.SortOrder,
		&connectedAt, &lastUsedAt, &updatedAt)
	if err != nil {
		return nil, wrapRead("get account", err)
	}

	a.Scope = store.Scope(scope)
	a.Status = store.AccountStatus(status)
	if a.ConnectedAt, err = parseTime(connectedAt); err != nil {
		return nil, err
	}
	if a.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return nil, err
	}
	if lastUsedAt.Valid {
		t, err := parseTime(lastUsedAt.String)
		if err != nil {
			return nil, err
		}
		a.LastUsedAt = &t
	}
	return &a, nil
}

// --- sessions --------------------------------------------------------------

const sessionColumns = `id, user_id, token_hash, user_agent, ip_address,
	created_at, last_seen_at, expires_at`

func (s *Store) CreateSession(ctx context.Context, sess *store.Session) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (`+sessionColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.UserID, sess.TokenHash, sess.UserAgent, sess.IPAddress,
		formatTime(sess.CreatedAt), formatTime(sess.LastSeenAt), formatTime(sess.ExpiresAt),
	)
	return wrapWrite("create session", err)
}

func (s *Store) GetSessionByTokenHash(ctx context.Context, tokenHash string) (*store.Session, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+sessionColumns+` FROM sessions WHERE token_hash = ?`, tokenHash)

	var (
		sess                             store.Session
		createdAt, lastSeenAt, expiresAt string
	)
	err := row.Scan(&sess.ID, &sess.UserID, &sess.TokenHash, &sess.UserAgent,
		&sess.IPAddress, &createdAt, &lastSeenAt, &expiresAt)
	if err != nil {
		return nil, wrapRead("get session", err)
	}
	if sess.CreatedAt, err = parseTime(createdAt); err != nil {
		return nil, err
	}
	if sess.LastSeenAt, err = parseTime(lastSeenAt); err != nil {
		return nil, err
	}
	if sess.ExpiresAt, err = parseTime(expiresAt); err != nil {
		return nil, err
	}
	return &sess, nil
}

func (s *Store) TouchSession(ctx context.Context, id string, at time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET last_seen_at = ? WHERE id = ?`, formatTime(at.UTC()), id)
	return affectedOne("touch session", res, err)
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return affectedOne("delete session", res, err)
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return wrapWrite("delete user sessions", err)
}

func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at <= ?`, formatTime(now.UTC()))
	if err != nil {
		return 0, fmt.Errorf("sqlite: delete expired sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite: delete expired sessions: %w", err)
	}
	return n, nil
}

// --- preferences -----------------------------------------------------------

func (s *Store) GetPreferences(ctx context.Context, userID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT key, value FROM preferences WHERE user_id = ?`, userID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get preferences: %w", err)
	}
	defer func() { _ = rows.Close() }()

	prefs := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("sqlite: get preferences: %w", err)
		}
		prefs[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: get preferences: %w", err)
	}
	return prefs, nil
}

func (s *Store) SetPreference(ctx context.Context, userID, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO preferences (user_id, key, value, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (user_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		userID, key, value, formatTime(time.Now().UTC()),
	)
	return wrapWrite("set preference", err)
}

func (s *Store) DeletePreference(ctx context.Context, userID, key string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM preferences WHERE user_id = ? AND key = ?`, userID, key)
	return wrapWrite("delete preference", err)
}

// --- helpers ---------------------------------------------------------------

func formatTime(t time.Time) string { return t.UTC().Format(timeFormat) }

func formatTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

func parseTime(raw string) (time.Time, error) {
	t, err := time.Parse(timeFormat, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("sqlite: parse timestamp %q: %w", raw, err)
	}
	return t.UTC(), nil
}

// wrapRead maps sql.ErrNoRows onto the store-level sentinel.
func wrapRead(op string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	return fmt.Errorf("sqlite: %s: %w", op, err)
}

// wrapWrite maps constraint violations onto the store-level sentinel.
func wrapWrite(op string, err error) error {
	if err == nil {
		return nil
	}
	if isUniqueViolation(err) {
		return store.ErrConflict
	}
	return fmt.Errorf("sqlite: %s: %w", op, err)
}

func affectedOne(op string, res sql.Result, err error) error {
	if err != nil {
		return wrapWrite(op, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: %s: %w", op, err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// isUniqueViolation matches on message text because modernc.org/sqlite does not
// export typed constraint errors.
func isUniqueViolation(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint failed") ||
		strings.Contains(msg, "constraint failed: unique")
}
