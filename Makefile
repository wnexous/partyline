# Tool binaries; override to pin or wrap (e.g. GOLANGCI_LINT="go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0").
GOLANGCI_LINT ?= golangci-lint
SQLC ?= sqlc
TYGO ?= tygo
BUN ?= bun

# RACE=1 adds the race detector to the Go tests. Local runs default to off for speed; CI and
# `make check-race` set RACE=1.
RACE ?= 0

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/wnexous/partyline/internal/cli.Version=$(VERSION)

# Paths written by `make generate`; they are committed and must not drift.
GENERATED := internal/store/db web/src/lib/api/types.gen.ts

HAS_WEB := $(wildcard web/package.json)

.PHONY: all generate check-generate check-drift lint vet test test-go build check check-race e2e web-install web-test web-check web-verify web-build integrations-test

all: check

## generate: regenerate committed code
generate:
	$(SQLC) generate
	$(TYGO) generate

## check-generate: fail when generated code differs from what is committed (or staged)
check-generate: generate check-drift

## check-drift: fail when the working tree's generated code differs from the index (no generation)
check-drift:
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

## test-go: sharded Go tests only (RACE=1 adds -race; SHARDS=N sets the shard count)
test-go:
	RACE=$(RACE) SHARDS=$(SHARDS) scripts/gotest.sh

## test: all Go tests (sharded; plus web tests when web/ exists)
test: test-go
ifneq ($(HAS_WEB),)
	$(MAKE) web-test
endif

## build: build the partyline binary into bin/
build:
ifneq ($(HAS_WEB),)
	$(MAKE) web-build
endif
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/partyline ./cmd/partyline

## check: the full gate used locally and in CI; independent stages run in parallel
check:
	RACE=$(RACE) SHARDS=$(SHARDS) scripts/check.sh

## check-race: the same gate with the race detector (before pushing; CI runs it)
check-race:
	$(MAKE) check RACE=1

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
	cd web && $(BUN) run lint && $(BUN) run check:layers && $(BUN) run typecheck

## web-verify: web lint, layer check, typecheck and unit tests in one stage of `check`
web-verify: web-install
	cd web && $(BUN) run lint && $(BUN) run check:layers && $(BUN) run typecheck && $(BUN) run test

## web-build: build web/ and copy web/dist into the embed directory (internal/webui/dist)
web-build: web-install
	cd web && $(BUN) run build
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
	cp -R web/dist/. internal/webui/dist/
