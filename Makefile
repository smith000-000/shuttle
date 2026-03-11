GO ?= go

.PHONY: test test-integration run

test:
	$(GO) test ./...

test-integration:
	$(GO) test ./integration/...

run:
	$(GO) run ./cmd/shuttle
