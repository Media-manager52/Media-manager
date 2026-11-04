# Manual installation and configuration reference

This is the detailed reference for running Media Manager from source with
Docker Compose. Most owners should start with
[getting-started.md](../getting-started.md) (Cosmos Cloud) or
[install-release.md](install-release.md) (published images); come here for the
mount contract, integration configuration, the public edge, the Seerr
Radarr/Sonarr replacement and day-two operations.

## Install with Docker Compose

1. Copy `.env.example` to `.env` and `.env.integrations.example` to `.env.integrations`; replace every placeholder.
2. Make the three host paths in `.env` point to the AltMount output, movie library, and TV library. Set `MEDIA_MANAGER_UID`/`MEDIA_MANAGER_GID` to an account that can read AltMount and create links in both library directories (often the output of `id -u` and `id -g`).
3. Create the shared Docker network once, then attach the existing AltMount container to it (replace the container name if needed):

   ```sh
   docker network create media-manager
   docker network connect media-manager altmount
   ```

   Set `MEDIA_NETWORK` if you use a different network name. You may instead use an HTTPS AltMount address reachable from that network.
4. Start the stack:

   ```sh
   docker compose up -d --build
   ```

5. Open the HTTPS Media Manager address configured in Cosmos and sign in with your Cosmos account. Cosmos is the only public edge. On the Docker host the API is also reachable directly at `127.0.0.1:${MEDIA_MANAGER_API_PORT}` (port 28081 by default) with the owner bearer token for machine checks.

### Acceptance checks

Three read-only checks cover the deployed architecture. None of them prints the
owner token.

Verify the public Cosmos edge is behind an authentication gate and routes
`/api/*` to the API rather than the UI container:

```sh
scripts/smoke-public-edge.sh https://media-manager.example.com
```

Verify the owner-authenticated API read models directly on the loopback
listener (the token is read from the running API container):

```sh
scripts/smoke-api-direct.sh
```

Run the read-only browser acceptance suite against the deployed UI and API. The
helper resolves the running containers, starts a loopback same-origin proxy
that reproduces the Cosmos route shape, and runs Playwright:

```sh
scripts/test-live-website.sh
```

The suite visits every static route at desktop, tablet, and mobile sizes, checks
available detail routes and safe interactions, captures core-page screenshots,
and audits layout, accessibility, browser errors, and failed local requests. It
blocks every state-changing request and every non-loopback request before
transmission; external artwork therefore exercises the local placeholder
without contacting its provider. Traces and video are disabled. Results stay
under `.sites-runtime/live-playwright-results/`.

Owner retry budgets are configurable in `.env`: `ACQUISITION_ATTEMPTS_PER_ROUND`
(default 2, range 1–6) and `ACQUISITION_RETAINED_OUTPUT_LIMIT` (default 4, range
1–12). Four attempts and eight retained failures allow more distinct alternatives
while keeping work bounded. Old rounds retain their immutable limits; open a
new round through the owner action. Failed files, exact exclusions, indexer
pacing and verification requirements remain enforced. See the
[operations runbook](runbook.md#acquisition-budget-changes).

Set `MEDIA_MANAGER_VERSION` and `MEDIA_MANAGER_COMMIT` to the release and Git commit you are deploying. PostgreSQL migrations run transactionally at API startup. PostgreSQL, the UI, and the API use isolated private networks; only the authenticated API also joins the external integrations network. Cosmos Cloud terminates TLS and authenticates the owner, then routes `/api/*` and `/health/*` to the API and every other path to the UI on one origin. The Cosmos `/api` route injects the owner bearer token server-side. The loopback API listener is bound to `127.0.0.1:${MEDIA_MANAGER_API_PORT}` and is never published to a LAN or public address.

### Public edge routing

Cosmos Cloud is the public edge and the owner authentication layer. The owner
token never reaches browser JavaScript. Cosmos registers proxy routes in
reverse configuration order (the last list entry is registered first and wins
for a shared host), so the catch-all UI route must be the last Media Manager
entry in the list:

| Path prefix | Target | Cosmos auth | Injected request header |
| --- | --- | --- | --- |
| `/api` | `http://media-manager-api-1:8080` | owner login | `Authorization: Bearer <OWNER_TOKEN>` |
| `/health` | `http://media-manager-api-1:8080` | owner login | none |
| `/` (catch-all) | `http://media-manager-ui-1:3000` | owner login | none |

The `/api` route's `ExtraHeaders` entry overwrites any inbound
`Authorization` header on the upstream request, so the browser can neither
supply nor read the token. Cosmos resolves both container names to their
private network addresses. The Seerr webhook posts directly to
`http://media-manager-api-1:8080/api/v1/integrations/seerr/webhook` over the
shared Docker network with its own secret, independent of Cosmos, so it never
requires an interactive login. A route pointed only at the `ui` container
serves the document but answers `/api/v1/*` with a bare HTTP 404, which the
browser surfaces as "Media Manager returned HTTP 404".

Verify the deployed public origin without changing server state:

```sh
scripts/smoke-public-edge.sh https://media-manager.example.com
```

The check fails if `/api/v1/*` reaches the `ui` container (a bare HTTP 404) or
if the API answers without an authentication gate. Use
`scripts/smoke-api-direct.sh` for the owner-authenticated read-model checks.

Compose passes PostgreSQL credentials as discrete `PG*` variables, so a strong database password does not need URL escaping. A standalone API deployment may instead provide `DATABASE_URL`.

## Mount contract

| Container path | Access | Purpose |
|---|---:|---|
| `/mnt/altmount` | read-only | AltMount completed/streaming media, reached through the host parent directory (`ALTMOUNT_HOST_PARENT_PATH`, bound at `/mnt` with `rslave` propagation) so FUSE remounts propagate in |
| `/libraries/movies` | read-write | Movie symlink library built by Media Manager |
| `/libraries/tv` | read-write | TV symlink library built by Media Manager |
| `/srv/media` | read-write | Optional parent of additional Plex library folders used by Seerr root-folder routing (`MEDIA_LIBRARY_HOST_PATH` / `MEDIA_LIBRARY_GID`; defaults to an empty project directory when unset) |

Plex should receive `/libraries/movies` and `/libraries/tv` read-only. Plex must also be able to resolve symlink targets; normally mount the same AltMount host folder read-only at the path configured as each root's `source_visible_root`.

Example root configuration:

```json
{
  "media_kind": "series",
  "name": "TV Primary",
  "destination_root": "/libraries/tv",
  "plex_visible_root": "/libraries/tv",
  "source_visible_root": "/mnt/altmount",
  "plex_instance_id": "PLEX-INTEGRATION-UUID",
  "plex_library_id": "2",
  "is_default": true,
  "enabled": true
}
```

The AltMount integration config uses its reported path plus the read-only container mapping:

```json
{
  "base_url": "http://altmount:8080",
  "authentication": "anonymous_admin",
  "allow_http": true,
  "reported_root": "/",
  "local_read_only_root": "/mnt/altmount",
  "category": "media-manager",
  "request_timeout_ms": 30000
}
```

Use `"authentication": "anonymous_admin"` only when AltMount reports
`login_required: false`. Media Manager verifies that live setting and the
anonymous admin identity before queue access, and stops if AltMount later
requires login. Otherwise omit the field (or use `"session"`) and provide the
documented username/password JSON secret. AltMount's personal API key does not
authenticate its native `/api/queue` endpoints and is not used for this path.
Set `reported_root` to the common absolute prefix returned in AltMount queue
`storage_path` values; this host reports `/movies`, `/tv`, and `/tmp`, so `/`
maps those paths into the read-only `/mnt/altmount` mount.

Integration secrets are referenced by environment-variable name. Put those values in `.env.integrations`, which is loaded only into the API container. An AltMount secret is JSON such as `{"username":"owner","password":"..."}`; Newznab references contain an API key; a Plex reference contains its token or `{"token":"..."}`. Never commit `.env` or `.env.integrations`.

OldBoys credential preparation has a separate owner form under **Settings →
Connections** for its API Token, Pass Key (PID), and RSS Key (RID). The API
encrypts that set in PostgreSQL using an independent server key from
`.env.integrations`; values never return to the browser. Saving does not enable
the provider. See [OldBoys setup and recovery](oldboys.md).

Media Manager owns metadata enrichment directly. Optional dedicated `TMDB_ACCESS_TOKEN`, `TVDB_API_KEY`/`TVDB_SUBSCRIBER_PIN`, and `SIMKL_CLIENT_ID` values belong in `.env.integrations`. Existing local secret files can instead be referenced with `METADATA_TMDB_TOKEN_SOURCE_FILE`, `METADATA_TVDB_KEY_SOURCE_FILE`, and `METADATA_SIMKL_CLIENT_ID_SOURCE_FILE`; Compose mounts only those three files read-only and never copies their values into the project. An owner refresh performs only exact-ID, bounded provider detail calls. TMDB is the primary descriptive source, TheTVDB supplements exact-ID movie/TV facts, and Simkl contributes ratings and lifecycle evidence. IMDb identities link to the exact title; an IMDb rating obtained from Simkl is stored with that provenance and is never represented as a direct IMDb API response. Direct IMDb API enrichment remains unavailable until a separately licensed IMDb/AWS source is configured. This product uses the TMDB API but is not endorsed or certified by TMDB.

Newznab records are always staged disabled. In Connections, **Validate & enable** performs only authenticated CAPS and one random negative search, spaced by at least three seconds through the same persisted rate limiter used by normal work. It enables the indexer only when both checks pass. After changing `.env.integrations`, reload those variables with `docker compose up -d --force-recreate api`; `docker compose restart` does not reload environment files.

The developer-only provider-onboarding procedure and the currently verified
movie/complete-season dialects are recorded in
[`docs/status/indexer-conformance.md`](../status/indexer-conformance.md). The lab is never a
runtime dependency and its raw evidence is not imported into Media Manager.

## Manual season-pack add

`POST /api/v1/media` accepts defaults or per-item overrides. A series request includes the exact episode snapshot that must be present before publishing:

```json
{
  "kind": "series",
  "title": "Example Show",
  "year": 2026,
  "identities": [
    {"namespace":"tvdb","value":"12345","state":"verified","source":"manual"}
  ],
  "seasons": [{
    "number": 1,
    "monitored": true,
    "search_immediately": true,
    "prefer_season_pack": true,
    "episodes": [
      {"number":1,"provider_episode_id":"tvdb:1001","title":"Pilot"},
      {"number":2,"provider_episode_id":"tvdb:1002","title":"Second"}
    ]
  }]
}
```

TVDB is preferred for series naming when verified; TMDB is the fallback. Movies use TMDB, then IMDb. An internal UUID is always the primary key, so no single external provider ID is required for every title.

## Seerr without Radarr/Sonarr

Media Manager can be Seerr's Radarr and Sonarr server. Set `SERVARR_API_KEY` in
`.env`, recreate the API, then point Seerr's services at the API container:

| Service | Hostname | Port | SSL | Base URL | API key |
| --- | --- | --- | --- | --- | --- |
| Radarr | `media-manager-api-1` | `8080` | off | `/radarr` | `SERVARR_API_KEY` |
| Sonarr | `media-manager-api-1` | `8080` | off | `/sonarr` | `SERVARR_API_KEY` |

Seerr reads Media Manager's enabled quality profiles and a single root folder,
`/srv/media/Auto`, from these services. For Auto requests Media Manager picks
the library root (kids, documentary, Christmas, Danish original, or default)
and, for kids content, the profile without required subtitles, from the
title's metadata. Adds are acknowledged without a second catalog: the Seerr
request itself (webhook/poll) creates the workflow. Availability scans read
Plex-confirmed publications. Requests created while Seerr still pointed at an
older server keep working through the Seerr root-folder routes in Settings →
Connections. Radarr and Sonarr are then not part of the stack at all.

### A 4K edition

To keep a 4K copy beside the standard one, create a separate Plex library for
it (for example `Film 4K` on `/media/Film 4K`), then a Media Manager library
root with `"edition": "4k"` for it (`POST /api/v1/settings/library-roots`; a 4K
root cannot be the default). In Seerr, add a second Radarr and Sonarr server
with **4K** enabled, the same settings as above but base URL `/radarr4k` and
`/sonarr4k`, and the Auto root folder. A request made with Seerr's 4K button
then goes to the 4K root, acquires only 2160p releases, and is reported back
to Seerr as 4K. While a title has a 4K edition its standard edition acquires at
most 1080p.

## Operations

- Liveness: `GET /health/live`
- Readiness, including schema version: `GET /health/ready`
- Version: `media-manager version`
- Manual migration: `media-manager migrate`
- Database backup: `docker compose exec -T postgres sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > media-manager.dump`
- Restore into a fresh database, run `media-manager migrate`, then complete the two live workflow checks before serving traffic.

Do not edit or delete AltMount files through Media Manager. Do not expose the API container port. Back up PostgreSQL, `.env`, and `.env.integrations` together; protect all three as secrets and restore the environment files before recreating the API container.
