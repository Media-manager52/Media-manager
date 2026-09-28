# Seerr retrieval recovery review — 2026-09-28

Production was inspected read-only, running `78c127b`, schema 40. This local
fix introduces schema 41 and **has not been deployed**. No requests were
retried during debugging. The separately approved update of nine provider
budgets from Danish Intelligence was already applied earlier in the session.

## Live evidence

At 21:37:58 UTC, requests 423 and 427 remained `needs_attention`, with
`release_retrieval_failed` and no pending workflow events. Requests 424–426
and 428 were `plex_confirmed`, each with one AltMount job.

| Request | Measured result |
| --- | --- |
| Borderline #423 | First output failed Danish verification (observed English/French/Spanish). The next NZBFinder candidate failed NZB retrieval twice with `newznab_invalid_nzb`. Its open round used one of two submission slots. At 21:15, 41 eligible, unattempted, unexcluded candidate identities remained; none had an explicit `da` fact. |
| Monster House #427 | Selected treasure-maps release returned HTTP 403. No job or slot was consumed. At 21:17, other providers included two explicit-Danish candidates on NZBFinder and five on abNZB. |
| Fall #424, Ice Road: Vengeance #425, Black Hawk Down #426, The Covenant #428 | Required Danish verification and exact Plex confirmation completed. |
| Lucky Strike #422 | Intake found an owner-archived media record and stopped. Restoration/reprocessing needs a separate owner decision. |

Candidate identities are provider/native-GUID pairs, not necessarily distinct
packages. Eligibility is a policy decision over observations, not proof of
Danish subtitles. These are dated snapshots, not permanent availability claims.

`RecordReleaseRetrievalFailure` previously excluded the exact release and
parked the workflow regardless of remaining slots or known alternatives.
Separately, retrieval fetched the NZB before the retained-output guard in
`BeginAltMountSubmission`. At 20:51, Power Ballad had four retained failed
outputs and a retrieval event with 27 attempts. The call ordering proves a late
admission check; that event count does not quantify provider-billed grabs.

## Local fix

- Check the exact selection/revision, open round, current policy/publication
  baseline, remaining slots, retained-output cap, blocklist and round-local
  provider denial before NZB work and at each request hop. Keep transactional
  submission and exact-package guards.
- Record retrieval failures against their round in additive nullable columns
  on `workflow_release_exclusions`. Leave historical failures untouched.
- Select a known eligible alternative and enqueue retrieval atomically within
  the same round. This transition creates no search, job, slot or round.
  Stale event replay does nothing.
- Stop after four failed candidate retrievals per round. Ordinary failures
  retain the two-attempt limit; HTTP 401/403 stops after one request and avoids
  that provider for the rest of this workflow's round. Budget/pacing deferrals
  do not consume the failure cap. Redirect and credential protections remain.
- Danish-required searches rank explicit observed/inferred `da` facts before
  bounded Danish/Nordic title clues, then unknown evidence. Eligibility,
  policy scores, upgrade qualification and output verification do not change.
  Existing search novelty preference still applies. Retrieval recovery uses
  evidence order before novelty/quality and deprioritizes titles that already
  failed subtitle inspection. Titles are not merged as identical packages.
- Recovery respects enabled/commissioned providers, policy-version decisions,
  exact release exclusions, attempted releases and current blocklist entries.
  Main search, duplicate-package fallback and budget cross-post fallback also
  avoid providers denied in the open round. Providers stay enabled globally.

Attention distinguishes no remaining suitable candidate, the retrieval failure
limit, retained-output capacity and round/policy/source blockers. Selection
reasons and workflow transitions explain recovery.

This does not discover new subtitle metadata or guarantee success for
Borderline. Unknown candidates may use its remaining slot and fail verification.
NFO/attribute enrichment and measured group history need separate bounded
investigation and approval before live provider calls. Two downloads per round,
four retained failures, required subtitles and exact Plex verification remain.

## Reproduction and tests

[Read-only SQL](retrieval-recovery-review.sql) reproduces request, round,
candidate and verification evidence. Run from the repository root:

```sh
docker compose exec -T postgres sh -c 'PGOPTIONS="-c default_transaction_read_only=on -c statement_timeout=30000" psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -P pager=off' < docs/status/retrieval-recovery-review.sql
```

Regressions cover remaining-slot recovery, Danish evidence versus clues,
provider denial after one HTTP request, stale delivery, four-candidate
exhaustion, storage/round/policy admission, disabled/blocked/attempted
alternatives, and migration without requeueing historical work. Tests use
isolated random schemas on disposable `mm-review-pg`, port 5433, never production.
See [release status](release.md) for the final gate results.

## Owner decisions

1. Approve deployment and the additive schema 41 migration. Existing pending
   work will then use the new behavior.
2. Separately approve resuming Borderline #423 and Monster House #427 within
   their existing open rounds. Migration leaves parked workflows unchanged;
   any resume must first recheck their current state and policy.
3. Decide separately on Lucky Strike restoration, treasure-maps credential
   review and retained-output storage review. This patch performs none of them.
