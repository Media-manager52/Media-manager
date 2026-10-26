#!/usr/bin/env bash
# Interactive first-time setup for a self-hosted Media Manager.
#
# It writes only local, uncommitted files (.env, .env.integrations), generates
# the owner/API/database/backup secrets, creates the integration network, and
# starts the stack with the Caddy edge. Existing files are never overwritten.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

NO_START=false
case "${1:-}" in
	--no-start) NO_START=true ;;
	--help|-h)
		printf 'Usage: %s [--no-start]\n' "$0"
		printf 'Writes .env and .env.integrations, then builds and starts the stack.\n'
		printf 'Use --no-start to review the generated files first.\n'
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

[[ -e .env ]] && die ".env already exists. Use ./scripts/standalone-up.sh to start the existing installation."
[[ -e .env.integrations ]] && die ".env.integrations already exists. Move it aside before running setup again."

prompt() {
	local label="$1" default="$2" value
	read -r -p "$label [$default]: " value
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

say "Media Manager standalone setup"
say "This creates .env and .env.integrations with mode 600. Nothing is sent anywhere."
if [[ "$(id -u)" == "0" ]]; then
	warn "You are running as root. The API will run as root; prefer an unprivileged user with Docker access."
fi
say
MEDIA_ROOT=$(prompt "Parent folder for the movie and TV libraries" "$HOME/media")
MOVIES=$(prompt "Movie library folder on this host" "$MEDIA_ROOT/movies")
TV=$(prompt "TV library folder on this host" "$MEDIA_ROOT/tv")
ALT_PARENT=$(prompt "Host folder that contains AltMount's FUSE mount" "/mnt")
NETWORK=$(prompt "Docker network shared with AltMount, Plex and Seerr" "media-manager")
ALT_CONTAINER=$(prompt "AltMount container name (blank if not installed yet)" "altmount")
PLEX_CONTAINER=$(prompt "Plex container name (blank if Plex is not a container)" "plex")
SEERR_CONTAINER=$(prompt "Seerr/Overseerr container name (blank if none)" "seerr")

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
MODE=$(prompt "Access mode: lan (HTTP on a port) or https (domain with automatic TLS)" "lan")
case "$MODE" in
	lan|https) ;;
	*) die "Access mode must be lan or https" ;;
esac
DOMAIN=""
if [[ "$MODE" == "https" ]]; then
	DOMAIN=$(prompt "Domain name (DNS must already point to this server)" "")
	DOMAIN=${DOMAIN#http://}; DOMAIN=${DOMAIN#https://}; DOMAIN=${DOMAIN%%/*}
	[[ -n "$DOMAIN" ]] || die "A domain is required for https mode"
	[[ "$DOMAIN" =~ ^[A-Za-z0-9.-]+$ ]] || die "The domain may contain only letters, digits, dot and dash"
	SITE="$DOMAIN"
else
	SITE=":8080"
fi
HTTP_PORT=$(prompt "LAN HTTP port (only used in lan mode)" "8080")
[[ "$HTTP_PORT" =~ ^[0-9]+$ ]] || die "The HTTP port must be a number"
API_PORT=$(prompt "Loopback API port for machine checks (must be free)" "28081")
[[ "$API_PORT" =~ ^[0-9]+$ ]] || die "The API port must be a number"

say
BASIC_AUTH_USER=$(prompt "Owner login name" "owner")
[[ "$BASIC_AUTH_USER" =~ ^[A-Za-z0-9._-]+$ ]] || die "The login name may contain only letters, digits, dot, underscore and dash"
while :; do
	read -r -s -p "Owner login password (at least 12 characters): " PASSWORD1
	printf '\n'
	read -r -s -p "Repeat the password: " PASSWORD2
	printf '\n'
	[[ ${#PASSWORD1} -ge 12 ]] || { warn "The password must be at least 12 characters."; continue; }
	[[ "$PASSWORD1" != *:* ]] || { warn "The password cannot contain a colon."; continue; }
	[[ "$PASSWORD1" == "$PASSWORD2" ]] || { warn "The passwords do not match."; continue; }
	break
done

say "Preparing the Caddy edge (the caddy image may be downloaded once)..."
BASIC_AUTH_HASH=$(docker run --rm caddy:2.10-alpine caddy hash-password --plaintext="$PASSWORD1") || die "Could not generate the basic-auth hash"
[[ "$BASIC_AUTH_HASH" == \$2* ]] || die "Caddy returned an unexpected password hash"

say
say "Optional secrets. You can leave these blank and edit .env.integrations later."
PLEX_TOKEN=$(prompt_secret "Plex token")
NEWZNAB_KEY=$(prompt_secret "First Newznab API key")

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
MEDIA_MANAGER_VERSION=standalone
MEDIA_MANAGER_COMMIT=$COMMIT
OWNER_TOKEN=$OWNER_TOKEN
MEDIA_MANAGER_API_PORT=$API_PORT
COMMISSIONING_V2_OWNER_ORIGIN=http://127.0.0.1:$API_PORT
MEDIA_NETWORK=$NETWORK
ALTMOUNT_CONTAINER=$ALT_CONTAINER
PLEX_CONTAINER=$PLEX_CONTAINER
SEERR_CONTAINER=$SEERR_CONTAINER
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
STANDALONE_MODE=$MODE
MEDIA_MANAGER_SITE=$SITE
MEDIA_MANAGER_DOMAIN=$DOMAIN
BASIC_AUTH_USER=$BASIC_AUTH_USER
BASIC_AUTH_HASH='$BASIC_AUTH_HASH'
MEDIA_MANAGER_HTTP_PORT=$HTTP_PORT
MEDIA_MANAGER_HTTPS_PORT=443
MEDIA_MANAGER_ACME_PORT=80
ENV
chmod 600 .env

cat > .env.integrations <<ENV
# Secret values referenced by integration records in the UI. Edit this file and
# recreate only the API service after changing a value.
ALTMOUNT_SECRET=
PLEX_TOKEN=$PLEX_TOKEN
NEWZNAB_PRIMARY_API_KEY=$NEWZNAB_KEY
NEWZNAB_SECONDARY_API_KEY=
TMDB_ACCESS_TOKEN=
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

if $NO_START; then
	say "Setup files written. Review .env and .env.integrations, then run ./scripts/standalone-up.sh."
	exit 0
fi

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
docker compose -f compose.yaml -f compose.standalone.yaml --profile "$MODE" config --quiet

say "Building and starting Media Manager. The first build can take 10-20 minutes..."
docker compose -f compose.yaml -f compose.standalone.yaml --profile "$MODE" up -d --build

HOST_IP=$(hostname -I 2>/dev/null | tr ' ' '\n' | grep -E '^[0-9.]+$' | head -1)
[[ -n "$HOST_IP" ]] || HOST_IP="127.0.0.1"
if [[ "$MODE" == "lan" ]]; then
	URL="http://$HOST_IP:$HTTP_PORT"
	READY=false
	for _ in $(seq 1 90); do
		if curl -fsS -u "$BASIC_AUTH_USER:$PASSWORD1" "http://127.0.0.1:$HTTP_PORT/api/v1/system/info" >/dev/null 2>&1; then
			READY=true
			break
		fi
		sleep 2
	done
	if ! $READY; then
		warn "The edge did not answer within three minutes. Check ./scripts/standalone-logs.sh."
	fi
else
	URL="https://$DOMAIN"
fi

say
say "Media Manager is starting."
say "Open:       $URL"
say "Login user: $BASIC_AUTH_USER"
say "Password:   the password you chose"
say
say "Secrets are in .env and .env.integrations (mode 600)."
say "Back up BACKUP_ENCRYPTION_KEY from .env.integrations; without it encrypted backups cannot be restored."
say "Next steps: docs/getting-started.md"
if [[ "$MODE" == "lan" ]]; then
	say "If the page is not ready yet, run: ./scripts/standalone-logs.sh"
fi
