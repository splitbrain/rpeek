# Build automation for rpeek, the read-only remote diagnostic tool.

GO      ?= go
BINARY  := rpeek
BIN_DIR := bin

# Version stamped into the binary. Defaults to the current git description
# (nearest tag, commit, and a -dirty marker) and falls back to "dev" outside a
# git checkout, matching the unstamped build's own default.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# Linker flags: strip the symbol table and debug info, then stamp the version.
LDFLAGS := -s -w -X rpeek/internal/version.Version=$(VERSION)

# Release targets built by the dist target; mirrors the CI matrix.
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

# Build static binaries with no cgo, as the released binaries are.
export CGO_ENABLED := 0

.DEFAULT_GOAL := build
.PHONY: build install test vet fmt clean dist help

build: ## Build the binary for the host platform into bin/
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) ./cmd/rpeek

install: ## Install the binary into GOBIN with the version stamped in
	$(GO) install -trimpath -ldflags "$(LDFLAGS)" ./cmd/rpeek

test: ## Run the tests with the race detector
	$(GO) test -race ./...

vet: ## Run go vet over all packages
	$(GO) vet ./...

fmt: ## Format all Go sources
	$(GO) fmt ./...

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) dist $(BINARY)

dist: ## Build release archives for all platforms into dist/
	rm -rf dist
	mkdir -p dist
	@for target in $(PLATFORMS); do \
		os=$${target%/*}; arch=$${target#*/}; \
		echo "Building $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch $(GO) build -trimpath \
			-ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/rpeek || exit 1; \
		tar -czf "dist/$(BINARY)_$(VERSION)_$${os}_$${arch}.tar.gz" $(BINARY) README.md; \
		rm -f $(BINARY); \
	done
	cd dist && sha256sum *.tar.gz > SHA256SUMS

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}'
