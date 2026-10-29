# Media Manager — finish-today execution brief (Claude Code)

You are the maintainer of Media Manager at `/home/vps/media-manager`. The owner
wants the remaining work finished **today**. Work autonomously, one milestone at
a time, and keep the owner informed with a one-line status after each item.

This brief is optimized for an Opus-class Claude Code session at **medium
reasoning effort**: front-loaded context, a concrete queue, no broad audits.

## Minutes 0–10: orient (exactly this, then start)

1. `git status` — the tree should be clean. The 2026-09-28 hardening
   (`c6bf1b8`) is committed on `main` and includes the docs restructure, the
   Radarr/Sonarr-compatible Seerr API, Seerr root-folder routing, and release
   package screening/recovery at schema 40. If anything is uncommitted, inspect
   it before touching it; never discard work you did not create.
2. Read `AGENTS.md`, then `docs/onboarding/README.md`, then
   `docs/architecture/overview.md`.
3. Skim `docs/status/implementation.md` — it is the authoritative backlog of
   what is Real vs Partial, with the exact "Required work" per screen. It is
   dated, so verify each item you pick with a quick grep before building.
4. For anything you touch, open the matching map:
   `docs/architecture/{backend,database,api,frontend,workflows}.md`.
5. Do **not** read everything. No broad audits. Start on item 1.

## Repository state

- V1 is released; `main` is at `c6bf1b8` (2026-09-28 hardening), database
  schema **40**, Go 1.27, Node 24 / Vinext / Next 16, PostgreSQL 18.
- The live stack runs via Docker Compose (`media-manager-api-1`,
  `media-manager-ui-1`, `media-manager-postgres-1`). **Do not recreate,
  restart, or migrate it** unless the owner asks.
- Commit one clear milestone per item after its gate passes; inspect the diff
  before each commit. Use conventional prefixes (`feat`, `fix`, `test`, `docs`,
  `chore`).

## Working agreement

- Take one item from the queue end to end: store → API → UI → focused tests →
  docs → commit. Do not batch unrelated changes.
- After each item: run the focused tests plus the relevant full gate below,
  commit, and post a one-line status (item, what changed, gate result).
- If an item is already done, blocked, or unsafe, mark it and move to the next.
  Do not stall or expand scope.
- Fail closed. Unknown states, identities, variants, or failure classes get an
  explicit error/attention code, never a guess.
- Do not fake completion. Only mark something Real in
  `docs/status/implementation.md` when it is end-to-end verified.
- Stop and ask only for: destructive actions, live operations, migrations,
  deployments, or a genuine design fork. Everything else: decide, do, verify.

## Hard safety rules (never violate)

1. AltMount output is **read-only**. Never write, move, or delete inside it.
2. Publish only contained Plex-safe symlinks, always through
   `internal/library` (never raw `os.Rename`/`os.Symlink`).
3. A season pack stays **one** AltMount job with one immutable expectation set.
4. AltMount completion ≠ import completion ≠ Plex completion. Nothing is
   successful without exact Plex identity + path confirmation.
5. Never weaken Newznab pacing, retry budgets, redirect pinning, TLS checks,
   credential stripping, or secret sanitization.
6. Failed outputs stay retained, owner-visible, and undeleted.
7. Only the owner opens another acquisition round; never fake a recovery.
8. Preserve provider identities, workflow revisions, and selected-release
   protections across asynchronous work.
9. Never add `.env*`, credentials, runtime data, media, or download output to git.
10. Frontend fixtures must never enter the live UI artifact.

**No live operations** (searches, downloads, submissions, publication, Plex
refreshes, media scans, migrations, deploys, state-changing API calls) without
explicit owner approval. Local tests, builds, and read-only checks are always
fine.

## Completed since this brief was written (2026-09-28)

Do not redo these; verify with a quick grep if a queue item touches them.

- Seerr uses Media Manager as its only Radarr/Sonarr server
  (`/radarr/api/v3`, `/sonarr/api/v3`, `SERVARR_API_KEY`); both services are
  configured, default, and verified through Seerr's own clients. Scan
  `syncEnabled` is off on purpose: Seerr's scanner would orphan-decline
  processing requests not yet in the Media Manager catalog, and Media Manager
  pushes Plex-confirmed availability itself.
- Seerr root-folder routing (schema 39) with request provenance and requeue on
  route save.
- 118 stranded Seerr requests backfilled through normal intake; Danish-original
  routing verified against the two `Best Available Danish Original` profiles.
- NZB package screening plus release-attributable AltMount failure recovery
  (schema 40): archive-only/ISO/non-playable manifests are rejected before
  upload, and archive/unplayable/missing-article failures exclude the release
  and retry inside the round (including from persisted detail after AltMount
  prunes the queue entry).
- `runtime = 0` provider metadata tolerated during intake.
- Danish subtitles remain required for foreign content on the `Best Available
  Danish Subs` profiles (a same-day preferred experiment was reverted) and
  msgnews downloads request the canonical `www` host directly.
- Documentation restructure, per-package `doc.go`, `scripts/check-docs.mjs`,
  and both CI pipelines wired to it.

## Today's queue (default priority — the owner may edit this section)

Build in this order. Each item is "finish it properly": backend + UI + tests +
docs. The first three are the highest-value remaining owner-facing gaps.

1. **Connection/configuration lifecycle (highest ROI).**
   - `integration_instances`: update (PATCH), enable/disable, set default, and a
     **safe read-only test** endpoint per kind (Plex refresh ping, AltMount
     queue access check, Seerr status, Newznab CAPS-less ping) that never
     downloads and never mutates the provider.
   - `library_roots`: PATCH, enable/disable, default reassignment, preflight
     (paths absolute/contained/exist), naming preview from real values.
   - UI: edit/disable/default/test controls on `/settings/connections` and
     `/settings/storage` with truthful error states.
   - Update `docs/status/implementation.md` rows for connections, storage,
     integration lifecycle.
2. **`/wanted`: independent goal deltas + pagination.**
   - Derive missing vs upgrade plus quality/language/audio/subtitle deltas from
     the persisted target profile, current publications, and adopted files.
   - Add bounded server-side pagination.
3. **`/console/requests`: server-side filtering, search, and pagination.**
   - Move filters/search/paging behind `GET /api/v1/requests` query parameters;
     keep the UI behavior and tests.
4. **`/activity/queue`: deep work detail and safe recovery.**
   - Job/attempt detail, round slots, phase evidence, retained-output summary,
     and guided owner recovery actions that already exist server-side
     (`acquisition-rounds`, `reconcile-altmount`, `reverify-retained-output`,
     `recover-altmount-submission`), without exposing live mutation by default.
5. **`/system/health`: bounded live probes with history.**
   - Read-only, timeout-bounded checks for Plex, AltMount, and Seerr persisted
     as operation status; the page must show real state or truthful
     "unknown", never synthetic success.
6. **Remaining partial screens**, as far as time allows, in this order:
   - `/intelligence/*` pagination + complete explanations;
   - `/files` companion relationships and bounded pagination;
   - `/calendar` movie dates and owner range controls;
   - `/sources/import-lists` movie monitoring approval + exact-season TV review;
   - `/system/tasks` persisted run outcomes/durations;
   - `/system/backups` encryption-key configuration and retention policy UI.
7. **Closeout.**
   - Refresh `docs/status/implementation.md` to match reality.
   - Add/adjust the `CHANGELOG.md` entry.
   - Run the full gates below; fix anything red.
   - Final message: what shipped, what remains, and the exact next action for
     each remaining item.

## Definition of done for today

- Every attempted queue item is implemented, tested, documented, and committed.
- All local gates are green (or a precise, honest failure is reported).
- `docs/status/implementation.md` reflects the real state.
- A final summary lists completed items, blocked items with reasons, and the
  next actionable step for anything unfinished.

## Gates (run the relevant full set before each commit)

Frontend / docs:

```sh
npm run lint
npm run typecheck
npm run docs:check
npm test
npm run test:browser        # needs local Playwright
```

Go:

```sh
"$(go env GOROOT)/bin/gofmt" -l cmd internal
GOFLAGS=-mod=readonly go vet ./cmd/... ./internal/...
GOFLAGS=-mod=readonly go test -count=1 -race ./cmd/... ./internal/...
```

PostgreSQL integration tests skip unless `MEDIA_MANAGER_TEST_DATABASE_URL` is
set. A disposable database is fine:

```sh
docker run --rm -d --name mm-test-pg -p 5433:5432 \
  -e POSTGRES_PASSWORD=media_manager_test -e POSTGRES_USER=media_manager \
  -e POSTGRES_DB=media_manager_test postgres:18.6-alpine3.24
export MEDIA_MANAGER_TEST_DATABASE_URL='postgres://media_manager:media_manager_test@127.0.0.1:5433/media_manager_test?sslmode=disable'
```

Compose changes: `docker compose config --quiet`. Report exact commands and
results; never claim a gate passed without running it.

## Environment facts

- Loopback API: `127.0.0.1:28081`, `Authorization: Bearer <OWNER_TOKEN>`
  (token in `.env`; never print or commit it).
- Cosmos Cloud is the public edge; the browser never sees the owner token.
- `ffprobe` is available inside the API image.
- Do not touch other stacks on this host (global-media-discovery, DRI,
  `mm-runner`, Plex/AltMount/Seerr containers).

## First response

1. Five-bullet system summary + current git state.
2. Your proposed order for today's queue (flag any item already done).
3. Then start item 1 immediately — no need to ask for confirmation.

## Task additions from the owner

<!-- Optional: paste specific priorities or acceptance criteria here. -->
