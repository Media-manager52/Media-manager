#!/usr/bin/env bash
# Wait for one GitHub Actions run and return as soon as it finishes.
#
#   scripts/ci/wait-for-run.sh <run-id> [max-minutes]
#
# Use it only when the next step depends on the run (merge after PR checks,
# deploy after main Verify, live checks after a deploy). It asks GitHub once
# every 30 seconds for the run's status only (no log streaming), prints the
# result, and exits 0 on success, 1 on any other conclusion, and 2 when the
# limit (default 45 minutes) passes first.
set -euo pipefail

repository="${GITHUB_REPOSITORY:-Media-manager52/Media-manager}"
run_id="${1:-}"
max_minutes="${2:-45}"
if ! [[ "$run_id" =~ ^[0-9]+$ ]] || ! [[ "$max_minutes" =~ ^[0-9]+$ ]]; then
  echo "usage: $0 <run-id> [max-minutes]" >&2
  exit 64
fi

deadline=$(( $(date +%s) + max_minutes * 60 ))
while :; do
  if summary="$(gh run view "$run_id" --repo "$repository" --json status,conclusion,headSha,url \
      --jq '"\(.status) \(.conclusion) \(.headSha) \(.url)"' 2>/dev/null)"; then
    read -r status conclusion sha url <<<"$summary"
    if [[ "$status" == "completed" ]]; then
      echo "run $run_id $conclusion on $sha: $url"
      [[ "$conclusion" == "success" ]] && exit 0
      exit 1
    fi
  fi
  if (( $(date +%s) >= deadline )); then
    echo "run $run_id still not finished after $max_minutes minutes" >&2
    exit 2
  fi
  sleep 30
done
