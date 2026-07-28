# Security policy

## Reporting a vulnerability

Please **do not open a public issue.** Use GitHub's private vulnerability
reporting on this repository, or email the maintainers listed in `README.md`.

Include reproduction steps, affected version, and impact. Expect an
acknowledgement within 72 hours. Please give us 90 days before public disclosure.

## Threat model

SangamDrive is self-hosted. The operator controls the server, so the design
protects against these instead:

| Threat                              | Mitigation                                              |
| ----------------------------------- | -------------------------------------------------------- |
| Database file is stolen             | Refresh tokens sealed with AES-256-GCM; the key lives in the environment, not the database. Sessions stored as SHA-256 hashes. |
| Session cookie is stolen via XSS    | Session cookie is `HttpOnly`; the API's CSP is `default-src 'none'`. |
| Cross-site request forgery          | Double-submit CSRF token required on every state-changing request. |
| Credential stuffing / brute force   | Rate limits per session and per IP. There are no passwords — auth is Google OAuth only. |
| One user reading another's Drive    | Every account query is scoped by `user_id`; account IDs are never trusted from the request alone. |
| Token leakage into logs or responses | Refresh tokens are never serialised into any response, and error causes are logged but never returned in production. |

## What is stored

| Stored                          | Not stored                     |
| ------------------------------- | ------------------------------ |
| User record (email, name, avatar) | File contents                |
| AES-GCM sealed refresh tokens   | File metadata                  |
| Session hashes                  | Thumbnails                     |
| UI preferences                  | Search indexes                 |
|                                 | Access tokens (memory only)    |

Access tokens are held in memory for the life of a request and never written
anywhere.

## Operator responsibilities

- Keep `ENCRYPTION_KEY` and `SESSION_SECRET` secret, unique per deployment, and
  backed up separately from the database.
- Serve over HTTPS with `COOKIE_SECURE=true` in production.
- Restrict `CORS_ORIGINS` to your own origin. Wildcards are rejected at startup.
- Keep the host and images patched.

## Key rotation

Rotating `ENCRYPTION_KEY` invalidates every stored refresh token — all users must
reconnect all accounts. Rotating `SESSION_SECRET` invalidates CSRF tokens, logging
everyone out. Neither is destructive to Drive data, but both are disruptive.

## Supported versions

The latest release only, until the project reaches 1.0.
