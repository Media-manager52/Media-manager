# Operating Media Manager through its API (for AI agents)

This guide is for an AI agent (Claude, Codex or another tool) that needs to
inspect or operate a running Media Manager. For changing the code, read
[AGENTS.md](../../AGENTS.md) instead.

The rule of thumb: **use the API, not the database.** Every supported owner
action has a route with the same guards the UI uses (state, revision, pacing,
budgets, audit). Writing SQL bypasses those guards and is not supported.

## Read-only inspector for another AI on this host

For a review that must not change anything, use the loopback-only read-only
bridge instead of the owner token or the MCP server:

```text
http://127.0.0.1:28082
```

It loads the live OpenAPI document and accepts only operations marked
`x-mm-effect: read`; writes and provider-effect GETs return `403`/`405`.
Doubled slashes, encoded separators, dot segments and upstream redirects are
refused. Backup binaries/off-site copies, notification destinations and the
Seerr webhook are excluded. It injects the owner bearer token server-side and
never exposes that token; the token is re-read after a `401` (and cached for at
most 30 seconds), so rotating it does not require a restart. It is loopback-only
and is not exposed through Cosmos or the public origin. Access is limited to
processes/accounts that can reach `127.0.0.1` on this host.

```bash
curl -s http://127.0.0.1:28082/health/ready
curl -s http://127.0.0.1:28082/api/v1/system/info
curl -s http://127.0.0.1:28082/api/v1/system/health
curl -s http://127.0.0.1:28082/api/v1/dashboard
curl -s http://127.0.0.1:28082/api/v1/problems
curl -s http://127.0.0.1:28082/api/v1/openapi.json
```

The service is `media-manager-ai-inspector`; request paths and statuses go to
the journal (no headers, tokens or query strings). Set
`MEDIA_MANAGER_AI_LOG` to also write the same JSON lines to a mode-600 file.

```bash
sudo systemctl status media-manager-ai-inspector
sudo journalctl -u media-manager-ai-inspector -n 50
```

The implementation and unit are `scripts/readonly-api-proxy.mjs` and
`deploy/systemd/media-manager-ai-inspector.service`. It reads the owner token
from the running `media-manager-api-1` container; if the API container is
renamed, set `MEDIA_MANAGER_API_CONTAINER` and restart the service.

### Paste-ready prompt for the reviewing AI

```text
Review the running Media Manager on this host. Use only the read-only
inspector at http://127.0.0.1:28082 and only GET/HEAD requests. Do not read
.env, secret files, provider tokens, or the owner token, and do not print any
secret. Start with /health/ready, /api/v1/system/info, /api/v1/system/health,
/api/v1/dashboard, /api/v1/problems and /api/v1/openapi.json. Read the source
under /home/vps/media-manager and report concrete findings with the endpoint
or file and evidence. Do not change files or call any write endpoint.
```
## 1. Find the API

| Where you run | Base URL | Authentication |
| --- | --- | --- |
| On the server (shell access) | `http://127.0.0.1:28081` (`MEDIA_MANAGER_API_PORT`) | `Authorization: Bearer <OWNER_TOKEN>` |
| Through the public site | `https://<ui-host>/api/...` | Cosmos owner login; the edge adds the bearer header |

On the server, `OWNER_TOKEN` is in the checkout's `.env`. Never print it, log it
or paste it into a chat. Pass it to `curl` without putting it on the command
line:

```bash
cd /home/vps/media-manager
mm() {  # usage: mm GET /api/v1/system/info   |   mm PUT /api/v1/x '{"json":1}'
  local token; token=$(grep '^OWNER_TOKEN=' .env | cut -d= -f2-)
  curl -sS -X "$1" "http://127.0.0.1:28081$2" \
    -H @<(printf 'Authorization: Bearer %s\n' "$token") \
    ${3:+-H 'Content-Type: application/json' --data "$3"}
}
```

### MCP (recommended for Claude, Codex and other MCP clients)

The API binary includes a Model Context Protocol server. It runs inside the
API container and uses that container's own `OWNER_TOKEN`, so no secret is
copied to the host or shown to the AI:

```bash
# Claude Code
claude mcp add media-manager -- docker exec -i media-manager-api-1 media-manager mcp
```

```toml
# Codex (~/.codex/config.toml)
[mcp_servers.media-manager]
command = "docker"
args = ["exec", "-i", "media-manager-api-1", "media-manager", "mcp"]
```

It offers four tools generated from the live OpenAPI document:

- `overview`: version, health, queue and indexer budgets.
- `list_operations`: filter by tag, effect or text.
- `describe_operation`: parameters and full schemas of one operation.
- `call_operation`: performs a call.

`call_operation` refuses destructive operations unless `confirm: true` is
passed after the owner approved. It also refuses browser-only routes and
binary transfers. Every server-side guard still applies, because the MCP
server is only an HTTP client. Set `MEDIA_MANAGER_MCP_URL` to point it at
another API address.

## 2. Discover what exists

1. `GET /api/v1` gives the index: version, conventions, what each effect
   means, operation counts per tag, and good first reads.
2. `GET /api/v1/openapi.json` gives the OpenAPI 3.1 description of every
   route: parameters, request schemas, summary and the two extensions below.
   It is generated from the live route table, so it always matches the
   running build.
3. Without a running server, read the committed copy
   [docs/reference/openapi.json](../reference/openapi.json). A test fails when
   it is stale.

Each operation carries:

- `x-mm-effect`:
  - `read`: no change and no provider contact. Always safe.
  - `write`: changes Media Manager state or queues local work. Audited.
  - `provider`: contacts, or queues requests to, indexers, Seerr, Plex or
    metadata services. Spends provider budget; do not loop it.
  - `destructive`: removes, replaces or purges data. Ask the owner first.
    These need an exact confirmation phrase, given in the operation's
    description.
- `x-mm-auth`:
  - `owner`: bearer token.
  - `owner_browser`: only from the Media Manager settings page, so an agent
    cannot call it.
  - `none`: health probes only.
  - `seerr_webhook`: Seerr only.

## 3. Conventions

- Success bodies are `{"data": ..., "meta": ...}`. Errors are
  `{"error": {"code": "snake_case", "message": "..."}}`. Branch on `code`,
  not on `message`.
- JSON bodies need `Content-Type: application/json`. Unknown fields are
  rejected with `400`.
- `202` means queued, not done. Background work is paced per provider, so
  check the workflow again later instead of re-sending.
- `409` means the action does not apply in the current state. `error.code`
  is stable (for example `round_exhausted`, `retained_output_limit_reached`,
  `altmount_job_active`, `concurrent_modification`) and `error.hint` says what
  to do instead. Read the workflow again rather than retrying.
- Some actions take a `revision` or `expected_revision`. Read it immediately
  before acting; a stale revision is refused by design.

## 4. Workflow actions

`GET /api/v1/workflows/{id}` (and each row of `GET /api/v1/workflows`)
includes `available_actions`. Each entry has:

```json
{ "action": "open_next_round", "operation_id": "openAcquisitionRound",
  "method": "POST", "path": "/api/v1/workflows/<id>/acquisition-rounds",
  "effect": "provider", "available": false,
  "reason": "No newly discovered eligible release is ready yet; paced discovery keeps looking." }
```

- Call only actions with `"available": true`. Send `body` exactly when
  present; it is pre-filled, for example the candidate and revision for an
  access-rejected retry.
- `confirmation` means the owner must approve first. Never send the phrase
  without asking.
- `available` reflects what the read model can see. The server re-checks
  everything, so a `409` with a hint is still possible.

The action names are `search`, `discover`, `open_next_round`,
`open_round_from_known`, `reconcile_altmount`, `reconfirm_plex`, `cancel` and,
when one exists, `retry_access_rejected_release`.

## 5. Common tasks

| Goal | Calls |
| --- | --- |
| Is it healthy? | `GET /health/ready`, `GET /api/v1/system/health`, `GET /api/v1/system/info` |
| What needs attention? | `GET /api/v1/dashboard`, `GET /api/v1/workflows`, `GET /api/v1/requests` |
| Look at one title | `GET /api/v1/media?limit=…` then `GET /api/v1/media/{id}/detail` |
| Look at one workflow | `GET /api/v1/workflows/{id}` |
| Why is a workflow not finding anything? | `GET /api/v1/workflows/{id}/searches` (per-indexer outcome of each run), `GET /api/v1/workflows/{id}/candidates?verdict=held` (decision reasons) |
| Is an indexer out of budget or cooling down? | `GET /api/v1/indexers` (budget, used, remaining, background allowance, pacing, 24h outcomes) |
| Is background work stuck? | `GET /api/v1/system/queue` (due, scheduled, leased, retrying per event type; completions in the last 10 min and hour) |
| Fix a workflow that needs attention | `GET /api/v1/workflows/{id}`, then the `available_actions` entry that is available |
| Search now for one workflow | `POST /api/v1/workflows/{id}/search` (provider) |
| Approve a Seerr request | `POST /api/v1/requests/{id}/approve` (provider) |
| Change an indexer's daily limits or pacing | `PATCH /api/v1/settings/integrations/{id}` `{"query_limit":5000,"grab_limit":100,"minimum_interval_ms":5000}` (audited; also works for OldBoys) |
| Switch off a misbehaving indexer | `PATCH /api/v1/settings/integrations/{id}` `{"enabled":false}` (re-enable with `true` once commissioned) |
| Back up now | `POST /api/v1/backups` |

## 6. Safety rules

- Prefer `read` operations. Ask the owner before any `destructive` operation
  and before bulk `provider` operations (many searches or rounds at once).
- Never weaken guards to "make something work": Danish subtitle requirements,
  retained-output caps, redirect/credential rules, exclusions and revisions
  are deliberate.
- Never print or store secrets: owner token, indexer keys, OldBoys
  credentials or webhook secrets.
- Report outcomes truthfully. "Queued" is not "in Plex"; the workflow state
  `plex_confirmed` is.

## Queue fairness

Lifecycle work (publication, Plex confirmation, Seerr reconciliation and other
non-search events) retains priority. After each claimed indexer event, the worker
alternates preference between acquisition searches and evidence-only discovery
rescans. Deferred attempts also consume a turn, so provider pacing cannot starve
the other class. Each class remains oldest-first; due times, leases, provider
budgets and sequential execution are unchanged.
