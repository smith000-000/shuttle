GO ?= go
VERSION ?= dev-$(shell git rev-parse --short HEAD)
COMMIT ?= $(shell git rev-parse --short HEAD)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X aiterm/internal/version.Version=$(VERSION) -X aiterm/internal/version.Commit=$(COMMIT) -X aiterm/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: build package test test-integration test-packaging run

build:
	mkdir -p bin
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/shuttle ./cmd/shuttle

package:
	VERSION="$(VERSION)" COMMIT="$(COMMIT)" BUILD_DATE="$(BUILD_DATE)" TARGETS="$(TARGETS)" DIST_DIR="$(DIST_DIR)" ./scripts/package-release.sh

test-packaging:
	./scripts/test-install-release.sh

test:
	$(GO) test ./...

test-integration:
	$(GO) test ./integration/...

run:
	$(GO) run ./cmd/shuttle
