# 04 — HTTP API reference

Purpose: the complete route surface, authentication model, envelopes, and the
write-path protections you must preserve. Source of truth for the route list:
`internal/httpapi/api_catalog.go`, which registers every route and generates
the OpenAPI 3.1 document served at `GET /api/v1/openapi.json` (committed copy:
[reference/openapi.json](../reference/openapi.json)). `GET /api/v1` is a short
index for agents; see [AI agent guide](../operations/ai-agents.md). Each
operation is tagged `x-mm-effect` (`read`, `write`, `provider`, `destructive`)
and `x-mm-auth`.

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

OldBoys credential setup uses owner-only GET/PUT
`/api/v1/settings/oldboys/credentials`. GET returns `available`, `configured`,
and optionally `updated_at`; PUT atomically replaces `api_token`, `pass_key`
(PID), and `rss_key` (RID), then returns the same status fields. Credentials
are encrypted with a separate API-only server key and never returned.
PUT requires JSON, rejects cross-site browser requests, bounds input to 16 KiB,
and never echoes malformed input. See [setup](../operations/oldboys.md).

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
| GET | `/api/v1/media/{id}/detail` | Media + workflows + seasons (each with its expected `episodes`: number, title, air date) + files. |
| POST | `/api/v1/media/{id}/metadata/refresh` | Owner-triggered exact-ID enrichment. |
| POST | `/api/v1/media/{id}/workflows` | Create another scope workflow for existing media. |
| DELETE | `/api/v1/media/{id}` | Recycle/archive a media item (safe delete path). |
| POST | `/api/v1/media/{id}/archive` | Safe archive operation for unwanted media. |

### Workflows

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/workflows` | Bounded list (`limit`, `active`). In-flight attempts carry `download_percent`/`download_stage`/`download_observed_at` exactly as AltMount reported them. |
| GET | `/api/v1/workflows/{id}` | One workflow + transitions. |
| POST | `/api/v1/workflows/{id}/search` | Queue a targeted search; may require `adopted_replacement_confirmation`. An open current-policy round retires stale discovery from an older exhausted round. |
| POST | `/api/v1/workflows/{id}/discover` | Owner advances the existing exhausted-round discovery schedule; evidence only, no new round or download slots. Idempotent while the current scan is due or running; unfinished-provider scope and pacing remain intact. |
| POST | `/api/v1/workflows/{id}/acquisition-rounds` | Owner opens the next round with the configured attempt limit (default two). |
| POST | `/api/v1/workflows/{id}/recover-altmount-submission` | Reconcile an unknown submission outcome. |
| POST | `/api/v1/workflows/{id}/reconcile-altmount` | Force a safe queue reconciliation. |
| POST | `/api/v1/workflows/{id}/reverify-retained-output` | Re-inspect one retained output (`job_id`), no AltMount call. |
| POST | `/api/v1/plex/reconfirm-timed-out` | Owner "re-check Plex": runs `reconfirm-plex` for every workflow in `needs_attention`/`plex_timeout` (at most 50 per call) and reports `queued` and `skipped` with reasons. Only exact-path scans; never a library-wide scan. |
| POST | `/api/v1/workflows/{id}/reconfirm-plex` | Retry exact Plex confirmation after `plex_timeout` from the attempt's existing verified links (idempotent, audited, ≤3 retries per publication; `409` if not timed out, links not verified, or limit reached). |
| POST | `/api/v1/workflows/{id}/cancel` | Cancel workflow (no fake success): pre-retrieval work, or `needs_attention` work with no active AltMount job (jobs and retained output stay untouched); otherwise `409 cancel_failed`. |

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
| POST | `/api/v1/requests/{id}/approve` | Owner approves a request that is still pending in Seerr (`409 not_pending` otherwise). Seerr must echo the same request with status approved (`502` if not); the decision is audited (`seerr.request_approved_by_owner`) and a `seerr.reconcile` delivery is queued so normal intake re-reads Seerr. Nothing is admitted locally. |
| POST | `/api/v1/requests/{id}/decline` | Same as approve for a decline (`seerr.request_declined_by_owner`); Seerr may notify the requester. |

Request summaries expose optional `waiting_reason`, `last_search_at` and
`next_search_at`. Search times come from linked persisted search runs and
incomplete outbox events for the current workflow revision or retry-discovery
generation. Stale and completed events do not promise another search. A waiting
workflow with a discovery candidate ready for owner approval gives the request
the `needs_attention` lifecycle; opening another round remains an owner action.

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
| GET/PUT | `/api/v1/settings/oldboys/credentials` | Read status only, or atomically save the API Token/PID/RID encrypted under the separate server key. Does not enable the provider. |
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
| GET | `/api/v1/system/info` | Version, `started_at` (API process start), auth boundaries, provider states, contracts. |
| GET/POST | `/api/v1/backups` | Backup overview / queue encrypted backup. |
| GET | `/api/v1/backups/{id}/download` | Checksum-matching encrypted download; falls back to a verified off-site copy when the local file is unavailable. |
| POST | `/api/v1/backups/{id}/offsite` | Queue a retry of the off-site copy for a verified backup. |

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

## Access rejection recovery

`POST /api/v1/workflows/{id}/retry-access-rejected-release` is owner-only and
requires `{ "candidate_id": "<uuid>", "revision": <current workflow revision> }`.
It returns `202` with `status: retrieval_queued`, `400` for malformed input,
`404` for an unknown workflow, and `409` when the reviewed selection changed
or is no longer eligible. Workflow summaries expose
`access_rejected_candidate_id`, `access_rejected_release_title`, and
`access_rejected_indexer_name` only while the action is eligible.

The transaction retries the exact selected release inside the existing open
round, removes only its pre-submission HTTP 401/403 exclusion, advances the
workflow revision, and queues `release.retrieve_requested`. The original
reason and timestamp are retained in audit event
`workflow.access_rejected_release_retried`. An old worker event or duplicate
owner request cannot act on the new revision. No provider request happens in
the handler; retrieval follows the existing durable pacing and grab budgets.

## Retained movie inspection

`POST /api/v1/workflows/{id}/reinspect-retained-movie` is owner-only and takes
`{ "job_id": "<uuid>" }`. It returns `202` with
`status: movie_reinspection_queued`, `400` for malformed input, `404` for an
unknown or unrelated job, and `409` when recovery is unsafe or already used.
This operational API action is not yet exposed as a UI control.

Only the latest failed movie job in the latest exhausted round is eligible:
its failure must be `movie_primary_ambiguous`, its slot must be the round's
last slot, its frozen inventory must exist, and no subtitle inspection may
already exist. The profile must remain enabled at the round's policy version;
active jobs and existing publications block the action. One audit authorization
per job binds the worker event to the new workflow revision.

The worker uses the original read-only inventory, requires a single main file
matching a saved movie title, and runs the normal bounded subtitle inspection,
publication and exact Plex verification. It creates no NZB request, job or
round. Historical failure evidence, exclusions and retained output remain.

Native OldBoys commissioning is owner-only `POST /api/v1/settings/oldboys/commission`.
It runs four bounded search controls and one discarded, validated NZB; it enables
only the exact saved credential/configuration snapshot. Credential status also
reports `enabled`, never values. See [OldBoys operations](../operations/oldboys.md).
