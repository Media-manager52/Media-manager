# 08 — Development guide

Purpose: how to build, test, change, and debug this repository safely. Follow
`AGENTS.md` first; this document is the expanded version.

## 1. Prerequisites

- Go 1.27 (`go env GOROOT` — use `$(go env GOROOT)/bin/gofmt` if `gofmt` is not
  on PATH).
- Node ≥22.13 (CI uses Node 24) and npm.
- Docker + Docker Compose (for the stack and container smoke tests).
- PostgreSQL 18 (only needed for Go integration tests; Docker is fine).
- Playwright browsers are provided by the container image in CI or installed
  locally for `npm run test:browser`.

## 2. Command reference

### Frontend

```sh
npm run install:ci       # reproducible install for CI/sandbox
npm run dev              # Vite/Vinext dev server (UI only, no API)
npm run build            # bounded production build (3-minute timeout)
npm run start            # serve the built UI
npm run lint             # ESLint
npm run typecheck        # tsc --noEmit
npm run docs:check       # validate Markdown links/anchors in docs/ and root files
npm test                 # build + node --test tests/*.test.mjs
npm run test:browser     # build + Playwright Chromium specs (tests/browser)
npm run test:live-browser# read-only acceptance against a deployed stack
npm run bench:library    # deterministic library-transform benchmark
npm run db:generate      # Drizzle generation (UI scaffolding only; see warning)
```

### Go

```sh
gofmt -w <changed files>
GOFLAGS=-mod=readonly go vet ./cmd/... ./internal/...
GOFLAGS=-mod=readonly go test -count=1 -race ./cmd/... ./internal/...
go run ./cmd/media-manager migrate     # needs DATABASE_URL or PG* env
```

Integration tests that touch PostgreSQL skip unless
`MEDIA_MANAGER_TEST_DATABASE_URL` is set:

```sh
export MEDIA_MANAGER_TEST_DATABASE_URL='postgres://user:pass@127.0.0.1:5432/media_manager_test?sslmode=disable'
GOFLAGS=-mod=readonly go test -count=1 -race ./internal/postgres/...
```

### Compose

```sh
docker compose config --quiet     # after any Compose change
docker compose build
docker compose up -d
```

## 3. CI

Two equivalent pipelines:

- **GitLab** (`.gitlab-ci.yml`): `frontend` (lint, typecheck, `docs:check`,
  `npm test`, `npm audit --omit=dev`), `browser` (Playwright image), `backend` (PostgreSQL
  18.6 service; `gofmt -l`, `go vet`, `go test -race`, `migrate`, then
  `git diff --exit-code -- go.mod go.sum`).
- **GitHub Actions** (`.github/workflows/verify.yml`): the same `frontend`,
  `browser` (pinned Playwright image), and `backend` jobs, plus a
  `containers` job that builds the stack, checks the API auth boundary
  (`401` unauthenticated, `200` with owner token), asserts the unconfigured
  Seerr webhook returns `503`, and verifies `pg_dump` works.

The GitLab `frontend` job additionally runs `npm audit --omit=dev`; a known
moderate transitive advisory has failed that step historically without blocking
the V1 gates. Check current CI status before assuming red = broken product.

## 4. Repository conventions

From `AGENTS.md` plus observed practice:

- Keep changes focused; one clear commit per passing milestone; conventional
  commit prefixes (`feat`, `fix`, `chore`, `test`, `docs`).
- Follow an existing nearby pattern before adding an abstraction. Prefer small
  pure helpers over expanding a screen or handler.
- Pure domain logic goes in `internal/workflows|catalog|releases|packs|subtitles|library|mediainfo|languages|catalog`;
  SQL only in `internal/postgres`; external I/O only in `internal/adapters`;
  HTTP only in `internal/httpapi`.
- Never guess: unknown variants, identities, states, or failure classes fail
  closed with an explicit error/attention code.
- Errors that cross the worker boundary must implement `SafeCode()` (lowercase
  snake code); raw error text is never persisted.
- New behavior needs focused tests. Run the full relevant gate before claiming
  success and inspect the final diff.
- Do not add `.env*`, credentials, runtime data, media, or download output.
- Check `git status` before editing; preserve unrelated local work.

## 5. Recipes

### 5.1 Add a read model / API route

1. Add a store method in the matching `internal/postgres/*.go` file returning a
   typed struct (follow `readmodels.go` patterns). Use bounded `limit`s.
2. Register the route in `internal/httpapi/server.go` `routes()` with
   `server.requireOwner`.
3. Handler: parse limit/offset with clamps → call store → `writeJSON`.
4. Add the type + loader to `lib/media-manager-api.ts`.
5. Add a table/screen in the UI (see [05-frontend.md](../architecture/frontend.md#8-adding-or-changing-a-screen)).
6. Update [04-api.md](../architecture/api.md).

### 5.2 Add a migration + column

1. `internal/postgres/migrations/00NN_name.sql`.
2. Bump `CurrentSchemaVersion` in `internal/postgres/migrate.go`.
3. Update the store read/write paths.
4. Add a migration integration test
   (`*_migration_integration_test.go` pattern) that applies migrations and
   asserts both upgrade and fresh-DB behavior.
5. Update [03-database.md](../architecture/database.md).

### 5.3 Add/modify a workflow transition

1. Edit `allowedTransitions` in `internal/workflows/model.go`.
2. Add/extend `model_test.go` for legal/illegal edges.
3. Audit every handler that produces or consumes the new edge; add revision
   guards and transitions in the same transaction as the state change.
4. Update [01-architecture.md](../architecture/overview.md#4-workflow-state-machine).

### 5.4 Add an outbox event

1. Insert the event in the same serializable transaction as its state change
   (never in a separate statement).
2. Add a handler in `cmd/media-manager/main.go` (`handlers["event.name"]`).
3. Make the handler idempotent, revision-checked, and safe to retry. Return
   `SafeCode()` errors; implement `RetryAt()` for paced deferrals.
4. Add an integration test that exercises the lifecycle; update
   [01-architecture.md](../architecture/overview.md#3-the-durable-outbox-the-heart-of-the-system).

### 5.5 Add or change a quality-profile policy field

1. Define/extend the rule JSON shape in `internal/releases` or
   `internal/subtitles` (pure package).
2. Parse + validate in `internal/acquisition/search.go` (`parseRules`) and/or
   `subtitles.ParsePolicy`; canonicalize on write in
   `httpapi.updateQualityProfile` so stored policies are normalized.
3. Bump `policy_version` semantics when the meaning of existing rules changes.
4. Add rule tests and a PostgreSQL lifecycle test (`quality_profile_routing_test.go`).

### 5.6 Add an adapter

1. New package under `internal/adapters/<name>` with an explicit `Config`,
   `New(config, transport http.RoundTripper)` constructor, bounded timeouts,
   response size limits, strict parsing, and error types with `SafeCode()`.
2. No adapter imports `internal/postgres`; services pass values in/out.
3. Unit tests use `httptest`/custom transports; never hit the real service in
   tests.
4. Wire it in `cmd/media-manager/main.go` behind config validation.

### 5.7 Debugging

- **Start here for any flow**: find the event type in
  `handlers` (`cmd/media-manager/main.go`), read its service `Handle`, then the
  store methods it calls.
- **Stale events look like no-ops**: handlers compare `AggregateRevision`. If
  nothing happens, check the workflow/job revision in the DB.
- **Worker failures** are stored as safe codes in `outbox_events.last_error`;
  query it instead of grepping container logs.
- **Serialization conflicts** surface as `ErrConcurrentModification` and are
  usually benign (stale event). Do not turn them into success; just return nil
  where handlers already do.
- **Deterministic discovery timing** comes from
  `retryDiscoveryAt`; read the test (`acquisition_rounds_test.go`) to compute
  expected times rather than guessing.
- **Frontend data issues**: check the API response first (network tab / curl),
  then the loader types in `lib/media-manager-api.ts`.

## 6. Never do these

1. Write, move, or delete anything under the AltMount mount.
2. Bypass `internal/library` for path operations.
3. Send more than one in-flight Newznab request per indexer or skip the
   scheduler/budget reservation.
4. Weaken redirect pinning, header stripping, TLS verification, credential
   sanitization, or request size limits.
5. Treat AltMount completion, a filesystem write, or a Plex scan as success
   without exact confirmation.
6. Mark a failed output as success, delete it, or silently retry it.
7. Open an acquisition round without the owner or without a ready discovery
   candidate.
8. Put secrets, `.env*`, media, or runtime data in git or in fixtures.
9. Import development fixture data into the live UI graph.
10. Edit applied migration files, `schema_migrations`, or provider identities
    to make tests pass.

## 7. Where to look when…

| Need | Go to |
| --- | --- |
| Understand a workflow state | `internal/workflows/model.go` + [06-workflows.md](../architecture/workflows.md) |
| Trace a search | `internal/acquisition/search.go` → `search_plan.go` → `internal/adapters/newznab` |
| Trace a failure | `internal/postgres/acquisition_rounds.go` (`recordAttributableAcquisitionFailure`) |
| Understand verification | `internal/packs/coverage.go`, `internal/subtitles/verify.go` |
| Understand publication | `internal/publication/service.go`, `internal/library/` |
| Understand Plex | `internal/publication/plex.go`, `internal/adapters/plex/` |
| Understand adoption | `internal/adoptionexec/service.go` |
| Understand recycle | `internal/recycle/service.go`, `internal/postgres/recycle.go` |
| Understand Seerr | `internal/intake/`, `internal/adapters/seerr/` |
| Understand commissioning | `internal/commissioningv2/`, `internal/commissioning/` |
| Understand the schema | `internal/postgres/migrations/` + [03-database.md](../architecture/database.md) |
| Understand the UI | `components/media-manager/app-shell.tsx` + [05-frontend.md](../architecture/frontend.md) |
