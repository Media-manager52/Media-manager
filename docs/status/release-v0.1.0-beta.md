# Media Manager v0.1.0 Beta readiness

Status: **BLOCKED — not release-ready and not published.**

This is the release-candidate evidence ledger, not a claim of completion. It is
based on source at `main` merge `c82d44ab08afe24b34d3f8c4105d61d22e45a5ec`
(30 October 2026). Production remains on `31789f7`/schema 49 and was not part of
the release work.

## Completed and merged

| Work | Merge commit |
| --- | --- |
| Dependency security (PR #89) | `6c414b10cda9f4bc376a6f5946d01d579c75cda8` |
| UI pagination (PR #87) | `c53fd0edacbd16bdea4b364b08bfb979534c9265` |
| Cosmos installation volumes (PR #88) | `39a250cd6d3bf6f09fc3b058ad60780c92b4c293` |
| Partial search retry, schema 50 in source (PR #86) | `c82d44ab08afe24b34d3f8c4105d61d22e45a5ec` |

Main CI for each merge passed, including image publication. The immutable image
digests are recorded in [versioning](../releases/versioning.md).

## Open release-relevant PRs

| PR | Status | Remaining blocker |
| --- | --- | --- |
| #90 Cosmos route host/auth | Open; CI green; independent security review APPROVE | **Owner merge authorization**; live Cosmos wizard/FUSE acceptance still manual |
| #91 CI parallelization/non-blocking policy | Open; CI on final HEAD pending at last check | **Owner merge authorization**, final CI result, and independent re-review of the final policy/docs commit |

Do not merge these from this work. They require the grouped owner approval below.

## Security

- Dependency audit on source: `npm audit` and `npm audit --omit=dev` report zero
  vulnerabilities; Dependabot open alerts were zero after the merge.
- The published UI image contains the patched next/zod/vite/sharp/source-map-js/
  undici/fast-uri versions; dev/build tooling is still present in the image.
- PR #90 is not merged. Until it is, the installed API/health routes can match an
  unintended Cosmos host and `AdminOnly:false` permits any signed-in Cosmos user
  to reach the owner token. This is a **release blocker** for public edge use.
- No production secrets or private VPS configuration are in this release work.

## Installation and onboarding

- **PASS (disposable):** `compose.release.yaml` with the published
  `sha-c82d44a...` API/UI images started against a fresh PostgreSQL, rejected an
  API call without the owner token, accepted it with the token, served the UI,
  showed AltMount's parent mount read-only, allowed writes to Movies/TV, and
  preserved a database marker after a PostgreSQL/API/UI restart.
- **PASS (disposable):** an encrypted backup was created through the API,
  verified in an isolated database, downloaded with an authenticated request,
  and the new `media-manager backup-decrypt` command produced a valid custom
  dump. The backup artifacts and test stack were removed.
- **NOT VERIFIED:** installing on a clean public host and restoring that backup
  into a fresh stack with production-like external services.
- **NOT VERIFIED:** actual Cosmos 0.23.4 wizard installation and a real AltMount
  FUSE child remount. The manual acceptance steps are in
  `deploy/cosmos/VERIFICATION.md`.
- **PARTIAL:** first-run configuration uses the existing Connections, Storage,
  Profiles and Add media screens plus a documented checklist. There is no
  automated clean-owner UI walkthrough; the Add media screen correctly names
  missing prerequisites and links to setup.

## End-to-end acceptance

- **PASS:** the full Go race suite, including acquisition, publication,
  retrieval, AltMount/Plex-facing unit and integration tests, ran in isolated
  PostgreSQL groups with all 247 PostgreSQL tests passing.
- **PASS:** the UI pagination/owner-attention browser suite passed 166 cases in
  the merged source; the release image use is covered by the disposable Compose
  check above.
- **NOT VERIFIED:** one complete real-system journey
  `request → search → verify → AltMount output → Plex confirmation → publication`
  against a disposable real AltMount/Plex/Seerr stack. The existing evidence is
  component/isolated integration, not a claim of a live end-to-end run.
- The real external-service gate remains manual and must be recorded before a
  public release is described as fully tested.

## Backup, restore, migration

- **PASS (disposable):** encrypted backup creation, isolated restore
  verification, authenticated download, and `backup-decrypt` were exercised in
  the release image stack; the decrypt command is covered by a Go test.
- **NOT VERIFIED:** restoring into a fresh production-like stack and off-site
  copy/restore using the owner's real S3/WebDAV destination; no credentials or
  destination were supplied. Local backups are not sufficient disaster recovery.
- **PLAN ONLY:** schema 49→50 is not applied in production. The exact deployment,
  backup, rollback, and post-checks are in
  [schema-50-deployment](../operations/schema-50-deployment.md). Schema 50 images
  must not run against schema 49, and schema 49 images must not run against a
  migrated schema 50 database.

## Release engineering

- **PASS:** immutable `sha-<commit>` image publication exists and the digests are
  recorded.
- **FAIL:** no `LICENSE`; `deploy/cosmos/README.md` still says “private / all
  rights reserved.” Public beta cannot ship with contradictory terms. See the
  [license decision](../status/license-decision.md).
- **FAIL:** no v0.1.0 tag/release, no release changelog publication, and only
  `linux/amd64` images. ARM64 may follow beta if amd64 is documented.
- **PARTIAL:** the release image install path and versioning contract are
  documented, but no release candidate has been tagged or published.
- **PASS:** source and images have been built/tested on the supported amd64
  platform; no production deployment was performed.

## Release checklist

| Requirement | Result |
| --- | --- |
| Critical/high dependency work complete | PASS on source; merge blocker for public exposure |
| API authentication/owner authorization | FAIL until PR #90 is merged |
| No secrets in release | PASS (no secrets printed or committed) |
| Clean install from immutable images | PASS in disposable stack; clean-host repeat NOT VERIFIED |
| Cosmos wizard and FUSE remount | NOT VERIFIED |
| First-run owner onboarding | PARTIAL (existing screens + docs; no automated walkthrough) |
| Workflows/Wanted/Files/Requests usable | PASS in browser/unit coverage; release image UI served |
| Request → search → verify → publish | PARTIAL (component/integration; real-system E2E NOT VERIFIED) |
| Restart/persistence | PASS in disposable Compose stack |
| Backup/restore | PARTIAL: encrypted create/verify/decrypt PASS in disposable stack; fresh-stack restore and off-site NOT VERIFIED |
| Schema 50 production deployment | NOT APPLIED (plan prepared) |
| License/public-distribution wording | FAIL (owner decision required) |
| Release tag/version/architecture | NOT PUBLISHED (owner decision required) |

## Required before release

1. Merge PR #90 after owner approval and repeat a disposable Cross-host/non-admin
   route check.
2. Merge or explicitly defer PR #91; do not merge it with pending final checks.
3. Choose a license and correct all distribution wording.
4. Configure an off-site backup destination and exercise a restore.
5. Run the real or faithfully simulated end-to-end acceptance journey and record
   it as real-system or mocked.
6. Obtain owner approval for a schema-50 deployment window if production is to
   move; this is separate from the public beta release.
7. Create the release candidate/tag and publish images only after explicit owner
   authorization.

## Post-beta backlog

- ARM64 images.
- Remove unused `zod`/resolver dependencies and prune dev tooling from the
  runtime image after verifying vinext starts.
- Keyset pagination/covering indexes for very high-churn libraries.
- Server-side filters for Workflows/Wanted/Requests and strict `/requests` limit
  validation.
- Replace the per-route Wanted context fallback with a bounded batch endpoint.
- Off-site backup automation beyond the documented owner-configured copy.
