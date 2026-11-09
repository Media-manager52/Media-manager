# Layer boundaries

Media Manager is one Go module with a layered design. The layers exist so a
change to one domain does not ripple through the others, and so a rule can be
tested without a database or a provider. `internal/architecture` enforces the
rules below on every test run.

| Layer | Packages | May import | Never imports |
|---|---|---|---|
| Domain | `catalog`, `languages`, `library`, `packs`, `releases`, `workflows` | other domain packages, the standard library | anything else in `internal/` |
| Adapters | `internal/adapters/*` | domain packages, `indexers` (pacing), `subtitles`, other adapters | storage, services, HTTP, platform |
| Storage | `internal/postgres` and its subject packages | domain and policy packages | adapters, services, HTTP |
| Services | `acquisition`, `publication`, `intake`, `adoptionexec`, `commissioning*`, … | domain, storage, adapters | HTTP |
| HTTP | `internal/httpapi` | services, storage, domain | adapters |
| MCP | `internal/mcpserver` | the public HTTP API only | anything in `internal/` |
| Wiring | `cmd/media-manager` | everything | — |

## Storage subjects

A storage subject that stands on its own lives in its own package under
`internal/postgres` (for example `backupstore`), so the compiler keeps it apart
from the others. It may import `dbcore` but no other storage package.
`postgres.Store` embeds it, and `stores.go` keeps its types available as
`postgres.X`, so callers do not change. `TestNewStoreWiresEverySubject` fails if
a subject is embedded without a database pool.

The subjects at the centre (settings, rounds, search, publication) use each
other in the same transactions and stay in `internal/postgres`.

## Known violations

Existing crossings are listed in `internal/architecture/allowlist.go`. The
list can only shrink: adding an entry is not allowed, and a fixed entry that
is still listed fails the test. Most entries are `internal/postgres` importing
provider adapters, because storage code grew to hold domain logic. Moving that
logic into services removes them.

## File size

Production Go files stay at or below 1,000 lines. Files that are already
larger have a ceiling in the same allowlist; they may shrink, never grow.

## Static analysis

CI runs `gofmt`, `go vet`, `staticcheck` (see `staticcheck.conf`) and
`govulncheck`, all pinned. The Go toolchain is pinned to one exact version in
`go.mod`, the Dockerfile and CI (`tests/ci/test_toolchain_pins.py`).

Disabled staticcheck checks: the defaults that are off upstream, and ST1005
(capitalised error strings), because many owner-facing errors start with a
proper noun such as "Plex" or "Seerr".
