# Versioning and image identity

Media Manager v0.1.0 Beta has not been tagged or published. This document defines
the contract to use when the owner approves a release; it does not create a tag.

## Versions

- Product version: semantic version `0.1.0-beta.N` (pre-release).
- Git tag: `v0.1.0-beta.N`.
- `MEDIA_MANAGER_VERSION` embeds the same value in the API binary and image
  labels; `MEDIA_MANAGER_COMMIT` embeds the exact Git commit.
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

Current published baseline (main merge `c82d44ab08afe24b34d3f8c4105d61d22e45a5ec`,
from source, not a release tag):

```text
api index sha256:941690e3980a2b3809585fee396c793aeffcb41d7171942f8083283465e6bf4b
api linux/amd64 sha256:ebe64a2fe1ab5295f6dfbf49458dedf099ab8f579d3230ebb3813fe55fd31fdc
ui  index sha256:f9c6fe02f4a287c05b79b38727ad27568768bd61e02430d48df5e209b9f1eb22
ui  linux/amd64 sha256:0a4fb936ed3fe5584fa5b94c1bacd04a6d5fdac806ed453160621971dec39035
```

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
