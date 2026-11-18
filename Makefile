# ReminisLink - pak and protocol
#
# Main targets:
#   make test       run Go unit tests (no toolchain, CGO_ENABLED=0)
#   make lint       gofmt + go vet
#   make contract   validate protocol/openapi.yaml (Redocly in Docker) and the test vectors
#   make vectors    regenerate protocol/test-vectors/ and copy them to the Swift tests
#
# The pak build targets (build, pak, release, docker-*) come with milestone M7.

GO_SOURCES    := internal protocol
GO_PACKAGES   := ./...

DOCKER        ?= docker
# Pinned OpenAPI validator image (no global install).
REDOCLY_IMAGE ?= redocly/cli:1.34.5

.DEFAULT_GOAL := help
.PHONY: help test lint contract vectors

help: ## Show this help
	@grep -E '^[a-zA-Z_%-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "} {printf "  %-18s %s\n", $$1, $$2}'

test: ## Run unit tests (pure Go, no SDL needed)
	CGO_ENABLED=0 go test $(GO_PACKAGES)

lint: ## Check formatting and run go vet
	@test -z "$$(gofmt -l $(GO_SOURCES))" || { echo "Files need gofmt:"; gofmt -l $(GO_SOURCES); exit 1; }
	CGO_ENABLED=0 go vet $(GO_PACKAGES)

contract: ## Validate the OpenAPI contract and check the test vectors (Go and Swift copies)
	$(DOCKER) run --rm -v "$(CURDIR)/protocol":/spec:ro -w /spec $(REDOCLY_IMAGE) \
		lint --config redocly.yaml openapi.yaml
	CGO_ENABLED=0 go run ./protocol/test-vectors/gen -check -out protocol/test-vectors
	scripts/sync-test-vectors.sh --check

vectors: ## Regenerate the test vectors and copy them to the Swift test resources
	CGO_ENABLED=0 go run ./protocol/test-vectors/gen -out protocol/test-vectors
	scripts/sync-test-vectors.sh
