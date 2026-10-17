# Deployed AltMount source patches (2026-09-30, 2026-10-06, 2026-10-17)

This bundle preserves the eight companion fixes deployed with Media Manager.
They apply to [kipsilabs/altmount](https://github.com/kipsilabs/altmount) at
`630cad9a845e88018dc521554d8e2f3bd86bc436`. The deployed source is
`91ed7d7`, built as `altmount-local:91ed7d7` (previous builds
`altmount-local:73b7e0e`, patches 1–5, and `altmount-local:321b3ba`, patches 1–3). These patches are maintained here for reproducibility;
they are not represented as merged upstream.

| Order | Original source commit | Change and regression coverage |
| --- | --- | --- |
| [1](0001-fix-allow-bounded-NNTP-failover-during-release-probe.patch) | `0c3325b63b1f4b1c0e01a807f97a7e68c961cf45` | Allow provider fallback within the existing 15-second sweep budget; prove fallback beyond two seconds and enforce the total deadline. |
| [2](0002-fix-detect-gzip-NZBs-when-retry-staging-renames-them.patch) | `c724ec8f0a3183129430c0788809d7237499de28` | Recognize gzip content when retry staging renames `.nzb.gz` to `.nzb`; prove the reader returns XML rather than compressed bytes. |
| [3](0003-fix-parser-retain-single-file-metadata-names-without.patch) | `321b3badc04760a387828bbde73e4bf15088508d` | Retain the original single-file metadata name when no extension override is supplied; test both retained names and explicit overrides. |
| [4](0004-fix-rar-rename-a-reposted-first-volume-to-its-.rNN-r.patch) | `ba0322f` | When one head volume (`.rar`/`.part01.rar`) sits next to one contiguous `.r00..` run, rename the head to `<run base>.rar` so old-style volume following continues. Before this, such NZBgeek sets stopped after the first volume and failed as "truncated". |
| [5](0005-fix-rar-merge-a-clean-named-first-volume-with-its-sc.patch) | `73b7e0e3051f7935b129f2373515d0f7617cae79` | Merge a first volume carrying the clean release name with its scene-named `.r00..` run when the run is contiguous and the head has the run's volume size (within 1%); base comparison is case-insensitive. Before this, such sets failed as "incomplete". |

| [6](0006-fix-aes-make-Close-safe-while-a-Read-is-creating-or-.patch) | `6784d3a` | Guard the AES decrypt reader's source and closed state with a mutex that is never held across a blocking read. `readFullContext` force-closes readers from another goroutine on a FUSE read timeout; the unsynchronized field could be seen torn and `(*UsenetReader).Close` ran on a nil receiver, crashing the FUSE server on 2026-10-06. A source finishing initialization after Close is closed, not leaked. Both regression tests fail under `-race` before the change. |
| [7](0007-feat-fuse-optional-read-only-kernel-mount-fuse.read_.patch) | `7d4d25d` | Optional `fuse.read_only`: mount the FUSE filesystem read-only at the kernel level, so every view of it, including binds that follow a remount through `rslave` propagation, refuses writes, deletes and renames. Deployed with `read_only: true`. |
| [8](0008-fix-fuse-fail-only-the-request-when-a-FUSE-operation.patch) | `91ed7d7` | Read-side FUSE operations (lookup, readdir, getattr, statfs, open, read, release) recover a panic, log it with its stack and return EIO for that one request. go-fuse runs each request on its own goroutine without recovery, so any unknown bug in a read path used to kill the process and take the mount away from every consumer. The regression test crashes the test binary before the change. |

Patches 4 and 5 recovered 47 of 49 releases that had failed as truncated or
incomplete on 2026-10-06; the other two are real NNTP 430 gaps.

Each source milestone passed the AltMount backend race suite and vet before
deployment (on 2026-10-06 one unrelated, pre-existing `nzbfilesystem`
migration test failed once under full-suite load and passed on three reruns).
Patches 6–8 passed AltMount's full backend race suite (64 packages) and vet.
Archival verification applies all eight patches to the pinned base
and compares the resulting complete Git tree with the deployed source tree:
`7363b18bb9d6d887e8bf58d1820f71de7f1c0b23` (patches 1–7 give `9d6d4ea02eddce1a0fef13b29656c31a63765816`; patches 1–5 give `6bba6f7033b757ca822b010d063e25f09cd7c4dc`;
patches 1–3 alone give `e3eda74c645d8689a902b19ed20adb66c5768fdd`).
Commit identities after `git am` may differ because committer timestamps differ;
the tree identity verifies the source content.

## Reproduce in a disposable checkout

Verify the [patch checksums](SHA256SUMS) from this directory with
`sha256sum -c SHA256SUMS`. Clone AltMount separately, check out the pinned base,
then apply the eight patches in order:

```sh
git checkout --detach 630cad9a845e88018dc521554d8e2f3bd86bc436
git am /absolute/path/to/media-manager/docs/operations/altmount-patches/000*.patch
git rev-parse 'HEAD^{tree}'
```

The resulting tree must equal the value above. Validate and build this source
using AltMount's own contributor instructions and Debian CI Dockerfile before
any future deployment. The deployed image retained the original frontend build;
this bundle contains backend source changes only.

Protected persistent queue/upload mounts and their ownership are separate
runtime configuration, described in the [deployment evidence](../../status/release.md).
No credentials, production NZBs, database contents or media are included.
The upstream MIT copyright and license are preserved in [LICENSE](LICENSE).
