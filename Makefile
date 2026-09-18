.PHONY: format-check build test test-integration vet staticcheck verify verify-integration

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
	go tool staticcheck ./...

verify: format-check build test vet staticcheck

verify-integration: verify test-integration
