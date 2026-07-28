# Developer guide

## Prerequisites

| Tool   | Version | Needed for            |
| ------ | ------- | --------------------- |
| Go     | 1.23+   | `apps/api`            |
| Node   | 20+     | `apps/web`, `packages/shared` |
| Docker | 24+     | container builds (optional) |

## Layout

```
apps/
  api/                        Go + Fiber backend
    cmd/server/               entrypoint, graceful shutdown
    internal/
      apperr/                 the error type crossing the HTTP boundary
      auth/                   sessions, CSRF, OAuth state, cookies
      config/                 env parsing and validation
      cryptobox/              AES-GCM, HMAC, token hashing
      google/                 Google OAuth client (injectable endpoints)
      httpx/                  envelope, middleware, error renderer
      logging/                slog setup
      server/                 Fiber app, middleware, route handlers
      store/                  persistence interfaces
        sqlite/               the one implementation, plus migrations
  web/                        Next.js frontend
    app/                      App Router pages and providers
    components/               UI, with primitives under components/ui
      auth/                   sign-in, session and callback UI
    lib/                      API client, query client, auth hooks
packages/
  shared/                     TypeScript types mirroring the Go DTOs
docker/                       compose files and reverse-proxy examples
docs/                         this documentation
```

## Running locally

```bash
cp .env.example .env
openssl rand -base64 32   # -> ENCRYPTION_KEY
openssl rand -base64 32   # -> SESSION_SECRET
# add GOOGLE_CLIENT_ID / GOOGLE_CLIENT_SECRET — see docs/oauth-setup.md
```

The API reads plain environment variables and does not parse `.env` itself.
Export them first:

```bash
cd apps/api
set -a && source ../../.env && set +a
go run ./cmd/server
```

In a second terminal:

```bash
npm install          # first time only
npm run dev          # -> http://localhost:3000
```

`apps/api/Makefile` wraps the common Go tasks — `make help` lists them.

## Testing

```bash
cd apps/api
go test ./...          # unit tests
go test -race ./...    # before opening a PR
make cover             # HTML coverage report
```

Tests never reach the network. Google is mocked at the client boundary, and the
SQLite tests use a real database in `t.TempDir()` — an in-memory database would
not survive the connection pool.

```bash
npm run typecheck      # both TS workspaces
npm run lint
```

## Conventions

### Go

- Handlers return `error`. The renderer in `httpx.ErrorHandler` turns it into a
  response — never call `c.Status(...).JSON(...)` for an error directly.
- Every error crossing the HTTP boundary is an `*apperr.Error` with a stable code.
  Internal detail goes in `WithCause`, which is logged and never serialised.
- Handlers depend on `store.Store`, not on the sqlite package.
- `gofmt -s` and `golangci-lint run` must both be clean.

### TypeScript

- Every API call goes through `lib/api.ts`. It handles the envelope, CSRF header,
  credentials and error normalisation; a bare `fetch` skips all four.
- Shared DTOs live in `packages/shared`. If you change a Go DTO, change the
  matching type in the same commit.
- Server components never fetch Drive data. Drive state belongs to TanStack Query
  in the browser, so the server holds no user file metadata even briefly.
- Query keys come from `queryKeys` in `lib/query-client.ts` so invalidation after
  a mutation cannot drift.

### Adding a route

1. Add the handler in `apps/api/internal/server/routes_<area>.go`.
2. Register it in `registerRoutes`.
3. Add the DTO to `packages/shared/src/`.
4. Add a hook under `apps/web/lib/` or the feature's directory.
5. Document it in `docs/api.md`.

### Adding a migration

Create `apps/api/internal/store/sqlite/migrations/000N_description.sql`. Files run
once, in filename order, each in its own transaction, tracked in
`schema_migrations`. Never edit a migration that has shipped — add a new one.

### Adding a shadcn/ui component

```bash
cd apps/web
npx shadcn@latest add dialog
```

`components.json` is already configured for the App Router, Tailwind v4 CSS
variables and the `@/` alias.

## Things that will bite you

**Uploads must not be buffered.** Anything that reads the whole request body into
memory before forwarding it breaks large-file support. Keep the pipe intact.

**Never persist Drive metadata.** Caching a listing "just for a minute" is the
first step toward the sync bugs this project exists to avoid.

**Rate limits are per Google project**, not per user. Fan-out concurrency is
bounded by `DRIVE_CONCURRENCY` for that reason — raising it makes 429s more likely,
not throughput higher.

**Tokens are per account, not per user.** A user with five Drives has five refresh
tokens, five access tokens, and five independent failure modes.
