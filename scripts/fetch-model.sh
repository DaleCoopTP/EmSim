#!/usr/bin/env bash
# Puts the GGUF weights for compose.yaml's llm service (ADR-029) into
# ./models, verified by sha256. Run once when preparing the offline
# package, on a machine with internet access; the class server itself
# never downloads anything.
#
#   scripts/fetch-model.sh                 # from the Ollama registry (default)
#   scripts/fetch-model.sh --from-ollama   # copy from this machine's Ollama store
#   scripts/fetch-model.sh --url URL       # any direct GGUF URL (e.g. Hugging Face)
#
# The Ollama registry and the local Ollama store are content-addressed:
# the model blob's name is its own sha256, so either source proves these
# are the very weights already tried in development through Ollama.
# LLM_MODEL_SHA256 pins that digest; when it is set every source must
# match it, so a moved registry tag or a different file is refused.
#
# Environment (defaults match compose.yaml):
#   LLM_MODEL_FILE    target file name in ./models  (T-lite-it-2.1-Q5_K_M.gguf)
#   LLM_OLLAMA_MODEL  Ollama model reference        (t-tech/T-lite-it-2.1:q5_K_M)
#   LLM_MODEL_SHA256  expected sha256 of the GGUF   (pinned below once known)
#   LLM_MODELS_DIR    target directory              (./models)
set -euo pipefail

# Pinned digest of the validated weights. Empty until confirmed against the
# Ollama blob the user has tested with (see README.md, "Модель"); while
# empty the script still verifies the file against the source's own digest
# and prints the value to pin here.
PINNED_SHA256=""

MODEL_FILE="${LLM_MODEL_FILE:-T-lite-it-2.1-Q5_K_M.gguf}"
OLLAMA_MODEL="${LLM_OLLAMA_MODEL:-t-tech/T-lite-it-2.1:q5_K_M}"
EXPECTED_SHA256="${LLM_MODEL_SHA256:-$PINNED_SHA256}"
MODELS_DIR="${LLM_MODELS_DIR:-$(cd "$(dirname "$0")/.." && pwd)/models}"
TARGET="$MODELS_DIR/$MODEL_FILE"

die() { echo "fetch-model: $*" >&2; exit 1; }

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		die "neither sha256sum nor shasum is available"
	fi
}

# check_expected DIGEST — the source's own digest must match the pin, if any.
check_expected() {
	if [ -n "$EXPECTED_SHA256" ] && [ "$1" != "$EXPECTED_SHA256" ]; then
		die "source digest $1 does not match the pinned LLM_MODEL_SHA256 $EXPECTED_SHA256"
	fi
}

# install_verified FILE DIGEST — verify FILE against DIGEST, then move it
# into place atomically.
install_verified() {
	local actual
	echo "fetch-model: verifying sha256…"
	actual="$(sha256_of "$1")"
	if [ "$actual" != "$2" ]; then
		rm -f "$1"
		die "sha256 mismatch: got $actual, want $2"
	fi
	mv -f "$1" "$TARGET"
	echo "fetch-model: $TARGET"
	echo "fetch-model: sha256 $actual"
	if [ -z "$EXPECTED_SHA256" ]; then
		echo "fetch-model: pin it: PINNED_SHA256=\"$actual\" in scripts/fetch-model.sh"
	fi
}

# ollama_ref_parts REF — prints "namespace/model tag" for an Ollama model
# reference such as t-tech/T-lite-it-2.1:q5_K_M or qwen3:8b.
ollama_ref_parts() {
	local ref="$1" name tag
	name="${ref%%:*}"
	tag="latest"
	if [ "$name" != "$ref" ]; then tag="${ref#*:}"; fi
	case "$name" in */*) ;; *) name="library/$name" ;; esac
	echo "$name $tag"
}

from_registry() {
	command -v curl >/dev/null 2>&1 || die "curl is required"
	command -v python3 >/dev/null 2>&1 || die "python3 is required to read the registry manifest"
	local parts name tag manifest digest part
	parts="$(ollama_ref_parts "$OLLAMA_MODEL")"
	name="${parts% *}"
	tag="${parts#* }"
	echo "fetch-model: reading registry.ollama.ai manifest for $name:$tag"
	manifest="$(curl -fsSL -H 'Accept: application/vnd.docker.distribution.manifest.v2+json' \
		"https://registry.ollama.ai/v2/$name/manifests/$tag")" || die "cannot read the manifest for $OLLAMA_MODEL"
	digest="$(printf '%s' "$manifest" | python3 -c '
import json, sys
layers = [l for l in json.load(sys.stdin).get("layers", []) if l.get("mediaType") == "application/vnd.ollama.image.model"]
if len(layers) != 1:
    sys.exit("expected exactly one model layer")
print(layers[0]["digest"].removeprefix("sha256:"))
')" || die "the manifest for $OLLAMA_MODEL has no single model layer"
	check_expected "$digest"
	part="$TARGET.part"
	echo "fetch-model: downloading $digest (several GB; resumes if interrupted)"
	curl -fL --retry 5 --continue-at - -o "$part" "https://registry.ollama.ai/v2/$name/blobs/sha256:$digest" ||
		die "download failed; run the command again to resume"
	install_verified "$part" "$digest"
}

from_ollama() {
	command -v ollama >/dev/null 2>&1 || die "ollama is not installed on this machine"
	local blob digest part
	blob="$(ollama show --modelfile "$OLLAMA_MODEL" | sed -n 's/^FROM \(\/.*\)$/\1/p' | head -n 1)" ||
		die "ollama show failed for $OLLAMA_MODEL (run: ollama pull $OLLAMA_MODEL)"
	[ -n "$blob" ] && [ -f "$blob" ] || die "no local blob found for $OLLAMA_MODEL (run: ollama pull $OLLAMA_MODEL)"
	digest="$(basename "$blob")"
	digest="${digest#sha256-}"
	check_expected "$digest"
	part="$TARGET.part"
	echo "fetch-model: copying $blob"
	cp "$blob" "$part"
	install_verified "$part" "$digest"
}

from_url() {
	command -v curl >/dev/null 2>&1 || die "curl is required"
	[ -n "$EXPECTED_SHA256" ] || die "--url needs a known digest: set LLM_MODEL_SHA256 or pin PINNED_SHA256 first"
	local part="$TARGET.part"
	echo "fetch-model: downloading $1 (resumes if interrupted)"
	curl -fL --retry 5 --continue-at - -o "$part" "$1" || die "download failed; run the command again to resume"
	install_verified "$part" "$EXPECTED_SHA256"
}

mkdir -p "$MODELS_DIR"
if [ -f "$TARGET" ] && [ -n "$EXPECTED_SHA256" ] && [ "$(sha256_of "$TARGET")" = "$EXPECTED_SHA256" ]; then
	echo "fetch-model: $TARGET is already present and verified"
	exit 0
fi

case "${1:-}" in
	"") from_registry ;;
	--from-ollama) from_ollama ;;
	--url)
		[ -n "${2:-}" ] || die "--url needs a URL"
		from_url "$2"
		;;
	-h | --help) sed -n '2,22p' "$0" ;;
	*) die "unknown option: $1 (see --help)" ;;
esac
