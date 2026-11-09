# Versioning and image identity

This document defines the contract every Media Manager release follows. The
current release, `v0.1.0-beta.6`, was published on 2026-11-10; see
[`v0.1.0-beta.6.md`](v0.1.0-beta.6.md) and its
[GitHub release](https://github.com/Media-manager52/Media-manager/releases/tag/v0.1.0-beta.6).
Earlier releases: [`v0.1.0-beta.5`](v0.1.0-beta.5.md), [`v0.1.0-beta.4`](v0.1.0-beta.4.md), [`v0.1.0-beta.3`](v0.1.0-beta.3.md), [`v0.1.0-beta.2`](v0.1.0-beta.2.md) and
[`v0.1.0-beta.1`](v0.1.0-beta.1.md).

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

Released images (`v0.1.0-beta.6`, commit `5f1a890563740e579a9b61c4ddbb6a1e8a11f91e`):

```text
api index         sha256:e9adaf3ffd09926a7db52da6850cd4fba1b075965723d6593f2c5c392ca51eaa
api linux/amd64   sha256:a7505bcf89d42c38f4e7d0a231332446d0287f5e68ab99827eafc000b0c6fc7d
ui  index         sha256:440a703e1d188b58a62e057be7f6c22bec0fa088fbbc6b04c5fcaeb1cfae870e
ui  linux/amd64   sha256:8c1daa6f08292cea92ca09d74d7606f33b4c8bbe85a170a4b7143d889cfa4d5f
```

Released images (`v0.1.0-beta.5`, commit `db81e67986b15a4886db4ea8f6e027422bd1f439`):

```text
api index         sha256:452276a24987b1bdea8bfefe35c68797260807c2b016efa5d2834416228b7070
api linux/amd64   sha256:ee4f8f624cf059ef7b3155a472b95fd094fba26c9edb9b57f00e6a7f99cc8af7
ui  index         sha256:3378a32013163276122955fbe7ed6759d32fd735161fdea5934bd66d3a3ae9e8
ui  linux/amd64   sha256:3364fa8a56381407aad8e431e4e461268c7f5411aee0897f82aa90a8af2f8a7d
```

Released images (`v0.1.0-beta.4`, commit `f928173a21e9045016783f56820e1a6fd06fbc81`):

```text
api index         sha256:08e74b5096d62e1fe37f8bc373a85315886b61f351fa1263b25de02565ac7043
api linux/amd64   sha256:1c2c5ab8a23bf5893bd3776653e2901d914636b700418fd831f24cc7033a0c00
ui  index         sha256:d2bf1b855f2aad923e8859849b198ebe9a582e19dbba94c8fadf43dca2c38163
ui  linux/amd64   sha256:6841a0230556090e6a397b805a158e94c7ba160e8636af6ca32ea25ec55f59b0
```

Released images (`v0.1.0-beta.3`, commit `c11b8997c7cf40bc5fdb4518fb1dc75a542b80a7`):

```text
api index         sha256:15cc6381bcf68a0244ed40acb34d709266521b5987e2da0980ffb2407b685f94
api linux/amd64   sha256:19935042fb3022dd26240b0e736d072cce6810019c6cf3416db64699288cc8a4
ui  index         sha256:0075d0d9bc5f06909b8b907025eeb2d187a0d27db20c975e830e467cdd1a64f8
ui  linux/amd64   sha256:441c01a5c14147921895724ce7f25a135e82355505335d1ded9c906653f09569
```

Released images (`v0.1.0-beta.2`, commit `bb24c654a6c881a896f863bf24a88962312d5aec`):

```text
api index         sha256:ed9e3685149bbc15cdf26907bacac1bbb81ae04a07df47a326ce24e69c515d49
api linux/amd64   sha256:5b87d38ae546e88168f33fdff2ebf351c59ec6afa4232433cd4392595efe8cb3
ui  index         sha256:ef131754a741a80edc311d823495b6830d273062c975cf6489811f9d80f7136d
ui  linux/amd64   sha256:35ed881fdbe227098916ce10a21b857acc28878129df2d2bd8e93424d7a4cd1a
```

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
