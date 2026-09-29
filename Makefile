.PHONY: demo class-up class-ca public-up stt-model format-check build test test-integration vet staticcheck verify verify-integration compose-config compose-build seed model web-install web-build web-check verify-web

# ADR-038: the build version shown on the admin status screen.
export EMSIM_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

GO_FILES := $(shell git ls-files --cached --others --exclude-standard -- '*.go' | while IFS= read -r file; do test -f "$$file" && printf '%s\n' "$$file"; done)

format-check:
	test -z "$(shell gofmt -l $(GO_FILES))"

build:
	go build ./...

test:
	go test ./...

test-integration:
	go test -tags=integration -count=1 ./test/integration/...

vet:
	go vet ./...

staticcheck:
	@output="$$(STATICCHECK_CACHE="$${TMPDIR:-/tmp}/emsim-staticcheck" go tool staticcheck -tests=false ./... 2>&1)"; status=$$?; \
	if test -n "$$output"; then printf '%s\n' "$$output"; fi; \
	if test $$status -ne 0; then exit $$status; fi; \
	case "$$output" in *"matched no packages"*) exit 1;; esac

verify: format-check build test vet staticcheck

verify-integration: verify test-integration

# Both supported stacks: the stock one with the bundled model (ADR-029)
# and the no-model override e2e/CI and Ollama-on-the-host development use.
compose-config:
	docker compose config --quiet
	docker compose -f compose.yaml -f compose.no-llm.yaml config --quiet
	EMSIM_HOST=emsim.local docker compose -f compose.yaml -f compose.class.yaml config --quiet
	EMSIM_HOST=emsim.example.org docker compose -f compose.yaml -f compose.class.yaml -f compose.public.yaml config --quiet
	DICTATION=stub docker compose -f compose.yaml -f compose.no-llm.yaml config --quiet
	REMOTE_LLM_URL=https://llm.example/v1 REMOTE_LLM_MODEL=model docker compose -f compose.yaml -f compose.remote-llm.yaml config --quiet

# Demo stand (ADR-033): workstations, an instructor and demo trainees
# for a running stack. DEMO_PASSWORD (8+ characters) comes from .env or the
# environment and is the password of every account it creates.
demo:
	docker compose --profile demo run --rm demo-setup

# Classroom profile (ADR-033): Caddy with HTTPS from its internal CA in
# front of api. EMSIM_HOST (the name/IP the classroom opens) comes from
# .env or the environment. class-ca exports the CA's root certificate to
# ./emsim-root.crt for installing on every classroom PC.
CLASS_COMPOSE = docker compose -f compose.yaml -f compose.class.yaml

class-up:
	$(CLASS_COMPOSE) up -d --build

class-ca:
	$(CLASS_COMPOSE) cp caddy:/data/caddy/pki/authorities/local/root.crt ./emsim-root.crt

# Public demo stand: the classroom profile with a Let's Encrypt certificate
# (compose.public.yaml). EMSIM_HOST is a public DNS name pointing at this
# server; ports 80 and 443 must be open to the internet.
public-up:
	$(CLASS_COMPOSE) -f compose.public.yaml up -d --build

# Offline update bundle (ADR-038): the emsim image and the images already
# on this machine that the compose files use, plus the compose files and
# scripts, in one tar for `scripts/update.sh <bundle>` on a server without
# internet. Nothing is pulled: build or start the stack first. Model weights
# are NOT included (see README, "Обновление"). RELEASE_BUNDLE names the file.
RELEASE_BUNDLE ?= emsim-release.tar
release-bundle:
	scripts/release-bundle.sh $(RELEASE_BUNDLE)

compose-build: compose-config
	docker compose build

# Re-runs compose.yaml's one-shot seed service on its own — e.g. after
# adding a scenario file to seed/ — without restarting the rest of the
# stack. Idempotent for content already loaded (see compose.yaml's seed
# service and seed/README.md).
seed:
	docker compose run --rm seed

# Fetches the llm service's GGUF weights into ./models with sha256
# verification (ADR-029) — once, on a machine with internet access, when
# preparing the offline package. See scripts/fetch-model.sh for the other
# sources (--from-ollama, --url).
model:
	scripts/fetch-model.sh

# Fetches the stt service's whisper.cpp weights into ./models with sha256
# verification (ADR-037). STT_MODEL_FILE picks another model.
stt-model:
	scripts/fetch-stt-model.sh

# The Go build/test/verify targets above never need Node — web/dist ships
# a checked-in .gitkeep placeholder (web/embed.go), so "go build ./..."
# always succeeds with or without a built SPA. These targets are for the
# web client itself (web/README.md); CI runs them as a separate job.
web-install:
	cd web && npm ci

web-check: web-install
	cd web && npm run check

web-build: web-install
	cd web && npm run build

verify-web: web-check web-build
