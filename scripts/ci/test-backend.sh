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

command -v python3 >/dev/null 2>&1 || {
  echo "python3 is required to summarise backend test groups" >&2
  exit 69
}

script_started=$(date +%s)
log_dir="$(mktemp -d "${RUNNER_TEMP:-/tmp}/backend-tests.XXXXXX")"
pids=()
names=()
test_names=()
mapfile -t test_names < <(go test ./internal/postgres -list '^Test' 2>/dev/null | grep '^Test')
if [[ "${#test_names[@]}" -eq 0 ]]; then
  echo "No PostgreSQL tests were discovered" >&2
  exit 1
fi

# Groups log `go test -json`. On failure the original plain-text output is
# rendered back from the "Output" events, so diagnostics read exactly like
# `go test` while the pass/fail/skip counts stay reliable for nested subtests.
summarize_group() {  # $1 = log, $2 = success|failure, $3 = count file
  python3 - "$1" "$2" "$3" <<'PY'
import json
import sys

log_path, mode, count_path = sys.argv[1:4]
counts = {"pass": 0, "fail": 0, "skip": 0}
with open(log_path, encoding="utf-8", errors="replace") as handle:
    for line in handle:
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            if mode == "failure":
                sys.stdout.write(line)
            continue
        if mode == "failure":
            sys.stdout.write(event.get("Output", ""))
        name = event.get("Test")
        action = event.get("Action")
        if name and "/" not in name and action in counts:
            counts[action] += 1
with open(count_path, "w", encoding="utf-8") as collector:
    collector.write(f"{counts['pass']} {counts['fail']} {counts['skip']}\n")
PY
}

run_group() {  # $1 = group name, remaining arguments = go test arguments
  local name="$1"
  shift
  names+=("$name")
  (
    started=$(date +%s)
    if go test -count=1 -race -timeout 20m -json "$@" >"${log_dir}/${name}.json" 2>&1; then
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

mapfile -t packages < <(go list ./cmd/... ./internal/... | grep -v '/internal/postgres$')
if [[ "${#packages[@]}" -gt 0 ]]; then
  run_group other-packages "${packages[@]}"
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
  run_group "postgres-${shard}" ./internal/postgres -run "^(${pattern})$"
done

failed=0
for pid in "${pids[@]}"; do
  if ! wait "$pid"; then failed=1; fi
done

total_tests=0
for name in "${names[@]}"; do
  status="$(cat "${log_dir}/${name}.status" 2>/dev/null || echo missing)"
  elapsed="$(cat "${log_dir}/${name}.elapsed" 2>/dev/null || echo 0)"
  counts="0 0 0"
  if [[ -f "${log_dir}/${name}.json" ]] && \
     summarize_group "${log_dir}/${name}.json" success "${log_dir}/${name}.counts" >/dev/null; then
    counts="$(cat "${log_dir}/${name}.counts" 2>/dev/null || echo "0 0 0")"
  fi
  read -r passed failed_tests skipped <<<"$counts"
  group_tests=$((passed + failed_tests + skipped))
  total_tests=$((total_tests + group_tests))
  printf -- '=== %s: exit %s, %s tests (%s passed, %s failed, %s skipped) in %ss ===\n' \
    "$name" "$status" "$group_tests" "$passed" "$failed_tests" "$skipped" "$elapsed"
  if [[ "$status" != "0" ]]; then
    if [[ -f "${log_dir}/${name}.json" ]]; then
      summarize_group "${log_dir}/${name}.json" failure "${log_dir}/${name}.counts" >&2 || true
    else
      echo "No log was captured for ${name}" >&2
    fi
    failed=1
  fi
done
if (( failed )); then
  exit 1
fi
echo "all backend tests passed in ${#names[@]} parallel groups (${#test_names[@]} PostgreSQL tests, ${total_tests} tests total) in $(( $(date +%s) - script_started ))s"
