# ReminisLink - pak and protocol
#
# Main targets:
#   make test       run Go unit tests (no toolchain, CGO_ENABLED=0)
#   make lint       gofmt + go vet
#   make fuzz       run each fuzz target of the patch parsers briefly (FUZZTIME, default 10s)
#   make contract   validate protocol/openapi.yaml (Redocly in Docker) and the test vectors
#   make vectors    regenerate protocol/test-vectors/ and copy them to the Swift tests
#   make headless   run the pak (server + Bonjour, no UI) on a temporary copy of testdata/sdcard
#
# The pak build targets (build, pak, release, docker-*) come with milestone M7.

GO_SOURCES    := cmd internal protocol
GO_PACKAGES   := ./...

BUILD_DIR     := build
HOST_BIN      := $(BUILD_DIR)/reminislink-host
# Extra options of "make headless", e.g. HEADLESS_ARGS="--port 48000 --name Test"
HEADLESS_ARGS ?=

DOCKER        ?= docker
# Pinned OpenAPI validator image (no global install).
REDOCLY_IMAGE ?= redocly/cli:1.34.5

# Duration of each fuzz target in "make fuzz".
FUZZTIME      ?= 10s
PATCH_FUZZERS := FuzzIPS FuzzBPS FuzzUPS FuzzBPSNoChecksum FuzzUPSNoChecksum

.DEFAULT_GOAL := help
.PHONY: help test lint fuzz contract vectors headless

help: ## Show this help
	@grep -E '^[a-zA-Z_%-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "} {printf "  %-18s %s\n", $$1, $$2}'

test: ## Run unit tests (pure Go, no SDL needed)
	CGO_ENABLED=0 go test $(GO_PACKAGES)

lint: ## Check formatting and run go vet
	@test -z "$$(gofmt -l $(GO_SOURCES))" || { echo "Files need gofmt:"; gofmt -l $(GO_SOURCES); exit 1; }
	CGO_ENABLED=0 go vet $(GO_PACKAGES)

fuzz: ## Run each fuzz target of the patch parsers for FUZZTIME (short runs)
	$(foreach f,$(PATCH_FUZZERS),CGO_ENABLED=0 go test ./internal/patch -run '^$$' -fuzz '^$(f)$$' -fuzztime $(FUZZTIME) &&) true

contract: ## Validate the OpenAPI contract and check the test vectors (Go and Swift copies)
	$(DOCKER) run --rm -v "$(CURDIR)/protocol":/spec:ro -w /spec $(REDOCLY_IMAGE) \
		lint --config redocly.yaml openapi.yaml
	CGO_ENABLED=0 go run ./protocol/test-vectors/gen -check -out protocol/test-vectors
	scripts/sync-test-vectors.sh --check

vectors: ## Regenerate the test vectors and copy them to the Swift test resources
	CGO_ENABLED=0 go run ./protocol/test-vectors/gen -out protocol/test-vectors
	scripts/sync-test-vectors.sh

headless: ## Run the pak headless (server + Bonjour) on a temporary copy of testdata/sdcard
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build -o $(HOST_BIN) ./cmd/reminislink
	@tmp="$${TMPDIR:-/tmp}" && sd="$$(mktemp -d "$${tmp%/}/reminislink-sd.XXXXXX")" && \
		cp -R testdata/sdcard/. "$$sd/" && \
		echo "Fake SD card copied to $$sd (logs in $$sd/.userdata/tg5040/logs)" && \
		exec $(HOST_BIN) --headless --sd "$$sd" $(HEADLESS_ARGS)
