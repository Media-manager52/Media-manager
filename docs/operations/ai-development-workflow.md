# AI-assisted development workflow

How one owner runs several AI coding agents (Claude, Codex, OpenCode/DeepSeek,
Grok) on this repository without losing work or blurring who approved what.
It adds the *task flow*; it does not replace [`AGENTS.md`](../../AGENTS.md)
(agent rules, verification gates, the non-blocking CI policy) or
[`CONTRIBUTING.md`](../../CONTRIBUTING.md) (safety boundaries, checks). If they
disagree, those two win.

## 1. The one rule

**One task = one GitHub issue = one branch = one worktree = one pull request.**

The issue is the source of truth for *what* and *why*. The PR is the source of
truth for *evidence*. A T3 Code thread is a scratchpad that points at both; it
is never the record.

## 2. Task lifecycle

| Stage | GitHub signal | Moves it | Exit criteria |
| --- | --- | --- | --- |
| **Backlog** | Open issue, no `status:` label | Owner or any agent files it | Issue form filled: problem, acceptance criteria, safety/approvals, evidence required |
| **Ready** | Label `status: ready` | Owner | Scope agreed; approvals the task needs (live search, migration, deploy) are named in the issue |
| **In progress** | Label `status: in progress` | The Build thread | Branch, worktree and one Build thread exist; branch name is commented on the issue |
| **Review** | Open, non-draft PR containing `Closes #N`; drop `status: in progress` | Build thread opens the PR | Independent Review thread (a *different* model than the builder) has posted a verdict; required checks pass on the exact head SHA. Find these with `is:issue is:open linked:pr` |
| **Done** | PR merged, issue auto-closed | **Owner only** | Cleanup (section 8) completed. Production deployment is tracked separately |

Move an issue backwards (and say why in a comment) rather than leaving a stale
label. A GitHub Projects board is not configured; if the owner adds one
(`gh auth refresh -s project` is required first), map its columns to the table
above and delete the two `status:` labels so state lives in one place.

## 3. Naming

| Thing | Convention | Example |
| --- | --- | --- |
| Issue | Imperative outcome, no agent or model names | `Reject stale quality-profile edits` |
| Branch | `<type>/<issue>-<slug>`; types: `feat` `fix` `docs` `ci` `test` `perf` `chore` `release`. No agent prefix (`ai/`, `claude/`) - the thread records who did it | `fix/123-stale-profile-edit` |
| Worktree | Created by T3 Code (or `git worktree add`) under `~/.t3/worktrees/`; never hand-copied directories | |
| Commit | Conventional prefix, one commit per passing milestone | `fix(releases): reject stale profile versions` |
| PR title | Same as the final commit subject; body starts from the PR template | |
| Thread | `<Role>: <subject> (#issue or PR #n)`; roles `Build` `Review` `Ops` `Scratch`; append `superseded` only when replaced by another thread | `Review: Release Intelligence 2.0 (PR #94)` |

Existing `ai/*`, `claude/*` and other historical branches keep their names.

## 4. Workspace layout (T3 Code on the VPS)

- The canonical checkout (`~/work/media-manager-t3`, T3 project
  **Media Manager — Development**) stays on `main`, clean, fast-forward only.
  No agent commits feature work there.
- New threads default to **worktree** mode, branching from `origin` (T3
  preferences `defaultThreadEnvMode: worktree`, `newWorktreesStartFromOrigin:
  true`). A new task gets a new worktree even when an old branch is still open.
- Do not point a second thread at a worktree another thread owns. Reviewers
  read the PR head from their own worktree or with `gh pr diff`/`gh pr view`;
  they never switch the branch under a builder.
- The older `~/work/media-manager` checkout and its T3 project are legacy
  history. Do not start new work there.
- Pause an idle thread instead of deleting it. *Settle* a thread only when its
  outcome is verified (section 8).

## 5. Start every thread with an identity check

Before editing or reviewing, report `pwd`, `git branch --show-current`,
`git rev-parse HEAD` and `git status --short`, and compare them with the task's
expected branch and commit. On a mismatch, **stop and say so**; do not review
or build on whatever is checked out. (A review once ran in the wrong checkout
and correctly refused to issue a verdict.)

Handoff and continuation prompts must name the expected branch, the expected
HEAD, which commits are final and must not be amended, and which files are
intentionally uncommitted. Unrelated uncommitted work is preserved, never
reverted.

## 6. Agent responsibilities

The owner assigns *roles* per task; models are interchangeable unless noted.

| Role | Typical agent | Does | Must not |
| --- | --- | --- | --- |
| **Build** | Claude or Codex for domain/safety-sensitive code; OpenCode/DeepSeek for tightly specified CI, script and docs tasks | Implements the issue in its own worktree, adds focused tests, runs the AGENTS.md gates, commits, opens the PR with evidence | Merge, deploy, run live searches/downloads/publication/Plex refreshes, or widen scope past the issue |
| **Review** | Grok or Codex (not the builder's model) | Reads the PR head in its own worktree, reruns what it can, and posts `APPROVE`, `CHANGES REQUESTED` or `BLOCKED` with the head SHA, the findings, and which tests it did and did not run | Edit the branch under review, approve a SHA it did not inspect, or claim unrun checks passed |
| **Ops** | Claude | Workspace, GitHub labels/templates, docs, cleanup *proposals* | Delete or archive anything with unresolved work without owner approval |
| **Owner** | You | Approves Ready, merge and deploy; decides performance or product trade-offs | |

OpenCode/DeepSeek sessions have reported hitting step limits and a
configuration policy blocking `gh` and `git push`. Treat their output as
local-branch work: have a Claude/Codex thread or the owner push and open the PR.

## 7. Develop, review, CI, merge

1. **Develop** - run the gates in `AGENTS.md` for the files you touched. Commit
   only what passes. Do not amend a commit once it is pushed or under review;
   add a new commit.
2. **PR** - fill the PR template. Link the issue with `Closes #N`.
3. **CI** - follow the non-blocking policy in `AGENTS.md`: check once, record the
   exact run ID, URL and commit SHA, mark **PENDING** if unfinished, and work on
   something else. Never poll. A new push invalidates earlier results. See
   [CI](ci.md) and [GitHub VPS operations](github-vps.md).
4. **Review** - the Review thread's verdict names the head SHA it inspected.
5. **Merge** - only the owner approves, by naming the PR and head SHA, after
   required checks pass on that exact SHA. If CI is unfinished, report
   **WAITING FOR CI**; do not merge.
6. **Deploy** - separate decision (section 9).

## 8. Evidence, acceptance, and safe cleanup

**Acceptance criteria** live in the issue as a checklist the PR can tick off.
**Evidence** lives in the PR: command, result and commit SHA for each claim, and
every item labelled *synthetic/local* or *live*. Never describe synthetic
evidence as live verification, and list what was **not** run.

**Cleanup** happens after the owner merges, and only when all of these hold:

- the PR is merged (`gh pr view N --json state,mergeCommit`) and the issue closed;
- the worktree is clean (`git -C <worktree> status --porcelain` is empty) and
  nothing is unpushed (`git -C <worktree> log @{u}..` is empty);
- no other open PR or thread depends on the branch.

Then: settle the Build and Review threads; remove the worktree with
`git worktree remove <path>` (no `--force`); delete the branch with
`git branch -d` (not `-D`). If any check fails, stop and ask the owner.
Never `rm -rf` a worktree, `git clean`, force-remove, rewrite history, or delete
backups and bundles. Dirty or unpushed work in an old checkout is reported, not
cleaned.

## 9. Approvals are separate

| Action | Approved by | Not implied by |
| --- | --- | --- |
| Start a task (Ready) | Owner | Issue creation |
| Merge a PR | Owner, naming PR + head SHA, after green required checks | A passing review or CI |
| Production deployment | Owner, running **Actions → Deploy** per [GitHub VPS operations](github-vps.md) | A merge, a green `main`, or published images |
| Live searches, downloads, publication, Plex refreshes, migrations, destructive operations | Owner, per task, in the issue | Anything above |

## 10. GitHub setup in use

- Labels: reuse `bug`, `enhancement`, `documentation`; add only `status: ready`
  and `status: in progress`.
- [`.github/ISSUE_TEMPLATE/task.yml`](../../.github/ISSUE_TEMPLATE/task.yml)
  captures problem, acceptance criteria, safety/approvals and required
  evidence. Security reports go through [`SECURITY.md`](../../SECURITY.md).
- [`.github/pull_request_template.md`](../../.github/pull_request_template.md)
  captures the evidence block described in section 8.
