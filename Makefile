SHELL := /bin/sh

APP_NAME := mcp-browser-control
BUILD_DIR := bin
BINARY := $(BUILD_DIR)/$(APP_NAME)
COMMAND := ./cmd/server
COVERAGE_FILE := coverage.out
GO_PACKAGES := ./cmd/... ./internal/...

GO ?= go
NPM ?= npm
GOLANGCI_LINT ?= golangci-lint
GOVULNCHECK ?= govulncheck
GITLEAKS ?= gitleaks

.DEFAULT_GOAL := help
.NOTPARALLEL: check verify

.PHONY: help deps fmt fmt-check build run test test-race coverage coverage-html vet lint extension-format-check extension-lint extension-test extension-build extension-license-check extension-check security-check check verify clean

help:
	@printf '%s\n' \
		'Available targets:' \
		'  deps            Download and normalize dependencies' \
		'  fmt             Format Go source files' \
		'  fmt-check       Check Go formatting without changing files' \
		'  build           Build bin/mcp-browser-control' \
		'  run             Run the server; pass flags through ARGS' \
		'  test            Run all Go tests' \
		'  test-race       Run all Go tests with the race detector' \
		'  coverage        Write coverage.out and print total coverage' \
		'  coverage-html   Generate coverage.html from coverage.out' \
		'  vet             Run go vet' \
		'  lint            Run golangci-lint' \
		'  extension-check Check extension formatting, lint, and tests' \
		'  extension-build Build the unpacked production extension' \
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
	$(GO) build -trimpath -o "$(BINARY)" "$(COMMAND)"

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

extension-license-check:
	$(NPM) run license:check --prefix chrome-extension

extension-check: extension-format-check extension-lint extension-test

security-check: extension-license-check
	$(GOVULNCHECK) ./cmd/server
	sh scripts/check-go-licenses.sh
	$(NPM) audit --prefix chrome-extension --audit-level=high
	$(GITLEAKS) git --redact --no-banner .

check: fmt-check vet lint test-race extension-check

verify: fmt check coverage build

clean:
	rm -f "$(BINARY)" "$(COVERAGE_FILE)" coverage.html
	rm -rf chrome-extension/dist
	@rmdir "$(BUILD_DIR)" 2>/dev/null || true
