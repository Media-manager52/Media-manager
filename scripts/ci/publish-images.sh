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
# pullable. The publish job has packages: write. Try the repo-scoped package
# endpoint first, then the user-scoped endpoint; a token without package
# metadata permission only produces a warning and leaves the current visibility.
for component in api ui; do
  package="media-manager-${component}"
  api_args=(
    --disable --silent --show-error --max-time 30
    -X PATCH
    -H "Authorization: Bearer ${GHCR_TOKEN}"
    -H "Accept: application/vnd.github+json"
    -H "X-GitHub-Api-Version: 2022-11-28"
    -H "Content-Type: application/json"
    --data '{"visibility":"public"}'
  )
  if curl "${api_args[@]}" "https://api.github.com/repos/${GITHUB_REPOSITORY}/packages/container/${package}" >/dev/null 2>&1 \
     || curl "${api_args[@]}" "https://api.github.com/user/packages/container/${package}" >/dev/null 2>&1; then
    printf 'Made `%s` publicly pullable for the Cosmos market.\n' "${package}"
  else
    printf '::warning::Could not set `%s` public automatically. Set it at https://github.com/users/%s/packages/container/%s/settings\n' "${package}" "${GITHUB_REPOSITORY_OWNER}" "${package}"
  fi
done
