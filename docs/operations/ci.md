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
| `backend` | self-hosted `media-manager-vps` | PostgreSQL service, Go race suite, 12-CPU capacity. |
| `containers` | self-hosted `media-manager-vps` | Requires the owner's separate rootless Docker engine for the disposable stack smoke. |
| `publish` | self-hosted `media-manager-vps` | Needs the rootless Docker engine and registry credentials; runs only on `main` pushes after every required job succeeds. |

The self-hosted runner is still a single-job runner. Backend and containers are
therefore serialized on it. The hosted frontend/browser jobs no longer wait
behind them. Adding a second self-hosted runner is an owner/infrastructure
decision, not something this workflow can invent safely.

## Backend test groups

`backend` runs `scripts/ci/test-backend.sh`. The script:

- runs every non-`internal/postgres` package with `-race`;
- discovers every top-level PostgreSQL test;
- partitions those tests into four groups;
- runs all four PostgreSQL groups concurrently with the full `-race` flag;
- fails if any group fails, and prints that group's log.

The PostgreSQL tests create unique schemas, which is what makes concurrent
groups safe. All 247 PostgreSQL tests are still executed; this is a split of the
same suite, not a reduction. `BACKEND_TEST_SHARDS` can be set from 1 to 8 for a
controlled experiment; the workflow default is 4.

## Caches and reproducibility

- Frontend/browser use the npm cache keyed by `package-lock.json`.
- Backend uses the Go module cache keyed by `go.sum`.
- The Playwright container remains pinned; browser tests are not moved to an
  unpinned browser install.
- Go build/test results are not cached as passing tests (`-count=1`); only
  compilation artifacts may be reused by Go's build cache. Every test still
  runs.

## Cancellation and main safety

The concurrency group is per Git ref:

- a superseded feature-branch push may cancel its own earlier Verify run;
- an in-progress `main` Verify run is never cancelled by a newer push or merge;
  GitHub may still drop a superseded *pending* main run when several main
  pushes arrive without a worker slot. The newer commit contains the older
  work, but the dropped commit itself has no Verify result and no `sha-<commit>`
  image; this is an intentional main-serialization trade-off;
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

Then enable GitHub's native merge queue for `main`, with the same required
checks. The merge queue creates a new merge candidate commit and runs Verify on
that candidate before merging, which is the correct place to validate PRs after
earlier merges. Auto-merge may be enabled only with that protection; an approval
must never be transferred to a newer HEAD. Publish and production deployment
remain separate from merge authorization.

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
backend and containers remain serialized on the single self-hosted worker.
