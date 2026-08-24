SHELL := /bin/sh

APP_NAME := mcp-browser-control
BUILD_DIR := bin
BINARY := $(BUILD_DIR)/$(APP_NAME)
COMMAND := ./cmd/server
COVERAGE_FILE := coverage.out
COVERAGE_MIN ?= 80.0
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
ACTIONLINT ?= actionlint
CHROME_BIN ?= chromium
E2E_EXTENSION_DIR := $(CURDIR)/chrome-extension/dist/e2e-extension
SOAK_DURATION ?= 8h
SOAK_SMOKE_DURATION ?= 5s
SOAK_TIMEOUT ?= 9h

.DEFAULT_GOAL := help
.NOTPARALLEL: check verify

.PHONY: help deps fmt fmt-check build version release release-check release-readiness release-readiness-check tool-reference tool-reference-check run test test-race coverage coverage-check coverage-html vet lint extension-format-check extension-lint extension-test extension-build extension-e2e-build extension-license-check extension-check e2e performance soak soak-smoke workflow-check security-check check verify clean

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
		'  release-readiness  Run every automated release-candidate gate' \
		'  release-readiness-check  Check static release metadata and docs' \
		'  tool-reference  Generate docs/tool-reference.md' \
		'  tool-reference-check  Check generated tool documentation' \
		'  run             Run the server; pass flags through ARGS' \
		'  test            Run all Go tests' \
		'  test-race       Run all Go tests with the race detector' \
		'  coverage        Write coverage.out and print total coverage' \
		'  coverage-check  Require total internal coverage to meet COVERAGE_MIN' \
		'  coverage-html   Generate coverage.html from coverage.out' \
		'  vet             Run go vet' \
		'  lint            Run golangci-lint' \
		'  extension-check Check extension formatting, lint, and tests' \
		'  extension-build Build the unpacked production extension' \
		'  e2e             Run two-profile Chrome for Testing E2E' \
		'  performance     Verify latency NFRs and print Go benchmarks' \
		'  soak-smoke      Run the reconnect/event soak harness for 5 seconds' \
		'  soak            Run the reconnect/event soak harness for 8 hours' \
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

release-readiness-check:
	sh scripts/check-release-readiness.sh

release-readiness: verify workflow-check security-check e2e performance soak-smoke release-check
	RELEASE_REQUIRE_ARTIFACTS=1 sh scripts/check-release-readiness.sh

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

coverage-check: coverage
	@total="$$( $(GO) tool cover -func="$(COVERAGE_FILE)" | awk '/^total:/ { gsub(/%/, "", $$3); print $$3 }')"; \
		test -n "$$total"; \
		awk -v actual="$$total" -v minimum="$(COVERAGE_MIN)" 'BEGIN { exit !(actual + 0 >= minimum + 0) }' || { \
			echo "coverage $$total% is below required $(COVERAGE_MIN)%" >&2; exit 1; \
		}; \
		echo "coverage gate passed: $$total% >= $(COVERAGE_MIN)%"

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

performance:
	$(GO) test -count=1 -v -run 'NFR$$' ./internal/router ./internal/tools
	$(GO) test -run '^$$' -bench 'Benchmark(RouterRoundTrip|BrowserList50)$$' -benchmem ./internal/router ./internal/tools

soak:
	MCP_BROWSER_SOAK_DURATION="$(SOAK_DURATION)" MCP_BROWSER_SOAK_TIMEOUT="$(SOAK_TIMEOUT)" \
		GO="$(GO)" bash scripts/run-soak.sh

soak-smoke:
	MCP_BROWSER_SOAK_DURATION="$(SOAK_SMOKE_DURATION)" MCP_BROWSER_SOAK_TIMEOUT="2m" \
		MCP_BROWSER_SOAK_RECONNECT_INTERVAL="25ms" \
		MCP_BROWSER_SOAK_EVENT_INTERVAL="50ms" \
		GO="$(GO)" bash scripts/run-soak.sh

workflow-check:
	$(ACTIONLINT) .github/workflows/*.yml

security-check: extension-license-check
	$(GOVULNCHECK) ./cmd/server
	sh scripts/check-go-licenses.sh
	$(NPM) audit --prefix chrome-extension --audit-level=high
	$(GITLEAKS) git --redact --no-banner .

check: fmt-check vet lint test-race extension-check tool-reference-check

verify: fmt check coverage-check build

clean:
	rm -f "$(BINARY)" "$(COVERAGE_FILE)" coverage.html
	rm -rf chrome-extension/dist
	rm -rf release
	@rmdir "$(BUILD_DIR)" 2>/dev/null || true
