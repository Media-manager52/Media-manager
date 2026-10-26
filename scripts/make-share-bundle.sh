#!/usr/bin/env bash
# Create a source bundle to hand to another owner. It contains tracked source
# only: no .env files, media, node_modules, runtime data or Git history.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
OUT=${1:-"../media-manager-share-$(date +%Y%m%d).tar.gz"}
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
	if [[ -n "$(git status --porcelain)" ]]; then
		printf 'Warning: uncommitted changes are not included in the bundle.\n' >&2
	fi
	git archive --format=tar.gz --prefix=media-manager/ --output="$OUT" HEAD
else
	tar \
		--exclude='./.git' --exclude='./node_modules' --exclude='./data' \
		--exclude='./.next' --exclude='./dist' --exclude='./coverage' \
		--exclude='./.sites-runtime' --exclude='./work' --exclude='./outputs' \
		--exclude='./.env' --exclude='./.env.integrations' --exclude='./.env.local' --exclude='*.log' \
		--transform='s,^\./,media-manager/,' -czf "$OUT" .
fi
printf 'Created %s\n' "$OUT"
printf 'The recipient extracts it and runs ./scripts/setup-standalone.sh\n'
