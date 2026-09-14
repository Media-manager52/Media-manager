#!/usr/bin/env bash
set -euo pipefail

repository_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
environment_file="${MEDIA_MANAGER_ENV_FILE:-${repository_dir}/.env}"

usage() {
  cat <<'EOF'
Usage: scripts/smoke-public-edge.sh <public-origin> [--authenticated]

Verify that the public Media Manager origin is protected by an authentication
gate and that /api/v1/* does NOT reach the UI container directly. The check is
read-only and never mutates server state.

  <public-origin>   e.g. https://media-manager.example.com
  --authenticated   also prompt for the owner password and verify, for an
                    HTTP Basic origin only, that the public origin serves the
                    UI document and the API from the same origin. Cosmos Cloud
                    uses OpenID, so use the read-only routing check there and
                    verify the authenticated browser separately.

Environment:
  MEDIA_MANAGER_PUBLIC_ORIGIN    alternative to the positional argument
  MEDIA_MANAGER_PUBLIC_INSECURE  set to 1 to accept a self-signed TLS chain

The supported public architecture is Cosmos Cloud as the owner-authenticated
edge: /api and /health route to http://media-manager-api-1:8080 and / routes to
http://media-manager-ui-1:3000, with the owner token injected server-side on the
/api route. The legacy loopback Caddy gateway (http://127.0.0.1:${MEDIA_MANAGER_PORT})
still works for local acceptance. In every case a public origin pointed at the
ui container serves the UI document but returns a bare HTTP 404 for /api/v1/*,
which the browser reports as "Media Manager returned HTTP 404".
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

curl_args() {
  printf '%s\n' \
    --disable \
    --silent \
    --show-error \
    --noproxy '*' \
    --max-redirs 0 \
    --connect-timeout 5 \
    --max-time 20
  if [[ "${MEDIA_MANAGER_PUBLIC_INSECURE:-}" == '1' ]]; then
    printf '%s\n' --insecure
  fi
  printf '%s\n' --proto '=https,http'
}

request() {
  local method="$1"
  local url="$2"
  local headers_file="$3"
  local body_file="$4"
  local auth_config="${5:-}"

  local -a args
  mapfile -t args < <(curl_args)
  if [[ -n "${auth_config}" ]]; then
    curl "${args[@]}" -X "${method}" \
      --config "${auth_config}" \
      --dump-header "${headers_file}" \
      --output "${body_file}" \
      --write-out '%{http_code}' \
      "${url}"
  else
    curl "${args[@]}" -X "${method}" \
      --dump-header "${headers_file}" \
      --output "${body_file}" \
      --write-out '%{http_code}' \
      "${url}"
  fi
}

authenticated='0'
origin=''
for argument in "$@"; do
  case "${argument}" in
    --authenticated) authenticated='1' ;;
    --help|-h) usage; exit 0 ;;
    -*) usage >&2; exit 2 ;;
    *) origin="${argument}" ;;
  esac
done
origin="${origin:-${MEDIA_MANAGER_PUBLIC_ORIGIN:-}}"
if [[ -z "${origin}" ]]; then
  usage >&2
  exit 2
fi
if [[ "${origin}" != http://* && "${origin}" != https://* ]]; then
  printf 'The public origin must start with http:// or https://.\n' >&2
  exit 2
fi
origin="${origin%/}"

headers_file="$(mktemp)"
body_file="$(mktemp)"
chmod 600 "${headers_file}" "${body_file}"
trap 'rm -f -- "${headers_file}" "${body_file}"' EXIT

status="$(request GET "${origin}/api/v1/dashboard" "${headers_file}" "${body_file}" || true)"
headers="$(tr -d '\r' < "${headers_file}")"

case "${status}" in
  401)
    if grep -Eiq '^www-authenticate:[[:space:]]*basic' <<<"${headers}"; then
      printf 'PASS: %s/api/v1/dashboard is behind the gateway owner login (HTTP 401 Basic).\n' "${origin}"
    else
      printf 'FAIL: %s/api/v1/dashboard returned HTTP 401 without a Basic challenge.\n' "${origin}" >&2
      exit 1
    fi
    ;;
  404)
    printf 'FAIL: %s/api/v1/dashboard returned HTTP 404.\n' "${origin}" >&2
    printf 'The public route is reaching the UI container directly. Route /api and /health\n' >&2
    printf 'to the API (or the legacy loopback gateway) and keep only / on the ui container.\n' >&2
    exit 1
    ;;
  200)
    if grep -Eq '"data"' "${body_file}"; then
      printf 'PASS: %s/api/v1/dashboard served the API JSON from the public origin.\n' "${origin}"
    else
      printf 'FAIL: %s/api/v1/dashboard returned HTTP 200 without API JSON.\n' "${origin}" >&2
      exit 1
    fi
    ;;
  301|302|303|307|308)
    printf 'PASS: %s/api/v1/dashboard is behind an authentication gate (HTTP %s); the API route is not the UI container. Authenticated content cannot be inspected without a session.\n' "${origin}" "${status}"
    ;;
  000)
    printf 'FAIL: could not reach %s.\n' "${origin}" >&2
    exit 1
    ;;
  *)
    printf 'FAIL: unexpected HTTP %s from %s/api/v1/dashboard.\n' "${status}" "${origin}" >&2
    exit 1
    ;;
esac

if [[ "${authenticated}" != '1' ]]; then
  printf 'Public edge routing check complete.\n'
  exit 0
fi

if [[ ! -r "${environment_file}" ]]; then
  printf 'Cannot read Media Manager environment file: %s\n' "${environment_file}" >&2
  exit 1
fi
if [[ ! -t 0 ]]; then
  printf 'Run the authenticated check from an interactive terminal.\n' >&2
  exit 1
fi

owner_username="$(read_setting OWNER_USERNAME)"
if [[ -z "${owner_username}" || "${owner_username}" == *:* ]]; then
  printf 'OWNER_USERNAME is missing or invalid for HTTP Basic authentication.\n' >&2
  exit 1
fi

auth_config="$(mktemp)"
chmod 600 "${auth_config}"
trap 'rm -f -- "${headers_file}" "${body_file}" "${auth_config}"' EXIT

IFS= read -r -s -p 'Existing Media Manager owner password: ' owner_password
printf '\n'
if [[ -z "${owner_password}" || ${#owner_password} -gt 4096 ]]; then
  unset owner_password
  printf 'A non-empty bounded owner password is required.\n' >&2
  exit 1
fi
trap 'unset owner_password; rm -f -- "${headers_file}" "${body_file}" "${auth_config}"' EXIT
curl_config_quote "${owner_username}:${owner_password}" > "${auth_config}"
unset owner_password

ui_status="$(request GET "${origin}/" "${headers_file}" "${body_file}" "${auth_config}")"
if [[ "${ui_status}" != '200' ]] || ! grep -Fq 'Media Manager' "${body_file}"; then
  printf 'FAIL: authenticated public UI document check failed (HTTP %s).\n' "${ui_status}" >&2
  exit 1
fi

health_status="$(request GET "${origin}/api/v1/system/health" "${headers_file}" "${body_file}" "${auth_config}")"
if [[ "${health_status}" != '200' ]] || ! grep -Eq '"database_state"[[:space:]]*:[[:space:]]*"ready"' "${body_file}"; then
  printf 'FAIL: authenticated public API health check failed (HTTP %s).\n' "${health_status}" >&2
  exit 1
fi

dashboard_status="$(request GET "${origin}/api/v1/dashboard" "${headers_file}" "${body_file}" "${auth_config}")"
if [[ "${dashboard_status}" != '200' ]] || ! grep -Eq '"counts"' "${body_file}"; then
  printf 'FAIL: authenticated public dashboard check failed (HTTP %s).\n' "${dashboard_status}" >&2
  exit 1
fi

printf 'PASS: public origin serves the UI document and the API from the same gateway origin.\n'
printf 'The original "Media Manager returned HTTP 404" request now succeeds.\n'