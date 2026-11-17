# Single entry point for building, testing and linting dnsmasq-manager.
# CI and the session-start hook call these targets rather than raw go commands.

# Stamped into the binary; falls back to "dev" outside a git checkout.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# Pinned so local, cloud and CI runs lint identically. Must be a v2 release
# that builds with the Go version in go.mod.
GOLANGCI_LINT_VERSION := v2.15.0

# Directory `go install` writes to: GOBIN when set, otherwise GOPATH/bin.
GO_BIN_DIR = $(or $(shell go env GOBIN),$(shell go env GOPATH)/bin)

# Prefer golangci-lint on PATH; otherwise use the copy `make tools` installed,
# so lint works in shells where the Go bin directory is not on PATH.
GOLANGCI_LINT ?= $(or $(shell command -v golangci-lint 2>/dev/null),$(GO_BIN_DIR)/golangci-lint)

.PHONY: build dist test test-hook lint tools integration integration-docker

# Static linux/amd64 binary for the DNS host; CGO off keeps it statically linked.
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X github.com/gsdevme/dnsmasq-manager/internal/version.Version=$(VERSION)" -o bin/dnsmasq-manager ./cmd/dnsmasq-manager

# The release asset set: the binary under its release name and its SHA-256
# checksum. sha256sum runs inside dist so the checksum line names the file
# without a directory.
dist: build
	rm -rf dist
	mkdir -p dist
	cp bin/dnsmasq-manager dist/dnsmasq-manager-linux-amd64
	cd dist && sha256sum dnsmasq-manager-linux-amd64 > dnsmasq-manager-linux-amd64.sha256

test:
	go test -race ./...

# Runs the session-start hook against stub commands; needs only bash.
test-hook:
	bash test/hook/hook_test.sh

lint:
	$(GOLANGCI_LINT) run ./...

tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

# Probes a real dnsmasq with the mock config in testdata/dnsmasq. Needs Linux
# with dnsmasq and dig on PATH; elsewhere use integration-docker.
integration:
	@if [ "$$(uname -s)" != "Linux" ]; then echo "integration tests need Linux; run 'make integration-docker'" >&2; exit 1; fi
	@command -v dnsmasq >/dev/null 2>&1 || { echo "dnsmasq is not on PATH; install it or run 'make integration-docker'" >&2; exit 1; }
	@command -v dig >/dev/null 2>&1 || { echo "dig is not on PATH; install it (bind-tools or dnsutils) or run 'make integration-docker'" >&2; exit 1; }
	go test -tags integration -count=1 -v ./test/integration/

# Runs the integration probes in Alpine against the production dnsmasq version.
integration-docker:
	docker build -f test/integration/Dockerfile -t dnsmasq-manager-integration . && docker run --rm dnsmasq-manager-integration
