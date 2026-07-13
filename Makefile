SHELL := /bin/sh

GO ?= go
BINARY ?= bin/skywalking-mirror
IMAGE ?= skywalking-mirror-dispatcher:local

GOAPI_MODULE := skywalking.apache.org/repo/goapi
GOAPI_VERSION := v0.0.0-20260521015734-5c05525a3cce

.DEFAULT_GOAL := help

.PHONY: help fmt fmt-check verify-goapi test test-race vet check build run docker-build

help:
	@printf '%s\n' \
		'fmt              Format Go source files' \
		'fmt-check        Fail when Go source files are not formatted' \
		'verify-goapi     Verify the pinned official goapi version' \
		'test             Run unit and integration tests' \
		'test-race        Run tests with the race detector' \
		'vet              Run go vet' \
		'check            Run fmt-check, goapi verification, tests, race and vet' \
		'build            Build the binary into $(BINARY)' \
		'run              Run the service with the current environment' \
		'docker-build     Build container image $(IMAGE)'

fmt:
	$(GO) fmt ./cmd/... ./internal/...

fmt-check:
	@files="$$(gofmt -l ./cmd ./internal)"; \
	if [ -n "$$files" ]; then \
		printf 'Go files require formatting:\n%s\n' "$$files" >&2; \
		exit 1; \
	fi

verify-goapi:
	@actual="$$( $(GO) list -m $(GOAPI_MODULE) )"; \
	expected="$(GOAPI_MODULE) $(GOAPI_VERSION)"; \
	if [ "$$actual" != "$$expected" ]; then \
		printf 'goapi version mismatch: expected %s, got %s\n' "$$expected" "$$actual" >&2; \
		exit 1; \
	fi

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

check: fmt-check verify-goapi test test-race vet

build:
	@mkdir -p "$(dir $(BINARY))"
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w -buildid=' -o "$(BINARY)" ./cmd/skywalking-mirror

run:
	$(GO) run ./cmd/skywalking-mirror

docker-build:
	docker build -t "$(IMAGE)" .
