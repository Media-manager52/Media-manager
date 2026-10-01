# Media Manager — onboarding (start here)

This is the entry point to the documentation set for the Media Manager
codebase. It is written for an engineer or AI agent with **zero prior
context** who must safely read, change, test, and operate the repository.

Source checkpoint: Seerr waiting progress and VPS GitHub workflows
(2026-10-01), database schema 43, Go 1.27, Node 24 / Next 16 (Vinext),
PostgreSQL 18. This includes the earlier bounded NZB retrieval and access-rejection
recovery, IPv4-only API egress, configurable acquisition budgets, and recorded
request search schedules. Existing round snapshots remain immutable.
Deployed source can lag this checkout: the authenticated
`/api/v1/system/info` reports the running commit and schema. See
[release status](../status/release.md) and the exact-commit GitHub Deploy result
for dated evidence.
The agent briefs under
`agent-briefs/` are dated session prompts and may describe an older snapshot.

> Read `AGENTS.md` in the repository root for the short contributor contract,
> then follow the read order below. The full documentation index, including
> status/evidence and archived proposals, is in
> [`docs/README.md`](../README.md). To start a coding-agent session, paste
> [agent-briefs/claude-code.md](agent-briefs/claude-code.md) into it first.
> For the currently open hard problems (policy-change migration, Plex
> re-confirmation recovery, worker starvation, the two publications made without
> Danish subtitles, subtitle evidence strategy, provider failures, Seerr scan
> sync, and adoption), paste
> [agent-briefs/claude-review-hard-parts.md](agent-briefs/claude-review-hard-parts.md).
> To prove whether the remaining "missing" titles are truly unavailable or
> blocked by a pipeline defect, paste
> [agent-briefs/claude-availability-audit.md](agent-briefs/claude-availability-audit.md).
> For a full-server-access review session (Codex/GPT-6-Astra or similar) use
> [agent-briefs/codex-availability-review.md](agent-briefs/codex-availability-review.md).

## Read order

| # | Document | Read it when you need |
| --- | --- | --- |
| 1 | [Architecture overview](../architecture/overview.md) | The big picture: processes, networks, data flow, outbox, state machine, safety invariants. **Read first.** |
| 2 | [Backend reference](../architecture/backend.md) | What every Go package does, key types/functions, where to change what. |
| 3 | [Database reference](../architecture/database.md) | Schema, migrations, table catalog, store/repository layout, concurrency rules. |
| 4 | [API reference](../architecture/api.md) | Every HTTP route, auth model, envelopes, write-path protections. |
| 5 | [Frontend reference](../architecture/frontend.md) | UI architecture, routing, screens, API client, tests, styling. |
| 6 | [Domain workflows](../architecture/workflows.md) | The end-to-end domain lifecycle: intake → search → AltMount → verify → publish → Plex → Seerr, plus rounds, upgrades, adoption, recycle. |
| 7 | [Operations runbook](../operations/runbook.md) | Deploy, env vars, Cosmos edge, backup/restore, health, smoke tests, troubleshooting. |
| 8 | [Development guide](development.md) | Build/test commands, CI, conventions, step-by-step recipes for common changes, pitfalls. |
| 9 | [Glossary](../reference/glossary.md) | Domain vocabulary and exact meanings. |

## The 60-second summary

Media Manager is an **owner-operated movie/TV manager for Usenet**. It owns one
narrow, fully verified pipeline:

```
Seerr or manual request
  → Newznab search (paced, capability-checked, budgeted)
  → evidence-based release selection (policy rules, ranking)
  → one AltMount job (season pack = one job)
  → bounded read-only output inspection + subtitle verification
  → Plex-safe symlink publication (video + verified subtitle companions)
  → exact Plex identity/path confirmation
  → Seerr availability feedback
```

Radarr and Sonarr are **not** runtime dependencies. AltMount output is
**read-only**; Media Manager only creates symlinks into the Plex libraries.
Nothing is claimed successful until an exact Plex confirmation occurs.

Three processes run from one Go binary (`cmd/media-manager`): the HTTP API, the
durable outbox worker, and a small scheduler goroutine (recycle purge planning,
Seerr polling/status sync). The UI is a separate Node/Cloudflare-Worker process
that talks to the API **same-origin** (`/api/*`); Cosmos Cloud is the public
edge and injects the owner bearer token server-side.

## Source-of-truth order

Several documents in the repository describe intent, audits, or release
evidence rather than current behavior. Prefer, in this order:

1. Code under `cmd/` and `internal/` (behavioral truth). Each Go package now
   carries a `doc.go` package comment summarizing its role.
2. `internal/postgres/migrations/*.sql` (data truth).
3. The `docs/onboarding/` + `docs/architecture/` + `docs/operations/` +
   `docs/reference/` set (maps and explanations).
4. `README.md` (product contract and invariants).
5. `docs/status/implementation.md` and `docs/status/release.md` (dated audits;
   may lag behind `main`).
6. `docs/archive/*.txt` (design proposals — **not** implemented behavior).
7. `docs/status/indexer-conformance.md` (developer-only lab evidence, not
   runtime).

If a change contradicts this documentation, fix the documentation in the same
commit. Never treat this set as permission to skip reading the code.

## Hard rules (violating these is a bug)

Expanded in
[Architecture § Safety invariants](../architecture/overview.md#7-safety-invariants)
and [Development § Never do these](development.md#6-never-do-these):

1. AltMount output is read-only. Never write, move, or delete inside it.
2. Only contained, Plex-safe symlinks are published. Never copy/link outside a
   configured root.
3. A season pack stays **one** AltMount job with one immutable expectation set.
4. AltMount completion ≠ import completion ≠ Plex completion.
5. Never weaken Newznab pacing, retry budgets, redirect pinning, credential
   stripping, or secret sanitization.
6. Required output properties (subtitles, episode coverage) fail closed and
   stay owner-visible and undeleted.
7. Only the owner can open another acquisition round; failed outputs are
   retained, never silently retried or deleted.
8. Preserve provider identities, workflow revisions, and selected-release
   protections across asynchronous work.
9. Never add `.env*`, credentials, runtime data, media, or download output to
   git.
10. Frontend fixtures must never be imported into the live UI artifact.

## Document maintenance contract

This tree is intentionally small and predictable:

```
docs/
├── README.md           # documentation hub and index
├── onboarding/         # start here: this file + development guide
├── architecture/       # overview, backend, database, api, frontend, workflows
├── operations/         # runbook
├── reference/          # glossary
├── status/             # dated audits and evidence (implementation, release, conformance)
└── archive/            # historical design proposals (.txt, never runtime truth)
```

When you change behavior:

- Update the matching document in `architecture/`, `operations/`, or
  `reference/`; update `status/` only with dated evidence.
- Keep the root `README.md`, `AGENTS.md`, and `CONTRIBUTING.md` pointers valid.
- Add new design proposals to `archive/` only (they are not behavior).
- Do not paste large code blocks; link to files and describe invariants.
- Run `npm run docs:check` to validate links, and refresh the Snapshot line
  above when the commit or schema changes.
