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
| `/mnt/altmount` | `ro` | AltMount completed output. |
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
2. Point `ALTMOUNT_HOST_PATH`, `MOVIES_HOST_PATH`, `TV_HOST_PATH` at real host
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
| `OWNER_TOKEN` | yes, ≥32 bytes | Owner bearer. Cosmos injects it; loopback clients use it. |
| `MEDIA_MANAGER_API_PORT` | no (28081) | Loopback port for direct API. Never expose. |
| `COMMISSIONING_V2_OWNER_ORIGIN` | for v2 UI | Trusted `Origin` for commissioning-v2 mutations. |
| `MEDIA_NETWORK` | no (`media-manager`) | External integrations network name. |
| `MEDIA_MANAGER_UID`, `MEDIA_MANAGER_GID` | recommended | API container user (library permissions). |
| `ALTMOUNT_HOST_PATH`, `MOVIES_HOST_PATH`, `TV_HOST_PATH` | yes | Host paths for the three mounts. |
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

## 3a. Configure Seerr without Radarr/Sonarr

When `SERVARR_API_KEY` is set, Media Manager serves the Radarr/Sonarr API that
Seerr needs. In Seerr, edit the existing services (or create new ones):

| Service | Hostname | Port | SSL | Base URL | API key |
| --- | --- | --- | --- | --- | --- |
| Radarr | `media-manager-api-1` | `8080` | off | `/radarr` | `SERVARR_API_KEY` |
| Sonarr | `media-manager-api-1` | `8080` | off | `/sonarr` | `SERVARR_API_KEY` |

Save each service (the Test button calls `system/status`, `qualityProfile`,
`rootfolder`, and `tag`). The root-folder and quality-profile lists come from
Media Manager's own library roots and enabled profiles; pick the defaults in
Seerr if prompted. No indexers or download clients exist behind these services
— all acquisition stays in Media Manager.

Existing requests that still reference the old server/folder configuration keep
working through the `seerr_library_routes` mappings (for example
`/media/Comedy`); requests created through the Media Manager service resolve
by `destination_root` and the profile id directly. No reconfiguration is needed
for already-stored requests. To retire Radarr/Sonarr completely, remove their
containers after the services above pass in Seerr.

## 4. Upgrade / redeploy procedure

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
| Backup creation disabled | `BACKUP_ENCRYPTION_KEY` absent. Provide a base64 32-byte key. |
| Plex never confirms | Read the workflow `attention_detail`: it says whether Plex never showed the path, showed it under a different provider identity (fix the Plex match), or confirmed only after the deadline. Check library paths and that Plex can resolve symlink targets. Never fake confirmation. Localized Plex titles are handled by the adapter's exact-path folder lookup. To retry a `plex_timeout` whose links are already `verified`, call `POST /api/v1/workflows/{id}/reconfirm-plex` (idempotent, audited, at most 3 retries per publication); never insert scan rows by hand. |
| Local test runner reports Vite HMR port `24678` in use | Harmless for production; builds/tests still pass (recorded in release notes). |

## 10. What not to do operationally

- Do not edit or delete files under `/mnt/altmount` via Media Manager or the
  shell.
- Do not publish the API container port to a LAN/public address.
- Do not run live searches/downloads/publication/Plex refreshes/media scans or
  destructive operations without explicit authorization.
- Do not restore a production database into the live instance as a "test".
- Do not rotate credentials by editing sanitized DB columns; use the provider's
  supported mechanism and the secret references.
