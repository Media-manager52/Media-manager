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

The live build is selected with `NEXT_PUBLIC_MEDIA_MANAGER_MODE=live`. In that
mode, unmapped routes render `LiveUnavailableScreen`; they do not fall through to
the fixture screens.

## UI route audit

| Route | Status | Current live behavior | Required work |
| --- | --- | --- | --- |
| `/` | Partial | Loads real media, active workflows, and recycle entries. | Restore the approved overview composition using real metrics, attention, health, and recent activity. |
| `/media` | Partial | Lists real catalog records and verified external identities. | Add persisted monitoring, publication, profile/root, and current-quality summaries. |
| `/media/movies/:id` | Partial | Loads the real item and its workflows; can request a new workflow or recycle it. | Show current files, publication state, policy, candidates, and safe upgrade context. |
| `/media/series/:id` | Partial | Loads the real item and its workflows; can create an exact-season workflow. | Load persisted seasons/episodes instead of asking the owner for a comma-separated episode list. |
| `/files` | Mock-only | Live build reports unavailable. | Add read-only managed-link/source inventory API and the approved file manager. |
| `/files/:id` | Mock-only | Live build reports unavailable. | Add real provenance, containment, publication, Plex, and recycle detail. |
| `/collections` | Mock-only | Live build reports unavailable. | Add persisted manual/smart collections and preview-before-write behavior. |
| `/calendar` | Mock-only | Live build reports unavailable. | Add metadata availability/air-date persistence and read API. |
| `/wanted` | Mock-only | Live build reports unavailable. | Derive missing and upgrade gaps from monitoring, current publication, and policy. |
| `/import` | Mock-only | Live build reports unavailable. | Implement read-only discovery, adoption plan persistence, review, and separately approved execution. |
| `/activity/queue` | Partial | Lists real workflow summaries and queues safe search/cancel transitions. | Add job/attempt detail, phase evidence, per-operation progress, and attention recovery. |
| `/activity/recycle` | Partial | Lists persisted recycle cycles and queues whole-media restore. | Add cycle detail and explicit confirmation UI for real-file purge cases. |
| `/activity/history` | Mock-only | Live build reports unavailable. | Expose workflow transitions and audit events as a durable timeline. |
| `/activity/blocklist` | Mock-only | Live build reports unavailable. | Add exact-release/pack-scope block persistence and APIs. |
| `/discover` | Partial | Creates real movie or season workflows from owner-supplied verified IDs and supports root/profile/client override. | Add real metadata search and identity verification before submission. |
| `/intelligence/releases` | Mock-only | Live build reports unavailable. | Expose persisted search runs, observations, decisions, selections, and evidence. |
| `/intelligence/groups` | Mock-only | Live build reports unavailable. | Add evidence-derived group aggregation without permanent trust labels. |
| `/intelligence/evidence` | Mock-only | Live build reports unavailable. | Add read APIs for normalized facts, raw-source provenance, confidence, and contradictions. |
| `/sources/indexers` | Mock-only | Live build reports unavailable. | List real Newznab integrations, capability snapshots, persisted budgets, and scoped outcomes. |
| `/sources/indexers/:id` | Mock-only | Live build reports unavailable. | Add real configuration/capability/budget detail and safe commissioning history. |
| `/sources/indexer-lab` | Partial | The live route is a real Prowlarr ZIP review/import flow; approved instances remain disabled pending commissioning. | Separate backup import from the approved paced commissioning diagnostics and remove the route-name mismatch. |
| `/sources/download-clients` | Mock-only | Live build reports unavailable. | Expose the real AltMount integration and capability/path state. |
| `/sources/import-lists` | Mock-only | Live build reports unavailable. | Implement list sources and monitoring-only synchronization, or remove only after an explicit product decision. |
| `/settings/profiles` | Partial | Lists and creates real quality profiles with one required and one preferred rule. | Implement the full approved rule editor, validation, versioning, cutoff, language/audio/subtitle goals, edit, and disable. |
| `/settings/metadata` | Mock-only | Live build reports unavailable. | Implement provider configuration, lookup status, artwork choices, locks, and refresh state. |
| `/settings/media-management` | Mock-only | Live build reports unavailable. | Expose the real publication/recycle/upgrade invariants and configurable supported policies. |
| `/settings/storage` | Partial | Lists and creates real Plex-backed movie/TV roots with defaults and path mappings. | Add preflight validation, edit/disable, naming preview from real values, and mount/capacity checks. |
| `/settings/connections` | Partial | Lists and creates real Plex, Seerr, AltMount, and staged Newznab records; Newznab commissioning is real and paced. | Add safe connection tests and edit/disable/default management; surface operation-level state without fake “ready” labels. |
| `/settings/notifications` | Mock-only | Live build reports unavailable. | Add persisted destinations, event selection, redaction, and delivery history. |
| `/system/tasks` | Mock-only | Live build reports unavailable. | Expose scheduled worker/outbox state and safe supported manual triggers. |
| `/system/backups` | Mock-only | Live build reports unavailable. | Implement Media Manager database/config backup and restore verification. Project tar backup is not this feature. |
| `/system/security` | Mock-only | Live build reports unavailable. | Expose owner-only boundary, secret-reference health, mount modes, and outbound policy checks. |
| `/system/api` | Mock-only | Live build reports unavailable. | Document the owner API and add scoped machine keys only if required by an approved integration. |
| `/system/health` | Partial | Calls real API liveness/readiness through the owner gateway. | Add component- and operation-level health for PostgreSQL, Plex, AltMount, Seerr, and each Newznab instance. |
| `/system/logs` | Mock-only | Live build reports unavailable. | Expose bounded redacted application diagnostics and persisted write audit events. |

## Backend capability audit

| Capability | Status | Evidence and gap |
| --- | --- | --- |
| Owner-only browser administration | Real | Caddy Basic Auth protects all owner routes and injects the API owner bearer token; every owner API handler independently checks it. The Seerr webhook is the only explicit exception and has separate authentication. |
| Media Manager and Seerr request intake only | Real | Manual API and authenticated Seerr webhook/poll intake are implemented. Compose and runtime contain no Radarr or Sonarr dependency. |
| Manual movie and exact-season requests | Real | Catalog, identities, root/profile/client overrides, season monitoring, immutable episode expectations, and workflows are transactionally persisted. |
| Metadata discovery and identity verification | Missing | The owner currently types provider IDs; no metadata provider client or lookup API exists. |
| Seerr reconciliation | Real | Webhook deduplication, five-minute polling, provenance, configuration retry, and revocation reconciliation are persisted. Management/status UI is missing. |
| Newznab commissioning | Partial | A disabled instance is paced at at least three seconds, fetches CAPS, requires generic search, and performs one random negative query before enablement. The approved multi-phase lab and detailed evidence are not implemented. |
| Newznab runtime pacing | Real | Per-indexer single-flight lanes, a global bound, persisted next-allowed/cooldown state, `Retry-After`, and bounded backoff wrap supported indexer requests. |
| Release search/evidence/policy | Real backend, missing UI | Search runs, raw observations, normalized evidence, decisions, and selection are persisted; no owner read API exposes them. |
| AltMount retrieval and reconciliation | Real | Submission intent, unknown-outcome recovery, queue observation, SSE/poll reconciliation, output inventory, and read-only source paths are implemented. |
| Complete TV season packs as one job | Real | One season workflow creates an immutable expectation; one AltMount job is evaluated for exact episode coverage and published only from verified content groups. |
| Plex-safe symlink publication | Real | Paths are containment-checked, names require verified Plex-supported identity, symlinks are batch-published, and Plex confirms every expected path/identity. |
| Safe upgrades | Real invariant, partial product | Existing current links are displaced and removed only inside the transaction triggered after Plex confirms the replacement. Current-quality comparison, cutoff presentation, and owner-facing upgrade evidence remain incomplete. |
| Multiple roots/defaults/per-item override | Real backend, partial UI | Movie/series defaults and explicit request overrides are enforced. UI can create/list but cannot validate, edit, or disable roots. |
| Recycle retention | Real | Managed links enter a 30-day persisted cycle; AltMount source is retained. Restore and due-purge workers exist, with confirmation required for real files. |
| Existing-library adoption review | Missing backend | Tables exist and the development concept shows the intended review, but there are no store services, API handlers, scanner, plan review, or executor. |
| Prowlarr backup-only import | Real, with a review gap | ZIP/SQLite parsing is bounded and review creates disabled Newznab instances. The owner must manually place extracted keys into the secret environment before approval; import history/listing is absent. There is no Prowlarr runtime connection. |
| Integration/profile/root lifecycle | Partial | Create and list are real. Update, disable/delete, default reassignment, and most safe tests are missing. |
| Audit/history | Partial | Core writes append `audit_events` and workflow transitions, but no bounded read API/UI exists and not every operation emits an audit event. |
| Component health | Partial | Process and database/schema probes are real. Dependency- and operation-level health is missing. |
| Backups, notifications, import lists, collections, blocklist, machine API keys | Missing | Only development concepts/schema-adjacent copy exist; no usable backend lifecycle is present. |

## Current owner API

Real handlers exist for liveness/readiness; media list/create/detail/recycle;
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
