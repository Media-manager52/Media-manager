# Media Manager — review brief for hard problems

**Audience:** a fresh reviewer session (Claude Code or engineer) with full repo
access at `/home/vps/media-manager`. **Purpose:** review and, where approved,
fix the hard problems listed below. This document is precise about current live
state so you do not have to rediscover it, and honest about what was already
tried.

## Implementation status (2026-09-28 evening)

Problems **1–4** have implementations on `main` — `ac870c0` (apply-policy),
`d90e2b9` (reconfirm-plex), `f2bd76d` (worker priority + Plex window),
`c47e958` (localized-title Plex confirmation), `590c889` (verified empty
subtitle set = missing language) — but they are **not deployed**; the running
API is the previous binary. The full race integration suite was re-run
independently on a disposable PostgreSQL and is green, live inspection shows
**no in-flight AltMount job on a relaxed round snapshot** and **no no-subtitle
verification since the policy was restored**, and 35 workflows with stale
relaxed round snapshots now park safely (`quality_policy_changed`) until the
owner applies the policy. Problems **5–8** are untouched. Deploy, the runbook
§9a replacements, and the post-deploy `reconfirm-plex`/`apply-policy` calls all
need owner approval.

Resolved before deploy: output verification now enforces the **union** of the
attempt's round snapshot and the profile's current required languages
(`mergeRequiredSubtitles` in `internal/postgres/subtitles.go`), so a later
tightening also applies to a job already in flight while quality rules stay
attempt-scoped. A relaxed snapshot can no longer bypass the live requirement.

Read `AGENTS.md`, then this file top-to-bottom before touching anything. For
system context use `docs/onboarding/README.md` and the
`docs/architecture/*.md` set. **Never print or commit secrets** (`.env`,
`data/provider-secrets/`, tokens). **Do not perform live migrations, deploys, or
state-changing operations without explicit owner approval.**

## 0. Current state (2026-09-28 snapshot — re-check with the probes below)

- Repo: `main` at `d63eba6` (clean tree). Commits: `c6bf1b8` (core release),
  `47f74f9`, `c723292`, `7d47b7e` (msgnews fix), `d63eba6`.
- Schema 40 (`internal/postgres/migrate.go`, `CurrentSchemaVersion = 40`).
  Live database is at schema 40.
- Live services: `media-manager-api-1` (loopback `127.0.0.1:28081`, owner token
  in `.env`), `media-manager-ui-1`, `media-manager-postgres-1`, plus `seerr`,
  `altmount` (read-only by policy), `plex`.
- Quality profiles (all version 3):
  - `Movie/TV Best Available Danish Subs` — `required_subtitle_languages:
    ["da"]`, default for foreign content.
  - `Movie/TV Best Available Danish Original` — no required subtitles,
    `original_language_routing: ["da"]`.
- Seerr is configured with Radarr/Sonarr entries pointing at Media Manager
  (`/radarr`, `/sonarr`, `SERVARR_API_KEY`) with **`syncEnabled=false`** on
  purpose (see problem 7). Seerr request status was ~309 available / ~96 open
  at last count.
- Non-terminal workflows: 89 `waiting`, 24 `held`, 19 `candidate_selected`,
  26 `needs_attention`, 2 `plex_scanning`, 1 `searching` (≈161 total).
- Outbox backlog: ~211 pending `workflow.search_requested`, ~330 pending events
  total. The single-event worker drains these slowly (problem 3).

### You have the same access the author had — verify, do not trust

This brief was written from the same server you are on. Treat the snapshot
above as a claim to re-check, not as ground truth: services restart, workflows
advance, and the backlog drains between sessions. Reproduce the state read-only
first, then work from what you actually observe.

Repo, build, and services:

```sh
cd /home/vps/media-manager
git log --oneline -6 && git status --short
docker compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc "SELECT max(version) FROM schema_migrations;"'
docker ps --format '{{.Names}}\t{{.Status}}' | grep -E 'media-manager|seerr|altmount|plex'
```

Profiles, workflow states, outbox backlog, and the case-specific workflows:

```sh
docker compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -P pager=off' <<'SQL'
SELECT name, policy_version, policy->>'required_subtitle_languages' AS required
FROM quality_profiles ORDER BY name;

SELECT state, count(*) FROM workflows WHERE terminal_at IS NULL GROUP BY 1 ORDER BY 2 DESC;

SELECT event_type, count(*) FROM outbox_events WHERE completed_at IS NULL GROUP BY 1 ORDER BY 2 DESC;

-- Resident Evil (no-da, confirmed), Dexter S01 (no-da, confirmed),
-- and the two Plex-confirmation retries.
SELECT left(m.title,26) AS title, w.variant, w.state, COALESCE(w.attention_code,'-') AS code
FROM workflows w JOIN media_items m ON m.id=w.media_id
WHERE w.id IN ('01a0e878-cd19-78f6-bc2b-5df3eb4ebfdf',
               '01a05eb8-3888-7b2a-abdc-4e959a672a8b',
               '01a0e879-4261-729b-bf14-087bd669f203',
               '01a0e87d-5527-730b-addd-3de2b3371f47');

SELECT scan.state, scan.poll_attempts, scan.deadline_at, scan.workflow_id
FROM plex_scan_requests scan WHERE scan.created_at > now() - interval '4 hours'
ORDER BY scan.created_at DESC LIMIT 6;
SQL
```

Seerr request state (the key stays inside the container; the probe prints only
counts):

```sh
docker exec -i -e KEY="$(grep -E '^SEERR_API_KEY=' .env | cut -d= -f2-)" seerr node - <<'EOF'
(async () => {
  const base = "http://127.0.0.1:5055";
  const dist = {};
  let page = 0, pages = 1;
  while (page < pages) {
    const res = await fetch(`${base}/api/v1/request?take=100&skip=${page*100}&sort=added`, {headers: {"X-Api-Key": process.env.KEY}});
    const data = await res.json();
    pages = data.pageInfo ? data.pageInfo.pages : 1;
    for (const r of data.results || []) {
      const key = `request=${r.status} media=${(r.media||{}).status}`;
      dist[key] = (dist[key] || 0) + 1;
    }
    page++;
  }
  console.log(JSON.stringify(dist));
})();
EOF
```

Owner API liveness and health (the token is read into a variable and never
printed):

```sh
TOKEN="$(grep -E '^OWNER_TOKEN=' .env | cut -d= -f2-)"
PORT="${MEDIA_MANAGER_API_PORT:-28081}"
curl -fsS "http://127.0.0.1:$PORT/health/ready" | head -c 400; echo
curl -fsS -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:$PORT/api/v1/system/health" | head -c 600; echo
```

Provider redirect probes must use the manual-redirect pattern from this
session: never follow a redirect with a credential automatically, build the URL
inside the container, and `| sed "s/$KEY/REDACTED/g"` before printing anything.
`internal/adapters/newznab/client.go` `doWithManualRedirects` documents exactly
what is validated.

Keep mutations for after you have written down what you observed, and ask the
owner when the problem requires opening rounds, deleting links, changing
profiles, or any live operation.

### What happened in this session (needed context)

1. Servarr-compatible API, Seerr root routing (schema 39), a 118-request
   intake backfill, and release package screening/auto-recovery (schema 40)
   were implemented, deployed, and committed.
2. A Danish-subtitle policy experiment changed the two `Danish Subs` profiles
   to `required: []` / `preferred: ["da"]`, migrated open acquisition rounds
   with raw SQL (immutable snapshots forced retirement/reopening), then was
   **reverted at owner direction** the same day. Profiles and rounds are back
   to the required-da policy. During the ~5-minute relaxed window two English
   titles were verified and published without Danish subtitles:
   - `Resident Evil` (2026 movie) — media `01a0e878-cd13-7032-a5d1-8c0bfde265a7`,
     workflow `01a0e878-cd19-78f6-bc2b-5df3eb4ebfdf`, `plex_confirmed`, current
     link, observed languages empty.
   - `Dexter` S01 — media `01a05eb8-3885-7f8c-8f19-dd8896a789dc`, workflow
     `01a05eb8-3888-7b2a-abdc-4e959a672a8b`, `plex_confirmed`, 12 verified
     episodes, observed languages `eng, ita`.
   Everything else verified since then carries `required=["da"]`.

## Hard safety rules (never violate)

1. AltMount output is **read-only**; never write/move/delete inside it.
2. Publish only contained Plex-safe symlinks through `internal/library`; never
   raw `os.Rename`/`os.Symlink`.
3. A season pack is **one** AltMount job with one immutable expectation.
4. AltMount completion ≠ import completion ≠ Plex completion. Nothing succeeds
   without exact Plex identity + path confirmation.
5. Never weaken Newznab pacing, retry budgets, redirect pinning, TLS checks,
   credential stripping, or secret sanitization.
6. Failed outputs stay retained, owner-visible, undeleted.
7. Only the owner opens another acquisition round; never fake recovery.
8. Never add `.env*`, credentials, runtime data, media, or download output to
   git. Frontend fixtures must never enter the live artifact.

## Problem 1 — No supported policy-change migration for acquisition rounds (highest priority)

**Why it is hard.** `acquisition_rounds` snapshots (`policy_version`,
`policy_snapshot`, `upgrade_baseline_link_versions`, attempt limit) are protected
by the trigger `enforce_acquisition_round_immutable_snapshot()`. `PATCH
/api/v1/settings/quality-profiles/{id}` (`internal/httpapi/server.go`
`updateQualityProfile` → `internal/postgres/settings.go` `UpdateQualityProfile`)
bumps `policy_version` but does nothing to open rounds. Any subsequent search on
a workflow with an open round fails the check in `internal/postgres/search.go`
(~line 380):

```go
if search.OpenRound.PolicyVersion != search.PolicyVersion || search.OpenRound.UpgradeBaseline != search.CurrentLinkVersions {
    return errors.New("acquisition round snapshot changed before candidate selection; owner review is required")
}
```

This session worked around it with raw SQL twice (retire open round →
`state='exhausted'` + `close_reason`, insert a new round with the new snapshot,
reset `novelty_cutoff_sequence`, queue searches). That worked but is unsupported,
un-audited in the product, and easy to get wrong (the first attempt set the new
round's cutoff to `max(first_seen_sequence)`, which silently filtered **every**
existing candidate because of the novelty check at `search.go` ~line 383; the
fix was cutoff `0` for the migration round).

**What a reviewer should decide/build.**
- A first-class owner flow, e.g. `Store.ApplyQualityProfilePolicyChange` +
  `POST /api/v1/settings/quality-profiles/{id}/apply-policy`, that for every
  non-terminal workflow on the profile: retires the open round, opens a new
  round with the new snapshot, chooses the correct `novelty_cutoff_sequence`
  (document the semantics!), transitions pre-retrieval states to `searching`,
  and queues `workflow.search_requested` — all in one transaction with
  `workflow_transitions` + `audit_events` rows.
- Decide whether a policy change should also re-evaluate previously
  *excluded*/*attempted* releases. Currently `workflow_attempted_releases` and
  `workflow_release_exclusions` are permanent for the workflow; a stricter
  policy change shouldn't revive them, but a looser one arguably should.
- Consider surfacing the mismatch as an attention code instead of a plain
  retried error; today a mismatched search event fails and retries forever.

**Acceptance:** changing a profile's policy through the owner API leaves every
non-terminal workflow searchable under the new policy without manual SQL; an
integration test covers open rounds in `waiting`, `held`, `searching`, and an
active `candidate_selected` attempt.

## Problem 2 — No supported retry for a timed-out Plex confirmation with verified links

**Why it is hard.** When Plex does not confirm inside the deadline,
`internal/postgres/publication.go` `TimeoutPlexScan` sets the scan to
`timed_out` and the workflow to `needs_attention` (`plex_timeout`). The links
are already `verified` and the symlinks exist; only the confirmation is
missing. `CompletePublication` (the only producer of `plex.scan_requested`)
requires `workflow.state='publishing'`, so it cannot be re-run from
`needs_attention`. There is no owner endpoint for this case. This session
recovered two workflows (`Unraveled`
`01a0e879-4261-729b-bf14-087bd669f203`, `Rumble in the Bronx`
`01a0e87d-5527-730b-addd-3de2b3371f47`) with raw SQL: insert a new
`plex_scan_requests` row (state `planned`, deadline +20 min), set the workflow
back to `plex_scanning`, insert a transition, queue `plex.scan_requested`. Both
are still `plex_scanning` because the worker backlog delayed their polling, and
their deadlines were extended manually.

**What a reviewer should decide/build.**
- A store method + owner endpoint, e.g. `POST
  /api/v1/workflows/{id}/reconfirm-plex`, that recreates the scan request from
  the existing verified `library_link_versions` and is idempotent.
- Retry-limit/pacing policy: how many reconfirmations, minimum interval,
  whether to escalate to `publication_failed`.
- Surface the real reason a confirmation stalled (Plex API timeout vs library
  refresh not picked up vs path mismatch) in the attention detail.

**Acceptance:** a `plex_timeout` workflow can be retried from the owner API
without SQL; tests cover the idempotent path and the "links are not verified"
rejection.

## Problem 3 — Single-event worker starves reconciles and Plex confirmations

**Why it is hard.** `internal/platform/worker.go` claims **one** outbox event
per iteration (`ClaimOutbox(..., 1, ...)`), ordered by
`occurred_at` in `internal/postgres/outbox.go`. `workflow.search_requested`
events run indexer queries with per-indexer single-flight and minimum-interval
pacing, so each search can occupy the worker for tens of seconds. With ~200
queued searches, `altmount.reconcile_requested`, `plex.confirm_requested`, and
`library.publish_requested` wait behind them for long periods — which is what
stalled the Plex confirmations above and delays imports.

**What a reviewer should decide/build.**
- Fair scheduling by event class (for example a small per-class concurrency
  with the existing per-indexer single-flight preserved), or claim N events
  and run only the safe ones concurrently; never run two searches for the same
  workflow or two events that race the same aggregate.
- The safety model (round revisions, optimistic concurrency) may already make
  per-aggregate serialization sufficient; prove it with tests before changing
  concurrency.
- Observability: the `/system/tasks` page already shows pending/due per type —
  consider a starvation metric.

**Acceptance:** no design change lands without a written concurrency argument
and a race test; Plex confirmations and AltMount reconciles must not be
starved by a large search queue.

## Problem 4 — Safely replace the two publications made without Danish subtitles

**Why it is hard.** Both are `plex_confirmed` with current links; the system
deliberately blocks overlapping same-scope requests, and the sanctioned removal
path is the recycle/upgrade lifecycle, not deletion. The upgrade path exists
("search rejects the exact current durable release and requires another
candidate to score strictly higher under the target policy, unless the current
release fails a newly required property" — see `docs/status/release.md` and
`internal/postgres/submission.go`/`publication.go`), but it is unproven for the
case "current release fails the *newly required subtitle property*" and for a
series season whose workflow is already terminal.

**What a reviewer should decide/build.**
- The exact owner sequence to create an upgrade workflow for `Resident Evil`
  and `Dexter` S01 under the current required-da profile, using
  `POST /api/v1/media/{id}/workflows` (`CreateManualWorkflowForExisting`) or the
  correct alternative. Determine whether the current release is correctly
  rejected as policy-failing so a replacement can be selected.
- Confirm the old no-da link is displaced only after the replacement's Plex
  confirmation, and that `Dexter` S01's 12 episodes are handled as one exact
  season replacement (not 12 independent upgrades).
- If a da-subtitled release does not exist, the truthful outcome is "waiting",
  and the existing link stays current — decide whether the owner should be able
  to mark such a link for removal into recycle.

**Acceptance:** both titles are replaced by da-subtitled releases (or are
truthfully waiting for one) with the old link displaced only after exact Plex
confirmation, and the replacement path is documented in the runbook.

## Problem 5 — Required-subtitle evidence strategy wastes round slots

**Why it is hard.** `internal/acquisition/search.go` `appendSubtitleRules`
adds a **required** rule for each required language with `Unknown: allow`, plus
a preferred evidence rule. Indexers rarely declare subtitle languages, so
unknown releases are downloaded, inspected, found to lack `da`, excluded, and
after two attempts the round is exhausted (33 movie workflows and several
series seasons are in this state). This is truthful but expensive and slow.

**Questions for the reviewer.**
- Can the selector use stronger pre-download evidence (indexer attributes,
  title markers, group history) without weakening the requirement or spending
  the two slots on hopeless candidates?
- Should a failed subtitle verification consume a release-attributable slot,
  or should it be treated differently from an archive/retrieval failure?
- Is there a safe way to prefer releases from Danish/Nordic groups
  (`NORDiC`, `DANiSH`, `DKSubs` markers) as tie-breakers? Note
  `internal/releases/title.go` deliberately keeps broad labels unknown.

**Acceptance:** a documented policy change plus tests demonstrating better
subtitle-hit rate without accepting releases that fail verification.

## Problem 6 — Remaining provider-specific retrieval failures

**Context.** The msgnews 308 redirect (`msgnews.live` → `www.msgnews.live`
carrying the API key in the path) was fixed by requesting the canonical host
directly (`internal/adapters/newznab/retrieval.go`, `canonicalDownloadHosts`),
keeping the key on the same registrable site. Remaining known failures:
- `treasure-maps`: `newznab_http_403` on some grabs and cross-host redirects to
  `file.treasure-maps.com` carrying the credential — left untouched on purpose.
- `nzb.life`: download locator redirects to `/login` (account/auth), not a
  redirect-policy bug.
- `altHUB`: `newznab_unsafe_redirect_target_rejected` for one title.

**Questions for the reviewer.**
- Is a narrowly-scoped canonical-host/alias mechanism (same registrable site,
  documented per provider, with tests) the right pattern for these, or should
  the owner re-commission the credentials/accounts instead?
- Do not weaken `doWithManualRedirects`/`redirectTarget` in
  `internal/adapters/newznab/client.go`.

## Problem 7 — Seerr library-sync stays disabled

**Context.** Seerr's scheduled Radarr/Sonarr scanner resets to `UNKNOWN` and
**declines** `PROCESSING` requests whose media is absent from the server list.
Media Manager's `/radarr/api/v3/movie` lists 130 movies while Seerr has 380;
the 258 missing ones are mostly old Radarr-era completions never adopted into
Media Manager. Enabling `syncEnabled` would orphan-decline paused or
not-yet-ingested requests, so it is off; Media Manager pushes Plex-confirmed
availability itself (`internal/intake/seerr_status.go`).

**Questions for the reviewer.**
- Should Media Manager expose paused/`needs_configuration` or adopted titles in
  its Servarr list so scans are safe? What is the correct Seerr-side behavior
  for `UNKNOWN` media?
- Is the alternative — bulk-adopt the 258 historical Plex files via
  `internal/adoption` — preferable, and what is the safe review/execution
  sequence?

## Problem 8 — Unmanaged Plex file needs adoption

`Sommerdahl (2020)` exists in Plex at `/media/Dansk Tv/Sommerdahl (2020)` but
is not in Media Manager; Seerr request 25 is completed. The adoption flow
(`POST /api/v1/adoption`, `PATCH .../items/{id}`, `POST .../approve`, `.../execute`)
is the sanctioned path and needs owner review. This is small but blocks that
title from being managed/verified.

## Problem 9 — Recovering a FAILED Seerr request is manual

Seerr marks a request FAILED when its Radarr/Sonarr add call throws. That was
caused by the strict Servarr JSON decoding fixed in `9853275` and deployed.
Even after the fix, a retried request does not reach Media Manager
automatically: `seerr_request_snapshots.state` keeps the last observed status
(`4`), Media Manager's poll only re-observes actionable snapshots, and the
Seerr `POST /api/v1/request/{id}/retry` notification did not produce a webhook
delivery in the live test. Both recent retries had to be backfilled with poll
deliveries (`retry-20260928-418/419`). A fix could make the poll re-observe
`state='4'`/`state='2'` snapshots without active workflows, or verify and handle
the retry notification end to end.

Second gap: `Minions & Monsters` (media
`01a05ee8-55ae-7d1d-8449-012ac11b627a`) has `media_items.deleted_at` set but
**no `recycle_entries` row**, so the Activity → Recycle restore path cannot
bring it back and intake parks with "matching media is recycled". Decide
whether recycled-without-entry media need an explicit owner restore path or
whether recycle entries must always be created when media is recycled.

## Problem 10 — Novelty cutoffs stranded known eligible releases (fixed)

The selection loop skipped every eligible candidate whose `first_seen_sequence`
predated the open round's `novelty_cutoff_sequence`, then parked the workflow as
`held`. After the manual round restore 67 open rounds were stuck this way with
5,154 eligible, never-attempted releases (many with explicit Danish-subtitle
evidence). The supported `apply-policy` path sets the replacement round's cutoff
to zero; the unsupported SQL restore reintroduced high cutoffs. `395f0d6` makes
selection prefer novel candidates and fall back to any eligible candidate that
was never attempted or excluded, and relaxes the submission-intent and
duplicate-package gates the same way. Attempted and excluded releases stay
disqualified, so availability sweeps cannot loop on the same release.

Follow-up worth deciding: a round still allows only two attempts, so a title
with a large candidate pool parks after two failures until a genuinely new
post is discovered. A full availability sweep needs an owner action that opens
a round from known eligible candidates plus a storage policy for failed
outputs (currently retained, capped at four per workflow).

## Environment and gates

- Loopback API: `127.0.0.1:28081`, `Authorization: Bearer <OWNER_TOKEN>`
  (read `OWNER_TOKEN` from `.env`; never print it).
- Servarr API: `X-Api-Key: <SERVARR_API_KEY>` on `/radarr/api/v3`,
  `/sonarr/api/v3` (also in `.env`).
- Seerr: reachable from containers as `http://seerr:5055`; API key in `.env`
  (`SEERR_API_KEY`).
- Tests: `GOFLAGS=-mod=readonly go test -count=1 ./...`; integration tests skip
  unless `MEDIA_MANAGER_TEST_DATABASE_URL` points at a disposable PostgreSQL
  (never the production database). Full race suite:
  `go test -count=1 -race ./internal/postgres/ ./internal/acquisition/ ./internal/httpapi/`.
- Docs gate: `npm run docs:check` (relative links/anchors, wired into CI).
- Read `docs/operations/runbook.md` before any operational action;
  `docs/architecture/workflows.md` for the state machine and attention codes.

## Definition of done for a reviewer session

- Each addressed problem has: a written analysis, a minimal implementation (or
  an explicit "owner decision required" note), focused tests, docs updates, and
  a commit with a conventional prefix.
- Full local gates green (or a precise honest failure).
- No secrets, no live destructive operations, no weakened safety invariants.
