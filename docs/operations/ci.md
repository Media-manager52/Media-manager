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
| `backend-checks` | GitHub-hosted `ubuntu-24.04` | No production secrets; gofmt, vet, staticcheck, govulncheck, every non-PostgreSQL package under `-race`, migrate and the `go.mod` check, with its own throwaway PostgreSQL. |
| `backend-db` (6 machines) | GitHub-hosted `ubuntu-24.04` | No production secrets; each machine runs one sixth of the PostgreSQL tests under `-race` against its own throwaway PostgreSQL. |
| `backend` | GitHub-hosted `ubuntu-24.04` | The single result the broker checks; it succeeds only when `backend-checks` and every `backend-db` machine succeeded. |
| `containers` | self-hosted `media-manager-vps` | Requires the owner's separate rootless Docker engine for the disposable stack smoke. |
| `publish` | self-hosted `media-manager-vps` | Needs the rootless Docker engine and registry credentials; runs only on `main` pushes after every required job succeeds. |

The deployment broker enforces this exact placement (`JOB_PLACEMENT` in
`deploy/github/deploy-broker.py`): a required job on any other runner, or a
VPS job moved to a hosted runner, blocks deployment. A contract test in
`tests/ci/test_deploy_broker.py` fails if `verify.yml` and the broker disagree,
so moving a job requires changing both and reinstalling the broker.

The self-hosted runner is still a single-job runner, but since 2026-11-05 it
runs only `containers`, `publish` and `deploy`. The backend suite moved to
GitHub-hosted machines: the repository is public, so they cost nothing, and on
the VPS the suite was capped at five CPUs by the CI slice, competed with Plex
and streaming, and queued behind deployments (a Verify run waited nine minutes
behind a deployment's backup on 2026-11-05). Adding a second self-hosted
runner remains an owner/infrastructure decision.

## Backend test groups

`backend-checks` and `backend-db` run `scripts/ci/test-backend.sh`. The script:

- with `BACKEND_TEST_PART=other`, runs every non-`internal/postgres` package
  with `-race`;
- with `BACKEND_TEST_PART=postgres:<machine>/<machines>`, discovers every
  top-level PostgreSQL test, sorts them and keeps test *i* when
  *i* mod *machines* is this machine, so every test runs on exactly one
  machine (`tests/ci/test_backend_split.py` checks that the matrix starts every
  machine the split counts);
- without `BACKEND_TEST_PART` (local use), runs everything;
- partitions this machine's PostgreSQL tests into `BACKEND_TEST_SHARDS` groups
  (the workflow sets 4; the script default is 4, allowed 1–16);
- runs all PostgreSQL groups concurrently with the full `-race` flag;
- fails if any group fails, and prints that group's log.

The PostgreSQL tests create unique schemas, which is what makes concurrent
groups safe. Every PostgreSQL test is still executed; this is a split of the
same suite, not a reduction.

Two things keep the groups from waiting on each other:

- `Migrate` locks per schema. The production schema `public` keeps the single
  historical advisory key; any other schema locks on its own
  `(namespace, schema)` key (`internal/postgres/migrate.go`, pinned by
  `TestMigrationLockKeepsProductionKeyAndIsolatesTestSchemas`). Before this,
  every test schema queued behind one database-wide lock, and more groups made
  the suite slower.
- The workflow raises the throwaway test database's
  `max_locks_per_transaction` to 1024, because ten groups dropping whole
  schemas at once overflow the default lock table ("out of shared memory").

Measured on the VPS: 4 groups with the shared lock took about 6 minutes;
10 groups with per-schema locks take about 2.5 minutes.

## Caches and reproducibility

- Frontend/browser run in `web/` and use the npm cache keyed by `web/package-lock.json`.
- Backend uses the Go module cache keyed by `go.sum`.
- The Playwright container remains pinned; browser tests are not moved to an
  unpinned browser install.
- Go build/test results are not cached as passing tests (`-count=1`); only
  compilation artifacts may be reused by Go's build cache. Every test still
  runs.

## Identical trees are not tested twice

A pull request branch is verified when it is pushed, and after a squash merge
the `main` commit usually has exactly the same tree. On a `main` push the
`reuse` job (`scripts/ci/find-tested-run.py`) looks for a successful Verify run
of a same-repository branch whose commit has a byte-identical Git tree and
whose `frontend`, `browser` and `backend` jobs passed. If it finds one, those
jobs are **skipped**, not marked as passed, and the run summary links the run
that tested the tree. `containers` and `publish` always run, so the images are
built, smoke-tested and labelled for the `main` commit itself. If the tree
differs in any byte (for example because another change was merged in between),
or the lookup fails, every test runs as before.

The deployment broker does not trust the `reuse` job. When the main run's test
jobs were skipped, it searches GitHub itself for a successful same-repository
Verify run of the identical tree and checks that its three test jobs passed on
their approved runners; without one it refuses the deployment. A commit
inherits a result only through an identical tree, never from an older or
different commit.

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

If nothing depends on the run, mark it **PENDING** and continue with
independent work on a separate branch/worktree. If the next step depends on it,
wait with `scripts/ci/wait-for-run.sh <run-id>`: it reads only the run status
every 30 seconds, returns as soon as the run finishes, and gives up after 45
minutes. Do not use `gh run watch` or a fixed `sleep`.

A merge is allowed only after the exact approved commit has successful required
checks and a completed review gate. A newer commit never inherits an older
commit's approval or CI result, except the test result of a byte-identical tree
(see above). If a dependent merge is waiting for CI and no
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
backend and containers remain serialized on the single self-hosted worker.
