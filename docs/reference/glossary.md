# 09 — Glossary

Purpose: exact meanings. Several words here have narrower meanings than in
general media-server software; use these definitions in code, commits, and
docs.

## Core

- **Media item** — a movie or series row (`media_items`) identified by an
  internal UUID. Provider IDs are identities on the item, never primary keys.
- **Identity** — an external provider reference (`tmdb`, `tvdb`, `imdb`) with
  namespace/value/state (`verified`/`candidate`/`rejected`) and source.
- **Workflow** — one media item + one acquisition scope. Owns state, revision,
  selected release, and acquisition rounds.
- **Variant / scope** — the workflow's acquisition unit: `default` (movie),
  `season:NN` (complete season pack), `season:NN:episode:NN` (single airing
  episode). See `internal/workflows/scope.go`.
- **Revision** — optimistic-concurrency counter. Events carry
  `aggregate_revision`; handlers drop stale ones.
- **Owner** — the single authenticated human operator. "Owner action" means an
  explicit API call with the owner bearer, never a background decision.
- **Outbox event** — a durable row in `outbox_events` that drives one handler.
  State changes and their event are written in the same transaction.

## Search and releases

- **Observation** — one raw search result normalized into
  `releases.Observation` (title, size, published date, attributes, native GUID).
- **Candidate** — a persisted observation that can be retrieved
  (`release_candidates` with indexer instance + native key identity).
- **Native GUID / native key** — the indexer's own release identifier; the only
  exact per-release identity.
- **Weak GUID** — a candidate whose provider exposes no retrievable identity
  (`weak:` prefix). Always held, never selected.
- **Evaluation / verdict** — the result of applying policy rules to an
  observation: `eligible`, `held`, or `rejected`, with score and explanations.
- **Rule** — a policy predicate (`required`, `preferred`, upgrade rules) parsed
  from the quality profile.
- **Selection** — the highest-ranked eligible candidate persisted for a
  workflow (`candidate_selections`).
- **Held** — releases were observed but none satisfied required evidence; a
  recoverable, paced outcome, not a terminal failure.
- **Blocklist** — owner-created rejection. `native_release` = indexer + native
  GUID; `pack_release` = media + season variant + exact release title.
- **Release token** — a unique persisted filename fragment so multiple
  generations of the same title can coexist during verification.
- **Package identity** — SHA-256 of the exact NZB bytes; used to exclude an
  identical package inside a workflow and to dedup submissions.

## Indexers and Newznab

- **Indexer** — a Newznab provider integration instance. Created disabled.
- **Commissioning** — the paced process that verifies CAPS + a negative search
  and enables an indexer. v2 adds owner-authorized CAPS/search steps and a
  capability-profile draft/activation.
- **Capability profile** — immutable, versioned record of what an indexer
  advertises and which search recipes are allowed for it.
- **Dialect** — the legacy per-indexer parameter vocabulary used when no
  capability profile is active.
- **Recipe** — a planned search shape (query style, identities, categories)
  derived from a capability profile.
- **Pacing floor** — the earliest time a lane may make its next call
  (`PacingFloor`); at least 3 seconds between request starts.
- **Budget / reservation** — rolling per-indexer query/grab allowance. A
  reservation is durably recorded **before** network I/O.
- **Cooldown / Retry-After** — provider-driven backoff; deferrals use
  `DeferOutbox` so retry attempts are not consumed.
- **Failure scope** — classification of an indexer failure (none, lane, indexer,
  provider classes) used to apply bounded backoff.

## AltMount acquisition

- **AltMount** — the Usenet download client. Its completed output is a
  **read-only** source for Media Manager.
- **Submission intent** — durable record written before uploading an NZB
  (workflow, candidate, package, upload name).
- **Transport unknown** — the upload result is not known; reconciled by stable
  upload name and never blindly resubmitted.
- **Queue item / phase** — AltMount's job state mapped by `altmount.Reconcile`
  to `queued`, `paused`, `processing`, `streaming_ready`, `import_complete`,
  `failed`, `cancelled`, `needs_attention`, `removed`, `unknown`.
- **Inventory** — frozen list of files under the completed job root
  (`altmount_output_inventories`/`_assets`).
- **Dominant file** — the single unambiguous primary movie file; zero or more
  than one is `movie_primary_ambiguous`.

## Season packs

- **Expectation set** — the immutable list of expected episodes frozen when a
  season workflow starts.
- **Coverage** — exact mapping of every expected episode to exactly one content
  group; every episode once, no extras.
- **Content group** — one physical file that may cover one or more episodes
  (multi-episode files are grouped, never double-counted).
- **Publishable** — `packs.Report.Publishable()`: coverage verdict is
  `verified` with no unresolved issues.

## Verification

- **Media info** — bounded ffprobe evidence (container/video/audio/subtitle
  tracks) captured per selected file.
- **Companion** — a managed subtitle sidecar published beside a video
  (`.srt/.ass/.ssa/.sub/.idx/.vtt`) and sharing its publication generation.
- **Subtitle policy** — required and preferred languages from the quality
  profile; required languages fail closed.
- **Verification** — per-job result of inspecting every selected video for
  required languages (embedded tracks or strictly matched companions). Status
  `verified` is required before publication.
- **Reverification** — one owner-authorized re-inspection of a retained output
  after a probe correction; reuses the frozen inventory, makes no AltMount call.
- **Retained output** — a completed but failed/unpublished output that stays on
  disk, owner-visible, and undeleted.

## Rounds and recovery

- **Acquisition round** — a durable period with an immutable configured
  submission limit (default two, range one–six), policy snapshot and upgrade
  baseline. Only the owner opens the next one.
- **Slot** — one of the round's bounded attempts. Only a release-attributable
  failure consumes a slot.
- **Release-attributable failure** — a failure that can be blamed on the
  release itself: `season_pack_incomplete`, `movie_primary_ambiguous`,
  `output_empty`, `media_identity_mismatch`, `media_corrupt`,
  `password_protected`. Excludes the exact release + package inside the
  workflow.
- **Infrastructure failure** — network/auth/mount/probe failures; the same job
  is reconciled and no release is blamed.
- **Novelty cutoff** — the highest `first_seen_sequence` at round exhaustion;
  discovery ignores everything at or below it.
- **Recovery discovery / rescan** — evidence-only low-priority search that can
  mark a newly first-seen eligible candidate `ready`. Never selects or submits.
- **Ready candidate** — a discovery find eligible for an owner-opened round.
- **`round_exhausted_waiting_new_identity`** — attention code for a workflow
  parked in `waiting` after both slots were used.

## Publication and Plex

- **Publication plan** — a contained mapping from AltMount source relative path
  to a Plex-safe destination relative path.
- **Link / version / generation** — `library_links` is the logical managed file;
  `library_link_versions` are immutable generations. The **current** version is
  the one Plex has confirmed.
- **Displacement** — removing an old generation's symlink after the replacement
  is Plex-confirmed.
- **Plex confirmation** — exact match of the verified provider GUID and every
  expected path in Plex. Without it, nothing is current or successful.
- **Plex library root** — a configured destination with `plex_visible_root` and
  `source_visible_root` mappings (`library_roots`).

## Upgrades and adoption

- **Upgrade** — a replacement release that must score strictly higher than the
  current one under the target policy (unless the current fails a newly required
  property).
- **Adopted file** — a pre-existing real file in a library that Media Manager
  fingerprinted, renamed/moved, and Plex-confirmed.
- **Adoption batch / item** — a read-only scan result and its per-item review
  (approve/reject/remap; TV requires exact-season scope).
- **Operation journal** — `adoption_operations`: every path/root identity and
  fingerprint recorded before mutation, with retry recovery and reverse-order
  rollback.
- **Replacement authorization** — an exact fingerprint-bound owner phrase
  allowing a workflow/delayed search to replace an adopted real file.

## Recycle

- **Recycle entry / cycle** — a retained old file or symlink
  (`recycle_entries`) with purpose `media_delete` or `upgrade_displacement`.
- **Retention** — 30 days for links and displaced adopted real files.
- **Purge** — physical deletion. Links may purge automatically after retention;
  adopted real files require a second exact owner confirmation.
- **Adopted rollback** — whole-cycle restore that keeps replacement links
  current until Plex re-confirms every restored path.

## Intake and platform

- **Seerr request** — the external user request that starts intake. Its
  lifecycle is derived from persisted Media Manager state; waiting/failed never
  looks successful.
- **Receipt key** — dedup hash for a Seerr webhook/poll delivery.
- **Availability sync** — pushing `available` back to Seerr only after exact
  Plex confirmation.
- **Import list snapshot** — review-only parse of an IMDb CSV export; creates no
  media.
- **Prowlarr backup import** — bounded ZIP/SQLite parse creating disabled
  Newznab candidates; the owner approves with env secret references.
- **Audit event** — owner-visible durable write record.
- **Command receipt** — idempotency record for an owner command.
- **Attention code** — a durable snake_case reason (`output_empty`,
  `altmount_identity_unresolved`, ...) shown to the owner.
- **Owner attention** — UI concept for a workflow that genuinely needs an owner
  decision, derived from `owner_action_required` or explicit attention states.

## External references (not runtime dependencies)

- **DRI Indexer Lab** — developer-only provider conformance lab; evidence in
  `docs/status/indexer-conformance.md`, never imported at runtime.
- **GMD (Global Media Discovery)** — a separate system referenced in historical
  notes; Media Manager has no GMD runtime dependency (`internal/adapters/gmd`
  is empty).
- **OldBoys** — an indexer with a non-Newznab custom API; intentionally disabled
  and uncontacted.
