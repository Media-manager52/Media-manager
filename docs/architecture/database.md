# 03 — Database reference (PostgreSQL 18)

Purpose: how schema, migrations, and persistence work, and where every table
belongs. Reading this before touching SQL saves hours.

## 1. Migration mechanism

- Migrations are **embedded** Go/SQL: `internal/postgres/migrations/NNNN_name.sql`,
  embedded via `//go:embed migrations/*.sql` in `migrate.go`.
- `CurrentSchemaVersion int64 = 39` in `migrate.go` is the single version gate.
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
2. Bump `CurrentSchemaVersion` in `internal/postgres/migrate.go` in the same
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
- **Secrets never live in the DB.** Integrations store `secret_ref` (an env var
  name); only sanitized values are persisted anywhere else.

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
| `workflows` | One row per media+scope workflow: state, root/profile/client, selected release, attention, revision. |
| `workflow_transitions` | Append-only `from`/`to`/reason/actor log. |
| `acquisition_rounds` | Durable two-slot rounds, policy/upgrade snapshots, ordinal, state. |
| `workflow_attempted_releases` | Lifetime attempt registry (monotonic). |
| `workflow_release_exclusions` | Exact release exclusions after attributable failure. |
| `workflow_package_exclusions` | Canonical NZB package exclusions. |
| `workflow_submitted_packages` | Package identity → submission dedup. |
| `workflow_retry_discovery` | Low-priority discovery state, completeness, jitter, new-identity flags. |

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
| `altmount_jobs` | Submission intent, upload name, queue id, job revision/state, attempts. |
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
| `library_roots` | Movie/TV roots with Plex/source visible mappings and defaults. |
| `quality_profiles` | Policy JSON, version, upgrades/default/enabled flags. |
| `indexer_capability_profiles` | Immutable commissioned capability profile (versioned). |
| `indexer_commissioning_runs`, `indexer_commissioning_steps` | v1/v2 commissioning lifecycle + steps. |
| `indexer_budget_state` | Rolling query/grab windows per indexer. |
| `indexer_request_reservations` | Durable per-call reservations (schema 30). |
| `indexer_operation_status` | Latest persisted operation outcome/cooldown/failures. |

### Seerr, notifications, import lists, backups, Prowlarr

| Table | Purpose |
| --- | --- |
| `seerr_deliveries` | Webhook/poll receipts + processing state (dedup by `ReceiptKey`; `outcome='requeued'` marks an owner-triggered re-evaluation). |
| `seerr_request_snapshots`, `seerr_request_seasons`, `seerr_request_workflows` | Durable request display state, workflow links, and observed `seerr_server_id`/`seerr_profile_id`/`seerr_root_folder`/`seerr_is_4k` routing provenance. |
| `seerr_library_routes` | Maps a Seerr root folder (instance + media kind + folder) to a Media Manager `library_roots` row and optional `quality_profiles` override. |
| `notification_destinations`, `notification_deliveries` | Owner webhook subscriptions + attempts. |
| `import_lists`, `import_list_snapshots`, `import_list_snapshot_items` | IMDb export lists and review-only snapshots. |
| `backup_records` | Encrypted backup jobs, checksums, verification state. |
| `prowlarr_backup_imports`, `prowlarr_indexer_candidates` | Review-only Prowlarr backup import. |

## 4. Repository (`*Store`) file map

All SQL lives in `internal/postgres`. The package exposes one `Store` with
`Pool *pgxpool.Pool`; services call typed methods. There are 212 `*Store`
methods at the snapshot. Group by file:

| File | Domain (approx. methods) |
| --- | --- |
| `store.go` | Open/Close/Ping, media+workflow creation (14), metadata persistence, error sentinels. |
| `migrate.go` | Migration runner, `CurrentSchemaVersion`, `SchemaVersion`. |
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
| `indexer_capabilities.go` | Capability profile CRUD/activation (19). |
| `indexer_commissioning.go` | v1 commissioning runs/steps (50). |
| `indexer_commissioning_live.go` | v2 live gate + activation (29). |
| `indexer_commissioning_draft.go` | Draft persistence. |
| `notifications.go` | Destinations + deliveries (8). |
| `importlists.go` | Lists/snapshots/items (9). |
| `backups.go` | Backup records + verified downloads (7). |
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

## 6. UI scaffolding warning

`db/schema.ts` is intentionally empty and `db/index.ts` is Cloudflare D1
boilerplate for the UI template (`drizzle.config.ts` uses SQLite). **The real
database is PostgreSQL managed by the Go migrations above.** Do not add
production tables to Drizzle or run `npm run db:generate` expecting a Go
migration; it only generates into `drizzle/`.
