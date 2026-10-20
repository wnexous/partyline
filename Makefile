# Tool binaries; override to pin or wrap (e.g. GOLANGCI_LINT="go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0").
GOLANGCI_LINT ?= golangci-lint
SQLC ?= sqlc
TYGO ?= tygo
BUN ?= bun

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/wnexous/partyline/internal/cli.Version=$(VERSION)

# Paths written by `make generate`; they are committed and must not drift.
GENERATED := internal/store/db web/src/lib/api/types.gen.ts

HAS_WEB := $(wildcard web/package.json)

.PHONY: all generate check-generate lint vet test build check e2e web-install web-test web-check web-build integrations-test

all: check

## generate: regenerate committed code
generate:
	$(SQLC) generate
	$(TYGO) generate

## check-generate: fail when generated code differs from what is committed (or staged)
check-generate: generate
	@if [ -n "$(GENERATED)" ]; then \
		git diff --exit-code -- $(GENERATED) || { echo "generated code is stale: run make generate"; exit 1; }; \
		untracked=$$(git ls-files --others --exclude-standard -- $(GENERATED)); \
		if [ -n "$$untracked" ]; then echo "untracked generated files: $$untracked"; exit 1; fi; \
	fi

## lint: static analysis
lint:
	$(GOLANGCI_LINT) run ./...

## vet: go vet
vet:
	go vet ./...

## test: all Go tests with the race detector (plus web tests when web/ exists)
test:
	go test -race -count=1 -timeout 20m ./...
ifneq ($(HAS_WEB),)
	$(MAKE) web-test
endif

## build: build the partyline binary into bin/
build:
ifneq ($(HAS_WEB),)
	$(MAKE) web-build
endif
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/partyline ./cmd/partyline

## check: the full gate used locally and in CI
check: check-generate lint vet
ifneq ($(HAS_WEB),)
	$(MAKE) web-check
endif
	$(MAKE) test
	$(MAKE) integrations-test
ifneq ($(HAS_WEB),)
	$(MAKE) web-build
endif
	go build ./...

## e2e: Playwright smoke suite against the built binary (not part of check; CI runs it as its own job).
## Set PLAYWRIGHT_CHROMIUM_EXECUTABLE to reuse an installed Chromium instead of `playwright install`.
e2e: build
	cd web && PARTYLINE_BIN=$(CURDIR)/bin/partyline $(BUN) x playwright test

## integrations-test: unit tests of the omp, pi and OpenCode adapters (bun test, fake host APIs)
integrations-test:
	cd integrations && $(BUN) test

web-install:
	cd web && $(BUN) install --frozen-lockfile

web-test: web-install
	cd web && $(BUN) run test

web-check: web-install
	cd web && $(BUN) run lint && $(BUN) run typecheck

## web-build: build web/ and copy web/dist into the embed directory (internal/webui/dist)
web-build: web-install
	cd web && $(BUN) run build
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
	cp -R web/dist/. internal/webui/dist/
