#!/usr/bin/env bash
# Runs the Go tests for one area of Media Manager, so a change can be checked
# in seconds instead of running everything. CI still runs the full suite.
#
#   scripts/dev/test-area.sh            list the areas
#   scripts/dev/test-area.sh search     test one area
#
# Database tests need MEDIA_MANAGER_TEST_DATABASE_URL. Without it this starts a
# throwaway PostgreSQL in Docker and removes it afterwards.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
conf="$root/scripts/dev/areas.conf"
cd "$root"

if [ $# -eq 0 ]; then
  echo "Areas:"
  grep -v '^#' "$conf" | grep -v '^$' | cut -d'|' -f1 | sed 's/ *$//; s/^/  /'
  exit 0
fi

line="$(grep -E "^$1 *\|" "$conf" || true)"
if [ -z "$line" ]; then
  echo "Unknown area: $1 (run without arguments to list them)" >&2
  exit 2
fi
packages="$(echo "$line" | cut -d'|' -f2)"
stems="$(echo "$line" | cut -d'|' -f3)"

tests=""
for stem in $stems; do
  for file in internal/postgres/"$stem"_test.go internal/postgres/"$stem"_integration_test.go; do
    [ -f "$file" ] || continue
    found="$(sed -nE 's/^func (Test[A-Za-z0-9_]+)\(t \*testing\.T\).*/\1/p' "$file" | paste -sd'|' -)"
    [ -z "$found" ] || tests="${tests:+$tests|}$found"
  done
done

container=""
cleanup() { [ -z "$container" ] || docker rm -f "$container" >/dev/null 2>&1 || true; }
trap cleanup EXIT
if [ -n "$tests" ] && [ -z "${MEDIA_MANAGER_TEST_DATABASE_URL:-}" ]; then
  container="mm-test-area-$$"
  docker run -d --rm --name "$container" -e POSTGRES_PASSWORD=test -e POSTGRES_DB=mm \
    -p 127.0.0.1::5432 postgres:18.6-alpine3.24 -c max_locks_per_transaction=512 >/dev/null
  until docker exec "$container" pg_isready -U postgres -q 2>/dev/null; do sleep 1; done
  port="$(docker port "$container" 5432/tcp | head -1 | sed 's/.*://')"
  export MEDIA_MANAGER_TEST_DATABASE_URL="postgres://postgres:test@127.0.0.1:$port/mm?sslmode=disable"
fi

export GOFLAGS=-mod=readonly
if [ -n "$tests" ]; then
  go test -count=1 -run "^($tests)\$" ./internal/postgres
fi
if [ -n "${packages// /}" ]; then
  # shellcheck disable=SC2086 # the package list is meant to split into words
  go test -count=1 $packages
fi
