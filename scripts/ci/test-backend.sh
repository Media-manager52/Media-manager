#!/usr/bin/env bash
# Run the whole Go suite under -race as concurrent groups.
#
# * `pkg-NAME` groups run each other package whose tests use the PostgreSQL
#   database (found by grepping for MEDIA_MANAGER_TEST_DATABASE_URL).
# * `other-packages` runs every remaining package.
# * `postgres-N` groups split the top-level internal/postgres tests, balanced
#   with scripts/ci/postgres-test-weights.txt.
#
# Every group gets its own disposable DATABASE on the same PostgreSQL server
# (tests still create their own unique schemas inside it). The schema migrator
# serialises on a database-wide advisory lock, so groups that shared one
# database queued behind each other's migrations and could time out waiting for
# the lock; separate databases remove that contention without touching the
# production migrator. Coverage is unchanged: every test still runs with
# -race and -count=1, and the run fails if any discovered test reports nothing.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${script_dir}/../.."
tools="${script_dir}/backend_test_tools.py"
weights="${script_dir}/postgres-test-weights.txt"

if [[ -z "${MEDIA_MANAGER_TEST_DATABASE_URL:-}" ]]; then
  echo "MEDIA_MANAGER_TEST_DATABASE_URL is required" >&2
  exit 64
fi

shards="${BACKEND_TEST_SHARDS:-6}"
if ! [[ "$shards" =~ ^[1-8]$ ]]; then
  echo "BACKEND_TEST_SHARDS must be an integer from 1 through 8" >&2
  exit 64
fi

isolation="${BACKEND_TEST_DATABASE_ISOLATION:-database}"
if [[ "$isolation" != "database" && "$isolation" != "shared" ]]; then
  echo "BACKEND_TEST_DATABASE_ISOLATION must be 'database' or 'shared'" >&2
  exit 64
fi

command -v python3 >/dev/null 2>&1 || {
  echo "python3 is required to summarise backend test groups" >&2
  exit 69
}

admin_url="$MEDIA_MANAGER_TEST_DATABASE_URL"
databases=()

if [[ "$isolation" == "database" ]]; then
  command -v psql >/dev/null 2>&1 || {
    echo "psql is required to create per-group databases (or set BACKEND_TEST_DATABASE_ISOLATION=shared)" >&2
    exit 69
  }
  url_pattern='^postgres(ql)?://([^:@/?]+)(:([^@/?]*))?@([^:/?]+)(:([0-9]+))?/([^?]+)(\?(.*))?$'
  if ! [[ "$admin_url" =~ $url_pattern ]]; then
    echo "MEDIA_MANAGER_TEST_DATABASE_URL must look like postgres://user[:password]@host[:port]/database[?query]" >&2
    exit 64
  fi
  db_user="${BASH_REMATCH[2]}"
  db_password="${BASH_REMATCH[4]}"
  db_host="${BASH_REMATCH[5]}"
  db_port="${BASH_REMATCH[7]}"
  db_name="${BASH_REMATCH[8]}"
  db_query="${BASH_REMATCH[10]}"
  db_sslmode=""
  [[ "$db_query" =~ (^|&)sslmode=([a-z-]+) ]] && db_sslmode="${BASH_REMATCH[2]}"

  psql_admin() {
    # Credentials travel in the environment, never on the command line.
    PGHOST="$db_host" PGPORT="${db_port:-5432}" PGUSER="$db_user" PGPASSWORD="$db_password" \
      PGSSLMODE="${db_sslmode:-prefer}" PGCONNECT_TIMEOUT=15 \
      psql -X -q -v ON_ERROR_STOP=1 -d "$db_name" "$@"
  }
  group_database_url() {  # $1 = database name
    printf 'postgres://%s%s@%s%s/%s%s' "$db_user" "${db_password:+:$db_password}" "$db_host" \
      "${db_port:+:$db_port}" "$1" "${db_query:+?$db_query}"
  }
  drop_databases() {
    local database
    for database in ${databases[@]+"${databases[@]}"}; do
      psql_admin -c "DROP DATABASE IF EXISTS \"${database}\" WITH (FORCE)" >/dev/null 2>&1 || true
    done
  }
  run_tag="$(od -An -N6 -tx1 /dev/urandom | tr -d ' \n')"
fi

cleanup() {
  # Stop any group still running (only after an early failure or a signal),
  # then drop the disposable databases.
  local running
  running="$(jobs -p)"
  [[ -n "$running" ]] && kill $running 2>/dev/null || true
  [[ "$isolation" == "database" ]] && drop_databases
  return 0
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

prepare_group_database() {  # $1 = group name; sets group_db_url
  if [[ "$isolation" == "shared" ]]; then
    group_db_url="$admin_url"
    return
  fi
  local database="mm_ci_${run_tag}_${1//-/_}"
  psql_admin -c "CREATE DATABASE \"${database}\"" >&2
  databases+=("$database")
  group_db_url="$(group_database_url "$database")"
}

script_started=$(date +%s)
log_dir="$(mktemp -d "${RUNNER_TEMP:-/tmp}/backend-tests.XXXXXX")"
python3 -I "$tools" snapshot "${log_dir}/resources.json" || true
pids=()
names=()
test_names=()

# -race here shares compiled artifacts with the groups below.
if ! go test -race ./internal/postgres -list '^Test' >"${log_dir}/list.out" 2>"${log_dir}/list.err"; then
  cat "${log_dir}/list.err" >&2
  echo "Could not list the PostgreSQL tests" >&2
  exit 1
fi
mapfile -t test_names < <(grep '^Test' "${log_dir}/list.out" || true)
if [[ "${#test_names[@]}" -eq 0 ]]; then
  echo "No PostgreSQL tests were discovered" >&2
  exit 1
fi
printf '%s\n' "${test_names[@]}" >"${log_dir}/expected-postgres-tests.txt"

run_group() {  # $1 = group name, $2 = database URL, remaining arguments = go test arguments
  local name="$1" url="$2"
  shift 2
  names+=("$name")
  (
    started=$(date +%s)
    if MEDIA_MANAGER_TEST_DATABASE_URL="$url" \
       go test -count=1 -race -timeout 20m -json "$@" >"${log_dir}/${name}.json" 2>&1; then
      status=0
    else
      status=$?
    fi
    finished=$(date +%s)
    printf '%s\n' "$status" >"${log_dir}/${name}.status"
    printf '%s\n' "$((finished - started))" >"${log_dir}/${name}.elapsed"
  ) &
  pids+=("$!")
}

# Every package that talks to the database gets its own group and database.
mapfile -t database_dirs < <(grep -rl --include='*_test.go' MEDIA_MANAGER_TEST_DATABASE_URL cmd internal \
  | xargs -r -n1 dirname | sort -u)
packages=()
while read -r import_path package_dir; do
  relative="${package_dir#"$PWD"/}"
  [[ "$relative" == "internal/postgres" ]] && continue
  database_package=""
  for candidate in ${database_dirs[@]+"${database_dirs[@]}"}; do
    [[ "$candidate" == "$relative" ]] && database_package="1"
  done
  if [[ -n "$database_package" ]]; then
    prepare_group_database "pkg-${relative##*/}"
    run_group "pkg-${relative##*/}" "$group_db_url" "$import_path"
  else
    packages+=("$import_path")
  fi
done < <(go list -f '{{.ImportPath}} {{.Dir}}' ./cmd/... ./internal/...)
if [[ "${#packages[@]}" -gt 0 ]]; then
  prepare_group_database other-packages
  run_group other-packages "$group_db_url" "${packages[@]}"
fi

# Spread the known-slow tests across groups instead of splitting by position.
mapfile -t shard_patterns < <(python3 -I "$tools" plan --shards "$shards" --weights "$weights" \
  "${log_dir}/expected-postgres-tests.txt")
for shard in "${!shard_patterns[@]}"; do
  pattern="${shard_patterns[$shard]}"
  [[ -z "$pattern" ]] && continue
  prepare_group_database "postgres-${shard}"
  run_group "postgres-${shard}" "$group_db_url" ./internal/postgres -run "^(${pattern})\$"
done

failed=0
for pid in "${pids[@]}"; do
  if ! wait "$pid"; then failed=1; fi
done

python3 -I "$tools" report --log-dir "$log_dir" --expected "${log_dir}/expected-postgres-tests.txt" \
  "${names[@]}" || failed=1
python3 -I "$tools" resources "${log_dir}/resources.json" || true
if (( failed )); then
  echo "Group logs were kept in ${log_dir}" >&2
  exit 1
fi
rm -rf "$log_dir"
echo "all backend tests passed in ${#names[@]} parallel groups (${#test_names[@]} PostgreSQL tests, ${isolation} databases) in $(( $(date +%s) - script_started ))s"
