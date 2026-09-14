#!/usr/bin/env bash
set -euo pipefail

# Runs the read-only live browser acceptance suite against the deployed Media
# Manager UI and API through a loopback same-origin proxy that reproduces the
# Cosmos Cloud route shape (/api and /health to the API with the owner token
# injected server-side, everything else to the UI). It never prints the owner
# token and does not require the public Cosmos login.

repository_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
environment_file="${MEDIA_MANAGER_ENV_FILE:-${repository_dir}/.env}"

read_setting() {
  local key="$1"
  local value

  value="$(awk -v key="${key}" '
    index($0, key "=") == 1 { value = substr($0, length(key) + 2) }
    END { print value }
  ' "${environment_file}" 2>/dev/null)"
  value="${value%$'\r'}"
  if [[ ${#value} -ge 2 && ${value:0:1} == '"' && ${value: -1} == '"' ]]; then
    value="${value:1:${#value}-2}"
  elif [[ ${#value} -ge 2 && ${value:0:1} == "'" && ${value: -1} == "'" ]]; then
    value="${value:1:${#value}-2}"
  fi
  printf '%s' "${value}"
}

api_container="${MEDIA_MANAGER_API_CONTAINER:-media-manager-api-1}"
ui_container="${MEDIA_MANAGER_UI_CONTAINER:-media-manager-ui-1}"
proxy_port="${MEDIA_MANAGER_LIVE_PROXY_PORT:-4175}"

if ! command -v docker >/dev/null 2>&1; then
  printf 'docker is required to resolve the running Media Manager containers.\n' >&2
  exit 1
fi
if [[ ! -x "${repository_dir}/node_modules/.bin/playwright" ]]; then
  printf 'Playwright is not installed. Run npm ci first.\n' >&2
  exit 1
fi
if ! [[ "${proxy_port}" =~ ^[0-9]+$ ]] || (( 10#${proxy_port} < 1 || 10#${proxy_port} > 65535 )); then
  printf 'MEDIA_MANAGER_LIVE_PROXY_PORT must be a valid TCP port.\n' >&2
  exit 2
fi

api_port="$(read_setting MEDIA_MANAGER_API_PORT)"
api_port="${api_port:-28081}"
if ! [[ "${api_port}" =~ ^[0-9]+$ ]] || (( 10#${api_port} < 1 || 10#${api_port} > 65535 )); then
  printf 'MEDIA_MANAGER_API_PORT must be a valid TCP port.\n' >&2
  exit 2
fi

owner_token="$(docker inspect "${api_container}" --format '{{range .Config.Env}}{{println .}}{{end}}' 2>/dev/null | sed -n 's/^OWNER_TOKEN=//p')"
if [[ -z "${owner_token}" ]]; then
  unset owner_token
  printf 'Could not read OWNER_TOKEN from the %s container.\n' "${api_container}" >&2
  exit 1
fi

ui_ip="$(docker inspect "${ui_container}" --format '{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}' 2>/dev/null | awk '{print $1}')"
if [[ -z "${ui_ip}" ]]; then
  unset owner_token
  printf 'Could not resolve the %s container address.\n' "${ui_container}" >&2
  exit 1
fi

MM_OWNER_TOKEN="${owner_token}" \
  MM_API_TARGET="127.0.0.1:${api_port}" \
  MM_UI_TARGET="${ui_ip}:3000" \
  MM_PROXY_PORT="${proxy_port}" \
  node "${repository_dir}/scripts/live-acceptance-proxy.mjs" &
proxy_pid=$!
unset owner_token
trap 'kill "${proxy_pid}" 2>/dev/null || true' EXIT

ready='0'
for _ in $(seq 1 40); do
  if ! kill -0 "${proxy_pid}" 2>/dev/null; then
    printf 'The acceptance proxy exited before becoming ready.\n' >&2
    exit 1
  fi
  if curl -sS --noproxy '*' --max-time 2 -o /dev/null "http://127.0.0.1:${proxy_port}/health/ready"; then
    ready='1'
    break
  fi
  sleep 0.25
done
if [[ "${ready}" != '1' ]]; then
  printf 'The acceptance proxy did not become ready.\n' >&2
  exit 1
fi

cd -- "${repository_dir}"
MEDIA_MANAGER_LIVE_PORT="${proxy_port}" \
  node ./node_modules/@playwright/test/cli.js test --config playwright.live.config.ts
status=$?

if [[ ${status} -eq 0 ]]; then
  printf 'Live read-only website acceptance passed. Screenshots: %s\n' \
    "${repository_dir}/.sites-runtime/live-playwright-results"
fi
exit "${status}"
