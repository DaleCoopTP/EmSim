#!/usr/bin/env bash
# Restores EmSim from a backup copy (ADR-033): the database and the blob
# store are replaced by the copy's, then migrations bring an older copy
# forward and the stack starts again.
#
#   scripts/restore.sh backups/emsim-20260928-000000Z
#   COMPOSE="docker compose -f compose.yaml -f compose.class.yaml" scripts/restore.sh backups/emsim-...
#
# The copy's checksums are verified before anything is changed; a copy made
# by a newer EmSim than this one is refused. api and worker are stopped for
# the whole restore; the class sees the service as unavailable meanwhile.
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
"${compose[@]}" run --rm --no-deps -v "$abs":/restore:ro worker restore --yes /restore
"${compose[@]}" run --rm --no-deps migrate
"${compose[@]}" up -d
