# Media Manager contributor guide

This repository is the source of truth for Media Manager. Keep changes focused,
truthful, and safe for an owner-operated production media system.

## Start here

- Read `docs/onboarding/README.md` for the full codebase onboarding set:
  read order, architecture maps, backend/database/API/frontend references,
  domain workflows, operations runbook, development recipes, and glossary.
  Start there when you have no prior context; the hub is `docs/README.md`.
- To inspect or operate a running Media Manager, use its API, not SQL: see
  `docs/operations/ai-agents.md` (`GET /api/v1`, `GET /api/v1/openapi.json`,
  or the MCP server: `docker exec -i media-manager-api-1 media-manager mcp`).
- Read `README.md` for the product overview, `docs/invariants.md` and
  `docs/architecture/acquisition-safety.md` for the guarantees, and
  `docs/operations/manual-install.md` for configuration and operations.
- Read `docs/status/release.md` for release evidence and
  `docs/status/implementation.md` before changing a partial feature.
- Read `docs/status/indexer-conformance.md` before touching Newznab behavior.
- Check `git status` before editing. Preserve unrelated local work and never add
  `.env*`, credentials, runtime data, media, or download output.

## Code map

- `cmd/media-manager`: Go API and worker entry point.
- `internal/workflows`, `catalog`, `releases`, and neighboring packages: focused
  domain rules and durable types.
- `internal/acquisition`, `publication`, and `adoptionexec`: lifecycle
  orchestration between policy, adapters, and storage.
- `internal/adapters`: bounded external-service clients. Keep pacing, redirect,
  path-containment, and credential boundaries intact.
- `internal/postgres`: PostgreSQL repositories and migrations.
- `internal/httpapi`: owner and internal HTTP routes. `api_catalog.go` is the
  single route table (auth, effect, body type); the OpenAPI document is
  generated from it. Add new routes there and regenerate
  `docs/reference/openapi.json`
  (`MM_WRITE_OPENAPI=1 go test ./internal/httpapi -run TestCommittedOpenAPIIsCurrent`).
- `web/`: the owner web UI (Vite, React, TypeScript, Tailwind), Danish text.
  `web/src/api/client.ts` is the API client, `web/src/pages/` one file per
  screen, `web/src/i18n.ts` the only place API codes become words, and
  `web/server.mjs` the dependency-free production server. See
  [docs/architecture/frontend.md](docs/architecture/frontend.md).
- `web/src/**/*.test.*` and `web/tests/browser`: unit and browser coverage. The
  browser tests mock the API; unexpected external network access must remain
  blocked.

## Guardrails

- `docs/invariants.md` lists the guarantees (S01–S18) and the tests that
  prove them. Never weaken one silently.
- `docs/architecture/boundaries.md` lists the layer rules.
  `internal/architecture` fails the build when a change crosses a layer or
  grows a file past 1,000 lines; its allowlist may only shrink.
- CI also runs pinned `staticcheck` and `govulncheck`.

Follow an existing nearby pattern before adding a new abstraction. Prefer a
small pure helper over expanding a screen or handler, and document why a new
layer is necessary if an existing one cannot serve the change.

## GitHub Actions Policy

Waiting for CI must never block unrelated work, and it must not add idle time
when the next step depends on it.

1. Record the exact run ID, URL and commit SHA of every run you depend on.
2. When nothing depends on the run, do not wait: mark it **PENDING** and
   continue independent work on a separate branch/worktree.
3. When the next step depends on the run (merge after PR checks, deploy after
   main Verify, live checks after a deploy), wait with
   `scripts/ci/wait-for-run.sh <run-id>`. It reads only the run status every
   30 seconds and returns as soon as the run finishes. Never use
   `gh run watch` (it streams logs) or a fixed `sleep` sized to a guess.
4. Prefer `gh pr merge --auto --squash` (the repository allows only squash
   merges, so `main` reads as one commit per pull request): GitHub merges by itself once the
   required checks (`frontend`, `browser`, `backend`, `containers`) pass on the
   PR's head commit.
5. Do not modify an already submitted commit merely to keep busy.
6. Never merge a PR until its required checks pass on the exact approved commit.
7. Preserve all security, test, review, branch-protection and
   deployment-authorization boundaries; skipping verification is never
   permitted.

## Verification

Frontend changes (from `web/`):

```bash
npm ci
npm run lint
npm run typecheck
npm test
npm run build
npm run test:browser
```

Go changes:

```bash
"$(go env GOROOT)/bin/gofmt" -w <changed-go-files>
GOFLAGS=-mod=readonly go vet ./cmd/... ./internal/...
GOFLAGS=-mod=readonly go test -count=1 -race ./cmd/... ./internal/...
```

Run `docker compose config --quiet` after Compose changes. Do not run live
searches, downloads, publication, Plex refreshes, media scans, destructive
operations, migrations, deployments, or external integration tests unless the
task explicitly authorizes them.

## Change discipline

- Preserve exact provider identities, workflow revisions, and selected-release
  protections across asynchronous work.
- AltMount output is read-only; publication uses managed Plex-safe symlinks.
- Required media properties are successful only after bounded output inspection
  and exact Plex verification. Failed output stays owner-visible and undeleted.
- Never weaken Newznab pacing, per-hop redirect pinning, header stripping, or
  secret sanitization for convenience.
- Add focused tests for behavior changes, run the relevant full gate, inspect the
  final diff, and use one clear commit per passing milestone.
