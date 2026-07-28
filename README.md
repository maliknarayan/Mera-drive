# SangamDrive

**One dashboard. Unlimited Google Drives.**

SangamDrive is a self-hosted, lightweight dashboard that lets you connect multiple Google
Drive accounts and manage them from a single interface — browse, search, upload, download,
preview and share across every account at once.

> SangamDrive is **not** cloud storage. Your files never touch the SangamDrive server.
> Everything streams directly between your browser and Google Drive.

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](./LICENSE)

---

## What it stores

| Stored                                | Never stored                  |
| ------------------------------------- | ----------------------------- |
| Your SangamDrive user record          | File contents                 |
| Encrypted Google refresh tokens       | File metadata (names, IDs, …) |
| UI preferences (theme, view mode, …)  | Thumbnails                    |
| Session records                       | Search indexes                |

Every file listing, search and storage number is fetched **live** from the Google Drive API
on each request. There is no metadata database to go stale, leak, or migrate.

---

## Status

Built in phases. Current: **Phase 3 — account management**.

| Phase | Scope                                | State |
| ----- | ------------------------------------ | ----- |
| 1     | Project scaffold                     | done  |
| 2     | Authentication (Google OAuth)        | done  |
| 3     | Account management                   | done  |
| 4     | Unified file browser                 | —     |
| 5     | Streaming uploads                    | —     |
| 6     | Streaming downloads                  | —     |
| 7     | Concurrent global search             | —     |
| 8     | Previews                             | —     |
| 9     | Sharing & permissions                | —     |
| 10    | Docker & documentation               | —     |

---

## Quick start (development)

Prerequisites: **Go 1.23+**, **Node 20+**, and a Google Cloud OAuth client.

```bash
git clone https://github.com/sangamdrive/sangamdrive.git
cd sangamdrive

cp .env.example .env
# generate the two required secrets and paste them into .env
openssl rand -base64 32   # -> ENCRYPTION_KEY
openssl rand -base64 32   # -> SESSION_SECRET

# API
cd apps/api && go mod tidy && go run ./cmd/server

# Web (second terminal)
npm install
npm run dev --workspace apps/web
```

- Web: <http://localhost:3000>
- API: <http://localhost:8080>
- Health: <http://localhost:8080/healthz>

See [docs/oauth-setup.md](docs/oauth-setup.md) for creating the Google OAuth client.

## Quick start (Docker)

```bash
cp .env.example .env   # fill in secrets
docker compose -f docker/docker-compose.yml up -d
```

Full guide: [docs/deployment.md](docs/deployment.md).

---

## Documentation

| Doc                                              | Contents                                     |
| ------------------------------------------------ | -------------------------------------------- |
| [Architecture](docs/architecture.md)             | System design, data flow, boundaries         |
| [OAuth setup](docs/oauth-setup.md)               | Google Cloud console walkthrough             |
| [Deployment](docs/deployment.md)                 | Docker, Nginx, Traefik, backups              |
| [API reference](docs/api.md)                     | Endpoints, error codes, envelopes            |
| [Developer guide](docs/development.md)           | Layout, conventions, testing                 |
| [Contributing](CONTRIBUTING.md)                  | How to propose changes                       |

---

## Permissions

At connection time each Google account is granted one of two scopes:

| Scope                                     | Access                                                  |
| ----------------------------------------- | ------------------------------------------------------- |
| `drive.file` (recommended)                | Only files created or explicitly opened via SangamDrive |
| `drive`                                   | Full read/write access to that Drive                    |

You may upgrade an account from `drive.file` to `drive` later without disconnecting it.

---

## Security

- Refresh tokens are sealed with AES-256-GCM before ever reaching disk.
- Sessions are opaque random tokens; only a SHA-256 hash is persisted.
- Cookies are `HttpOnly`, `SameSite=Lax`, and `Secure` in production.
- All state-changing requests require a double-submit CSRF token.
- Per-IP and per-session rate limits sit in front of every route.

Found a vulnerability? See [SECURITY.md](SECURITY.md).

---

## License

MIT — see [LICENSE](LICENSE).
