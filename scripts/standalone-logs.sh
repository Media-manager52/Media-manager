#!/usr/bin/env bash
# Follow the standalone stack logs.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
MODE=$(sed -n 's/^STANDALONE_MODE=//p' .env | tail -1)
exec docker compose -f compose.yaml -f compose.standalone.yaml --profile "${MODE:-lan}" logs -f --tail=200
