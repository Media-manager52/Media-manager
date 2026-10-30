# Getting started with Cosmos Cloud

This guide is for a new owner running Media Manager behind **Cosmos Cloud**.
Cosmos is the public edge: it terminates TLS, authenticates the owner with
Cosmos OpenID, and routes one public origin to the UI and API. The `/api` route
injects the owner bearer token server-side; the browser never receives it. All
three routes are host-bound and require a Cosmos admin/owner, so another Cosmos
app host or a non-admin Cosmos user cannot reach the owner API. This is the same
architecture as the production deployment.

## Install from the Cosmos market

If your Cosmos market already shows **Media Manager**, install it there and skip
the source setup below. The market wizard asks for the library paths, AltMount
parent, shared media network, owner token, and optional Plex/Newznab/TMDB
credentials. It creates the API, UI and PostgreSQL services plus the UI,
`/api` and `/health` routes on one host, with the owner token injected
server-side on `/api`. Then continue at **Connect the pieces**.

To make it appear in your Cosmos market before the official listing is merged,
add this source under **Market → Sources**:

```text
Name: Media Manager
URL:  https://raw.githubusercontent.com/Media-manager52/Media-manager/main/deploy/cosmos/index.json
```

The official Cosmos store submission is
[PR #336](https://github.com/azukaar/cosmos-servapps-official/pull/336).
The source installation below remains available for development and for hosts
where you want to manage the Compose project directly.

## Source installation

Media Manager is not a downloader. It acquires through **AltMount**, publishes
Plex-safe links, and confirms the result in **Plex**. You need AltMount running
before the first title can be acquired. Seerr is optional; Media Manager can
also create and search titles directly.

## What you need

- Cosmos Cloud installed, started and reachable over HTTPS with an owner
  account. Complete the Cosmos first-run before this guide.
- A Linux host with Docker and Docker Compose v2. Your user must be able to
  run `docker` (usually membership of the `docker` group).
- Internet access for the first image build (Go modules, npm packages and
  Alpine packages). The running stack only needs the integrations it uses.
- Plex Media Server (required for publication and confirmation).
- AltMount (required for Usenet acquisition), on the same Docker host or
  reachable from it.
- At least one Newznab-compatible indexer.
- A TMDB API read access token (recommended; discovery and descriptive
  metadata).
- Optional: Seerr/Overseerr/Jellyseerr for requests.
- Host folders for movies and TV, and the parent folder that contains
  AltMount's FUSE mount.
- A hostname for Media Manager in Cosmos. Use the host Cosmos assigns to the
  route, or a custom hostname whose DNS points at Cosmos. Use the **same host**
  for the UI, `/api` and `/health` routes. If you have not created that route
  yet, enter the hostname you intend to use; you can update `.env` later.

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
./scripts/setup-cosmos.sh
```

It asks for the library folders, the AltMount parent folder, the Docker network
shared with AltMount/Plex/Seerr, the Cosmos public origin, optional container
names, and optional Plex/Newznab/TMDB credentials. It then:

- writes `.env` and `.env.integrations` with mode `600`
- pins `COMPOSE_PROJECT_NAME=media-manager` so the Cosmos targets
  `media-manager-api-1` and `media-manager-ui-1` are stable
- generates the database password, owner token, backup key, OldBoys key and
  Servarr key
- creates the shared Docker network and connects the AltMount/Plex/Seerr
  containers you name
- validates Compose, builds the images, starts the stack, and waits for the API
- prints the three Cosmos routes you need to add

The setup **does not modify Cosmos Cloud**. You add the routes in the Cosmos UI
in the next step.

Use `--no-start` if you want to review the generated files first. It still
creates and joins the Docker network; start the stack later with:

```sh
docker compose up -d --build
```

The first build can take 10–20 minutes. Follow it with:

```sh
docker compose logs -f --tail=200
```

## 3. Add the Cosmos routes

In Cosmos Cloud, open **Proxy → Routes** and create three routes on the same
host. Cosmos 0.23.4 shows a separate hostname field for each route that has
`UseHost=true`; set the API, health and UI fields to exactly the same hostname.
Use the host Cosmos assigned to the UI route, or a custom hostname you pointed
at Cosmos.

| Path prefix | Target | Cosmos auth | Extra header |
| --- | --- | --- | --- |
| `/api` | `http://media-manager-api-1:8080` | owner login | `Authorization: Bearer <OWNER_TOKEN>` |
| `/health` | `http://media-manager-api-1:8080` | owner login | none |
| `/` (catch-all) | `http://media-manager-ui-1:3000` | owner login | none |

Production uses `Mode=SERVAPP`, `UseHost=true`, the same explicit host for all
three routes, `UsePathPrefix=true` for `/api` and `/health`, an empty path prefix
for the UI, `StripPathPrefix=false`, `Timeout=14400000` (4 hours) and
`AdminOnly=true` for all three routes.

Important details:

- Keep the `/api` and `/health` path prefixes **above** the catch-all `/`
  route in the Cosmos route list; the catch-all must be last. If
  `/api/v1/dashboard` returns a bare HTTP 404, the catch-all is winning and the
  browser will show "Media Manager returned HTTP 404".
- Do not strip the path prefix. The API expects the `/api/...` path.
- Set the route timeout high enough for long downloads and backups; production
  uses 4 hours.
- `AuthEnabled` and `AdminOnly` must be on for all three routes. This makes the
  API, health and UI owner/admin-only; a signed-in non-admin Cosmos user must
  not receive the Media Manager owner token. The API and health routes must also
  use `UseHost=true` with the same host as the UI, otherwise `/api` can match on
  another Cosmos app's hostname.
- After saving, confirm all three stored routes show the same `host`. The
  wizard overwrites each template route's host from its own hostname field, so
  changing only the UI field can leave `/api` and `/health` on a different
  host; `/api` would then fall through to the UI catch-all and fail closed
  without bearer injection.
- The `/api` route's `ExtraHeaders` entry is what authenticates the browser to
  the API. Get the token value from `.env` on the server:

  ```sh
  grep '^OWNER_TOKEN=' .env
  ```

  Paste it as `Authorization: Bearer <value>`. The route overwrites any inbound
  `Authorization` header, so the browser can neither supply nor read the token.
  Never put the token in browser code, a screenshot or a commit.
- The target container names are stable because the setup pins the Compose
  project to `media-manager`. If you change the project name, update the Cosmos
  targets too.

Open `https://<your-host>` and sign in with your Cosmos account. Media Manager
should load, and its API calls should succeed because Cosmos injects the owner
bearer.

If you change the public hostname later, update
`COMMISSIONING_V2_OWNER_ORIGIN` in `.env` and recreate the API:

```sh
docker compose up -d --force-recreate api
```

Verify the public edge:

```sh
scripts/smoke-public-edge.sh https://<your-host>
```

It must report an authentication gate (`401` or a redirect), never HTTP Basic
auth, and never a bare `404` for `/api/v1/*`. For direct owner-authenticated
read checks on the loopback listener:

```sh
scripts/smoke-api-direct.sh
```

The API port (`127.0.0.1:28081` by default) is only for machine checks; never
publish it.

## 4. Connect the pieces

The application is running but intentionally starts without integrations.
Work through this checklist in the UI.

### AltMount

Open **Sources → Download clients** and add an AltMount integration:

- Base URL: `http://altmount:8080` when the container is on the shared network.
- Category: `media-manager`.
- Local read-only root: `/mnt/altmount` (the path inside the API container).
- Authentication: `anonymous_admin` if AltMount has no API secret, otherwise
  create a secret ref whose value is in `.env.integrations`.

The API mounts the **parent** of AltMount's FUSE mount at `/mnt` read-only with
`rslave` propagation, so an AltMount restart or remount reaches the running API.
Both the Compose file and the Cosmos market template declare this propagation;
mount the parent folder, never the FUSE mount point itself.

The read-only bind is only a guard: it does not make a writable AltMount FUSE
mount read-only. Before the first acquisition, enable AltMount's
`fuse.read_only: true` (or expose the output through a read-only mount) and make
the host parent a shared mount so new child mounts propagate to the API. For
example, on a host where AltMount mounts under `/mnt/altmount`, verify both the
mount options and the container view:

```sh
findmnt -no TARGET,OPTIONS /mnt /mnt/altmount
docker exec media-manager-api-1 sh -c 'test -r /mnt/altmount && ! test -w /mnt/altmount'
```

The second command must succeed. If it fails, fix AltMount's output mode or the
host propagation before adding a title; Media Manager will not copy or modify
AltMount output. The AltMount output must be visible to Plex at the same
absolute path as `source_visible_root`; Plex resolves the symlinks Media Manager
publishes.

### Plex

Add a Plex integration with its URL and token. Put the token in
`.env.integrations` as `PLEX_TOKEN=` and use `PLEX_TOKEN` as the secret ref.
For a local HTTP Plex, allow HTTP on the integration; use HTTPS when possible.

### Indexers

Open **Sources → Indexers**, add a Newznab indexer, and commission it. Put its
API key in `.env.integrations` under a name such as
`NEWZNAB_PRIMARY_API_KEY=` and use that name as the secret ref. Only
commissioned indexers search.

### Metadata providers

TMDB is the primary source for discovery and descriptive metadata. Create a
TMDB API read access token, put it in `.env.integrations` as
`TMDB_ACCESS_TOKEN=...`, and recreate the API. The setup asks for it; you can
leave it blank and add it later. TVDB and Simkl are optional supplements:
`TVDB_API_KEY`/`TVDB_SUBSCRIBER_PIN` and `SIMKL_CLIENT_ID`. Without TMDB,
discovery and descriptive metadata are limited.

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
enabled Seerr integration and matching environment values. Create the Seerr
integration in **Settings → Connections** and copy its integration UUID from
the API, then put these in `.env`:

```sh
# On the Media Manager host; the API is loopback-only. The token is read from
# .env and is never printed.
OWNER_TOKEN=$(sed -n 's/^OWNER_TOKEN=//p' .env)
curl -sS -H "Authorization: Bearer $OWNER_TOKEN" \
  http://127.0.0.1:28081/api/v1/settings/integrations
unset OWNER_TOKEN
# Find the Seerr integration's "id" and use it as SEERR_INSTANCE_ID.

SEERR_BASE_URL=http://seerr:5055
SEERR_API_KEY=...
SEERR_INSTANCE_ID=<the integration UUID>
SEERR_WEBHOOK_SECRET=<at least 32 random characters>
SEERR_ALLOW_HTTP=true   # only for a local HTTP Seerr
```

Recreate the API after changing `.env`:

```sh
docker compose up -d --force-recreate api
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
docker compose up -d --build
```

This rebuilds the images, recreates changed containers, and runs database
migrations at API startup. Migrations are forward-only; take a backup before a
major update. Cosmos routes and container names do not change.

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
| "Media Manager returned HTTP 404" | The Cosmos `/api` route targets the UI or the catch-all is winning. Point `/api` at `http://media-manager-api-1:8080`, keep it above the catch-all, and do not strip the prefix. |
| `401 unauthorized` on every API call | The Cosmos `/api` route is missing `Authorization: Bearer <OWNER_TOKEN>` or its token differs from `.env`. |
| Browser shows an HTTP Basic auth prompt | The public edge is not Cosmos, or another proxy is in front. Cosmos uses OpenID; remove the extra edge. |
| UI loads but API calls fail | `docker compose ps`; the API must be healthy. Check the Cosmos `/api` target and `docker compose logs --tail=200 api`. |
| Cosmos cannot reach the containers | Use the same host for all three routes and the targets `media-manager-api-1`/`media-manager-ui-1`. Confirm `COMPOSE_PROJECT_NAME=media-manager` in `.env` and recreate. |
| Cannot create library symlinks | The API UID/GID must own or be able to write the library folders. Check `ls -ld` and `MEDIA_MANAGER_UID/GID`. |
| Plex cannot play published files | Plex must see AltMount output at the same absolute path as the root's `source_visible_root`, and the library folder at `plex_visible_root`. |
| AltMount output unavailable | Check `ALTMOUNT_HOST_PARENT_PATH`, the `rslave` mount, `MEDIA_NETWORK`, and that the AltMount container is connected. |
| AltMount/API cannot resolve `plex`/`seerr` | Connect those containers to `MEDIA_NETWORK`, or use their reachable addresses. |
| Discovery is empty or titles lack descriptions | Add `TMDB_ACCESS_TOKEN` to `.env.integrations` and recreate the API; check the API logs for a provider error. |
| Schema mismatch after an update | The API did not finish migrations. Check its logs; restore the pre-update backup if needed. |

## Security notes

- Cosmos Cloud is the only public edge. It handles TLS and OpenID; do not add a
  second Basic-auth proxy in front.
- The browser never receives `OWNER_TOKEN`. It is injected only by the Cosmos
  `/api` route; keep that route's extra header private.
- Do not expose the API port. It is bound to loopback for machine checks.
- Keep `.env` and `.env.integrations` at mode `600`; they contain the database
  password, owner token, backup key and integration secrets.
- There is one Media Manager owner token. All three routes require
  `AdminOnly=true`; do not grant Cosmos `PERM_ADMIN_READ` to non-owner roles or
  API tokens, because that permission is the route's admin gate. Do not share
  the owner token or run a second proxy that injects it.
- There is no separate Caddy or Basic-auth edge here. Cosmos is the supported
  public edge, matching production.

## What is not included

Cosmos Cloud, AltMount, Plex, Seerr and the indexers are separate
applications. This bundle is the Media Manager source; it builds its own API/UI
images and runs its own PostgreSQL. Media Manager is licensed under
`AGPL-3.0-only`; see [`LICENSE`](../LICENSE) and
[`THIRD_PARTY_NOTICES.md`](../THIRD_PARTY_NOTICES.md). The corresponding source
is <https://github.com/Media-manager52/Media-manager>.

## Where to go next

- [Operations runbook](operations/runbook.md): deployment details, Cosmos
  routes, env vars, recovery, backups and troubleshooting.
- [Architecture overview](architecture/overview.md): the workflow and data
  model.
- [AI agent operations](operations/ai-agents.md): inspect and operate the
  running server through the API.
