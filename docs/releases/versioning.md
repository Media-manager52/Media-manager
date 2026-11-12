# Versioning and image identity

This document defines the contract every Media Manager release follows. The
current release, `v0.1.0-beta.19`, was published on 2026-11-12; see
[`v0.1.0-beta.19.md`](v0.1.0-beta.19.md) and its
[GitHub release](https://github.com/Media-manager52/Media-manager/releases/tag/v0.1.0-beta.19).
Earlier releases: [`v0.1.0-beta.11`](v0.1.0-beta.11.md), [`v0.1.0-beta.10`](v0.1.0-beta.10.md), [`v0.1.0-beta.9`](v0.1.0-beta.9.md), [`v0.1.0-beta.8`](v0.1.0-beta.8.md), [`v0.1.0-beta.7`](v0.1.0-beta.7.md), [`v0.1.0-beta.6`](v0.1.0-beta.6.md), [`v0.1.0-beta.5`](v0.1.0-beta.5.md), [`v0.1.0-beta.4`](v0.1.0-beta.4.md), [`v0.1.0-beta.3`](v0.1.0-beta.3.md), [`v0.1.0-beta.2`](v0.1.0-beta.2.md) and
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

Released images (`v0.1.0-beta.19`, commit `bc812957fd8e236303145341670e6a909c39cb84`):

```text
api index         sha256:f36d65708a9c7fb9ada6dd272711800ffd80f8ce60172176ec9610911d1bd957
api linux/amd64   sha256:7dea56abaeff183ae255530c2a23b335f5432b56a32e6c704e7362683ba47762
ui  index         sha256:9f55b999a64cfada2f1d76a37e837243a3baa50c3693f74185af42883ccede37
ui  linux/amd64   sha256:3239a07f5022774b4ba4a03ea78b317e44b008b04bfe6f6b4d4c819ce2baf11c
```

Released images (`v0.1.0-beta.18`, commit `a361ef97560058dfe29e519ca5f32cfaa7f96efd`):

```text
api index         sha256:1106ad263d4ad60123388910bcd259d6e426432ba9c615f21fb358b163d96ccc
api linux/amd64   sha256:56221b0d52d2008588767474ff1cb2a98aea6223b100ba7130b5cafaa05176f9
ui  index         sha256:3c44b96cb11c5ca049aa3cdcd0b4d5b5ad18eb4cdfd638ed1edeff3451a8a725
ui  linux/amd64   sha256:96d25013cff8ba1eda0ebcda6187a11caaea126aaf800b1c669be1b4df82d703
```

Released images (`v0.1.0-beta.17`, commit `d89572098d0e1d1b1e42fa0ec4c8422922ad5853`):

```text
api index         sha256:baa41de363b6db62c9e0491e23e8b6b81e3b5e274858764f60b2b240d5afdf99
api linux/amd64   sha256:822b8c82da2a9d04ca88a40ba8e7f5d124297a7cb31db6d6627b8fa1ec7931e7
ui  index         sha256:470374296eeb239614966b83f206af0e5d709ba4262f1634d93cb53a90dd1fbe
ui  linux/amd64   sha256:217f5fa7e398af5a13e730a6fde6148f41a3341ab10b1dd99b4f56a97ddc73b3
```

Released images (`v0.1.0-beta.16`, commit `59c568653f00d7f61c73a9bb8083fa348bd3f534`):

```text
api index         sha256:3dba1e502236868fe570be75c16205be501e660b98050b585e34197c1f3fecae
api linux/amd64   sha256:1af4a1053a5e0111fac9a54a3e0f8a452a329fbaeec2eb044cf8051851c2bb58
ui  index         sha256:72dd6623cddf38f9c68984805f42188d4b91efbc7a48bb112715c6209567186a
ui  linux/amd64   sha256:02d23ea0047256547c7c1eaa83e8422fddbc1f912d81bd94774e92ea3425f5fa
```

Released images (`v0.1.0-beta.15`, commit `f97c60a445025a5d8e9e66b812ff951d29f5912d`):

```text
api index         sha256:bb730cbfff87b15045564b7aba646e97b8407c17a3e9f4998c2c92e1c59604f6
api linux/amd64   sha256:60beb9915e6a6a461d5ce54903b0dbb8bd2f3fb9b1650dd67896ffb46320ffed
ui  index         sha256:f35f1ce7aa6d97c9635774653b0479ed79650a2d8307e8e27a89242c34f08aef
ui  linux/amd64   sha256:5df15be8b1e150957ff26653e89f500aa501dcc797aebbc0c9292de5c8313cef
```

Released images (`v0.1.0-beta.14`, commit `7d1ad1b2d3622509e49fea2df8481553611ddba6`):

```text
api index         sha256:1ea1778f0850536be1a711b5d2eb728bda5b364f0a2b8b495e0b4fbc6fd4402d
api linux/amd64   sha256:785f8c5b6123691840b41c7d373c67a0273d58042e31ff12f742decde81fbca4
ui  index         sha256:67c8ce1869c259a620befc8cfb063c50e0e2a65ad8e970d44391956ab678a537
ui  linux/amd64   sha256:ea087eb5e6404ac7206e4814a0d90f2555b09326b8b551258f7f13718740fdd3
```

Released images (`v0.1.0-beta.13`, commit `7d32aa68895b4264ba2280a57d6ac55d1105b8cc`):

```text
api index         sha256:8fffd2eff84d816f2c90b2fe43a87d29be40dcd832ee7b0908898874e31d7c2a
api linux/amd64   sha256:eb7ec9f2d155f3cec7d766bb2aac69f379379f1416161f5cef8f7dd80f0ce329
ui  index         sha256:1f58b59e41f6a9b8e065f3b2174be26cc5d919cedc2cf9faf87114a99545af8d
ui  linux/amd64   sha256:5417bf474e27a2fab84a2a13547666f24025c5479b30205f7b75a13db042de37
```

Released images (`v0.1.0-beta.12`, commit `9aa1ea72f687a007373ec8a87eb4a3fad40f26e8`):

```text
api index         sha256:f5ce93b7ec2d609b5ddeb4ce49fc3a237a6a45748735598df3ec2d7e9e9e3d97
api linux/amd64   sha256:46fd463bfd7f27b829e75624632ffd5d8a2bc2bd136e9d1d1f9c722848b630ba
ui  index         sha256:d7a7b2749f6b6ffad4f560ef17efe2d0fb343ddd12ae8cfcee7bf775d38e09ba
ui  linux/amd64   sha256:38279d257b1ef6a9109c5f1c056f02cbc48aa0a630c21967534f9e23c0a8c389
```

Released images (`v0.1.0-beta.11`, commit `b7a0c69b1886459c7c31c59c573bd48ebbb524a0`):

```text
api index         sha256:9d160b24d52a1be786dc6a910295423ef66a556f55eebc05d518e79e9fc483e5
api linux/amd64   sha256:f6ff7014039d69c6e83854207bdab91917b90d8a182573c65ded163d35a5d03f
ui  index         sha256:d632cc55a2a40b6dbd2135b53672c2c7fc75427bdbfdff4c1442b2985f67288a
ui  linux/amd64   sha256:638be9be2e81e6c62557751472337851bd5de13e66cbe2fe2086798d70431b77
```

Released images (`v0.1.0-beta.10`, commit `03e1c1a4cfe59937a48312354c4eb3587a094b11`):

```text
api index         sha256:65e7baa952ceefbb9195526302cbba6e4b92aacb083e2b1fc7dc83c15f3fdeb0
api linux/amd64   sha256:945a0954a5d930dd2943b4a51c2450c60f68f561683917f4db408da014e6cae2
ui  index         sha256:cafe4da286a9cfa3345a55a79bacbc9cf8b9e7641c9410cfeaaaa647b1f12c2b
ui  linux/amd64   sha256:16c99dace804ebccec2fc506b3f740bf032b7cc0a9ed8a0fd746cb8a00ab6311
```

Released images (`v0.1.0-beta.9`, commit `9160aae85642ccdbfc7aaad90722b0539bd7289d`):

```text
api index         sha256:9673a1856fe4f937db30e86e7b5c80afa3fcf73ea1ae32e0fdad790480691f91
api linux/amd64   sha256:d04474940f90eb4ecc20b7b23a72ac6192063eb8d3071a69394de30cb9c024f3
ui  index         sha256:ebf32c62d956f52af994cd24b26c4849ba324f730b720e4ff73fae02f0c04854
ui  linux/amd64   sha256:72abc0209ac5bf5263fda2d2278e6dedb4646d81bcff23e4e909726835d10e39
```

Released images (`v0.1.0-beta.8`, commit `94010c112bf288a6532341cb36c36a23aa5fc4e4`):

```text
api index         sha256:8f635c43bc77adfa2f72a7f83026317111060dbbbc8d586830c64c5aeb10f466
api linux/amd64   sha256:1b616b1781c9319d3014af64ccf9ac1d0a12634d7e7eca96eed768c2b1e097b2
ui  index         sha256:b70000dd80b28f9dcac464eec3dfc4d1acad902e0c091249f93951e34b750410
ui  linux/amd64   sha256:97a19edb8a844ece0dc29d56909f9ec8c8eb790b343f8979783a776f329d5e26
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
