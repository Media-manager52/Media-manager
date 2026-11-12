# Media Manager documentation

Hub for all project documentation. Source code lives at the repository root;
behavioral truth is always the code and the SQL migrations.

## Start here

| Audience | Path |
| --- | --- |
| Dansk begyndervejledning | [onboarding/kom-i-gang.da.md](onboarding/kom-i-gang.da.md) — installation, forbindelser, første titel og sikker gendannelse |
| New owner installing with Cosmos Cloud | [getting-started.md](getting-started.md) — Cosmos routes, setup script, first integrations, folder contract, updates, backups and troubleshooting |
| New engineer or AI agent | [onboarding/README.md](onboarding/README.md) — overview, read order, hard rules |
| AI agent operating or reviewing the running server | [operations/ai-agents.md](operations/ai-agents.md) — API discovery (`GET /api/v1`, OpenAPI), effects, safe calling, the loopback read-only inspector, and common tasks |
| Contributor workflow | [../CONTRIBUTING.md](../CONTRIBUTING.md) and [../AGENTS.md](../AGENTS.md) |
| Product overview | [../README.md](../README.md) — what it does, how it works, how to install |
| Manual installation | [operations/manual-install.md](operations/manual-install.md) — Compose install, acceptance checks, public edge routing, mount contract, integration settings, Seerr without Radarr/Sonarr, operations |
| Change history | [../CHANGELOG.md](../CHANGELOG.md) |

## Architecture and reference

| Document | Contents |
| --- | --- |
| [architecture/areas.md](architecture/areas.md) | Where to go for a change or a bug: each product area, its code, page, Fejllog area and quick test |
| [architecture/overview.md](architecture/overview.md) | System context, processes, networks, outbox, workflow state machine, data ownership, safety invariants |
| [architecture/backend.md](architecture/backend.md) | Package-by-package Go reference (`cmd/`, `internal/`) |
| [architecture/database.md](architecture/database.md) | Migrations, conventions, table catalog, `*Store` file map, concurrency rules |
| [architecture/api.md](architecture/api.md) | Every HTTP route, auth model, envelopes, write-path protections |
| [reference/openapi.json](reference/openapi.json) | Generated OpenAPI 3.1 description of every route (also served at `GET /api/v1/openapi.json`) |
| [architecture/frontend.md](architecture/frontend.md) | Web UI in `web/`: pages, API client, Danish labels, tests, styling |
| [architecture/acquisition-safety.md](architecture/acquisition-safety.md) | Acquisition and publication safety rules (pacing, verification, rounds, upgrades, adoption) |
| [architecture/workflows.md](architecture/workflows.md) | Domain lifecycle: intake → search → AltMount → verify → publish → Plex → Seerr, plus rounds, upgrades, adoption, recycle |
| [reference/glossary.md](reference/glossary.md) | Exact domain vocabulary |

## Operations

| Document | Contents |
| --- | --- |
| [operations/runbook.md](operations/runbook.md) | Deploy, Cosmos edge, env vars, backup/restore, smoke tests, troubleshooting |
| [operations/github-vps.md](operations/github-vps.md) | VPS-hosted GitHub checks, public GHCR images for the Cosmos market and owner-triggered production updates |
| [operations/oldboys.md](operations/oldboys.md) | Owner credential setup, encryption key backup/recovery, and pending native provider validation |
| [operations/altmount-patches/README.md](operations/altmount-patches/README.md) | Reproducible source patches and regression tests for the deployed companion AltMount fixes |
| [operations/owner-console-rollout.md](operations/owner-console-rollout.md) | Handoff for upgrading the production server to the owner console: pre-flight, backup, deploy, verification, what was and was not tested |
| [operations/install-release.md](operations/install-release.md) | Immutable-image installation, first-run checklist, upgrade and encrypted restore |
| [releases/versioning.md](releases/versioning.md) | Version/tag/image-identity contract, supported architecture, license and rollback rules |
| [releases/v0.1.0-beta.15.md](releases/v0.1.0-beta.15.md) | Fifteenth public beta (2026-11-12): fourteen new tests and whole-number MDBList ids and years. |
| [releases/v0.1.0-beta.14.md](releases/v0.1.0-beta.14.md) | Fourteenth public beta (2026-11-12): ten fixes from a code review of search, indexer pacing and downloading. |
| [releases/v0.1.0-beta.13.md](releases/v0.1.0-beta.13.md) | Thirteenth public beta (2026-11-12): ten fixes from a code review of language checks, publication and recovery. |
| [releases/v0.1.0-beta.12.md](releases/v0.1.0-beta.12.md) | Twelfth public beta (2026-11-12): a followed list no longer fetches titles Plex already has. |
| [releases/v0.1.0-beta.11.md](releases/v0.1.0-beta.11.md) | Eleventh public beta (2026-11-12): following MDBList lists, and Kræver handling resolving itself (automatic AltMount import retries, rounds that wait for new uploads, Plex reconfirmation) plus a queue a hanging Plex cannot stall. |
| [releases/v0.1.0-beta.10.md](releases/v0.1.0-beta.10.md) | Tenth public beta (2026-11-11): a gap-free newest-uploads reader, date search for daily shows, and a search schedule of 30 minutes, 6 hours and a day, then monthly. |
| [releases/v0.1.0-beta.9.md](releases/v0.1.0-beta.9.md) | Ninth public beta (2026-11-11): one search per season, the next release without a new search after a failed download, learned Danish records of release groups, Find manglende and the HolyArrs-style redesign. |
| [releases/v0.1.0-beta.8.md](releases/v0.1.0-beta.8.md) | Eighth public beta (2026-11-10): reading each indexer's newest uploads, larger result pages, the most reliable indexer first and more room for background searches. |
| [releases/v0.1.0-beta.7.md](releases/v0.1.0-beta.7.md) | Seventh public beta (2026-11-10): delete and rename in the settings, moving a profile's titles before deleting it, a tidier title page and faster lists. |
| [releases/v0.1.0-beta.6.md](releases/v0.1.0-beta.6.md) | Sixth public beta (2026-11-10): recently added row, menu search, library sort and folder filter, group actions, indexer download numbers and calendar posters. |
| [releases/v0.1.0-beta.5.md](releases/v0.1.0-beta.5.md) | Fifth public beta (2026-11-09): Fejllog, a usability pass over every page, and two more release-picking fixes. |
| [releases/v0.1.0-beta.4.md](releases/v0.1.0-beta.4.md) | Fourth public beta (2026-11-09): finds more existing releases; IMDb, Danish name, escaped name and date-search fixes. |
| [releases/v0.1.0-beta.3.md](releases/v0.1.0-beta.3.md) | Third public beta (2026-11-09): Plex login, recommended setup, title actions, plain-language status; images and verification |
| [releases/v0.1.0-beta.2.md](releases/v0.1.0-beta.2.md) | Second public beta (2026-11-04): website setup, encrypted keys, Seerr without `.env`, 4K editions; images and verification |
| [releases/v0.1.0-beta.1.md](releases/v0.1.0-beta.1.md) | First public beta (2026-11-04): images, verification, limitations and how each publication gate was closed |

## Status, audits, and evidence

These are dated and may lag `main`. They are not behavioral truth.

| Document | Contents |
| --- | --- |
| [status/implementation.md](status/implementation.md) | Feature-by-feature implementation audit |
| [status/release-v0.1.0-beta.md](status/release-v0.1.0-beta.md) | v0.1.0 Beta readiness ledger and PASS/FAIL/NOT VERIFIED checklist |
| [status/license-decision.md](status/license-decision.md) | AGPL-3.0-only owner decision, compatibility and verification record |
| [status/release.md](status/release.md) | Release evidence and milestone closeouts |
| [status/indexer-conformance.md](status/indexer-conformance.md) | Developer-only Newznab provider conformance evidence (not a runtime dependency) |
| [status/availability-audit.md](status/availability-audit.md) | 2026-09-28 audit of why non-terminal titles are missing, with a per-title truth table |
| [status/retrieval-recovery-review.md](status/retrieval-recovery-review.md) | Seerr request evidence, bounded retrieval recovery, Danish ranking and read-only reproduction SQL |
| [status/playback-verification-2026-09-29.md](status/playback-verification-2026-09-29.md) | Read-only playback benchmark: FUSE and Plex throughput, latency, concurrency, CPU decode, library health |

## Archive

Historical design proposals, kept for traceability. They are **not** implemented
behavior; see [archive/README.md](archive/README.md).

- `archive/indexer-capability-architecture-proposal.txt`
- `archive/indexer-commissioning-v2-architecture-proposal.txt`
- `archive/indexer-commissioning-v2-live-executor-architecture-proposal.txt`

## Documentation rules

- Put onboarding material in `onboarding/`, behavior/reference docs in
  `architecture/`, `operations/`, or `reference/`, dated evidence in `status/`,
  and superseded proposals in `archive/`.
- Every Go package carries a `doc.go` package comment; keep it accurate when the
  package's role changes.
- `node scripts/check-docs.mjs` validates every relative link and anchor in Markdown.
- Prefer linking to files (with relative paths) over pasting code. Keep paths
  relative so the tree can move.
