# paseo-tui: run `make help` for the targets.

BINARY    := paseo-tui
PASEO_REF ?= v0.17.3
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X main.version=$(VERSION)
GO        ?= go
DIST      := dist

# Release targets as GOOS/GOARCH.
PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64

export PASEO_REF

.PHONY: all build bridge run test vet fmt fmt-check lint release clean distclean help

all: build ## Build the bridge and the binary (default)

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

bridge: bridge/dist/paseo-bridge.mjs ## Bundle the Node bridge (fetches Paseo $(PASEO_REF) once)

bridge/dist/paseo-bridge.mjs: bridge/bridge.ts scripts/build-bridge.sh
	./scripts/build-bridge.sh

build: bridge ## Build ./paseo-tui for this machine
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) .

run: build ## Build and run; pass flags with ARGS="--host host:port"
	./$(BINARY) $(ARGS)

test: bridge ## Run the tests
	$(GO) test ./...

vet: bridge ## Run go vet
	$(GO) vet ./...

fmt: ## Format the Go code
	gofmt -w *.go

fmt-check: ## Fail if any Go file isn't gofmt-formatted
	@test -z "$$(gofmt -l *.go)" || (echo "These files need 'make fmt':"; gofmt -l *.go; exit 1)

lint: fmt-check vet ## fmt-check + vet

release: bridge ## Cross-compile release archives and checksums into dist/
	@mkdir -p $(DIST)
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=""; [ "$$os" = windows ] && ext=.exe; \
		name=$(BINARY)_$${os}_$${arch}; stage=$(DIST)/stage/$$name; \
		mkdir -p "$$stage"; \
		echo "  $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "$$stage/$(BINARY)$$ext" . ; \
		cp README.md LICENSE NOTICE "$$stage/"; \
		if [ "$$os" = windows ]; then (cd "$(DIST)/stage" && zip -qr "../$$name.zip" "$$name"); \
		else tar -C "$(DIST)/stage" -czf "$(DIST)/$$name.tar.gz" "$$name"; fi; \
	done
	@cd $(DIST) && (sha256sum *.tar.gz *.zip 2>/dev/null || shasum -a 256 *.tar.gz *.zip) > checksums.txt
	@ls -1 $(DIST)/*.tar.gz $(DIST)/*.zip $(DIST)/checksums.txt

clean: ## Remove build output
	go clean
	@test ! -e $(BINARY) || unlink $(BINARY)
	@test ! -d $(DIST) || find $(DIST) -mindepth 1 -delete

distclean: clean ## Also remove the bridge bundle and the fetched Paseo source
	@test ! -d bridge/dist || find bridge/dist -mindepth 1 -delete
	@test ! -f bridge/package.json || unlink bridge/package.json
	@test ! -d .paseo-src || find .paseo-src -delete
