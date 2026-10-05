# Media Manager implementation status

Audited on 2026-08-30, updated on 2026-09-02 against the live Docker build at
API commit `1a4d53f` and schema 32, and refreshed on 2026-09-28 against `main`
at `c6bf1b8` and schema 40. Deployment and acquisition recovery were refreshed
on 2026-09-30 at `34f0024`, schema 43; older assessments remain dated evidence.
The audit covers the Go route registry, PostgreSQL migrations/store methods
and the React route switch. This document describes implemented behavior,
not the development fixture's claims.

## Native OldBoys provider (2026-10-05, source)

The dedicated OldBoys adapter supports bearer filter search and the live-proven
PID download route, with fixed HTTPS origin, IPv4, no redirects, shared durable
pacing/budgets, exact numeric provider identities and secret-free metadata.
Only Danish-original catalogue films/TV and standalone DANiSH releases are
eligible. Unknown IDs and sizes stay unknown; TV title bindings are explicitly
inferred, with unchanged conflict, frozen episode, output and Plex checks.
The owner can validate four positive/negative search controls and one discarded
NZB in Connections. Credential replacement disables the commission atomically;
configuration and credential snapshots guard every send, result persistence and
activation. Schema remains 44. See [native contract](../operations/oldboys.md).
Deployment and completed acquisition evidence are pending at this source note.

## OldBoys credential setup (2026-10-05)

Settings → Connections accepts the API Token, Pass Key (PID), and RSS Key (RID)
through owner-only, bounded GET/PUT routes. Schema 44 stores the complete set
with AES-256-GCM encryption under a separate server key. Status responses expose
only availability, saved state and update time; successful saving clears the
password inputs. Native provider search/download support and commissioning
remain pending. Saving credentials does not enable the disabled legacy proxy.
See [setup and key recovery](../operations/oldboys.md).

This source note describes code, not deployment evidence.

## Annual TV episode acquisition (2026-10-05)

TV searches can alternate the existing ordinal-season request with one bounded
ID-only request when all regular catalogue seasons have exact frozen snapshots,
dated episodes identify distinct calendar years, and the mapped season is
fully dated with a contiguous episode sequence. This removes unreliable provider season filters without
adding requests to a scan. Year/date episode names and bare annual pack labels require positive verified
series identity before they are mapped to the canonical frozen season/episode;
raw and previous provider scope facts remain recorded. Ambiguous or incomplete
metadata remains on the canonical path. Output inspection applies the same
mapping while retaining the actual source paths and exact coverage checks.

The existing completed-season episode fallback now runs immediately after
relevant episode observations arrive and no eligible, unattempted pack remains. Unrelated scopes or rejected/unknown
series identity cannot activate or suppress that fallback; active pack jobs and
revision changes remain protected. Request summaries include only children in
the requested season's frozen expectation, expose their attention/progress, and
recognise a season fully covered by Plex-confirmed children.

No schema change applies. This source note describes code, not deployment.

## Retained movie and cross-post recovery (2026-10-04)

The backend supports one owner-authorized inspection of an eligible latest
ambiguous movie inventory without opening a round or acquiring another NZB.
Explicit bonus directories do not count as competing primary files, and movie
publication uses exactly the file identified by verified inspection evidence.
The action is operational API-only; a guided web control remains future work.
Completed negative subtitle evidence excludes exact-title cross-posts across
all selection/retrieval paths. Explicit conflicting remake years cannot be
overridden by provider IDs, including old persisted eligible selections.
Movie event names also accept the exact canonical year moved to the end,
with other words and their order preserved.
See [the API contract](../architecture/api.md#retained-movie-inspection).

## Configurable acquisition budgets (2026-09-30)

Acquisition attempts and retained failed-output limits are owner-configurable
at API startup. Defaults remain two/four; bounded ranges are one–six and
one–twelve. Schema 43 widens the database slot bounds while preserving all
existing snapshots and their immutability trigger. All round-creation paths
use the configured attempt budget, and every admission/read-model retention
gate shares the configured retention budget. Larger budgets do not delete
outputs, remove exclusions, open rounds or queue retries by themselves.

## Seerr acquisition attention follow-up (2026-09-30)

Definitive AltMount errors for missing archive parts, a release reduced to PAR2
files after missing-article checks, an empty NZB manifest, or a definitive
fast-fail rejection now use the existing exact-release/package exclusion and
in-round recovery. Generic validation errors, inconclusive STAT checks,
deadlines, permissions and connection failures remain infrastructure attention;
they are not evidence that a release is defective. Deleted Seerr requests are
shown as deleted rather than awaiting approval, while linked workflow attention
and Plex confirmation still take precedence.

A successfully parsed audio-only media document now reports a typed
`ErrNoVideoTrack`. Acquisition preserves that evidence through subtitle
inspection and records `no_playable_media` using normal exact-package failure
recovery. Timeouts, unavailable codecs and generic probe errors remain
infrastructure failures; matching error text alone cannot exclude a release.
Output inventories stay retained. A held search with no workflow attention
appears as waiting in Requests because its bounded retry is already scheduled;
intake configuration failures and attention on other linked workflows retain
precedence.

The companion AltMount fixes are deployed at `321b3ba` and preserved in the
[source patch bundle](../operations/altmount-patches/README.md).
Commit `0c3325b` gives provider fallback its bounded
15-second budget instead of cutting it off at two seconds; `c724ec8` recognizes
gzip NZB content after a legacy retry renames `.nzb.gz` to `.nzb`; `321b3ba`
preserves the original single-file metadata name when no extension override
is supplied. Each passed the full AltMount backend race suite and vet.
Production evidence is recorded in the owner's private recovery report;
these fixes do not relax subtitle,
publication, retention, provider pacing or indexer credential requirements.

## Access rejection recovery follow-up (2026-09-30, deployed)

The owner-only `retry-access-rejected-release` action and Workflows control
are deployed at `17981e5` (PR #26). They retry the exact selected
pre-submission HTTP 401/403 release inside its open round, with candidate and
revision guards, an audit of the original exclusion, and the existing policy,
submission, retrieval, output-retention, block and publication protections.
Exact historical access rejection messages from before schema 41 are also
recognized. A read-only production eligibility check matched all 38 selected
treasure-maps failures (26 structured and 12 historical). The first live trial
retrieved a valid NZB but exposed a password-protected archive; the exact
package was excluded through normal recovery. Subsequent eligible recoveries
used the supported action and shared production provider budgets. See
[the API contract](../architecture/api.md#access-rejection-recovery)
and [the workflow rules](../architecture/workflows.md#owner-recovery-after-nzb-access-rejection).

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

## Local retrieval recovery follow-up (2026-09-28, not deployed)

Schema 41 adds bounded, round-scoped recovery from NZB retrieval failures,
pre-grab admission and Danish-clue ranking without weakening output verification.
See the [review](retrieval-recovery-review.md) for behavior, tests and limitations.
Production was independently verified at `78c127b`, schema 40; older deployed
snapshots below remain historical.

## Current hardening milestone (2026-09-28)

Verified against the deployed build at `c6bf1b8` and schema 40. The following
are Real, tested, and deployed on top of the historical audit below:

- **Seerr as the only server.** A Radarr/Sonarr-compatible API
  (`/radarr/api/v3`, `/sonarr/api/v3`) is served behind a dedicated
  `SERVARR_API_KEY`. Root folders are Media Manager library roots, profiles are
  enabled quality profiles, adds are acknowledged and driven by Seerr's own
  request intake, and availability is pushed by Media Manager's Plex-confirmed
  status sync (Seerr's library scan is intentionally disabled). Seerr is
  configured with both entries (`/radarr` and `/sonarr`, default, Danish Subs
  profiles, `/libraries/movies` and `/libraries/tv`) and the saved
  configuration was verified through Seerr's own clients.
- **Seerr root-folder routing (schema 39).** Request provenance
  (`serverId`/`profileId`/`rootFolder`/`is4k`) is persisted, resolved through
  owner-created `seerr_library_routes` mappings, and an unmapped folder pauses
  with `needs_configuration` plus the exact folder. Saving a route requeues
  paused requests.
- **Release package screening and recovery (schema 40).** NZB manifests are
  screened before submission; archive-only, ISO/DVD, and non-playable
  containers are rejected without an AltMount upload. Release-attributable
  AltMount failures (archives, unplayable manifests, missing Usenet articles)
  exclude the exact release and package and retry inside the open round,
  including from the persisted detail when AltMount already pruned the queue
  entry. Only genuine infrastructure failures remain owner attention.
- **Metadata tolerance.** A provider `runtime = 0` now means unknown rather
  than invalid, so Seerr intake for such titles no longer retries forever.
- **Documentation.** The docs set is reorganized under
  `docs/{onboarding,architecture,operations,reference,status,archive}`, every Go
  package has a `doc.go` comment, and `scripts/check-docs.mjs` (`npm run
  docs:check`) gates relative links and anchors in both CI pipelines.

## Deployed schema-32 recovery milestone

The deployed source now models acquisition rounds separately from lifetime
attempts. Each round has two durable submission slots; release-attributable
failures atomically record exact release and canonical package exclusions, while
infrastructure failures retain and reconcile the same AltMount job. After round
exhaustion, background discovery uses immutable first-seen identity, persisted
per-indexer completeness, shared provider budgets, stable jitter, and owner-gated
round opening. The activity queue exposes round slots, lifetime attempts,
retained bytes, partial/complete discovery evidence, same-job reconciliation,
the exact ready candidate/provider evidence, and the owner action. Schema 32
also snapshots the complete set of current link versions before upgrade
selection and submission. GitLab pipeline 26 passed the frontend, Chromium, and
PostgreSQL 18 backend gates before only the Media Manager API and UI were
recreated. Local liveness, readiness, gateway reachability, schema, outbox, and
running-commit checks then passed without initiating media work.

## UI route audit

The opt-in V2 test routes `/v2`, `/v2/library`, `/v2/requests` and
`/v2/attention` provide a new shell and read-only views of existing live data.
They preserve V1 routes and link detailed recovery actions back to that console;
this initial preview does not replace the complete owner interface. See the
[frontend reference](../architecture/frontend.md#v2-test-ui).

| Route | Status | Current live behavior | Required work |
| --- | --- | --- | --- |
| `/` | Partial | Loads real catalog, managed-file (including adopted current files), workflow, recycle, outbox, and recent durable-activity aggregates in the approved overview composition. | Add wanted/upgrade and component-health summaries as those read models are completed. |
| `/media` | Partial | Presents the complete bounded movie catalog with real metadata/artwork, managed-file quality, profile/root, monitoring, missing/upgrade, Plex-confirmation, filters, pagination, and selected-item inspection. | Add richer current-policy comparison and explicit multi-generation summaries. |
| `/media/series` | Partial | The deployed implementation presents the complete bounded TV catalog with real metadata/artwork, aggregate season-file and workflow state, profile/root, monitoring, missing/upgrade, Plex-confirmation, filters, pagination, and exact series-detail navigation. Local unit/browser gates and the local gateway smoke test pass. | Add per-season coverage summaries in the inspector and direct TV preselection on the shared add form. |
| `/media/movies/:id` | Partial | Loads the real item, complete bounded workflow history, AltMount link generations, adopted-file provenance, Plex confirmation, and explicit quality-evidence state. Starting an adopted-file upgrade requires the exact replacement phrase and captures an immutable path/size/modification fingerprint. Every selected release contributes a unique durable release token to its Plex filename, so the replacement can coexist for verification. | Add current policy/candidate comparison and direct cycle navigation. |
| `/media/series/:id` | Partial | Loads persisted monitoring and exact expected-episode counts per season, complete bounded workflow history, AltMount link generations, and adopted files with reviewed exact-season scope. Only the selected season's adopted files trigger the exact replacement confirmation, release-token filenames prevent replacement collisions, and rollback is whole-cycle rather than per episode. | Add episode-level coverage/evidence, metadata-backed season creation, and direct cycle navigation. |
| `/files` | Partial | Unifies real AltMount symlink generations and Plex-confirmed adopted real files, with destination, exact workflow/season scope, target profile, source size, persisted release/media-info evidence, and provenance. | Add companion relationships, richer track filters, and bounded pagination. |
| `/files/:id` | Partial | Shows real path relationships, origin, exact scope, source fingerprint metadata, target profile, bounded container/video/audio/subtitle evidence, Plex identity/path evidence, and displacement/removal timestamps. | Add companion detail, full track presentation, and recycle-cycle linkage. |
| `/collections` | Partial | Persists audited manual groupings of real catalog items and requires a server-side exact-member preview before creation; deleting a collection never changes media or files. | Add approved evidence-backed smart rules, rule previews, and membership refresh history. |
| `/calendar` | Partial | Persists exact Seerr episode air dates with immutable season expectations and lists dated episodes with real monitoring/workflow state; missing dates are omitted and dates never trigger search. | Add movie release/digital dates, provider refresh history, owner range controls, and timezone preferences. |
| `/wanted` | Partial | Derives missing versus upgrade gaps from active workflows, current AltMount publications and adopted files by item/exact scope, and the persisted target profile. | Add independent quality/language/audio/subtitle goal deltas and pagination. |
| `/requests` | Real | Lists persisted Seerr requests joined to their exact media/workflows, with durable title/year/poster/requester and a derived owner-facing lifecycle. Filters by active/completed/attention/type, free-text search, and a detail panel with linked workflows, seasons, and published files. Waiting and failed requests never look successful. | Add server-side pagination/filtering and per-workflow owner actions if the workflow model permits. |
| `/import` | Partial | Performs bounded read-only discovery and per-item review, requires exact-season scope for series, then requires an exact owner confirmation before a maximum 25-item execution. Primary files and recognized companions move through contained filesystem roots; cross-root moves use an operation-owned temporary file, SHA-256 verification, atomic destination publication, and source removal only after verification. Unknown companions are fingerprinted and preserved. Every path and both root identities are journaled before mutation, retries recover unknown outcomes, and failures roll back in reverse order. A 30-second/1 MiB ffprobe gate records bounded real container/video/audio/subtitle facts, rechecks the path fingerprint, and completion transactionally creates current-file plus Plex identity/path provenance. Reviewed monitoring is applied only after Plex confirmation as one fresh workflow per exact scope; immediate search additionally requires the adopted replacement phrase, a commissioned paced Newznab source, and an exact reusable TV episode snapshot. | Add operation/confirmation detail and larger batches without stressing Plex. |
| `/activity/queue` | Partial | Lists real workflow summaries and queues safe search/cancel transitions. A delayed search that would replace a current adopted file exposes and enforces the same exact fingerprint-bound owner confirmation as workflow creation. | Add job/attempt detail, phase evidence, per-operation progress, and attention recovery. |
| `/activity/recycle` | Partial | Lists persisted cycles by cycle ID, distinguishes managed links from retained adopted real files, queues whole-link-cycle restore, and exposes physical real-file purge only after 30 days and the exact second owner phrase. An adopted rollback requires its own exact phrase, restores the complete fingerprint-bound cycle, keeps replacement links current until Plex re-confirms every old path, and only then displaces those links. | Add richer cycle evidence, deadlines/attempt presentation, and guided recovery for explicit attention states. |
| `/activity/history` | Partial | Merges real workflow transitions and audit events into a bounded durable timeline. | Add correlation, filtering, pagination, and complete audit coverage. |
| `/activity/blocklist` | Real | Creates blocks only from persisted observations, scopes them to an indexer/native GUID or exact media/season/title, supports 30/90-day or manual retention, audits create/remove, and makes future searches persist a rejected blocklist decision. | — |
| `/discover` | Partial | Searches real Seerr metadata on owner action, verifies TMDB/TVDB/IMDb identities with a detail lookup, and enriches the exact selected identity through configured direct TMDB, TheTVDB, and Simkl clients. Persisted evidence includes overview/artwork, tagline, dates, certification, runtime, genres/keywords, countries, networks/studios, cast/crew, provider-separated ratings, aliases, events, and provenance. IMDb-through-Simkl evidence is labeled and never presented as direct IMDb API data; exact TV episode snapshots still resolve per selected season. The direct series path is live-tested with TMDB, TheTVDB, Simkl, and IMDb-via-Simkl evidence; the direct movie path is live-tested with TMDB, Simkl, and IMDb-via-Simkl evidence. | Add result pagination, duplicate preview, and owner artwork choice/lock controls. |
| `/intelligence/releases` | Partial | Lists real persisted candidates, observations, policy verdicts/scores, selections, size, and indexer provenance. | Add per-search comparison, complete explanations, and pagination. |
| `/intelligence/groups` | Partial | Aggregates observed group facts from persisted release evidence and explicitly avoids trust labels. | Add time windows, contradiction detail, and server-side aggregation. |
| `/intelligence/evidence` | Partial | Expands real normalized facts with their candidate and policy outcome. | Add raw-field provenance, confidence/source attribution, contradictions, and pagination. |
| `/sources/indexers` | Partial | Lists real Newznab configuration and persisted pacing/outcome state without making a network request. | Add capability snapshots, scoped operation history, coverage, and edit/disable controls. |
| `/sources/indexers/:id` | Partial | Shows real endpoint, secret reference name, commission state, minimum interval, cooldown, failures, and latest outcome. | Add capability detail and commissioning history. |
| `/sources/indexer-lab` | Partial | The live route is a real Prowlarr ZIP review/import flow; approved instances remain disabled pending commissioning. | Separate backup import from the approved paced commissioning diagnostics and remove the route-name mismatch. |
| `/sources/download-clients` | Partial | Exposes real AltMount configuration state and endpoint without probing or claiming synthetic health. | Add capability/path observations, queue summaries, and edit/disable controls. |
| `/sources/import-lists` | Partial | Persists owner-configured IMDb CSV export lists and bounded, digest-deduplicated review snapshots. Rows are classified as pending review, already cataloged, or unsupported; snapshot upload is proven not to create media, workflows, searches, release evidence, or downloads. TV rows are held because exact-season selection is required. | Implement explicit movie monitoring approval with metadata verification, exact-season TV review, repeat-snapshot reconciliation, and only then additional approved providers such as Trakt. |
| `/settings/profiles` | Partial | Lists, creates, and edits real quality profiles; the owner can choose best-available resolution and audio ranking without a hard minimum. Reliable provider dimensions are canonicalized while raw evidence remains stored, and bounded release-title audio evidence affects preference only. Required/preferred subtitle languages are canonicalized by the API, and the same screen shows durable completed-output verification, failure, and bounded-retry evidence. Required Danish is enforced after bounded AltMount inspection, while unknown pre-download subtitle evidence stays neutral. Reliable Seerr original-language metadata can route `da` titles to a separate non-default profile; unknown metadata uses the default fallback, and the UI does not call this audio-track verification. | Implement the remaining general cutoff editor and required/preferred audio-track language goals; Forced and SDH remain evidence-only. |
| `/settings/metadata` | Partial | Reports configured direct TMDB, TheTVDB, Simkl, and truthful IMDb evidence modes, exact identity merge rules, provider roles, persisted metadata fields, and exact-season snapshot behavior without probing on page load. Provider values are loaded through read-only local secret-file references; Media Manager does not call the GMD API. | Add artwork choices/locks, catalog-wide refresh review, and sidecar output. |
| `/settings/media-management` | Partial | Reports the running read-only AltMount, Plex-safe symlink, Plex-confirmed upgrade gate, and 30-day recycle contracts without pretending unsupported policies are editable. | Add configuration only for narrowly supported lifecycle policies and naming/companion behavior. |
| `/settings/storage` | Partial | Lists and creates real Plex-backed movie/TV roots with defaults and path mappings. | Add preflight validation, edit/disable, naming preview from real values, and mount/capacity checks. |
| `/settings/connections` | Partial | Lists and creates real Plex, Seerr, AltMount, and staged Newznab records; Newznab commissioning is real and paced. A Seerr request-routing panel maps observed Seerr root folders to library roots and optional quality profiles, and requeues paused requests on save. | Add safe connection tests and edit/disable/default management; surface operation-level state without fake “ready” labels. |
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
| Owner-only browser administration | Real | Cosmos Cloud OpenID authenticates the owner and its `/api` route injects the owner bearer token server-side; every owner API handler independently checks that token. The Seerr webhook posts directly to the API over the internal Docker network with its own secret and does not require an interactive login. |
| Media Manager and Seerr request intake only | Real | Manual API and authenticated Seerr webhook/poll intake are implemented. Compose and runtime contain no Radarr or Sonarr dependency. |
| Manual movie and exact-season requests | Real | Catalog, identities, root/profile/client overrides, season monitoring, immutable episode expectations, and workflows are transactionally persisted. |
| Metadata discovery and identity verification | Partial | Owner-triggered Seerr search/detail APIs return only movie/TV results. Configured direct TMDB, TheTVDB, and Simkl clients perform bounded exact-ID enrichment, reject redirect/identity drift, strip all Simkl redirect queries before validation/persistence, and persist rich provider-separated metadata plus provenance. IMDb identity is exact; rating evidence through Simkl is labeled as indirect and no IMDb scraping exists. Series and movie refreshes have been live-tested with existing verified identities; TheTVDB remains neutral when an exact movie identity is unavailable. Per-item owner refresh is real and audited; automated Seerr intake uses the same merge path. Licensed direct IMDb access, search pagination, artwork locks, and bulk refresh review remain. |
| Seerr reconciliation | Real | Webhook deduplication, five-minute polling, provenance, configuration retry (including a requeue marker that re-evaluates unchanged snapshots), revocation reconciliation, and idempotent verified-availability feedback are persisted. Seerr can also use Media Manager as its Radarr/Sonarr server (`/radarr/api/v3`, `/sonarr/api/v3`): root folders and quality profiles are Media Manager's own, adds are acknowledged while the Seerr request drives intake, and availability scans read Plex-confirmed publications. The service, profile, and root folder chosen in Seerr are stored as request provenance and resolved through owner-created `seerr_library_routes` mappings; an unmapped or unavailable folder pauses the request with `needs_configuration` and the exact folder instead of publishing into the default library. Exact Plex confirmation drives Seerr movie/season availability; partial TV requests keep unconfirmed seasons requested. The `/requests` page surfaces each request's durable lifecycle and attention state. |
| Newznab commissioning | Partial | A disabled instance is paced at at least three seconds, fetches CAPS, requires a usable movie/TV dialect, and performs one random negative query before enablement. A separate developer-only bounded lab has now semantically verified the real movie and complete-season request shapes for all nine enabled standard providers; it is intentionally not a production dependency or live UI feature. Persisting owner-reviewed semantic conformance for future providers remains missing; raw lab evidence must never be imported. |
| Newznab runtime pacing | Real | Per-indexer single-flight lanes, a global bound, persisted next-allowed/cooldown state, `Retry-After`, and bounded backoff wrap supported indexer requests. |
| Release search/evidence/policy | Partial UI | Search runs, raw observations, normalized evidence, decisions, and selection are persisted and now have bounded read screens. Raw provenance, comparison detail, and pagination remain incomplete. |
| Narrow release blocklist | Real | Owner blocks originate from persisted candidates; native blocks bind indexer plus native GUID and pack blocks bind media plus season variant plus exact title. Matching observations remain visible but are rejected before ranking. |
| AltMount retrieval and reconciliation | Real | Submission intent, unknown-outcome recovery, queue observation, SSE/poll reconciliation, frozen output inventory, and read-only source paths are implemented. Every completed selected video is inspected through a bounded file descriptor rooted at that job output before publication. Required subtitle and other completed-output failures remain owner-visible and undeleted. Release-attributable payload failures (archive sets, unplayable manifests, missing Usenet articles) are screened before submission and, when they reach AltMount, exclude the exact release and package and retry another candidate inside the open round automatically — including from the persisted detail when AltMount has already pruned the queue entry. Genuine infrastructure failures still require owner reconciliation. |
| Complete TV season packs as one job | Real | One season workflow creates an immutable expectation; one AltMount job is evaluated for exact episode coverage. Every selected pack video must independently satisfy required subtitle languages through embedded evidence or one strictly matched managed sidecar. Incomplete or ambiguous output stays unpublished and can use only the same bounded exact-release alternate path. |
| Plex-safe symlink publication | Real | Paths are containment-checked, names require verified Plex-supported identity, and video plus verified external subtitle companions are batch-published from the authorized job root. Companion records share the video publication generation and become current only in the exact Plex confirmation transaction. |
| Safe upgrades | Real invariant, partial product | Every selected release has a unique persisted release-ID filename token, allowing old and new paths to coexist through verification. Existing current AltMount links are displaced only inside the transaction triggered after Plex confirms the replacement. Search rejects the exact current durable release and requires another candidate to score strictly higher under the target policy, unless the current release fails a newly required property. An adopted-file workflow requires an exact owner phrase and immutable fingerprint before both workflow creation and any otherwise-unapproved delayed search; after Plex confirms the new publication, the old regular file is moved through a contained fingerprint-checked operation and retained for 30 days. Rollback restores the complete retained cycle, including partial adopted TV coverage, but preserves the full replacement publication until Plex re-confirms every restored path. Physical purge still requires a second exact owner confirmation after retention. Whole-item deletion and overlapping same-scope requests stay blocked throughout the lifecycle. Cutoff presentation and richer owner comparison evidence remain incomplete. |
| Multiple roots/defaults/per-item override | Real backend, partial UI | Movie/series defaults and explicit request overrides are enforced. UI can create/list but cannot validate, edit, or disable roots. |
| Recycle retention | Real | Managed links enter a 30-day persisted cycle and preserve AltMount source. Displaced adopted real files enter a separate fingerprint-bound cycle only after replacement Plex confirmation, are never automatically deleted, and require an exact owner phrase after retention before the purge worker can remove them. Both link restore and Plex-gated whole-cycle adopted rollback are implemented; unsafe collisions stop in an owner-attention state. |
| Existing-library adoption | Partial | Read-only bounded discovery, companion classification, per-item review, exact-season TV scope, compatible catalog/root/profile validation, exact live-action confirmation, contained same-root renames, verified cross-root copy/remove, durable root/path/content fingerprints, retry recovery, reverse rollback, bounded ffprobe media-info capture, Plex path/identity confirmation, current-file provenance, reviewed post-Plex monitoring/search, uniquely named replacements, separately confirmed 30-day displacement, and Plex-gated rollback are real. Monitoring requires the prior same-scope workflow to be resolved; immediate search is separately exact-phrase authorized and series workflows clone the latest immutable episode snapshot. Execution remains capped at 25 approved items. | Add richer journal/recovery UI. |
| Prowlarr backup-only import | Real, with a review gap | ZIP/SQLite parsing is bounded, preserves provider query/grab ceilings, and review creates disabled Newznab instances. Commissioning snapshots advertised movie/TV dialects and allowed parameters before enablement. The owner must manually place extracted keys into the secret environment before approval; there is no Prowlarr runtime connection. |
| Integration/profile/root lifecycle | Partial | Create and list are real. Quality profiles additionally support owner-only edit/enable/default changes with policy-version advancement and canonical subtitle goals. Integration/root update, delete, default reassignment, and most safe connection tests remain missing. |
| Audit/history | Partial | Core writes append `audit_events` and workflow transitions and now have a bounded owner read API/UI; not every operation emits an audit event and pagination/filtering are missing. |
| Component health | Partial | Process, PostgreSQL/schema, outbox, integration configuration, and persisted indexer-operation state are real. Bounded Plex/AltMount/Seerr operation status is still missing. |
| Owner notifications | Real | Exact audit-event subscriptions queue durable webhook deliveries; endpoint paths, audit detail, and secret values are excluded from owner responses and delivery payloads. Delivery attempts, bounded errors, response status, and retry state are persisted. |
| Import-list snapshots | Partial | Owner-supplied IMDb exports are parsed with strict identity/year/duplicate bounds and persisted as review-only snapshots. Applying monitoring decisions and external provider synchronization are deliberately absent. |
| Encrypted backups | Partial | Durable backup work, authenticated chunked encryption, checksum validation, isolated full restore verification, schema validation, safe history, and encrypted download are implemented. A production-restore workflow and automatic retention deletion are deliberately absent; live creation requires an external encryption key. |
| Machine API keys | Missing | Scoped issuance, rotation, and revocation do not exist; browser owner authentication remains the only owner API boundary. |

## Current owner API

Real handlers exist for liveness/readiness; dashboard; activity; managed files;
release intelligence and narrow blocklist; durable AltMount subtitle verification review; dated episode calendar; manual collections; owner notifications and delivery history; review-only import-list snapshots; encrypted backup history/queue/download; journaled same-root and cross-root adoption review/execution/Plex confirmation; source/pacing state; system health/tasks/info; owner-triggered Seerr metadata search/detail; wanted gaps; media list/create/detail/recycle;
workflow list/detail/create/search/cancel; recycle list/link restore/Plex-gated adopted restore/owner-confirmed retained-real-file purge; Prowlarr backup
create/detail/approve; integration list/create/Newznab commission; library-root
list/create; quality-profile list/create/update; and authenticated Seerr webhook intake.
Every response is `no-store`. Mutation handlers are real actions and are not used
for smoke testing without explicit approval.

## Infrastructure and verification status

- The three Compose services are healthy: PostgreSQL, API, and UI. Schema 43 preserves credential sanitization, metadata protections, durable Newznab reservation budgets and immutable acquisition rounds. It includes Seerr root-folder routing (39), release-payload recovery (40), bounded NZB retrieval recovery (41), reported AltMount progress (42) and configurable acquisition slot bounds (43).
- Cosmos 0.23.4 is the public edge and the owner authentication layer. It
  authenticates the owner with OpenID and routes `/api/*` and `/health/*` to the
  API container and every other path to the UI container on one origin. The
  `/api` route injects the owner bearer token server-side, so the browser never
  sees it. The former loopback Caddy gateway and its Basic Auth are removed; the
  API is also reachable on the host at `127.0.0.1:${MEDIA_MANAGER_API_PORT}` with
  the owner token for machine checks.
- Baseline verification passed: ESLint, TypeScript, UI unit and Chromium tests,
  production builds, deterministic library benchmark, Go formatting, vet and
  race tests, PostgreSQL lifecycle tests, and Compose validation. All nine
  enabled Newznab endpoints passed live CAPS, one paced random negative TV query,
  and a separate bounded semantic check of Media Manager's real extended movie
  and complete-season request shapes. OldBoys remained disabled and uncontacted.
- The live UI artifact is dependency-separated from the development concept
  fixtures; an automated artifact scan verifies that fixture records are absent.

## Implementation order from this audit

1. Add richer adoption journal and recovery detail without expanding the live-action batch limit.
2. Complete configuration edit/disable/test lifecycles and full profile rules.
3. Finish remaining partial read models and approved route interactions, preserving
   truthful empty/error states and never substituting fixture data.
