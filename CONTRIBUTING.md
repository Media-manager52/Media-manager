# Contributing to Media Manager

Media Manager is an owner-operated, safety-sensitive application. Keep changes small, reviewable, and grounded in the existing architecture. Do not introduce new infrastructure or abstractions when the current Go, PostgreSQL, React, Vinext/Vite, Cosmos Cloud edge, and Docker Compose stack can express the change directly.

## Before changing code

1. Read `README.md`, [`docs/README.md`](docs/README.md), and the relevant
guides under `docs/` (start with [`docs/onboarding/README.md`](docs/onboarding/README.md)).
2. Confirm the requested behavior is not already implemented or documented.
3. Keep credentials, private URLs, media paths, provider payloads, `.env` files, and generated runtime data out of commits and test output.
4. Use local fixtures and test servers. Tests must not contact Plex, Seerr, AltMount, metadata providers, indexers, or other external services unless a separately authorized live acceptance run explicitly requires it.

## Development workflow

1. Create a focused branch from the current `main`.
2. Make the smallest coherent change and preserve unrelated work.
3. Add regression coverage at the narrowest useful layer.
4. Run the relevant checks below.
5. Update `CHANGELOG.md` for user-visible, operational, or security-relevant changes.
6. Use a clear conventional commit such as `fix(ui): ...`, `feat(indexers): ...`, `security: ...`, or `docs: ...`.
7. Open a merge request with the problem, evidence, safety impact, checks performed, and any remaining limitation. Never describe synthetic evidence as live verification.

## Verification

Frontend changes:

```sh
npm ci
npx playwright install chromium
npm run lint
npm run typecheck
npm test
npm run test:browser
```

Go changes:

```sh
test -z "$(gofmt -l cmd internal)"
GOFLAGS=-mod=readonly go vet ./cmd/... ./internal/...
GOFLAGS=-mod=readonly go test -count=1 -race ./cmd/... ./internal/...
```

Compose or deployment changes:

```sh
docker compose config
```

Use a disposable PostgreSQL 18 database for lifecycle tests. Never point tests or migrations at the production database. Build or recreate only the Media Manager services explicitly in scope.

## Non-negotiable safety boundaries

- Media Manager and Seerr are the only request entry points; Radarr and Sonarr are not runtime dependencies.
- AltMount output is read-only. Publication creates contained Plex-safe managed links.
- Complete TV seasons prefer one pack and remain one acquisition job.
- Required media properties are verified before publication; unknown evidence is not fabricated into success.
- A failed replacement never displaces current media.
- Release exclusions remain exact and workflow-scoped.
- Existing-library adoption, rename, recycle, purge, deletion, and live acquisition require explicit authorization and review.
- Indexers are paced and budgeted; never use broad searches or stress tests as verification.
- Do not weaken DNS pinning, redirect limits, workflow guards, credential stripping, containment checks, publication generations, or exact Plex verification.

## Documentation and release notes

Documentation must distinguish implemented behavior, synthetic test evidence, live verification, blockers, and missing work. Do not add release versions or dates until a corresponding Git tag exists. Licensing is an owner decision and must not be added or changed as part of an unrelated contribution.
