# Media Manager contributor guide

This repository is the source of truth for Media Manager. Keep changes focused,
truthful, and safe for an owner-operated production media system.

## Start here

- Read `README.md` for the product contract, local commands, and safety invariants.
- Read `CORE_RELEASE_STATUS.md` for release evidence and
  `docs/IMPLEMENTATION_STATUS.md` before changing a partial feature.
- Read `docs/INDEXER_CONFORMANCE.md` before touching Newznab behavior.
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
- `internal/httpapi`: owner and internal HTTP routes.
- `app`, `components`, `lib`: Vinext/React UI, route screens, and API client.
- `lib/media-library.ts`: pure movie/series library joins and local filtering.
- `tests` and `tests/browser`: local UI/unit and browser coverage; unexpected external
  network access must remain blocked.

Follow an existing nearby pattern before adding a new abstraction. Prefer a
small pure helper over expanding a screen or handler, and document why a new
layer is necessary if an existing one cannot serve the change.

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
