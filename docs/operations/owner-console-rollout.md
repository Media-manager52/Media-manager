# Owner console rollout — handoff for the production server

Purpose: bring the production server from an older Media Manager build to the
current `main` (the owner-console redesign, Seerr Approve/Decline, real
AltMount progress, the safe Plex re-check, and the fixes found by end-to-end
testing), verify it, and know exactly what was and was not tested before it
reached the server.

This note is written for two readers: the **owner**, and a **Claude Code
session running on the server** that has none of the history of the session
that built this work. Everything that session learned and that matters for the
rollout is here.

> **Owner: how to use this.** Open Claude Code in the Media Manager folder on
> the server and say:
>
> *"Read AGENTS.md and docs/operations/owner-console-rollout.md. Do the
> pre-flight checks and the backup, then tell me what you found before
> deploying anything."*
>
> The server session should stop and report after each numbered stage below
> that says **Stop and report**.

## 1. What changed

All of it is merged into `main` on GitHub. The server's old build may predate
some or all of it; §3.1 shows how to find out exactly which.

| PR | What it did | Server impact |
| --- | --- | --- |
| #1 | Codebase onboarding documentation (`docs/onboarding/`). | None (docs). |
| #2 | Seerr episode-availability bug fix. | Behavior fix in Seerr sync. |
| #3 | Seerr request revocation fix. | Behavior fix in Seerr intake. |
| #4 | First UI redesign (semantic tokens, dark/light themes). | UI only. |
| #6 | Owner console, step 1: new sidebar layout, Overview page, rebuilt Library, Requests, Wanted, Workflows, and title pages. | UI only. |
| #7 | Step 2: real AltMount download percentage, per-episode status on TV pages, API uptime. | **Migration 42** (additive, see §4). |
| #8 | Step 3: **Approve / Decline** Seerr requests from the console; **Re-check Plex** for timed-out titles. Also a flaky-test fix. | New owner-only API routes. |
| #10 | Step 4: calendar week view, health tiles, mobile polish. | UI only. |
| #11 | Two bugs found by testing against a real Seerr (§7), plus this note. | **Important crash fix** (§7.1). |

(#5 was an alternative design that was closed, and #9 was a duplicate of the
test fix that went in through #8.)

New owner-only API routes (all behind the owner token, like every `/api` route):

- `POST /api/v1/requests/{id}/approve` and `POST /api/v1/requests/{id}/decline`:
  sends the decision **to Seerr**; Media Manager never admits a request
  locally. Refused with `409 not_pending` unless Seerr still shows it pending,
  and `502` if Seerr does not confirm the new status.
- `POST /api/v1/plex/reconfirm-timed-out`: re-queues the existing exact-path
  Plex confirmation for up to 50 `plex_timeout` workflows, each within its
  retry limit. It never asks Plex for a library-wide scan.

## 2. Rules for the server session

These restate `AGENTS.md` for this rollout. They override convenience.

- **Do not change code on the server.** If something needs fixing, describe it
  to the owner; fixes go through a branch and a pull request, not edits in the
  production checkout.
- **Preserve local state.** Run `git status` first. If the checkout has local
  changes or is not on `main`, stop and report; do not stash, reset, or
  discard anything.
- **Never print, commit, or copy secrets** (`.env`, `.env.integrations`,
  tokens, API keys) into chat, commits, or logs. Read a value into a shell
  variable only when a command needs it.
- **No live actions without the owner's explicit go-ahead in this
  conversation:** no searches, downloads, publication, Plex refreshes or
  scans, Approve/Decline clicks, or destructive commands. The owner does the
  live checks in §6 themselves.
- **Record server facts locally.** Paths, the Compose launch command, which
  optional keys exist (names only, never values), and the pre-upgrade commit
  belong in `CLAUDE.local.md` in the repo folder. That file is git-ignored, so
  it stays on the server and later sessions read it automatically.

## 3. Pre-flight (read-only)

### 3.1 Find what is running now

```sh
cd <media-manager checkout>          # the folder with compose.yaml
git status
git log -1 --oneline
docker compose ps
docker compose exec -T api media-manager version
```

`media-manager version` prints the embedded version and commit. Then list what
the upgrade brings in (read-only):

```sh
git fetch origin main
git log --oneline <running-commit>..origin/main
```

If the running commit is `local` or unknown (the build did not set
`MEDIA_MANAGER_COMMIT`), use `git log -1` of the checkout as the best guide and
say so.

### 3.2 Current schema and work in flight

```sh
TOKEN=$(grep -E '^OWNER_TOKEN=' .env | cut -d= -f2-)
curl -fsS -H "Authorization: Bearer $TOKEN" http://127.0.0.1:28081/health/ready
curl -fsS -H "Authorization: Bearer $TOKEN" http://127.0.0.1:28081/api/v1/system/health
unset TOKEN
```

Use the port from `MEDIA_MANAGER_API_PORT` if it is not the default `28081`.
`/health/ready` answers `"status":"ready"` only when the database schema
matches the running build. `/api/v1/system/health` shows `schema_version` and
`expected_schema`. After the upgrade both must be **42**. Every migration between the
current schema and 42 runs automatically on start (`AUTO_MIGRATE=true` in
Compose). Migrations are forward-only.

### 3.3 Optional metadata keys (names only)

Check which of these are set, without printing values:

- In `.env`: `METADATA_TMDB_TOKEN_SOURCE_FILE`, `METADATA_TVDB_KEY_SOURCE_FILE`,
  `METADATA_SIMKL_CLIENT_ID_SOURCE_FILE`. Also check whether each named file
  exists and is non-empty (`test -s <file>`).
- In `.env.integrations`: `TMDB_ACCESS_TOKEN`, `TVDB_API_KEY`, `SIMKL_CLIENT_ID`.

Why it matters: builds before #11 **crash the API on the first incoming Seerr
request** when any of these providers has no credential (§7.1). After the
upgrade, a missing key is handled correctly. If the old build is running
without all three and has not crashed, it predates that code.

### 3.4 Work in flight

From the `/api/v1/system/health` output in §3.2, note the outbox
`pending`/`due`/`retrying` counts, and in the UI whether
anything is downloading or publishing. The upgrade restarts the API. Durable
work is resumed from PostgreSQL, but a quiet moment is still the better time.

**Stop and report** what you found: running commit, schema, what the upgrade
brings in, which metadata keys exist, and in-flight work.

## 4. Back up (required)

This follows runbook §5. Keep the dump and both env files together, and
protect them as secrets.

```sh
BACKUP_DIR=~/media-manager-backups/$(date +%F-%H%M)
mkdir -p "$BACKUP_DIR" && chmod 700 "$BACKUP_DIR"
docker compose exec -T postgres sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > "$BACKUP_DIR/media-manager.dump"
cp .env .env.integrations "$BACKUP_DIR/" && chmod 600 "$BACKUP_DIR"/*
git rev-parse HEAD > "$BACKUP_DIR/previous-commit.txt"
# Prove the dump is readable (lists its contents, changes nothing):
docker compose exec -T postgres pg_restore -l < "$BACKUP_DIR/media-manager.dump" | head -5
ls -la "$BACKUP_DIR"
```

A zero-byte dump or a `pg_restore` error means **stop**.

About migration 42: it only adds three nullable columns to `altmount_jobs`
(`progress_percent`, `progress_stage`, `progress_observed_at`). No data is
rewritten, so it is quick. It is still forward-only: going back means
restoring this dump (§8).

**Stop and report** the backup folder and that the dump listed correctly. Wait
for the owner's go-ahead to deploy.

## 5. Deploy

Follows runbook §4.

```sh
git checkout main                      # only if §3.1 showed a clean checkout
git pull --ff-only origin main
export MEDIA_MANAGER_VERSION=$(date +%Y.%m.%d)
export MEDIA_MANAGER_COMMIT=$(git rev-parse --short HEAD)
docker compose build
docker compose up -d
docker compose logs --since 5m api | tail -50
```

The shell exports override the placeholder values in `.env` for this build, so
`media-manager version` reports the real commit afterwards.

## 6. Verify

### 6.1 Automatic checks (the server session may run these)

```sh
docker compose ps                                  # api, ui, postgres healthy
docker compose exec -T api media-manager version   # new commit
TOKEN=$(grep -E '^OWNER_TOKEN=' .env | cut -d= -f2-)
curl -fsS -H "Authorization: Bearer $TOKEN" http://127.0.0.1:28081/health/ready   # "status":"ready"
curl -fsS -H "Authorization: Bearer $TOKEN" http://127.0.0.1:28081/api/v1/system/health   # schema_version 42 = expected_schema
curl -fsS -H "Authorization: Bearer $TOKEN" http://127.0.0.1:28081/api/v1/system/info   # includes started_at
unset TOKEN
scripts/smoke-api-direct.sh
scripts/smoke-public-edge.sh https://<your public Media Manager address>
docker compose logs --since 10m api | grep -iE 'panic|fatal|outbox batch failed' || echo "no crash lines"
```

Both smoke scripts are read-only.

### 6.2 Owner checks in the browser (the owner does these)

| Check | Expected | Counts as a problem |
| --- | --- | --- |
| Open the site | New dark console: sidebar, Overview with Needs attention / Active now / Recently completed / Waiting / Recent requests / Plex publication. The status chip at the bottom left says "All systems healthy" or names the component that isn't. | Old look (UI container not rebuilt) or "Media Manager returned HTTP 404" (Cosmos `/api` route, runbook §9). |
| Requests → **Approve** on a request you actually want | A toast "Approved in Seerr · intake will pick it up"; the buttons disappear; Seerr shows the request approved; within about a minute Media Manager shows it progressing (or a clear attention reason). | An error toast; the buttons staying; Seerr unchanged. |
| **Decline** on a request you don't want | A confirmation dialog first ("Keep pending" / "Decline in Seerr"), then "Declined in Seerr", and Seerr shows it declined. Seerr notifies the requester, as it does for any decline. | No dialog, or Seerr unchanged. |
| While something downloads, open Overview → Active now | "AltMount NN%" with a bar, only while AltMount reports a number. | A percentage that never changes while AltMount's own UI moves, or a percentage shown with no active download. |
| TV title page | Seasons with a coverage bar and episode rows (air date, status). | Missing episode rows for a season that has expected episodes. |
| Overview → Plex publication → **Re-check Plex** (only shown when some titles timed out waiting for Plex) | "Plex re-check queued for N titles" (or "Nothing could be re-checked" when each is already at its retry limit); those titles then re-confirm by exact path. | Any sign of a full-library scan in Plex (it must not do one). |

If anything counts as a problem, send the server session (or a new Claude Code
session on the GitHub repo) the exact message, a screenshot, and the output of
`docker compose logs --since 30m api | tail -100`. Remove anything that looks
like a token first.

## 7. Found by end-to-end testing (fixed in #11)

Before the rollout, the whole stack ran against a throwaway local **Seerr
3.5.0**: PostgreSQL, the API and worker, the built UI behind a stand-in for
the Cosmos edge that injects the owner token, and a real Chromium browser
clicking Approve and Decline.

### 7.1 API crash on the first Seerr request

A metadata provider without a credential was wired as a nil client that still
looked "configured". The first Seerr intake called it, and the API process
panicked (`TMDBClient.Lookup` nil pointer dereference in
`internal/metadata/service.go`). The default Compose secrets fall back to
`/dev/null`, so any missing TMDB, TVDB or Simkl credential triggered it.
Fixed: unconfigured providers stay unwired and intake uses Seerr's TMDB data.
Regression tests are in `cmd/media-manager/main_test.go`.

### 7.2 Decision buttons stayed visible after Seerr confirmed

The stored Seerr state stayed "pending" until the worker re-read the request,
so the page could still offer Approve and Decline. Fixed: the status Seerr
confirms is stored in the same transaction as the audit event. The re-read
still replaces it with Seerr's own data afterwards.

## 8. Rollback

Runbook §4: restore the §4 dump into a fresh database, check out the commit in
`previous-commit.txt`, run `media-manager migrate`, and recreate the
containers. Do not point the old build at the upgraded database. Its expected
schema is older than 42, so readiness will fail until the dump is restored.

## 9. What was and was not tested

**Tested**

- The full Go suite with the race detector, including PostgreSQL integration
  tests for migration 42, progress capture, episode lists, owner decisions,
  and the Plex re-check.
- 124 browser tests, covering accessibility and mobile overflow on every route.
- GitHub CI on every PR.
- Against a real **Seerr 3.5.0**:
  - approve and decline, confirmed in Seerr's own database;
  - already-decided requests refused (`409`);
  - unknown requests reported as not found;
  - a wrong API key refused (`403`);
  - the full browser flow described in §7.

**Not tested before reaching the server** (so they are the §6.2 checks)

- **Seerr's own webhook and TMDB lookups.** The test sandbox blocks TMDB, so
  the webhook was sent by hand with the same fields, and movie titles came
  from a local stand-in. Everything else went to the real Seerr.
- **AltMount progress.** It depends on what the production AltMount actually
  reports. The design fails safe: a missing value shows no percentage, never
  an estimate.
- **Re-check Plex against the production Plex.** It reuses the existing,
  already-deployed exact-path re-confirmation, one workflow at a time.
- **Your data at scale.** The pages were exercised with fixtures and a small
  test database.

**Existing behavior left unchanged (possible follow-ups, owner's call)**

- A request that is declined in Seerr is labelled "Not approved". An existing
  test in `internal/postgres/seerr_requests_test.go` asserts this on purpose.
- Requests waiting for a decision, and declined ones, count under the Requests
  page's "Needs attention" filter.
- Just after Approve, the row may read "Not approved" for a few seconds until
  intake re-reads the request from Seerr.

## 10. Reference: the Seerr webhook fields Media Manager reads

These are only needed if the webhook is not already working. Endpoint:
`POST /api/v1/integrations/seerr/webhook`, header
`Authorization: Bearer <SEERR_WEBHOOK_SECRET>`. The JSON body must carry:

- `notification_type`: non-empty;
- `request_id`: a positive number, as a number or string;
- `media_type`: `movie` or `tv`;
- `media_tmdbid` and `media_tvdbid`: may be empty.

Media Manager then fetches the full request from Seerr itself, so nothing else
in the payload is trusted. The polling ticker (every 5 minutes) only follows up
requests Media Manager already knows about. New requests arrive through the
webhook.
