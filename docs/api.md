# API reference

Base path: `/api/v1`. All responses are JSON unless the endpoint streams file
bytes.

> Phase 1 implements the probes and `/meta` only. Remaining endpoints are listed
> under [Planned](#planned) with the shape they will take, so the client contract
> is stable to build against.

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

## Planned

| Phase | Endpoint                                    | Purpose                                    |
| ----- | ------------------------------------------- | ------------------------------------------ |
| 2     | `GET /auth/google/start`                    | Begin OAuth; `?scope=drive.file\|drive`    |
| 2     | `GET /auth/google/callback`                 | OAuth redirect target                      |
| 2     | `GET /auth/session`                         | Current user, or 401                       |
| 2     | `POST /auth/logout`                         | Revoke the current session                 |
| 3     | `GET /accounts`                             | Connected accounts with live quota         |
| 3     | `POST /accounts/{id}/reconnect`             | Re-run consent for one account             |
| 3     | `POST /accounts/{id}/upgrade`               | `drive.file` → `drive`                     |
| 3     | `DELETE /accounts/{id}`                     | Disconnect and delete the stored token     |
| 3     | `GET /storage`                              | Aggregate usage across all accounts        |
| 4     | `GET /files`                                | Unified listing; `?account_id&parent&page` |
| 4     | `POST /files/folder`                        | Create a folder                            |
| 4     | `PATCH /files/{account}/{id}`               | Rename, star, move, trash                  |
| 4     | `DELETE /files/{account}/{id}`              | Trash or permanently delete                |
| 5     | `POST /upload`                              | Streaming resumable upload                 |
| 6     | `GET /files/{account}/{id}/content`         | Streaming download                         |
| 7     | `GET /search`                               | Concurrent search across every account     |
| 8     | `GET /files/{account}/{id}/preview`         | Thumbnail or embed URL                     |
| 9     | `GET /files/{account}/{id}/permissions`     | List permissions                           |
| 9     | `POST /files/{account}/{id}/permissions`    | Share                                      |
