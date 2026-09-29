#!/usr/bin/env bash
# Puts the ggml weights for compose.yaml's stt service (ADR-037,
# whisper.cpp) into ./models, verified by sha256. Run once when preparing
# the offline package, on a machine with internet access; the class server
# itself never downloads anything.
#
#   scripts/fetch-stt-model.sh                              # ggml-small.bin
#   STT_MODEL_FILE=ggml-large-v3-turbo-q5_0.bin scripts/fetch-stt-model.sh
#
# The model is fetched from Hugging Face (ggerganov/whisper.cpp). The
# digests of the known files are pinned below (the "x-linked-etag" Hugging
# Face publishes for a LFS file is its sha256); any other file needs its
# digest in STT_MODEL_SHA256, and a download that does not match is
# removed.
#
# Environment (defaults match compose.yaml):
#   STT_MODEL_FILE    file name in ./models and on Hugging Face  (ggml-small.bin)
#   STT_MODEL_SHA256  expected sha256; overrides the pinned one
#   STT_MODEL_URL     download URL; defaults to the Hugging Face file
#   STT_MODELS_DIR    target directory                            (./models)
set -euo pipefail

MODEL_FILE="${STT_MODEL_FILE:-ggml-small.bin}"
MODELS_DIR="${STT_MODELS_DIR:-$(cd "$(dirname "$0")/.." && pwd)/models}"
TARGET="$MODELS_DIR/$MODEL_FILE"
URL="${STT_MODEL_URL:-https://huggingface.co/ggerganov/whisper.cpp/resolve/main/$MODEL_FILE}"

die() { echo "fetch-stt-model: $*" >&2; exit 1; }

# Pinned digests (Hugging Face, 2026-09-29).
pinned_sha256() {
	case "$1" in
		ggml-small.bin) echo "1be3a9b2063867b937e64e2ec7483364a79917e157fa98c5d94b5c1fffea987b" ;;
		ggml-large-v3-turbo-q5_0.bin) echo "394221709cd5ad1f40c46e6031ca61bce88931e6e088c188294c6d5a55ffa7e2" ;;
		*) echo "" ;;
	esac
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		die "neither sha256sum nor shasum is available"
	fi
}

EXPECTED="${STT_MODEL_SHA256:-$(pinned_sha256 "$MODEL_FILE")}"
[ -n "$EXPECTED" ] || die "no pinned digest for $MODEL_FILE: set STT_MODEL_SHA256"
command -v curl >/dev/null 2>&1 || die "curl is required"

mkdir -p "$MODELS_DIR"
if [ -f "$TARGET" ] && [ "$(sha256_of "$TARGET")" = "$EXPECTED" ]; then
	echo "fetch-stt-model: $TARGET is already present and verified"
	exit 0
fi

part="$TARGET.part"
echo "fetch-stt-model: downloading $URL (resumes if interrupted)"
curl -fL --retry 5 --continue-at - -o "$part" "$URL" || die "download failed; run the command again to resume"
echo "fetch-stt-model: verifying sha256…"
actual="$(sha256_of "$part")"
if [ "$actual" != "$EXPECTED" ]; then
	rm -f "$part"
	die "sha256 mismatch: got $actual, want $EXPECTED"
fi
mv -f "$part" "$TARGET"
echo "fetch-stt-model: $TARGET"
echo "fetch-stt-model: sha256 $actual"
