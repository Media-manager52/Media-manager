# 03 — Database reference (PostgreSQL 18)

Purpose: how schema, migrations, and persistence work, and where every table
belongs. Reading this before touching SQL saves hours.

## 1. Migration mechanism

- Migrations are **embedded** Go/SQL: `internal/postgres/migrations/NNNN_name.sql`,
  embedded via `//go:embed *.sql` in `migrations/migrate.go`.
- `CurrentSchemaVersion` in `internal/postgres/migrations/migrate.go` is the single version
  gate. Read its value from the checked-out source; the running API reports
  its own schema version in `/api/v1/system/info` and `/health/ready`.
  `Migrate` applies every file whose numeric prefix is `<= CurrentSchemaVersion`
  and not yet in `schema_migrations`.
- Each migration runs in its own transaction; a global advisory lock
  (`pg_advisory_lock(473652773271)`) serializes concurrent API starts.
- `AUTO_MIGRATE=true` (Compose) runs migrations at API startup before serving;
  `media-manager migrate` runs them as a one-off command.
- `SchemaVersion()` reads `MAX(version) FROM schema_migrations`. `/health/ready`
  and `SystemHealth` compare it with `CurrentSchemaVersion`.
- Files must be named `NNNN_lower_snake.sql`; the prefix must parse as an int64.

**To add a migration:**

1. Create `internal/postgres/migrations/00NN_your_change.sql` (additive,
   idempotent where practical, `IF NOT EXISTS` for new objects).
2. Bump `CurrentSchemaVersion` in `internal/postgres/migrations/migrate.go` in the same
   commit.
3. Add/extend repository methods and their integration tests.
4. Run `MEDIA_MANAGER_TEST_DATABASE_URL=... go test ./internal/postgres/...`
   and the migration command against a disposable database.
5. Never edit an applied migration file; add a new one instead.

The older `internal/postgres/security_migration_aggregates` table and
migrations 0027/0028 exist to sanitize credentials that were persisted in raw
Newznab fields; do not weaken those guarantees in new columns.

## 2. Conventions

- **IDs**: UUIDv7 (`uuid.NewV7()`) for time-sortable keys. Workflow variant
  scopes are encoded strings, not IDs.
- **Timestamps**: `timestamptz`, always written as UTC.
- **State**: sealed string enums in Go; DB stores text.
- **Optimistic concurrency**: repository reads return the row `revision`;
  mutations take `(id, expectedRevision, ...)` and return
  `postgres.ErrConcurrentModification` when the revision moved. Services treat
  that as "drop the stale event", not as an error.
- **Serializable writes**: multi-row invariants use
  `pgx.TxOptions{IsoLevel: pgx.Serializable}` (see `Store.CreateMediaWorkflow`).
- **Outbox**: state and event are inserted in the same transaction.
- **Audit**: user-visible mutations append `audit_events`
  (`actor`, `action`, `aggregate_type`, `aggregate_id`, `detail` JSON,
  `occurred_at`). `workflow_transitions` is the append-only workflow log.
- **JSON columns** hold rich evidence (policy, metadata, verification, plan
  payloads). They are validated by pure packages before or after load; treat
  them as untrusted when reading.
- **Plaintext secrets never live in the DB.** Integrations store `secret_ref`
  (an env var name); ordinary provider evidence is sanitized. The dedicated
  `oldboys_credentials` table is an explicit encrypted-storage exception:
  AES-256-GCM seals all three keys under a separate API-only server key. No
  read model returns the ciphertext or credentials. See
  [OldBoys key recovery](../operations/oldboys.md).

## 3. Table catalog

Grouped by domain. Arrow notes indicate the owning repository file when it is
non-obvious.

### Platform and audit

| Table | Purpose |
| --- | --- |
| `schema_migrations` | Applied migration versions. |
| `outbox_events` | Durable work queue (leases, attempts, retry state). |
| `audit_events` | Owner-visible write audit. |
| `command_receipts` | Idempotency receipts for owner commands. |
| `security_migration_aggregates` | Credential-sanitization evidence from migrations 27/28. |
| `oldboys_credentials` | One encrypted API Token/PID/RID set plus update time; `oldboys_credentials.go`. Saving appends a secret-free audit in the same transaction. |

### Catalog and metadata

| Table | Purpose |
| --- | --- |
| `media_items` | Internal UUID primary key, kind, title, year, revision. |
| `external_identities` | Per-item provider identities (`tmdb`, `tvdb`, `imdb`), state, source. |
| `media_metadata` | Persisted normalized metadata (JSON) + observed_at. |
| `media_collections`, `media_collection_members` | Manual owner collections. |
| `season_monitoring` | Per-series/per-season monitoring + flags. |

### Workflows and acquisition rounds

| Table | Purpose |
| --- | --- |
| `workflows` | One row per media+scope+edition workflow: state, root/profile/client, edition (equal to its root's, enforced by a composite foreign key), selected release, attention, revision. At most one active workflow per media, variant and edition. |
| `workflow_transitions` | Append-only `from`/`to`/reason/actor log. |
| `acquisition_rounds` | Durable rounds with immutable bounded attempt limits, policy/upgrade snapshots, ordinal, state. |
| `workflow_attempted_releases` | Lifetime attempt registry (monotonic). |
| `workflow_release_exclusions` | Exact release exclusions after attributable failure. |
| `workflow_package_exclusions` | Canonical NZB package exclusions. |
| `workflow_submitted_packages` | Package identity → submission dedup. |
| `workflow_retry_discovery` | Low-priority discovery state, completeness, jitter, new-identity flags; `automatic_round_refused_generation` remembers a discovery for which an automatic round was refused (schema 60). |
| `season_search_responses` | One indexer's complete answer to a whole-season `tvsearch`, shared by the season's episode workflows for 12 hours: observation IDs and download locator references; `complete=false` remembers a season too large to share. Removed after a day (schema 61). |
| `search_recent_answers` | Per workflow, the per-indexer candidate evidence of the last complete targeted search that selected a release, reused for two hours by the search after that release fails. Removed after a day (schema 62). |
| `search_partial_retries` | Durable targeted/recovery partial-search completion evidence keyed by workflow, round, mode and search-intent fingerprint; stores exact candidate and immutable observation IDs. |

### Search, evidence, selection, blocklist

| Table | Purpose |
| --- | --- |
| `search_runs` | One search execution per workflow/round/fingerprint. |
| `search_run_indexers` | Per-indexer outcome within a run (pacing, error codes). |
| `release_observations` | Raw normalized observations (sanitized). |
| `release_candidates` | Durable candidates with release title/guid/indexer identity. |
| `candidate_decisions` | Policy evaluations (verdict, score, explanations) per candidate. |
| `candidate_selections` | The chosen release per run/workflow. |
| `release_blocks` | Owner blocklist: native (indexer+GUID) or pack (media+season+title). |

### Retrieval, AltMount, packs

| Table | Purpose |
| --- | --- |
| `streaming_choices` | Viewer release choices behind an addon stream list (indexer id, configuration fingerprint, native id, title, sanitized locator ref; no credential), kept for six hours so a list survives a restart. |
| `altmount_jobs` | Submission intent, upload name, queue id, job revision/state, attempts, and the latest AltMount-reported progress (display only). |
| `altmount_job_observations` | Queue observations over time. |
| `altmount_output_inventories` | Frozen inventory per job (local relative, completeness). |
| `altmount_output_assets` | Files inside an inventory (path, size, kind). |
| `pack_expectation_sets`, `pack_expected_episodes` | Immutable exact-season expectation. |
| `pack_evaluations`, `pack_issues` | Coverage verdict + reasons per inventory. |
| `pack_content_groups`, `pack_group_assets`, `pack_group_episodes` | Multi-episode file grouping and episode mapping. |

### Verification and subtitles

| Table | Purpose |
| --- | --- |
| `subtitle_verifications` | Per-job verification result: status, languages, evidence, retry state. |
| `subtitle_verification_companions` | Strictly matched managed sidecar companions per video. |

### Publication, Plex, files, recycle

| Table | Purpose |
| --- | --- |
| `library_links` | Logical managed link (media + scope + destination identity). |
| `library_link_versions` | Immutable generations: destination relative, target, state, release token. |
| `library_link_companions` | Subtitle companions tied to a version/generation. |
| `library_link_removals` | Displaced-generation removal state. |
| `plex_scan_requests` | Refresh/scan + exact confirmation polling state. |
| `plex_confirmations` | Exact GUID/path confirmations. |
| `recycle_entries` | Retention cycles for links and adopted real files. |
| `altmount_repairs` | One broken published file and its replacement workflow. `reason` is `corrupted` (AltMount health reports missing articles; carries the health id), `missing_file` (the file is gone from the AltMount mount; no health id), `missing_required_audio` (the library requires an audio language the file lacks) or `owner_requested` (the owner asked for a file that plays badly to be replaced, `POST /api/v1/repairs`; no health id). |

### Adoption

| Table | Purpose |
| --- | --- |
| `adoption_batches`, `adoption_items` | Reviewed discovery batches and per-item decisions. |
| `adoption_operations` | Journaled moves/renames with fingerprints and rollback state. |
| `adoption_plex_confirmations` | Exact Plex confirmation of adopted paths. |
| `adoption_monitoring_applications` | Post-Plex monitoring/search application records. |
| `adopted_files` | Current adopted real files + provenance/fingerprint. |
| `adopted_replacement_authorizations` | Exact fingerprint-bound replacement authorizations. |
| `adopted_restore_confirmations` | Plex re-confirmation state for rollback. |

### Integrations, indexers, settings

| Table | Purpose |
| --- | --- |
| `integration_instances` | Plex/Seerr/AltMount/Newznab config (sanitized) + `secret_ref`. |
| `library_roots` | Movie/TV roots with Plex/source visible mappings and defaults. `edition` is `standard` or `4k` (schema 54); a 4K root is never a default. |
| `quality_profiles` | Policy JSON, version, upgrades/default/enabled flags. |
| `indexer_capability_profiles` | Immutable commissioned capability profile (versioned). |
| `indexer_commissioning_runs`, `indexer_commissioning_steps` | v1/v2 commissioning lifecycle + steps. |
| `indexer_budget_state` | Rolling query/grab windows per indexer. |
| `indexer_request_reservations` | Durable per-call reservations (schema 30). |
| `indexer_feed_state` | Newest-uploads reader per indexer: last and next read, current interval, last outcome, consecutive failures (schema 58); the newest 1,000 upload IDs seen (`recent_guids`), successful reads and reads that did not reach an upload seen before (`reads_total`, `gap_reads_total`, `last_read_complete`, schema 63). |
| `indexer_feed_matches` | Uploads from the newest-uploads lists that matched a waiting workflow, with what was done about each (`pending`, `search_advanced`, `search_due`, `discovery_advanced`, `round_opened` (schema 59), `not_applicable`, `expired`). A note for the reader, not release evidence; rows older than 30 days are removed (schema 58). |
| `indexer_operation_status` | Latest persisted operation outcome/cooldown/failures. |

### Seerr, notifications, import lists, backups, Prowlarr

| Table | Purpose |
| --- | --- |
| `seerr_deliveries` | Webhook/poll receipts + processing state (deduplicated by unique indexes: `(instance, event_id)` for events with an ID, otherwise `(instance, request_id, payload_sha256)`; `outcome='requeued'` marks an owner-triggered re-evaluation). |
| `seerr_request_snapshots`, `seerr_request_seasons`, `seerr_request_workflows` | Durable request display state, workflow links, and observed `seerr_server_id`/`seerr_profile_id`/`seerr_root_folder`/`seerr_is_4k` routing provenance. |
| `seerr_library_routes` | Maps a Seerr root folder (instance + media kind + folder) to a Media Manager `library_roots` row and optional `quality_profiles` override. |
| `notification_destinations`, `notification_deliveries` | Owner webhook subscriptions + attempts. |
| `import_lists`, `import_list_snapshots`, `import_list_snapshot_items` | IMDb export lists and review-only snapshots; followed MDBList lists (`provider='mdblist'`, `source_owner`/`source_slug`, film and series folder and profile, `next_sync_at`, last read state, schema 64; address parts may hold any letters such as æ, ø and å but no slash, space, `?`, `#`, `%` or `.`/`..`, schema 65). |
| `import_list_entries` | Every title a followed MDBList list has carried and what happened to it (`pending`, `added`, `in_library`, `removed_earlier`, `failed` with a code and attempt count), with the media it maps to. Deleting a list deletes its entries, never its titles (schema 64). |
| `backup_records` | Encrypted backup jobs, checksums, verification state. |
| `prowlarr_backup_imports`, `prowlarr_indexer_candidates` | Review-only Prowlarr backup import. |

## 4. Repository (`*Store`) file map

All SQL lives in `internal/postgres`. The package exposes one `Store` with
`Pool *pgxpool.Pool`; services call typed methods. There are 212 `*Store`
methods at the snapshot. Group by file:

| File | Domain (approx. methods) |
| --- | --- |
| `store.go` | Open/Close/Ping, media+workflow creation (14), metadata persistence, error sentinels. |
| `migrations/migrate.go` | Migration runner, `CurrentSchemaVersion`, `SchemaVersion`. |
| `outbox.go` | Claim/complete/fail/defer/renew lease (9). |
| `readmodels.go` | Dashboard, activity, managed files, media detail, calendar, wanted, tasks, source status, release candidates, system health (14). |
| `settings.go` | Integrations, library roots, quality profiles (14). |
| `search.go` | Search context load + `PersistSearch` + indexer outcomes (9). |
| `submission.go` | Retrieval context, retrieval failures, submission intent/state (9). |
| `altmount.go` | Job load/attach/observe/inventory/import-complete (13). |
| `acquisition_rounds.go` | Round snapshot, attributable failures, exclusions, open next round (8). |
| `publication.go` | Publication context/plans/complete/fail; Plex scans/confirmations/displacement (21). |
| `plex_publication_integration_test.go` etc. | Lifecycle tests (build-tagged by env var). |
| `recycle.go` | Plan/move/restore/purge entries, adopted restore confirmations (11). |
| `adoption.go` | Batches/items/review/approval (9). |
| `adoption_execution.go` | Execution context, operations, rollback, Plex confirm (12). |
| `adopted_upgrade.go`, `adopted_restore.go`, `adoption_monitoring.go` | Upgrade authorization, restoration, post-Plex monitoring (8+). |
| `archive.go` | Safe media archive/delete operation. |
| `manual.go` | Manual media/workflow creation paths (8). |
| `airing.go` | Episode air-date expectations and dispatch (5). |
| `blocklist.go` | Blocklist CRUD + matching (6). |
| `collections.go` | Collections + preview (5). |
| `activity.go` | Merged audit/workflow timeline (5). |
| `metadata.go` | Metadata load/persist/refresh. |
| `subtitles.go`, `subtitle_reverification.go` | Verification records, retained-output reverification. |
| `seerr_intake.go`, `seerr_status.go`, `seerr_requests.go` | Seerr dedup, status sync, request read models. |
| `commissioningstore/indexer_capabilities.go` | Capability profile CRUD/activation (19). |
| `commissioningstore/indexer_commissioning*.go` | v1 commissioning runs/steps (50). |
| `commissioningstore/indexer_commissioning_live*.go` | v2 live gate + activation (29). |
| `commissioningstore/indexer_commissioning_draft.go` | Draft persistence. |
| `notificationstore/` | Destinations + deliveries (8). |
| `importliststore/` | Lists/snapshots/items (9). |
| `backupstore/` | Backup records + verified downloads (7). |
| `commission.go` | Staged Newznab commissioning completion. |
| `prowlarr.go` | Backup imports + candidate approval. |
| `failure_recovery_integration_test.go`, `held_recovery_integration_test.go`, `legacy_exhausted_round_recovery_migration_integration_test.go` | Recovery migrations/tests. |

When adding a method, place it in the domain file, reuse `ErrNotFound` /
`ErrConcurrentModification`, and add an integration test when it spans tables.

## 5. Concurrency rules to preserve

1. **Revision CAS.** Never update a `workflows` row without matching
   `revision` and incrementing it; same for `altmount_jobs`, publication
   versions, and adoption operations.
2. **Stale events are no-ops.** Workers compare
   `event.AggregateRevision` before acting.
3. **Outbox ordering is not guaranteed.** Handlers must tolerate
   out-of-order/duplicate delivery. `ClaimOutbox` claims lifecycle events
   before searches and discovery rescans last; that is a latency preference,
   not an ordering contract.
4. **Round snapshot guard.** A search whose round policy version is older
   than the profile parks the workflow with `quality_policy_changed`; a changed
   upgrade baseline fails closed with "owner review is required". Only
   `ApplyQualityProfilePolicy` replaces rounds after a policy change.
5. **Retention.** Recycle automatic purge planning runs hourly via
   `PlanDueRecyclePurges`; adopted real files are never auto-deleted.

## 6. The UI has no database

The web UI (`web/`) has no database of its own. **The only database is
PostgreSQL managed by the Go migrations above**; the UI reads and writes
through the HTTP API.

## Schema 41: bounded NZB retrieval recovery

Migration `0041_bounded_retrieval_recovery.sql` adds nullable
`retrieval_round_id` and `retrieval_error_code` to `workflow_release_exclusions`,
with a same-workflow round foreign key and a round/provider index. Four failed
candidate retrievals pause the round separately from its two submission slots.
HTTP 401/403 records prevent reuse of that provider inside this round. Historical
rows remain unassigned; migration never resumes parked workflows. The runtime
admission and atomic alternative selection live in `retrieval_recovery.go`.

## Schema 42: AltMount-reported progress

Migration `0042_altmount_job_progress.sql` adds nullable `progress_percent`
(0-100), `progress_stage` (at most 64 characters), and `progress_observed_at`
to `altmount_jobs`. `ObserveAltMountAndReschedule` stores exactly what
AltMount reported on each observation; an absent or out-of-range percentage is
stored as NULL. The columns never drive workflow state. Workflow summaries
expose them as `download_percent`, `download_stage`, and
`download_observed_at` only for the current grab attempt while the workflow is
submitting, queued, downloading, or streaming ready.


### Configurable acquisition bounds (schema 43)

`0043_configurable_acquisition_limits.sql` allows attempt snapshots and round
slots in the bounded range 1–6. It does not update historical rows or relax
the immutable acquisition-round snapshot trigger. API startup configuration
chooses new-round budgets and the independent retained-output admission limit.
