# 04 — HTTP API reference

Purpose: the complete route surface, authentication model, envelopes, and the
write-path protections you must preserve. Source: `internal/httpapi/server.go`
and `internal/httpapi/commissioning_v2.go`.

## 1. Authentication model

| Surface | How it is authenticated |
| --- | --- |
| `/health/live`, `/health/ready` | No bearer check inside the API. Publicly gated by Cosmos owner login; loopback-restricted in the deployment. |
| Every `/api/v1/*` owner route except the Seerr webhook | `Authorization: Bearer <OWNER_TOKEN>`. Handlers call `requireOwner` (constant-time SHA-256 comparison). Missing/wrong token → `401` `{ "error": { "code": "unauthorized" } }`. |
| Commissioning v2 mutation routes | `requireOwnerMutation`: owner token **plus** `Origin` exactly equal to `COMMISSIONING_V2_OWNER_ORIGIN`, `Sec-Fetch-Site` `same-origin`/`none` (when present), and `Content-Type: application/json`. Unconfigured → `503 not_configured`; bad origin → `403 origin_rejected`. |
| `POST /api/v1/integrations/seerr/webhook` | `Authorization: Bearer <SEERR_WEBHOOK_SECRET>` (constant-time, ≥32 bytes). Seerr posts directly over the internal Docker network. |

How the browser gets the owner token: it never does. Cosmos Cloud's `/api`
route injects the header server-side. The UI issues same-origin `fetch()` calls
with `credentials: "same-origin"` and no authorization header.
See [07-operations.md](../operations/runbook.md#public-edge-cosmos-cloud).

Every response goes through `security()`:
`X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`,
`Referrer-Policy: no-referrer`, `Cache-Control: no-store`,
`Content-Security-Policy: default-src 'none'; frame-ancestors 'none'`.

## 2. Envelopes and errors

Success:

```json
{ "data": <payload>, "meta": <optional> }
```

Error (`writeError`):

```json
{ "error": { "code": "snake_case_code", "message": "human-readable" } }
```

Notes:

- JSON request bodies are decoded with `DisallowUnknownFields`; unknown fields
  → `400`.
- Bodies are size-limited per route (for example webhook 256 KiB).
- `204 No Content` is used for idempotent duplicates (Seerr webhook, some
  deletes). The UI API client (`lib/media-manager-api.ts`) tolerates 204.
- Status codes: `200` reads/actions, `201`/`202` accepted, `400` bad input,
  `401`/`403` auth, `404` not found, `409` conflicts (`concurrent_modification`),
  `422` semantic validation, `503` not configured/dependency unavailable.

## 3. Routes

### Health

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/health/live` | Process liveness (`{"status":"live"}`-style payload). |
| GET | `/health/ready` | Readiness incl. DB ping and schema version comparison. |

### Dashboard, activity, files

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/dashboard` | Counts + recent workflows + recent activity. |
| GET | `/api/v1/activity` | Merged audit + workflow transition timeline (`limit`). |
| GET | `/api/v1/files` | Managed file read model (link generations + adopted files). |
| GET | `/api/v1/files/{id}` | One managed file with provenance/evidence. |

### Media catalog and details

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/media` | Bounded catalog pages (`limit`/`offset`; client caps at 200×50). |
| POST | `/api/v1/media` | Create media + workflow(s) transactionally; optional immediate search. |
| GET | `/api/v1/media/{id}` | Media item incl. identities/metadata. |
| GET | `/api/v1/media/{id}/detail` | Media + workflows + seasons + files. |
| POST | `/api/v1/media/{id}/metadata/refresh` | Owner-triggered exact-ID enrichment. |
| POST | `/api/v1/media/{id}/workflows` | Create another scope workflow for existing media. |
| DELETE | `/api/v1/media/{id}` | Recycle/archive a media item (safe delete path). |
| POST | `/api/v1/media/{id}/archive` | Safe archive operation for unwanted media. |

### Workflows

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/workflows` | Bounded list (`limit`, `active`). |
| GET | `/api/v1/workflows/{id}` | One workflow + transitions. |
| POST | `/api/v1/workflows/{id}/search` | Queue a targeted search; may require `adopted_replacement_confirmation`. |
| POST | `/api/v1/workflows/{id}/acquisition-rounds` | Owner opens the next fixed two-slot round. |
| POST | `/api/v1/workflows/{id}/recover-altmount-submission` | Reconcile an unknown submission outcome. |
| POST | `/api/v1/workflows/{id}/reconcile-altmount` | Force a safe queue reconciliation. |
| POST | `/api/v1/workflows/{id}/reverify-retained-output` | Re-inspect one retained output (`job_id`), no AltMount call. |
| POST | `/api/v1/workflows/{id}/reconfirm-plex` | Retry exact Plex confirmation after `plex_timeout` from the attempt's existing verified links (idempotent, audited, ≤3 retries per publication; `409` if not timed out, links not verified, or limit reached). |
| POST | `/api/v1/workflows/{id}/cancel` | Cancel workflow (no fake success). |

### Release intelligence and verification

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/intelligence/releases` | Persisted release candidates + verdicts/scores. |
| GET | `/api/v1/acquisition-verifications` | Subtitle/output verification review rows. |
| GET | `/api/v1/blocklist` | Owner blocklist. |
| POST | `/api/v1/blocklist` | Create block from a persisted candidate (`native_release` or `pack_release`, retention 30/90/manual). |
| DELETE | `/api/v1/blocklist/{id}` | Remove block. |

### Wanted and requests

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/wanted` | Missing/upgrade gaps derived from workflows, links, adopted files. |
| GET | `/api/v1/requests` | Persisted Seerr requests + lifecycle. |
| GET | `/api/v1/requests/{id}` | Request detail + workflows/seasons/files. |

### Sources

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/sources` | Integration list with pacing/commission/outcome state; no network probe. |

### Discovery (metadata)

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/discovery/search` | Owner-triggered Seerr metadata search (`query`, `page`). |
| GET | `/api/v1/discovery/{kind}/{id}` | Exact detail lookup; `kind` = `movie`\|`tv`; optional `season`. Enriches through configured direct providers. |

### Adoption (import existing library)

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/adoption` | Bounded batch list. |
| POST | `/api/v1/adoption` | Start a read-only scan of a root. |
| GET | `/api/v1/adoption/{id}` | Batch + items. |
| PATCH | `/api/v1/adoption/{id}/items/{itemID}` | Approve/reject/remap one item (exact-season scope for TV). |
| POST | `/api/v1/adoption/{id}/approve` | Lock the reviewed plan. |
| POST | `/api/v1/adoption/{id}/execute` | Execute with exact confirmation phrase (+ replacement phrase when required); max 25 items. |

### Collections, calendar

| Method | Path | Purpose |
| --- | --- | --- |
| GET/POST | `/api/v1/collections` | Manual collections. |
| POST | `/api/v1/collections/preview` | Server-side exact-member preview before creation. |
| DELETE | `/api/v1/collections/{id}` | Delete collection (never touches media/files). |
| GET | `/api/v1/calendar` | Dated episodes with monitoring/workflow state. |

### Recycle (retention)

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/recycle` | Retention entries (link + real file cycles). |
| POST | `/api/v1/recycle/{id}/restore` | Restore a managed link. |
| POST | `/api/v1/recycle/{id}/restore-adopted` | Plex-gated whole-cycle adopted rollback (`confirmation`). |
| POST | `/api/v1/recycle/{id}/confirm-purge` | Second exact owner confirmation to purge a retained real file after 30 days. |

### Settings: integrations, roots, profiles

| Method | Path | Purpose |
| --- | --- | --- |
| GET/POST | `/api/v1/settings/integrations` | List/create Plex/Seerr/AltMount/Newznab instances (disabled staging by default). |
| POST | `/api/v1/settings/integrations/{id}/commission` | Paced CAPS + negative query; enable only on success. |
| GET | `/api/v1/settings/integrations/{id}/seerr-routes` | Configured Seerr root-folder routes plus every folder observed on persisted Seerr requests. |
| POST | `/api/v1/settings/integrations/{id}/seerr-routes` | Create/replace a route (`media_kind`, `root_folder`, `library_root_id`, optional `quality_profile_id`); requeues paused Seerr requests. |
| DELETE | `/api/v1/settings/integrations/{id}/seerr-routes/{route}` | Remove a route; later requests for that folder pause for configuration. |
| GET/POST | `/api/v1/settings/library-roots` | Movie/TV roots with path mappings/defaults. |
| GET/POST | `/api/v1/settings/quality-profiles` | List/create policy profiles. |
| PATCH | `/api/v1/settings/quality-profiles/{id}` | Edit policy (bumps policy version / canonicalizes subtitle goals). Existing rounds keep their snapshot. |
| POST | `/api/v1/settings/quality-profiles/{id}/apply-policy` | Move the profile's non-terminal workflows onto the current policy version (new rounds; per-workflow outcomes). Idempotent; `409` asks for a retry if workflows changed concurrently. See [workflows §5a](workflows.md#5a-changing-a-quality-profiles-policy). |

### Settings: commissioning v2 (owner-authorized)

All mutation routes below require `requireOwnerMutation`.

| Method | Path | Purpose |
| --- | --- | --- |
| POST | `/api/v1/settings/integrations/{id}/commissioning-v2/runs` | Create an inert run (fixtures optional). |
| GET | `.../runs/{run}` | Read run state. |
| POST | `.../runs/{run}/authorize-caps` | Owner authorizes the single CAPS call. |
| POST | `.../runs/{run}/categories` | Seal category/fallback plan. |
| POST | `.../runs/{run}/authorize-searches` | Owner authorizes bounded search calls. |
| POST | `.../runs/{run}/steps/{step}/execute` | Execute exactly one authorized step. |
| POST | `.../runs/{run}/draft` | Finalize capability-profile draft. |
| GET | `.../runs/{run}/draft` | Read the draft profile. |
| POST | `.../runs/{run}/activate` | Activate the profile for runtime search. |

### Notifications

| Method | Path | Purpose |
| --- | --- | --- |
| GET/POST | `/api/v1/settings/notifications` | Destinations + event subscriptions (HTTPS by default, env-referenced secret). |
| PATCH | `/api/v1/settings/notifications/{id}` | Enable/disable. |
| GET | `/api/v1/notifications/deliveries` | Durable delivery history (attempts/status/errors). |

### Import lists

| Method | Path | Purpose |
| --- | --- | --- |
| GET/POST | `/api/v1/import-lists` | IMDb export lists. |
| GET | `/api/v1/import-lists/{id}` | One list + latest snapshot. |
| POST | `/api/v1/import-lists/{id}/snapshots` | Upload CSV snapshot (review only; creates no media). |

### Prowlarr backup import

| Method | Path | Purpose |
| --- | --- | --- |
| POST | `/api/v1/imports/prowlarr` | Upload backup ZIP; parse to disabled candidates. |
| GET | `/api/v1/imports/prowlarr/{id}` | Import + candidates. |
| POST | `/api/v1/imports/prowlarr/{id}/approve` | Approve candidates with `secret_ref` names. |

### System and backups

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/system/health` | DB/schema/outbox/component status; no synthetic success. |
| GET | `/api/v1/system/tasks` | Per-event-type outbox aggregates + recurring schedules. |
| GET | `/api/v1/system/info` | Version, auth boundaries, provider states, contracts. |
| GET/POST | `/api/v1/backups` | Backup overview / queue encrypted backup. |
| GET | `/api/v1/backups/{id}/download` | Checksum-matching encrypted download only. |

### Radarr/Sonarr-compatible API (Seerr *arr replacement)

Media Manager serves the subset of the Radarr and Sonarr APIs that Seerr uses
so the *arr applications are not part of the stack. Authentication is a
dedicated key: `X-Api-Key: <SERVARR_API_KEY>` or `?apikey=<SERVARR_API_KEY>`.
An empty `SERVARR_API_KEY` disables the surface (`503 not_configured`).

| Method | Path (Radarr flavor) | Purpose |
| --- | --- | --- |
| GET | `/radarr/api/v3/system/status` | Server identity/version for Seerr's settings test (Radarr 5.x). |
| GET | `/radarr/api/v3/qualityProfile` | Enabled movie quality profiles with synthetic integer IDs. |
| GET | `/radarr/api/v3/rootfolder` | The single synthetic Auto root `/srv/media/Auto` when an enabled, Plex-backed movie root exists, otherwise `[]`. Media Manager chooses the real root from metadata ([workflows §3.2](workflows.md#32-seerr-intake-webhook--polling)). |
| GET | `/radarr/api/v3/tag` | Empty list. |
| GET | `/radarr/api/v3/movie` | Movie catalog with `hasFile` from Plex-confirmed publications. |
| GET | `/radarr/api/v3/movie?tmdbId=` | One movie or `[]`. |
| GET | `/radarr/api/v3/movie/lookup?term=tmdb:N` | Lookup used by Seerr's add flow. |
| GET | `/radarr/api/v3/movie/{id}` | Movie by synthetic ID. |
| POST | `/radarr/api/v3/movie` | Acknowledge an add; Seerr's request drives intake. |
| PUT | `/radarr/api/v3/movie` | Update acknowledgement. |
| DELETE | `/radarr/api/v3/movie/{id}` | Removal acknowledgement. |
| POST | `/radarr/api/v3/command` | Search-command acknowledgement. |
| GET | `/radarr/api/v3/queue` | Empty queue page. |

| Method | Path (Sonarr flavor) | Purpose |
| --- | --- | --- |
| GET | `/sonarr/api/v3/system/status` | Server identity/version (Sonarr 4.x so Seerr skips removed language profiles). |
| GET | `/sonarr/api/v3/qualityProfile` | Enabled series quality profiles. |
| GET | `/sonarr/api/v3/rootfolder` | The single synthetic Auto root, as for Radarr. |
| GET | `/sonarr/api/v3/languageprofile` | English language profile. |
| GET | `/sonarr/api/v3/series` | Series catalog with per-season `episodeFileCount`/`totalEpisodeCount`. |
| GET | `/sonarr/api/v3/series?tvdbId=` | One series or `[]`. |
| GET | `/sonarr/api/v3/series/lookup?term=tvdb:N` | Lookup used by Seerr's add flow. |
| GET | `/sonarr/api/v3/series/{id}` | Series by synthetic ID. |
| POST/PUT | `/sonarr/api/v3/series` | Acknowledge add/update. |
| DELETE | `/sonarr/api/v3/series/{id}` | Removal acknowledgement. |
| GET | `/sonarr/api/v3/episode` | Empty episode list. |
| PUT | `/sonarr/api/v3/episode/monitor` | Monitoring acknowledgement. |
| POST | `/sonarr/api/v3/command` | Search-command acknowledgement. |
| GET | `/sonarr/api/v3/queue` | Empty queue page. |

These routes are reachable only on the API container (for example
`http://media-manager-api-1:8080/radarr`); Seerr's service configuration uses
that address directly over the integrations network.

### Seerr webhook

`POST /api/v1/integrations/seerr/webhook` — not owner-authenticated; see §1.

- Required header: `Authorization: Bearer <SEERR_WEBHOOK_SECRET>`.
- Optional header: `X-Seerr-Event-ID` (dedup).
- Body ≤ 256 KiB strict JSON (`seerr.ParseWebhook`).
- Statuses: `503 not_configured` when Seerr is not configured (CI asserts this),
  `401 unauthorized`, `422 invalid_event`, `204` duplicate, `202 accepted`.
- A receipt row is inserted first (`RecordSeerrDelivery`); a `seerr.reconcile`
  event is queued once per new request.

## 4. Route-changing checklist

1. Decide whether it is read or mutation. Mutations must be owner-only and
   idempotent where possible.
2. Register it in `server.routes()`.
3. Keep the handler thin: parse/validate → call a store method or queue an
   event → `writeJSON`. Do not put domain logic in the handler.
4. Do not return 200 for an operation that only queued work unless the UI
   understands "accepted"; use `202` when appropriate.
5. Add request validation tests in `internal/httpapi/*_test.go` (there are
   existing fakes/in-memory patterns).
6. Update [04-api.md](api.md) (this file) and the UI client if it is used by
   the web app.
