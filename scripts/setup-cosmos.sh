#!/usr/bin/env bash
# First-time setup for Media Manager behind Cosmos Cloud.
#
# Writes .env and .env.integrations with mode 600, generates the owner/database/
# backup secrets, creates the shared integrations network, builds and starts the
# stack, and prints the three Cosmos Cloud routes. It never modifies Cosmos
# itself: the owner adds the routes in the Cosmos UI.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

NO_START=false
case "${1:-}" in
	--no-start) NO_START=true ;;
	--help|-h)
		printf 'Usage: %s [--no-start]\n' "$0"
		printf 'Writes .env and .env.integrations, prepares the Docker network, then builds and starts the stack.\n'
		printf 'Use --no-start to review the generated files before starting the stack.\n'
		exit 0
		;;
	"") ;;
	*) printf 'Unknown option: %s\n' "$1" >&2; exit 2 ;;
esac

say() { printf '%s\n' "$*"; }
warn() { printf 'Warning: %s\n' "$*" >&2; }
die() { printf 'Error: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required"; }

need docker
docker compose version >/dev/null 2>&1 || die "Docker Compose v2 is required"
need openssl
need curl

[[ -e .env ]] && die ".env already exists. Use docker compose for updates, or move it aside before running setup again."
[[ -e .env.integrations ]] && die ".env.integrations already exists. Move it aside before running setup again."

prompt() {
	local label="$1" default="$2" value
	if [[ -n "$default" ]]; then
		read -r -p "$label [$default]: " value
	else
		read -r -p "$label: " value
	fi
	printf '%s' "${value:-$default}"
}
prompt_secret() {
	local label="$1" value
	read -r -s -p "$label (leave blank to configure later): " value
	printf '\n' >&2
	printf '%s' "$value"
}
require_absolute() {
	case "$1" in
		/*) ;;
		*) die "$2 must be an absolute path" ;;
	esac
}

say "Media Manager setup for Cosmos Cloud"
say "This creates .env and .env.integrations with mode 600. Nothing is sent anywhere."
if [[ "$(id -u)" == "0" ]]; then
	warn "You are running as root. The API will run as root; prefer an unprivileged user with Docker access."
fi
if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet CosmosCloud 2>/dev/null; then
	say "Cosmos Cloud is running."
else
	warn "Cosmos Cloud was not detected as a running CosmosCloud service. Install/start it and add the routes; the stack can still be prepared."
fi
say
MEDIA_ROOT=$(prompt "Parent folder for the movie and TV libraries" "$HOME/media")
MOVIES=$(prompt "Movie library folder on this host" "$MEDIA_ROOT/movies")
TV=$(prompt "TV library folder on this host" "$MEDIA_ROOT/tv")
ALT_PARENT=$(prompt "Host folder that contains AltMount's FUSE mount" "/mnt")
NETWORK=$(prompt "Docker network shared with AltMount, Plex and Seerr" "media-stack")
ALT_CONTAINER=$(prompt "AltMount container name (blank if not installed yet)" "")
PLEX_CONTAINER=$(prompt "Plex container name (blank if Plex is not a container)" "")
SEERR_CONTAINER=$(prompt "Seerr/Overseerr container name (blank if none)" "")

require_absolute "$MEDIA_ROOT" "The media parent"
require_absolute "$MOVIES" "The movie folder"
require_absolute "$TV" "The TV folder"
require_absolute "$ALT_PARENT" "The AltMount parent folder"
[[ -d "$ALT_PARENT" ]] || die "AltMount parent $ALT_PARENT does not exist. Create it (or correct the path) and rerun."
for folder in "$MEDIA_ROOT" "$MOVIES" "$TV"; do
	mkdir -p "$folder" 2>/dev/null || warn "Could not create $folder; create it yourself before adding media."
done
MEDIA_LIBRARY="$MEDIA_ROOT"
mkdir -p "$MEDIA_LIBRARY" 2>/dev/null || true
mkdir -p data/backups

say
PUBLIC_ORIGIN=$(prompt "Cosmos public origin for Media Manager (e.g. https://media-manager.example.com)" "")
[[ -n "$PUBLIC_ORIGIN" ]] || die "The Cosmos public origin is required"
PUBLIC_ORIGIN=${PUBLIC_ORIGIN%/}
[[ "$PUBLIC_ORIGIN" =~ ^https?://[A-Za-z0-9.-]+(:[0-9]+)?$ ]] || die "The public origin must be a URL such as https://media-manager.example.com (no path)"
if [[ "$PUBLIC_ORIGIN" != https://* ]]; then
	warn "Use HTTPS for the public Cosmos origin; HTTP exposes the owner session."
fi
API_PORT=$(prompt "Loopback API port for machine checks (must be free)" "28081")
[[ "$API_PORT" =~ ^[0-9]+$ ]] || die "The API port must be a number"

say
say "Optional secrets. You can leave these blank and edit .env.integrations later."
PLEX_TOKEN=$(prompt_secret "Plex token")
NEWZNAB_KEY=$(prompt_secret "First Newznab API key")
TMDB_TOKEN=$(prompt_secret "TMDB API read access token (recommended)")

OWNER_TOKEN=$(openssl rand -hex 32)
POSTGRES_PASSWORD=$(openssl rand -hex 24)
BACKUP_KEY=$(openssl rand -base64 32 | tr -d '\n')
OLDBOYS_KEY=$(openssl rand -hex 32)
SERVARR_API_KEY=$(openssl rand -hex 24)
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo shared)

umask 077
cat > .env <<ENV
POSTGRES_DB=media_manager
POSTGRES_USER=media_manager
POSTGRES_PASSWORD=$POSTGRES_PASSWORD
MEDIA_MANAGER_VERSION=local
MEDIA_MANAGER_COMMIT=$COMMIT
# Pin the Compose project name so the Cosmos route targets
# media-manager-api-1 and media-manager-ui-1 stay valid.
COMPOSE_PROJECT_NAME=media-manager
OWNER_TOKEN=$OWNER_TOKEN
MEDIA_MANAGER_API_PORT=$API_PORT
COMMISSIONING_V2_OWNER_ORIGIN=$PUBLIC_ORIGIN
MEDIA_NETWORK=$NETWORK
MEDIA_MANAGER_UID=$(id -u)
MEDIA_MANAGER_GID=$(id -g)
ALTMOUNT_HOST_PARENT_PATH=$ALT_PARENT
MOVIES_HOST_PATH=$MOVIES
TV_HOST_PATH=$TV
MEDIA_LIBRARY_HOST_PATH=$MEDIA_LIBRARY
MEDIA_LIBRARY_GID=$(id -g)
METADATA_TMDB_TOKEN_SOURCE_FILE=
METADATA_TVDB_KEY_SOURCE_FILE=
METADATA_SIMKL_CLIENT_ID_SOURCE_FILE=
SEERR_BASE_URL=
SEERR_API_KEY=
SEERR_INSTANCE_ID=
SEERR_WEBHOOK_SECRET=
SEERR_ALLOW_HTTP=false
SERVARR_API_KEY=$SERVARR_API_KEY
ENV
chmod 600 .env

cat > .env.integrations <<ENV
# Secret values referenced by integration records in the UI. Edit this file and
# recreate only the API service after changing a value.
ALTMOUNT_SECRET=
PLEX_TOKEN=$PLEX_TOKEN
NEWZNAB_PRIMARY_API_KEY=$NEWZNAB_KEY
NEWZNAB_SECONDARY_API_KEY=
TMDB_ACCESS_TOKEN=$TMDB_TOKEN
TVDB_API_KEY=
TVDB_SUBSCRIBER_PIN=
SIMKL_CLIENT_ID=
OLDBOYS_CREDENTIAL_ENCRYPTION_KEY=$OLDBOYS_KEY
BACKUP_ENCRYPTION_KEY=$BACKUP_KEY
BACKUP_OFFSITE_KIND=
BACKUP_OFFSITE_PREFIX=media-manager/backups
BACKUP_OFFSITE_S3_ENDPOINT=
BACKUP_OFFSITE_S3_REGION=
BACKUP_OFFSITE_S3_BUCKET=
BACKUP_OFFSITE_S3_ACCESS_KEY=
BACKUP_OFFSITE_S3_SECRET_KEY=
BACKUP_OFFSITE_WEBDAV_URL=
BACKUP_OFFSITE_WEBDAV_USERNAME=
BACKUP_OFFSITE_WEBDAV_PASSWORD=
BACKUP_OFFSITE_ALLOW_HTTP=false
ENV
chmod 600 .env.integrations

if ! docker network inspect "$NETWORK" >/dev/null 2>&1; then
	say "Creating Docker network $NETWORK..."
	docker network create "$NETWORK" >/dev/null
fi
for container in "$ALT_CONTAINER" "$PLEX_CONTAINER" "$SEERR_CONTAINER"; do
	[[ -n "$container" ]] || continue
	if docker inspect "$container" >/dev/null 2>&1; then
		docker network connect "$NETWORK" "$container" >/dev/null 2>&1 || true
		say "Connected $container to $NETWORK."
	else
		warn "Container '$container' was not found. Connect it later with: docker network connect $NETWORK $container"
	fi
done

say "Validating the Compose configuration..."
docker compose config --quiet

print_routes() {
	say
	say "Cosmos Cloud routes (same host, in this order in the Cosmos UI; catch-all last):"
	say "  /api     -> http://media-manager-api-1:8080   auth: on   ExtraHeaders: Authorization: Bearer <OWNER_TOKEN from .env>"
	say "  /health  -> http://media-manager-api-1:8080   auth: on"
	say "  /       -> http://media-manager-ui-1:3000    auth: on   catch-all"
	say "Copy OWNER_TOKEN from .env; never paste it into browser code or commit it."
	say "Public origin: $PUBLIC_ORIGIN"
	say "After the routes work, verify with: scripts/smoke-public-edge.sh $PUBLIC_ORIGIN"
}

if $NO_START; then
	say "Setup files written. Review .env and .env.integrations, then run: docker compose up -d --build"
	print_routes
	exit 0
fi

say "Building and starting Media Manager. The first build can take 10-20 minutes..."
docker compose up -d --build

READY=false
for _ in $(seq 1 90); do
	if curl -fsS "http://127.0.0.1:$API_PORT/health/ready" >/dev/null 2>&1; then
		READY=true
		break
	fi
	sleep 2
done
if ! $READY; then
	warn "The API did not become ready within three minutes. Check: docker compose logs --tail=200"
fi

say
say "Media Manager is running."
print_routes
say
say "Add the three routes in Cosmos, open $PUBLIC_ORIGIN, and sign in with your Cosmos account."
say "Then follow docs/getting-started.md for AltMount, Plex, indexers, metadata, roots and profiles."
