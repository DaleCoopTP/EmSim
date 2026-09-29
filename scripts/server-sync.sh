#!/usr/bin/env bash
# Copies this working tree to a rented test server over SSH, without the
# model weights (the server fetches them itself, `make model`), secrets or
# build output. Run on the developer's machine:
#
#   scripts/server-sync.sh emsim@<server-ip>            # to ~/EmSim
#   scripts/server-sync.sh emsim@<server-ip> /opt/emsim
#
# Then on the server: scripts/server-bootstrap.sh
set -euo pipefail

[ $# -ge 1 ] || { echo "usage: $0 user@host [remote-dir]" >&2; exit 2; }
TARGET="$1"
REMOTE_DIR="${2:-EmSim}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

rsync -az --delete \
	--exclude '.git/' \
	--include '.env.example' \
	--exclude '.env' \
	--exclude '.env.*' \
	--exclude '/models/' \
	--exclude '/data/' \
	--exclude '/backups/' \
	--exclude '/bin/' \
	--include '/tmp/stt-sample.wav' \
	--exclude '/tmp/*' \
	--exclude '/orchestration-core-main/' \
	--exclude '/handoff/' \
	--exclude 'node_modules/' \
	--exclude '/web/.playwright-browsers/' \
	--include '/web/dist/.gitkeep' \
	--exclude '/web/dist/*' \
	--exclude '/web/test-results/' \
	--exclude '/web/playwright-report/' \
	--exclude '/*.zip' --exclude '/*.pdf' --exclude '/*.xlsx' \
	--exclude '.DS_Store' \
	"$ROOT/" "$TARGET:$REMOTE_DIR/"

echo "server-sync: copied to $TARGET:$REMOTE_DIR"
