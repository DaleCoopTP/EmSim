#!/usr/bin/env bash
# The W0 measurement run (slice-112-5b-plan.md stage 2) on a server where
# scripts/server-bootstrap.sh has brought the stack up. For each llm
# setting in CONFIGS it restarts llama-server, waits for it, and runs
# bench-llm; then it times whisper on a sample phrase, alone and under LLM
# load. Reports go to bench-results/<vcpu>vcpu-<timestamp>/.
#
#   scripts/server-bench.sh                          # default plan
#   CONFIGS="6:4 8:8" LEVELS=1,4,8 scripts/server-bench.sh
#
# CONFIGS: space-separated THREADS:PARALLEL pairs for llama-server
# (LLM_THREADS / LLM_PARALLEL; the context grows with PARALLEL so each
# dialogue keeps 4096 tokens). The sample WAV for whisper is
# tmp/stt-sample.wav (16 kHz mono; make it on a Mac with
#   say -v Milena -o tmp/stt-sample.wav --data-format=LEI16@16000 "…"
# before server-sync.sh); without it the STT step is skipped.
set -euo pipefail

cd "$(dirname "$0")/.."
DOCKER="docker"
docker info >/dev/null 2>&1 || DOCKER="sudo docker"

VCPU="$(nproc)"
CONFIGS="${CONFIGS:-$([ "$VCPU" -ge 16 ] && echo "8:4 14:4 14:8" || echo "4:4 6:4 6:8")}"
LEVELS="${LEVELS:-1,4,8,20}"
DURATION="${DURATION:-2m}"
JUDGE="${JUDGE:-1}"
OUT="bench-results/${VCPU}vcpu-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$OUT"
log() { echo "==> $*" | tee -a "$OUT/summary.txt"; }

{
	echo "host: $(hostname), $VCPU vCPU, $(free -g | awk '/Mem:/ {print $2}') GB RAM"
	grep -m1 'model name' /proc/cpuinfo
	grep -o -m1 'avx512[a-z_]*' /proc/cpuinfo | head -1 | sed 's/^/avx512: /' || echo "avx512: none"
} | tee "$OUT/summary.txt"

wait_llm() {
	for _ in $(seq 1 90); do
		[ "$($DOCKER compose ps llm --format '{{.Health}}')" = healthy ] && return 0
		sleep 5
	done
	echo "llm did not become healthy" >&2
	return 1
}

for cfg in $CONFIGS; do
	threads="${cfg%%:*}"
	parallel="${cfg##*:}"
	ctx=$((parallel * 4096))
	log "llm: threads=$threads parallel=$parallel ctx=$ctx"
	LLM_THREADS="$threads" LLM_PARALLEL="$parallel" LLM_CTX_SIZE="$ctx" $DOCKER compose up -d llm
	wait_llm
	$DOCKER compose run --rm worker bench-llm \
		--concurrency "$LEVELS" --duration "$DURATION" --judge "$JUDGE" \
		2>&1 | tee "$OUT/llm-t${threads}-p${parallel}.txt"
done

# Back to the compose defaults so the stack is left as bootstrapped.
log "llm: restoring compose defaults"
$DOCKER compose up -d llm
wait_llm

if [ -f tmp/stt-sample.wav ]; then
	$DOCKER compose cp tmp/stt-sample.wav stt:/tmp/sample.wav
	stt_once() {
		$DOCKER compose exec -T stt curl -s -o /dev/null -w '%{time_total}\n' \
			-F file=@/tmp/sample.wav -F response_format=json http://127.0.0.1:8080/inference
	}
	log "stt: text of the sample"
	$DOCKER compose exec -T stt curl -s -F file=@/tmp/sample.wav -F response_format=json \
		http://127.0.0.1:8080/inference | tee -a "$OUT/summary.txt"
	echo
	log "stt: seconds per phrase, idle (5 runs)"
	for _ in 1 2 3 4 5; do stt_once; done | tee -a "$OUT/summary.txt"
	log "stt: seconds per phrase while bench-llm runs 8 dialogues"
	$DOCKER compose run --rm worker bench-llm --concurrency 8 --duration 90s \
		>"$OUT/llm-under-stt.txt" 2>&1 &
	bench=$!
	sleep 30
	for _ in 1 2 3 4 5; do stt_once; done | tee -a "$OUT/summary.txt"
	wait "$bench" || true
else
	log "stt: tmp/stt-sample.wav not found, skipped"
fi

log "done: $OUT"
