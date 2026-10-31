# Verification CI

`Verify` is triggered by every same-repository push and by manual dispatch. It
deliberately has **no `pull_request` trigger**, so fork code never executes on
the owner's self-hosted runner. Fork PRs are reviewed by the owner and validated
by a trusted push after review.

## Runner layout

| Job | Runner | Why |
| --- | --- | --- |
| `frontend` | GitHub-hosted `ubuntu-24.04` | No production secrets; runs lint, typecheck, docs checks, dependency audits and unit tests. GitHub-hosted capacity runs it in parallel with the self-hosted work. |
| `browser` | GitHub-hosted `ubuntu-24.04`, pinned Playwright container | No production secrets; the browser image remains pinned to `mcr.microsoft.com/playwright:v1.62.1-noble`. |
| `backend` | GitHub-hosted `ubuntu-24.04` | Experiment (2026-10-31): needs only a disposable PostgreSQL service and the Go toolchain, so it runs in parallel with the self-hosted container smoke instead of queueing behind it. |
| `containers` | self-hosted `media-manager-vps` | Requires the owner's separate rootless Docker engine for the disposable stack smoke. |
| `publish` | self-hosted `media-manager-vps` | Needs the rootless Docker engine and registry credentials; runs only on `main` pushes after every required job succeeds. |

The self-hosted runner is still a single-job runner, so `containers` and
`publish` are serialized on it. `backend` no longer occupies that slot, which
removes the 37–80 s container tail and, more importantly, stops a queued
`backend` from blocking the container smoke for up to 26 minutes when several
pushes overlap. `publish` still waits for every verification job. Adding a
second self-hosted runner remains an owner/infrastructure decision, not
something this workflow can invent safely.

## Backend test groups

`backend` runs `scripts/ci/test-backend.sh`. The script:

- runs every non-`internal/postgres` package with `-race`;
- discovers every top-level PostgreSQL test;
- partitions those tests into four groups;
- runs all four PostgreSQL groups concurrently with the full `-race` flag;
- fails if any group fails, and prints that group's log;
- prints one timing line per group with its name, exit status, test count and
  elapsed seconds, plus a total at the end.

The PostgreSQL tests create unique schemas, which is what makes concurrent
groups safe. All 247 PostgreSQL tests are still executed; this is a split of the
same suite, not a reduction. `BACKEND_TEST_SHARDS` can be set from 1 to 8 for a
controlled experiment; the workflow default is 4.

Groups run with `go test -json`; `-count=1`, `-race` and the 20-minute timeout
are unchanged. On failure the original plain-text output is rendered back from
the JSON `Output` events, so diagnostics read exactly like `go test`. A group
reports like:

```text
=== postgres-2: exit 0, 66 tests (65 passed, 0 failed, 1 skipped) in 341s ===
```

Use these lines to compare shard balance between runs. A change is only an
improvement if every group still reports its tests and the totals do not drop.

## Caches and reproducibility

- Frontend/browser use the npm cache keyed by `package-lock.json`.
- Backend uses the Go module cache keyed by `go.sum`.
- The Playwright container remains pinned; browser tests are not moved to an
  unpinned browser install.
- Go build/test results are not cached as passing tests (`-count=1`); only
  compilation artifacts may be reused by Go's build cache. Every test still
  runs.

## Cancellation and main safety

The concurrency group is per Git ref for feature branches and per commit for
`main`:

- a superseded feature-branch push may cancel its own earlier Verify run;
- every `main` commit gets its own group, so a newer push cannot replace a
  queued verification for an earlier merge; queued main commits run in order;
- `cancel-in-progress` stays false for `main`, so an active main verification is
  never cancelled by a newer push or merge;
- this deliberately queues more self-hosted work on a busy main branch in
  exchange for preserving per-commit evidence and `sha-<commit>` publication;
- `publish` is a separate job that requires frontend, browser, backend and
  containers; it never starts from a cancelled or incomplete verification;
- production deployment remains a separate manual workflow and is not triggered
  by merges.

## CI status without blocking

Agents must not wait synchronously for GitHub Actions. After a push, make one
short status check and record the exact run:

```sh
sha="$(git rev-parse HEAD)"
gh run list --repo Media-manager52/Media-manager --workflow Verify \
  --commit "$sha" --limit 1 --json databaseId,url,status,conclusion
```

If the run is queued or in progress, mark it **PENDING** and continue with
independent work on a separate branch/worktree. Do not use `gh run watch`,
`sleep` loops, repeated polling, or long command timeouts. Recheck only at a
natural dependency boundary. If no independent work remains, finish the turn and
report the pending run.

A merge is allowed only after the exact approved commit has successful required
checks and a completed review gate. A newer commit never inherits an older
commit's approval or CI result. If a dependent merge is waiting for CI and no
authorized GitHub-native auto-merge/merge queue is configured, stop that merge
and report **WAITING FOR CI**.

## Merge queue and branch protection

The GitHub `main` branch is currently **not protected** (verified 2026-10-30).
For public-beta operation the owner should enable branch protection requiring:

- the exact Verify jobs (`frontend`, `browser`, `backend`, `containers`);
- at least one independent approval;
- dismissal of stale approvals after new commits;
- no force pushes or deletions.

GitHub's native merge queue is **not enabled**, and Verify does not currently
have a `merge_group` trigger. Do not claim that the queue runs this workflow:
without a separate `merge_group` trigger it would wait forever for the required
checks. Enabling the queue requires a separate security review of the
self-hosted runner exposure and a workflow change; until then, merge sequentially
and verify the exact resulting commit. An approval must never be transferred to a
newer HEAD. Publish and production deployment remain separate from merge
authorization.

## Measured baseline

The 20 most recent Verify runs before this change (2026-10-30) showed:

- successful-run median wall time: **1,992 s** (about 33 minutes);
- backend median execution: **586 s**, with `internal/postgres` alone taking
  about **598 s** in the final main run;
- browser median: **294 s**;
- frontend median: **62 s**;
- containers median: **36 s**;
- every job was queued behind the single self-hosted worker in several runs.

A local four-group PostgreSQL run completed in about **285 s** instead of about
598 s, while all 247 PostgreSQL tests passed under `-race`. Two optimized
GitHub runs on an idle runner (`41063007937` and `41064445136`) completed in
**471 s** and **465 s**; their longest PostgreSQL shards were **345 s** and
**343 s**. These are two samples, not a like-for-like median claim. The
GitHub-hosted frontend/browser split removes their serialized queue time;
at that point backend and containers were still serialized on the single
self-hosted worker.

### Baseline for the CI optimization work

The post-split steady state measured on 2026-10-30 (runs `41138196759` and
`41080415579`) is **432 s** for a branch push and **477 s** for a `main` push:

| Segment | Seconds |
| --- | --- |
| `backend` job (setup + tests) | 386 |
| `test-backend.sh` step | 332–343 |
| `containers` job, which only starts once `backend` frees the runner | 37 |
| `frontend` / `browser` (parallel, off the critical path) | 64–97 / 250–342 |

To compare the next run, read the job and step timings and the per-group lines:

```sh
run=<run-id>
gh api "repos/Media-manager52/Media-manager/actions/runs/${run}/jobs" \
  --jq '.jobs[] | {name, started_at, completed_at}'
```

Compare the `backend` execution time, the `test-backend.sh` step, the longest
`=== postgres-N` group and the run wall time (`created_at` → `updated_at`).
Quote the run ID with every number, and remember that a single run on a shared
runner is noisy: the queue time of `containers` is part of the wall clock.

## Hosted backend experiment

`backend` currently runs on GitHub-hosted `ubuntu-24.04`; `containers` and
`publish` stay on the self-hosted runner. The previous self-hosted baseline was
**432 s** of total workflow wall time on a feature-branch push.

Judge the experiment on two numbers from the same run, not on wall time alone:

- `backend` job execution time (checkout, setup and `test-backend.sh`), which
  should stay close to the 386 s baseline;
- total workflow wall time, which shows whether freeing the single-job runner
  actually shortened the critical path below 432 s.

The first hosted run may be slower: its Go module and build caches are cold, so
its `test-backend.sh` step is not comparable to a warm baseline. Retain the
hosted runner only if warm-run performance is acceptable — after the cache is
warm, backend execution must not regress materially and the total workflow wall
time should stay below the 432 s baseline. If it does not, revert and record the
measured numbers here.

Reverting is a one-line change: restore
`runs-on: [self-hosted, linux, x64, media-manager-vps]` on the `backend` job and
update the runner layout above. No verification gate changes with the runner:
the Go race suite, the four PostgreSQL groups, the container smoke and the
publishing rules all remain exactly as documented above.
