#!/usr/bin/env bash
set -euo pipefail
[[ "${GITHUB_REPOSITORY:-}" == "Media-manager52/Media-manager" ]]
[[ "${GITHUB_REF:-}" == "refs/heads/main" ]]
[[ "${GITHUB_SHA:-}" =~ ^[0-9a-f]{40}$ ]]
[[ "$(docker info --format '{{json .SecurityOptions}}')" == *rootless* ]]
registry_repo="ghcr.io/${GITHUB_REPOSITORY,,}"
export DOCKER_CONFIG
DOCKER_CONFIG="$(mktemp -d "${RUNNER_TEMP:?}/registry-login.XXXXXX")"
trap 'rm -rf -- "${DOCKER_CONFIG}"' EXIT
printf '%s' "${GHCR_TOKEN:?}" | docker login ghcr.io --username "${GITHUB_REPOSITORY_OWNER:?}" --password-stdin
for component in api ui; do
  image="${registry_repo}-${component}:sha-${GITHUB_SHA}"
  docker build --target "${component}" \
    --build-arg "VERSION=github-${GITHUB_SHA:0:12}" --build-arg "COMMIT=${GITHUB_SHA}" \
    --label "org.opencontainers.image.source=https://github.com/${GITHUB_REPOSITORY}" \
    --label "org.opencontainers.image.revision=${GITHUB_SHA}" \
    --tag "${image}" .
  docker push "${image}"
  docker tag "${image}" "${registry_repo}-${component}:main"
  docker push "${registry_repo}-${component}:main"
  docker tag "${image}" "${registry_repo}-${component}:latest"
  docker push "${registry_repo}-${component}:latest"
  printf '\nPublished `%s` (tags: sha, main, latest)\n' "${image}" >>"${GITHUB_STEP_SUMMARY:?}"
done

# The Cosmos market installs from these packages, so they must be publicly
# pullable. A direct manifest request without a bearer token returns 401 even
# for a public GHCR package, so verify with an anonymous pull token first. Only
# attempt the metadata PATCH when the package is actually not public.
public_pull() {
  local package="$1"
  local token
  token="$(curl -sS --max-time 15 "https://ghcr.io/token?service=ghcr.io&scope=repository:${registry_repo}/${package}:pull" | jq -r '.token // empty')"
  [[ -n "${token}" ]] || return 1
  curl -sS --fail --max-time 30 -o /dev/null     -H "Authorization: Bearer ${token}"     -H 'Accept: application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json'     "https://ghcr.io/v2/${registry_repo}/${package}/manifests/main"
}

for component in api ui; do
  package="media-manager-${component}"
  if public_pull "${package}"; then
    printf '`%s` is publicly pullable for the Cosmos market.\n' "${package}"
    continue
  fi
  api_args=(
    --disable --silent --show-error --fail --max-time 30
    -X PATCH
    -H "Authorization: Bearer ${GHCR_TOKEN}"
    -H "Accept: application/vnd.github+json"
    -H "X-GitHub-Api-Version: 2022-11-28"
    -H "Content-Type: application/json"
    --data '{"visibility":"public"}'
  )
  if curl "${api_args[@]}" "https://api.github.com/repos/${GITHUB_REPOSITORY}/packages/container/${package}" >/dev/null 2>&1 \
     || curl "${api_args[@]}" "https://api.github.com/user/packages/container/${package}" >/dev/null 2>&1; then
    :
  fi
  if public_pull "${package}"; then
    printf 'Made `%s` publicly pullable for the Cosmos market.\n' "${package}"
  else
    printf '::warning::Could not verify `%s` as public. Set it at https://github.com/users/%s/packages/container/%s/settings\n' "${package}" "${GITHUB_REPOSITORY_OWNER}" "${package}"
  fi
done
