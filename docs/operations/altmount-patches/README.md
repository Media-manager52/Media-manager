# Deployed AltMount source patches (2026-09-30)

This bundle preserves the three companion fixes deployed with Media Manager.
They apply to [kipsilabs/altmount](https://github.com/kipsilabs/altmount) at
`630cad9a845e88018dc521554d8e2f3bd86bc436`. The deployed source is
`321b3badc04760a387828bbde73e4bf15088508d`, built as
`altmount-local:321b3ba`. These patches are maintained here for reproducibility;
they are not represented as merged upstream.

| Order | Original source commit | Change and regression coverage |
| --- | --- | --- |
| [1](0001-fix-allow-bounded-NNTP-failover-during-release-probe.patch) | `0c3325b63b1f4b1c0e01a807f97a7e68c961cf45` | Allow provider fallback within the existing 15-second sweep budget; prove fallback beyond two seconds and enforce the total deadline. |
| [2](0002-fix-detect-gzip-NZBs-when-retry-staging-renames-them.patch) | `c724ec8f0a3183129430c0788809d7237499de28` | Recognize gzip content when retry staging renames `.nzb.gz` to `.nzb`; prove the reader returns XML rather than compressed bytes. |
| [3](0003-fix-parser-retain-single-file-metadata-names-without.patch) | `321b3badc04760a387828bbde73e4bf15088508d` | Retain the original single-file metadata name when no extension override is supplied; test both retained names and explicit overrides. |

Each source milestone passed the AltMount backend race suite and vet before
deployment. Archival verification applies all three patches to the pinned base
and compares the resulting complete Git tree with the deployed source tree:
`e3eda74c645d8689a902b19ed20adb66c5768fdd`.
Commit identities after `git am` may differ because committer timestamps differ;
the tree identity verifies the source content.

## Reproduce in a disposable checkout

Verify the [patch checksums](SHA256SUMS) from this directory with
`sha256sum -c SHA256SUMS`. Clone AltMount separately, check out the pinned base,
then apply the three patches in order:

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
