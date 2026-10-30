#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${MEDIA_MANAGER_TEST_DATABASE_URL:-}" ]]; then
  echo "MEDIA_MANAGER_TEST_DATABASE_URL is required" >&2
  exit 64
fi

shards="${BACKEND_TEST_SHARDS:-4}"
if ! [[ "$shards" =~ ^[1-8]$ ]]; then
  echo "BACKEND_TEST_SHARDS must be an integer from 1 through 8" >&2
  exit 64
fi

log_dir="$(mktemp -d "${RUNNER_TEMP:-/tmp}/backend-tests.XXXXXX")"
pids=()
names=()
test_names=()
mapfile -t test_names < <(go test ./internal/postgres -list '^Test' 2>/dev/null | grep '^Test')
if [[ "${#test_names[@]}" -eq 0 ]]; then
  echo "No PostgreSQL tests were discovered" >&2
  exit 1
fi

mapfile -t packages < <(go list ./cmd/... ./internal/... | grep -v '/internal/postgres$')
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
echo "all backend tests passed in ${#names[@]} parallel groups (${#test_names[@]} PostgreSQL tests)"
