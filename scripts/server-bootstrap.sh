#!/usr/bin/env bash
# Brings the full compose stack up on a fresh Ubuntu 24.04 test server
# (the W0 measurement, slice-112-5b-plan.md stage 2). Idempotent: rerun it
# after a failure or a VM resize. Run from the copied tree on the server:
#
#   cd ~/EmSim && scripts/server-bootstrap.sh
#
# Steps: Docker from Ubuntu's own packages, a .env with random passwords
# (kept only on the server, mode 600), both model weights fetched here
# with sha256 checks, image build, stack start, demo accounts. api stays
# bound to 127.0.0.1:8080 — open it through an SSH tunnel:
#   ssh -L 8080:localhost:8080 user@server   →   http://localhost:8080
set -euo pipefail

cd "$(dirname "$0")/.."
log() { echo "==> $*"; }

if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
	log "installing Docker"
	sudo apt-get update -q
	sudo DEBIAN_FRONTEND=noninteractive apt-get install -yq docker.io docker-compose-v2 make curl python3 rsync
	sudo systemctl enable --now docker
fi
DOCKER="docker"
docker info >/dev/null 2>&1 || DOCKER="sudo docker"
if [ "$DOCKER" = "sudo docker" ] && ! id -nG | grep -qw docker; then
	sudo usermod -aG docker "$USER"
	log "added $USER to the docker group (takes effect on the next login; using sudo for now)"
fi

if [ ! -f .env ]; then
	log "writing .env with random passwords"
	rnd() { python3 -c 'import secrets; print(secrets.token_urlsafe(18))'; }
	(umask 077 && cat >.env) <<EOF
POSTGRES_PASSWORD=$(rnd)
BOOTSTRAP_ADMIN_LOGIN=admin
BOOTSTRAP_ADMIN_PASSWORD=$(rnd)
DEMO_PASSWORD=$(rnd)
API_PORT=8080
API_ADMIN_PORT=8081
WORKER_ADMIN_PORT=8082
BLOB_ROOT=./data/blobs
EOF
fi

log "model weights (skipped when already present and verified)"
scripts/fetch-model.sh
scripts/fetch-stt-model.sh

log "building and starting the stack"
$DOCKER compose up -d --build

log "waiting for llm and api to become healthy (loading ~6 GB of weights)"
for _ in $(seq 1 120); do
	unhealthy="$($DOCKER compose ps --format '{{.Service}} {{.Health}}' | awk '$2 != "" && $2 != "healthy"')"
	[ -z "$unhealthy" ] && break
	sleep 5
done
$DOCKER compose ps
[ -z "$unhealthy" ] || { echo "not healthy yet: $unhealthy" >&2; exit 1; }

log "demo accounts"
$DOCKER compose --profile demo run --rm demo-setup || log "demo-setup failed (already created?)"

cat <<EOF

Stack is up. CPU: $(nproc) vCPU, RAM: $(free -g | awk '/Mem:/ {print $2}') GB.
Logins and passwords: grep -E 'ADMIN|DEMO' .env   (demo logins: see demo-setup output above)
Browser: ssh -L 8080:localhost:8080 $USER@<this-ip>   then http://localhost:8080
Measurements: scripts/server-bench.sh
EOF
