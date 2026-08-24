SHELL := /bin/sh

APP_NAME := mcp-browser-control
BUILD_DIR := bin
BINARY := $(BUILD_DIR)/$(APP_NAME)
COMMAND := ./cmd/server
COVERAGE_FILE := coverage.out
GO_PACKAGES := ./cmd/... ./internal/...
VERSION ?= $(shell node -p "require('./chrome-extension/manifest.json').version")
COMMIT ?= $(shell git rev-parse HEAD)
SOURCE_DATE_EPOCH ?= $(shell git show -s --format=%ct HEAD)
BUILD_DATE := $(shell date -u --date="@$(SOURCE_DATE_EPOCH)" +%Y-%m-%dT%H:%M:%S.000Z)
TARGETS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
RELEASE_DIR ?= release
VERSION_PACKAGE := github.com/hightemp/go_mcp_browser_ext_tool/internal/app
BUILD_LDFLAGS := -s -w -buildid= -X $(VERSION_PACKAGE).Version=$(VERSION) -X $(VERSION_PACKAGE).Commit=$(COMMIT) -X $(VERSION_PACKAGE).BuildDate=$(BUILD_DATE)

GO ?= go
NPM ?= npm
GOLANGCI_LINT ?= golangci-lint
GOVULNCHECK ?= govulncheck
GITLEAKS ?= gitleaks
CHROME_BIN ?= chromium
E2E_EXTENSION_DIR := $(CURDIR)/chrome-extension/dist/e2e-extension

.DEFAULT_GOAL := help
.NOTPARALLEL: check verify

.PHONY: help deps fmt fmt-check build version release release-check tool-reference tool-reference-check run test test-race coverage coverage-html vet lint extension-format-check extension-lint extension-test extension-build extension-e2e-build extension-license-check extension-check e2e security-check check verify clean

help:
	@printf '%s\n' \
		'Available targets:' \
		'  deps            Download and normalize dependencies' \
		'  fmt             Format Go source files' \
		'  fmt-check       Check Go formatting without changing files' \
		'  build           Build bin/mcp-browser-control' \
		'  version         Print build version metadata' \
		'  release         Build cross-platform release artifacts' \
		'  release-check   Build twice and compare release checksums' \
		'  tool-reference  Generate docs/tool-reference.md' \
		'  tool-reference-check  Check generated tool documentation' \
		'  run             Run the server; pass flags through ARGS' \
		'  test            Run all Go tests' \
		'  test-race       Run all Go tests with the race detector' \
		'  coverage        Write coverage.out and print total coverage' \
		'  coverage-html   Generate coverage.html from coverage.out' \
		'  vet             Run go vet' \
		'  lint            Run golangci-lint' \
		'  extension-check Check extension formatting, lint, and tests' \
		'  extension-build Build the unpacked production extension' \
		'  e2e             Run two-profile Chrome for Testing E2E' \
		'  security-check  Scan vulnerabilities, licenses, and secrets' \
		'  check           Run non-mutating validation checks' \
		'  verify          Format, check, measure coverage, and build' \
		'  clean           Remove generated build and coverage files'

deps:
	$(GO) mod download
	$(GO) mod tidy
	$(NPM) ci --prefix chrome-extension

fmt:
	$(GO) fmt $(GO_PACKAGES)

fmt-check:
	@files="$$(git ls-files '*.go')"; test -z "$$(gofmt -l $$files)"

build:
	@mkdir -p "$(BUILD_DIR)"
	CGO_ENABLED=0 $(GO) build -mod=readonly -trimpath -buildvcs=false \
		-ldflags="$(BUILD_LDFLAGS)" -o "$(BINARY)" "$(COMMAND)"

version: build
	"$(BINARY)" --version

release:
	VERSION="$(VERSION)" COMMIT="$(COMMIT)" SOURCE_DATE_EPOCH="$(SOURCE_DATE_EPOCH)" \
		TARGETS="$(TARGETS)" RELEASE_DIR="$(RELEASE_DIR)" sh scripts/build-release.sh

release-check: release
	VERSION="$(VERSION)" COMMIT="$(COMMIT)" SOURCE_DATE_EPOCH="$(SOURCE_DATE_EPOCH)" \
		TARGETS="$(TARGETS)" RELEASE_DIR="$(RELEASE_DIR)" \
		sh scripts/check-release-reproducibility.sh

tool-reference:
	$(GO) run ./cmd/tool-reference -output docs/tool-reference.md

tool-reference-check:
	$(GO) run ./cmd/tool-reference -check -output docs/tool-reference.md

run:
	$(GO) run "$(COMMAND)" $(ARGS)

test:
	$(GO) test $(GO_PACKAGES)

test-race:
	$(GO) test -race $(GO_PACKAGES)

coverage:
	$(GO) test -count=1 -covermode=atomic -coverpkg=./internal/... -coverprofile="$(COVERAGE_FILE)" ./internal/...
	$(GO) tool cover -func="$(COVERAGE_FILE)" | tail -n 1

coverage-html: coverage
	$(GO) tool cover -html="$(COVERAGE_FILE)" -o coverage.html

vet:
	$(GO) vet $(GO_PACKAGES)

lint:
	$(GOLANGCI_LINT) run $(GO_PACKAGES)

extension-format-check:
	$(NPM) run format:check --prefix chrome-extension

extension-lint:
	$(NPM) run lint --prefix chrome-extension

extension-test:
	$(NPM) test --prefix chrome-extension

extension-build:
	$(NPM) run build --prefix chrome-extension

extension-e2e-build:
	$(NPM) run build:e2e --prefix chrome-extension

extension-license-check:
	$(NPM) run license:check --prefix chrome-extension

extension-check: extension-format-check extension-lint extension-test

e2e: extension-e2e-build
	CHROME_BIN="$(CHROME_BIN)" MCP_BROWSER_EXTENSION_DIR="$(E2E_EXTENSION_DIR)" $(GO) test -tags=e2e -count=1 -timeout=2m ./internal/e2e

security-check: extension-license-check
	$(GOVULNCHECK) ./cmd/server
	sh scripts/check-go-licenses.sh
	$(NPM) audit --prefix chrome-extension --audit-level=high
	$(GITLEAKS) git --redact --no-banner .

check: fmt-check vet lint test-race extension-check tool-reference-check

verify: fmt check coverage build

clean:
	rm -f "$(BINARY)" "$(COVERAGE_FILE)" coverage.html
	rm -rf chrome-extension/dist
	rm -rf release
	@rmdir "$(BUILD_DIR)" 2>/dev/null || true
