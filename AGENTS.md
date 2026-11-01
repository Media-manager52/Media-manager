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
- Read `README.md` for the product contract, local commands, and safety invariants.
- Read `docs/status/release.md` for release evidence and
  `docs/status/implementation.md` before changing a partial feature.
- Read `docs/status/indexer-conformance.md` before touching Newznab behavior.
- Working on Media Manager v2? Read `docs/v2/V1_MAP.md` first: it maps every
  v1 module to a v2 verdict and lists the rules and tests v2 must preserve.
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
- `app`, `components`, `lib`: Vinext/React UI, route screens, and API client.
- `lib/media-library.ts`: pure movie/series library joins and local filtering.
- `tests` and `tests/browser`: local UI/unit and browser coverage; unexpected external
  network access must remain blocked.

Follow an existing nearby pattern before adding a new abstraction. Prefer a
small pure helper over expanding a screen or handler, and document why a new
layer is necessary if an existing one cannot serve the change.

## Non-blocking GitHub Actions Policy

GitHub Actions must never block engineering progress:

1. Never use `gh run watch`, `sleep` loops, repeated polling, or a long-running shell command merely to wait for GitHub Actions.
2. Never use a 5–20 minute command timeout for CI status observation.
3. Check CI status once with a short `gh run view`/API call and record the exact run ID, URL and commit SHA.
4. If the run is queued or in progress, mark it **PENDING** and continue independent work on a separate branch/worktree.
5. Do not modify an already submitted commit merely to keep busy.
6. Recheck CI only at a natural dependency boundary, never through a polling loop.
7. If no independent work remains, finish the response and report the pending run.
8. Never merge a PR until its required checks pass on the exact approved commit.
9. If a merge depends on unfinished CI and no authorized GitHub-native auto-merge/merge queue is configured, stop that merge and report **WAITING FOR CI**.
10. Preserve all security, test, review, branch-protection and deployment-authorization boundaries; skipping verification is never permitted.

## Verification

Frontend changes:

```bash
npm run lint
npm run typecheck
npm test
npm run test:browser
```

The library transformation benchmark is local, deterministic, and makes no
network requests:

```bash
npm run bench:library
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
