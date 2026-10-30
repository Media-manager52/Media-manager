# Install a released Media Manager image pair

This is the normal installation path for a release. It starts the published API
and UI images without compiling source. The `v0.1.0-beta.1` candidate is not
tagged or published yet; use the owner-approved immutable image references when
available.

## Prerequisites

- Linux host, `amd64`, Docker Engine, Docker Compose v2.24 or later.
- A `media-manager` source checkout (or at least `compose.yaml`,
  `compose.release.yaml`, `.env`, and `.env.integrations`).
- Movies/TV host directories, the AltMount parent directory, PostgreSQL storage,
  and a shared Docker network for AltMount/Plex/Seerr.
- Plex, AltMount, and at least one Newznab indexer for a working acquisition
  system. Seerr and metadata providers are optional.
- The software is licensed under `AGPL-3.0-only`; the image contains the license
  and third-party notices at `/usr/share/doc/media-manager/`. The corresponding
  source is <https://github.com/Media-manager52/Media-manager>.

## Install

1. Generate the environment with the existing setup helper. It creates `.env`,
   `.env.integrations`, the shared network, and the secrets:

   ```sh
   ./scripts/setup-cosmos.sh --no-start
   ```

2. Set the two immutable image references in `.env`:

   ```sh
   MEDIA_MANAGER_API_IMAGE=ghcr.io/media-manager52/media-manager-api@sha256:<index-digest>
   MEDIA_MANAGER_UI_IMAGE=ghcr.io/media-manager52/media-manager-ui@sha256:<index-digest>
   ```

   Do not use `:main` or `:latest` for a release installation.

3. Reject mutable tags and pull/start without a local build:

   ```sh
   set -a; . ./.env; set +a
   ./scripts/validate-release-images.sh
   docker compose -f compose.release.yaml --env-file .env pull
   docker compose -f compose.release.yaml --env-file .env up -d --wait
   ```

4. Verify the API, UI, schema, and owner authentication:

   ```sh
   curl -fsS http://127.0.0.1:${MEDIA_MANAGER_API_PORT:-28081}/health/ready
   curl -fsS -H "Authorization: Bearer $(grep '^OWNER_TOKEN=' .env | cut -d= -f2-)" \
     http://127.0.0.1:${MEDIA_MANAGER_API_PORT:-28081}/api/v1/system/info
   ```

5. Open the Cosmos/UI origin while signed in as the Cosmos owner. Continue with
   the first-run checklist below.

`compose.release.yaml` includes `compose.yaml` but resets `build:` and requires
both image variables. A missing variable fails closed.

## First-run checklist

Use the existing screens; no generated defaults are assumed:

1. **Connections** (`/settings/connections`): add AltMount and Plex; commission
   a Newznab indexer. Secrets are referenced by environment name and are never
   returned to the browser.
2. **Storage** (`/settings/storage`): create the movie/TV library roots with the
   Plex-visible and source-visible paths.
3. **Quality profiles** (`/settings/profiles`): create at least one profile per
   media kind and set required/preferred language rules. Danish is not invented
   as a default; the owner chooses it explicitly.
4. **Add media** (`/discover`): add a movie or series; the form explicitly tells
   you which of the three prerequisites is missing and links to setup.
5. **Wanted/workflows**: wait for a search, inspect evidence, and use only the
   offered owner recovery actions. Waiting is not an error.
6. **System → Backups**: create and verify an encrypted backup before relying on
   the installation.

## Update, backup, rollback

- Update by replacing both image variables with a new immutable pair, pulling,
  and recreating. Read the release's schema requirement first.
- Back up before every migration. The encrypted backup and `BACKUP_ENCRYPTION_KEY`
  are separate; without the key the backup cannot be restored.
- To turn an authenticated `.mmbak` file into the PostgreSQL custom dump:
  ```sh
  docker compose -f compose.release.yaml cp media-manager.mmbak api:/tmp/media-manager.mmbak
  docker compose -f compose.release.yaml exec -T --user root api chmod 0644 /tmp/media-manager.mmbak
  docker compose -f compose.release.yaml exec -T api \
    media-manager backup-decrypt /tmp/media-manager.mmbak /tmp/media-manager.dump
  docker compose -f compose.release.yaml cp api:/tmp/media-manager.dump ./media-manager.dump
  ```
  `backup-decrypt` reads `BACKUP_ENCRYPTION_KEY` from the API environment and
  refuses a missing key or a corrupt/truncated artifact.
- Restore the dump into a fresh database and start the matching image. Do not
  run old schema images against a newer migrated database.
- Off-site copies require owner-supplied S3/WebDAV credentials; they are not
  included in the image or repository.
