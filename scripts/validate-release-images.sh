#!/usr/bin/env bash
# Reject mutable release image references before a release install.
# Usage (after loading .env): ./scripts/validate-release-images.sh
set -euo pipefail

for name in MEDIA_MANAGER_API_IMAGE MEDIA_MANAGER_UI_IMAGE; do
	value="${!name:-}"
	if [[ -z "$value" ]]; then
		echo "$name is required" >&2
		exit 1
	fi
	if [[ ! "$value" =~ @sha256:[0-9a-f]{64}$ && ! "$value" =~ :sha-[0-9a-f]{40}$ ]]; then
		echo "$name must be an immutable @sha256:<64 hex> digest or :sha-<40 hex> tag; mutable tags are not allowed" >&2
		exit 1
	fi
done
echo "release images are immutable"
