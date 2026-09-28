# Archive

This directory holds historical design proposals for Media Manager features.
They document how decisions were made at the time and are kept for
traceability.

**They are not behavioral truth.** Features may have shipped differently, or not
at all. Always verify current behavior against the code (`cmd/`, `internal/`)
and the migrations (`internal/postgres/migrations/`), then read
[../architecture/](../architecture/) for the current map.

| File | Proposal |
| --- | --- |
| `indexer-capability-architecture-proposal.txt` | Indexer capability profiles and dialect planning |
| `indexer-commissioning-v2-architecture-proposal.txt` | Owner-authorized commissioning v2 |
| `indexer-commissioning-v2-live-executor-architecture-proposal.txt` | Commissioning v2 live executor |

Related current documentation:

- [../status/indexer-conformance.md](../status/indexer-conformance.md) — verified
  provider dialects from the developer-only lab.
- [../architecture/workflows.md](../architecture/workflows.md#14-indexer-commissioning) —
  current commissioning behavior.
