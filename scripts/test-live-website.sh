#!/usr/bin/env bash
set -euo pipefail

repository_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
environment_file="${MEDIA_MANAGER_ENV_FILE:-${repository_dir}/.env}"

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

if [[ ! -r "${environment_file}" ]]; then
  printf 'Cannot read Media Manager environment file: %s\n' "${environment_file}" >&2
  exit 1
fi
if [[ ! -t 0 ]]; then
  printf 'Run the live website suite from an interactive terminal.\n' >&2
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
if [[ ! -x "${repository_dir}/node_modules/.bin/playwright" ]]; then
  printf 'Playwright is not installed. Run npm ci first.\n' >&2
  exit 1
fi

IFS= read -r -s -p 'Existing Media Manager owner password: ' owner_password
printf '\n'
if [[ -z "${owner_password}" || ${#owner_password} -gt 4096 ]]; then
  unset owner_password
  printf 'A non-empty bounded owner password is required.\n' >&2
  exit 1
fi
trap 'unset owner_password' EXIT

cd -- "${repository_dir}"
MEDIA_MANAGER_LIVE_USERNAME="${owner_username}" \
  MEDIA_MANAGER_LIVE_PORT="${gateway_port}" \
  MEDIA_MANAGER_LIVE_PASSWORD="${owner_password}" \
  node ./node_modules/@playwright/test/cli.js test --config playwright.live.config.ts
status=$?
unset owner_password

if [[ ${status} -eq 0 ]]; then
  printf 'Live read-only website acceptance passed. Screenshots: %s\n' \
    "${repository_dir}/.sites-runtime/live-playwright-results"
fi
exit "${status}"
