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

# Pinned for the same reason; `make tools` installs it with `go install`.
ACTIONLINT_VERSION := v1.7.12

# Prefer actionlint on PATH; otherwise the copy `make tools` installed.
ACTIONLINT ?= $(or $(shell command -v actionlint 2>/dev/null),$(GO_BIN_DIR)/actionlint)

# shellcheck is not a Go program, so `make tools` cannot install it. It is
# preinstalled on the CI runner; elsewhere install it from the system's
# packages (`brew install shellcheck`, `apt-get install shellcheck`).
SHELLCHECK ?= shellcheck

# Every shell file git tracks or would track (untracked and not ignored), so
# a new script is linted wherever it is added and before it is staged.
# $(wildcard) drops files deleted from the working tree. shellcheck reads
# each one's dialect from its shebang: install.sh and the scripts that run
# inside Alpine are POSIX sh, the rest are bash.
SHELL_FILES = $(wildcard $(shell git ls-files --cached --others --exclude-standard '*.sh'))

.PHONY: build dist test test-hook test-release test-install test-install-docker lint tools integration integration-docker e2e

# Static linux/amd64 binary for the DNS host; CGO off keeps it statically linked.
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X github.com/gsdevme/dnsmasq-manager/internal/version.Version=$(VERSION)" -o bin/dnsmasq-manager ./cmd/dnsmasq-manager

# The release asset set: the binary under its release name, its SHA-256
# checksum and the installer. sha256sum runs inside dist so the checksum line
# names the file without a directory.
dist: build
	rm -rf dist
	mkdir -p dist
	cp bin/dnsmasq-manager dist/dnsmasq-manager-linux-amd64
	cd dist && sha256sum dnsmasq-manager-linux-amd64 > dnsmasq-manager-linux-amd64.sha256
	cp install.sh dist/install.sh

test:
	go test -race ./...

# Runs the Claude Code hooks against stub commands; needs only bash.
test-hook:
	bash test/hook/hook_test.sh
	bash test/hook/pm_test.sh
	bash test/hook/pr_review_test.sh

# Runs scripts/publish-release.sh against a stub gh and checks the
# release-please settings it relies on; needs only bash.
test-release:
	bash test/release/publish_test.sh

# Runs install.sh against stub commands. Needs bash for the harness and the
# shell in TEST_SH (default /bin/dash) to run the installer.
test-install:
	bash test/install/install_test.sh

# Runs install.sh end to end in alpine:3.23 against the real binary served
# over local HTTP, then the stub suite under BusyBox ash. dist/ is excluded
# from the build context, so it is bind-mounted at /dist. --platform
# linux/amd64 makes uname -m report x86_64 on arm64 hosts, which the
# installer requires.
test-install-docker:
	$(MAKE) dist VERSION=v0.0.0-e2e
	docker build --platform linux/amd64 -f test/install/Dockerfile -t dnsmasq-manager-install-e2e .
	docker run --rm --platform linux/amd64 -v "$(CURDIR)/dist:/dist:ro" dnsmasq-manager-install-e2e

# golangci-lint on the Go code, shellcheck on every shell file (-x follows
# the files they source) and actionlint on the workflows in .github/workflows.
# A missing tool fails the target rather than skipping its check.
lint:
	$(GOLANGCI_LINT) run ./...
	$(SHELLCHECK) -x $(SHELL_FILES)
	$(ACTIONLINT)

# Installs the pinned golangci-lint and actionlint into the Go bin directory.
tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	go install github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)

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

# Runs the real external-dns v0.24.0 against the service beside dnsmasq 2.91,
# with a throwaway k3s for its Ingresses, under Docker Compose. Needs only
# Docker; see test/e2e/run.sh for the checks.
e2e:
	bash test/e2e/run.sh
