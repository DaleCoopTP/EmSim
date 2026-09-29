#!/usr/bin/env bash
# Updates a running EmSim installation to the version in this directory
# (ADR-038): the images are built or, from an offline bundle, loaded; a
# backup copy of the current state is made FIRST and the update stops if it
# cannot be made; then api and worker are stopped, migrations run and the
# stack starts again. If the new version does not come up healthy, the copy
# taken here is the way back: scripts/restore.sh <copy>.
#
#   scripts/update.sh                       # build the images from this tree
#   scripts/update.sh emsim-release.tar     # from `make release-bundle`
#   COMPOSE="docker compose -f compose.yaml -f compose.class.yaml" scripts/update.sh
#
# The class sees the service as unavailable while it runs. Turn on
# maintenance mode in the admin's "Состояние" screen beforehand so no new
# lesson starts and trainees finish what they are doing.
set -euo pipefail

bundle=${1:-}
# Intentionally word-split: COMPOSE may carry -f overlays.
# shellcheck disable=SC2206
compose=(${COMPOSE:-docker compose})
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

if [ -n "$bundle" ]; then
	[ -f "$bundle" ] || { echo "bundle not found: $bundle" >&2; exit 2; }
	work=$(mktemp -d)
	trap 'rm -rf "$work"' EXIT
	tar -xf "$bundle" -C "$work"
	[ -f "$work/images.tar" ] || { echo "not a release bundle (no images.tar): $bundle" >&2; exit 2; }
	echo "==> loading images from the bundle"
	docker load -i "$work/images.tar"
	echo "==> replacing compose files and scripts with the bundle's"
	cp "$work"/compose*.yaml "$root"/
	cp "$work"/scripts/*.sh "$root"/scripts/
	[ -f "$work/.env.example" ] && cp "$work/.env.example" "$root/.env.example"
else
	echo "==> building the images"
	"${compose[@]}" build
fi

echo "==> backup copy of the current state (mandatory)"
"${compose[@]}" up -d --wait postgres
if ! "${compose[@]}" run --rm --no-deps worker backup; then
	echo "The backup copy could not be made; nothing was changed. Fix the backup first (BACKUP_DIR, disk space)." >&2
	exit 1
fi
echo "    the newest copy is in the backup directory (BACKUP_HOST_DIR, default ./backups); keep its name"

echo "==> stopping api and worker"
"${compose[@]}" stop api worker

echo "==> migrations"
"${compose[@]}" run --rm --no-deps migrate

echo "==> starting"
if ! "${compose[@]}" up -d --wait --wait-timeout 300; then
	echo "The new version did not become healthy. Look at: ${compose[*]} logs api worker" >&2
	echo "To go back to the state before the update: scripts/restore.sh <the copy made above>" >&2
	exit 1
fi
echo "==> updated"
