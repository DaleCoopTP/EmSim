#!/usr/bin/env bash
set -euo pipefail

[ $# -ge 1 ] || { echo "usage: $0 user@host [remote-dir]" >&2; exit 2; }
TARGET="$1"
REMOTE_DIR="${2:-EmSim}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

rsync -az --delete \
	"$ROOT/" "$TARGET:$REMOTE_DIR/"

echo "server-sync: copied to $TARGET:$REMOTE_DIR"
