#!/usr/bin/env bash
set -euo pipefail

# Verifies the public Media Manager origin without changing server state.
#
# The supported public architecture is Cosmos Cloud:
#   /api/...    -> http://media-manager-api-1:8080
#   /health/... -> http://media-manager-api-1:8080
#   /           -> http://media-manager-ui-1:3000
# Cosmos authenticates the owner with OpenID and injects the owner bearer token
# server-side on the /api route. The browser must never see an HTTP Basic
# challenge and /api/* must never reach the UI container, which would answer it
# with a bare HTTP 404 ("Media Manager returned HTTP 404").

usage() {
  cat <<'EOF'
Usage: scripts/smoke-public-edge.sh <public-origin>

Verify that the public Media Manager origin is behind an authentication gate
and that /api/v1/* is routed to the API and not to the UI container.

  <public-origin>   e.g. https://media-manager.example.com

Environment:
  MEDIA_MANAGER_PUBLIC_ORIGIN    alternative to the positional argument
  MEDIA_MANAGER_PUBLIC_INSECURE  set to 1 to accept a self-signed TLS chain
EOF
}

origin=''
for argument in "$@"; do
  case "${argument}" in
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

curl_args=(
  --disable --silent --show-error --noproxy '*' --max-redirs 0
  --connect-timeout 5 --max-time 20 --proto '=https,http'
)
if [[ "${MEDIA_MANAGER_PUBLIC_INSECURE:-}" == '1' ]]; then
  curl_args+=(--insecure)
fi

status="$(curl "${curl_args[@]}" -X GET \
  --dump-header "${headers_file}" --output "${body_file}" \
  --write-out '%{http_code}' "${origin}/api/v1/dashboard" || true)"
headers="$(tr -d '\r' < "${headers_file}")"

case "${status}" in
  401)
    if grep -Eiq '^www-authenticate:[[:space:]]*basic' <<<"${headers}"; then
      printf 'FAIL: %s still presents an HTTP Basic challenge. The public edge must use Cosmos OpenID, not gateway Basic Auth.\n' "${origin}" >&2
      exit 1
    fi
    printf 'PASS: %s/api/v1/dashboard is behind an authentication gate (HTTP 401).\n' "${origin}"
    ;;
  301|302|303|307|308)
    printf 'PASS: %s/api/v1/dashboard is behind the Cosmos authentication gate (HTTP %s).\n' "${origin}" "${status}"
    ;;
  404)
    printf 'FAIL: %s/api/v1/dashboard returned HTTP 404.\n' "${origin}" >&2
    printf 'The public route is reaching the UI container directly. Route /api and /health\n' >&2
    printf 'to http://media-manager-api-1:8080 and keep only / on http://media-manager-ui-1:3000.\n' >&2
    exit 1
    ;;
  200)
    printf 'FAIL: %s/api/v1/dashboard returned HTTP 200 without an authentication gate.\n' "${origin}" >&2
    printf 'The API must stay behind the Cosmos owner login; never expose it publicly unauthenticated.\n' >&2
    exit 1
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

for path in / /media /api/v1/system/info /api/v1/system/health /health/ready; do
  probe_status="$(curl "${curl_args[@]}" -X GET --output /dev/null \
    --write-out '%{http_code}' "${origin}${path}" || true)"
  if [[ "${probe_status}" == '404' || "${probe_status}" == '000' ]]; then
    printf 'FAIL: %s%s returned HTTP %s.\n' "${origin}" "${path}" "${probe_status}" >&2
    exit 1
  fi
done
printf 'PASS: public route shape is intact for /, /media, /api/v1/system/info, /api/v1/system/health, and /health/ready.\n'

printf 'Public edge routing check complete.\n'
