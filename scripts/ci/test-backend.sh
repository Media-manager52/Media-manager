#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${MEDIA_MANAGER_TEST_DATABASE_URL:-}" ]]; then
  echo "MEDIA_MANAGER_TEST_DATABASE_URL is required" >&2
  exit 64
fi

# BACKEND_TEST_SHARDS groups run in parallel on this machine. CI spreads the
# suite over several machines: BACKEND_TEST_PART selects "other" (every package
# except internal/postgres) or "postgres:<machine>/<machines>" (one machine's
# share of the PostgreSQL tests); "all" (the default) runs everything here.
shards="${BACKEND_TEST_SHARDS:-4}"
if ! [[ "$shards" =~ ^([1-9]|1[0-6])$ ]]; then
  echo "BACKEND_TEST_SHARDS must be an integer from 1 through 16" >&2
  exit 64
fi
part="${BACKEND_TEST_PART:-all}"
machine=0
machines=1
if [[ "$part" =~ ^postgres:([0-9]+)/([1-9][0-9]?)$ ]]; then
  machine="${BASH_REMATCH[1]}"
  machines="${BASH_REMATCH[2]}"
  if (( machine >= machines )); then
    echo "BACKEND_TEST_PART machine must be below the machine count" >&2
    exit 64
  fi
elif [[ "$part" != all && "$part" != other ]]; then
  echo "BACKEND_TEST_PART must be all, other or postgres:<machine>/<machines>" >&2
  exit 64
fi

log_dir="$(mktemp -d "${RUNNER_TEMP:-/tmp}/backend-tests.XXXXXX")"
pids=()
names=()
test_names=()
test_names=()
if [[ "$part" != other ]]; then
  mapfile -t all_tests < <(go test ./internal/postgres -list '^Test' 2>/dev/null | grep '^Test' | sort)
  if [[ "${#all_tests[@]}" -eq 0 ]]; then
    echo "No PostgreSQL tests were discovered" >&2
    exit 1
  fi
  # Every test lands on exactly one machine: test i goes to machine i % machines.
  for index in "${!all_tests[@]}"; do
    if (( index % machines == machine )); then
      test_names+=("${all_tests[$index]}")
    fi
  done
fi

packages=()
if [[ "$part" != postgres:* ]]; then
  mapfile -t packages < <(go list ./cmd/... ./internal/... | grep -v '/internal/postgres$')
fi
if [[ "${#packages[@]}" -gt 0 ]]; then
  names+=("other-packages")
  (go test -count=1 -race -timeout 20m "${packages[@]}" >"$log_dir/other-packages.log" 2>&1; echo $? >"$log_dir/other-packages.status") &
  pids+=("$!")
fi

for shard in $(seq 0 $((shards - 1))); do
  pattern=""
  for index in "${!test_names[@]}"; do
    if (( index % shards == shard )); then
      [[ -n "$pattern" ]] && pattern+="|"
      pattern+="${test_names[$index]}"
    fi
  done
  [[ -z "$pattern" ]] && continue
  names+=("postgres-$shard")
  (go test -count=1 -race -timeout 20m ./internal/postgres -run "^(${pattern})$" >"$log_dir/postgres-${shard}.log" 2>&1; echo $? >"$log_dir/postgres-${shard}.status") &
  pids+=("$!")
done

failed=0
for pid in "${pids[@]}"; do
  if ! wait "$pid"; then failed=1; fi
done
for name in "${names[@]}"; do
  status="$(cat "$log_dir/${name}.status" 2>/dev/null || echo missing)"
  echo "--- ${name}: exit ${status} ---"
  if [[ "$status" != "0" ]]; then
    cat "$log_dir/${name}.log" >&2
    failed=1
  else
    tail -n 2 "$log_dir/${name}.log"
  fi
done
if (( failed )); then
  exit 1
fi
echo "all backend tests passed in ${#names[@]} parallel groups (${#test_names[@]} PostgreSQL tests, part ${part})"
