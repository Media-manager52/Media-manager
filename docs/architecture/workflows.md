# 06 — Domain workflows and rules

Purpose: the end-to-end behavior of Media Manager, in execution order, with the
exact rules that make each step truthful. This is the document to read before
changing acquisition, verification, publication, adoption, or recycle code.

Vocabulary is in [09-glossary.md](../reference/glossary.md). State machine is in
[01-architecture.md](overview.md#4-workflow-state-machine).

## 1. Scope model

A **workflow** is one media item + one acquisition scope:

| Variant | Meaning | Example |
| --- | --- | --- |
| `default` | A movie (or a whole-series operation that is not season-scoped). | movie workflow |
| `season:NN` | One complete-season pack, one AltMount job, one immutable expectation. | `season:01` |
| `season:NN:episode:NN` | One airing episode, its own expectation/job/round/publication. | `season:01:episode:02` |

`internal/workflows/scope.go` is the only parser/renderer. Unknown variants fail
closed. A series that is currently airing gets per-episode workflows dispatched
from the season workflow (`DispatchAiringSeason`); the pack workflow does not
search until the season is complete and entirely missing. A completed season
that is entirely missing but whose searches have only ever observed episode
releases (and no season pack) dispatches the same per-episode path, because
Danish TV is posted per episode and otherwise waits forever for a pack that is
never posted.

## 2. End-to-end map

```
intake ──▶ workflow(monitored) ──▶ search rounds ──▶ candidate_selected
                                                         │
                                                     retrieve
                                                         │
                                        AltMount submit ─┴─▶ reconcile
                                                         │
                                             ┌───────────┴───────────┐
                                      verified output          attributable failure
                                             │                       │
                                         publish              exclude release+package
                                             │                 consume round slot
                                          Plex scan ──▶ confirm ──▶ plex_confirmed
                                             │                       │
                                      displace old gen        Seerr availability
                                      move adopted file
```

Every arrow is an outbox event, and every handler is idempotent and revision
checked.

## 3. Intake

### 3.1 Manual add (`POST /api/v1/media`)

`httpapi.createMedia` validates kind/title/identities/metadata, resolves the
default (or overridden) library root, quality profile, and download client, then
calls `Store.CreateMediaWorkflow` which writes, in one serializable transaction:

- `media_items`, `external_identities`, `media_metadata`;
- `workflows` + first `acquisition_rounds` row (round 1, policy snapshot,
  upgrade baseline = current link versions for the same media+variant);
- `audit_events` (`media.created`);
- when `search_immediately`, a `workflow.search_requested` event.

Series requests include an exact episode snapshot per season
(`season_monitoring`, expected episodes) so pack verification has frozen
expectations from the start. A movie needs a year and a verified Plex-supported
identity before publication (not necessarily before creation).

### 3.2 Seerr intake (webhook + polling)

- **Webhook**: `POST /api/v1/integrations/seerr/webhook`, authenticated with
  `SEERR_WEBHOOK_SECRET`; body ≤256 KiB; `RecordSeerrDelivery` dedups by
  `X-Seerr-Event-ID`/payload receipt; queues `seerr.reconcile` once.
- **Polling**: the ticker goroutine queues active Seerr polls and confirmed
  status syncs every 5 minutes (`QueueActiveSeerrPolls`,
  `QueueConfirmedSeerrStatusSyncs`).
- `intake.SeerrService.Handle` fetches the exact request, verifies
  TMDB/TVDB/IMDb identities against Seerr metadata, enriches via the configured
  direct providers, and persists request + seasons + workflows. Movies create a
  `default` workflow; series create one `season:NN` workflow per selected
  season. Availability routing can choose a non-default quality profile by
  canonical original language (`da`), but this is **not** evidence of a Danish
  audio track.
- **Request routing.** A Seerr request carries the `serverId`, `profileId`, and
  `rootFolder` chosen against the configured service. Media Manager stores that
  provenance on `seerr_request_snapshots` and resolves the destination through
  an owner-created `seerr_library_routes` mapping (per instance, media kind, and
  folder, with an optional quality-profile pin). Seerr may also be configured
  against Media Manager's own Radarr/Sonarr-compatible API (see
  [backend.md](backend.md#internalservarr--radarrsonarr-compatibility-surface)):
  in that mode Seerr sees exactly one synthetic root folder per media kind,
  `/srv/media/Auto` (`servarr.AutoRootPath`; the legacy `/media/Auto` spelling
  is also accepted). An **Auto** request delegates both root and profile to
  Media Manager (`resolveSeerrAutoRoute` in `internal/postgres/seerr_routes.go`),
  which classifies the title's metadata in this order and ignores the
  `profileId` Seerr carried:

  | Category | Rule | Movie root | Series root | Profile |
  | --- | --- | --- | --- | --- |
  | kids | genre `Family` or `Kids` | `Børn Film` | `Børn Serier` | enabled profile with no required subtitle language (prefers one routed for `da` originals) |
  | documentary | genre `Documentary` | `Dokumentar` | `Dokumentar Serie` | language routing |
  | holiday | keyword containing `christmas`, or `holiday` | `Julefilm` | `Julekalender` | language routing |
  | danish | reliable original language `da` | `Danske Film` | `Dansk Tv-Serier` | language routing |
  | default | anything else | kind default | kind default | language routing |

  Themed roots are matched by exact `library_roots.name` and must be enabled
  with a Plex library; a missing themed root falls back to the kind's default
  root instead of stranding the request. A request carrying a real
  `destination_root` (older Seerr service rows) is still matched directly (no
  mapping row needed), and there the `profileId` issued by Media Manager
  selects the quality profile ahead of language routing. A
  request with a folder that
  has no enabled route, or whose mapped root/profile is disabled, pauses with
  `needs_configuration` and the exact folder in the reason; it never silently
  publishes into the default library. Saving the route in **Settings →
  Connections → Seerr request routing** requeues those deliveries immediately
  (the requeue marker bypasses unchanged-snapshot dedup). Requests without a
  folder keep the previous default-root behavior.
- **Already-available media.** When a request names a variant (movie
  `default` or a season) that already has a `plex_confirmed` workflow, intake
  attaches the request to that workflow (`owns_workflow = false`) and creates
  no new workflow or download. Whenever a requested variant already has
  Plex-confirmed content (the movie, a season, or any episode of a requested
  season), intake queues one `seerr.status_sync_requested` keyed by the request
  and its snapshot revision (aggregate type `seerr_request`), so it never
  collides with the sync an earlier request queued for the same workflow. If
  every requested variant is confirmed, the delivery outcome is
  `already_available`; a mixed request creates workflows only for the missing
  variants.
- **Snapshot cursor.** Seerr can change a request's availability without moving
  its `updatedAt` cursor. An equal-cursor snapshot is accepted when its payload
  hash changed; an unchanged payload is `stale_ignored`, and an older cursor
  never overwrites the stored snapshot. An approved request whose current
  snapshot never produced any workflow (for example one stranded by an earlier
  intake conflict) is re-evaluated instead of ignored, and the 5-minute poll
  retries such requests with a 24-hour backoff after a `needs_attention` or
  `needs_configuration` outcome.
- A request deleted in Seerr is reconciled: never started → observed, no
  attention; already retrieving → `needs_attention` (never silently cancelled).
  Revocation covers the workflows the request owns and the single-episode
  workflows dispatched from a season workflow it owns (matched through the
  season, since episodes carry no provenance of their own). Pre-retrieval
  episodes are cancelled, retrieving ones need owner attention, confirmed
  ones are untouched, and any workflow another approved request still tracks
  (directly or through its season) is kept.
- Seerr availability is pushed only after exact Plex confirmation; partial TV
  requests keep unconfirmed seasons `requested`. A season acquired through
  single-episode workflows (airing, or episode-only postings) counts as
  available once every episode frozen in its season workflow's latest
  expectation has a Plex-confirmed episode workflow; an empty expectation never
  counts. Seerr has no per-season partial status in this API, so confirmed
  episodes of an incomplete season mark the media partially available. Episode
  workflows carry no request provenance of their own, so both the Plex
  confirmation transaction and the 5-minute bootstrap
  (`QueueConfirmedSeerrStatusSyncs`) follow the season workflow's provenance;
  the bootstrap queues at most one episode per media per tick and skips an
  episode that a later sync for the same media already covers. The season
  workflow itself stays `waiting`: it never gains its own Plex evidence, so it
  is not marked `plex_confirmed`.

### 3.3 Discovery (owner metadata search)

`GET /api/v1/discovery/search` searches Seerr metadata (movie/TV only).
`GET /api/v1/discovery/{kind}/{id}` does an exact detail lookup and enriches
through direct TMDB/TheTVDB/Simkl when configured. Persisted evidence carries
provider-separated ratings/provenance; IMDb ratings obtained through Simkl are
labeled and never presented as a direct IMDb API response. There is no scraping.

### 3.4 Import lists

`internal/importlists` parses an IMDb CSV export into a review-only snapshot.
Rows are classified `pending_review` / `already_cataloged` / `unsupported`; TV
rows are held. **Nothing** is monitored, searched, or downloaded as a result of
an upload.

## 4. Search and selection

Handler: `acquisition.SearchService` (`internal/acquisition/search.go`).

### 4.1 Guards before any network call

- Loads `SearchContext` (workflow, media, policy, indexers, current release,
  open round, exclusions, blocklist).
- Recovery discovery (round exhausted) is evidence-only and uses a generation
  guard (`ValidateRecoveryDiscovery`).
- Non-recovery: if the workflow is already `candidate_selected`/`queued`/
  `submitting`, stop. If the open round's `policy_version` is older than the
  quality profile's current version, the search makes no provider request and
  parks the workflow in `waiting` with `quality_policy_changed`
  (`MarkQualityPolicyReviewRequired`) until the owner applies the policy (§5a).
  If the round's `upgrade_baseline_link_versions` no longer matches the current
  links, fail with owner review required.
- If no commissioned enabled Newznab indexer exists → error (recovery defers
  24h).
- Airing season dispatch may replace the pack search with per-episode scopes.

### 4.2 Indexer calls

For each indexer: unmarshal config → resolve secret by `secret_ref` → configure
the scheduler budget → build the client → build the request. The `q` text uses
scene spelling (`releases.SearchQueryTitle`: Nordic letters transliterated,
punctuation collapsed), because indexers match release names:

1. Prefer the immutable **active capability profile**
   (`searchProfilePlan` → `PlanSearch`).
2. Fall back to the legacy commissioned dialect when no profile is active or it
   cannot satisfy the item.
3. Unknown/unavailable configurations are recorded per indexer, never fatal for
   the others.

Pacing rules (must not be weakened):

- one in-flight request per indexer lane;
- global scheduler bound (main wires 4);
- persisted per-indexer minimum interval, default floor ≥3s;
- rolling query/grab budgets reserved **before** I/O (defaults 1 000 queries /
  100 grabs per 24h; Prowlarr-imported limits preserved);
- `Retry-After` respected; paced deferral uses `DeferOutbox` (attempts are not
  consumed);
- cooldown/backoff per indexer failure scope.

### 4.3 Evaluation

Each observation is normalized (`InferTitleFacts`) then evaluated by pure rules
(`releases.Evaluate`) with these layers applied in order:

1. **Policy rules** parsed from the quality profile (`required`, `preferred`,
   upgrade rules). No rules configured → candidate is `held`.
2. **Subtitle rules** (`appendSubtitleRules`) so pre-download language evidence
   only helps, never lies.
3. **Scope rules** (`workflowScopeRules`) force season/episode correctness.
4. **Media match constraint** (`constrainMediaMatch`) — verified external
   identity or exact title/year match; IMDb IDs normalized to numeric form.
   The title is compared in its release spellings (`releases.TitleSpellings`:
   literal, scene transliteration `Forræder` → `Forraeder`, single-letter
   folding, `&` as `and`, apostrophes dropped); the release must still begin
   with the whole title. A
   provider identity that exactly matches a verified identity satisfies the
   movie year check (release years are often a year off); without one, a
   movie release must name the year. A mismatched identity always rejects.
5. **Blocklist** (`applyReleaseBlocks`) — native or pack blocks reject before
   ranking.
6. **Workflow exclusions** (`applyWorkflowExclusions`) — exact failed
   release/package exclusions.
7. **Upgrade baseline** (`applyUpgradeBaseline`) — a candidate replacing a
   current release must score **strictly higher** under the target policy,
   unless the current release now fails a required property. The current
   release's verified subtitle languages replace title inference; a verified
   output with no subtitles is the observed value `none`, so it fails any
   required language. The current release itself is rejected by exact
   indexer + GUID and by exact release title (cross-posts).

Any observation with a `weak:` native GUID (no retrievable identity) is held —
never selected.

### 4.4 Persistence and outcomes

`Store.PersistSearch` (serializable) stores the run, per-indexer outcomes,
observations, candidates, decisions, and a selection. Then:

- **Selected** → workflow `candidate_selected`, queues
  `release.retrieve_requested`.
- **Nothing eligible** → `waiting`/`held`, with a retry after 30 minutes that
  doubles for each further targeted run in the current round that selects
  nothing (1h, 2h, 4h, 8h, capped at 12h) when there are no prior submissions,
  otherwise the deterministic discovery delay (24h ±20%, see §5). Every search
  queries every indexer, so a flat 30-minute retry spent the rolling query
  budgets on repeat empty searches.
- **All indexers failed** → the search event is deferred/failed with a safe code;
  the workflow is not falsely advanced.

## 5. Acquisition rounds, failures, and discovery recovery

Source: `internal/postgres/acquisition_rounds.go`, `search.go`,
`subtitle_reverification.go`.

- A round snapshots `ACQUISITION_ATTEMPTS_PER_ROUND` (default 2, range 1–6)
  with an immutable policy + upgrade baseline. Configuration changes affect
  newly created rounds only; existing snapshots and exclusions stay intact.
- **Release-attributable failures** are only these codes:
  `season_pack_incomplete`, `movie_primary_ambiguous`, `output_empty`,
  `media_identity_mismatch`, `media_corrupt`, `password_protected`.
  They atomically:
  - insert exact `workflow_release_exclusions` (indexer + native key) and
    `workflow_package_exclusions` (NZB SHA-256);
  - mark the AltMount job `needs_attention` with the outcome;
  - move the workflow through `needs_attention` → `waiting` (revision +2);
  - if a slot remains, queue the next `workflow.search_requested` immediately.
- **Infrastructure failures** (network, auth, unreconcilable identity, unreadable
  output) use `CompleteImportWithCode` and reconcile the same job — they never
  exclude a release and never silently consume a slot.
- **All configured slots used** → the round closes `exhausted` with a novelty cutoff
  (highest `first_seen_sequence` seen), subtitles `retry_state=exhausted`, and a
  `workflow_retry_discovery` row in `waiting` with a `workflow.discovery_rescan_requested`
  event at a deterministic time:
  - base interval 24 hours;
  - stable ±20% jitter derived from `sha256(workflowID:generation)`;
  - interval doubles after each 14 consecutive **complete** scans with no new
    eligible identity, capped at 14 days;
  - an incomplete scan (any indexer not succeeded) reschedules at the earliest
    indexer retry time instead of counting as complete.
- Discovery can flip the row to `ready` when a newly first-seen eligible
  candidate passes policy and is not attempted/excluded. It never submits.
- **Owner opens the next round** via `POST /api/v1/workflows/{id}/acquisition-rounds`
  (`Store.OpenNextAcquisitionRound`): requires `waiting`, fewer retained failed
  outputs than `ACQUISITION_RETAINED_OUTPUT_LIMIT`, a `ready` discovery candidate, candidate first-seen > cutoff, still
  eligible under the round's policy version, and not attempted/excluded. It
  creates round N+1 with a fresh upgrade baseline and queues a search.
- The owner cannot reopen an exhausted round whose policy snapshot is older
  than the profile; apply the current policy instead (§5a).
- **Retained output limit**: `ACQUISITION_RETAINED_OUTPUT_LIMIT` (default 4,
  range 1–12) counts distinct failed-output jobs with an
  `acquisition_outcome <> 'output_verified'`. It gates retrieval before a
  provider grab, submission, owner round opening, access recovery and policy
  application. Raising the owner-configured limit preserves every failed
  inventory and file and never queues work or opens a round by itself.
- **Reverification**: an owner may reverify each exact retained output once
  after a probe correction (`altmount.output_reverify_requested`), reusing the
  frozen inventory and original subtitle policy, making no AltMount request. It
  resumes publication only if the existing verification and Plex gates pass.
- There are migrations/tests for recovering held workflows and legacy exhausted
  rounds (`0037_legacy_exhausted_round_recovery.sql`,
  `0038_held_workflow_retry_recovery.sql`) — do not remove those paths.

## 5a. Changing a quality profile's policy

`PATCH /api/v1/settings/quality-profiles/{id}` bumps `policy_version` but never
touches existing rounds: round snapshots are immutable (trigger
`enforce_acquisition_round_immutable_snapshot`). The snapshot is the policy of
record for an attempt: selection uses it, and output verification reads the
attempt's round snapshot (`SubtitlePolicyForJob`), never the live profile.

To move open work onto the new version the owner calls
`POST /api/v1/settings/quality-profiles/{id}/apply-policy`
(`Store.ApplyQualityProfilePolicy`). In one serializable transaction, for every
non-terminal workflow on the profile whose open (or exhausted-and-discovering)
round is older:

- the open round is superseded (`cancelled`, `close_reason` names the new
  version) and round N+1 opens with the current policy snapshot, the current
  upgrade baseline, and `novelty_cutoff_sequence = 0`. The novelty cutoff
  exists so an owner-approved retry round spends slots only on newly seen
  releases; a policy round changes the evaluation itself, so every known
  candidate is re-evaluated. Attempted and excluded releases stay disqualified
  and are never revived, even when the policy is loosened;
- `candidate_selected`: the selection was made under the old policy, so it is
  cleared (`needs_attention` → `waiting`) and a search is queued now; the
  pending retrieval becomes a stale no-op;
- `waiting` after round exhaustion or with `quality_policy_changed`: the
  attention is cleared, discovery is paused, and a search is queued now.
  Exhausted workflows still honor the configured retained-failed-output limit;
- `monitored`, `waiting`, `held`, `searching`, and `needs_attention` without an
  AltMount attempt: only the round changes, so any existing search schedule
  continues under the new policy;
- workflows with an active or attention AltMount attempt, a started
  publication, or a recovery-only attention code are skipped
  (`in_flight` / `owner_recovery_required`) and finish under the policy they
  started with. Re-run the endpoint after they settle; it is idempotent
  (`already_current`).

Every changed workflow gets `workflow_transitions` rows as needed and a
`workflow.quality_policy_applied` audit event; the profile gets a
`quality_profile.policy_applied` summary.

## 5b. Owner actions for known candidates and pre-upload rejections

Two owner endpoints support the availability audit without weakening the
normal guards:

- `POST /api/v1/workflows/{id}/acquisition-rounds/from-known`
  (`Store.OpenKnownAcquisitionRound`) opens one bounded round from releases
  Media Manager has already observed and judged eligible under the profile's
  current policy, when the workflow is `waiting` or `held` with no open round
  and the retained failed-output count is below the configured limit. The best known candidate
  is selected directly, ordered by explicit Danish-subtitle evidence, then a
  Danish/Nordic title marker (`NORDiC`, `DANiSH`, `DKsubs`, `DANSK`), then
  decision score, and a `release.retrieve_requested` event is queued. The
  round's `novelty_cutoff_sequence` is zero so the same action can re-evaluate
  known candidates under the current policy. Attempted and excluded releases
  stay disqualified.
- `POST /api/v1/workflows/{id}/revive-rejected-archives`
  (`Store.RevivePreSubmissionRejections`) revives the archive rejections that
  predate RAR/7-Zip submission. Only jobs that never reached AltMount
  (`external_queue_id IS NULL`) with the `archive_unsupported` outcome are
  removed, together with their attempt markers, package guards, and exclusion
  rows; the audit event `workflow.archive_rejections_revived` records the
  count. Queue-time archive failures stay excluded.
- `POST /api/v1/workflows/{id}/relocate-links`
  (`Store.RelocateWorkflowLinks`) moves a published workflow's current links
  and verified subtitle companions to another enabled root of the same media
  kind, updating `library_links.root_id` and the workflow root with a
  `library.links_relocated` audit event. Link targets and AltMount content are
  never touched, destinations are never overwritten, and a partially applied
  move is completed by a rerun. Use it when a title was published into the
  wrong Plex folder (for example before Seerr root routing existed).

## 6. Retrieval and AltMount submission

Handler: `acquisition.RetrievalService` (`retrieve.go`).

1. Load the retrieval context for the selected candidate.
2. Fetch the NZB through the Newznab client's strict locator handling
   (`DownloadLocatorReference`, provider-contract checks, size limit, redirect
   pinning). Cross-origin credential stripping applies on every hop.
3. Screen the package manifest **before the submission intent**: zip/tar/gzip/
bzip2/xz sets, ISO/DVD structures, and recognized non-playable containers are
rejected here. RAR/7-Zip sets pass because AltMount analyzes them itself.
A pre-upload rejection excludes the exact release, records the reason, and
queues the next search **without creating a job or spending a round slot**
(`RecordPreSubmissionRejection`).
4. Compute `PackageIdentity(body)` = SHA-256 of the exact bytes.
5. `BeginAltMountSubmission` records the durable intent (workflow, candidate,
   package, upload name) before any upload I/O.
6. Upload to AltMount with a unique filename derived from the release.
7. Mark submitted / unknown / failed. An **unknown** outcome is reconciled later
   by listing the queue for the stable upload name: exactly one match attaches;
   zero matches within 30 minutes → `altmount_identity_unresolved` (owner must
   confirm it was not accepted); multiple matches → `altmount_identity_ambiguous`.
   Never blindly resubmit.
8. Terminal retrieval failures (e.g. provider 403/404) are release attributable
   to that provider only inside the workflow: the exact release is excluded and
   another indexer may still be used.

Grab budgets: fetching the NZB reserves the indexer's rolling grab budget
before any request. When the selected indexer's budget is exhausted, retrieval
moves the selection to the identical release (same title) on another enabled,
commissioned indexer that still has grab budget, if that copy is eligible
under the open round and was never attempted or excluded
(`SelectCrossPostWithGrabBudget`); otherwise the event waits for the window.

## 7. AltMount reconciliation and output verification

Handler: `acquisition.AltMountReconcileService` (`reconcile_altmount.go`).

- Queue polling every 10 seconds until a terminal phase.
- On `completed`, the bounded read-only mapping (`ReportedRoot` →
  `LocalReadOnlyRoot`) must enumerate the output; a 5-minute grace handles slow
  mounts, then `altmount_output_unavailable` attention.
- On `import_complete`:
  1. Persist `altmount_output_inventories` + `altmount_output_assets`.
  2. Empty inventory → release-attributable `output_empty`.
  3. **Movie**: exactly one dominant playable file
     (`selectedMovieAssets`/`hasOneDominantMovie`); anything else →
     `movie_primary_ambiguous`.
  4. **Series**: `packs.Evaluate` against the immutable expectation. Not
     publishable → `season_pack_incomplete`. Every expected episode maps exactly
     once; multi-episode files are grouped.
  5. **Subtitle verification** (`subtitles.Verify`): every selected video is
     probed inside the authorized root with ffprobe (bounded: 20 000 assets,
     256 videos, 5 minutes). Required languages must be present as embedded
     tracks or strictly matched managed sidecar companions
     (`.srt/.ass/.ssa/.sub/.idx/.vtt`). Danish variants canonicalize to `da`
     with raw evidence retained.
  6. `verified` → `CompleteImport(true)` and queue publication. Anything else →
     a recorded verification result (retry state, `can_reverify`) with the
     output retained and undeleted.
- Inspection failures (probe unavailable, containment error, timeout) →
  `output_inspection_failed` attention; never a false success.

## 8. Publication and Plex

Handler: `publication.Service` (`service.go`, `plex.go`).

### 8.1 Build plans

`buildPlans` requires a media year and a verified Plex-supported identity:
movies prefer TMDB then IMDb; series prefer TVDB then TMDB. It:

- validates the AltMount read-only root and every source relative path
  (containment);
- maps the source root to the Plex-visible root;
- renders Plex-safe names (`library.RenderMovie`/`RenderEpisode`), appending the
  unique persisted **release token** so old and new generations can coexist
  during verification;
- emits one plan per verified asset, plus subtitle companions that share the
  same publication generation.

`library.PreflightBatch` catches collisions/escapes before any mutation.

### 8.2 Publish

`PreparePublication` (durable plans + versions) → `MarkPublicationPublishing` →
`library.PublishBatch` (symlinks only) → `CompletePublication`. A filesystem
failure calls `FailPublication` → workflow attention; never a partial claim.

### 8.3 Confirm

`plex.scan_requested` → `PlexService.HandleScan` refreshes the exact library.
`plex.confirm_requested` → `HandleConfirm` polls for exact identity GUID + every
expected path. The item is found by the section title filter or, when Plex
shows a localized title, by walking the section folder tree to the exact path. On success (`ConfirmPlexScan`, one transaction):

- the new link generation becomes `current`;
- the previous generation is displaced (queued for removal only now);
- subtitle companions become current;
- `plex_confirmations` evidence is stored;
- Seerr status sync is queued.

The 20-minute confirmation window starts when the refresh is requested, so
time a scan spends queued never counts against Plex. A timeout marks attention
(`TimeoutPlexScan`) with the last poll's reason.
The owner can retry from the same verified links with
`POST /api/v1/workflows/{id}/reconfirm-plex` (`Store.RequestPlexReconfirmation`),
which creates a fresh scan request and never touches the links. Publication is
never assumed from a scan alone.

## 9. Upgrade rules

- Every selected release gets a unique durable filename token, so the
  replacement can coexist through verification.
- A future candidate must score **strictly higher** than the current release
  under the target policy, unless the current release fails a newly required
  property (`applyUpgradeBaseline`).
- Managed-link upgrades: the old symlink moves to recycle only after exact Plex
  confirmation of the new file (`library.displaced_remove_requested`).
- Adopted real-file upgrades: creation of the workflow (and any delayed search
  that would replace it) requires an exact owner phrase and an immutable
  path/size/mtime fingerprint (`adopted_replacement_authorizations`). After Plex
  confirms the replacement, the old file is moved into recycle with a 30-day
  retention and **never** physically purged without a second exact owner
  confirmation after retention.
- Rollback (`POST /api/v1/recycle/{id}/restore-adopted`) restores the whole
  retained cycle with exact fingerprints; replacement links stay current until
  Plex re-confirms every restored path, then are safely displaced and removed.
  Rollback is whole-cycle, not per episode.

## 10. Adoption of an existing library

Packages: `internal/adoption` (scanner), `internal/adoptionexec` (execution),
`internal/postgres/adoption*.go`.

1. `POST /api/v1/adoption` starts a **read-only** bounded scan of a root.
2. Each item is reviewed (approve/reject/remap); TV requires exact-season scope.
3. `POST .../approve` locks the reviewed plan.
4. `POST .../execute` requires an exact confirmation phrase; batches cap at 25
   items.
   - Same-root renames: fingerprint-checked contained moves.
   - Cross-root moves: copy to an operation-owned temp file, SHA-256 verify,
     atomic publish, then remove source.
   - Every path and both root identities are journaled before mutation; retries
     recover unknown outcomes; failures roll back in reverse order.
   - Unknown companions are fingerprinted and preserved.
   - A 30s/1 MiB ffprobe gate records bounded media-info evidence and rechecks
     the path fingerprint.
5. Plex confirmation runs before anything is marked current. Only after Plex
   path+identity confirmation are `adopted_files` provenance and reviewed
   monitoring applied (one fresh workflow per exact scope).
6. Immediate search additionally requires the adopted replacement phrase, a
   commissioned paced Newznab source, and (TV) an exact reusable episode
   snapshot.

## 11. Recycle and archive

- Managed links enter a 30-day cycle; the AltMount source is always retained.
- Adopted real files enter a separate fingerprint-bound cycle only after
  replacement Plex confirmation.
- Restore: links restore directly; real files restore through
  `restore_planned` → Plex re-confirmation (`recycle.restore_requested` +
  `plex.adopted_restore_confirm_requested`).
- Purge: links may purge automatically after retention (`PlanDueRecyclePurges`,
  hourly); real files require the second exact owner phrase
  (`confirm-purge`) and a fingerprint match.
- `DELETE /api/v1/media/{id}` and `POST .../archive` route through the same safe
  paths; they refuse while adopted real files/retention must be resolved.

## 12. Blocklist

`release_blocks` supports two scopes:

- `native_release`: indexer instance + native GUID (blocks one exact posting);
- `pack_release`: media + season variant + exact release title.

Only owner-created blocks from persisted candidates. Matching candidates remain
visible in evidence but are rejected before ranking. Blocks expire after 30/90
days or are manual. Create/remove are audited.

## 13. Notifications

`notification_destinations` subscribe to exact audit actions; delivery is a
durable outbox job with retry/backoff and visible history. Payloads contain
event id/action/aggregate id/type/time — never audit detail or secrets.
Endpoints are HTTPS by default (HTTP opt-in), and credentials are env
references resolved at delivery time.

## 14a. Seerr *arr replacement

`internal/servarr` plus `internal/httpapi/servarr.go` expose the Radarr and
Sonarr endpoints Seerr uses to configure a server, add movies/series, and scan
availability. The root-folder list is the single synthetic Auto root (only
when at least one enabled, Plex-backed root of that kind exists; see §3.2),
profiles come from `quality_profiles`, and availability from Plex-confirmed
publications (`ServarrMovie.HasFile`, season `episodeFileCount`). Adds are
acknowledged, not duplicated: Seerr's own request (webhook/poll) remains the
single intake path into the workflow engine.

## 14. Indexer commissioning

### v1 (`internal/commissioning`, `POST /settings/integrations/{id}/commission`)

Staged Newznab rows are always created disabled. Commissioning fetches CAPS,
requires a usable movie/TV dialect, and performs one random negative search —
all paced ≥3s through the shared scheduler. Only both-passing enables the
indexer and persists capability state.

### v2 (`internal/commissioningv2`, owner-authorized)

A multi-step, explicitly owner-authorized run: create run → authorize CAPS →
review/seal categories → authorize searches → execute one step at a time →
finalize draft → activate profile. The domain package is transport-free; live
execution is constructed only in `internal/commissioning/live_v2.go` and exposed
through `httpapi/commissioning_v2.go` with origin/CSRF protections. Limits:
≤5 API calls / ≤4 search calls per run. Activation stores
`indexer_capability_profiles`; runtime search prefers the active profile and
falls back to the legacy dialect.

The developer-only DRI lab evidence is recorded in
`docs/status/indexer-conformance.md`; it is not a runtime dependency and raw evidence is
never imported.

## 15. Encrypted backups

`POST /api/v1/backups` queues `backup.create_requested`. The worker runs
`pg_dump -Fc`, then chunked AES-256-GCM encryption (`MMBAK001` format). Records
track size/sha256/schema; verification restores into an isolated temporary
database and requires the exact schema version. Download serves only
checksum-matching encrypted files. There is deliberately **no** production
restore automation and no automatic deletion.

## 16. Attention codes and recovery playbook

Before an NZB is submitted to AltMount, Media Manager screens its file
manifest. Packages that only contain archive formats AltMount cannot open
(`.zip`, `.tar`, `.gz`, `.bz2`, `.xz`), ISO/DVD structures, or other recognized
non-playable containers are excluded immediately **before the submission intent
is created**, so the rejection never consumes one of the round's configured attempt
slots; another candidate is searched inside the open round. RAR
(`.rar`/`.r00`/`.partNNN.rar`) and 7-Zip sets are submitted: AltMount analyzes
stored and nested RAR and RAR-in-7-Zip itself (most NORDiC/DANiSH/DKsubs scene
posts are packed this way) and reports the ones it cannot stream as queue
failures, handled below; those did consume a slot, so
`POST /api/v1/workflows/{id}/revive-rejected-archives` (§5b) revives the ones
rejected before submission that predate RAR support. AltMount queue failures that identify the same payload classes
(archive iteration errors, `compressed files are not supported`, `no files
with allowed extensions`) or missing Usenet articles (`incomplete or missing
parts`) are treated the same way: the exact release and package are excluded
and a replacement search is queued while the round still has a slot. Unknown or
obfuscated manifests are never rejected by name alone. If AltMount has already
pruned the failed queue entry, owner reconciliation applies the same decision to
the persisted failure detail instead of retrying that reconcile forever.

| Code / situation | Meaning | Owner action |
| --- | --- | --- |
| `round_exhausted_waiting_new_identity` | All configured round slots used; discovery waiting for a new eligible release. | Wait for discovery `ready`, then open a new round; or open one from known eligible releases with `POST /api/v1/workflows/{id}/acquisition-rounds/from-known` (§5b). |
| `altmount_identity_unresolved` | Upload name not visible within 30 min. | Confirm the original upload was not accepted, then re-search. |
| Archive/unplayable/missing-segment release | RAR set AltMount cannot stream (compressed or corrupt), zip/tar archive, ISO/DVD structure, no playable media, or Usenet articles absent from the providers. | Exact release + package excluded; next slot auto-searches. |
| `altmount_identity_ambiguous` | Multiple queue jobs match. | Resolve queue manually; do not resubmit. |
| `altmount_output_unavailable` | Completion but output unreadable for 5 min. | Verify shared mount, then reconcile. |
| `output_inspection_failed` | ffprobe/containment failed. | Fix probe/mount, reverify retained output once. |
| `season_pack_incomplete` | Missing/ambiguous expected episodes. | Exact release + package excluded; next slot auto-searches. |
| `movie_primary_ambiguous` | ≠1 dominant movie file. | Same as above. |
| `output_empty` | No readable media. | Same as above. |
| Subtitle `unverified` / `exhausted` | Required language missing. | Inspect evidence; offer alternate release or reverify after correction. |
| `plex_timeout` | No exact confirmation before the deadline; `attention_detail` names the reason (path not shown, identity mismatch, confirmed late). | Fix the named cause, then `POST .../reconfirm-plex` (verified links only, ≤3 retries). Old link stays current. |
| `held` | Releases observed, none satisfied required evidence. | Paced retry is automatic; adjust policy if intended. |
| `quality_policy_changed` | The profile's policy changed after the open round was snapshotted; searching is parked. | `POST /api/v1/settings/quality-profiles/{id}/apply-policy` (§5a). |
| `transport_unknown` | Submission outcome unknown. | `recover-altmount-submission`. |
| Recycle `restore_attention` | Restore collision/fingerprint mismatch. | Resolve manually; nothing is deleted. |

Never "fix" an attention state by editing the database or faking a Plex
confirmation. Recovery paths exist; use them.

## Bounded NZB retrieval recovery (schema 41)

Before fetching an NZB, admission checks selection/revision, round/policy/current
publication, available submission slots, retained failed outputs and owner
blocks. Failure to fetch a candidate does not spend a download slot: up to four
failed candidates can advance directly to known eligible alternatives in the
same open round. HTTP 401/403 avoids that provider for this round after one
request. Other failures retain two bounded attempts and all pacing controls.
No alternative or reaching the failure cap leaves an explicit attention reason.

Danish-required search ranking prefers explicit `da` observations/inferences,
then bounded Danish/Nordic title clues, then unknown evidence. These clues do
not create subtitle facts, change eligibility, or qualify an upgrade by
increasing its policy score. Output inspection and exact Plex verification
remain mandatory. See the [review](../status/retrieval-recovery-review.md) for
live evidence, limitations and the owner decisions for deploying/resuming work.

## Owner recovery after NZB access rejection

A 401/403 access rejection excludes the exact release and avoids its provider
for the remainder of the open round. After repairing provider access, the
owner can use **Retry access-rejected release** in Workflows and confirm the
release and provider. This retries that exact selection without a new search
or round. The UI sends the candidate ID and reviewed workflow revision.

Eligibility requires a `release_retrieval_failed` or `release_retrieval_limit`
attention state, an unsubmitted selected candidate with a current eligible
policy decision on a commissioned provider, unchanged round policy and
publication baseline, a remaining submission slot, fewer retained
failed outputs than the configured limit, and room under the retrieval cap after lifting this exclusion.
Owner blocks, attempted releases, active jobs, begun publication, other
current-round access rejections on that provider, and missing adopted-file
replacement authorization prevent the action. Payload and output exclusions
are never lifted. Structured access errors and the four exact legacy 401/403
failure messages are accepted; legacy rows must have been recorded after the
current round opened. Unknown historical failure text remains excluded.

The transaction preserves the original failure in audit history when removing
its exclusion. Repeated
access rejection restores normal exclusion and provider avoidance. Recovery
does not change any other workflow or globally enable/disable the provider.
