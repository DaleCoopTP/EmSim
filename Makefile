.PHONY: format-check build test test-integration vet staticcheck verify verify-integration compose-config compose-build seed model web-install web-build web-check verify-web

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
