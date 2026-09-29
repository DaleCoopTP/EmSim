#!/usr/bin/env bash
set -euo pipefail

out=${1:-emsim-release.tar}
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

if ! docker image inspect emsim:local >/dev/null 2>&1; then
	echo "emsim:local is not built here: run 'docker compose build' first" >&2
	exit 1
fi

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
