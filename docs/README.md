# Media Manager documentation

Hub for all project documentation. Source code lives at the repository root;
behavioral truth is always the code and the SQL migrations.

## Start here

| Audience | Path |
| --- | --- |
| New engineer or AI agent | [onboarding/README.md](onboarding/README.md) — overview, read order, hard rules |
| Coding agent session | [onboarding/agent-briefs/claude-code.md](onboarding/agent-briefs/claude-code.md) — paste-ready finish-today execution brief |
| Hard-problem review | [onboarding/agent-briefs/claude-review-hard-parts.md](onboarding/agent-briefs/claude-review-hard-parts.md) — the eight open hard problems with live state, tried approaches, constraints, and acceptance criteria |
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
| [architecture/frontend.md](architecture/frontend.md) | Vinext/Next UI, route→screen map, API client, tests, styling |
| [architecture/workflows.md](architecture/workflows.md) | Domain lifecycle: intake → search → AltMount → verify → publish → Plex → Seerr, plus rounds, upgrades, adoption, recycle |
| [reference/glossary.md](reference/glossary.md) | Exact domain vocabulary |

## Operations

| Document | Contents |
| --- | --- |
| [operations/runbook.md](operations/runbook.md) | Deploy, Cosmos edge, env vars, backup/restore, smoke tests, troubleshooting |

## Status, audits, and evidence

These are dated and may lag `main`. They are not behavioral truth.

| Document | Contents |
| --- | --- |
| [status/implementation.md](status/implementation.md) | Feature-by-feature implementation audit |
| [status/release.md](status/release.md) | Release evidence and milestone closeouts |
| [status/indexer-conformance.md](status/indexer-conformance.md) | Developer-only Newznab provider conformance evidence (not a runtime dependency) |

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
