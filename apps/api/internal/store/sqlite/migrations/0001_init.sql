-- Initial schema.
--
-- Deliberately narrow: SangamDrive stores identity, credentials and preferences.
-- There is no files table, no folders table and no search index — Drive owns all
-- of that and is queried live.

CREATE TABLE users (
    id         TEXT PRIMARY KEY,
    email      TEXT NOT NULL,
    name       TEXT NOT NULL DEFAULT '',
    avatar_url TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX idx_users_email ON users (email);

CREATE TABLE accounts (
    id                TEXT PRIMARY KEY,
    user_id           TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    google_user_id    TEXT NOT NULL,
    email             TEXT NOT NULL,
    name              TEXT NOT NULL DEFAULT '',
    avatar_url        TEXT NOT NULL DEFAULT '',
    scope             TEXT NOT NULL CHECK (scope IN ('drive.file', 'drive')),
    status            TEXT NOT NULL CHECK (status IN ('connected', 'reauth_required', 'disconnected')),
    refresh_token_enc TEXT NOT NULL,
    sort_order        INTEGER NOT NULL DEFAULT 0,
    connected_at      TEXT NOT NULL,
    last_used_at      TEXT,
    updated_at        TEXT NOT NULL
);

-- One SangamDrive user may link a given Google account only once.
CREATE UNIQUE INDEX idx_accounts_user_google ON accounts (user_id, google_user_id);
CREATE INDEX idx_accounts_user ON accounts (user_id, sort_order);

CREATE TABLE sessions (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL,
    user_agent   TEXT NOT NULL DEFAULT '',
    ip_address   TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    expires_at   TEXT NOT NULL
);

CREATE UNIQUE INDEX idx_sessions_token_hash ON sessions (token_hash);
CREATE INDEX idx_sessions_user ON sessions (user_id);
CREATE INDEX idx_sessions_expires ON sessions (expires_at);

CREATE TABLE preferences (
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, key)
);
