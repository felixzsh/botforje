GO ?= go
BIN := bin/botflota
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
CONFIG ?= config/examples/reference.yml
DEV_CONFIG ?= .dev-data/config.yml
DEV_DATA ?= .dev-data
ARGS ?=
LINE_MAX := 100

.DEFAULT_GOAL := help
.PHONY: help build run dev dev-reset test test-race vet fmt fmt-check \
	lines-check check schema validate tidy clean

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

build: ## Build the binary into bin/botflota
	$(GO) build -ldflags "-X main.version=$(VERSION)" -o $(BIN) ./cmd/botflota

run: build ## Run the CLI against the real config: make run ARGS="config validate"
	exec $(BIN) $(ARGS)

dev: build ## Run the CLI in development: make dev ARGS="bot list"
	BOTFLOTA_CONFIG=$(DEV_CONFIG) exec $(BIN) $(ARGS)

dev-reset: ## Delete the development database, never the local config or real one
	@test -n "$(DEV_DATA)" || { echo "refusing: DEV_DATA is empty"; exit 1; }
	rm -f $(DEV_DATA)/botflota.db* $(DEV_DATA)/owner.token

test: ## Run all tests
	$(GO) test ./...

test-race: ## Run all tests with the race detector
	$(GO) test -race ./...

vet: ## Run go vet
	$(GO) vet ./...

fmt: ## Format the source
	gofmt -w $$(git ls-files '*.go')

fmt-check: ## Fail if any file is not gofmt'd
	@out=$$(gofmt -l $$(git ls-files '*.go')); \
	if [ -n "$$out" ]; then echo "not formatted:"; echo "$$out"; exit 1; fi

lines-check: ## Fail if a tracked file has a line longer than LINE_MAX
	@bad=$$(git ls-files -z | xargs -0 awk -v max=$(LINE_MAX) \
		'FILENAME != "config.schema.json" && FILENAME != "go.sum" \
		&& length > max {printf "%s:%d (%d)\n", FILENAME, FNR, length}'); \
	if [ -n "$$bad" ]; then \
		echo "lines over $(LINE_MAX) characters:"; echo "$$bad"; exit 1; \
	fi

check: vet fmt-check lines-check test ## Run vet, format check, line check, and tests

schema: ## Regenerate config.schema.json (run from the repo root)
	$(GO) run ./cmd/botflota config schema -o config.schema.json

validate: ## Validate a config: make validate CONFIG=path
	$(GO) run ./cmd/botflota config validate --config $(CONFIG)

tidy: ## Tidy go.mod and go.sum
	$(GO) mod tidy

clean: ## Remove build artifacts
	rm -rf bin
