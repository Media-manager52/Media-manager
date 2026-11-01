# Media Manager v1 map — input for v2

**Purpose.** v2 is built as new code with a clean structure, but it must not
lose what v1 already gets right. This map tells a v2 agent, module by module,
what v1 does, where the proven rules live, what is structurally wrong, and
whether each part should be **ported**, **rewritten** or **dropped**.

**Snapshot.** `main` at `34478c4` (2026-10-31) plus the cleanup commit on
branch `cleanup/v1-v2-prep`. Schema 50. Go 1.27, PostgreSQL 18, React 19 /
Next 16 via Vinext. Measured on this snapshot:

| Area | Code lines | Test lines |
| --- | ---: | ---: |
| Go (`cmd/`, `internal/`) | 53,826 | 47,924 |
| of which `internal/postgres` | 20,423 | 21,142 |
| UI (`app/`, `components/`, `lib/`, `hooks/`, `worker/`) | 8,324 | 84 unit + 190 browser tests |

**Evidence rule.** Every statement below was read from source at this
snapshot. Behaviour claims that were *not* run against live services are
marked **UNVERIFIED**. The authoritative v2 requirements are in the planning
package (`MASTER_PLAN.md`, `INVARIANTS.md`, `DOMAIN_MODEL.md`); this map is the
bridge between that package and the real v1 code.

**Verdicts used below**

- **Port** — copy the code into v2, clean it up, keep its tests. The behaviour
  is proven and the code is reasonably shaped.
- **Rewrite** — re-implement in the v2 structure. The v1 tests and rules are
  the specification; the v1 code is the reference, not the template.
- **Redesign** — v2's domain model differs on purpose (Variant / Asset /
  Placement / Generation). Keep the v1 *rules*, not the v1 shape.
- **Drop** — not carried into v2.

---

## 1. What v1 is, in one paragraph

An owner-operated movie/TV manager for Usenet with one narrow, verified
pipeline: Seerr or manual request → paced, capability-checked Newznab search →
pure policy evaluation and ranking → one AltMount job (a season pack is one
job) → bounded read-only output inspection and subtitle verification →
Plex-safe symlink publication → exact Plex path/identity confirmation → Seerr
availability feedback. One Go binary runs the HTTP API, a durable PostgreSQL
outbox worker and a small scheduler. Full detail:
[architecture overview](../architecture/overview.md) and
[domain workflows](../architecture/workflows.md).

## 2. Module inventory and v2 verdict

Planning-package module names: Catalog, Policy, Intake, Acquisition,
Inventory, Publication, Operations, API/UI.

| v1 package | Code / test lines | What it does | v2 module | Verdict | Notes |
| --- | ---: | --- | --- | --- | --- |
| `internal/platform` | 256 / 91 | Outbox worker: leases, heartbeats, backoff, paced deferral, safe error codes | Operations | **Port** | Small and proven. Add the planning package's lease-fence token and multi-event throughput (see §4, P6). |
| `internal/indexers` | 1,131 / 1,787 | Durable per-indexer pacing, rolling query/grab budgets, reservations, cooldowns | Intake | **Port** | Well tested. Satisfies S09. |
| `internal/adapters/newznab` | 4,363 / 4,360 | Newznab client: capability profiles, strict XML, pinned redirects, credential stripping | Intake | **Port** | One of the most valuable parts of v1. Do not rewrite from memory. |
| `internal/adapters/oldboys` | 278 / 157 | Native fixed-origin Danish provider | Intake | **Port** | Not in the planning package; must not be lost. |
| `internal/releases` | 1,322 / 1,086 | Observation normalisation, title-fact inference, pure policy evaluation and ranking | Policy | **Port**, then extend | Pure and testable. v2 adds claim/measurement separation (S05) around it. |
| `internal/packs` | 581 / 251 | Frozen season-pack expectations, exact episode coverage | Catalog / Inventory | **Port** | Satisfies S11. |
| `internal/subtitles` | 394 / 163 | Required-subtitle verification: embedded tracks + strict sidecars, fail-closed | Inventory | **Port** | Some rules live in `postgres/subtitles.go` (`mergeRequiredSubtitles`); move them here. |
| `internal/mediainfo` | 540 / 227 | Bounded ffprobe evidence | Inventory | **Port** | v2 must label tags as *declared*, not verified (S05, R03). |
| `internal/languages` | 27 / 15 | ISO-639 canonicalisation | Catalog | **Port** | |
| `internal/library` | 899 / 701 | The only package allowed to touch the filesystem; containment, link-target and fingerprint checks | Inventory | **Port** | Core of S10. Extend with the X4 filesystem matrix before relying on it for deletion. |
| `internal/adapters/altmount` | 852 / 173 | Queue, submit, output enumeration, `ErrMountUnavailable`, corrupted-file list | Acquisition / Inventory | **Port**, then extend | Mount-outage handling already exists (S14 partly). Masking and offline/missing/unknown separation are not modelled. Thin tests. |
| `internal/adapters/plex` | 660 / 455 | Library refresh and exact identity/path confirmation | Publication | **Port** | Satisfies S15. |
| `internal/adapters/seerr` + `internal/intake` | 1,024 / 459 | Seerr client, webhook auth, intake dedup, availability feedback | Intake | **Port** | |
| `internal/servarr` | 218 / 34 | Radarr/Sonarr-compatible API so Seerr can use Media Manager directly | Intake / API | **Port** | Not in the planning package; it is what removes Radarr/Sonarr. Thin tests. |
| `internal/adapters/metadataproviders` + `internal/metadata` | 1,421 / 236 | TMDB, TheTVDB and Simkl with exact IDs and provenance | Catalog | **Port** | Thin tests for 1,400 lines. |
| `internal/catalog` | 468 / 68 | Media items, external identities, metadata with provenance | Catalog | **Rewrite** | Correct ideas, too thin. v2 needs `Title`/`ExternalIdentity`/`Episode`/`Edition` with conflict review. |
| `internal/workflows` | 230 / 112 | Workflow state machine per media item and scope | Policy / Acquisition | **Redesign** | v2 replaces "workflow per scope" with Variant + FulfillmentScope + Want. Keep the transition-table idea and the revision check. |
| `internal/acquisition` | 4,557 / 6,209 | Search, select, retrieve, AltMount reconcile, output verification | Acquisition | **Rewrite** | Heavily tested: the tests are the specification. Logic is spread between here and `postgres`. |
| `internal/publication` | 580 / 287 | Symlink publication, Plex-gated generation switch, displaced-link removal | Publication | **Redesign** | Rules are right (S02, S15). v2 needs the Generation / PublicationConfirmation / CurrentServerVisibility split (R06, R07). |
| `internal/recycle` | 103 / **0** | Recycle move, restore, purge | Inventory | **Rewrite** | Deletion path with **no dedicated tests** (covered only indirectly). v2 must write the S03 test matrix first. |
| `internal/adoption` + `internal/adoptionexec` | 713 / 269 | Read-only library scan; journaled, reversible adoption with Plex confirmation | Inventory | **Port** | Satisfies S16. |
| `internal/backups` | 885 / 297 | Encrypted, checksummed PostgreSQL backups with restore verification | Operations | **Port** | Satisfies most of S18. |
| `internal/notifications` | 93 / 67 | Owner webhooks with durable retry | Operations | **Port** | |
| `internal/mcpserver` | 513 / 218 | MCP server generated from the live OpenAPI, for AI clients | API | **Port** | Useful for v2 agents to inspect a running system read-only. |
| `internal/importlists` | 116 / 23 | IMDb CSV → review-only snapshots | Intake | **Port** | |
| `internal/prowlarrbackup` | 460 / 82 | Prowlarr backup ZIP → disabled Newznab candidates | Intake | **Port** (low priority) | One-time migration tool. |
| `internal/oldboyscredentials` | 141 / 104 | AES-GCM encrypted provider credentials | Operations | **Port**, generalise | v2 should have one secret store for all integrations. |
| `internal/commissioning` + `internal/commissioningv2` | 5,372 / 5,212 | Indexer commissioning: v1 (CAPS + negative search) and v2 (pure domain + live executor), bridged | Intake | **Rewrite** as one | Two generations coexist and call each other (`commissioning/live_v2.go`). v2 keeps one model; the v2 tests are the spec. |
| `internal/postgres` | 20,423 / 21,142 | All SQL, migrations and 888 store functions, many of them containing business rules | all | **Rewrite** | See §4, P1. Migrations and integration tests are the most important specification in the repository. |
| `internal/httpapi` | 4,743 / 3,512 | Route catalog, owner auth, handlers; `server.go` is 2,245 lines | API | **Rewrite** | Keep `api_catalog.go` as the single route table and the generated OpenAPI. |
| `cmd/media-manager` | 463 / 127 | Wiring, config validation, worker handler map | Operations | **Rewrite** | |
| UI (`app/`, `components/`, `lib/`) | 8,324 | Owner console on Next/Vinext targeting Cloudflare Workers, run on Node through a loader shim; `app/v2` preview inside v1 | API/UI | **Rewrite** | Port the pure helpers in `lib/` (`media-library.ts`, `release-evidence.ts`, `workflow-presentation.ts`, …) and their tests. Drop the hosting template. |

## 3. Rules v1 already enforces (map to `INVARIANTS.md`)

Where to look before re-implementing each invariant. "Partly" means part of the
invariant exists; v2 completes it.

| Invariant | v1 status | Where |
| --- | --- | --- |
| S01 no loss of working media | Yes | `publication`: old generation removed only after Plex confirms the replacement (`library.displaced_remove_requested`). |
| S02 no switch before server confirmation | Yes, no waivers | `publication`, `postgres/publication.go` (`ConfirmPlexScan`). v2 adds audited waivers (R07). |
| S03 recycle/retention before deletion | Yes, **untested package** | `recycle`, `postgres/recycle.go`; hourly `PlanDueRecyclePurges`; adopted real files never auto-deleted. |
| S04 hard requirements not offset by score | Yes | `releases` (pure evaluation), round snapshots, `quality_policy_changed` parking. |
| S05 claims vs measurements vs availability | Partly | Observations are immutable and facts carry `state`; ffprobe tags are not separated from measured truth. |
| S06 selection evidence immutable | Yes | Immutable observations, semantic observation reuse (`search_semantic_reuse_integration_test.go`). |
| S07 intent before side effect, reconcile ambiguity | Yes | `release.retrieve_requested`: SHA-256 package identity, submission intent, ambiguous AltMount upload reconciliation. |
| S08 idempotent jobs, stale-worker fencing | Partly | Outbox leases + aggregate revision checks drop stale events. No lease-fence token on external effects. |
| S09 persistent provider budgets | Yes | `indexers` scheduler; pacing deferral does not consume retry budget. |
| S10 root-confined paths | Yes | `library` containment, link-target and fingerprint checks. Symlink-swap races not proven (X4). |
| S11 season pack proven per episode | Yes | `packs`, `season_pack_integration_test.go`. |
| S12 upgrade no-regression | Partly | Upgrade baseline fails closed on policy change; no full per-dimension no-regression contract. |
| S13 bounded virtual probing | Yes | Bounded output inspection. No byte budget accounting (X1). |
| S14 offline mount ≠ missing | Partly | `altmount.ErrMountUnavailable` waits instead of failing. Masking, NNTP 430/423 vs timeout not modelled. |
| S15 scan ≠ confirmation | Yes | `plex.confirm_requested`, localized-title confirmation, reconfirmation flow. |
| S16 adoption read-only by default | Yes | `adoption` scan is read-only; `adoptionexec` is journaled and reversible. |
| S17 sidecar repair preserves original | Not applicable | No subtitle repair in v1. |
| S18 backup/restore exercised | Yes | `backups` restores into an isolated database to verify. |

## 4. Structural problems v2 must not repeat

| # | Problem | Evidence | v2 rule |
| --- | --- | --- | --- |
| P1 | **The database layer is the domain.** 38 % of all Go code and 888 functions live in `internal/postgres`; business decisions sit inside transactions. | `postgres/publication.go` decides playable assets and Plex confirmation; `postgres/subtitles.go` merges subtitle requirements. | Domain rules are pure functions in domain packages. The store only loads and saves. A store method may not contain a policy decision. |
| P2 | **Domain packages are thin.** `workflows` 230 lines, `catalog` 468 lines with 68 test lines. | Package sizes in §2. | Each planning-package module owns its types, rules and tests. |
| P3 | **Two generations of one feature coexist.** Commissioning v1 and v2 call each other. | `commissioning/live_v2.go`, 5,372 lines across both. | When a feature is replaced, the old one is deleted in the same task. |
| P4 | **The UI sits on a hosted-site template.** Next 16 via Vinext with a Cloudflare Worker build target, run on Node through `register-cloudflare-loader.mjs`; a second UI (`app/v2`) lives inside v1. | `vite.config.ts`, `worker/index.ts`, `Dockerfile` UI stage. | A plain Vite + React + TypeScript SPA, served as static files by the Go binary (planning `ARCHITECTURE.md`). No Cloudflare, no Next server runtime. |
| P5 | **Oversized files.** `httpapi/server.go` 2,245 lines; several store files over 1,000 lines. | §2. | Soft limit of ~400 lines per file; one handler group per file. |
| P6 | **One event per worker claim** starved Plex confirmations behind a search backlog; a priority-tier fix was added later. | `platform/worker.go`; hard-parts brief, problem 3. | Design throughput and priority lanes from the start. |
| P7 | **Risky code with thin tests.** Recycle (deletion) has no dedicated tests; AltMount adapter 852/173; metadata 1,421/236; `servarr` 218/34. | §2. | Safety-critical modules are written test-first. The S01/S03/S14 tests are locked (see the AI setup). |
| P8 | **Documentation sprawl.** A 53 KB changelog, dated status/evidence documents and dated agent briefs that describe older snapshots. | `CHANGELOG.md`, `docs/status/`, `docs/onboarding/agent-briefs/`. | One current design document per module, kept in sync in the same commit as the code; history lives in git, not in docs. |
| P9 | **GitHub-centred process.** `AGENTS.md` spends most of its length on GitHub Actions waiting rules. | `AGENTS.md`. | Local inner loop: local git, local test gate, local review. GitHub only for backups and releases. |
| P10 | **Workflow-per-scope model** cannot express several variants of one title, shared assets or per-server publication. | `workflows/scope.go`. | Variant / FulfillmentScope / Want / Asset / Placement / Generation (planning `DOMAIN_MODEL.md`). |

## 5. Features v1 has that the planning package does not mention

These must be listed in the v2 feature register so they are not silently lost:

1. Native **OldBoys** provider (Danish-original films/TV) with encrypted credentials and owner validation controls.
2. **Radarr/Sonarr-compatible API** (`servarr`) so Seerr talks to Media Manager directly.
3. **MCP server** generated from the live OpenAPI.
4. **IMDb CSV import lists** (review-only).
5. **Prowlarr backup import** into disabled indexer candidates.
6. **Encrypted, restore-verified backups** triggered as durable jobs.
7. **Owner webhook notifications.**
8. **Metadata from TMDB, TheTVDB and Simkl** with provenance.
9. **Airing-episode scope** (`season:NN:episode:NN`) and calendar.
10. **Dry-run** of search/selection (`acquisition/dryrun`).
11. **Owner recovery tools**: apply-policy, Plex reconfirmation (single and bulk), held rescore, owner cancel and discovery.
12. **Cosmos Cloud market app** and install templates.

## 6. Regression scenarios to extract as v2 golden tests

Each line is behaviour v1 proves today, with the v1 test to read first. v2
must pass an equivalent test before the matching module is declared done.

| Scenario | Read first |
| --- | --- |
| Partial search retries only incomplete indexers; rollback and migration safety | `postgres/search_partial_retry_*_test.go` |
| Semantic observation reuse keeps original evidence timestamp | `postgres/search_semantic_reuse_integration_test.go` |
| Held candidates are rescored, not lost | `postgres/search_held_rescore_integration_test.go` |
| Policy change parks rounds (`quality_policy_changed`) until applied | `postgres/policy_application_integration_test.go` |
| Concurrent profile edit is refused with `409` | `postgres/quality_profile_concurrency_integration_test.go` |
| Season pack must map every frozen episode exactly once | `postgres/season_pack_integration_test.go`, `packs/*_test.go` |
| Episode numbering and season episodes | `postgres/episode_numbering_integration_test.go`, `season_episodes_integration_test.go` |
| Required Danish subtitles; verified empty set = missing language | `postgres/subtitles_integration_test.go`, `known_subtitle_failure_integration_test.go` |
| Exact Plex confirmation, timeout and reconfirmation | `postgres/plex_publication_integration_test.go`, `plex_reconfirmation_integration_test.go`, `plex_bulk_reconfirmation_integration_test.go` |
| Outbox priority: lifecycle before search before discovery | `postgres/outbox_priority_integration_test.go` |
| Bounded NZB retrieval and access-rejection recovery | `postgres/retrieval_recovery_*_test.go`, `access_recovery_integration_test.go` |
| Failure, held and owner recovery paths | `postgres/failure_recovery_integration_test.go`, `held_recovery_integration_test.go`, `owner_recovery_integration_test.go` |
| Seerr intake, routing, revocation, status feedback | `postgres/seerr_*_test.go` |
| Indexer pacing, budgets, reservations | `indexers/*_test.go` |
| Newznab parsing, redirects, credential stripping | `adapters/newznab/*_test.go` |
| Path containment and publication links | `library/paths_test.go`, `library/publication_test.go` |
| Dry-run has no side effects | `acquisition/dryrun_test.go`, `postgres/dryrun_integration_test.go` |
| **Missing in v1, must be written for v2:** recycle move/restore/purge guards; AltMount masked/offline/430/timeout; crash injection matrix (planning `ACCEPTANCE_TESTS.md`) | — |

## 7. Open problems recorded in v1

From [`agent-briefs/claude-review-hard-parts.md`](../onboarding/agent-briefs/claude-review-hard-parts.md)
(dated 2026-09-28; current status **UNVERIFIED**). They become v2 requirements:

1. No supported policy-change migration for acquisition rounds (implemented on `main`, deployment status unknown).
2. Retry of a timed-out Plex confirmation with verified links (implemented, deployment unknown).
3. Worker starvation (priority tiers added; throughput still one event per claim).
4. Safe replacement of publications made without the required Danish subtitles.
5. Required-subtitle evidence strategy wastes round slots.
6. Provider-specific retrieval failures.
7. Seerr library sync deliberately disabled.
8. Unmanaged Plex files needing adoption.
9. Recovering a failed Seerr request is manual.

## 8. Drop in v2

- The hosted-site UI template: Vinext/Next server runtime, Cloudflare Worker
  entry, `wrangler`, `.openai/hosting.json`, `build/sites-vite-plugin.ts`,
  `scripts/sites-env.sh`, `register-cloudflare-loader.mjs`.
- The in-repo UI preview `app/v2`.
- Commissioning v1 once the single commissioning model passes the v2 tests.
- GitHub-Actions-centred agent rules; the dated agent briefs (replaced by the v2 agent setup).
- Dated status/evidence documents as a source of truth (keep them in v1 as history).

## 9. How a v2 agent uses this map

1. Read the planning package first, then this map.
2. For the module you are building, open the v1 package in §2 and the tests in §6.
3. **Port**: copy, restructure, keep the tests green. **Rewrite**: write the v2
   tests from the v1 tests first, then implement. **Redesign**: write down the
   v1 rule you are preserving and the v2 shape you are changing it to, in the
   task's decision record.
4. Never weaken a rule listed in §3. If v2 needs a rule to change, stop and ask the owner.
