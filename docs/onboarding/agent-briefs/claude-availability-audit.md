# Media Manager — availability audit brief (are the missing titles truly missing?)

**Audience:** a fresh Claude Code reviewer session with full repo and server
access at `/home/vps/media-manager`. **Mission:** independently determine which
of the non-terminal ("missing") titles are genuinely unavailable with Danish
subtitles (or exempt as Danish originals) and which are blocked by a pipeline
defect; find and, where approved, fix the defects. The owner has nine active
Newznab indexers and a strong prior that releases exist, so a conclusion of
"unavailable" must be backed by evidence, not by the absence of a selection.

Read `AGENTS.md` first, then
`docs/onboarding/agent-briefs/claude-review-hard-parts.md` (problems 1–10; 5–8
are still open and overlap this audit) and the `docs/architecture/*.md` set.
This brief is the availability-specific expansion.

## What changed immediately before this brief

- `395f0d6` (deployed, running) fixed a systemic selection bug: a round's
  `novelty_cutoff_sequence` made the selector skip every eligible candidate
  first observed before the cutoff and park the workflow as `held`. 67 open
  rounds, 5,154 eligible never-attempted releases (many with explicit Danish
  subtitle evidence) were stranded. Selection now prefers novel releases and
  falls back to any eligible release that was never attempted or excluded; the
  submission-intent and duplicate-package gates follow the same rule.
- `36560ba` documents the bug and a follow-up: a round still allows only **two
  download attempts**, then the workflow parks until discovery reports a new
  eligible release. A full sweep of a large candidate pool is not possible yet.
- A first post-fix wave (47 requeued searches) produced 27 selections in the
  first hour. Explicit evidence that the pipeline can find Danish:
  `Reacher.S04E04.NORDiC`, `Outlander...S02E01/E02.NORDiC`, `Dirch...DANiSH`,
  `Fuze...DKsubs` all reached `subtitle_verifications.status='verified'` on
  2026-09-28. In the same window 11 downloads failed truthfully with no Danish
  track (e.g. `Under the Skin` had 18 subtitle languages and no `da`; NCIS
  S06/S08/S11/S22 releases had none), 28 releases were rejected as archives
  before download, and 11 retrieval attempts failed at the provider.
- Suspected false negative: `Criminal Minds` S16 workflow
  `01a0a0dc-80be-7b81-b536-4d88f4f8d086` has a 2026-09-14 verification whose
  `observed_languages` contains `da` but whose status is `unverified` with
  reason "has no verified da subtitle". Determine whether the `da` was an audio
  track (correct failure), a subtitle track (verifier bug), or a subtitle whose
  language could not be confirmed.

## Verify first — the snapshot below is a claim, not ground truth

You are on the same server the author was. Services restart, searches advance,
and the backlog drains between sessions. Reproduce state read-only before
drawing any conclusion.

```sh
cd /home/vps/media-manager
git log --oneline -8 && git status --short
docker ps --format '{{.Names}}\t{{.Status}}' | grep -E 'media-manager|seerr|altmount|plex'
docker compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc "SELECT max(version) FROM schema_migrations;"'
```

Non-terminal inventory and where each workflow parks:

```sh
docker compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -P pager=off' <<'SQL'
SELECT state, COALESCE(attention_code,'-') AS code, count(*)
FROM workflows WHERE terminal_at IS NULL GROUP BY 1,2 ORDER BY 3 DESC;

SELECT event_type, count(*) FROM outbox_events WHERE completed_at IS NULL GROUP BY 1 ORDER BY 2 DESC;
SQL
```

Indexer coverage (all nine must succeed; `deferred` is normal pacing):

```sh
docker compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -P pager=off' <<'SQL'
SELECT i.name, i.enabled, r.state, count(*)
FROM search_run_indexers r
JOIN integration_instances i ON i.id=r.indexer_instance_id
JOIN search_runs s ON s.id=r.search_run_id
WHERE s.started_at > now() - interval '24 hours'
GROUP BY 1,2,3 ORDER BY 1,3;
SQL
```

Per-title evidence: for every non-terminal workflow, how many candidates were
observed, how many carry an explicit `da` subtitle fact, how many were never
attempted, and which failure kinds were recorded:

```sh
docker compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -P pager=off' <<'SQL'
WITH blocked AS (
  SELECT w.id, m.title, w.variant, w.state, COALESCE(w.attention_code,'-') AS code
  FROM workflows w JOIN media_items m ON m.id=w.media_id
  WHERE w.terminal_at IS NULL)
SELECT b.title, b.variant, b.state, b.code,
       count(c.id) AS observed,
       count(c.id) FILTER (WHERE EXISTS (
         SELECT 1 FROM jsonb_array_elements(o.normalized_facts) f
         WHERE f->>'name'='subtitle_languages' AND f->'values' @> '["da"]'::jsonb)) AS with_da,
       count(c.id) FILTER (WHERE NOT EXISTS (
         SELECT 1 FROM workflow_attempted_releases a
         WHERE a.workflow_id=b.id AND a.candidate_id=c.id)) AS never_attempted
FROM blocked b
JOIN release_candidates c ON c.workflow_id=b.id
JOIN release_observations o ON o.id=c.observation_id
GROUP BY 1,2,3,4 ORDER BY 1,2;
SQL
```

Verification evidence (what the probe actually saw, and why it failed):

```sh
docker compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -P pager=off' <<'SQL'
SELECT m.title, w.variant, left(c.title,50) AS release, sv.status,
       sv.required_languages, sv.observed_languages,
       left(sv.failure_reasons::text,160) AS failure,
       to_char(sv.created_at,'MM-DD HH24:MI') AS at
FROM subtitle_verifications sv
JOIN workflows w ON w.id=sv.workflow_id
JOIN media_items m ON m.id=w.media_id
LEFT JOIN release_candidates c ON c.id=sv.candidate_id
WHERE sv.created_at > now() - interval '24 hours'
ORDER BY sv.created_at DESC LIMIT 40;

SELECT failure_kind, count(*) FROM workflow_release_exclusions
WHERE created_at > now() - interval '24 hours' GROUP BY 1 ORDER BY 2 DESC;
SQL
```

Read-only API probes: `TOKEN="$(grep -E '^OWNER_TOKEN=' .env | cut -d= -f2-)"`,
then `curl -fsS -H "Authorization: Bearer $TOKEN" http://127.0.0.1:28081/api/v1/system/health`.
Never print the token.

## Hypotheses to test (each needs live evidence, not code reading alone)

### H1 — Selection ranking starves Danish-evidence releases
`appendSubtitleRules` (`internal/acquisition/search.go`) adds a required rule
with `Unknown: allow` plus a preferred evidence rule for `da`. `NORDiC` is
deliberately not inferred as `da` (`internal/releases/title.go`), so a NORDiC
pack can lose to a higher-scored no-evidence release.
- Do explicit-`da` candidates actually outrank NORDiC/no-evidence candidates?
  Check `candidate_decisions.score` and `reasons` for the same workflow.
- Should `NORDiC`/`DANiSH`/`DKsubs`/`Retail.DKsubs` be a ranked tie-breaker
  (not a fact) without weakening the post-download requirement?
- Acceptance: measured subtitle-hit rate improvement on the same candidate
  pool, with tests; no release may pass without a verified `da` track.

### H2 — Archive rejects consume round slots
28 `archive_unsupported` rejections in one hour (RAR sets). The pre-flight
screening is correct, but the selector still spends attempts on them.
- Can archive likelihood be estimated pre-selection (title/attrs/group/size)
  and used for ordering, without skipping the screening itself?
- Should an archive rejection consume an attempt slot at all, or requeue the
  round without counting it? Document the storage/pacing consequences.
- Acceptance: a policy with tests showing fewer wasted slots, never accepting
  an archive.

### H3 — Subtitle verifier false negatives (highest-value investigation)
The Criminal Minds S16 case above. `internal/subtitles/verify.go` builds
`observed_languages`; establish exactly which stream types contribute and how
`da` is confirmed.
- Distinguish audio vs subtitle tracks; check `language`/`title` MKV tags
  (`dan`, `dansk`, `Danish`, `DK`), forced/SDH tracks, `.srt`/`.sub` sidecar
  files, and multi-file season packs (is every file probed?).
- Reproduce on a known NORDiC pack (they verify today) and on the Criminal
  Minds file if it is retained; compare probe output with the verifier result.
- Acceptance: either a fix with tests for the false-negative class, or a
  written proof that the failure is correct (Danish audio, no subtitles).

### H4 — Search query construction misses Danish releases
- Are all nine indexers queried with the same query shape? Inspect
  `internal/adapters/newznab/client.go` and `internal/acquisition/search.go`.
- Does the query use the original/alternate titles and aliases the owner cares
  about (e.g. `Drabscheferne - de uopklarede sager`), and TVDB/IMDB ID search
  modes where supported? Are results paged (indexers default to 100)?
- Examine a title whose releases use a different title than TMDB shows.
- Acceptance: concrete query improvements with tests, or proof the query is
  already complete.

### H5 — Retrieval/provider failures still block grabs
Known: treasure-maps `newznab_http_403` and cross-host credential redirects;
nzb.life download locator to `/login`; altHUB `newznab_unsafe_redirect_target_rejected`;
NZBFinder daily grab limits. The msgnews canonical-host fix
(`canonicalDownloadHosts` in `internal/adapters/newznab/retrieval.go`) is the
safe precedent.
- Which indexers fail structurally vs per-release? Quantify by indexer.
- Do not weaken `doWithManualRedirects`/redirect pinning/credential stripping.
- Acceptance: per-provider recommendation (fix pattern or re-commission
  credentials) with evidence; owner decides live credential changes.

### H6 — Discovery and round opening cannot use known candidates
After two failed attempts a round exhausts and `OpenNextAcquisitionRound`
requires `workflow_retry_discovery.state='ready'` with a candidate first seen
after the retained cutoff. Known, never-attempted eligible releases cannot open
a round.
- How deep does the discovery rescan look, and how often?
- Design (do not implement without owner approval) an owner action that opens
  a round from known never-attempted eligible candidates, preferring explicit
  `da`, then Nordic markers, then the rest; define the failed-output storage
  policy (retained today, capped at four per workflow) required to make a sweep
  safe.
- Acceptance: written design + storage math + owner decision.

### H7 — Parsing holds legitimate releases
Examples seen: `NCIS 2003 Season 14 NL Subs ingebakken` (season token not
recognized), partial-season packs (`NCIS.S19E16-E21...`), releases without a
year (`requested movie year` hold, `internal/acquisition/search.go`), and
`series identity evidence` holds for Danish TV titles.
- Quantify how many held decisions are caused by each scope rule and whether
  the fact is genuinely unknowable or a parser gap.
- Acceptance: parser fixes with tests (never weakening required evidence).

### H8 — Per-title truth table
For every non-terminal workflow, produce a row with: title, variant, media
year/kind, profile (required or Danish original), candidates observed,
explicit-`da` candidates, attempts made, verification outcomes (languages
observed), provider failures, and a verdict from {`verified da`, `verified
without da` (should not exist for required profiles), `not yet tried`,
`blocked by provider`, `blocked by archives`, `blocked by policy/round limit`,
`no da release observed anywhere`}.
Report the counts per verdict. This is the deliverable that answers the
owner's question.

## Deliverables

1. A findings report (Markdown, committed under `docs/` or posted in the
   session) giving per-hypothesis analysis with the exact queries and results,
   separating "truly unavailable" from "pipeline defect".
2. The H8 truth table for the current non-terminal set.
3. For each confirmed defect: a minimal fix on a branch or `main` with focused
   tests, plus `CHANGELOG.md`/brief updates. The full gate set must be green:
   `GOFLAGS=-mod=readonly go test -count=1 ./...` with
   `MEDIA_MANAGER_TEST_DATABASE_URL` pointing at a **disposable** PostgreSQL
   (never production), plus `npm run docs:check`.
4. An explicit owner-decision list for anything needing live changes (rounds,
   profiles, credentials, sweeps, storage).
5. No live migration, deploy, round opening, blocklist change, or provider
   change without explicit owner approval.

## Hard safety rules (never violate)

1. AltMount output is read-only; never write, move, or delete inside it.
2. Publish only via `internal/library`; no raw symlink/rename operations.
3. A season pack is one AltMount job with one immutable expectation.
4. Nothing succeeds without exact Plex identity + path confirmation.
5. Never weaken Newznab pacing, retry budgets, redirect pinning, TLS checks,
   credential stripping, or secret sanitization.
6. Failed outputs stay retained and owner-visible unless the owner approves a
   new storage policy.
7. Only the owner opens acquisition rounds; never fake recovery.
8. Never print, log, or commit secrets (`.env`, `data/provider-secrets/`).
   Redact provider probes with `sed "s/$KEY/REDACTED/g"` and never follow a
   credential-bearing redirect automatically.
9. Frontend fixtures and test data must never enter the live artifact.

## Definition of done

- Each hypothesis has a written conclusion backed by live evidence, or an
  explicit "cannot determine without owner action" note.
- The H8 truth table exists and its counts reconcile with the repository.
- Confirmed defects have tests; no safety invariant is weakened; gates are
  green; every commit uses a conventional prefix.
- The owner has a clear, prioritized list of remaining decisions.
