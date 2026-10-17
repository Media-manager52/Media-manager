# 07 — Operations guide

Purpose: deploy, configure, verify, back up, restore, and troubleshoot the
supported production architecture. Product contract: `README.md`. This document
is the operational runbook.

## 1. Supported deployment

Docker Compose with three services (`compose.yaml`):

| Service | Image/build | Networks | Purpose |
| --- | --- | --- | --- |
| `postgres` | `postgres:18.6-alpine3.24` | `backend` | Durable state. |
| `api` | Dockerfile target `api` (distroless-style alpine, non-root UID/GID) | `backend`, `frontend`, `integrations` | Go API + outbox worker + ticker. |
| `ui` | Dockerfile target `ui` (Node 24, Vinext) | `frontend` | Owner web UI. |

The API container is hardened: `cap_drop: ALL`, `no-new-privileges`, runs as
`MEDIA_MANAGER_UID:MEDIA_MANAGER_GID` (default 65532). Networks `frontend` and
`backend` are internal; only the API also joins the external `integrations`
network where AltMount/Seerr/Plex reach it.

Mounts (container → purpose):

| Container path | Mode | Purpose |
| --- | --- | --- |
| `/mnt` | `ro`, `rslave` | Host `ALTMOUNT_HOST_PARENT_PATH`, the directory containing AltMount's FUSE mount point (`/mnt/altmount`). Binding the parent, not the mount point, lets an AltMount restart or FUSE remount propagate in; a bind of the mount point itself stays dead ("socket not connected") until the API is recreated. Set AltMount's `fuse.read_only: true` so the propagated mount is read-only too. |
| `/libraries/movies` | `rw` | Movie symlink library. |
| `/libraries/tv` | `rw` | TV symlink library. |
| `/srv/media` | `rw` | Optional parent of additional Plex library folders (Seerr root-folder routing); set `MEDIA_LIBRARY_HOST_PATH`/`MEDIA_LIBRARY_GID`. |
| `/var/lib/media-manager/backups` | `rw` | `data/backups` on the host. |

Plex must see `/libraries/movies` and `/libraries/tv` read-only **and** be able
to resolve symlink targets (mount the same AltMount host folder read-only at
each root's `source_visible_root`).

## 2. First-run checklist

1. `cp .env.example .env`, `cp .env.integrations.example .env.integrations`;
   replace every placeholder. `POSTGRES_PASSWORD` is required; `OWNER_TOKEN`
   must be ≥32 random characters.
2. Point `ALTMOUNT_HOST_PARENT_PATH` (parent of the AltMount FUSE mount point),
   `MOVIES_HOST_PATH`, `TV_HOST_PATH` at real host
   paths and set `MEDIA_MANAGER_UID`/`GID` to an account that can read AltMount
   and write both library roots (`id -u`, `id -g`).
3. Create the shared network and attach AltMount once:
   ```sh
   docker network create media-manager        # or set MEDIA_NETWORK
   docker network connect media-manager altmount
   ```
4. `docker compose up -d --build`
5. Configure Cosmos Cloud routing (see §3) and open the public HTTPS address.
6. Verify with the smoke scripts (§6).

### Public edge: Cosmos Cloud

Cosmos is the only public edge and the owner auth layer. The token never reaches
the browser. Routes (Cosmos registers shared-host routes in **reverse** order,
so the catch-all UI route must be last in the list):

| Path prefix | Target | Cosmos auth | Injected header |
| --- | --- | --- | --- |
| `/api` | `http://media-manager-api-1:8080` | owner login | `Authorization: Bearer <OWNER_TOKEN>` |
| `/health` | `http://media-manager-api-1:8080` | owner login | none |
| `/` (catch-all) | `http://media-manager-ui-1:3000` | owner login | none |

The `/api` route's `ExtraHeaders` overwrites any inbound `Authorization`, so the
browser can neither supply nor read the token. A route pointed only at the UI
container answers `/api/v1/*` with a bare 404 ("Media Manager returned HTTP
404") — that is the symptom of a misrouted `/api` prefix.

`COMMISSIONING_V2_OWNER_ORIGIN` must equal the browser origin that may call
commissioning-v2 mutations (e.g. `https://media-manager.example.com`, or
`http://127.0.0.1:28081` for loopback acceptance). If empty, those mutations are
disabled with `503 not_configured`.

## 3. Environment variables

Runtime variables in `.env` (Compose interpolation):

| Variable | Required | Meaning |
| --- | --- | --- |
| `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD` | yes (password) | Database credentials. Compose passes discrete `PG*` vars so URL escaping is unnecessary. |
| `MEDIA_MANAGER_VERSION`, `MEDIA_MANAGER_COMMIT` | recommended | Embedded in the binary and reported by `/api/v1/system/info` and `version`. |
| `ACQUISITION_ATTEMPTS_PER_ROUND` | no (2) | Distinct submissions per newly opened acquisition round, integer 1–6. Existing round snapshots remain unchanged. |
| `ACQUISITION_RETAINED_OUTPUT_LIMIT` | no (4) | Retained failed-output jobs per workflow before owner review, integer 1–12. Raising it never deletes failures or queues work. |
| `OWNER_TOKEN` | yes, ≥32 bytes | Owner bearer. Cosmos injects it; loopback clients use it. |
| `MEDIA_MANAGER_API_PORT` | no (28081) | Loopback port for direct API. Never expose. |
| `COMMISSIONING_V2_OWNER_ORIGIN` | for v2 UI | Trusted `Origin` for commissioning-v2 mutations. |
| `MEDIA_NETWORK` | no (`media-manager`) | External integrations network name. |
| `MEDIA_MANAGER_UID`, `MEDIA_MANAGER_GID` | recommended | API container user (library permissions). |
| `ALTMOUNT_HOST_PARENT_PATH` | no (`/mnt`) | Host directory containing AltMount's FUSE mount point; mounted read-only at `/mnt` with `rslave` propagation. |
| `MOVIES_HOST_PATH`, `TV_HOST_PATH` | yes | Host paths for the two library mounts. |
| `MEDIA_LIBRARY_HOST_PATH`, `MEDIA_LIBRARY_GID` | optional | Parent of additional Plex library folders and the host group that owns them; used by Seerr root-folder routing. Defaults: `./data` and the primary GID. |
| `METADATA_*_SOURCE_FILE` | if using direct providers | Host files mounted as Docker secrets for TMDB/TVDB/Simkl. |
| `SEERR_BASE_URL`, `SEERR_API_KEY`, `SEERR_INSTANCE_ID`, `SEERR_WEBHOOK_SECRET` | together or not at all | Seerr intake. Secret ≥32 bytes; instance must be an enabled `seerr` integration UUID. |
| `SEERR_ALLOW_HTTP` | no | Allow plain HTTP to Seerr. |
| `SERVARR_API_KEY` | optional | Enables the Radarr/Sonarr-compatible API Seerr uses when the *arr applications are not installed. Empty disables the surface. |

API-only variables (set by Compose, overridable for standalone deploys):

| Variable | Meaning |
| --- | --- |
| `DATABASE_URL` | Full libpq URL; alternative to `PGHOST`/`PGPORT`/`PGUSER`/`PGPASSWORD`/`PGDATABASE`. |
| `AUTO_MIGRATE` | `true` runs embedded migrations at startup. |
| `LISTEN_ADDR` | API bind address (default `0.0.0.0:8080` in-container). |
| `HEALTHCHECK_URL` | Target for `media-manager healthcheck`. |
| `BACKUP_DIRECTORY` | Backup output directory (default `/var/lib/media-manager/backups`). |
| `BACKUP_ENCRYPTION_KEY` / `BACKUP_ENCRYPTION_KEY_FILE` | Base64 32-byte key for encrypted backups. Absent → backup creation disabled in the UI. |
| `<NAME>_FILE` | For any secret `NAME`, a file path whose content overrides the env value. |

`.env.integrations` (loaded only into the API container) contains the values
referenced by `secret_ref` names: `ALTMOUNT_SECRET`, `PLEX_TOKEN`,
`NEWZNAB_*_API_KEY`, and optional `TMDB_ACCESS_TOKEN`/`TVDB_API_KEY`/
`TVDB_SUBSCRIBER_PIN`/`SIMKL_CLIENT_ID`. After changing it:
`docker compose up -d --force-recreate api` (`restart` does **not** reload env
files).

### Single-address indexer egress

Compose disables IPv6 inside the API container through
`net.ipv6.conf.all.disable_ipv6` and `net.ipv6.conf.default.disable_ipv6`.
Provider requests therefore use the host's outbound IPv4 address. Verify the
actual address from the API container after a network or host change; Docker
NAT, upstream routing, or a VPN determines the public address. This setting
does not pin a particular public IPv4 address or change the provider's own
DNS/redirect address validation.

Keep indexer credentials in Media Manager only among active clients. Retire
other Prowlarr, DKSubs, legacy collector, and diagnostic schedules that use the
same keys, including clients on other machines. One server cannot enforce
scheduling or egress for another machine. A single IP does not replace the
provider's request pacing, rolling budgets, or account limits.

Only the API namespace is changed: host IPv6, the public UI, and other
containers keep their existing network settings. After changing the setting,
recreate the API container; a restart does not apply Compose sysctls. Check
readiness and the direct/public-edge smoke scripts after recreation.

## 3a. Configure Seerr without Radarr/Sonarr

When `SERVARR_API_KEY` is set, Media Manager serves the Radarr/Sonarr API that
Seerr needs. In Seerr, edit the existing services (or create new ones):

| Service | Hostname | Port | SSL | Base URL | API key |
| --- | --- | --- | --- | --- | --- |
| Radarr | `media-manager-api-1` | `8080` | off | `/radarr` | `SERVARR_API_KEY` |
| Sonarr | `media-manager-api-1` | `8080` | off | `/sonarr` | `SERVARR_API_KEY` |

Save each service (the Test button calls `system/status`, `qualityProfile`,
`rootfolder`, and `tag`). Seerr sees one root folder, `/srv/media/Auto`; pick
it as the default. Media Manager then routes each request to a themed library
root (`Børn Film`/`Børn Serier`, `Dokumentar`/`Dokumentar Serie`,
`Julefilm`/`Julekalender`, `Danske Film`/`Dansk Tv-Serier`) or the kind's
default root from the title's metadata, and ignores the Seerr profile for Auto
requests (see [workflows §3.2](../architecture/workflows.md#32-seerr-intake-webhook--polling)).
A themed root is matched by its exact name; create it in Settings → Storage
with that name, or its requests go to the default root. The quality-profile
list comes from Media Manager's enabled profiles. No indexers or download clients exist behind these services
— all acquisition stays in Media Manager.

Existing requests that still reference the old server/folder configuration keep
working through the `seerr_library_routes` mappings (for example
`/media/Comedy`); requests that carry a real library `destination_root` (created
before the Auto root) resolve by that path and the profile id directly. A
request for a movie or season that is already Plex-confirmed is attached to the
existing workflow and marked available in Seerr without a new download
(delivery outcome `already_available`). No reconfiguration is needed
for already-stored requests. To retire Radarr/Sonarr completely, remove their
containers after the services above pass in Seerr.

## 4. Upgrade / redeploy procedure

For VPS-built, GitHub-verified image updates, use the owner-triggered
[Deploy workflow](github-vps.md#normal-use). The broker creates a backup and
checks the exact running commit. The procedure below remains available for
reviewed manual source builds.

1. Fetch/tag the commit; set `MEDIA_MANAGER_VERSION`/`_COMMIT`.
2. Back up PostgreSQL and both env files together (§5).
3. `docker compose build` then `docker compose up -d`.
4. Watch migration logs; confirm `/health/ready` reports the expected schema.
5. Run the smoke scripts (§6).
6. For behavior changes, run one complete live movie and one complete live
   season-pack workflow against the configured stack before declaring success.

Rollback: restore the DB dump into a fresh database, check out the previous
source, `media-manager migrate` (migrations are forward-only; restoring the DB
is the supported way back), recreate containers.

## 5. Backup, restore, disaster recovery

Two independent backup mechanisms:

1. **Encrypted application backups** (`/system/backups`, durable worker job):
   `pg_dump -Fc` + chunked AES-256-GCM (`MMBAK001`), checksum + schema version
   recorded, restore-verified into an isolated temporary database, encrypted
   download only on checksum match. Requires `BACKUP_ENCRYPTION_KEY` (base64 32
   bytes). There is **no** production restore automation and no auto-deletion.
2. **Manual dump** (README):
   ```sh
   docker compose exec -T postgres sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > media-manager.dump
   ```

Restore procedure (manual):

1. Stop the API/UI (`docker compose stop api ui`).
2. Restore the dump into a fresh database.
3. Run `docker compose run --rm api migrate`.
4. Verify `/health/ready` schema version.
5. Complete two live workflow checks before serving traffic.

**Always back up `.env` and `.env.integrations` together with the database**;
without them the DB cannot reach integrations. Protect all three as secrets.

## 6. Health checks and smoke tests

| Check | Command |
| --- | --- |
| Liveness | `curl -fsS http://127.0.0.1:28081/health/live` |
| Readiness (DB + schema) | `curl -fsS -H "Authorization: Bearer $OWNER_TOKEN" http://127.0.0.1:28081/health/ready` |
| Version | `docker compose exec -T api media-manager version` |
| Public-edge routing/auth | `scripts/smoke-public-edge.sh https://media-manager.example.com` |
| Owner API read models (loopback) | `scripts/smoke-api-direct.sh` |
| Live read-only browser acceptance | `npm run test:live-browser` |

`smoke-public-edge.sh` fails if `/api/v1/*` reaches the UI container (bare 404)
or the API answers without an auth gate. `smoke-api-direct.sh` resolves the
running containers and reads the owner token from the API container — it never
prints it. `test:live-website.sh` starts the loopback proxy that reproduces the
Cosmos route shape, runs Playwright against every static route at desktop,
tablet, and mobile sizes, blocks all state-changing and non-loopback requests,
and writes results to `.sites-runtime/live-playwright-results/`.

## 7. Logs and task visibility

- Container stdout/stderr carries structured `slog` records (`media manager API
  listening`, `outbox batch failed`, ...).
- Durable task state: `/api/v1/system/tasks` (per-event-type pending/due/retry/
  completed/attempts/latest) and `/system/logs` (persisted audit + workflow
  transitions). Container stdout is **not** the audit log and the UI must not
  present it as such.
- Outbox diagnosis: `pending`/`due`/`retrying` counts in `/api/v1/system/health`.
  A single future non-due event is normal; a growing `retrying` count is not.
- `indexer_operation_status` exposes the latest persisted indexer outcome per
  source without making a live request.

## 8. Resource and safety expectations

- The API enforces one in-flight Newznab request per indexer and a global
  scheduler bound (4). Do not raise these without updating budgets and tests.
- `internal/mediainfo` runs `ffprobe` with a 30s/1MiB cap per file and a
  5-minute total verification window.
- `internal/library` never follows a path outside the configured root; symlink
  creation uses atomic operations.
- The API's HTTP port must remain loopback-only. If you need remote access, use
  the Cosmos public origin.

## 9. Troubleshooting

| Symptom | Likely cause / fix |
| --- | --- |
| UI shows "Media Manager returned HTTP 404" | `/api` prefix is routed to the UI container, not the API. Fix the Cosmos `/api` route (must target the API container). |
| `401 unauthorized` on every UI call | Cosmos `/api` route is not injecting the owner bearer, or `OWNER_TOKEN` differs between the route and container. |
| Seerr webhook returns `503 not_configured` | Seerr env vars incomplete; all four must be set and the instance row enabled. |
| Commissioning v2 returns `503 not_configured` | `COMMISSIONING_V2_OWNER_ORIGIN` unset (or no service). Set it to the browser origin. |
| Commissioning v2 returns `403 origin_rejected` | Origin mismatch or cross-site request. Use the configured origin; don't strip Origin headers. |
| Readiness fails with schema mismatch | Migrations didn't run (`AUTO_MIGRATE` off) or binary/DB mismatch. Run `media-manager migrate`; never edit a schema row manually. |
| Newznab calls return `deferred`/cooldown | Pacing/budget working as intended. Inspect `indexer_operation_status` and `/sources`, not logs. |
| Workflow stuck in `waiting` with `owner_action_required` | Round exhausted; wait for discovery `ready`, then `POST .../acquisition-rounds`. |
| Output retained and unpublished | This is the designed failure behavior. Inspect `/api/v1/acquisition-verifications`, fix the cause, use `reverify-retained-output` once, or open a new round after approval. |
| Health shows the AltMount output mount `unavailable`; worker events retry with `altmount_mount_unavailable` | The API cannot read `/mnt/altmount` (FUSE gone, "socket not connected", or only the bare mount point is visible). Imports and output inspection wait and resume by themselves once the mount is readable; no workflow is failed or release excluded for it. Check AltMount and `ls /mnt/altmount` on the host. If the host mount is fine but the API still cannot read it, the API was started with a bind of the mount point itself instead of its parent (see §1 mounts); recreate it with the current `compose.yaml`. |
| A title stops playing; AltMount health shows it `corrupted` | Usenet articles behind the file disappeared. Media Manager repairs these by itself, both files it published and older library symlinks it can identify through Plex: every 30 minutes it opens a replacement workflow for the movie or episode (see `GET /api/v1/repairs`) and retires the broken link after Plex confirms the replacement. A repair in `needs_attention` explains why it cannot proceed automatically. AltMount's own "Failed to trigger ARR rescan" log lines are expected: its repair needs Radarr/Sonarr. |
| A kids title plays without Danish audio | A library whose Seerr route profile sets **Required audio languages** (Børn Film, Børn Serier, Animation) accepts only dubs. The same 30-minute sweep asks Plex which files there have no audio track in that language and opens upgrades (`GET /api/v1/repairs`, reason `missing_required_audio`): at most 40 search at a time and 10 new ones per sweep, movies first. The old file keeps playing until Plex confirms the dub. An upgrade that finds nothing in 7 days is closed as `not_found` (its waiting workflow is cancelled) and the file is tried again after 60 days. Titles made in that language are `skipped`, because their untagged audio is the original. Only AltMount symlinks are replaced; real files are never removed. |
| Backup creation disabled | `BACKUP_ENCRYPTION_KEY` absent. Provide a base64 32-byte key. |
| Workflows show `quality_policy_changed` | A quality profile was edited after their rounds opened. Call `POST /api/v1/settings/quality-profiles/{id}/apply-policy` and review the per-workflow outcomes; re-run it after `in_flight` work settles. Never retire or insert `acquisition_rounds` rows by hand. |
| Plex never confirms | Read the workflow `attention_detail`: it says whether Plex never showed the path, showed it under a different provider identity (fix the Plex match), or confirmed only after the deadline. Check library paths and that Plex can resolve symlink targets. Never fake confirmation. Localized Plex titles are handled by the adapter's exact-path folder lookup. To retry a `plex_timeout` whose links are already `verified`, call `POST /api/v1/workflows/{id}/reconfirm-plex` (idempotent, audited, at most 3 retries per publication); never insert scan rows by hand. |
| Local test runner reports Vite HMR port `24678` in use | Harmless for production; builds/tests still pass (recorded in release notes). |

## 9a. Replace a publication that fails a newly required property

Use this when a confirmed publication lacks something the profile now
requires (for example Danish subtitles). The sanctioned path is an upgrade
workflow; never delete links or edit rows.

1. Confirm the profile requires the property and has `upgrades_enabled`
   (`GET /api/v1/settings/quality-profiles`).
2. Block releases already proven to fail it, so the new workflow does not
   spend slots on them: `POST /api/v1/blocklist` with each failed
   `candidate_id` and `scope: native_release` (blocks that indexer + GUID). For
   a season, `scope: pack_release` also blocks the exact release title on
   every indexer. Exclusions from the old workflow do not carry over.
3. Create the upgrade workflow: `POST /api/v1/media/{id}/workflows` with the
   same root and profile, `search_immediately: true`, and for a season the
   exact `season` snapshot (number + every expected episode). A season is one
   workflow and one pack: all its episode links are replaced together.
4. Selection: the current release is rejected by exact identity and by exact
   title (cross-posts). When its verified output lacks a now-required
   subtitle language (including no subtitles at all), any eligible candidate
   may replace it; otherwise a candidate must score strictly higher.
5. The old links stay current until the replacement passes output
   verification and exact Plex confirmation; only then are they displaced
   and moved to recycle (`library.displaced_remove_requested`).
6. If no compliant release exists, the truthful outcome is `waiting` (then
   round exhaustion and discovery) while the old publication stays current.

## 10. What not to do operationally

- Do not edit or delete files under `/mnt/altmount` via Media Manager or the
  shell.
- Do not publish the API container port to a LAN/public address.
- Do not run live searches/downloads/publication/Plex refreshes/media scans or
  destructive operations without explicit authorization.
- Do not restore a production database into the live instance as a "test".
- Do not rotate credentials by editing sanitized DB columns; use the provider's
  supported mechanism and the secret references.

## Retrying a release after provider access is repaired

When a credential or NZB download contract is fixed, plain Search/Retry still
honors exact release exclusions and current-round provider avoidance. In
**Workflows**, eligible pre-submission HTTP 401/403 failures offer
**Retry access-rejected release**, naming the selected release and provider.
Confirm only after access has been repaired. Start with one workflow and
check its retrieval, output verification, publication, and exact Plex
confirmation before authorizing more. A successful NZB grab alone is not
proof that the media can be published. If the action is absent or conflicts,
refresh and review the policy, round, blocks, jobs, and retained output;
never clear exclusions through manual database edits.

### Acquisition budget changes

Set both budgets in `.env`, validate Compose, and rebuild/recreate the API.
Invalid or out-of-range values fail startup rather than silently enabling
unbounded retries. `/api/v1/system/info` reports `acquisition_limits`; workflow
rows report their immutable `round_attempt_limit` and the configured
`retained_output_limit`. A balanced owner setting is 4 attempts and 8 retained
outputs. Preserve Danish and all other verification requirements.

For an exhausted workflow, use the owner next-round or from-known action;
configuration does not rewrite the exhausted round or remove exact exclusions.
For `retained_failed_output_limit` with an open round and available slots,
review its failed inventories and use the supported Retry action after raising
the retention budget. Existing infrastructure, episode-snapshot and publication
failures still require their own recovery; more slots cannot repair them.
