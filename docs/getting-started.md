# Getting started on your own server

This guide is for a new owner setting up Media Manager on their own machine.
It uses the **standalone Caddy edge**: a browser login, HTTPS (or a LAN port),
and a server-side owner bearer. You do not need Cosmos Cloud for this path.

Media Manager is not a downloader. It acquires through **AltMount**, publishes
Plex-safe links, and confirms the result in **Plex**. You need AltMount running
before the first title can be acquired. Seerr is optional; Media Manager can
also create and search titles directly.

## What you need

- A Linux host with Docker and Docker Compose v2. Your user must be able to
  run `docker` (usually membership of the `docker` group).
- Internet access for the first image build (Go modules, npm packages and
  Alpine packages). The running stack itself only needs the integrations it
  uses.
- Plex Media Server (required for publication and confirmation).
- AltMount (required for Usenet acquisition), running on the same Docker host
  or reachable from it.
- At least one Newznab-compatible indexer.
- Optional: Seerr/Overseerr/Jellyseerr for requests.
- Optional: a domain name for automatic HTTPS.
- Host folders for movies and TV, and the parent folder that contains
  AltMount's FUSE mount.
- The chosen LAN port, or ports 80/443 for HTTPS, open in the host firewall.

A small server is enough for the application containers (roughly 2 CPUs and
4 GB RAM); media storage and Plex are separate.

## 1. Get the source

From a shared bundle:

```sh
tar xzf media-manager-share-YYYYMMDD.tar.gz
cd media-manager
```

Or from the repository:

```sh
git clone <repository-url> media-manager
cd media-manager
```

## 2. Run the setup

```sh
./scripts/setup-standalone.sh
```

It asks for the library folders, the AltMount parent folder, the Docker network,
the access mode, and the owner login. It then:

- writes `.env` and `.env.integrations` with mode `600`
- generates the database password, owner token, backup key and OldBoys key
- hashes the owner login for Caddy
- creates the shared Docker network and connects AltMount/Plex/Seerr if their
  containers exist
- builds and starts PostgreSQL, the API, the UI and the Caddy edge

Use `./scripts/setup-standalone.sh --no-start` if you want to review the
generated files before starting. The setup never prints generated secrets.

The first build can take 10–20 minutes. If the page is not ready when the
script returns, follow the logs:

```sh
./scripts/standalone-logs.sh
```

## 3. Open Media Manager

- LAN mode: `http://<server-ip>:8080` (or the port you chose).
- Domain mode: `https://<your-domain>` after DNS points at the server and
  Caddy obtains a certificate. Ports 80 and 443 must be reachable.

Log in with the owner name and password you chose. This login is only for
Media Manager; Plex, Seerr and your indexers keep their own accounts. The
browser authenticates to Caddy with Basic auth. Caddy replaces the header with the owner bearer before
the request reaches the API, so the bearer never reaches browser JavaScript.
The loopback API port (default `127.0.0.1:28081`) is only for machine checks;
do not publish it.

## 4. Connect the pieces

The application is running but intentionally starts without integrations.
Work through this checklist in the UI.

### AltMount

Open **Sources → Download clients** and add an AltMount integration:

- Base URL: `http://altmount:8080` when the container is on the shared network,
  or `http://host.docker.internal:<port>` when AltMount runs on the host.
- Category: `media-manager`.
- Local read-only root: `/mnt/altmount` (the path inside the API container).
- Authentication: `anonymous_admin` if AltMount has no API secret, otherwise
  create a secret ref whose value is in `.env.integrations`.

The API mounts the **parent** of AltMount's FUSE mount at `/mnt` read-only with
`rslave` propagation, so an AltMount restart or remount reaches the running API.
The AltMount output must be visible to Plex at the same absolute path as
`source_visible_root`; Plex resolves the symlinks Media Manager publishes.

### Plex

Add a Plex integration with its URL and token. Put the token in
`.env.integrations` as `PLEX_TOKEN=` and use `PLEX_TOKEN` as the secret ref.
For a local HTTP Plex, allow HTTP on the integration; use HTTPS when possible.

### Indexers

Open **Sources → Indexers**, add a Newznab indexer, and commission it. Put its
API key in `.env.integrations` under a name such as
`NEWZNAB_PRIMARY_API_KEY=` and use that name as the secret ref. Only
commissioned indexers search.

### Library root and profile

Open **Settings → Storage** and add one root per media kind:

- Destination root: `/libraries/movies` or `/libraries/tv` (inside the API).
- Plex-visible root: the path Plex uses for the same folder.
- Source-visible root: the path Plex uses to see AltMount output.
- Plex instance and library ID.

Then open **Settings → Quality profiles**, create or enable a profile, and set
the required/preferred audio and subtitle languages. The profile is what links
requests to a root and controls acquisition evidence.

### Add a title

Use **Discover / Add media** for a manual title or send a request from Seerr.
The title page shows the acquisition pipeline. After AltMount completes, the
output is inspected read-only and published as a managed symlink; Plex then
confirms the exact path and provider identity before the title is success.

### Seerr (optional)

Seerr is a second request entry point. Media Manager's Seerr intake needs an
enabled Seerr integration and matching environment values. The simplest path is
to create the Seerr integration in **Settings → Connections**, copy its
integration UUID from the API (the browser can use the edge login), then put
these in `.env`:

```sh
# On the Media Manager host, with the edge login:
curl -u owner:YOUR_PASSWORD http://127.0.0.1:8080/api/v1/settings/integrations
# Find the Seerr integration's "id" and use it as SEERR_INSTANCE_ID.

SEERR_BASE_URL=http://seerr:5055
SEERR_API_KEY=...
SEERR_INSTANCE_ID=<the integration UUID>
SEERR_WEBHOOK_SECRET=<at least 32 random characters>
SEERR_ALLOW_HTTP=true   # only for a local HTTP Seerr
```

Recreate the API after changing `.env`:

```sh
# Use --profile https instead of lan when you chose HTTPS.
docker compose -f compose.yaml -f compose.standalone.yaml --profile lan up -d --force-recreate api
```

Configure Seerr's webhook to
`http://media-manager-api-1:8080/api/v1/integrations/seerr/webhook` with the
same secret. If Seerr is configured to use Radarr/Sonarr services, point them at
`http://media-manager-api-1:8080/radarr` and `/sonarr` with `SERVARR_API_KEY`
from `.env` as the API key. The detailed Seerr and root-routing workflow is in
[the runbook](operations/runbook.md). Seerr is optional; manual requests work
without it.

## Folder and permission contract

| Host path | Container path | Purpose |
| --- | --- | --- |
| `MOVIES_HOST_PATH` | `/libraries/movies` | Managed movie symlinks |
| `TV_HOST_PATH` | `/libraries/tv` | Managed TV symlinks |
| `ALTMOUNT_HOST_PARENT_PATH` | `/mnt` read-only | Parent of AltMount's FUSE mount |
| `MEDIA_LIBRARY_HOST_PATH` | `/srv/media` | Optional parent for extra Plex library folders |

The API runs as `MEDIA_MANAGER_UID:GID` from `.env` (the setup uses your current
user). It must be able to create symlinks in both library folders and read the
AltMount mount. Plex must be able to read the same library folders and resolve
the symlink targets. If Plex runs in a container, mount the AltMount output and
the library folders into Plex at paths that match the roots you configure.

Media Manager never copies or edits AltMount output. It publishes managed
symlinks and leaves the source read-only.

## Updating

```sh
# from the new bundle or after git pull
./scripts/standalone-up.sh
```

This rebuilds the images, recreates changed containers, and runs database
migrations at API startup. Migrations are forward-only; take a backup before a
major update. Caddy certificates live in named volumes and survive restarts.

## Backups

- Open **System → Backups** to create an encrypted, restore-verified backup.
- Back up `BACKUP_ENCRYPTION_KEY` from `.env.integrations` separately. Without
  it the encrypted backups cannot be restored.
- Optional off-site copies: set `BACKUP_OFFSITE_KIND=s3` or `webdav` and the
  matching `BACKUP_OFFSITE_*` values in `.env.integrations`, then recreate the
  API. The destination receives only the encrypted `.mmbackup` artifact.
- The backup archive contains the database and configuration, not media.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Browser asks for a password and rejects it | Recreate the edge after editing `.env`: `./scripts/standalone-up.sh`. If you changed the password, regenerate the hash with `docker run --rm caddy:2.10-alpine caddy hash-password --plaintext='new password'` and replace `BASIC_AUTH_HASH` in `.env`. |
| Page loads but API calls fail | `./scripts/standalone-status.sh` and `./scripts/standalone-logs.sh`; the API must be healthy and the Caddy `/api/*` route must be present. |
| `401` on `/api` from a browser | The Caddy edge is not injecting the owner bearer, or `OWNER_TOKEN` differs between the edge and the API. |
| Cannot create library symlinks | The API UID/GID must own or be able to write the library folders. Check `ls -ld` and `MEDIA_MANAGER_UID/GID`. |
| Plex cannot play published files | Plex must see AltMount output at the same absolute path as the root's `source_visible_root`, and the library folder at `plex_visible_root`. |
| AltMount output unavailable | Check `ALTMOUNT_HOST_PARENT_PATH`, the `rslave` mount, and that the AltMount container is on `MEDIA_NETWORK`. |
| AltMount/API cannot resolve `plex`/`seerr` | Connect those containers to `MEDIA_NETWORK`, or use `host.docker.internal` for host services. |
| Port already in use | Change `MEDIA_MANAGER_HTTP_PORT` (LAN), `MEDIA_MANAGER_API_PORT` (loopback; also update `COMMISSIONING_V2_OWNER_ORIGIN`), or stop the service using 80/443 (HTTPS). |
| Build fails | Check Docker disk space and network access; run the build again with `./scripts/standalone-up.sh`. |
| Schema mismatch after an update | The API did not finish migrations. Check its logs; restore the pre-update backup if needed. |

## Security notes

- Use HTTPS for anything reachable from the internet. LAN mode is for a trusted
  network only.
- The browser never receives `OWNER_TOKEN`. Do not expose the API port directly.
- Keep `.env` and `.env.integrations` at mode `600`; they contain the database
  password, owner token, backup key and integration secrets.
- There is one owner account. Multi-user roles and external identity providers
  are not part of this path; Cosmos remains an option for the original
  production deployment.
- The Caddy edge uses Caddy's automatic HTTPS for a domain. If you put another
  reverse proxy in front, preserve the same-origin `/api/*` route and the
  server-side bearer injection, or use the API directly only on loopback.

## What is not included

AltMount, Plex, Seerr and the indexers are separate applications. This bundle
is the Media Manager source; it builds its own API/UI images and runs its own
PostgreSQL. The repository currently has no license file; ask the owner before
redistributing it beyond the friend it was shared with.

## Where to go next

- [Operations runbook](operations/runbook.md): deployment details, Seerr,
  recovery, backups and troubleshooting.
- [Architecture overview](architecture/overview.md): the workflow and data
  model.
- [AI agent operations](operations/ai-agents.md): inspect and operate the
  running server through the API.
