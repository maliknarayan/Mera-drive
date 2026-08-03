# API reference

Base path: `/api/v1`. All responses are JSON unless the endpoint streams file
bytes.

> Phases 1–3 are implemented: probes, `/meta`, authentication, and account
> management. Remaining endpoints are listed under [Planned](#planned) with the
> shape they will take, so the client contract is stable to build against.

## Conventions

### Envelope

Success:

```json
{
  "data": { },
  "meta": { "count": 42, "next_page_token": "..." },
  "request_id": "0f9b1c2e-..."
}
```

Failure:

```json
{
  "error": {
    "code": "reauth_required",
    "message": "This Google account needs to be reconnected.",
    "account_id": "acc_7f3a"
  },
  "request_id": "0f9b1c2e-..."
}
```

`request_id` is also returned in the `X-Request-ID` header. Quote it in bug
reports — it is the key to the server-side log line.

### Authentication

A session cookie, sent automatically. Requests without a valid session get
`401 unauthorized`.

### CSRF

Every `POST`, `PATCH` and `DELETE` must send the value of the `sangam_csrf` cookie
in the `X-CSRF-Token` header. Mismatches get `403 csrf_invalid`.

### Error codes

| Code                   | HTTP | Meaning                                              |
| ---------------------- | ---- | ---------------------------------------------------- |
| `bad_request`          | 400  | Malformed request                                    |
| `validation_failed`    | 422  | Well-formed but semantically invalid                 |
| `unauthorized`         | 401  | No valid session                                     |
| `csrf_invalid`         | 403  | Missing or mismatched CSRF token                     |
| `forbidden`            | 403  | Authenticated but not allowed                        |
| `not_found`            | 404  | No such resource                                     |
| `conflict`             | 409  | Already exists, e.g. account already linked          |
| `payload_too_large`    | 413  | Request body over the limit                          |
| `rate_limited`         | 429  | SangamDrive or Google throttled the request          |
| `internal_error`       | 500  | Unexpected server fault                              |
| `reauth_required`      | 401  | Refresh token rejected — reconnect that account      |
| `insufficient_scope`   | 403  | Operation needs `drive`, account has `drive.file`    |
| `quota_exceeded`       | 507  | Target Drive is full                                 |
| `upstream_unavailable` | 502  | Google unreachable, or the call timed out            |

### Partial failure

Fan-out endpoints return `200` when at least one account succeeded. Failed
accounts appear in `meta.errors`, each tagged with `account_id`. Treat an empty
`meta.errors` as "all accounts responded".

### Rate limiting

`RATE_LIMIT_MAX` requests per `RATE_LIMIT_WINDOW`, keyed by session when
authenticated and by IP otherwise.

---

## Implemented

### `GET /healthz`

Liveness. Never touches the database, so a database fault does not cause an
orchestrator to restart a healthy process. Outside `/api/v1`, so it is not rate
limited.

```json
{ "data": { "status": "ok" } }
```

### `GET /readyz`

Readiness. Pings the store. Returns `502 upstream_unavailable` when the database
is unreachable.

### `GET /api/v1/meta`

Build and environment information.

```json
{
  "data": {
    "name": "SangamDrive",
    "environment": "production",
    "build": { "version": "1.0.0", "commit": "a1b2c3d", "built": "2026-01-01T00:00:00Z" }
  }
}
```

---

## Authentication endpoints

### `GET /api/v1/auth/google/start`

Begins the OAuth flow. This is a **browser navigation**, not an API call — it sets
an `HttpOnly` state cookie and responds `302` to Google's consent screen.

| Parameter    | Required            | Values                                     |
| ------------ | ------------------- | ------------------------------------------ |
| `intent`     | no (default `login`) | `login`, `link`, `reconnect`, `upgrade`   |
| `scope`      | no (default `drive.file`) | `drive.file`, `drive`                |
| `account_id` | `reconnect`, `upgrade` | Connected account to act on             |
| `next`       | no                  | Site-relative path to return to            |

| Intent      | Session | Notes                                                     |
| ----------- | ------- | --------------------------------------------------------- |
| `login`     | not required | Finds or creates the user, links the account         |
| `link`      | required | Adds another Google account to the signed-in user       |
| `reconnect` | required | Replaces a rejected refresh token                       |
| `upgrade`   | required | `drive.file` → `drive`; `scope` must be `drive`          |

Errors are returned as JSON (`400`, `401`, `404`) because the user has not left
the app yet.

`next` is validated server-side: anything that is not a site-relative path — an
absolute URL, `//host`, a value containing CR/LF — is replaced with `/`.

### `GET /api/v1/auth/google/callback`

Google's redirect target. Always responds `302` back to `APP_BASE_URL`, because
the browser is mid-navigation and a JSON body would be a dead end.

On success the redirect carries:

```
?auth=login&account=you@example.com
```

On failure it carries a stable code plus a message safe to display:

```
?auth_error=insufficient_scope&auth_message=The+requested+Drive+permission+was+not+granted.
```

Three checks must pass before anything is written: the state HMAC (this instance
minted it), the state cookie nonce (this browser started it), and the 15-minute
age limit (it is not a replayed link). The state cookie is cleared on arrival, so
a given consent link works exactly once.

The flow also refuses to proceed when Google returns no refresh token, when the
email is unverified, or when the requested Drive scope was not actually granted.

### `GET /api/v1/auth/session`

Requires a session. Returns the signed-in user.

```json
{
  "data": {
    "user": {
      "id": "9f1c…",
      "email": "you@example.com",
      "name": "You",
      "avatar_url": "https://lh3.googleusercontent.com/…"
    },
    "expires_at": "2026-02-27T10:12:00Z"
  }
}
```

A `401` here is the normal answer for "signed out" — the web client treats it as
a state, not an error.

### `POST /api/v1/auth/logout`

Requires a session and a CSRF token. Revokes this session server-side and clears
both cookies. Responds `204`.

### `POST /api/v1/auth/logout-all`

As above, but revokes every session belonging to the user.

### Cookies

| Cookie           | Flags                            | Contents                          |
| ---------------- | -------------------------------- | --------------------------------- |
| `sangam_session` | `HttpOnly`, `SameSite=Lax`, `Secure`\* | Opaque token; only its SHA-256 hash is stored |
| `sangam_csrf`    | `SameSite=Lax`, `Secure`\*       | `HMAC(session hash)` — readable by JS by design |
| `sangam_oauth`   | `HttpOnly`, `SameSite=Lax`, `Secure`\*, 15 min | OAuth state nonce |

\* `Secure` when `COOKIE_SECURE=true`, which is mandatory in production.

`SameSite=Lax` rather than `Strict`: the OAuth callback is a top-level cross-site
navigation, and `Strict` would withhold the session cookie on arrival.

---

## Account endpoints

All require a session. Writes also require the CSRF header.

### `GET /api/v1/accounts`

Every connected account with **live** storage figures, fetched concurrently from
Google on each request. Nothing here is cached server-side.

```json
{
  "data": [
    {
      "id": "acc_7f3a",
      "email": "you@example.com",
      "name": "You",
      "avatar_url": "https://lh3.googleusercontent.com/…",
      "scope": "drive.file",
      "status": "connected",
      "connected_at": "2026-01-04T09:22:11Z",
      "last_used_at": null,
      "quota": {
        "limit": 16106127360,
        "usage": 5368709120,
        "usage_in_drive": 5000000000,
        "usage_in_trash": 368709120
      }
    }
  ],
  "meta": { "count": 1, "errors": [] }
}
```

`quota.limit` is `null` for accounts with unlimited storage. `quota` is **absent**
when the account is unusable or its live call failed — check `meta.errors`.

Refresh tokens, Google user IDs and access tokens never appear in this payload.

**Partial failure.** If one Drive fails, the response is still `200`:

```json
{
  "data": [ /* … all accounts, the failed one without `quota` … */ ],
  "meta": {
    "count": 4,
    "errors": [
      {
        "code": "reauth_required",
        "message": "Google rejected the stored credentials. Please reconnect this account.",
        "account_id": "acc_9b21"
      }
    ]
  }
}
```

A `reauth_required` failure also **persists** `status: "reauth_required"` on that
account, so the dashboard is correct on reload without a second probe. A transient
`upstream_unavailable` does not change status — a Google blip is not a credentials
problem.

Concurrency is bounded by `DRIVE_CONCURRENCY`; each call is bounded by
`DRIVE_TIMEOUT`. Transient failures (`429`, `5xx`) are retried up to 4 times with
exponential backoff and jitter, honouring `Retry-After`.

### `GET /api/v1/storage`

The aggregate summary.

```json
{
  "data": {
    "total_limit": 48318382080,
    "total_usage": 12884901888,
    "total_free": 35433480192,
    "account_count": 3,
    "connected_count": 3,
    "unlimited_count": 0
  },
  "meta": { "count": 3, "errors": [] }
}
```

Accounts reporting unlimited storage are counted in `unlimited_count` and excluded
from `total_limit` — folding them in would badly understate free space.

> This endpoint performs the **same fan-out** as `GET /accounts`. The web app does
> not call it; it derives the summary from the accounts payload it already has,
> rather than doubling Google API calls to render one card. The endpoint exists for
> scripts and other API consumers.

### `DELETE /api/v1/accounts/{id}`

Disconnects an account: revokes the grant at Google, drops the cached access
token, and deletes the row. Responds `204`.

Revocation is **best effort**. If Google is unreachable the local row is still
deleted — an outage must not trap credentials on your server. The failure is
logged, not returned.

`404 not_found` if the account does not belong to the caller. Account IDs are
always scoped by `user_id`; an ID alone grants nothing.

### `PATCH /api/v1/accounts/order`

Sets the display order of the account cards.

```json
{ "account_ids": ["acc_9b21", "acc_7f3a", "acc_1c44"] }
```

Must list **all** of the caller's accounts exactly once — a partial list would
leave cards with duplicate positions. `422 validation_failed` otherwise. Responds
`204`.

Not applied in a transaction: display order is cosmetic, and a half-applied order
is corrected by the next move.

### Account limit

A user may connect up to **50** Google accounts. "Unlimited" is the product
promise, but an unbounded fan-out means one Google call per account per request;
this is a sanity ceiling, not a product limit. Exceeding it returns `409 conflict`
at connection time.

---

## File endpoints

All require a session. Writes also require the CSRF header.

Every listing is fetched **live** from Google on each request. No file metadata is
cached or persisted anywhere, so there is nothing to invalidate and nothing to go
stale.

### `GET /api/v1/files`

One page of files, merged across the caller's connected Drives.

| Query       | Default    | Meaning                                                     |
| ----------- | ---------- | ----------------------------------------------------------- |
| `account_id` | all        | Restrict to one Drive                                       |
| `parent`    | each root  | Folder to open. **Requires `account_id`**                   |
| `scope`     | `children` | `children`, `starred`, `recent` or `trash`                   |
| `sort`      | `name`     | `name`, `modified_at`, `size` or `account_email`             |
| `direction` | `asc`      | `asc` or `desc`                                              |
| `page_size` | `100`      | 1–500                                                        |
| `page`      | —          | Opaque cursor from a previous `meta.next_page_token`         |

```json
{
  "data": [
    {
      "id": "1AbCdEf",
      "name": "Q1 report.pdf",
      "mime_type": "application/pdf",
      "kind": "pdf",
      "size": 248113,
      "modified_at": "2026-02-11T14:03:52Z",
      "created_at": "2026-02-10T08:11:00Z",
      "starred": false,
      "trashed": false,
      "shared": true,
      "parents": ["0BxYz"],
      "web_view_link": "https://drive.google.com/file/d/1AbCdEf/view",
      "icon_link": "https://drive-thirdparty.googleusercontent.com/…",
      "thumbnail_link": "https://lh3.googleusercontent.com/…",
      "owner": { "display_name": "You", "email": "you@example.com", "photo_url": "…" },
      "capabilities": { "can_edit": true, "can_rename": true, "can_delete": true,
                        "can_trash": true, "can_share": true, "can_copy": true,
                        "can_add_children": false },
      "account_id": "acc_7f3a",
      "account_email": "you@example.com"
    }
  ],
  "meta": {
    "count": 1,
    "next_page_token": "eyJhY2NfN2YzYSI6IuKApiJ9",
    "path": [{ "id": "0BxYz", "name": "Reports" }],
    "errors": []
  }
}
```

`account_id` and `account_email` are on every file: without them the client cannot
tell which Drive to act against.

`capabilities` is forwarded from Google so the UI can disable actions Google would
reject, rather than guessing from the account's scope.

**Opening a folder is single-account.** A folder id only means something inside its
own Drive, so `parent` without `account_id` is `400 bad_request`. `meta.path` is the
breadcrumb trail, root-first, and is only present for a folder listing — a merged
listing spans several roots.

**Ordering is per page, not global.** Each Drive is asked for its own slice in the
same order and the slices are merged. Google paginates per account and offers no
cross-account cursor, so a globally sorted stream is not purchasable at any
reasonable number of calls. Folders always lead, in both directions.

**Pagination.** `meta.next_page_token` bundles one Google page token per account
that still has more. Send it back as `page`; accounts absent from it are finished
and are not called again. The cursor is **not signed** because it carries no
authority: the account ids inside it are intersected with the caller's own accounts
before use, so a forged cursor reaches nothing a plain request could not.

`page_size` is split across the fan-out so a merged page stays near what was asked
for, with a floor of 10 per Drive — a very wide fan-out can therefore overshoot.

**Partial failure.** If one Drive fails, the response is still `200` with the files
the others returned; the failure is in `meta.errors`, tagged with its `account_id`.
A `reauth_required` failure also persists that status on the account.

### `POST /api/v1/files/folder`

```json
{ "account_id": "acc_7f3a", "name": "Invoices", "parent_id": "0BxYz" }
```

Omit `parent_id` to create in that Drive's root. Responds `201` with the new folder
in the same shape as a listing entry.

Names are trimmed, must be non-empty, at most 255 characters, and may not contain a
slash. Drive itself allows longer names, but one that long breaks as a filename on
download.

Not retried on a transient failure: a retried create would leave two folders behind.

### `PATCH /api/v1/files/{account}/{id}`

A partial update — every field is optional, and an absent field is left untouched.

```json
{ "name": "Q1 report final.pdf", "starred": true, "trashed": false, "parent_id": "0BnEw" }
```

`parent_id` moves the file. Drive has no move operation, so this becomes
add-parent plus remove-every-current-parent, which costs one extra metadata read.
An empty body is `422 validation_failed`.

Responds `200` with the updated file.

### `DELETE /api/v1/files/{account}/{id}`

Trashes the file. Responds `204`.

`?permanent=true` erases it instead, bypassing the trash. Trashing is the default
because it is recoverable from Drive's own UI; permanent deletion is not, and
Google offers no undo.

### Ownership

`{account}` is always resolved as `(user_id, account_id)`. An account id belonging
to someone else is `404 not_found` — an id alone grants nothing.

---

## Planned

| Phase | Endpoint                                    | Purpose                                    |
| ----- | ------------------------------------------- | ------------------------------------------ |
| 5     | `POST /upload`                              | Streaming resumable upload                 |
| 6     | `GET /files/{account}/{id}/content`         | Streaming download                         |
| 7     | `GET /search`                               | Concurrent search across every account     |
| 8     | `GET /files/{account}/{id}/preview`         | Thumbnail or embed URL                     |
| 9     | `GET /files/{account}/{id}/permissions`     | List permissions                           |
| 9     | `POST /files/{account}/{id}/permissions`    | Share                                      |
