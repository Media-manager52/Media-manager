# 01 — System architecture

Purpose: give a new agent the complete mental model of Media Manager before it
touches code. Everything here is grounded in `cmd/media-manager/main.go`,
`internal/platform/worker.go`, `internal/workflows/model.go`, and `compose.yaml`.

## 1. System context

```
                      ┌──────────────────────────── Docker host ───────────────────────────┐
  Owner browser ──TLS──▶ Cosmos Cloud (public edge, OpenID auth, injects owner bearer)
                      │       │ /api, /health                     │ everything else
                      │       ▼                                   ▼
                      │  media-manager-api-1:8080            media-manager-ui-1:3000
                      │  (Go: HTTP + outbox worker)          (Node + Vinext/Next UI)
                      │       │  │  │
                      │       │  │  └──────── integration network ────────┐
                      │       │  └─── backend network ───┐                │
                      │       ▼                          ▼                ▼
                      │  postgres:5432               (internal)     AltMount, Seerr,
                      │                                              Plex, Newznab
                      └────────────────────────────────────────────────────────────────────┘
```

- **One origin.** Cosmos Cloud terminates TLS, authenticates the owner, and
  routes `/api/*` + `/health/*` to the API container and all other paths to the
  UI container. The `/api` Cosmos route overwrites the inbound `Authorization`
  header with `Bearer <OWNER_TOKEN>` server-side, so browser JavaScript never
  sees the token and cannot forge or read it. See
  [07-operations.md](../operations/runbook.md#public-edge-cosmos-cloud) for the exact
  route table and `README.md` § "Public edge routing".
- **Loopback bypass.** The API is also published on
  `127.0.0.1:${MEDIA_MANAGER_API_PORT}` (default `28081`) for machine checks and
  the live-acceptance proxy. It must never be exposed to a LAN or public address.
- **Seerr webhook bypass.** Seerr posts directly to
  `http://media-manager-api-1:8080/api/v1/integrations/seerr/webhook` over the
  shared integrations network with its own `SEERR_WEBHOOK_SECRET`; it does not
  use Cosmos and does not need an interactive login.
- **Networks** (`compose.yaml`): `frontend` (UI↔API, internal), `backend`
  (API↔Postgres, internal), `integrations` (external, shared with AltMount/other
  containers; `MEDIA_NETWORK`, default `media-manager`).

## 2. Runtime processes

All Go runtime behavior lives in one binary, `cmd/media-manager`:

| Command | What it does |
| --- | --- |
| `serve` (default) | Connects to Postgres, optionally migrates (`AUTO_MIGRATE=true`), validates config, builds the HTTP server, starts the outbox worker goroutine and a ticker goroutine, then serves HTTP until SIGINT/SIGTERM (20s graceful shutdown). |
| `migrate` | Applies embedded SQL migrations and exits. |
| `version` | Prints `Media Manager <version> (<commit>)`. |
| `healthcheck` | HTTP GET to `HEALTHCHECK_URL` (default `http://127.0.0.1:8080/health/live`), used by the container healthcheck. |

Startup validation in `run()` (`cmd/media-manager/main.go`) fails fast when:

- neither `DATABASE_URL` nor `PGHOST` is set;
- `OWNER_TOKEN` is shorter than 32 bytes (enforced again in `httpapi.New`);
- Seerr config is partially set (all of `SEERR_BASE_URL`, `SEERR_API_KEY`,
  `SEERR_INSTANCE_ID`, `SEERR_WEBHOOK_SECRET` must be present together);
- `SEERR_WEBHOOK_SECRET` is shorter than 32 bytes, or `SEERR_INSTANCE_ID` is not
  a UUID pointing at an enabled `seerr` integration row;
- metadata provider credentials are inconsistent (e.g. TVDB PIN without key).

Secrets are resolved through `optionalSecret(name)`: if `<NAME>_FILE` is set the
file content wins, otherwise the environment variable. Compose uses this for
the three metadata providers (Docker secrets) and `.env.integrations` is loaded
only into the API container.

### Graceful shutdown order

1. SIGINT/SIGTERM received.
2. `stopWorker()` cancels the worker context.
3. HTTP server `Shutdown` with a 20s timeout.

An in-flight worker handler is interrupted by context cancellation; the outbox
lease is not renewed and a later worker run re-claims the event.

## 3. The durable outbox (the heart of the system)

There is no queue service. Work is state + events in PostgreSQL.

- **`outbox_events` row**: `id`, `aggregate_type`, `aggregate_id`,
  `aggregate_revision`, `event_type`, `payload` (JSON), `attempts`,
  `available_at`, `lease_owner`, `lease_until`, `completed_at`, `last_error`.
- **Producer side.** Store methods insert an event in the same transaction as
  the state change, so an event can never exist without its state or vice versa.
- **Consumer side.** `platform.Worker` (`internal/platform/worker.go`):
  - Polls every second; claims **1** event per batch via
    `Store.ClaimOutbox` (`FOR UPDATE SKIP LOCKED`), so multiple API replicas
    would not double-process. Due events are claimed in three tiers, oldest
    first within each: lifecycle work (AltMount, publication, Plex, Seerr,
    recycle, ...), then `workflow.search_requested`, then the evidence-only
    `workflow.discovery_rescan_requested`. A paced search backlog therefore
    cannot delay time-bounded Plex confirmation or reconciliation.
  - Leases for 1 minute; a heartbeat goroutine renews at `lease/3` (max 30s,
    min 1s) and cancels the handler if renewal fails.
  - Success → `CompleteOutbox`. Error → `FailOutbox` with exponential backoff
    `2^min(attempts,8)` seconds, unless the error implements
    `RetryAt() time.Time` (a deliberate deferral), in which case
    `DeferOutbox` is used and the attempt counter is **not** incremented (pacing
    errors must not consume retry budget).
  - Errors are stored only as safe codes: an error's `SafeCode()` is used when
    it matches `^[a-z][a-z0-9_]{2,99}$`, otherwise the Go type name.
- **Registered types** (`cmd/media-manager/main.go` handler map):

| Event type | Handler | Meaning |
| --- | --- | --- |
| `workflow.search_requested` | `acquisition.SearchService.Handle` | Run a targeted search for one workflow/round. |
| `workflow.discovery_rescan_requested` | `SearchService.HandleDiscovery` | Evidence-only low-priority rescan after round exhaustion. Never selects or submits. |
| `release.retrieve_requested` | `acquisition.RetrievalService.Handle` | Fetch the selected NZB and submit to AltMount. |
| `altmount.reconcile_requested` | `acquisition.AltMountReconcileService.Handle` | Poll queue, enumerate completed output, verify, then import or fail. |
| `altmount.output_reverify_requested` | `AltMountReconcileService.HandleRetainedOutputReverification` | Re-inspect one retained output after a probe correction; no AltMount call. |
| `library.publish_requested` | `publication.Service.Handle` | Build and create Plex-safe symlinks. |
| `library.displaced_remove_requested` | `publication.Service.HandleDisplacedRemoval` | Remove an old generation only after Plex confirms the replacement. |
| `plex.scan_requested` | `publication.PlexService.HandleScan` | Ask Plex to refresh/scan the library. |
| `plex.confirm_requested` | `PlexService.HandleConfirm` | Exact identity/path confirmation poll. |
| `plex.recycle_refresh_requested` | `PlexService.HandleRecycleRefresh` | Refresh after recycle move/restore. |
| `plex.displaced_refresh_requested` | `PlexService.HandleDisplacedRefresh` | Refresh after displacing an old generation. |
| `plex.adopted_restore_confirm_requested` | `PlexService.HandleAdoptedRestoreConfirm` | Re-confirm every restored adopted path before rollback displaces replacements. |
| `adoption.execute_requested` | `adoptionexec.Service.HandleExecute` | Execute the journaled adopted-file move/rename plan. |
| `adoption.plex_confirm_requested` | `adoptionexec.Service.HandlePlexConfirm` | Confirm adopted paths in Plex, create current files, apply reviewed monitoring. |
| `recycle.move_requested` | `recycle.Service.HandleMove` | Move a symlink/real file into the recycle area. |
| `recycle.restore_requested` | `recycle.Service.HandleRestore` | Restore a recycle entry (link directly; real file → Plex-gated). |
| `recycle.purge_requested` | `recycle.Service.HandlePurge` | Physically remove a retained entry after retention/confirmation. |
| `notification.deliver_requested` | `notifications.Service.Handle` | Deliver one owner-configured webhook. |
| `backup.create_requested` | `backups.Service.Handle` | Run a durable encrypted PostgreSQL backup. |
| `seerr.reconcile` | `intake.SeerrService.Handle` | Reconcile one Seerr request (webhook or poll). |
| `seerr.status_sync_requested` | `intake.SeerrStatusService.Handle` | Push verified availability back to Seerr. |
| `workflow.transitioned` | no-op | Audit/notification fan-out placeholder. |

The two `seerr.*` handlers are only registered when Seerr is fully
configured (`seerrEnabled`); without it, those events would fail as
"no handler registered". The ticker goroutine in `main.go` (every hour / every 5 minutes) plans due
recycle purges, requeues Seerr requests that need configuration, and queues
active Seerr polls + confirmed status syncs (25 each per tick).

## 4. Workflow state machine

`internal/workflows/model.go` defines the durable per-scope acquisition state.
A workflow is created per media item **and scope** (movie `default`, season
`season:NN`, airing episode `season:NN:episode:NN` — see
`internal/workflows/scope.go`).

```
monitored ──▶ waiting ──▶ searching ──▶ candidate_selected ──▶ submitting
                 ▲            │               │                    │
                 │            ▼               ▼                    ▼
                 │          held            blocked         transport_unknown
                 │                                                 │
                 │                                                 ▼
                 │                                               queued
                 │                                                 │
                 │                                                 ▼
                 │                                          streaming_ready
                 │                                                 │
                 │                                                 ▼
                 │                                         import_complete
                 │                                                 │
                 │                                                 ▼
                 │                                            verifying
                 │                                                 │
                 │                                                 ▼
                 │                                            publishing
                 │                                                 │
                 │                                                 ▼
                 │                                           plex_scanning
                 │                                                 │
                 │                                                 ▼
                 └────────────── needs_attention ◀──────────── plex_confirmed (terminal)
```

- `needs_attention` can be entered from almost every active state and can
  re-enter `waiting`, `searching`, `candidate_selected`, `submitting`,
  `verifying`, `publishing`, `plex_scanning`, or `cancelled`.
- `held` → `searching` / `candidate_selected` / `waiting` / `cancelled`.
- `blocked` → `waiting` / `cancelled`; `quarantined` → `verifying` /
  `cancelled`; `cancelled` has no outgoing transition.
- A same-state `Transition` is an idempotent no-op (does not bump revision).
- Every valid transition bumps `Revision`; handlers compare
  `event.AggregateRevision` with the current revision to drop stale events.
- `Workflow.OwnerAttentionState()` reports `needs_attention` for a workflow
  parked in `waiting` with `AttentionRoundExhaustedWaitingNewIdentity` (or any
  release-attributable failure) so owner UIs/acceptance drivers see the
  required action while background discovery keeps the durable `waiting` state.

State persistence: `workflows` + `workflow_transitions` (append-only audit).

## 5. Request flow (happy path, movie)

1. Owner adds media (`POST /api/v1/media`) or Seerr webhook/poll intake
   (`internal/intake`). Catalog + identities + workflow + first acquisition
   round are written in one serializable transaction; a `workflow.search_requested`
   event is queued when `search_immediately`.
2. `acquisition.SearchService` validates round/policy/revision, enforces
   at most one search per indexer with persisted pacing/budgets, builds a
   capability-profile search plan, runs one request per indexer, evaluates every
   observation against policy rules, and persists candidates + a selection.
3. `release.retrieve_requested` fetches the NZB with a pinned locator, computes
   an exact package identity (SHA-256), records the submission intent, and
   uploads to AltMount. Ambiguous upload results are reconciled by unique
   filename and never blindly resubmitted.
4. `altmount.reconcile_requested` polls the queue, checks the bounded read-only
   output mapping, freezes an inventory, evaluates movie/season-pack coverage,
   probes required subtitles, and either marks import complete or records a
   release-attributable failure that consumes an acquisition slot.
5. `library.publish_requested` builds contained, Plex-named symlink plans plus
   verified subtitle companions and publishes the batch transactionally.
6. `plex.scan_requested` → `plex.confirm_requested` polls Plex until every exact
   expected path matches the verified provider GUID. Only then is the new
   generation "current", old generations are displaced (after confirmation
   only), and Seerr availability is queued.
7. `seerr.status_sync_requested` pushes availability to Seerr.

Failure paths never fake success: they end in `needs_attention` or retained
`waiting` with an exact reason, and output stays on disk.

## 6. Data ownership

PostgreSQL is the only durable store. Runtime data on disk is deliberately
minimal: `/libraries/movies` + `/libraries/tv` (managed symlinks/real files),
recycle areas, `data/backups`, and read-only `/mnt/altmount`.

Table groups (full catalog in [03-database.md](database.md)):

- Catalog/metadata: `media_items`, `external_identities`, `media_metadata`,
  `media_collections*`.
- Acquisition: `workflows`, `workflow_transitions`, `acquisition_rounds`,
  `workflow_attempted_releases`, `workflow_release_exclusions`,
  `workflow_package_exclusions`, `workflow_submitted_packages`,
  `workflow_retry_discovery`.
- Search: `search_runs`, `search_run_indexers`, `release_observations`,
  `release_candidates`, `candidate_decisions`, `candidate_selections`,
  `release_blocks`.
- Retrieval/AltMount: `altmount_jobs`, `altmount_job_observations`,
  `altmount_output_inventories`, `altmount_output_assets`.
- Season packs: `pack_expectation_sets`, `pack_expected_episodes`,
  `pack_evaluations`, `pack_content_groups`, `pack_group_assets`,
  `pack_group_episodes`, `pack_issues`.
- Verification: `subtitle_verifications`, `subtitle_verification_companions`.
- Publication/Plex: `library_links`, `library_link_versions`,
  `library_link_companions`, `library_link_removals`, `plex_scan_requests`,
  `plex_confirmations`.
- Recycle: `recycle_entries`.
- Adoption: `adoption_batches`, `adoption_items`, `adoption_operations`,
  `adoption_plex_confirmations`, `adoption_monitoring_applications`,
  `adopted_files`, `adopted_replacement_authorizations`,
  `adopted_restore_confirmations`.
- Settings/integrations: `integration_instances`, `library_roots`,
  `quality_profiles`, `indexer_*` (capabilities, commissioning, budgets,
  reservations, operation status).
- Platform: `outbox_events`, `audit_events`, `command_receipts`,
  `schema_migrations`, Seerr tables, notifications, backups, import lists.

## 7. Safety invariants

These come from `README.md` and `AGENTS.md`; they are enforced by code and tests.

1. **Read-only AltMount.** `internal/adapters/altmount.Enumerate` maps reported
   paths under `local_read_only_root` and refuses anything outside it. Nothing
   in the codebase writes to `/mnt/altmount`.
2. **Contained publication.** `internal/library` validates every relative path
   (`ValidateRelative`), refuses escape symlinks/collisions, and only creates
   symlinks under the configured destination root. Plex never follows links
   outside the mounted roots.
3. **One job per scope.** A season pack is one retrieval/submission/inventory
   evaluated against one immutable `pack_expectation_sets` row. Airing series
   may instead dispatch per-episode scopes (`DispatchAiringSeason`).
4. **Completion ≠ success.** `altmount` phase `import_complete` only means
   queue completion; publication additionally requires inventory + coverage +
   subtitle verification; success additionally requires exact Plex confirmation.
5. **Fail closed.** Missing expected episodes, ambiguous primary movie file,
   unreadable output, missing required subtitle language, or Plex timeout all
   produce owner-visible attention, never a synthetic success. Failed output is
   retained and undeleted.
6. **Two-slot rounds.** A round has two durable submission slots; only
   release-attributable output failures consume slots and atomically exclude
   the exact release + NZB package inside that workflow. Infrastructure failures
   reconcile the same job. After both slots, low-priority discovery may run but
   only the owner can open the next round (`POST .../acquisition-rounds`).
   Four retained failed outputs stop further rounds pending storage review.
7. **Exact confirmation.** Plex confirmation requires a verified provider GUID
   and every exact expected path. Old current links are removed only inside that
   confirmation transaction's follow-up. Adopted real files are displaced only
   after Plex confirms the replacement and are retained 30 days.
8. **Pacing and budgets.** Every Newznab call reserves a durable per-provider
   rolling budget before network I/O; one in-flight request per indexer; 3s
   minimum interval; `Retry-After` and bounded backoff are honored. The
   commissioning lab uses the same scheduler.
9. **Hostile-input posture.** Outbound redirects are pinned to validated public
   addresses (max 2 hops), cross-origin credentials/Referer are stripped,
   responses are size-bounded, XML/NZB parsing is strict, and credentials are
   sanitized out of every persisted/owner-visible field.
10. **Owner-only writes.** Every `/api/v1/*` route calls `requireOwner` except
    the Seerr webhook, which authenticates with its own constant-time
    `SEERR_WEBHOOK_SECRET` bearer. The
    commissioning-v2 mutation routes additionally require a trusted `Origin`
    matching `COMMISSIONING_V2_OWNER_ORIGIN`, reject cross-site Fetch Metadata,
    and require `application/json`.

## 8. Where to start for common questions

| Question | File |
| --- | --- |
| How does startup wiring work? | `cmd/media-manager/main.go` |
| What routes exist and how is auth done? | `internal/httpapi/server.go`, `internal/httpapi/commissioning_v2.go` |
| How does the worker work? | `internal/platform/worker.go`, `internal/postgres/outbox.go` |
| What are the legal workflow transitions? | `internal/workflows/model.go` |
| How are searches planned/executed? | `internal/acquisition/search.go`, `search_plan.go`, `internal/adapters/newznab/` |
| How is a release evaluated? | `internal/releases/evidence.go` |
| How is a pack verified? | `internal/packs/coverage.go` |
| How are subtitles verified? | `internal/subtitles/verify.go`, `policy.go` |
| How is AltMount reconciled? | `internal/acquisition/reconcile_altmount.go`, `internal/adapters/altmount/` |
| How is publication done? | `internal/publication/service.go`, `internal/library/` |
| How does Plex confirmation work? | `internal/publication/plex.go`, `internal/adapters/plex/` |
| How does Seerr intake work? | `internal/intake/`, `internal/adapters/seerr/` |
| How does adoption work? | `internal/adoption/`, `internal/adoptionexec/` |
| How does recycle work? | `internal/recycle/`, `internal/postgres/recycle.go` |
| How do I read the DB? | `internal/postgres/*.go` (see [03-database.md](database.md)) |
| How does the UI map routes to screens? | `components/media-manager/app-shell.tsx` |
