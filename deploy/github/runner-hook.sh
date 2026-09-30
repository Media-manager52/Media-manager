#!/usr/bin/env bash
set -euo pipefail
[[ "$(id -un)" == mm-ci ]]
[[ "$(docker info --format '{{json .SecurityOptions}}')" == *rootless* ]]
[[ ! -w /var/run/docker.sock ]]
[[ ! -r /home/vps/media-manager/.env ]]
if [[ "${0##*/}" == *completed* ]]; then
  docker container prune --force
  docker network prune --force
  docker volume prune --all --force
fi
