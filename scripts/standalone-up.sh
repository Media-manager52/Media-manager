#!/usr/bin/env bash
# Build and start the standalone stack using the mode written by setup.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
[[ -f .env ]] || { printf 'Error: .env not found. Run ./scripts/setup-standalone.sh first.\n' >&2; exit 1; }
MODE=$(sed -n 's/^STANDALONE_MODE=//p' .env | tail -1)
case "$MODE" in lan|https) ;; *) printf 'Error: STANDALONE_MODE must be lan or https in .env.\n' >&2; exit 1 ;; esac
NETWORK=$(sed -n 's/^MEDIA_NETWORK=//p' .env | tail -1)
[[ -n "$NETWORK" ]] || { printf 'Error: MEDIA_NETWORK is missing from .env.\n' >&2; exit 1; }
docker network inspect "$NETWORK" >/dev/null 2>&1 || docker network create "$NETWORK" >/dev/null
for key in ALTMOUNT_CONTAINER PLEX_CONTAINER SEERR_CONTAINER; do
	container=$(sed -n "s/^$key=//p" .env | tail -1)
	[[ -n "$container" ]] || continue
	if docker inspect "$container" >/dev/null 2>&1; then
		docker network connect "$NETWORK" "$container" >/dev/null 2>&1 || true
	fi
done
docker compose -f compose.yaml -f compose.standalone.yaml --profile "$MODE" config --quiet
exec docker compose -f compose.yaml -f compose.standalone.yaml --profile "$MODE" up -d --build
