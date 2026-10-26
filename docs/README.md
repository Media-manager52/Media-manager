# Media Manager documentation

Hub for all project documentation. Source code lives at the repository root;
behavioral truth is always the code and the SQL migrations.

## Start here

| Audience | Path |
| --- | --- |
| New owner installing on their own server | [getting-started.md](getting-started.md) — standalone setup, Caddy edge, first integrations, folder contract, updates, backups and troubleshooting |
| New engineer or AI agent | [onboarding/README.md](onboarding/README.md) — overview, read order, hard rules |
| AI agent operating the running server | [operations/ai-agents.md](operations/ai-agents.md) — API discovery (`GET /api/v1`, OpenAPI), effects, safe calling, common tasks |
| Coding agent session | [onboarding/agent-briefs/claude-code.md](onboarding/agent-briefs/claude-code.md) — paste-ready finish-today execution brief |
| Hard-problem review | [onboarding/agent-briefs/claude-review-hard-parts.md](onboarding/agent-briefs/claude-review-hard-parts.md) — the eight open hard problems with live state, tried approaches, constraints, and acceptance criteria |
| Availability audit | [onboarding/agent-briefs/claude-availability-audit.md](onboarding/agent-briefs/claude-availability-audit.md) — prove per title whether missing content is truly unavailable or blocked by a pipeline defect |
| Codex/agent review | [onboarding/agent-briefs/codex-availability-review.md](onboarding/agent-briefs/codex-availability-review.md) — full-server-access brief to challenge the audit, improve Danish evidence, and hunt defects |
| Contributor workflow | [../CONTRIBUTING.md](../CONTRIBUTING.md) and [../AGENTS.md](../AGENTS.md) |
| Product contract | [../README.md](../README.md) — deployment, commands, safety invariants |
| Change history | [../CHANGELOG.md](../CHANGELOG.md) |

## Architecture and reference

| Document | Contents |
| --- | --- |
| [architecture/overview.md](architecture/overview.md) | System context, processes, networks, outbox, workflow state machine, data ownership, safety invariants |
| [architecture/backend.md](architecture/backend.md) | Package-by-package Go reference (`cmd/`, `internal/`) |
| [architecture/database.md](architecture/database.md) | Migrations, conventions, table catalog, `*Store` file map, concurrency rules |
| [architecture/api.md](architecture/api.md) | Every HTTP route, auth model, envelopes, write-path protections |
| [reference/openapi.json](reference/openapi.json) | Generated OpenAPI 3.1 description of every route (also served at `GET /api/v1/openapi.json`) |
| [architecture/frontend.md](architecture/frontend.md) | Vinext/Next UI, route→screen map, API client, tests, styling |
| [architecture/workflows.md](architecture/workflows.md) | Domain lifecycle: intake → search → AltMount → verify → publish → Plex → Seerr, plus rounds, upgrades, adoption, recycle |
| [reference/glossary.md](reference/glossary.md) | Exact domain vocabulary |

## Operations

| Document | Contents |
| --- | --- |
| [operations/runbook.md](operations/runbook.md) | Deploy, Cosmos edge, env vars, backup/restore, smoke tests, troubleshooting |
| [operations/github-vps.md](operations/github-vps.md) | VPS-hosted GitHub checks, private image uploads and owner-triggered production updates |
| [operations/oldboys.md](operations/oldboys.md) | Owner credential setup, encryption key backup/recovery, and pending native provider validation |
| [operations/altmount-patches/README.md](operations/altmount-patches/README.md) | Reproducible source patches and regression tests for the deployed companion AltMount fixes |
| [operations/owner-console-rollout.md](operations/owner-console-rollout.md) | Handoff for upgrading the production server to the owner console: pre-flight, backup, deploy, verification, what was and was not tested |

## Status, audits, and evidence

These are dated and may lag `main`. They are not behavioral truth.

| Document | Contents |
| --- | --- |
| [status/implementation.md](status/implementation.md) | Feature-by-feature implementation audit |
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
- `npm run docs:check` validates every relative link and anchor in Markdown.
- Prefer linking to files (with relative paths) over pasting code. Keep paths
  relative so the tree can move.
