#!/usr/bin/env bash
# Builds an offline update bundle (ADR-038; make release-bundle): a tar with
# images.tar (docker save of the emsim image and of every other image the
# compose files use that is already present locally), the compose files,
# scripts and .env.example. Nothing is downloaded — an image that is not
# present locally is skipped with a notice, and a server that lacks it must
# already have it. Model weights (./models) are not part of the bundle.
#
#   scripts/release-bundle.sh emsim-release.tar
set -euo pipefail

out=${1:-emsim-release.tar}
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

if ! docker image inspect emsim:local >/dev/null 2>&1; then
	echo "emsim:local is not built here: run 'docker compose build' first" >&2
	exit 1
fi

# Every image: the compose files' own, resolved with overlays.
images=$(docker compose -f compose.yaml -f compose.class.yaml config --images 2>/dev/null | sort -u || true)
present=()
for image in emsim:local $images; do
	[ "$image" = "emsim:local" ] && [ "${#present[@]}" -gt 0 ] && continue
	if docker image inspect "$image" >/dev/null 2>&1; then
		present+=("$image")
	else
		echo "skipping $image: not present locally" >&2
	fi
done

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
docker save -o "$work/images.tar" "${present[@]}"
mkdir -p "$work/scripts"
cp compose*.yaml "$work"/
cp scripts/*.sh "$work/scripts/"
[ -f .env.example ] && cp .env.example "$work/.env.example"
tar -cf "$out" -C "$work" .
echo "wrote $out ($(du -h "$out" | cut -f1)); images: ${present[*]}"
