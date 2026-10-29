# Media Manager core release status

## Everyday app (2026-10-29, source)

A new poster-first interface replaces the V2 preview and becomes the default
at `/`: Home, Library, title pages, Requests, Needs you, Add and Settings, in
plain language and with a phone layout. Owner actions are only the server's
available workflow actions and the existing Seerr decisions; Add uses the same
verified-identity and exact-season payload as Discover. The expert console is
unchanged apart from its overview moving to `/console` and its request table
to `/console/requests`. No API, schema, acquisition or publication change.
Local lint, typecheck, docs, unit and browser gates passed; this note is source
evidence, not deployment evidence.

## Native collection-pack discovery (2026-10-05, source)

The OldBoys query cycle now includes title-only searches, alongside ordinal and
verified calendar-year searches, and visits four bounded pages for each shape.
Each scan still sends one request per provider. This fixes collection titles
such as `Operation.X.S01-S16.DANISH.720p.AAC2.0-PlexLover` being invisible to
`S16E06` or `2019` filters. Native range admission records the explicitly declared
season membership without inventing episode IDs or rewriting the first-season
facts. Malformed or out-of-catalogue ranges remain held; unrelated titles,
conflicting IDs, quality rules and workflow/round protections still apply.

For a selected native collection, output evaluation checks only the immutable
requested season/episode scope. Extra clearly numbered episodes in the declared
range remain retained and unpublished by that workflow. Missing, duplicate,
unsafe, ambiguous, composite and out-of-range media cannot pass verification.
The full inventory digest remains bound to the check. Media/subtitle inspection
and exact Plex confirmation still gate completion. This introduces no migration
or automatic fan-out of download jobs; each workflow uses its existing bounded
acquisition lifecycle. Source gate/deployment and completed-pack evidence are
pending at this checkpoint.

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
PR #43 is deployed at `666056b45350026da38a9024e4494eec2ad01e9c`:
[Verify 37337754290](https://github.com/Media-manager52/Media-manager/actions/runs/37337754290)
passed all five jobs and
[Deploy 37339782861](https://github.com/Media-manager52/Media-manager/actions/runs/37339782861)
succeeded with backup, schema 44 and healthy matching API/UI images. Native owner
commissioning passed all five controls and enabled the source at 16:20:19 UTC.
Normal worker searches succeeded; deployed read-only desktop/tablet/mobile checks
passed with configured/enabled status and empty credential fields. This proves
provider integration, not acquisition of every waiting request.


## OldBoys credential setup (2026-10-05, source)

An owner-only form under Settings → Connections accepts the OldBoys API Token,
Pass Key (PID), and RSS Key (RID). Schema 44 adds encrypted credential storage;
the API requires a separate server sealing key. Read responses contain status
only, and password fields clear after a successful save. This prepares native
provider work without enabling the retired proxy or claiming provider validation.
See [setup and recovery](../operations/oldboys.md). Deployment evidence remains
pending at this source checkpoint.

## Completed-output recovery and duplicate prevention (2026-10-04)

The deeper backlog review identified repeated exact-title releases with
different NZB hashes after completed subtitle failures, and a usable movie
rejected because its inventory included a sizeable film under `extra/`.
Completed negative subtitle evidence now guards fresh searches, persisted
selection paths and final retrieval admission against exact-title cross-posts.
Infrastructure/access failures are not broadened into title exclusions.

Movie primary-file selection ignores only explicit bonus directories. An
owner-only, one-use retained-movie action can inspect the latest ambiguous
last-slot output under the unchanged policy, without requesting another NZB
or resetting any accounting. Publication binds to the exact verified asset
ID/path. Multiple features outside explicit bonus directories remain blocked.

A provider's matching movie ID no longer overrides an explicit release year
more than one year from the canonical year. The final retrieval guard also
covers old persisted eligible decisions, preventing mislabelled remake grabs.
Required subtitles, identity/scope and exact Plex confirmation still apply.

Movie event titles can place the canonical year at the end (`SummerSlam
2026 Sunday` → `SummerSlam Sunday 2026`); every other word retains its order.
Other event parts, years and arbitrary word reordering remain rejected.

No schema change is needed. Tests cover negative cross-post evidence,
selection/retrieval guards, bonus selection, retained inspection concurrency
and policy/publication guards, verified asset binding, and remake-year
contradictions. GitHub Verify/Deploy receipts and the running commit establish
deployment; this source note alone does not assert production rollout.

## Owner recovery of stopped searches (2026-10-04)

An owner-only discovery action advances an exhausted workflow’s existing
paced search without opening a round or downloading. It preserves partial-scan
provider scope, counters and all failure evidence, invalidates obsolete
generations, and is idempotent while a current scan is due or running.

An open round restored under the current policy now permits an owner search
even when historical discovery still points at an older exhausted round.
The obsolete schedule is paused transactionally; active jobs and policy
mismatches still block recovery. No schema or media-property change applies.

AltMount's explicit "compressed media files are not supported" failure now
uses the existing unsupported-archive recovery, like its older "compressed
files" wording. Only the exact release/package is excluded; the next candidate
uses the current bounded round. Infrastructure error precedence is preserved.

A successfully parsed video stream with no usable codec and an explicit `encv`
tag is now a typed no-playable-media failure. The exact release/package stays
retained and excluded, and bounded acquisition recovery can try an alternative.
Unknown codecs without this explicit evidence and failed probes remain
infrastructure attention. Required subtitle and Plex verification still apply.

## Waiting search evidence (2026-10-04)

A read-only production review covered all 79 waiting requests. All were linked
to workflows; 43 carried the generic no-eligible reason. Of those, 31 had no
stored candidates, 12 had observed candidates, and eight included latest scans
with incomplete provider coverage. These are search outcomes, not evidence of
global unavailability. Saved original titles and aliases were ignored by both
query construction and title matching. An offline replay of stored facts found
113 previously title-rejected, unattempted candidates across five titles that
can satisfy existing policy/match checks when saved aliases are honoured;
Danish output verification remains required.

Scheduled scans now rotate one saved name per complete scan, with no additional
requests per scan, and matching accepts provenanced saved aliases. Partial scans
keep their query title, do not increase the empty-search backoff, and retry with
a 30-minute floor or the earliest later provider deferral. Request summaries
identify historical partial scans; the UI describes the latest search rather
than implying permanent absence. Identity/year/scope, acquisition-round, failed
output retention, exclusions and provider pacing protections remain intact.

Focused regressions, full local Go vet/race tests with disposable PostgreSQL,
frontend lint/typecheck/docs/build, 46 unit cases and 153 browser cases passed.
GitHub Verify/Deploy runs and the authenticated running commit remain the
deployment evidence.

## V2 test UI (2026-10-01)

An opt-in preview at `/v2` provides a new scoped shell, poster library, request
progress and attention inbox. The current interface offers **Try V2** and stays
available. Preview controls perform local search/filter/pagination/theme changes
and read existing owner models; detailed recovery links open the current UI.
Approval, workflow admission, recorded searches and Plex confirmation remain
distinct. No schema, provider, acquisition or publication policy changes apply.

Local lint, typecheck, docs, build, all 46 unit cases and 153 browser cases
passed. Coverage includes four new routes in dark/light/mobile, seven preview
interaction regressions, keyboard/a11y checks, real fallback assets and blocked
external/mutating requests. Deployed acceptance also captures the V2 routes;
the exact GitHub Verify/Deploy runs and running commit remain deployment truth.

## Dependency security follow-up (2026-10-01)

Next.js and its ESLint configuration are pinned to 16.3.6, addressing
[the critical `next/og` advisory](https://github.com/advisories/GHSA-vcvr-r3jv-pc5j).
No application `ImageResponse` route was identified. The lockfile also resolves
Undici 7.29.1, fflate 0.7.5 and the legacy Drizzle loader's esbuild 0.25.12.
Scoped overrides preserve the existing parent packages instead of accepting
npm audit's suggested major downgrades. Remove these overrides when upstream
parents incorporate the fixes.

The resolved dependency audit reports zero known vulnerabilities. A disposable
SQLite schema generation exercised the overridden Drizzle loader and emitted
the expected table SQL without a database connection; a local ZIP roundtrip
checked fflate. The normal frontend, browser and isolated container gates still
apply. GitHub Verify/Deploy runs and the running commit identify deployment;
this source note alone does not assert that the update is deployed.

## Seerr waiting progress and VPS deployment checkpoint (2026-10-01)

[PR #34](https://github.com/Media-manager52/Media-manager/pull/34) explains
successful Seerr admission separately from acquisition waiting. Request summaries
expose recorded waiting reasons and last/next search times; completed events and
stale workflow revisions/discovery generations cannot promise another search.
Discovery candidates requiring a new owner-approved round correctly appear under
Needs attention. No schema or acquisition-policy change is required.

Local verification passed full Go vet/race gates with disposable PostgreSQL
(final query package: 381.808 seconds), frontend lint/typecheck/unit/build/docs
and 134 browser cases. A read-only production query plan measured 29 ms after
aggregating search summaries once. The operational recovery used the existing
production owner action: nine reviewed rounds yielded six Plex confirmations
(Bedeviled, Under the Skin, Dexter S07, Lucifer S02 and NCIS S12/S15). CSI
S01/S04/S13 exhausted four attempts on incomplete or subtitle-unverified outputs;
failed output inventories and exact exclusions remain retained.

[PR #33](https://github.com/Media-manager52/Media-manager/pull/33) moves GitHub
verification and private image publication onto the isolated VPS runner and adds
owner-triggered deployment with independently checked commit/run identities,
protected backups and health checks. Twelve deployment-boundary regressions and
an isolated rootless stack test passed, including restrictive checkout
permissions, UI assets, authentication, four/eight limits and a readable database
dump. The [operations guide](../operations/github-vps.md) describes the boundaries.
GitHub Verify and Deploy runs record results for exact commits; the running API
identity is the deployment truth. Earlier deployed snapshots below are dated
history. Off-server backups remain outstanding.

## Deployed source checkpoint (2026-09-30)

Production API and UI run `34f0024` (PR #31), version `2026.09.30`, with
schema 43. PRs #25–31 are merged on GitHub. This documentation checkpoint
does not rebuild or redeploy the application; the deployed source identity
remains `34f0024`.

The owner configured four acquisition attempts per new round and eight
retained failed outputs per workflow. Existing rounds keep their original
immutable limits. Seventeen reviewed workflows were resumed through supported
owner actions: twelve searches in open rounds and five rounds from known
eligible alternatives. At the recorded recovery snapshot, attention fell
from 31 to 14 and three additional workflows reached exact Plex confirmation.
All 60 original reviewed output inventories remained present. Remaining
inspection/import failures, episode-snapshot conflicts, provider redirects
and missing verified subtitle alternatives still require recovery or review.

The deployed read-only browser suite passed all 117 checks after deployment.
Database readiness, schema 43, zero due/retrying outbox work and no degraded
enabled sources were verified again at 21:57 UTC. The local verification
below passed; GitHub Actions for PRs #30–31 could not start because of account
billing/spending limits. Remote CI is not recorded as passing.

All three deployed AltMount source fixes, including regression tests, are
preserved in the [reproducible patch bundle](../operations/altmount-patches/README.md).
Protected on-server backups exist; off-server backups remain outstanding.
Schema 43 requires a compatible binary or a controlled database restore for
rollback; starting the former schema-42 binary alone is insufficient.

## Configurable acquisition budget verification (2026-09-30)

The owner requested more acquisition slots after reviewing the remaining Seerr
backlog. Startup configuration now supports bounded round attempts (default 2,
range 1–6) and retained failed outputs (default 4, range 1–12). New rounds
snapshot the attempt budget; old snapshots and exact release/package exclusions
stay unchanged. Raising retention never deletes failed media or starts work.
Schema 43 widens slot constraints while preserving the immutable-round trigger.

Verification passed: full Go vet and race suite with disposable PostgreSQL
(postgres package: 377 seconds), focused configurable-budget and schema-42
upgrade regressions, lint/typecheck/build, 45 UI unit cases, 133 browser cases,
docs links, Compose configuration, and an isolated container build/smoke with
schema 43 and four/eight limits. The default two/four behavior remains tested.
The workflow table displays the configured retention limit and actual round
snapshot; messages no longer assume every round has two slots. Production
recovery evidence is recorded separately in the owner's private report.

## Seerr acquisition and request-status follow-up (2026-09-30)

Production was verified at `ef6a628`, schema 42, after PRs #25–29. Provider
download recovery, IPv4-only indexer egress, definitive incomplete-package
fallback, deleted-request lifecycle handling and audio-only output recovery
are deployed. The request-notice follow-up in this change also fixes the
detail banner that called every intake message “Needs attention”, including
“Seerr reports the request completed”. Notice labels/tones now follow the
actual lifecycle, while original messages and genuine attention remain visible.

The companion AltMount source branch `fix/bounded-probe-failover` is deployed
at `321b3ba`: provider fallback has its bounded time budget, legacy renamed
gzip NZBs are decoded correctly, and single-file metadata names retain their
extension when no separate extension override is supplied. Pending NZB queue
and upload directories now have protected persistent host mounts. Verified
database/configuration backups and all three source patches are retained.

Live recovery reduced the overview from 87 to 31 attention items at the
post-PR-#29 snapshot. All eight S.W.A.T. seasons and Supernatural S05 are
Plex-confirmed; the stale Minions & Monsters intake was recovered through
normal authoritative Seerr re-reading. Newly added Runner, Coyote vs. Acme
and The Uprising requests also reached Plex confirmation. The remaining
backlog includes retained-output limits, unusable or uninspectable media,
older pruned/generic import failures, an immutable episode-snapshot conflict
and new-round decisions. It is not declared resolved or silently cleared.

The backend milestones passed full race suites with disposable PostgreSQL,
vet, focused regressions and all eight CI checks each. AltMount passed its
full backend race suite and vet for each source milestone. The deployed
read-only browser suite passed 117 checks before the final notice change;
the notice change has its own focused render/browser regressions and full
frontend gates. Required Danish subtitles, failed-output retention, exact
release/job identities, shared pacing and publication verification remain
enforced. Only MediaManager uses current indexer keys on this VPS, through
IPv4 egress `159.195.149.137`; other machines' historical activity remains
unverified. Off-server backups are still outstanding.

The older assessments below are historical evidence.

Assessed against the live stack on 2026-09-02, updated at the V1 release
closeout on 2026-09-14, and refreshed on 2026-09-28. The deployed API and UI
were independently verified at `78c127b`, schema 40, on 2026-09-28; see the
follow-up immediately below. Earlier sections are retained as historical evidence for the schema-32,
schema-35, and schema-36 milestones.

“Implemented” means that the core behavior exists in the real application.
“Synthetic tested” means that its local deterministic tests pass. “Live tested”
means that the configured services completed the real operation; health checks
alone do not qualify.

## Local retrieval recovery follow-up (2026-09-28, not deployed)

Production was independently verified at `78c127b`, schema 40. The local schema
41 follow-up fixes stranded retrieval failures, checks admission before provider
grabs, and ranks Danish clues without treating them as verified subtitle facts.
See the [review and reproduction SQL](retrieval-recovery-review.md).

Validation passed: `gofmt`, focused recovery/migration regressions,
`go vet ./...`, `GOFLAGS=-mod=readonly go test -count=1 ./...`, the full
`go test -count=1 -race ./cmd/... ./internal/...` suite with disposable
PostgreSQL on port 5433 (postgres package: 314 seconds), and `npm run docs:check`.
No production migration, deployment or request resume was performed.

## Post-V1 hardening (2026-09-28)

The deployed source is `c6bf1b8` at schema 40
(`0040_release_package_failures.sql`, additive). This milestone finishes the
Seerr integration and hardens acquisition against defective release payloads.

**Seerr without Radarr/Sonarr.** Media Manager serves a Radarr/Sonarr-compatible
API (`/radarr/api/v3`, `/sonarr/api/v3`) behind a dedicated `SERVARR_API_KEY`.
Root folders and quality profiles resolve to Media Manager's own records, adds
are acknowledged without creating a second catalog, and availability scans
report Plex-confirmed publications. Live verification passed: unauthenticated
and wrong-key requests return 401, version routes answer Radarr 5.x / Sonarr
4.x, unknown lookups return id 0 (forcing an add), and known lookups return the
synthetic id with `hasFile`/`monitored`. Seerr is now configured: the Radarr
entry (base URL `/radarr`, default, profile `Movie Best Available Danish Subs`,
directory `/libraries/movies`) and the Sonarr entry (base URL `/sonarr`,
default, season folders enabled, profile `TV Best Available Danish Subs`,
directory `/libraries/tv`) both point at `media-manager-api-1:8080`. Seerr's own
servarr clients were verified against the saved configuration: Radarr reports
5.14.0.9383 and returns Moana (TMDB 1108427) with `hasFile: true`; Sonarr
reports 4.0.10.2544 with both TV profiles and nine root folders. Both entries
keep `syncEnabled` **off**: Seerr's scheduled Radarr/Sonarr scan resets and
declines processing media that its server list does not contain, which would
orphan-decline paused or not-yet-ingested requests. Media Manager already
pushes exact Plex-confirmed availability itself, so the server entries exist
for adds, lookups, and commands rather than library scanning.

**Request backlog reconciled.** Of 412 Seerr requests, 278 were already
complete. The remaining 134 were classified: 118 had never reached Media
Manager. All 118 were backfilled through the normal intake path (114 ingested;
2 blocked on media sitting in the recycle bin; 2 unreleased titles with no
TMDB release date). Another 4 delivered requests were pushed to Seerr as
available/partial, and 8 were already active in Media Manager. The backfill
created 123 workflows; more than 16 titles were already imported into Plex
within the first hour.

**Danish-subtitle policy clarified and enforced.** `da` remains required for
foreign content and is not required for Danish originals (the two
`Best Available Danish Original` profiles route on canonical original language
`da`). 42 of 43 Danish-origin titles were already on the correct profile; the
one misroute was corrected. 68 foreign titles currently wait because no
available release carried verified Danish subtitles — the truthful result of
the required-language policy, not a workflow failure.

**Release payload screening and recovery (schema 40).** Every NZB manifest is
screened before submission: archive-only sets (`.rar`/`.r00`/`partNNN.rar`,
`.7z`, `.zip`), ISO/DVD structures, and other recognized non-playable
containers are rejected without an AltMount upload; obfuscated manifests are
never rejected by name alone. AltMount queue failures for archives
(`compressed files are not supported`, RAR iteration errors), `no files with
allowed extensions`, and missing Usenet articles (`incomplete or missing
parts`) now exclude the exact release and package and retry another candidate
inside the open round, including when AltMount has already pruned the failed
queue entry. Live recovery of the 19 previously parked failures produced two
immediate Plex confirmations from replacement releases, with the rest
retrying or waiting for the next discovery cycle; only two genuine
infrastructure items remain owner-visible.

The AltMount failure analysis behind this change was concrete: WEB-DL and
REMUX single-file releases imported at roughly 92/94, while RAR-packed WEBRip
releases failed 7 of 9 and DVD/ISO releases failed every attempt.

**Other fixes.** Seerr metadata that reports `runtime = 0` no longer
invalidates intake (zero now means unknown), which had left 15 titles
retrying forever.

**Verification.** `gofmt` and `go vet` clean; full unit suite; `docs:check`;
and the PostgreSQL-backed race integration suite for `internal/postgres`,
`internal/acquisition`, and `internal/httpapi` all pass against a disposable
PostgreSQL 18.6 cluster. Schema 40 was applied to the live database.

**Follow-up request audit (same day).** A full audit of all 413 Seerr requests
classified the 96 open ones: 36 in flight, 44 blocked by the
required-Danish-subtitle policy (33 round-exhausted, 7 held, 4 series
seasons), 7 provider/NZB retrieval failures, 5 infrastructure attention, and 3
airing partials. The `Best Available Danish Subs` profiles keep Danish
subtitles **required** for foreign content, and named `Danish Original` routing
is unchanged; a same-day experiment that relaxed the requirement was reverted
at owner direction before any relaxed-policy verification or Plex confirmation
completed (zero confirmations during the window). Because acquisition-round
snapshots are immutable, the brief experiment had replaced round snapshots;
the rollback restored the da-required snapshots and returned the affected
workflows to their truthful waiting/held states. Provider fix: msgnews
download locators 308-redirect to `www.msgnews.live` while carrying the API key
in the path, which the cross-origin credential policy correctly rejected;
Media Manager now requests the canonical `www` host directly, keeping the key
on the same registrable site. Two Plex confirmation timeouts (`Unraveled`,
`Rumble in the Bronx`) were retried against their already-verified links.
During the audit, Bones, Cobra Kai, Peaky Blinders, Stranger Things, and
Supernatural were pushed to completed; Breaking Bad seasons 2–4 were ingested
and are Plex-confirmed; `Sommerdahl` was identified as an unmanaged Plex file
that needs adoption rather than acquisition.

## V1 release closeout (2026-09-14)

Media Manager V1 is complete and released. The functional release source is
`e339208d0f052b166f14e4beb08bdea9bd69485b` (`test(requests): cover live Requests
filters, search, and detail`), fast-forward merged from `feat/v1-final-regression`
into `main` with history preserved and no force push. Production already ran this
exact source and was not redeployed. The database schema is 36
(`0036_seerr_request_display.sql`, additive).

Accepted V1 scope:

- Movie end-to-end accepted.
- Complete-season-pack end-to-end accepted as one acquisition job.
- Failure/recovery accepted: a failed output stays retained, unpublished, and
  owner-visible, and cannot be recorded as success.
- Plex publication accepted through managed Plex-safe symlinks confirmed by exact
  Plex identity and path.
- Final regression accepted against a disposable PostgreSQL 18.6 cluster.
- Cosmos-only public architecture: Cosmos Cloud is the only public edge. The
  redundant Caddy gateway was removed and `/api` plus `/health` route directly to
  the API with the owner bearer injected server-side.
- Functional owner UI across library, workflows, wanted, files, sources, settings,
  health, audit, adoption, and release evidence.
- Requests/Seerr management UI with filters, search, and exact detail.
- Live read-only browser acceptance 117/117 across desktop, tablet, and mobile,
  including the Requests interactions.

Release verification at the closeout source: `gofmt` and `git diff --check`
clean; `go vet` pass; `go test -count=1` and `go test -count=1 -race` pass
against a disposable PostgreSQL 18.6 cluster (including the commissioning
capability activation, concurrent-activation rollback, migration, season-pack,
and Plex publication tests); frontend TypeScript, ESLint, production build, 26
unit/artifact tests, and 10 local Chromium/axe tests pass; loopback API smoke and
Cosmos public-edge routing smoke pass; Seerr webhook authentication remains
valid.

GitLab pipeline #2847605585 for `e339208` passed the `browser` and `backend` jobs.
The `frontend` job passed lint, typecheck, tests, and build but failed its extra
`npm audit --omit=dev` step on one pre-existing moderate transitive advisory
(`baseline-browser-mapping`, GHSA-w5vr-8v7q-w6rv), which is not part of the V1
release gates. No release tag was created because the repository has no
established tag convention. No known V1 product blockers remain; OldBoys stays
intentionally disabled.

## V1 release candidate (2026-09-14 terminal regression)

The repository source at this milestone is the V1 release candidate at schema 35.
Commissioning A-E remain intact, and the accepted V1 workflow milestones below
are preserved without behavioral change. The four V1 real-acceptance runs passed
against the configured stack:

- V1-1 movie end-to-end (`3f0a547`): one successful workflow with exact Plex
  confirmation and zero acquisition, publication, verification, or Plex
  failures.
- V1-2 season-pack end-to-end (`220a08c`): one complete season pack published
  seven files with exact episode coverage and Plex confirmation, with zero
  failures.
- V1-3 failure/recovery (`022daf1`): one successful movie workflow with zero
  acquisition, publication, verification, or Plex failures.
- V1-4 Plex publication (`09ab2f2`): four movies and one season pack confirmed
  by exact Plex identity and path, with zero publication or recognition
  failures.

The terminal V1-5 regression re-ran the full Go suite and the race suite against
a disposable PostgreSQL 18.6 cluster, including the required commissioning
capability activation and concurrent-activation rollback tests, together with
the repository frontend lint, typecheck, production build, unit/artifact,
benchmark, and Chromium/axe suites. No concrete behavioral regression was found,
so this section records the candidate without changing runtime behavior. That
candidate was subsequently promoted to `main` at schema 36; see the V1 release
closeout above.

## Deployed schema-32 recovery milestone

The deployed source adds durable fixed two-slot acquisition rounds,
monotonic lifetime attempts, exact release/package attempt registries, immutable
first-seen sequencing, partial per-indexer recovery scans, deterministic paced
discovery, owner-gated next rounds, same-job infrastructure recovery, retained
output accounting, owner-visible candidate evidence, low-priority scheduling,
and a complete current-publication-set guard for upgrades.
It does not open a new round automatically and cannot make waiting or inspection
look successful. GitLab pipeline 26 passed frontend, Chromium, and the full Go
race suite against disposable PostgreSQL 18. The live API and UI are healthy,
schema 32 is ready, and the existing future outbox event remained non-due.

| Core item | Implemented | Synthetic tests passed | Live tested | Blocked | Missing |
| --- | --- | --- | --- | --- | --- |
| 1. Cosmos route | **Yes** — Cosmos’s supported route configuration targets `http://127.0.0.1:28080`, the authenticated Media Manager gateway, rather than the UI container. | **Not applicable** — this is external configuration rather than repository behavior. | **Yes** — authoritative route state and the gateway authentication boundary were verified without changing unrelated Cosmos fields. | **No**. | **No**. |
| 2. Seerr discovery and intake | **Yes** — exact metadata identity, movie and selected-season intake, webhook/poll reconciliation, deduplication, revocation reconciliation, and verified-availability feedback are durable. Status feedback uses Seerr’s supported media-status API only after exact Plex confirmation. | **Yes** — adapter, intake, identity, season snapshot, idempotency, partial-season status, and PostgreSQL lifecycle tests pass under race detection. | **Yes** — real movie and multi-season TV requests created exact workflows. Reacher, Supernatural, NCIS, and Dexter preserve per-season state; confirmed seasons are available while unfinished seasons remain requested. The A Taste of Hunger movie and The Investigation season became available only after exact Plex confirmation. | **No** — stale “available” records already inside the isolated Seerr can truthfully prevent a duplicate request even when exact Plex lookup finds no item; that test-data discrepancy requires owner cleanup in Seerr, not a Media Manager bypass. | **No core path**. |
| 3. Newznab/Prowlarr import and commissioning | **Yes** — bounded Prowlarr backup import creates disabled review candidates. Commissioning records provider-specific CAPS/dialects, paced negative validation, rolling query/grab budgets, and enables only passing standard Newznab endpoints. Runtime requests use the commissioned dialect and exact IDs where supported. Redirects are manually bounded to two; every hop rechecks the workflow guard, pins only validated public addresses, preserves TLS hostname verification, disables proxies/reuse, strips cross-origin credentials and Referer, and runs strict bounded NZB validation. | **Yes** — parsing, commissioning, dialect, budgets, pacing, guard/revision/candidate protection, two-hop redirect, DNS-rebinding, TLS/SNI, proxy, mixed-address, credential-leak, redaction, sanitizer, and selection tests pass. | **Yes** — ten Prowlarr candidates were imported; nine standard endpoints were commissioned and completed bounded real request-shape conformance. Real movie and season workflows used paced Newznab search/retrieval. | **No core blocker** — OldBoys remains intentionally disabled because its custom API is not Newznab. | **No core path**. |
| 4. AltMount submission and completion reconciliation | **Yes** — durable submission intent, unknown-outcome recovery, queue reconciliation, frozen output inventory, read-only source handling, exact NZB-manifest identity, and non-blocking persisted pacing are implemented. Completed output inspection is bounded to the authorized job root. Failed output remains owner-visible and undeleted. Each round has two submission slots; another round requires a newly observed exact identity and owner approval. | **Yes** — adapter, state machine, containment, exact release/package exclusion and deduplication, round/lifetime accounting, retry deferral, retained-output, concurrency, replay, and PostgreSQL lifecycle tests pass. | **Yes** — multiple real movie and season jobs were submitted, completed, reconciled, inspected, and either safely retained or published. The schema-32 recovery controls themselves are deployed but have not initiated a new live acquisition. | **No**. | **No**. |
| 5. Complete TV season-pack handling | **Yes** — an immutable exact-season expectation is processed as one AltMount job; every expected episode must map exactly once and every selected file must pass the profile’s completed-output requirements before publication. | **Yes** — complete, incomplete, duplicate, extra, multi-episode, embedded-subtitle, strict-sidecar, per-video Danish, retained-failure, and exact alternate-package cases pass. | **Yes** — Adolescence season 1 verified required Danish subtitles on all four files after a retained failed first attempt. The Investigation season 1 verified all six expected episodes in one final pack after one retained empty-output attempt; its Danish-original profile did not falsely claim subtitle evidence. | **No**. | **No**. |
| 6. Plex naming, symlink publication, and exact verification | **Yes** — verified provider identities drive contained Plex-compatible naming. Managed videos and qualifying sidecars share one generation. Nothing becomes current until exact Plex identity/path confirmation; upgrades retain the old current generation until the replacement transaction is confirmed. | **Yes** — naming, containment, collisions, companion generation/lifecycle, exact movie/series identity, path confirmation, displacement, rollback, and timeout tests pass. | **Yes** — Klaus, Adolescence, A Taste of Hunger, and The Investigation have durable exact Plex confirmations. The Investigation confirmed all six episode paths. A real A Taste of Hunger replacement left the old generation current until the exact new Plex confirmation and retained both read-only AltMount source files. | **No**. | **No**. |
| 7. One real movie acceptance workflow | **Yes** — the complete movie lifecycle is implemented. | **Yes** — discovery, policy, paced search, retrieval, inspection, publication, exact confirmation, and safe replacement are covered. | **Yes** — A Taste of Hunger (2021), TMDB `766105` / IMDb `tt11188560`, routed by canonical original language `da`, completed one AltMount acquisition, bounded verification, managed publication, exact Plex identity/path confirmation, and Seerr availability feedback in about 47 seconds. | **No**. | **No**. |
| 8. One real complete-season-pack acceptance workflow | **Yes** — the complete exact-season lifecycle is implemented. | **Yes** — frozen expectation, one-job pack evaluation, bounded retry, per-file verification, publication, and exact episode confirmation are covered. | **Yes** — The Investigation (2020) season 1, TMDB `110333` / TVDB `378992` / IMDb `tt9112152`, froze six expected episodes. Attempt one completed without allowed video and remained retained. A different exact release failed retrieval without consuming an AltMount attempt. The second and final AltMount acquisition verified six of six episodes, published six managed files, confirmed every exact Plex path, and updated Seerr in about 2 minutes 47 seconds. | **No**. | **No**. |
| 9. Core live website parity with the approved design | **Yes** — the approved dense dark library composition is implemented with real movie and TV data, complete bounded catalog loading, responsive desktop/mobile layouts, accessible selection, filters, summaries, pagination, details tabs, and preserved routes/actions. Fixture data cannot enter the live artifact. | **Yes** — lint, TypeScript, production builds, 21 unit/live-artifact tests, and 10 Chromium interaction/accessibility tests pass. Tests block unexpected network access and cover add-without-search, complete catalog paging, selection replacement, filters, grid/list mode, details tabs, keyboard use, mobile fit, TV navigation, profile persistence, and bounded recovery. | **Yes** — the redesigned UI is deployed and healthy behind the local gateway; authenticated local smoke and route rendering passed. The owner has exercised the live site. | **No**. | **No core parity item** — richer evidence and administration views in `docs/status/implementation.md` are scoped future enhancements, not missing core workflow truthfulness. |

## 2026-09-02 release evidence

- `main` and `origin/main` matched at `1a4d53f197fc6d5647ee97e63a24d0eb5b93d256`
  before this documentation closeout. The API binary reports that exact commit.
- API, UI, PostgreSQL, and gateway are running. API, UI, and PostgreSQL health
  checks pass. Schema 32 matches the deployed binary.
- The outbox has one future, non-due `workflow.search_requested` event and zero
  due or retrying events. It is intentional workflow state, not a stuck worker.
- Full current gates pass: Go formatting/vet/race tests and disposable
  PostgreSQL lifecycle tests; frontend lint, TypeScript, deterministic library
  benchmark, 21 UI tests, 10 Chromium tests, production build; and Compose
  validation.
- Seerr verified-availability feedback completed 17 backfilled/live events with
  no pending, retrying, or failed status-sync event. Partial TV availability is
  season-exact rather than a fabricated whole-series success.
- Future upgrades reject the exact current durable release and require a
  strictly higher policy score unless the current release fails a newly required
  property. This preserves the existing current media while still allowing a
  truthful hard-requirement correction.

## Safe stops and non-core follow-up

- Several requested seasons remain in owner-attention states because two real
  attempts could not verify their required Danish subtitles. Their output is
  retained, unpublished, and undeleted. This is the required policy result, not
  a fake workflow success or a release-code failure.
- Treasure Maps returned a bounded 403 during one retrieval. The release was
  excluded only inside that workflow and another eligible provider was used.
- OldBoys remains disabled and uncontacted because it requires a reviewed custom
  adapter rather than Newznab emulation.
- A post-hardening Treasure Maps replacement was previously proven loaded using
  local non-reversible comparison. The later, broader provider-side
  reissue/revocation of the seven active credential references remains deferred
  by owner choice. It requires each provider’s supported account mechanism and
  is not included in source or recovery artifacts.
- The local test runner may report Vite HMR port `24678` already in use. All
  production builds and tests pass; the deployed UI is unaffected.
