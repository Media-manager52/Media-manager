#!/usr/bin/env bash
set -euo pipefail

repository_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
environment_file="${MEDIA_MANAGER_ENV_FILE:-${repository_dir}/.env}"

usage() {
  cat <<'EOF'
Usage: scripts/smoke-gateway-auth.sh

Performs read-only authenticated checks through the loopback Media Manager
gateway. The existing owner password is prompted for silently and is never
stored or passed as a command argument.
EOF
}

read_setting() {
  local key="$1"
  local value

  value="$(awk -v key="${key}" '
    index($0, key "=") == 1 { value = substr($0, length(key) + 2) }
    END { print value }
  ' "${environment_file}")"
  value="${value%$'\r'}"

  if [[ ${#value} -ge 2 && ${value:0:1} == '"' && ${value: -1} == '"' ]]; then
    value="${value:1:${#value}-2}"
  elif [[ ${#value} -ge 2 && ${value:0:1} == "'" && ${value: -1} == "'" ]]; then
    value="${value:1:${#value}-2}"
  fi

  printf '%s' "${value}"
}

curl_config_quote() {
  local value="$1"

  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  value="${value//$'\t'/\\t}"
  value="${value//$'\r'/\\r}"
  value="${value//$'\n'/\\n}"
  printf 'user = "%s"\n' "${value}"
}

curl_status() {
  local output_file="$1"
  local url="$2"
  shift 2

  curl \
    --disable \
    --silent \
    --show-error \
    --noproxy '*' \
    --proto '=http' \
    --max-redirs 0 \
    --connect-timeout 3 \
    --max-time 15 \
    --output "${output_file}" \
    --write-out '%{http_code}' \
    "$@" \
    "${url}"
}

authenticated_status() {
  local output_file="$1"
  local url="$2"

  curl_config_quote "${owner_username}:${owner_password}" |
    curl_status "${output_file}" "${url}" --config -
}

case "${1:-}" in
  '') ;;
  --help|-h) usage; exit 0 ;;
  *) usage >&2; exit 2 ;;
esac

if [[ ! -r "${environment_file}" ]]; then
  printf 'Cannot read Media Manager environment file: %s\n' "${environment_file}" >&2
  exit 1
fi
if [[ ! -t 0 ]]; then
  printf 'Run this check from an interactive terminal.\n' >&2
  exit 1
fi
if ! command -v curl >/dev/null 2>&1; then
  printf 'curl is required.\n' >&2
  exit 1
fi

owner_username="$(read_setting OWNER_USERNAME)"
gateway_port="$(read_setting MEDIA_MANAGER_PORT)"
gateway_port="${gateway_port:-8080}"

if [[ -z "${owner_username}" || "${owner_username}" == *:* ]]; then
  printf 'OWNER_USERNAME is missing or invalid for HTTP Basic authentication.\n' >&2
  exit 1
fi
if [[ ! "${gateway_port}" =~ ^[0-9]+$ ]] || \
  (( 10#${gateway_port} < 1 || 10#${gateway_port} > 65535 )); then
  printf 'MEDIA_MANAGER_PORT must be a valid TCP port.\n' >&2
  exit 1
fi

gateway_origin="http://127.0.0.1:${gateway_port}"
ui_response="$(mktemp)"
api_response="$(mktemp)"
chmod 600 "${ui_response}" "${api_response}"
trap 'rm -f -- "${ui_response}" "${api_response}"' EXIT

unauthenticated_status="$(curl_status "${ui_response}" "${gateway_origin}/media/series")"
if [[ "${unauthenticated_status}" != '401' ]]; then
  printf 'Gateway protection check failed: expected HTTP 401, received %s.\n' \
    "${unauthenticated_status}" >&2
  exit 1
fi

read -r -s -p 'Existing Media Manager owner password: ' owner_password
printf '\n'

ui_status="$(authenticated_status "${ui_response}" "${gateway_origin}/media/series")"
if [[ "${ui_status}" != '200' ]] || ! grep -Fq 'TV Shows' "${ui_response}"; then
  unset owner_password
  printf 'Authenticated gateway-to-UI check failed (HTTP %s).\n' "${ui_status}" >&2
  exit 1
fi

api_status="$(authenticated_status "${api_response}" "${gateway_origin}/api/v1/system/health")"
unset owner_password
if [[ "${api_status}" != '200' ]] || \
  ! grep -Eq '"database_state"[[:space:]]*:[[:space:]]*"ready"' "${api_response}"; then
  printf 'Authenticated gateway-to-API check failed (HTTP %s).\n' "${api_status}" >&2
  exit 1
fi

printf 'Authenticated local gateway smoke passed (protection, UI, and API readiness).\n'
