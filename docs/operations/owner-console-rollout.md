# Owner console rollout — handoff for the production server

This handoff records the earlier schema-42 console rollout. Current production
is `34f0024`, schema 43; use [release status](../status/release.md) for the latest
checkpoint and [the runbook](runbook.md) for current deployment configuration.
Historical rehearsal results below are preserved as recorded.

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
>
> Later, to find problems: *"Do the full debugging pass in §11 and give me the
> report."* To fix one: *"Fix <the problem> following §12."*

## 1. What changed

All of it is merged into `main` on GitHub. The server's old build may predate
some or all of it, and also earlier work that is not listed here (for example
acquisition hardening and Seerr root routing). §3.1 and §3.5 show how to find
out exactly what the upgrade brings in.

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
| #12 | Read-only screens refresh themselves; request labels follow Seerr ("Awaiting approval", "Approved in Seerr", "Declined"). | UI and read model only. |
| #13 | Fixes from the full code review and the upgrade rehearsal (§9). | Behavior fixes; no migration. |

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

- **Never hand-edit the live checkout or running containers.** Fixes follow
  §12: configuration changes are backed up first, and code changes are made in
  a separate fix folder, tested, and merged through a pull request before a
  normal deploy.
- **Explain first, then fix with the owner's OK.** Before any change, tell the
  owner what is wrong, the evidence, the proposed fix, and its risk, then wait
  for a go-ahead.
- **Use the remote that points to GitHub.** GitHub is the source of truth, but
  its remote is not always called `origin`: on some servers `origin` is an old
  mirror and GitHub is another remote. Check with `git remote -v` and use the
  remote whose URL is `github.com/Media-manager52/Media-manager`. The commands
  in this note find it themselves and keep its name in `$GH`:
  ```sh
  GH=$(git remote -v | awk '$2 ~ /github\.com[:\/]Media-manager52\/Media-manager(\.git)?$/ && $3 == "(fetch)" {print $1; exit}')
  : "${GH:?no git remote points to GitHub; see §2}"
  ```
  The second line stops with a clear message when no remote points to GitHub.
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
GH=$(git remote -v | awk '$2 ~ /github\.com[:\/]Media-manager52\/Media-manager(\.git)?$/ && $3 == "(fetch)" {print $1; exit}')
: "${GH:?no git remote points to GitHub; see §2}"
git fetch "$GH" main
git log --oneline <running-commit>.."$GH"/main
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

### 3.5 Upgrading from an older build

The server runs an older build, so this upgrade brings in **everything merged
since that build**, not only the console work in §1. Work out exactly what
that is before deploying. All of these commands are read-only:

```sh
OLD=<running commit from §3.1>
GH=$(git remote -v | awk '$2 ~ /github\.com[:\/]Media-manager52\/Media-manager(\.git)?$/ && $3 == "(fetch)" {print $1; exit}')
: "${GH:?no git remote points to GitHub; see §2}"
git log --oneline "$OLD".."$GH"/main                         # every change coming in
git diff "$OLD".."$GH"/main -- CHANGELOG.md                  # the same, in plain language
git diff --stat "$OLD".."$GH"/main -- internal/postgres/migrations   # migrations that will run
git diff "$OLD".."$GH"/main -- compose.yaml .env.example .env.integrations.example
```

What to look for, and what it means:

- **Migrations.** Every migration after the current schema runs once, in
  order, at start-up (§3.2 gives the current number). Most only add tables
  or columns. A few older ones deliberately change existing rows. Read the
  comment at the top of each one that will run. In particular:
  - `0032` introduces acquisition rounds. It also labels existing failed
    AltMount jobs, and workflows that need attention because of one, with an
    explicit `altmount_submission_rejected` reason.
  - `0037` and `0038` recover workflows that older builds left stuck.
    Exhausted rounds get a paced, low-priority retry discovery; stranded
    `held` workflows go back onto the normal search path.
  - `0033` adds explicit movie and TV category settings to each stored indexer
    configuration, copied from its existing categories.
  - **So within minutes of the upgrade, Media Manager may start paced indexer
    searches for workflows that were idle before.** This is intended, it stays
    within the configured Newznab pacing, and it is the automatic behavior the
    new build is designed to have. It is not the server session doing a live
    search. Tell the owner beforehand.
- **Configuration.** New optional settings have safe defaults, for example
  `SERVARR_API_KEY`, `MEDIA_LIBRARY_HOST_PATH`/`MEDIA_LIBRARY_GID` and the
  `METADATA_*_SOURCE_FILE` secrets. If `compose.yaml` gained a required
  variable (written `${NAME:?...}`) that `.env` lacks, `docker compose` refuses
  to start and names it; it never starts half-configured. Compare `.env`
  against `.env.example` **by variable name only** and list any names that
  are missing.
- **Behavior.** Read the `CHANGELOG.md` diff. It lists every behavior change
  since the old build, including acquisition, Seerr routing and recovery
  changes that are not part of the console work.

If the server's checkout is not a git clone of this repository (for example,
files copied by hand), or `$OLD` is not found in the history, stop and report
that rather than guessing.

**Stop and report** what you found: running commit, schema, the migrations
that will run (flagging the row-changing ones), any configuration names missing
from `.env`, the main behavior changes from the changelog, which metadata keys
exist, and in-flight work.

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
GH=$(git remote -v | awk '$2 ~ /github\.com[:\/]Media-manager52\/Media-manager(\.git)?$/ && $3 == "(fetch)" {print $1; exit}')
: "${GH:?no git remote points to GitHub; see §2}"
git pull --ff-only "$GH" main
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

**Upgrade rehearsal from an older build**

The 14 September build (schema 36) was run on a fresh database and configured
the way a production server is: Seerr, Plex, AltMount, a commissioned indexer
(all pointing at unreachable `.invalid` addresses so nothing could search,
download or contact Plex), library roots and default profiles. Through its own
webhook intake from a real Seerr 3.5.0 it created movie and TV requests,
media, and search, airing-season and episode workflows. The database was then
backed up, the new build was started on it with `AUTO_MIGRATE`, and the
result was checked:

- Migrations 37–42 applied in one start-up and `/health/ready` answered
  ready at schema 42.
- Every media item, workflow, Seerr request, expectation and setting was
  preserved, and every workflow's state and revision was unchanged.
- The new build re-read the approved Seerr requests on its own start-up poll.
- The repository's live read-only acceptance suite passed 114 of 117 checks
  at desktop, tablet and mobile sizes. The other three are one pre-existing
  test that times out when the Files list is empty (the rehearsal had no
  downloaded files). On a server with files it finds them.
- In a browser on the upgraded stack:
  - approving a pending TV request from the Overview stayed on the Overview;
    Seerr recorded it and intake created the season workflow;
  - declining a movie was recorded in Seerr;
  - the airing season listed its unaired episode;
  - there were no page errors or failed API calls.
- Rollback: the pre-upgrade dump restored into a fresh database and the old
  build ran on it. The old build pointed at the upgraded database correctly
  refused readiness, as §8 says.
- Found and fixed along the way (§7.1 and the changelog):
  - the old build also has the metadata-provider crash;
  - a title page read "Quality profile: Not assigned" before any file existed.

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

**Request labels and live refresh (added after the end-to-end test)**

- Requests follow Seerr's own state:
  - "Awaiting approval" while pending, counted under Needs attention because
    the owner has to decide;
  - "Approved in Seerr" once approved, shown as active and not as success
    until intake admits it;
  - "Declined" once declined, and no longer counted as needing attention.
- Read-only screens re-read Media Manager's state every 15 seconds (Workflows
  every 8) while the tab is visible, so progress and decisions appear without
  pressing Refresh. Form and settings screens stay manual.
- Real posters appear wherever Media Manager has TMDB metadata for a title,
  with a letter tile only when there is none. The test sandbox could not load
  TMDB images, so the §6.2 browser check is the first time real posters are
  seen.

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

## 11. Full debugging pass on the server (read-only)

Use this after §6, or at any time the owner wants a complete health check.
Every step here only **reads**. It never starts searches, downloads,
publication, Plex refreshes or scans, and never changes files or rows.

> **Owner: how to start it.** In Claude Code on the server:
>
> *"Read AGENTS.md and docs/operations/owner-console-rollout.md §11. Do the
> full debugging pass, change nothing, and give me the report."*

### 11.1 Setup

```sh
cd <media-manager checkout>
PORT=$(grep -E '^MEDIA_MANAGER_API_PORT=' .env | cut -d= -f2-); PORT=${PORT:-28081}
TOKEN=$(grep -E '^OWNER_TOKEN=' .env | cut -d= -f2-)
mm() { curl -fsS -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:$PORT$1"; }
pp() { python3 -m json.tool 2>/dev/null || cat; }   # pretty-print when python3 exists
```

Never echo `$TOKEN`. Run `unset TOKEN` when finished. Do **not** call
`/api/v1/discovery/*` in this pass: those routes search Seerr and TMDB. Every
route below reads only Media Manager's database.

### 11.2 Checklist

| # | Area | Read with | Healthy | Red flags |
| --- | --- | --- | --- | --- |
| 1 | Containers | `docker compose ps`; `docker stats --no-stream` | `api`, `ui` and `postgres` up and healthy; modest CPU and memory | restarting, unhealthy, a memory climb |
| 2 | Crashes and errors | `docker compose logs --since 24h api \| grep -iE 'panic\|fatal\|outbox batch failed\|error' \| sort \| uniq -c \| sort -rn \| head -30` | nothing, or a few explained lines | any `panic`; repeating `outbox batch failed` |
| 3 | Version and schema | `docker compose exec -T api media-manager version`; `mm /health/ready`; `mm /api/v1/system/health \| pp` | the expected commit; `schema_version` = `expected_schema` for that build (currently 43) | a mismatch or `not_ready` |
| 4 | Worker queue | `mm /api/v1/system/health \| pp` (outbox); `mm /api/v1/system/tasks \| pp` | `due` near 0, `retrying` small and not growing | `retrying` growing between two reads 10 minutes apart; one event type with high `attempts` |
| 5 | Integrations | `mm /api/v1/system/health \| pp` (components); `mm /api/v1/settings/integrations \| pp` (secret **references** only) | every enabled component configured, `consecutive_failure` 0 | failures on Seerr, AltMount, Plex or an indexer; an enabled integration that is not configured |
| 6 | Indexers | `mm /api/v1/sources \| pp` | a recent successful `last_outcome`, no long cooldown | `auth_failed`, `system_failure`, long cooldowns. Pacing deferrals are normal (runbook §9) |
| 7 | Workflows needing the owner | `mm '/api/v1/workflows?limit=300&active=true' \| pp` | few or no `needs_attention`, `quarantined`, `blocked` or `transport_unknown` | many at once; the same `attention_code` repeated (one root cause) |
| 8 | Retained or failed output | `mm /api/v1/acquisition-verifications \| pp` | empty or small, each explained | many retained outputs with the same failure |
| 9 | Plex confirmation | workflows from row 7 with `attention_code` `plex_timeout` | none | many: check library paths and that Plex can resolve symlink targets (runbook §1) |
| 10 | Seerr intake | `mm '/api/v1/requests?limit=300' \| pp` | recent requests progressing; `intake_error` empty | `needs_configuration` (a missing default root or quality profile); repeated `intake_error`; nothing new for days while Seerr has new requests (webhook, §10) |
| 11 | Library configuration | `mm /api/v1/settings/library-roots \| pp`; `mm /api/v1/settings/quality-profiles \| pp` | one default root per media kind; enabled profiles | no default root or profile (causes `needs_configuration`) |
| 12 | Mounts and space | `df -h` on the AltMount, movies, TV and backups host paths; `docker compose exec -T api ls /mnt/altmount /libraries/movies /libraries/tv \| head` (if the image has `ls`) | readable mounts, free space | full disks, `Permission denied`, empty AltMount |
| 13 | Notifications and backups | `mm '/api/v1/notifications/deliveries?limit=300' \| pp`; `mm /api/v1/backups \| pp` | deliveries succeeding; a recent `verified` backup if backups are configured | failing deliveries; no backup ever verified |
| 14 | Recent history | `mm '/api/v1/activity?limit=300' \| pp` | a steady flow of transitions | a burst of the same failure; nothing at all for a long time |
| 15 | Website | open the public address in a browser; browser DevTools console | no red errors; pages refresh on their own; posters on titles with metadata | console errors; "Media Manager returned HTTP 404" (runbook §9) |

Symlink targets are container paths (`/mnt/altmount/...`), so symlinks look
broken when inspected from the host. Judge them only from inside the `api`
container or from Plex.

### 11.3 Report format

Give the owner one table, worst first:

| Area | Status (OK / Watch / Problem) | Evidence (counts, codes, times; never secrets) | Likely cause | Suggested next step |
| --- | --- | --- | --- | --- |

Then:

- **Nothing is changed during the pass.** For each Problem, say what the fix
  would be and whether it is:
  - an owner action in the UI;
  - a configuration change (§12.2);
  - a code change (§12.3).
- Offer to fix the Problems in order, worst first, and wait for the owner's
  go-ahead on each. Live actions (searches, approvals, Plex re-checks) are the
  owner's to trigger.
- Record anything learned about this server (paths, recurring issues, what was
  fixed) in `CLAUDE.local.md`, so the next session starts from it.

## 12. Fixing problems on the server

The server session is expected to fix problems, not only report them, in a
way that keeps production safe and every change reviewable and reversible.
Every fix starts the same way: find the root cause (read-only, as in §11), then
explain it to the owner with the evidence, the proposed fix, and its risk, and
wait for a go-ahead.

### 12.1 Which kind of fix

| Kind | Examples | How |
| --- | --- | --- |
| Owner action | Approve a request, open a new acquisition round, re-check Plex, fix a Plex match | Tell the owner exactly what to click. The owner performs live actions. |
| Configuration | A missing key in `.env.integrations`, a wrong path, a missing library root or default profile | §12.2 |
| Code | A crash, wrong behavior, a wrong label | §12.3 |
| Emergency | The site or API is down after a deploy and no quick fix is clear | Roll back (§8), then fix with §12.3 |

Never:
- edit database rows by hand (runbook §9 lists the supported endpoints);
- disable or skip a test to get green;
- weaken Newznab pacing, redirect pinning, header stripping, or secret
  sanitization (`AGENTS.md`);
- commit or print secrets.

### 12.2 Configuration fixes

1. Back up first:
   `cp .env .env.bak-$(date +%F-%H%M); cp .env.integrations .env.integrations.bak-$(date +%F-%H%M); chmod 600 .env*.bak-*`
2. Change only the named setting. Refer to it by name in chat, never by value.
   Ask the owner to paste a new secret straight into the file, not into the chat.
3. Apply it:
   - for `.env.integrations` or API settings,
     `docker compose up -d --force-recreate api` (`restart` does not reload env
     files);
   - for `compose.yaml` or `.env` path changes, `docker compose up -d`.
4. Verify with §6.1 and the relevant §11 rows. If it made things worse,
   restore the `.bak` files and apply again.

Settings stored in Media Manager itself (integrations, library roots, quality
profiles) are changed through the website or the owner API, never in the
database.

### 12.3 Code fixes

**One-time setup** (with the owner's OK):
- **GitHub access,** so the session can push a branch and open a pull request.
  Either install the GitHub CLI and run `gh auth login` (the owner completes the
  browser step), or use an SSH key with push access.
  - Check with `git ls-remote "$GH" HEAD` (`$GH` as in §2).
- **Commit identity:** set `git config user.name` and `git config user.email`
  to the owner's choice, in this repository only.
- **Docker only:** nothing else needs installing. The tests below run in the
  same public images CI uses.

**Workflow:**

1. **Separate fix folder.** The live checkout is never edited:
   ```sh
   GH=$(git remote -v | awk '$2 ~ /github\.com[:\/]Media-manager52\/Media-manager(\.git)?$/ && $3 == "(fetch)" {print $1; exit}')
   : "${GH:?no git remote points to GitHub; see §2}"
   git fetch "$GH" main
   git worktree add ../media-manager-fix -b fix/<short-name> "$GH"/main
   cd ../media-manager-fix
   ```
2. **Smallest change that fixes the root cause, plus a test that fails
   without it.** Run the test once against the unfixed code to prove it catches
   the problem (`AGENTS.md`: focused tests for behavior changes). Update the
   docs and `CHANGELOG.md` when behavior changes.
3. **Run the checks locally in Docker** from the fix folder. They were verified
   to pass on `main` exactly as written.
   - Go (about 6 minutes the first time, mostly downloads):
     ```sh
     docker network create mm-fix-net 2>/dev/null || true
     docker run -d --rm --name mm-fix-pg --network mm-fix-net -e POSTGRES_PASSWORD=test -e POSTGRES_DB=media_manager_test postgres:18.6-alpine3.24
     until docker exec mm-fix-pg pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done
     docker run --rm --network mm-fix-net --user "$(id -u):$(id -g)" -e HOME=/tmp -e GOCACHE=/tmp/gocache -e GOPATH=/tmp/gopath -e GOFLAGS=-mod=readonly \
       -e MEDIA_MANAGER_TEST_DATABASE_URL='postgres://postgres:test@mm-fix-pg:5432/media_manager_test?sslmode=disable' \
       -v "$PWD":/src -w /src golang:1.27 \
       sh -c 'test -z "$(gofmt -l cmd internal)" && go vet ./cmd/... ./internal/... && go test -count=1 -race ./cmd/... ./internal/...'
     docker stop mm-fix-pg
     ```
   - Website (about 1 minute):
     ```sh
     docker run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp -v "$PWD":/src -w /src node:24 \
       sh -c 'npm ci --no-audit --no-fund && npm run lint && npm run typecheck && npm run docs:check && npm test'
     ```
     "WebSocket server error: Port 24678 is already in use" is a known,
     harmless warning.
   - Browser suite (about 4 minutes):
     ```sh
     docker run --rm --ipc=host --user "$(id -u):$(id -g)" -e HOME=/tmp -v "$PWD":/src -w /src mcr.microsoft.com/playwright:v1.62.1-noble \
       sh -c 'npm ci --no-audit --no-fund && npm run test:browser'
     ```
   Run the ones that match what changed; CI runs all of them anyway.
4. **Commit and open a pull request.** Use one clear commit per fix. Then:
   - `git push -u "$GH" fix/<short-name>`;
   - `gh pr create --fill`, or open it on GitHub.

   CI then runs the full suite; watch it with `gh pr checks --watch`. If CI
   fails, find the cause, fix it and push again. Never skip a test.
5. **Merge with the owner's OK** once CI is green:
   `gh pr merge --squash --delete-branch`.
6. **Deploy the merged fix.** This is the normal procedure:
   - back up as in §4;
   - in the live checkout, `git pull --ff-only "$GH" main` (`$GH` as in §2);
   - then §5 (build and up) and §6.1 (verify).

   If it makes things worse, roll back (§8).
7. **Clean up.**
   - `cd` back to the live checkout;
   - `git worktree remove ../media-manager-fix`;
   - note the problem, cause, fix, pull request and deploy time in
     `CLAUDE.local.md`.

### 12.4 Keeping the server current

To update to a newer `main` later, run the same sequence:
- §3 pre-flight, including §3.5 for the commits, migrations and configuration
  that come in;
- §4 back up;
- §5 deploy;
- §6 verify;
- §11 debugging pass.

