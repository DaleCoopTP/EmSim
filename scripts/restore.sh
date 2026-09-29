#!/usr/bin/env bash
set -euo pipefail

copy=${1:?usage: scripts/restore.sh <backup copy directory>}
if [ ! -f "$copy/manifest.json" ]; then
	echo "not a backup copy (no manifest.json): $copy" >&2
	exit 2
fi
abs=$(cd "$copy" && pwd)
# Intentionally word-split: COMPOSE may carry -f overlays.
# shellcheck disable=SC2206
compose=(${COMPOSE:-docker compose})

read -r -p "Restore replaces ALL current data with $(basename "$abs"). Type yes to continue: " answer
if [ "$answer" != "yes" ]; then
	echo "cancelled" >&2
	exit 1
fi

"${compose[@]}" stop api worker
"${compose[@]}" up -d --wait postgres
echo "==> safety copy of the current state (mandatory)"
if ! "${compose[@]}" run --rm --no-deps -e BACKUP_KEEP=1000 worker backup; then
	echo "The safety copy could not be made; nothing was restored. Starting the stack again." >&2
	"${compose[@]}" up -d
	exit 1
fi
echo "    the newest copy in the backup directory is the state before this restore"
"${compose[@]}" run --rm --no-deps -v "$abs":/restore:ro worker restore --yes /restore
"${compose[@]}" run --rm --no-deps migrate
"${compose[@]}" up -d
