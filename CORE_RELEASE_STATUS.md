# Media Manager core release status

Assessed against the live stack on 2026-09-02. The deployed API and UI were
built from `1a4d53f197fc6d5647ee97e63a24d0eb5b93d256`. The database schema is 32.

“Implemented” means that the core behavior exists in the real application.
“Synthetic tested” means that its local deterministic tests pass. “Live tested”
means that the configured services completed the real operation; health checks
alone do not qualify.

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
| 9. Core live website parity with the approved design | **Yes** — the approved dense dark library composition is implemented with real movie and TV data, complete bounded catalog loading, responsive desktop/mobile layouts, accessible selection, filters, summaries, pagination, details tabs, and preserved routes/actions. Fixture data cannot enter the live artifact. | **Yes** — lint, TypeScript, production builds, 21 unit/live-artifact tests, and 10 Chromium interaction/accessibility tests pass. Tests block unexpected network access and cover add-without-search, complete catalog paging, selection replacement, filters, grid/list mode, details tabs, keyboard use, mobile fit, TV navigation, profile persistence, and bounded recovery. | **Yes** — the redesigned UI is deployed and healthy behind the local gateway; authenticated local smoke and route rendering passed. The owner has exercised the live site. | **No**. | **No core parity item** — richer evidence and administration views in `docs/IMPLEMENTATION_STATUS.md` are scoped future enhancements, not missing core workflow truthfulness. |

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
