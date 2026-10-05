# Operating Media Manager through its API (for AI agents)

This guide is for an AI agent (Claude, Codex or another tool) that needs to
inspect or operate a running Media Manager. For changing the code, read
[AGENTS.md](../../AGENTS.md) instead.

The rule of thumb: **use the API, not the database.** Every supported owner
action has a route with the same guards the UI uses (state, revision, pacing,
budgets, audit). Writing SQL bypasses those guards and is not supported.

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
- `409` means the action does not apply in the current state. The message
  says why, for example "the acquisition round is exhausted; …". Read the
  workflow again rather than retrying.
- Some actions take a `revision` or `expected_revision`. Read it immediately
  before acting; a stale revision is refused by design.

## 4. Common tasks

| Goal | Calls |
| --- | --- |
| Is it healthy? | `GET /health/ready`, `GET /api/v1/system/health`, `GET /api/v1/system/info` |
| What needs attention? | `GET /api/v1/dashboard`, `GET /api/v1/workflows`, `GET /api/v1/requests` |
| Look at one title | `GET /api/v1/media?limit=…` then `GET /api/v1/media/{id}/detail` |
| Look at one workflow | `GET /api/v1/workflows/{id}` |
| Search now for one workflow | `POST /api/v1/workflows/{id}/search` (provider) |
| Continue after AltMount finished | `POST /api/v1/workflows/{id}/reconcile-altmount` |
| Plex confirmation timed out | `POST /api/v1/workflows/{id}/reconfirm-plex` |
| Approve a Seerr request | `POST /api/v1/requests/{id}/approve` (provider) |
| Change OldBoys daily limits | `PUT /api/v1/settings/oldboys/budget` `{"query_limit":5000}` |
| Back up now | `POST /api/v1/backups` |

## 5. Safety rules

- Prefer `read` operations. Ask the owner before any `destructive` operation
  and before bulk `provider` operations (many searches or rounds at once).
- Never weaken guards to "make something work": Danish subtitle requirements,
  retained-output caps, redirect/credential rules, exclusions and revisions
  are deliberate.
- Never print or store secrets: owner token, indexer keys, OldBoys
  credentials or webhook secrets.
- Report outcomes truthfully. "Queued" is not "in Plex"; the workflow state
  `plex_confirmed` is.
