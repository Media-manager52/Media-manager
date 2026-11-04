# Versioning and image identity

This document defines the contract every Media Manager release follows. The
first release, `v0.1.0-beta.1`, was published on 2026-11-04; see
[`v0.1.0-beta.1.md`](v0.1.0-beta.1.md) and its
[GitHub release](https://github.com/Media-manager52/Media-manager/releases/tag/v0.1.0-beta.1).

## Versions

- Product version: semantic version `0.1.0-beta.N` (pre-release).
- Git tag: `v0.1.0-beta.N`.
- `MEDIA_MANAGER_VERSION` embeds the same value in the API binary and image
  labels; `MEDIA_MANAGER_COMMIT` embeds the exact Git commit. The automated
  publish workflow labels image builds `github-<sha12>` for traceability; a
  release installation sets the product version explicitly.
- The compose project remains `media-manager`; container names remain
  `media-manager-api-1`, `media-manager-ui-1`, and `media-manager-postgres-1`.
- Database schema is forward-only. A release document must state its required
  schema and the previous compatible image digest.

## Images

Published images are immutable by commit:

```text
ghcr.io/media-manager52/media-manager-api:sha-<40-char-commit>
ghcr.io/media-manager52/media-manager-ui:sha-<40-char-commit>
```

The publish job also maintains `main` and `latest`, but release installations
must use the `sha-...` tag or a registry digest, never `main`/`latest`. The
release record must include both index digests and both platform manifest
digests.

## License

The product is `AGPL-3.0-only`. Every published image contains the unmodified
[`LICENSE`](../../LICENSE) and [`THIRD_PARTY_NOTICES.md`](../../THIRD_PARTY_NOTICES.md)
under `/usr/share/doc/media-manager/`, and carries the
`org.opencontainers.image.licenses="AGPL-3.0-only"` label. The corresponding
source is <https://github.com/Media-manager52/Media-manager>. Release checks must
verify the label and both files in the published image.

Released images (`v0.1.0-beta.1`, commit `0041902eb32e1a5d3aa1d983fe61329e7c5c487c`):

```text
api index         sha256:971d073a1d5083745ffd495ea12acda2c4293d16e584f6dd5769cc5e743535a5
api linux/amd64   sha256:9b60c4e2870e9c857ee86463b5e1a2931f36c0653a12ad964fc3c673cc582a94
ui  index         sha256:6051f4ff129dc276f3530360446a1d79270b4ff33c9f4a768a2300274efe78d2
ui  linux/amd64   sha256:41ddf532a57df8c25efbc76d8786408851d890dcd0f20645c8d57fe289c62837
```

The earlier unreleased baseline (main merge `c82d44ab08afe24b34d3f8c4105d61d22e45a5ec`)
used api index `sha256:941690e3980a2b3809585fee396c793aeffcb41d7171942f8083283465e6bf4b`
and ui index `sha256:f9c6fe02f4a287c05b79b38727ad27568768bd61e02430d48df5e209b9f1eb22`.

## Supported platform for beta

- Linux `amd64` only.
- Docker Engine with Compose v2.24 or later (`include` and `!reset` are used by
  `compose.release.yaml`).
- PostgreSQL 18.6 Alpine is the tested database image.
- ARM64 is explicitly out of scope for v0.1.0 Beta.

## Upgrade and rollback

1. Record the current image digests and schema.
2. Create and verify an encrypted backup; retain its encryption key separately.
3. Pull the next immutable image pair and recreate only API/UI, or use the
   verified deployment broker for production.
4. Migrations run transactionally at API startup. Wait for `/health/ready` with
   the expected schema.
5. Rollback to the previous image requires restoring the pre-upgrade database
   backup into a fresh database; schema-49 images must not run against a schema-50
   database.
