# ReminisLink - pak and protocol
#
# Main targets:
#   make test       run Go unit tests (no toolchain, CGO_ENABLED=0)
#   make lint       gofmt + go vet
#
# The pak build targets (build, pak, release, docker-*) come with milestone M7.

GO_SOURCES    := internal
GO_PACKAGES   := ./...

.DEFAULT_GOAL := help
.PHONY: help test lint

help: ## Show this help
	@grep -E '^[a-zA-Z_%-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "} {printf "  %-18s %s\n", $$1, $$2}'

test: ## Run unit tests (pure Go, no SDL needed)
	CGO_ENABLED=0 go test $(GO_PACKAGES)

lint: ## Check formatting and run go vet
	@test -z "$$(gofmt -l $(GO_SOURCES))" || { echo "Files need gofmt:"; gofmt -l $(GO_SOURCES); exit 1; }
	CGO_ENABLED=0 go vet $(GO_PACKAGES)
