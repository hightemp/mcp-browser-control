SHELL := /bin/sh

APP_NAME := mcp-browser-control
BUILD_DIR := bin
BINARY := $(BUILD_DIR)/$(APP_NAME)
COMMAND := ./cmd/server
COVERAGE_FILE := coverage.out

GO ?= go
NPM ?= npm
GOLANGCI_LINT ?= golangci-lint

.DEFAULT_GOAL := help
.NOTPARALLEL: check verify

.PHONY: help deps fmt fmt-check build run test test-race coverage coverage-html vet lint extension-check check verify clean

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
		'  extension-check Check extension JavaScript and run its tests' \
		'  check           Run non-mutating validation checks' \
		'  verify          Format, check, measure coverage, and build' \
		'  clean           Remove generated build and coverage files'

deps:
	$(GO) mod download
	$(GO) mod tidy

fmt:
	$(GO) fmt ./...

fmt-check:
	@test -z "$$(gofmt -l .)"

build:
	@mkdir -p "$(BUILD_DIR)"
	$(GO) build -trimpath -o "$(BINARY)" "$(COMMAND)"

run:
	$(GO) run "$(COMMAND)" $(ARGS)

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

coverage:
	$(GO) test -count=1 -covermode=atomic -coverpkg=./internal/... -coverprofile="$(COVERAGE_FILE)" ./internal/...
	$(GO) tool cover -func="$(COVERAGE_FILE)" | tail -n 1

coverage-html: coverage
	$(GO) tool cover -html="$(COVERAGE_FILE)" -o coverage.html

vet:
	$(GO) vet ./...

lint:
	$(GOLANGCI_LINT) run ./...

extension-check:
	@for file in chrome-extension/src/*.js chrome-extension/tests/*.js; do \
		node --check "$$file"; \
	done
	$(NPM) test --prefix chrome-extension

check: fmt-check vet lint test-race extension-check

verify: fmt check coverage build

clean:
	rm -f "$(BINARY)" "$(COVERAGE_FILE)" coverage.html
	@rmdir "$(BUILD_DIR)" 2>/dev/null || true
