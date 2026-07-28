# Contributing

Thanks for wanting to help. SangamDrive is small on purpose — the fastest way to
get a change merged is to keep it small too.

## Before you start

Open an issue first for anything beyond a bug fix or a typo. A short conversation
about approach saves a long conversation about a large diff.

## Setup

See [docs/development.md](docs/development.md).

## The project's constraints

These are not style preferences. A PR that breaks one of them will be asked to
change, however good the code is.

1. **No file metadata is ever persisted.** No caching listings, no search index,
   no thumbnail store.
2. **No user file ever touches the server's disk.** Uploads and downloads stream.
3. **No business logic duplicated from Google.** If Drive already decides it, let
   Drive decide it.
4. **Persistence stays behind the `store` interfaces.** Handlers never import a
   concrete driver.
5. **Every error crossing the HTTP boundary carries a stable `apperr` code.**

## Making a change

```bash
git checkout -b feat/short-description
```

Before pushing:

```bash
cd apps/api && gofmt -s -w . && golangci-lint run && go test -race ./...
cd ../.. && npm run lint && npm run typecheck && npm run build
```

## Commits

[Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/):

```
feat(accounts): allow upgrading drive.file to full drive scope
fix(upload): keep the resumable session alive on transient 5xx
docs(deployment): document proxy buffering requirements
```

Types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `chore`, `ci`,
`build`, `revert`. Breaking changes get a `!` after the type or a
`BREAKING CHANGE:` footer. Subject line: imperative, lowercase, no trailing
period, 72 characters max. Explain **why** in the body, not what.

## Pull requests

- One logical change per PR.
- Describe the user-visible effect, not just the implementation.
- Include tests for behaviour you added or fixed.
- Update `docs/api.md` if you touched the HTTP surface, and
  `packages/shared` if you touched a DTO.
- Screenshots for UI changes, light and dark mode both.

## Tests

- Go tests never reach the network. Mock Google at the client boundary.
- SQLite tests use a real database in `t.TempDir()`.
- Test the failure paths — expired tokens, quota exceeded, 429, partial fan-out
  failure. Those are the paths users actually hit.

## Reporting bugs

Include the `request_id` from the error message or the `X-Request-ID` header, the
SangamDrive version from `/api/v1/meta`, and what you expected instead.

For security issues see [SECURITY.md](SECURITY.md) — please do not open a public
issue.

## License

Contributions are accepted under the [MIT License](LICENSE).
