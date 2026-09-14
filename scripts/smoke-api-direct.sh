#!/usr/bin/env bash
set -euo pipefail

# Read-only direct API acceptance against the loopback listener.
#
# Cosmos Cloud reaches the API container directly; this script exercises the
# same owner-authenticated read models on 127.0.0.1:${MEDIA_MANAGER_API_PORT}
# without going through the public edge. It never prints the owner token.

api_port="${MEDIA_MANAGER_API_PORT:-28081}"
api_container="${MEDIA_MANAGER_API_CONTAINER:-media-manager-api-1}"

if ! [[ "${api_port}" =~ ^[0-9]+$ ]] || (( 10#${api_port} < 1 || 10#${api_port} > 65535 )); then
  printf 'MEDIA_MANAGER_API_PORT must be a valid TCP port.\n' >&2
  exit 2
fi

owner_token="${OWNER_TOKEN:-}"
if [[ -z "${owner_token}" ]] && command -v docker >/dev/null 2>&1; then
  owner_token="$(docker inspect "${api_container}" --format '{{range .Config.Env}}{{println .}}{{end}}' 2>/dev/null | sed -n 's/^OWNER_TOKEN=//p')"
fi
if [[ -z "${owner_token}" ]]; then
  printf 'OWNER_TOKEN is unavailable. Set it in the environment or run against a host with the %s container.\n' "${api_container}" >&2
  exit 1
fi

origin="http://127.0.0.1:${api_port}"
body_file="$(mktemp)"
chmod 600 "${body_file}"
trap 'rm -f -- "${body_file}"' EXIT

unauth_status="$(curl -sS --noproxy '*' --max-time 15 -o /dev/null -w '%{http_code}' "${origin}/api/v1/dashboard")"
if [[ "${unauth_status}" != '401' ]]; then
  printf 'FAIL: unauthenticated %s/api/v1/dashboard returned HTTP %s; expected 401.\n' "${origin}" "${unauth_status}" >&2
  exit 1
fi
printf 'PASS: unauthenticated API read is rejected with HTTP 401.\n'

check_json() {
  local path="$1"
  local pattern="$2"
  local status
  status="$(curl -sS --noproxy '*' --max-time 15 \
    -H "Authorization: Bearer ${owner_token}" \
    -o "${body_file}" -w '%{http_code}' "${origin}${path}")"
  if [[ "${status}" != '200' ]]; then
    printf 'FAIL: %s%s returned HTTP %s.\n' "${origin}" "${path}" "${status}" >&2
    exit 1
  fi
  if ! grep -Eq "${pattern}" "${body_file}"; then
    printf 'FAIL: %s%s did not return the expected JSON shape.\n' "${origin}" "${path}" >&2
    exit 1
  fi
  printf 'PASS: %s%s -> HTTP 200\n' "${origin}" "${path}"
}

check_json '/health/ready' '"status"[[:space:]]*:[[:space:]]*"ready"'
check_json '/api/v1/dashboard' '"counts"'
check_json '/api/v1/system/info' '"schema_version"'
check_json '/api/v1/system/health' '"database_state"[[:space:]]*:[[:space:]]*"ready"'

printf 'Direct API acceptance complete.\n'
