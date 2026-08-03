# Architecture

## The one rule

**Google Drive is the database.**

Every other decision follows from that. SangamDrive holds no file metadata, no
thumbnails, no search index, and no copy of any file. When the browser asks for a
folder listing, the API asks Google, transforms the response, and forwards it. When
the user uploads, the bytes flow through the API as a stream and are never written
to disk.

This is what keeps the project honest: there is nothing to sync, nothing to
invalidate, and nothing of consequence to leak.

## Components

```
┌─────────────────────────────┐
│  Browser                    │
│  Next.js 15 · React 19      │
│  TanStack Query = the cache │
└──────────────┬──────────────┘
               │ HTTPS, cookie auth, CSRF header
┌──────────────▼──────────────┐
│  Go API (Fiber)             │
│  ├─ OAuth + session         │
│  ├─ token decryption        │
│  ├─ per-account fan-out     │
│  └─ retry / backoff         │
└──────┬───────────────┬──────┘
       │               │
┌──────▼──────┐  ┌─────▼───────────────────┐
│  SQLite     │  │  Google Drive API v3    │
│  users      │  │  files, about, changes  │
│  accounts   │  │  upload, download       │
│  sessions   │  └─────────────────────────┘
│  prefs      │
└─────────────┘
```

### apps/web

Next.js App Router. All Drive data is client-fetched through TanStack Query — no
server components hold Drive state, because a server-rendered listing would mean
the server briefly holds user file metadata.

Routes: `/` signs in, `/dashboard` shows storage across every Drive, `/files` is
the unified browser. Both signed-in pages share `AppShell`, which gates on the
session in the browser — the session cookie is scoped to the API origin, so a
server component cannot see it.

The browser keeps its listing state in the URL (`account`, `parent`, `scope`,
`sort`, `direction`, `view`) so back, forward and refresh behave, and a folder is
a link. `parent` always travels with `account`: a folder id only means something
inside its own Drive.

### apps/api

Fiber. Deliberately thin. A handler's job is:

1. Authenticate the session cookie.
2. Resolve which connected accounts the request targets.
3. Decrypt each account's refresh token and mint an access token.
4. Call Google, concurrently, with a bounded worker pool.
5. Merge results, forward errors as structured per-account entries.

No business logic is reimplemented from Google. There is no "SangamDrive folder
model" — a folder is whatever Drive says it is.

### packages/shared

TypeScript types mirroring the Go DTOs. The web app imports these so a field
rename in the API surfaces as a type error rather than a runtime `undefined`.

## Data model

Four tables. That is the whole schema.

| Table         | Why it must exist                                                     |
| ------------- | --------------------------------------------------------------------- |
| `users`       | A SangamDrive login, so several Google accounts can share one session. |
| `accounts`    | The sealed refresh token per linked Google account.                    |
| `sessions`    | Revocable browser sessions.                                            |
| `preferences` | Theme, view mode, sort order.                                          |

Everything else is a live Drive call.

### Persistence is behind interfaces

`internal/store` declares `UserStore`, `AccountStore`, `SessionStore` and
`PreferenceStore`. `internal/store/sqlite` is the only implementation today.
Handlers never import the sqlite package, so adding Postgres means writing one
package and changing one line in `main.go`.

SQLite runs with `WAL`, `foreign_keys=ON`, and a **single pooled connection**.
SQLite serialises writers regardless; capping the pool at one removes an entire
class of `SQLITE_BUSY` races at a throughput cost that is irrelevant for a
self-hosted dashboard.

## Authentication

1. The user starts Google OAuth. The API stores a signed, single-use `state`.
2. Google redirects back with a code. The API exchanges it for tokens.
3. The refresh token is sealed with AES-256-GCM and stored.
4. A session token (32 random bytes) is set as an `HttpOnly` cookie; only its
   SHA-256 hash is persisted, so a database leak yields no usable sessions.
5. A separate non-`HttpOnly` CSRF cookie is set. The client echoes it in the
   `X-CSRF-Token` header on every state-changing request — classic double-submit.

Connecting an additional Google account reuses the existing session; the new
account is linked to the same `users` row rather than creating a second login.

### Scopes

An account is connected with either `drive.file` or `drive`. The choice is stored
per account, so one user can have a locked-down work Drive and a full-access
personal Drive side by side. Upgrading re-runs consent and replaces the token.

## Error model

`internal/apperr` defines one error type with a stable machine `Code`. Every
Google failure is mapped onto one of these before it reaches the client:

| Google condition                  | Code                    | UI response                     |
| --------------------------------- | ----------------------- | ------------------------------- |
| `invalid_grant` on refresh        | `reauth_required`       | "Reconnect this account" button |
| 403 `insufficientPermissions`     | `insufficient_scope`    | "Upgrade permissions" prompt    |
| 403 `storageQuotaExceeded`        | `quota_exceeded`        | Suggest another Drive           |
| 429 / 403 `rateLimitExceeded`     | `rate_limited`          | Retry with backoff, then toast  |
| 5xx or timeout                    | `upstream_unavailable`  | Retry, then "Google is having trouble" |

The UI never string-matches an error message.

## Fan-out and partial failure

A unified listing queries N accounts concurrently through a semaphore bounded by
`DRIVE_CONCURRENCY`. If three accounts succeed and one needs reconnecting, the
response is **200 with partial results**:

```json
{
  "data": [ /* files from the three healthy accounts */ ],
  "meta": {
    "count": 143,
    "errors": [
      { "code": "reauth_required", "message": "…", "account_id": "acc_7…" }
    ]
  }
}
```

Failing the whole page because one Drive is unhappy would make the product
unusable for exactly the people it is built for.

## Streaming

Uploads use Drive's resumable upload protocol. The browser sends bytes to the API,
which pipes them straight into the Google request body — `StreamRequestBody` is on
and there is no temp file anywhere in the path. Downloads work the same way in
reverse, with `Content-Disposition` and range headers passed through.

Consequence for operators: reverse proxies must have request buffering **off** and
body size limits removed. Both example configs do this.

## Rate limits and retries

Google's Drive API enforces per-user and per-project quotas. The client retries
429 and 5xx with exponential backoff plus jitter, honouring `Retry-After` when
present. Non-retryable codes fail immediately — retrying an `invalid_grant` only
wastes the user's time.

## What is deliberately absent

- No file metadata cache. Stale listings are worse than slightly slower ones.
- No background sync workers. Nothing to sync.
- No server-side search index. Search fans out to Drive live.
- No message queue, no Redis. A self-hosted dashboard should be two containers.
