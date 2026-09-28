# Media Manager — Codex/GPT-6-Astra availability review brief

**Audience:** a coding-agent session (Codex/GPT-6-Astra or similar) running on
the same server as Media Manager with full repo, container, log, and database
access at `/home/vps/media-manager`. **Mission:** challenge and extend the
availability work, find defects the previous sessions missed, and propose or
implement safe fixes. You are expected to verify everything yourself before
acting: the snapshot below is a claim, not ground truth, and the pipelines move
between sessions.

Read `AGENTS.md` first, then `docs/onboarding/README.md`, then
`docs/status/availability-audit.md` (the per-title truth table and defect
evidence), then `docs/onboarding/agent-briefs/claude-review-hard-parts.md`
(problems 1–10; 5–8 remain open here) and
`docs/onboarding/agent-briefs/claude-availability-audit.md` (the H1–H8
hypotheses). Use `docs/architecture/*.md` for how the system works.

## Verify first — re-check everything read-only

```sh
cd /home/vps/media-manager
git log --oneline -8 && git status --short
docker ps --format '{{.Names}}\t{{.Status}}' | grep -E 'media-manager|seerr|altmount|plex'
docker compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc "SELECT max(version) FROM schema_migrations;"'

# Live commit and health (token read into a variable, never printed).
TOKEN="$(grep -E '^OWNER_TOKEN=' .env | cut -d= -f2-)"
curl -fsS -H "Authorization: Bearer $TOKEN" \
  "http://127.0.0.1:${MEDIA_MANAGER_API_PORT:-28081}/api/v1/system/info" | head -c 400; echo
```

Non-terminal workflows, attention codes, and the outbox backlog:

```sh
docker compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -P pager=off' <<'SQL'
SELECT state, COALESCE(attention_code,'-') AS code, count(*)
FROM workflows WHERE terminal_at IS NULL GROUP BY 1,2 ORDER BY 3 DESC;
SELECT event_type, count(*) FROM outbox_events WHERE completed_at IS NULL GROUP BY 1 ORDER BY 2 DESC;
SELECT status, count(*) FROM subtitle_verifications
WHERE created_at > now() - interval '24 hours' GROUP BY 1;
SQL
```

Per-title evidence (observed candidates, explicit `da` facts, untried
releases) and failure kinds — see the queries in
`docs/onboarding/agent-briefs/claude-availability-audit.md`; reuse them rather
than rewriting.

## Snapshot at hand-off (claim — re-measure)

- Running commit `78c127b`, schema 40. Previous sessions deployed: the
  Radarr/Sonarr-compatible API for Seerr, the availability-audit fixes D1–D7
  (`23c95a3`, `f9c8b2a`, `cc57d7e`, `d34e7bf`, `3486ab3`), and the follow-ups in
  `78c127b` (pre-submission screening without spending a round slot, owner
  endpoints `acquisition-rounds/from-known` and `revive-rejected-archives`,
  per-episode dispatch for episode-only completed seasons).
- Live execution already done: 60 pre-upload archive rejections revived across
  41 workflows; 24 rounds opened from known eligible releases (one refused by
  the retained failed-output cap on *Under the Skin*); Rivalerne S01 dispatched
  its first episode workflow. The rest of the episode-only seasons are queued
  behind a single-event worker (roughly 190 pending searches when last seen).
- Non-terminal workflows at hand-off: ~64 `searching`, ~54
  `candidate_selected`, ~21 `waiting`, ~10 `needs_attention` with
  `release_retrieval_failed`, 3 `held`, 1 `round_exhausted_waiting_new_identity`.
- Subtitle verification, last 24h: 67 verified with Danish, 103 failed with no
  Danish track. Historical: releases with explicit `da` evidence verified 96/96;
  releases with no marker verified ~10% (204 attempts). Evidence quality, not
  ranking, is the bottleneck.
- The audit concluded only 5 of ~169 non-terminal titles are genuinely
  unavailable (Pokémon films and niche specials).

## What the owner wants from this session

1. **Falsify the audit.** Challenge the "5 truly unavailable" conclusion and
   the per-title verdicts. What would a rigorous, reproducible availability
   audit look like, and where is the current one wrong?
2. **Improve Danish evidence.** NORDiC/DANiSH/DKsubs markers are deliberately
   unknown pre-download and verified after. Is there a defensible way to use
   them (or indexer attributes, group history, cross-post behavior) without
   blocking or accepting bad releases? Ranking must never bypass verification.
3. **Attempt economics and the sweep.** Two downloads per round, failed outputs
   retained (cap 4/workflow), archives and provider failures waste attempts.
   Design (and, with approval, implement) a sweep through large candidate pools
   that respects storage, grab budgets, and the one-season-one-pack rule.
4. **Providers.** treasure-maps fails every grab with HTTP 403 while spending
   budget; nzb.life download locators redirect to `/login`; altHUB has unsafe
   redirects; indexer budgets use conservative defaults. Recommend fix patterns
   (msgnews's canonical-host precedent) vs re-commissioning credentials.
5. **Observability.** Make "why is this title missing?" answerable in one
   screen with evidence for every conclusion (per-title state, attempts,
   verification evidence, blockers, next action).
6. **Fresh bug hunt.** Everything above is the known set. Look for defects the
   previous sessions missed — selection, verification, intake, publication,
   state machine, worker scheduling, retries. Verify claims against live data
   before writing code.
7. **Architecture.** If this were yours, what would you change? Be specific and
   prioritise.

Open owner decisions already recorded: treasure-maps disable/re-commission,
indexer budget limits, *Under the Skin* retained-output review, Seerr scan
sync/orphan-decline (see problem 7), historical-title adoption (problem 8), and
the `Minions & Monsters`/`The Odyssey` intake cases (problem 9).

## Hard safety rules (never violate, even when proposing)

1. AltMount output is read-only; never write/move/delete inside it.
2. Publish only contained Plex-safe symlinks through `internal/library`.
3. A season pack is one AltMount job with one immutable expectation.
4. Nothing succeeds without exact Plex identity and path confirmation.
5. Never weaken Newznab pacing, retry budgets, redirect pinning, TLS checks,
   credential stripping, or secret sanitization.
6. Failed outputs stay retained and owner-visible unless the owner approves a
   storage policy.
7. Only the owner opens acquisition rounds; never fake recovery.
8. Never print, log, or commit secrets (`.env`, `data/provider-secrets/`).
   Redact provider probes (`sed "s/$KEY/REDACTED/g"`) and never auto-follow a
   credential-bearing redirect.
9. Tests that need a database must use a disposable PostgreSQL
   (`MEDIA_MANAGER_TEST_DATABASE_URL`, port 5433 container), never production.

## Deliverables

- A findings report with exact queries/commands and results, separating "truly
  unavailable" from "pipeline defect".
- For each confirmed defect: a minimal fix with focused tests, conventional
  commit, and docs/CHANGELOG updates. Gates: `gofmt`, `go vet ./...`,
  `GOFLAGS=-mod=readonly go test -count=1 ./...` plus the race suite on a
  disposable database, and `npm run docs:check`.
- An explicit owner-decision list for anything requiring live changes.
- No live migration, deploy, round opening, blocklist change, provider change,
  or search/grab without the owner's explicit approval.

## Definition of done

Every claim in the report has live evidence; every fix has tests; no safety
invariant is weakened; the owner has a prioritised, actionable list.
