BINARY     := toris
CMD        := ./cmd/toris
BIN_DIR    := ./bin
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -X 'github.com/tobibamidele/toris/internal/cli.Version=$(VERSION)' \
           -X 'github.com/tobibamidele/toris/internal/cli.Commit=$(COMMIT)' \
           -X 'github.com/tobibamidele/toris/internal/cli.BuildDate=$(BUILD_DATE)'

GO         := go
GOFLAGS    := -mod=mod
GONOSUMDB  := *

export GONOSUMDB
export GOFLAGS

.PHONY: all build test lint fmt vet clean doctor help

all: fmt vet build test

## build: compile the toris binary into ./bin/
build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD)
	@echo "✓ built $(BIN_DIR)/$(BINARY) ($(VERSION))"

## build-race: build with race detector enabled
build-race:
	@mkdir -p $(BIN_DIR)
	$(GO) build -race -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY)-race $(CMD)

## test: run all unit tests
test:
	$(GO) test ./... -count=1 -timeout 120s

## test-v: run all unit tests with verbose output
test-v:
	$(GO) test ./... -v -count=1 -timeout 120s

## test-race: run tests with the race detector
test-race:
	$(GO) test -race ./... -count=1 -timeout 120s

## test-cover: run tests and produce a coverage report
test-cover:
	$(GO) test ./... -coverprofile=coverage.out -covermode=atomic
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "✓ coverage report: coverage.html"

## lint: run golangci-lint (requires golangci-lint in PATH)
lint:
	golangci-lint run ./...

## fmt: run gofmt on all Go source files
fmt:
	$(GO) fmt ./...

## vet: run go vet
vet:
	$(GO) vet ./...

## tidy: tidy go.sum
tidy:
	$(GO) mod tidy

## clean: remove build artifacts
clean:
	rm -rf $(BIN_DIR) coverage.out coverage.html

## doctor: run 'toris doctor' against the built binary
doctor: build
	$(BIN_DIR)/$(BINARY) doctor

## help: print this help
help:
	@grep -E '^## ' Makefile | sed 's/## /  /'
