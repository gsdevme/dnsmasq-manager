# dnsmasq-manager

dnsmasq-manager will be a small Go service that manages LAN-only DNS records
in dnsmasq. It will offer a REST API for records and an external-dns webhook
provider on top, so a Kubernetes cluster can register and remove private
hostnames itself. It runs on the DNS host beside dnsmasq and writes a file in
dnsmasq's hosts directory.

Go module: `github.com/gsdevme/dnsmasq-manager`.

## Status

Foundation only. There is no service yet: the binary prints its version and
exits. What exists today:

- the service requirements, corrected against the DNS host, dnsmasq 2.91 and
  external-dns v0.24.0;
- a Go skeleton (`cmd/dnsmasq-manager`, `internal/version`) with no
  third-party dependencies;
- dnsmasq behaviour probes in `test/integration/`, run against a mock config
  in `testdata/dnsmasq/dnsmasq.conf`, and their results;
- a session-start hook that prepares Claude Code cloud sessions.

The hook has not yet been tried in a real cloud session, so whether the
plugin it installs is usable there is not yet verified (see below). The
service itself starts with its own design doc, written from the requirements.

## Building and testing

Needs Go 1.27 (from `go.mod`). Lint needs golangci-lint at the version pinned
in the `Makefile`; `make tools` installs it with `go install`. The integration
probes need either Linux with `dnsmasq` 2.91 or later and `dig`, or Docker.

| Target | What it does |
| --- | --- |
| `make build` | Static `linux/amd64` binary at `bin/dnsmasq-manager`, with the version from `git describe` |
| `make test` | Unit tests with `-race` |
| `make lint` | `golangci-lint run ./...` |
| `make tools` | Installs the pinned golangci-lint |
| `make test-hook` | Tests the session-start hook against stub commands; needs only bash |
| `make integration` | Integration probes against a real `dnsmasq`; Linux only, with `dnsmasq` 2.91 or later and `dig` on PATH |
| `make integration-docker` | The same probes in `alpine:3.23` with dnsmasq 2.91, the production release and version and the supported minimums; use this on macOS |

## Claude Code sessions

Work in this repo follows the workflow in [`CLAUDE.md`](CLAUDE.md):
requirements, design doc, plan, test-driven implementation, review, using the
superpowers plugin when it is loaded.

`.claude/settings.json` enables `superpowers@claude-plugins-official` for
local sessions and registers `.claude/hooks/session-start.sh` as a
`SessionStart` hook on startup and resume. Cloud sessions ignore
`enabledPlugins`, so the hook installs what a cloud session needs:

- Outside a cloud session (`CLAUDE_CODE_REMOTE` is not `true`) it exits at
  once and prints nothing.
- In a cloud session it installs the superpowers plugin from the
  `anthropics/claude-plugins-official` marketplace if `claude plugin list`
  does not already show it; installs `dnsmasq-base` and `dnsutils` with
  `apt-get` if `dnsmasq` or `dig` is missing (as root or with passwordless
  `sudo`, otherwise it skips them with a note); runs `make tools` if
  golangci-lint is missing; runs `go mod download`; and adds the Go bin
  directory to the session's PATH.
- It then prints a short status block (Go version, whether `dnsmasq`, `dig`,
  golangci-lint and superpowers are present) that becomes session context.
- Anything already present is left alone. Every step tolerates failure and
  the hook always exits 0, so a blocked download never stops the session.

Known limit: Claude Code loads plugins when a session starts, so a plugin the
hook installs during that start may only be usable after `/reload-plugins` or
in a second session. This is pending the cloud test, whose results go in
[`docs/cloud-session-findings.md`](docs/cloud-session-findings.md). Until
superpowers is loaded, follow the workflow in `CLAUDE.md` by hand.

## Documents

- [Service requirements](docs/requirements/dnsmasq-api.md): the input to the
  service design.
- [dnsmasq findings](docs/dnsmasq-findings.md): what the probes observed on
  dnsmasq 2.91, the minimum supported version, and why older ones are not
  supported.
- [Cloud session findings](docs/cloud-session-findings.md): the cloud test
  checklist, not yet answered.
- [Foundation design](docs/superpowers/specs/2026-11-17-agentic-repo-foundation-design.md)
  and [plan](docs/superpowers/plans/2026-11-17-agentic-repo-foundation.md):
  how this repository was set up.
