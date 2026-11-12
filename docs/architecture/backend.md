# 02 — Backend reference (Go)

Purpose: a package-by-package map of everything under `cmd/` and `internal/`,
with the key types and functions you will touch. Line counts are approximate and
were taken at the documentation snapshot; use them only to gauge size.

Module: `github.com/media-manager/media-manager` (`go.mod`), Go 1.27.
Non-stdlib dependencies are deliberately few: `github.com/jackc/pgx/v5`
(PostgreSQL), `modernc.org/sqlite` (Prowlarr backup parsing), and the Go 1.27
standard `uuid` package.

Layering (a dependency may only point downward):

```
cmd/media-manager            process entry, wiring, CLI
  └─ internal/httpapi        HTTP transport, auth, request/response mapping
  └─ internal/platform       outbox worker engine
  └─ domain services         acquisition, publication, recycle, intake,
                             adoptionexec, notifications, backups, commissioning
       └─ pure domain        workflows, catalog, releases, packs, subtitles,
                             library, mediainfo, languages, metadata
       └─ internal/adapters  external systems (bounded clients)
       └─ internal/postgres  persistence (the only SQL owner)
```

Rule of thumb from `AGENTS.md`: keep pure rules in the pure packages, keep
external calls in `internal/adapters`, and keep SQL in `internal/postgres`.
When adding an abstraction, prefer a small pure helper over expanding a service.

Every package carries a `doc.go` package comment; `go doc ./internal/<package>`
prints the package role and core invariants without opening files.

---

## cmd/media-manager

`main.go` (299 lines). No tests of its own; behavior is covered by
`internal/httpapi` integration tests.

- `run()` parses `os.Args[1]` as command (`serve` default, `migrate`,
  `version`, `healthcheck`).
- Opens `postgres.Store`, validates config, constructs every service, builds the
  handler map, starts `platform.Worker` and the ticker goroutine, then serves.
- `requiredSecret` / `optionalSecret` implement the `<NAME>` / `<NAME>_FILE`
  secret convention. Use this for any new secret.
- All wiring is here. A new outbox event type = add handler in the map; a new
  service = construct here and pass `*postgres.Store` plus a `ResolveSecret`
  closure.

---

## internal/platform — outbox worker engine

`worker.go` (145 lines) + tests.

```go
type Handler func(context.Context, postgres.OutboxEvent) error
type Worker struct { Store *postgres.Store; Owner string; Handlers map[string]Handler; PollInterval, Lease time.Duration }
func (worker *Worker) Run(ctx context.Context) error
```

- The main worker runs one event at a time and keeps indexer pacing. Backups
  (`backup.create_requested`, `backup.offsite_requested`) run in a separate
  `OutboxLane`, also one at a time, because they take far longer than other
  handlers and touch only their own files and verification database. Do not
  add a type to that lane unless it is equally self-contained.
- Claims one event per iteration, renews the lease while the handler runs,
  completes/defer/fails as described in [overview.md](overview.md#3-the-durable-outbox-the-heart-of-the-system).
- Unknown event type → failed with "no handler registered" and a 30-minute retry.
- `safeError()` only persists errors with a valid `SafeCode()` or the Go type
  name — never raw error strings (they may contain secrets/paths).
- Handlers that need paced deferral return an error implementing
  `RetryAt() time.Time`; the worker then uses `DeferOutbox` (attempts preserved).

If you add a service handler, keep it idempotent: events are at-least-once.

---

## internal/workflows — durable state machine and scope

`model.go` (138 lines), `scope.go` (79 lines), tests.

- `State` constants: `monitored`, `waiting`, `searching`, `candidate_selected`,
  `submitting`, `transport_unknown`, `queued`, `streaming_ready`,
  `import_complete`, `verifying`, `publishing`, `plex_scanning`,
  `plex_confirmed`, `needs_attention`, `held`, `blocked`, `quarantined`,
  `cancelled`.
- `allowedTransitions` map is the single source of legal edges. `Transition()`
  is idempotent for same-state and bumps `Revision`.
- `AttentionRoundExhaustedWaitingNewIdentity = "round_exhausted_waiting_new_identity"`;
  `OwnerAttentionState()` upgrades such `waiting` workflows to
  `needs_attention` for owner-facing reads.
- `VariantScope`/`ParseVariant`: `""`/`default`, `season:NN`, and
  `season:NN:episode:NN` (airing episodes). Never guess an unknown variant.
- `New`/`NewWithClient` create a `monitored` workflow id `uuid.NewV7()`.

---

## internal/catalog — media items and metadata model

`model.go` (438 lines) + tests.

- `MediaKind`: `movie`, `series`; `New()` validates kind/title/identity rules.
- `ExternalIdentity` (`namespace`, `value`, `state`: `verified`/`candidate`/
  `rejected`, `source`); `ValidateIdentities` enforces namespace-specific value
  shapes (e.g. IMDb `tt` form).
- `MediaItem`, `MediaMetadata` (+ aliases, companies, credits, events, networks,
  ratings, sources). `NormalizeMetadata` canonicalizes and validates.
- Metadata model deliberately keeps **provider-separated ratings/sources** with
  provenance; IMDb-through-Simkl is labeled historical evidence, never a direct
  IMDb API response.

## internal/releases — release observation evaluation

`evidence.go`, `title.go` + tests.

- `Observation` = normalized candidate facts from Newznab (`title`, `size`,
  `published`, `attrs`, `native_guid`, indexer identity, `RawFields`).
- `InferTitleFacts` extracts resolution, codecs, source, languages, group, etc.
- `Evaluate(observation, rules)` → `Evaluation` with `Verdict`
  (`eligible`/`held`/`rejected`), score, `Explanation`s, problems.
- `Rank` sorts eligible evaluations: releases the provider did not mark as a bad archive first, then `score`, then the indexer's 30-day share of verified downloads (`RankWith`), then a stable indexer and release key.
- `Rule` kinds: `required`, `preferred`, `upgrade`, ... with `UnknownPolicy`
  (`reject`/`ignore`) for unknown facts. `CanonicalResolution`, `TitleMatches`,
  `TitleContainsYear` are the pure matchers.
- Quality-profile policy JSON is parsed into `[]Rule` in
  `acquisition/search.go` (`parseRules`).

## internal/packs — season-pack expectation and coverage

`coverage.go`, `job.go` + tests.

- `ExpectedEpisode`, `EpisodeKey`, `ObservedAsset`, `ContentGroup`.
- `PackJob` state machine: `submitted` → ... (`ErrInvalidJobTransition`).
- `Evaluate(seriesID, season, expected, assets)` → `Report` with `Verdict`
  (`verified`, `incomplete`, `ambiguous`, ...), `Missing`, `Issues`,
  content groups. `Report.Publishable()` gates publication.
- Exact coverage rule: every expected episode maps exactly once; multi-episode
  files are grouped (`pack_group_episodes`), never double-counted.
- A season pack is **one** AltMount job with one immutable expectation set.

## internal/subtitles — language policy and output verification

`policy.go`, `verify.go` + tests.

- `languages.Canonical` / `CanonicalLanguage`: ISO-639 canonicalization; Danish
  variants collapse to `da` with raw evidence retained.
- `Policy` (`required`, `preferred`) parsed from quality-profile JSON via
  `ParsePolicy`; `CanonicalizePolicy` rewrites a profile body to canonical form.
- `Verify(ctx, authorizedRoot, selectedVideos, inventory, policy, probe)`:
  bounded inspection (max 20 000 assets, 256 videos, 5-minute limit) of each
  selected video through `mediainfo` plus strictly matched managed sidecar
  companions (`.srt/.ass/.ssa/.sub/.idx/.vtt`). Produces `Verification` with
  per-video evidence, missing required languages, failure reasons, and
  `managed_companions`.
- Status must be `verified` before publication. Unknown pre-download evidence
  stays neutral; only bounded output inspection counts.

## internal/library — safe filesystem operations and Plex naming

`paths.go`, `naming.go` + tests.

- Containment: `ValidateRelative`, `DestinationRelative`, `ErrUnsafePath`,
  `InspectRegularFile`.
- Naming: `RenderMovie(MovieName)` and `RenderEpisode`/`RenderEpisodeSet`
  produce Plex-compatible folder/file names (identity + year + optional release
  token). Series prefers TVDB identity, movies prefer TMDB (`publication.namingIdentity`).
- Symlinks: `ValidatePlan`, `PreflightBatch`, `PublishSymlink`, `PublishBatch`,
  `MoveSymlink`, `RemoveSymlink` — every operation checks the expected target.
- Regular files: `MoveRegularFile`, `RemoveRegularFile`, `TransferRegularFile`
  (cross-root copy to a temp file, SHA-256 verify, atomic rename, source removal
  last) with size/mtime fingerprints.
- Used by publication, recycle, and adoptionexec. Never bypass it with
  `os.Rename`.

## internal/mediainfo — bounded ffprobe evidence

`probe.go` + tests.

- `Probe.InspectWithin(ctx, authorizedRoot, relative)` runs `ffprobe` under
  containment with output limits; `Probe.Inspect(ctx, path)` for adoption.
- `Evidence` (schema version 1) contains container/video/audio/subtitle
  `Track`s. Parse validates JSON strictly; `ErrOutputLimit` stops runaway output.
- ffprobe is available in the API image (Dockerfile installs `ffmpeg`).

## internal/metadata — direct provider enrichment

`service.go` (280 lines) + tests.

- `Service{TMDB, TVDB, Simkl}` with `Result` (rich metadata + identities +
  provenance).
- Owner refresh performs only exact-ID, bounded detail calls. Merge rules:
  TMDB primary descriptive source; TVDB supplements exact movie/TV facts; Simkl
  ratings/lifecycle; IMDb identity is exact but ratings via Simkl are labeled
  `via_simkl`. No scraping, no search-driven enrichment.
- Provider states reported to the UI: `not_configured`, `direct_configured`,
  `seerr_fallback`, `identity_only`, `via_simkl_when_available`.

## internal/languages

`language.go`: `Canonical(value)` → canonical ISO-639 code. Tiny pure package.

---

## internal/indexers — pacing, budgets, scheduler

10 files, ~2 800 lines. No network I/O.

- `Scheduler(globalLimit, store)` is shared by runtime search and the
  commissioning lab. Main wires `NewScheduler(4, store)`: at most four
  requests in flight across indexers. Viewer stream lists (`WorkloadBrowse`)
  draw from their own pool of `BrowseConcurrency` (16) slots instead, so a list
  that asks every indexer at once is not queued behind background searches.
- `Budget` = rolling query/grab window (`NewznabBudget(minimumIntervalMS,
  queryLimit, grabLimit, windowSeconds)`). Conservative fallback 1 000 queries /
  100 grabs per 24h (`ConservativeQueryLimit` etc.), in addition to a ≥3s
  minimum interval and one in-flight request per indexer.
- `Permit`, `RecordResult`, `CooldownError`, `RequestBudgetError`,
  `CommissioningGateBlockedError`; failures have `FailureScope`.
- Persistence contract: `SnapshotStore`, `ReservationStore`,
  `AuthoritativeStore` interfaces; `Snapshot`/`ComputeOutcomeEffect` are pure
  state transitions. Durable reservations live in
  `indexer_request_reservations`.
- `ParseRetryAfter`, `PacingFloor`, `MarkLanePacingFloor` implement header
  respect and lane floors.
- `WorkloadBrowse` is reserved for viewer-facing searches. It waits out the
  remaining per-indexer minimum interval while retaining the lane, so active
  background recovery cannot repeatedly take the next pacing slot. Failure
  cooldowns and intervals longer than the bounded browse wait still defer.
- `WorkloadFeed` is the newest-uploads reader. Like browse it waits out the
  remaining minimum interval (bounded) and never a failure cooldown; unlike
  background work it is limited by the whole query limit, and the reader keeps
  itself to a tenth of it, counting every page a read may take, through its
  read interval.
- `SearchPageLimit(ownerStated, advertised)` (`page_limit.go`) is the page
  size one request asks for: the owner's `page_limit`, else the provider's
  advertised maximum, else 100, never above 500.

---

## internal/acquisition — search, retrieval, reconciliation

15 files, ~3 736 lines.

- `SearchService.Handle` / `HandleDiscovery` (`search.go`):
  - Loads `SearchContext`; validates round snapshot equality
    (`OpenRound.PolicyVersion`, `UpgradeBaseline`); rejects stale revisions.
  - Airing partial seasons dispatch per-episode workflows
    (`DispatchAiringSeason`) and stop pack search until complete.
  - Per indexer: unmarshal config, resolve secret, configure budget, build
    capability-profile plan (or legacy dialect), execute **one** paced search,
    infer/evaluate observations, apply blocklist + workflow exclusions +
    media-match constraints + upgrade baseline, persist.
  - Recovery mode is **evidence only**: never selects/submits, retries with
    `RetryAt` when no recovery indexer is available.
- Paging (`search_pages.go`): a search reads at most three pages and 1 000
  releases per indexer. It continues after a (nearly) full page, or after a
  short page while the provider's stated total (`newznab:response total`) is
  clearly larger than what was read. A failed, deferred or unaffordable
  follow-up page keeps the pages already read. `searchAllPages` also reports
  whether everything was read.
- Shared season answers (`season_share.go`, `postgres/season_search.go`): an
  episode search in a season with at least three open episode workflows sends
  the same `tvsearch` request without `ep`, asking with one of the title's
  names per 12-hour slot for every episode (`seasonTitle`), and stores the indexer's complete
  answer in `season_search_responses` for 12 hours; the season's other
  episodes reuse it (indexer outcome detail `season search` / `reused season
  search`). Each episode keeps only the releases its scope rules do not reject
  and evaluates them under its own rules; reused releases become its own
  candidates with the provider's download locator. An answer is not reused by
  a workflow created after it, after a newest-uploads match for that workflow,
  or after an owner search; an incomplete answer (more results than the page
  bound) is never shared and the episodes search on their own (invariant S21).
- Date-named episodes (`episode_numbering.go`): an episode of a series
  numbered by broadcast year whose air date is more than 180 days old rotates
  through three requests, one per complete search: the Newznab daily-episode
  search (series IDs, `season` = air year, `ep` = `MM/DD`, no name), the air
  date as text under the shortest saved Latin-script name
  (`shortSearchTitle`), and the scoped catalogue request. A daily-episode
  request is never widened to a season answer. Recent episodes alternate
  between the scoped request and the ID-only request as before.
- Danish group record (`danish_groups.go`, `postgres/danish_groups.go`): each
  release group's measured Danish outcomes (`output_verified` versus
  `subtitle_requirements_unverified`, 90 days, profiles requiring Danish,
  titles not originally Danish, names without a Danish clue), cached for 30
  minutes and kept apart for Danish-audio profiles. `PersistSearch` ranks
  releases without a Danish clue by the group's tier
  (`RankOptions.DanishGroupShare`); `holdUnlikelyDanishGroups` holds an
  eligible release without a clue from a group with ≥ 20 downloads and ≤ 5 %
  Danish (invariant S22).
- Retry after a failed release (`search_recent_answer.go`): a complete
  targeted search that selects a release keeps its per-indexer candidate
  evidence in `search_recent_answers`. The search event written after a
  release-attributable failure or a pre-submission package rejection carries
  `reuse_recent_answer`; it reuses that evidence for every indexer (detail
  `reused recent search answer`) when the answer is at most two hours old, in
  the same round and indexer set, and no newest-uploads match or owner search
  came after it. The evidence is validated like partial retry evidence and
  re-evaluated under current rules and exclusions; otherwise every indexer is
  asked as before (invariant S23).
- `FeedWatcher` (`feed_watch.go`): every minute it reads the newest uploads
  (`t=search` without a query, movie and TV categories) of each enabled,
  commissioned Newznab indexer whose read is due, through the paced client as
  `WorkloadFeed`. Uploads not seen before are matched against all waiting,
  held and monitored (a new episode before its first scheduled search)
  workflows with the search's own checks (`TitleIndex` lookup, then
  `workflowScopeRules` and `constrainMediaMatch`; only `eligible` counts).
  Matches are stored and `Store.AdvanceFeedMatches` brings the workflow's own
  scheduled search forward: never within 30 minutes of its last search, at most
  ten workflows a minute, never a search that is leased or continues an
  unfinished one. A waiting workflow without a scheduled search (round
  exhausted) gets the paced discovery of `RequestWorkflowDiscovery`, audited as
  actor `worker`. **The reader never selects or submits a release**; the
  search it brings forward does what every search does. When that discovery
  holds a newly observed eligible release (already `ready` at the match, or
  found by the discovery the match brought forward), the same pass opens the
  next round through `openNextAcquisitionRound`, the transaction behind the
  owner's `POST .../acquisition-rounds`, with every guard of that action
  (waiting workflow, exhausted round, eligible untried release, unchanged
  policy, retained-output limit). It is recorded as actor `worker` (transition
  and `workflow.acquisition_round_opened` audit event), happens at most once
  per workflow per 24 hours, and a refusal settles the match as
  `not_applicable` without writing anything else. Each pass then also opens
  that round for titles whose own scheduled rescan found such a release
  (`OpenRescanFoundRounds`, transition reason `rescanRoundReason`, audit
  reason `rescan_found`), under the same guards and the same once-per-24-hours
  limit, shared with the match path; a refusal only stores the discovery
  generation in `automatic_round_refused_generation`, so that discovery is
  not asked again. `FEED_OPEN_ROUNDS=off` leaves every new round to the owner. The read interval
  moves between 5 and 20 minutes with the share of new uploads per read and
  never below `window × 3 pages × 10 / query_limit` (a tenth of the query limit
  at three pages a read); a failing list backs off from one
  hour to one day. A read follows further pages while a whole page is new, up
  to ten; a read that never reaches an upload seen before counts as a gap
  (`gap_reads_total`, logged, next read at the lower bound). The newest 1,000
  upload IDs are saved with the state (`recent_guids`), so a read after a
  restart goes on from the last one. `FEED_WATCH=off` disables it.
- `SearchPlan`/`PlanSearch` (`search_plan.go`): pure translation of a validated
  `newznab.CapabilityProfile` + verified identities into one primary request and
  strictly bound fallbacks (`FallbackFor(trigger)`).
- `RetrievalService.Handle` (`retrieve.go`): fetch NZB (strict validation,
  exact package identity), record submission intent, upload to AltMount,
  reconcile ambiguous results by filename.
- Pre-retrieval NFO check (`nfo_check.go`): when the round requires a
  subtitle-or-audio language, the title is not originally in it, and the
  provider gave no subtitle/audio languages of its own but advertised an NFO
  (an `info` link on the configured endpoint naming this release, such as
  `t=getnfo` or DrunkenSlug's `t=info`, or `nfo=1`), one interactive query reads
  the NFO before the NZB. `newznab.ParseNFOLanguages` reads MediaInfo Text/Audio
  sections and release-group lines (`SUBTiTLES....: DANISH`) with a fixed
  language list. Only a completely stated subtitle list without the language,
  plus a completely stated audio list without it (or no hint of it in the
  release name), excludes the exact provider release through
  `RecordPreSubmissionRejection` (kind `subtitle_verification`, no
  verification row, so cross-posts are not excluded); no grab, job or round
  slot is used. Anything partial, unknown or failing proceeds as before. Each
  candidate is checked once (`release.nfo_checked` audit event), because the
  NFO request takes the pacing slot and the grab usually runs on the deferred
  retry. "No such item"/"No such function" answers count as no results, never
  as indexer failures.
- `AltMountReconcileService` (`reconcile_altmount.go`):
  - If the queue id is unknown, list by stable upload name (30-minute
    visibility window; `altmount_identity_unresolved` / `_ambiguous` otherwise).
  - Poll queue every 10s; enumerate output under the configured read-only
    mapping; on `completed` allow a 5-minute grace for output availability.
  - On `import_complete`: persist inventory, evaluate movie (exactly one
    dominant file) or season pack; return a release-attributable failure for
    empty/incomplete/ambiguous output; otherwise probe subtitles and record
    verification. Only `verified` calls `CompleteImport`.
  - On `failed` (`altmount_auto_retry.go`): a message that names a release
    defect (`releaseAttributableQueueFailure`) excludes the release at once.
    Any other failure never stops at owner attention: Media Manager asks
    AltMount to retry the same queue item (`POST /api/queue/{id}/retry`, the
    same NZB, no indexer request) after 10 minutes, 1, 4 and 12 hours, at most
    4 times for timeouts/connection failures and 2 times otherwise. Retries
    are paced to one per 90 seconds across all jobs, because AltMount runs a
    retried item outside its worker pool. When the retries are used, or the
    item is gone from AltMount, `GiveUpAltMountImport` excludes the release
    (`import_failed`) and the round tries the next one.
  - `ResumeAltMountAttention` (every 5 minutes, 10 at a time) hands failed
    imports that stopped at `altmount_job_attention` before this to the same
    retry, once per job.
  - `HandleRetainedOutputReverification`: reuses the frozen inventory, makes no
    AltMount request, re-runs inspection with the original policy.
- Constants: `altMountSubmissionVisibilityWindow = 30m`,
  `altMountCompletedOutputGrace = 5m`, `altMountAutoRetrySpacing = 90s`.

## internal/publication — symlink publication and Plex

`service.go`, `plex.go`, `remove.go` + tests. See
[workflows.md](workflows.md#8-publication-and-plex) for the full flow.

- `Service.Handle`: `LoadPublicationContext` → `buildPlans` → `PreflightBatch`
  → `PreparePublication` → `MarkPublicationPublishing` → `PublishBatch` →
  `CompletePublication`. Any filesystem failure → `FailPublication` (attention),
  never a partial "success".
- `buildPlans`: requires media year + verified Plex-supported identity; maps
  AltMount source to Plex-visible root; appends the unique release token to the
  filename; creates one plan per verified asset + subtitle companion.
- `PlexService`: `HandleScan` (refresh library), `HandleConfirm` (exact
  GUID+path confirmation), `HandleRecycleRefresh`, `HandleDisplacedRefresh`,
  `HandleAdoptedRestoreConfirm`. `ClientFor(integration)` builds the adapter.

## internal/recycle — 30-day retention

`service.go` (97 lines).

- `HandleMove` (planned → retained), `HandleRestore` (link direct; real file
  only for `upgrade_displacement` → `restore_planned` → Plex confirm), and
  `HandlePurge` (only `purge_planned`). All operations go through
  `internal/library` fingerprints/targets.
- The 30-day `purge_after` is computed in `internal/postgres/recycle.go`
  (`now + 30*24h`). Link purges are automatic after retention; adopted real
  files require a second exact owner confirmation.

## internal/adoption — read-only scanner

`scanner.go` + tests. Bounded discovery of an existing library root, companion
classification (recognized vs unknown), per-item review data. **Read-only**:
scanning never moves files.

## internal/adoptionexec — journaled adoption execution

`service.go` + tests.

- `HandleExecute`: loads `AdoptionExecutionContext`, builds operations once
  (`buildOperations`), journals every path/root identity before mutation,
  executes same-root renames or cross-root transfers, recovers retries, rolls
  back in reverse order on failure.
- `HandlePlexConfirm`: confirms renamed paths in Plex, transactionally creates
  current-file + Plex provenance, and only then applies reviewed monitoring
  (one fresh workflow per exact scope). Immediate search additionally needs the
  adopted replacement phrase + commissioned paced Newznab source + TV episode
  snapshot.
- Interfaces `PlexClient` and `MediaInfoProbe` are injected from `main.go` so
  the package stays testable.

## internal/intake — Seerr reconciliation

`seerr.go`, `seerr_status.go` + tests.

- `SeerrService.Handle`: reads the webhook/poll payload, fetches the Seerr
  request, relies on the delivery's unique keys for deduplication (see
  `seerr_deliveries`), verifies TMDB/TVDB/IMDb identities,
  enriches through `metadata.Service`, then persists request + seasons +
  workflows transactionally (movies `default`, series per selected season).
- Handles deletion/revocation: a deleted request, or one whose Seerr status
  is pending, declined or failed, marks the delivery `observed_not_approved`;
  if retrieval already started, it surfaces `needs_attention`
  (`seerr_request_revoked`).
- Seerr's own Completed status (5), which Seerr sets once it sees the media
  available, is not a revocation: the delivery is `already_available`, linked
  workflows keep their state, and no new work is created.
- `SeerrStatusService.Handle`: pushes availability only after exact Plex
  confirmation; partial TV requests keep unconfirmed seasons `requested`.
  `ErrSeerrStatusIdentityMismatch` protects against drift.
- `ImportListService.Handle` (`import_lists.go`, event
  `import_list.sync_requested`) reads a followed public MDBList list through
  `adapters/mdblist` and records every title in `import_list_entries`. Each
  new title (and each one that failed before) is first matched against the
  catalogue by verified TMDB/IMDb (and, for series, TVDB) identity: a known
  title becomes `in_library`, one the owner removed `removed_earlier`, and
  neither is touched. Otherwise it goes through the same path as **Tilføj**:
  `metadata.Service.Discover`, every season from TMDB's season list except
  specials (0), each frozen from TMDB's exact episode list (seasons with no
  episodes yet are skipped), the list's film or series folder and quality
  profile or the defaults, `CreateManualMedia` with an immediate search. A
  title that cannot be added now is `failed` with a stable code and tried
  again on the next read. Titles that leave the list are never removed. The
  handler runs on its own outbox lane, so a long list never holds up the main
  worker; reads are due every 6 hours (`QueueDueImportListSyncs` on the
  five-minute ticker), and a list that could not be read is retried after an
  hour.

## internal/notifications

`service.go`: durable webhook deliveries. Endpoints validated HTTPS-by-default,
credentials resolved by environment reference (`ResolveSecret`), audit detail
and secrets excluded from payloads. Retries stay visible
(`notification_deliveries`).

## internal/backups

`service.go`, `crypto.go` + tests.

- `Service.Handle` runs `pg_dump -Fc` into the configured directory, then
  encrypts with chunked AES-256-GCM (`Encrypt`/`Decrypt`, magic `MMBAK001`,
  1 MiB chunks, random 8-byte nonce prefix + counter nonce).
- `KeyReference` names the env var; key must be base64 32 bytes (`ParseKey`).
- Job records track state/attempts/size/sha256/schema version; verification
  restores into an isolated temporary database and requires the exact schema
  version. Download only serves checksum-matching encrypted files.

## internal/importlists

`parser.go`: strict IMDb CSV export parsing (identity/year/duplicate bounds) →
review-only snapshots. No monitoring is applied by parsing alone. Followed
MDBList lists are handled by `intake.ImportListService` (above).

## internal/prowlarrbackup

`parser.go`: bounded ZIP + SQLite parsing of a Prowlarr backup into disabled
Newznab candidates, preserving query/grab ceilings. No runtime Prowlarr
connection. Owner must place extracted keys into secret env names manually.

## internal/commissioning — Newznab commissioning (v1 + v2 bridge)

`newznab.go`, `live_v2.go` + tests.

- `NewznabService.Commission`: validates a staged Newznab instance: CAPS +
  one random negative query, ≥3s paced through the shared scheduler, then
  enables only on success.
- `LiveCAPSService`: implements `httpapi.CommissioningV2Service` by bridging the
  pure `commissioningv2` domain to the live executor (the only place a live
  executor is constructed).

## internal/commissioningv2 — owner-authorized commissioning (pure + live)

30 files, ~9 562 lines, the most intricate package.

- `types.go`: `RunState` (`awaiting_caps_authorization` → `caps_authorized` →
  `awaiting_category_review` → `awaiting_search_authorization` →
  `searches_authorized` → `ready_for_draft` → `completed`; plus owner_attention,
  cancel, stale, safety_blocked), `ValidRunTransition`, `StepPhase`,
  limits (`MaximumAPICalls = 5`, `MaximumSearchCalls = 4`).
- Pure planning/evidence: `draft.go`, `probe_plan.go`, `search_proof.go`,
  `evidence.go`, `fixture.go`, `digest.go`, `fingerprint.go`,
  `secret_guard.go`, `live_classification.go`.
- Live pieces: `live_execute.go`, `live_executor.go`, `live_search.go`
  (bounded, explicitly owner-authorized CAPS/search steps).
- The package is intentionally transport-free at the domain layer; HTTP access
  goes through `httpapi/commissioning_v2.go` and the bridge in
  `internal/commissioning/live_v2.go`. See [workflows.md](workflows.md#14-indexer-commissioning).

## internal/servarr — Radarr/Sonarr compatibility surface

2 files, ~240 lines. Pure JSON shapes and helpers for the Radarr/Sonarr API
that Seerr speaks when Media Manager replaces the *arr applications:
`SystemStatus`, `QualityProfile`, `RootFolder`, `Movie`, `Series`, `Episode`,
`CommandResponse`, `SyntheticID`, `Slug`, and `AutoRootPath`/`IsAutoRootPath`
(the single synthetic `/srv/media/Auto` root folder Seerr is shown; intake
routes Auto requests by metadata in `internal/postgres/seerr_routes.go`). No I/O. The handlers live in
`internal/httpapi/servarr.go` and the data in `internal/postgres/servarrstore`.

## internal/httpapi — HTTP transport

`server.go` (1 979 lines), `commissioning_v2.go` + tests.

- `Config` carries owner token, Seerr flag/secret/instance, version/commit,
  metadata provider + enricher + states, commissioning functions, backup info.
- `New` rejects tokens < 32 bytes and panics-free builds `http.ServeMux` routes.
- `Handler()` wraps the mux with `security()` (nosniff, DENY, no-referrer,
  `Cache-Control: no-store`, CSP `default-src 'none'`).
- `requireOwner` hashes both sides with SHA-256 and uses
  `subtle.ConstantTimeCompare`.
- `requireOwnerMutation` (commissioning v2) additionally requires configured
  service + trusted `Origin`, same-origin Fetch Metadata, and JSON content type.
- `requireServarr` guards `/radarr/api/v3/*` and `/sonarr/api/v3/*` (and the
  4K-edition `/radarr4k` and `/sonarr4k` twins) with the
  dedicated `SERVARR_API_KEY` (`X-Api-Key` header or `apikey` query parameter);
  an empty key disables the surface with `503`.
- Helpers: `writeJSON` wraps success in `{ "data": ... }`; `writeError` emits
  `{ "error": { "code", "message" } }`; `decodeJSON` uses `DisallowUnknownFields`
  and a request size limit.
- `seerrWebhook` is the only unauthenticated-by-owner route and uses
  `seerr.AuthenticateWebhook` with the instance secret.

Full route list: [api.md](api.md).

## internal/postgres — persistence

76 files, ~29 400 lines, 221 `*Store` methods. The only package that contains
SQL. See [database.md](database.md) for the file → domain map, table
catalog, and concurrency rules.

---

## internal/adapters — bounded external clients

Every adapter takes an optional `http.RoundTripper` for tests, validates config,
sets explicit timeouts, size limits, and never retries outside scheduler policy.
`internal/adapters/gmd` exists but is intentionally empty (no GMD runtime
dependency).

### altmount (Adapter to the download client)

- `Client` interface + `HTTPClient`; session/API-key authentication, anonymous
  admin support verified live before queue access.
- `QueueQuery`, `QueuePage`, `QueueItem`, `QueueID`, `QueueStatus`
  (`pending`, `downloading`, `completed`, ...), `Phase` reconcile enum.
- `Reconcile(ReconcileInput)` maps queue + output observation to a `Phase`
  (`queued`, `paused`, `processing`, `streaming_ready`, `import_complete`,
  `failed`, `cancelled`, `needs_attention`, `removed`, `unknown`).
- `OutputMapping{ReportedRoot, LocalReadOnlyRoot}` + `Enumerate` produce an
  `Inventory` bounded under the read-only mount; `ErrOutputMapping` on escape.
- `UploadRequest`, `Session`, `User`, `RemoveResult`, `AmbiguousSubmissionError`.

### newznab (Indexer protocol)

19 files. Security-critical.

- `Client` implements CAPS, search (movie/tvsearch/general + extended), and
  retrieval (`t=get`/locator) with scheduler integration.
- `CapabilityProfile` (schema v1) records advertised search modes, allowed
  params, categories, bindings (`imdb_numeric`, `tmdbid`, `tvdbid`, ...),
  recipes and fallback triggers; `ValidateCapabilityProfileForActivation`.
- `StrictCommissioningCAPS`, `CapsSnapshot`, `CAPSModeObservation`,
  `CapabilityAssessment` drive commissioning.
- `ParseSearch` → `[]releases.Observation`; `ProviderContractNZBFinderV1` and
  `ProviderContractAcceptsEndpoint` enforce verified download locators.
- `PackageIdentity(body)` = SHA-256 of the exact NZB bytes.
- `Sanitize*` functions strip credentials from observations/raw fields before
  persistence; `ValidStructurallySafePersistedText` guards stored text.
- Redirect policy: max 2 hops, workflow guard rechecked per hop, only validated
  public addresses pinned, TLS hostname verification preserved, proxies/reuse
  disabled, cross-origin credentials/Referer stripped.

### plex

- `Client` confirms exact items by `GUID` + `Expected`/`ExpectedSeries` paths.
- The section title filter is only a fast path. Plex matches it against its own
  (often localized, e.g. Danish) metadata title, so when it finds nothing the
  client walks `/library/sections/{key}/folder` from the section `Location`
  that contains the expected path, matching each folder by exact name and
  following only that section's own numeric `parent` keys. Series use the
  matched episode's `grandparentRatingKey` and then the same show-identity and
  every-leaf checks. Title is never part of the proof.
- `Confirmation`, `Section`, `Location`, `Item`, `Media`, `Part`, `EpisodeLeaf`
  model the server response. A timeout or missing path is a failure, never
  assumed true.

### seerr

- `Client` for requests, seasons, media status; `ParseWebhook`,
  `AuthenticateWebhook` (constant-time secret check), `Identity`/`ExternalIDs` for TMDB/TVDB/IMDb.
- `MediaStatus` values (`available`, `partial`, ...). Status updates go through
  the supported media-status API only after exact Plex confirmation.

### mdblist

`client.go`: reads a public list as `https://mdblist.com/lists/{owner}/{slug}/json`
(no account or key). `importlists.ParseMDBListURL` accepts only mdblist.com list addresses (any letters, also æ, ø and å, plain or percent-encoded)
(or `owner/slug`); redirects are not followed, the answer is capped at 8 MiB
and 5,000 titles (a longer list is refused, never cut short), and only films
and shows with a TMDB number are kept.

### metadataproviders

- `TMDBClient`, `TVDBClient`, `SimklClient` with `Lookup` interface and
  `Contribution`. Explicit config structs (`TMDBConfig`, `TVDBConfig`,
  `SimklConfig`), bounded exact-ID calls, redirect/identity drift rejection.

---

## Tests at a glance

- 114 Go test files, 573 `Test*` functions. `go test ./cmd/... ./internal/...`.
- Integration tests that need PostgreSQL skip unless
  `MEDIA_MANAGER_TEST_DATABASE_URL` is set (guard in
  `internal/postgres/integration_test.go`).
- The highest-risk packages have focused table tests (`releases`, `packs`,
  `subtitles`, `library`, `indexers`, `newznab`, `altmount`, `commissioningv2`)
  plus PostgreSQL lifecycle tests (`*_integration_test.go`).
- Run `-race`; CI runs `go test -count=1 -race ./cmd/... ./internal/...`
  against PostgreSQL 18.

### Interactive streaming browse service

`internal/acquisition/browse.go` reuses SearchService's provider request planning,
media matching, Newznab transport and durable scheduler for the private addon.
It keeps bounded expiring choices in memory, writes each stream list's choices
to `streaming_choices` (`browse_choices.go`) and reads a choice back from there
after a restart or eviction, and permits retrieval only through an exact choice.
A stored choice is usable only while its indexer is enabled with the same
configuration fingerprint. No acquisition workflow or library publication is created.
Each provider search runs in its own goroutine (up to `BrowseConcurrency`) so per-indexer
pacing waits do not serialize the other providers; the scheduler still enforces
the global and per-indexer limits. Optional details/NFO enrichment is capped at
five seconds and falls back to the extended-search facts when it is slow or
unavailable.
`internal/adapters/newznab/media.go` reads explicit media attributes and bounded
MediaInfo-style NFO fields while keeping subtitle/audio provenance separate.
Primary browse searches use `WorkloadBrowse`; acquisition search and recovery
keep their existing fail-fast pacing behavior.
