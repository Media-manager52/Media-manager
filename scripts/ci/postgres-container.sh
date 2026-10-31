#!/usr/bin/env bash
# Disposable PostgreSQL for the backend gate, reachable only over a unix socket.
#
#   postgres-container.sh start   launch the container and export the test URL
#   postgres-container.sh wait    block until the server accepts connections
#   postgres-container.sh stop    stop it and remove its socket directory
#
# Why not a `services:` container with a published port: on the rootless engine
# every query crossed rootlesskit and docker-proxy inside the CI account's
# 5-CPU quota (measured 222 us per round trip versus 62 us over a socket, and
# the Go suite used about 800 CPU-seconds instead of about 270). The server
# listens on no TCP port at all, the socket lives in a private directory under
# RUNNER_TEMP, there is no fixed host port to collide with another job, and the
# password is random per run.
set -euo pipefail

image="${MM_CI_POSTGRES_IMAGE:-postgres:18.6-alpine3.24}"
action="${1:-}"
runner_temp="${RUNNER_TEMP:?RUNNER_TEMP is required}"
name="mm-ci-pg-${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}-${GITHUB_JOB:-job}"
socket_dir="${runner_temp}/${name}-socket"
env_file="${runner_temp}/${name}.env"

if ! [[ "$name" =~ ^[A-Za-z0-9_.-]+$ ]]; then
  echo "unsafe container name: ${name}" >&2
  exit 64
fi
if ! [[ "$socket_dir" =~ ^[A-Za-z0-9_./-]+$ ]]; then
  echo "RUNNER_TEMP must contain only letters, digits and _ . / - (got ${runner_temp})" >&2
  exit 64
fi

reap_stale() {
  # A runner crash can leave a labelled container behind (the stop step is
  # skipped). Remove only old ones, so a concurrent job's database is never hit.
  local id created age
  for id in $(docker ps -aq --filter label=mm-ci=postgres 2>/dev/null); do
    created="$(docker inspect --format '{{.Created}}' "$id" 2>/dev/null || true)"
    age=$(( $(date +%s) - $(date -d "$created" +%s 2>/dev/null || date +%s) ))
    if (( age > 10800 )); then
      echo "removing stale disposable PostgreSQL ${id} (${age}s old)"
      docker rm -f "$id" >/dev/null 2>&1 || true
    fi
  done
}

start() {
  [[ "$(docker info --format '{{json .SecurityOptions}}')" == *rootless* ]] || {
    echo "CI requires its separate rootless Docker engine" >&2
    exit 77
  }
  reap_stale
  docker rm -f "$name" >/dev/null 2>&1 || true
  local password
  password="$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')"
  echo "::add-mask::${password}"
  mkdir -p "$socket_dir"
  # The server runs as an unprivileged container user and creates the socket here.
  chmod 0777 "$socket_dir"
  (umask 077 && printf 'POSTGRES_USER=media_manager\nPOSTGRES_PASSWORD=%s\nPOSTGRES_DB=media_manager_test\n' "$password" >"$env_file")
  # Disposable data only: no durability waits, and enough lock slots for many
  # concurrent DROP SCHEMA ... CASCADE calls (the default 64 fails at about 7).
  docker run -d --name "$name" --label mm-ci=postgres --env-file "$env_file" \
    --volume "${socket_dir}:/var/run/postgresql" --shm-size=256m --stop-timeout 30 "$image" \
    postgres -c listen_addresses= -c max_locks_per_transaction=256 -c max_connections=200 \
    -c fsync=off -c synchronous_commit=off -c full_page_writes=off >/dev/null
  rm -f "$env_file"
  local url="postgres://media_manager:${password}@/media_manager_test?host=${socket_dir}&sslmode=disable"
  if [[ -n "${GITHUB_ENV:-}" ]]; then
    {
      echo "MEDIA_MANAGER_TEST_DATABASE_URL=${url}"
      echo "DATABASE_URL=${url}"
    } >>"$GITHUB_ENV"
  else
    echo "export MEDIA_MANAGER_TEST_DATABASE_URL='${url}' DATABASE_URL='${url}'"
  fi
  echo "started ${name} (unix socket ${socket_dir})"
}

wait_ready() {
  local attempt
  for attempt in $(seq 120); do
    # pg_isready also succeeds against the init-time server, so wait for the
    # image's own "init finished" marker before trusting it.
    if docker logs "$name" 2>&1 | grep -q 'PostgreSQL init process complete; ready for start up' \
       && docker exec "$name" pg_isready -q -U media_manager -d media_manager_test; then
      echo "${name} ready after ${attempt}s"
      return 0
    fi
    sleep 1
  done
  docker logs --tail 50 "$name" >&2 || true
  echo "${name} did not become ready" >&2
  return 1
}

stop() {
  # A graceful stop lets PostgreSQL remove its socket and lock files.
  docker stop --time 30 "$name" >/dev/null 2>&1 || true
  docker rm -f "$name" >/dev/null 2>&1 || true
  rm -f "$env_file"
  if [[ -d "$socket_dir" ]] && ! rmdir "$socket_dir" 2>/dev/null; then
    # Files created by the container user need the container's namespace to remove.
    docker run --rm --volume "${socket_dir}:/cleanup" --entrypoint sh "$image" \
      -c 'rm -rf /cleanup/* /cleanup/.[!.]*' >/dev/null 2>&1 || true
    rmdir "$socket_dir" 2>/dev/null || echo "warning: could not remove ${socket_dir}" >&2
  fi
  echo "stopped ${name}"
}

case "$action" in
  start) start ;;
  wait) wait_ready ;;
  stop) stop ;;
  *) echo "usage: postgres-container.sh start|wait|stop" >&2; exit 64 ;;
esac
