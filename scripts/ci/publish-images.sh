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
  printf '\nPublished `%s`\n' "${image}" >>"${GITHUB_STEP_SUMMARY:?}"
done
