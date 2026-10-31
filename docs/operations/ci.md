# Verification CI

`Verify` is triggered by every same-repository push and by manual dispatch. It
deliberately has **no `pull_request` trigger**, so fork code never executes on
the owner's self-hosted runner. Fork PRs are reviewed by the owner and validated
by a trusted push after review.

## Runner layout

| Job | Runner | Why |
| --- | --- | --- |
| `frontend` | GitHub-hosted `ubuntu-24.04` | No production secrets; runs lint, typecheck, docs checks, dependency audits and unit tests. GitHub-hosted capacity runs it in parallel with the self-hosted work. |
| `browser-shard (1/2)`, `browser-shard (2/2)` | GitHub-hosted `ubuntu-24.04`, pinned Playwright container | No production secrets; two independent jobs, each running its half of the suite with two Playwright workers. The image remains pinned to `mcr.microsoft.com/playwright:v1.62.1-noble`. |
| `browser` | GitHub-hosted `ubuntu-24.04` | Aggregate that fails unless every shard passed; it keeps the required job name `browser` stable for branch protection and the deployment broker. |
| `backend` | self-hosted `media-manager-vps` | PostgreSQL service, Go race suite in concurrent groups (see below). |
| `containers` | self-hosted `media-manager-vps` | Requires the owner's separate rootless Docker engine for the disposable stack smoke. |
| `publish` | self-hosted `media-manager-vps` | Needs the rootless Docker engine and registry credentials; runs only on `main` pushes after every required job succeeds. |

The self-hosted runner is still a single-job runner. Backend and containers are
therefore serialized on it. The hosted frontend/browser jobs no longer wait
behind them. Adding a second self-hosted runner is an owner/infrastructure
decision, not something this workflow can invent safely.

## Backend test groups

`backend` runs `scripts/ci/test-backend.sh`, which launches these groups
concurrently, each with `go test -race -count=1 -timeout 20m -json`:

- `postgres-N`: the top-level `internal/postgres` tests, split into
  `BACKEND_TEST_SHARDS` groups (1 to 8);
- `pkg-NAME`: every other package whose tests use
  `MEDIA_MANAGER_TEST_DATABASE_URL` (found by grepping the test files, so a
  new database-backed package is picked up automatically);
- `other-packages`: every remaining package.

Why the groups are fast and still safe:

- **One disposable database per group.** The schema migrator takes a
  database-wide advisory lock (`pg_advisory_lock`) for every test fixture, so
  groups that shared one database queued behind each other's migrations; under
  CPU pressure that convoy showed up as 30-120 s "context deadline exceeded"
  fixture timeouts. The script creates `mm_ci_<random>_<group>` databases on the
  same server and drops them on exit. Tests still create their own unique
  schemas inside them, and production migration code is untouched.
  `BACKEND_TEST_DATABASE_ISOLATION=shared` restores the old single-database
  behavior (and needs no `psql`).
- **Balanced groups.** `scripts/ci/postgres-test-weights.txt` lists the
  measured seconds of the ~20 slowest PostgreSQL tests; `backend_test_tools.py
  plan` spreads them across groups (longest first). A missing or stale weight
  only affects balance, never which tests run.
- **Nothing is skipped silently.** The run fails if any discovered PostgreSQL
  test reports no pass/fail/skip event, and it prints one timing line per
  group (`=== postgres-2: exit 0, 43 tests (...) in 61s ===`), the five slowest
  tests, and the job's CPU/memory use (see below).
- **A tuned, disposable PostgreSQL.** The service container is initialised with
  `POSTGRES_INITDB_ARGS` (`max_locks_per_transaction=256`, `max_connections=200`,
  `fsync=off`, `synchronous_commit=off`, `full_page_writes=off`). The default 64
  lock slots are exhausted by roughly seven concurrent `DROP SCHEMA ... CASCADE`
  calls (`out of shared memory`), which is what limited the old workflow to four
  groups. The database holds only throwaway test data.

Run it locally against any disposable PostgreSQL (needs `psql` and `python3`):

```sh
MEDIA_MANAGER_TEST_DATABASE_URL='postgres://user:pass@127.0.0.1:5432/postgres?sslmode=disable' \
  BACKEND_TEST_SHARDS=6 bash scripts/ci/test-backend.sh
```

The role needs `CREATEDB`; the URL's database is only used to issue
`CREATE/DROP DATABASE`.

## Resource ceiling

The CI account's cgroup (`user-997.slice`) is capped at **5 CPUs**
(`CPUQuota=500%`, `CPUWeight=25`) and **12 GiB** (`MemoryMax`, `TasksMax=4096`)
across its runner, rootless Docker engine and containers; production keeps the
rest of the 12-vCPU host. This is the real speed limit of the self-hosted jobs.
At the time of measurement the counters (read-only, from
`/sys/fs/cgroup/user.slice/user-997.slice`) showed `nr_throttled=6493` and
`throttled_usec=1,280,914,245` since boot and 7,287 `memory.events max` hits
with no OOM kills: the quota is occasionally throttled and the memory ceiling is
reached (mostly reclaimable page cache). Do not add large tmpfs mounts or
memory-hungry fixtures to CI jobs without checking `memory.peak`.

Every backend run now prints the quota, the CPU seconds it used, throttling and
memory high-water for that cgroup (`resources:` lines), so a change to the
ceiling is visible in the job log.

## Second VPS runner: investigated, not enabled

Running `backend` and `containers` at the same time needs a second self-hosted
runner. It was investigated but **not** enabled, because it cannot be done
safely from the repository alone:

- **The ceiling is shared, not doubled.** A second runner under the same
  account adds concurrency, not capacity: it shares the 5 CPUs / 12 GiB above.
  The container smoke is mostly waiting on cached image layers and health
  checks, so the saving is bounded by its own duration (see the measured
  results). Raising the ceiling is the owner's decision because it is
  production headroom.
- **Registration needs root and a registration token.** The existing runner
  lives in a root-managed directory that AI agents cannot read or change.
- **Isolation a second runner would need:** a separate OS account with its own
  rootless Docker engine (own socket and data root), its own systemd slice with
  explicit `CPUQuota`, `MemoryMax` and `TasksMax`, a distinct runner name and
  label (for example `media-manager-vps-containers`) so jobs cannot land on the
  wrong runner, no access to `/home/vps`, `.env*` files or the production Docker
  socket, and no registry or integration credentials.
- **Fixed host ports.** The backend PostgreSQL service publishes
  `127.0.0.1:35432` and the container smoke publishes the API on
  `127.0.0.1:38081` (hard-coded in `scripts/ci/container-smoke.py`). They differ,
  so a backend job and a smoke job do not collide, but two jobs of the same kind
  on one host would; rootless engines under different accounts still share the
  host loopback.
- **The deployment broker pins the runner name.**
  `deploy/github/deploy-broker.py` accepts a Verify run only if every
  `REQUIRED_JOBS` job (`frontend`, `browser`, `backend`, `containers`,
  `publish`) concluded `success` with `runner_name == "media-manager-vps"`. A job
  on a second runner fails that check, and so does any GitHub-hosted job (see
  the next section).

## Known conflict: deployment broker versus hosted frontend/browser jobs

`frontend` and `browser` already run on GitHub-hosted runners (their
`runner_name` is `GitHub Actions <number>`), but the broker's evidence check
still requires `media-manager-vps` for all five required jobs and
`docs/operations/github-vps.md` still says all five ran on the VPS runner. As
written, the broker would reject a current `main` run ("Required VPS checks did
not all succeed"), so a production deployment from it is expected to be refused.
This predates this change and is not altered here: the broker is a root-owned
production authorization boundary and the installed copy is outside the
repository. The owner should decide whether hosted `frontend`/`browser` evidence
is acceptable (and update the broker, its tests and the installed copy) or move
those jobs back. This workflow keeps the required job names unchanged
(`frontend`, `browser`, `backend`, `containers`, `publish`) so either decision
stays possible.

## Caches and reproducibility

- Frontend/browser use the npm cache keyed by `package-lock.json`.
- Backend does not restore a Go cache from GitHub: the persistent self-hosted
  runner keeps its own toolchain, module cache and build cache (`setup-go`
  `cache: false`), which avoided a ~900 MB download and extract per job.
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
backend and containers remain serialized on the single self-hosted worker.
