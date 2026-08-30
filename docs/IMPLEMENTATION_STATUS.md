# Media Manager implementation status

Audited on 2026-08-30 against baseline commit `32ee814`, the live Docker build,
the Go route registry, PostgreSQL migrations/store methods, and the React route
switch. This document describes implemented behavior, not the development
fixture's claims.

## Status definitions

- **Real** — persisted or directly observed behavior is connected end to end and
  does not substitute fixture data or invented success.
- **Partial** — a real path exists, but the approved screen or required lifecycle
  is incomplete.
- **Mock-only** — an approved development concept exists with hard-coded data,
  while the live build truthfully withholds it.
- **Missing** — no usable implementation exists yet.

The application entry is live-only. Approved development concept files remain in
source as visual references, but they are not imported into the Docker UI graph.
Unmapped routes render `LiveUnavailableScreen` and cannot fall through to fixture
screens.

## UI route audit

| Route | Status | Current live behavior | Required work |
| --- | --- | --- | --- |
| `/` | Partial | Loads real catalog, managed-file, workflow, recycle, outbox, and recent durable-activity aggregates in the approved overview composition. | Add wanted/upgrade and component-health summaries as those read models are completed. |
| `/media` | Partial | Lists real catalog records and verified external identities. | Add persisted monitoring, publication, profile/root, and current-quality summaries. |
| `/media/movies/:id` | Partial | Loads the real item, complete bounded workflow history, managed file generations, Plex confirmation, and AltMount provenance; can request a new workflow or recycle it. | Add current policy/candidate comparison and richer safe-upgrade evidence. |
| `/media/series/:id` | Partial | Loads persisted monitoring and exact expected-episode counts per season, complete bounded workflow history, and managed file generations; can create another exact-season workflow. | Add episode-level coverage/evidence and metadata-backed season creation. |
| `/files` | Partial | Lists real link generations, destinations, AltMount source provenance, publication state, and Plex confirmation. | Add track/media-info summaries, companion relationships, and bounded filters. |
| `/files/:id` | Partial | Shows real path relationships, immutable generation, Plex evidence, and displacement/removal timestamps. | Add inventory/media-info evidence and recycle-cycle linkage. |
| `/collections` | Partial | Persists audited manual groupings of real catalog items and requires a server-side exact-member preview before creation; deleting a collection never changes media or files. | Add approved evidence-backed smart rules, rule previews, and membership refresh history. |
| `/calendar` | Partial | Persists exact Seerr episode air dates with immutable season expectations and lists dated episodes with real monitoring/workflow state; missing dates are omitted and dates never trigger search. | Add movie release/digital dates, provider refresh history, owner range controls, and timezone preferences. |
| `/wanted` | Partial | Derives missing versus upgrade gaps from active workflows, current publication by item/scope, and the persisted target profile. | Add independent quality/language/audio/subtitle goal deltas and pagination. |
| `/import` | Partial | Performs a bounded read-only directory-entry scan under a configured library root without following symlinks, persists every video/companion review item, requires an explicit catalog/root/profile/path decision or rejection, and can approve a fully reviewed plan without executing it. | Implement journaled execution, verification, rollback, Plex reconciliation, and separately gated live-action confirmation. |
| `/activity/queue` | Partial | Lists real workflow summaries and queues safe search/cancel transitions. | Add job/attempt detail, phase evidence, per-operation progress, and attention recovery. |
| `/activity/recycle` | Partial | Lists persisted recycle cycles and queues whole-media restore. | Add cycle detail and explicit confirmation UI for real-file purge cases. |
| `/activity/history` | Partial | Merges real workflow transitions and audit events into a bounded durable timeline. | Add correlation, filtering, pagination, and complete audit coverage. |
| `/activity/blocklist` | Real | Creates blocks only from persisted observations, scopes them to an indexer/native GUID or exact media/season/title, supports 30/90-day or manual retention, audits create/remove, and makes future searches persist a rejected blocklist decision. | — |
| `/discover` | Partial | Searches real Seerr metadata on owner action, verifies TMDB/TVDB/IMDb identities with a detail lookup, resolves exact TV episode snapshots per selected season, and creates workflows with root/profile/client overrides. A clearly labeled manual-ID fallback remains. | Add result pagination, richer disambiguation, duplicate preview, and artwork selection. |
| `/intelligence/releases` | Partial | Lists real persisted candidates, observations, policy verdicts/scores, selections, size, and indexer provenance. | Add per-search comparison, complete explanations, and pagination. |
| `/intelligence/groups` | Partial | Aggregates observed group facts from persisted release evidence and explicitly avoids trust labels. | Add time windows, contradiction detail, and server-side aggregation. |
| `/intelligence/evidence` | Partial | Expands real normalized facts with their candidate and policy outcome. | Add raw-field provenance, confidence/source attribution, contradictions, and pagination. |
| `/sources/indexers` | Partial | Lists real Newznab configuration and persisted pacing/outcome state without making a network request. | Add capability snapshots, scoped operation history, coverage, and edit/disable controls. |
| `/sources/indexers/:id` | Partial | Shows real endpoint, secret reference name, commission state, minimum interval, cooldown, failures, and latest outcome. | Add capability detail and commissioning history. |
| `/sources/indexer-lab` | Partial | The live route is a real Prowlarr ZIP review/import flow; approved instances remain disabled pending commissioning. | Separate backup import from the approved paced commissioning diagnostics and remove the route-name mismatch. |
| `/sources/download-clients` | Partial | Exposes real AltMount configuration state and endpoint without probing or claiming synthetic health. | Add capability/path observations, queue summaries, and edit/disable controls. |
| `/sources/import-lists` | Partial | Persists owner-configured IMDb CSV export lists and bounded, digest-deduplicated review snapshots. Rows are classified as pending review, already cataloged, or unsupported; snapshot upload is proven not to create media, workflows, searches, release evidence, or downloads. TV rows are held because exact-season selection is required. | Implement explicit movie monitoring approval with metadata verification, exact-season TV review, repeat-snapshot reconciliation, and only then additional approved providers such as Trakt. |
| `/settings/profiles` | Partial | Lists and creates real quality profiles with one required and one preferred rule. | Implement the full approved rule editor, validation, versioning, cutoff, language/audio/subtitle goals, edit, and disable. |
| `/settings/metadata` | Partial | Reports whether the running Seerr metadata provider is configured, the verified identity namespaces, exact-season snapshot behavior, and the absence of artwork persistence without probing on page load. | Add separate provider lifecycle/status, artwork choices and locks, catalog refresh state, and sidecar output. |
| `/settings/media-management` | Partial | Reports the running read-only AltMount, Plex-safe symlink, Plex-confirmed upgrade gate, and 30-day recycle contracts without pretending unsupported policies are editable. | Add configuration only for narrowly supported lifecycle policies and naming/companion behavior. |
| `/settings/storage` | Partial | Lists and creates real Plex-backed movie/TV roots with defaults and path mappings. | Add preflight validation, edit/disable, naming preview from real values, and mount/capacity checks. |
| `/settings/connections` | Partial | Lists and creates real Plex, Seerr, AltMount, and staged Newznab records; Newznab commissioning is real and paced. | Add safe connection tests and edit/disable/default management; surface operation-level state without fake “ready” labels. |
| `/settings/notifications` | Real | Persists owner webhook destinations with exact event selection, HTTPS-by-default validation, environment-referenced bearer credentials, enable/disable controls, redacted endpoint display, and durable delivery history. Event payloads omit audit detail and secrets; worker retries remain visible. | — |
| `/system/tasks` | Partial | Shows real per-event-type pending, due, retry, completed, attempt, and latest-event aggregates plus configured recurring schedules; opening it triggers nothing. | Persist recurring-run outcomes/durations and add only bounded, explicitly supported manual triggers. |
| `/system/backups` | Partial | Queues durable owner-triggered PostgreSQL custom-format backups, encrypts them with chunked AES-256-GCM using an externally supplied key, records checksums/size/attempts, restores into an isolated temporary database, requires the exact schema version, and serves only checksum-matching encrypted downloads. The live page truthfully disables creation while the key is absent. | Configure the external encryption key, add an explicitly approved retention policy, and design a separately confirmed production-restore plan; no production restore or automatic deletion exists. |
| `/system/security` | Partial | Reports the running owner-only, gateway/browser, internal bearer, Seerr webhook, and no-machine-key boundaries without exposing secret values. | Add secret-reference presence checks, mount-mode verification, outbound-policy reporting, and security-event detail. |
| `/system/api` | Partial | Documents the versioned owner read surface and authentication boundary and explicitly reports that scoped machine keys do not exist. | Add complete generated API exploration; implement scoped key issuance/rotation only when an approved integration requires it. |
| `/system/health` | Partial | Reports real PostgreSQL/schema latency, outbox state, integration configuration, and latest persisted indexer outcomes without synthetic connection success. | Add bounded live operation probes/history for Plex, AltMount, and Seerr. |
| `/system/logs` | Partial | Shows bounded persisted audit writes and workflow transitions; it does not mislabel container stdout as durable audit. | Add redacted diagnostic-log ingestion, correlation, filters, and pagination. |

## Backend capability audit

| Capability | Status | Evidence and gap |
| --- | --- | --- |
| Owner-only browser administration | Real | Caddy Basic Auth protects all owner routes and injects the API owner bearer token; every owner API handler independently checks it. The Seerr webhook is the only explicit exception and has separate authentication. |
| Media Manager and Seerr request intake only | Real | Manual API and authenticated Seerr webhook/poll intake are implemented. Compose and runtime contain no Radarr or Sonarr dependency. |
| Manual movie and exact-season requests | Real | Catalog, identities, root/profile/client overrides, season monitoring, immutable episode expectations, and workflows are transactionally persisted. |
| Metadata discovery and identity verification | Partial | Owner-triggered Seerr search/detail APIs return only movie/TV results, verify TMDB plus available TVDB/IMDb aliases, and fetch exact TV season episodes and air dates before creation. Search pagination, additional providers, artwork, and refresh persistence remain. |
| Seerr reconciliation | Real | Webhook deduplication, five-minute polling, provenance, configuration retry, and revocation reconciliation are persisted. Management/status UI is missing. |
| Newznab commissioning | Partial | A disabled instance is paced at at least three seconds, fetches CAPS, requires generic search, and performs one random negative query before enablement. The approved multi-phase lab and detailed evidence are not implemented. |
| Newznab runtime pacing | Real | Per-indexer single-flight lanes, a global bound, persisted next-allowed/cooldown state, `Retry-After`, and bounded backoff wrap supported indexer requests. |
| Release search/evidence/policy | Partial UI | Search runs, raw observations, normalized evidence, decisions, and selection are persisted and now have bounded read screens. Raw provenance, comparison detail, and pagination remain incomplete. |
| Narrow release blocklist | Real | Owner blocks originate from persisted candidates; native blocks bind indexer plus native GUID and pack blocks bind media plus season variant plus exact title. Matching observations remain visible but are rejected before ranking. |
| AltMount retrieval and reconciliation | Real | Submission intent, unknown-outcome recovery, queue observation, SSE/poll reconciliation, output inventory, and read-only source paths are implemented. |
| Complete TV season packs as one job | Real | One season workflow creates an immutable expectation; one AltMount job is evaluated for exact episode coverage and published only from verified content groups. |
| Plex-safe symlink publication | Real | Paths are containment-checked, names require verified Plex-supported identity, symlinks are batch-published, and Plex confirms every expected path/identity. |
| Safe upgrades | Real invariant, partial product | Existing current links are displaced and removed only inside the transaction triggered after Plex confirms the replacement. Current-quality comparison, cutoff presentation, and owner-facing upgrade evidence remain incomplete. |
| Multiple roots/defaults/per-item override | Real backend, partial UI | Movie/series defaults and explicit request overrides are enforced. UI can create/list but cannot validate, edit, or disable roots. |
| Recycle retention | Real | Managed links enter a 30-day persisted cycle; AltMount source is retained. Restore and due-purge workers exist, with confirmation required for real files. |
| Existing-library adoption review | Partial | Read-only bounded discovery, companion classification, per-item review, compatible catalog/root/profile validation, path containment, batch approval, and audit are real. Plan approval queues no work; journaled execution, verification, rollback, and Plex reconciliation remain missing and must stay separately gated. |
| Prowlarr backup-only import | Real, with a review gap | ZIP/SQLite parsing is bounded and review creates disabled Newznab instances. The owner must manually place extracted keys into the secret environment before approval; import history/listing is absent. There is no Prowlarr runtime connection. |
| Integration/profile/root lifecycle | Partial | Create and list are real. Update, disable/delete, default reassignment, and most safe tests are missing. |
| Audit/history | Partial | Core writes append `audit_events` and workflow transitions and now have a bounded owner read API/UI; not every operation emits an audit event and pagination/filtering are missing. |
| Component health | Partial | Process, PostgreSQL/schema, outbox, integration configuration, and persisted indexer-operation state are real. Bounded Plex/AltMount/Seerr operation status is still missing. |
| Owner notifications | Real | Exact audit-event subscriptions queue durable webhook deliveries; endpoint paths, audit detail, and secret values are excluded from owner responses and delivery payloads. Delivery attempts, bounded errors, response status, and retry state are persisted. |
| Import-list snapshots | Partial | Owner-supplied IMDb exports are parsed with strict identity/year/duplicate bounds and persisted as review-only snapshots. Applying monitoring decisions and external provider synchronization are deliberately absent. |
| Encrypted backups | Partial | Durable backup work, authenticated chunked encryption, checksum validation, isolated full restore verification, schema validation, safe history, and encrypted download are implemented. A production-restore workflow and automatic retention deletion are deliberately absent; live creation requires an external encryption key. |
| Machine API keys | Missing | Scoped issuance, rotation, and revocation do not exist; browser owner authentication remains the only owner API boundary. |

## Current owner API

Real handlers exist for liveness/readiness; dashboard; activity; managed files;
release intelligence and narrow blocklist; dated episode calendar; manual collections; owner notifications and delivery history; review-only import-list snapshots; encrypted backup history/queue/download; read-only adoption discovery/review; source/pacing state; system health/tasks/info; owner-triggered Seerr metadata search/detail; wanted gaps; media list/create/detail/recycle;
workflow list/detail/create/search/cancel; recycle list/restore; Prowlarr backup
create/detail/approve; integration list/create/Newznab commission; library-root
list/create; quality-profile list/create; and authenticated Seerr webhook intake.
Every response is `no-store`. Mutation handlers are real actions and are not used
for smoke testing without explicit approval.

## Infrastructure and verification status

- The four Compose services are healthy: PostgreSQL, API, UI, and gateway.
- The gateway is bound to `127.0.0.1:28080` through its dedicated publish
  network and retains `NET_BIND_SERVICE` only.
- Cosmos 0.23.2 still points the Media Manager domain at
  `http://media-manager-ui-1:3000`. The supported Cosmos route API was identified,
  but the existing protected local API token returns HTTP 401. The target must be
  changed to `http://127.0.0.1:28080` once a current route-management token is
  available; unrelated Cosmos configuration must remain untouched.
- Baseline verification passed: ESLint, TypeScript, UI build/tests, Go tests with
  race detection, and `docker compose config --quiet`.
- The live UI artifact is dependency-separated from the development concept
  fixtures; an automated artifact scan verifies that fixture records are absent.

## Implementation order from this audit

1. Correct and externally verify the single Cosmos route when its credential is
   available.
2. Add read models/endpoints for dashboard, workflow detail/history, managed
   files, release evidence, indexer/client state, audit, and component health.
3. Replace `LiveUnavailableScreen` route by route using the approved concept
   layout and truthful empty/error states.
4. Complete configuration edit/disable/test lifecycles and full profile rules.
5. Implement adoption discovery/review/execution as a separately gated,
   reversible milestone; never execute it as part of automated verification.
6. Implement remaining approved persisted domains (collections, calendar/wanted,
   blocklist, import lists, notifications, backups) in small verified milestones.
