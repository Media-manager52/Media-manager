# Media Manager

Media Manager is an owner-operated movie and TV manager for Usenet. It owns the workflow from a Seerr or manual request through evidence-based Newznab selection, one AltMount job, read-only output verification, Plex-safe symlink publication, and exact Plex confirmation.

The first production path is deliberately narrow: **Seerr + Media Manager + Newznab indexers + AltMount + Plex**. Radarr and Sonarr are not runtime dependencies. A season pack is one download job, and publication is blocked unless every frozen expected episode maps exactly once.

## Stack

- Go 1.27 API and durable outbox workers
- PostgreSQL 18
- React 19, TypeScript 5.9, Next 16 UI through Vinext/Vite
- Caddy same-origin gateway; the owner token stays server-side
- Docker Compose for the supported deployment

## Start

1. Copy `.env.example` to `.env` and `.env.integrations.example` to `.env.integrations`; replace every placeholder. Generate the owner password hash with the command shown in `.env.example`.
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

5. Open the HTTPS address configured in Cosmos and sign in with `OWNER_USERNAME` and the password used to create `OWNER_PASSWORD_HASH`. On the Docker host itself, use the loopback origin configured by `MEDIA_MANAGER_PORT` (port 8080 by default).

### Authenticated local smoke test

The gateway correctly stores only the owner's password hash, so an HTTP client
cannot derive a login from the deployment configuration. Run the local smoke
helper from an interactive terminal:

```sh
scripts/smoke-gateway-auth.sh
```

It prompts silently for the existing owner password, keeps it only in process
memory for the duration of the check, and never passes it as a command argument.
The check is locked to `127.0.0.1`, does not follow redirects, and verifies the
unauthenticated boundary, authenticated UI rendering, and authenticated API
readiness. Nothing is added to `.env`, the repository, or local credential
storage.

Set `MEDIA_MANAGER_VERSION` and `MEDIA_MANAGER_COMMIT` to the release and Git commit you are deploying. PostgreSQL migrations run transactionally at API startup. PostgreSQL, UI, and Caddy use isolated private networks; only the authenticated API also joins the external integrations network. No service port except the loopback gateway is published. Caddy proxies `/api/*` and injects the owner credential only after its own owner login. Do not change `MEDIA_MANAGER_BIND` to a LAN address unless another authenticated TLS proxy protects that listener.

Compose passes PostgreSQL credentials as discrete `PG*` variables, so a strong database password does not need URL escaping. A standalone API deployment may instead provide `DATABASE_URL`.

## Mount contract

| Container path | Access | Purpose |
|---|---:|---|
| `/mnt/altmount` | read-only | AltMount completed/streaming media |
| `/libraries/movies` | read-write | Movie symlink library built by Media Manager |
| `/libraries/tv` | read-write | TV symlink library built by Media Manager |

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

Media Manager owns metadata enrichment directly. Optional dedicated `TMDB_ACCESS_TOKEN`, `TVDB_API_KEY`/`TVDB_SUBSCRIBER_PIN`, and `SIMKL_CLIENT_ID` values belong in `.env.integrations`. Existing local secret files can instead be referenced with `METADATA_TMDB_TOKEN_SOURCE_FILE`, `METADATA_TVDB_KEY_SOURCE_FILE`, and `METADATA_SIMKL_CLIENT_ID_SOURCE_FILE`; Compose mounts only those three files read-only and never copies their values into the project. An owner refresh performs only exact-ID, bounded provider detail calls. TMDB is the primary descriptive source, TheTVDB supplements exact-ID movie/TV facts, and Simkl contributes ratings and lifecycle evidence. IMDb identities link to the exact title; an IMDb rating obtained from Simkl is stored with that provenance and is never represented as a direct IMDb API response. Direct IMDb API enrichment remains unavailable until a separately licensed IMDb/AWS source is configured. This product uses the TMDB API but is not endorsed or certified by TMDB.

Newznab records are always staged disabled. In Connections, **Validate & enable** performs only authenticated CAPS and one random negative search, spaced by at least three seconds through the same persisted rate limiter used by normal work. It enables the indexer only when both checks pass. After changing `.env.integrations`, reload those variables with `docker compose up -d --force-recreate api`; `docker compose restart` does not reload environment files.

The developer-only provider-onboarding procedure and the currently verified
movie/complete-season dialects are recorded in
[`docs/INDEXER_CONFORMANCE.md`](docs/INDEXER_CONFORMANCE.md). The lab is never a
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

## Safety invariants

- One in-flight request per indexer; persisted pacing, `Retry-After`, and bounded backoff apply to normal work and the Indexer Lab.
- Repeated Newznab attributes retain their original order and values.
- Commissioning snapshots each provider's advertised movie and TV search dialect. Runtime searches send only supported parameters, prefer verified external identities, normalize IMDb IDs to their numeric Newznab form, and retain general search as a capability-backed fallback.
- Every Newznab call reserves a durable per-provider rolling budget before network I/O. Prowlarr-imported query/grab limits are preserved; otherwise the conservative fallback is 1,000 queries and 100 grabs per 24 hours, in addition to the three-second minimum interval and one-in-flight lane.
- Ambiguous AltMount upload results are reconciled by a unique filename and are never blindly resubmitted.
- AltMount output is read-only. Media Manager publishes only contained symlinks.
- A season pack remains one AltMount job and one immutable expectation set.
- AltMount completion is not import completion; import completion is not Plex completion.
- Completed output is inspected only inside the authorized AltMount job root. Required subtitle languages must be verified on every selected video before publication; Danish variants are stored canonically as `da` with raw evidence retained.
- A verified external subtitle is published beside its video with Plex-compatible naming and shares that publication generation. It becomes current only after the video completes exact Plex confirmation.
- Subtitle-unverified output is retained for owner review, never made current, and never automatically deleted. One alternate exact release is permitted per workflow, for two acquisition attempts total.
- Plex confirmation requires a verified provider GUID and every exact expected path.
- An upgrade receives a distinct release-version filename. The old symlink is removed only after Plex confirms the replacement; the AltMount source is retained.
- Replacing an adopted real file requires an exact owner confirmation before every new or delayed search unless that workflow already holds an exact fingerprint-bound authorization. The old file is moved only after Plex confirms the new publication, retained for 30 days, and never physically purged without a second exact owner confirmation after retention.
- Rolling an adopted upgrade back restores the entire retained cycle with exact fingerprints. Replacement links remain current until Plex re-confirms every restored path, then are safely displaced and removed.
- Reviewed adoption monitoring is applied only after the renamed files pass Plex path and identity confirmation. Immediate search additionally requires the adopted replacement phrase and preserves an existing exact TV episode snapshot for one complete-pack workflow.

## Verification

```sh
npm run lint
npm run typecheck
npm test
npm run test:browser
GOFLAGS=-mod=readonly go vet ./cmd/... ./internal/...
GOFLAGS=-mod=readonly go test -count=1 -race ./cmd/... ./internal/...
```

The browser suite uses local API fixtures, blocks unexpected network access, and checks the library at desktop and mobile sizes with Playwright and axe. GitLab CI runs the frontend, browser, and PostgreSQL-backed Go verification jobs for branches, tags, and merge requests.

The release workflow also applies all migrations to a disposable PostgreSQL 18 service and builds both container targets. Before promoting to the main server, run one complete live movie and one complete live season-pack workflow against the dev Plex, AltMount, Seerr, and real indexers.

## Operations

- Liveness: `GET /health/live`
- Readiness, including schema version: `GET /health/ready`
- Version: `media-manager version`
- Manual migration: `media-manager migrate`
- Database backup: `docker compose exec -T postgres sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > media-manager.dump`
- Restore into a fresh database, run `media-manager migrate`, then complete the two live workflow checks before serving traffic.

Do not edit or delete AltMount files through Media Manager. Do not expose the API container port. Back up PostgreSQL, `.env`, and `.env.integrations` together; protect all three as secrets and restore the environment files before recreating the API container.
