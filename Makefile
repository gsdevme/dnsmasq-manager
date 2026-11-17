# Single entry point for building, testing and linting dnsmasq-manager.
# CI and the session-start hook call these targets rather than raw go commands.

# Stamped into the binary; falls back to "dev" outside a git checkout.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# Pinned so local, cloud and CI runs lint identically. Must be a v2 release
# that builds with the Go version in go.mod.
GOLANGCI_LINT_VERSION := v2.15.0

# Directory `go install` writes to: GOBIN when set, otherwise GOPATH/bin.
GO_BIN_DIR := $(or $(shell go env GOBIN),$(shell go env GOPATH)/bin)

# Prefer golangci-lint on PATH; otherwise use the copy `make tools` installed,
# so lint works in shells where the Go bin directory is not on PATH.
GOLANGCI_LINT ?= $(or $(shell command -v golangci-lint 2>/dev/null),$(GO_BIN_DIR)/golangci-lint)

.PHONY: build test lint tools

# Static linux/amd64 binary for the DNS host; CGO off keeps it statically linked.
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X github.com/gsdevme/dnsmasq-manager/internal/version.Version=$(VERSION)" -o bin/dnsmasq-manager ./cmd/dnsmasq-manager

test:
	go test -race ./...

lint:
	$(GOLANGCI_LINT) run ./...

tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
