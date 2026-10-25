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

- Claims one event per iteration, renews the lease while the handler runs,
  completes/defer/fails as described in [01-architecture.md](overview.md#3-the-durable-outbox-the-heart-of-the-system).
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
- `Rank` sorts eligible evaluations (`score`, age, size tie-breakers).
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
  commissioning lab. Main wires `NewScheduler(4, store)`.
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
- `SearchPlan`/`PlanSearch` (`search_plan.go`): pure translation of a validated
  `newznab.CapabilityProfile` + verified identities into one primary request and
  strictly bound fallbacks (`FallbackFor(trigger)`).
- `RetrievalService.Handle` (`retrieve.go`): fetch NZB (strict validation,
  exact package identity), record submission intent, upload to AltMount,
  reconcile ambiguous results by filename.
- `AltMountReconcileService` (`reconcile_altmount.go`):
  - If the queue id is unknown, list by stable upload name (30-minute
    visibility window; `altmount_identity_unresolved` / `_ambiguous` otherwise).
  - Poll queue every 10s; enumerate output under the configured read-only
    mapping; on `completed` allow a 5-minute grace for output availability.
  - On `import_complete`: persist inventory, evaluate movie (exactly one
    dominant file) or season pack; return a release-attributable failure for
    empty/incomplete/ambiguous output; otherwise probe subtitles and record
    verification. Only `verified` calls `CompleteImport`.
  - `HandleRetainedOutputReverification`: reuses the frozen inventory, makes no
    AltMount request, re-runs inspection with the original policy.
- Constants: `altMountSubmissionVisibilityWindow = 30m`,
  `altMountCompletedOutputGrace = 5m`.

## internal/publication — symlink publication and Plex

`service.go`, `plex.go`, `remove.go` + tests. See
[06-workflows.md](workflows.md#8-publication-and-plex) for the full flow.

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
  request, deduplicates by `ReceiptKey`, verifies TMDB/TVDB/IMDb identities,
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
review-only snapshots. No monitoring is applied by parsing alone.

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
  `internal/commissioning/live_v2.go`. See [06-workflows.md](workflows.md#14-indexer-commissioning).

## internal/servarr — Radarr/Sonarr compatibility surface

2 files, ~240 lines. Pure JSON shapes and helpers for the Radarr/Sonarr API
that Seerr speaks when Media Manager replaces the *arr applications:
`SystemStatus`, `QualityProfile`, `RootFolder`, `Movie`, `Series`, `Episode`,
`CommandResponse`, `SyntheticID`, `Slug`, and `AutoRootPath`/`IsAutoRootPath`
(the single synthetic `/srv/media/Auto` root folder Seerr is shown; intake
routes Auto requests by metadata in `internal/postgres/seerr_routes.go`). No I/O. The handlers live in
`internal/httpapi/servarr.go` and the data in `internal/postgres/servarr.go`.

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
- `requireServarr` guards `/radarr/api/v3/*` and `/sonarr/api/v3/*` with the
  dedicated `SERVARR_API_KEY` (`X-Api-Key` header or `apikey` query parameter);
  an empty key disables the surface with `503`.
- Helpers: `writeJSON` wraps success in `{ "data": ... }`; `writeError` emits
  `{ "error": { "code", "message" } }`; `decodeJSON` uses `DisallowUnknownFields`
  and a request size limit.
- `seerrWebhook` is the only unauthenticated-by-owner route and uses
  `seerr.AuthenticateWebhook` with the instance secret.

Full route list: [04-api.md](api.md).

## internal/postgres — persistence

76 files, ~29 400 lines, 221 `*Store` methods. The only package that contains
SQL. See [03-database.md](database.md) for the file → domain map, table
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
  `AuthenticateWebhook` (constant-time secret check), `ReceiptKey`
  (dedup hash), `Identity`/`ExternalIDs` for TMDB/TVDB/IMDb.
- `MediaStatus` values (`available`, `partial`, ...). Status updates go through
  the supported media-status API only after exact Plex confirmation.

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
It keeps bounded expiring choices in memory and permits retrieval only through
an exact choice. No acquisition workflow or library publication is created.
`internal/adapters/newznab/media.go` reads explicit media attributes and bounded
MediaInfo-style NFO fields while keeping subtitle/audio provenance separate.
Primary browse searches use `WorkloadBrowse`; acquisition search and recovery
keep their existing fail-fast pacing behavior.
