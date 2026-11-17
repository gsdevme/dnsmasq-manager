# dnsmasq-manager

dnsmasq-manager will be a small Go service that manages LAN-only DNS records
in dnsmasq. It will offer a REST API for records and an external-dns webhook
provider on top, so a Kubernetes cluster can register and remove private
hostnames itself. It runs on the DNS host beside dnsmasq and writes a file in
dnsmasq's hosts directory.

Go module: `github.com/gsdevme/dnsmasq-manager`.

## Status

Foundation and release path only. There is no service yet: the binary
reports its version and does nothing else. What exists today:

- the service requirements, corrected against the DNS host, dnsmasq 2.91 and
  external-dns v0.24.0;
- a command line on [cobra](https://github.com/spf13/cobra)
  (`cmd/dnsmasq-manager`, `internal/cli`, `internal/version`) with one
  command, `version`; cobra is the one third-party dependency;
- releases published by release-please, each with a static `linux/amd64`
  binary, its SHA-256 checksum and an installer for Alpine (see
  [Installation](#installation) and [Releasing](#releasing));
- dnsmasq behaviour probes in `test/integration/`, run against a mock config
  in `testdata/dnsmasq/dnsmasq.conf`, and their results;
- a session-start hook that prepares Claude Code cloud sessions.

The hook has not yet been tried in a real cloud session, so whether the
plugin it installs is usable there is not yet verified (see below). The
service itself starts with its own design doc, written from the requirements.

## Installation

The installer puts the binary on an Alpine host. Today that binary only
reports its version, so installing it is a way to check the release path,
not to run the service.

Requirements: Alpine Linux 3.23 or later, `x86_64`, and root.

The quick form downloads the installer from the latest release and pipes it
to `sh`:

```sh
wget -qO- https://github.com/gsdevme/dnsmasq-manager/releases/latest/download/install.sh | sh
```

In this form a failed download of the script itself still exits 0, because
`sh` reads nothing and succeeds, so check for the `installed version` line
or use the form below when the exit status matters.

The safer form downloads it first, so you can read it before running it:

```sh
wget -O install.sh https://github.com/gsdevme/dnsmasq-manager/releases/latest/download/install.sh
less install.sh
sh install.sh
```

To install a specific release rather than the latest, set
`DNSMASQ_MANAGER_VERSION`. `0.1.0` and `v0.1.0` mean the same release:

```sh
DNSMASQ_MANAGER_VERSION=0.1.0 sh install.sh
```

The script reads its settings from the environment:

| Variable | Default | Meaning |
| --- | --- | --- |
| `DNSMASQ_MANAGER_VERSION` | `latest` | `latest`, or a release such as `v0.1.0`; anything else is refused before any download |
| `DNSMASQ_MANAGER_BASE_URL` | `https://github.com/gsdevme/dnsmasq-manager/releases` | Where releases are downloaded from; the tests point it at a local server |
| `DNSMASQ_MANAGER_INSTALL_DIR` | `/usr/local/bin` | Where the binary goes; must already exist and be writable |
| `DNSMASQ_MANAGER_OS_RELEASE` | `/etc/os-release` | The file read for the OS check; a test seam |

What the script does, in order:

1. Checks that it runs as root, on `x86_64`, on Alpine 3.23 or later (read
   from `os-release` without sourcing it), that the version is safe to put
   in a URL, that the install directory exists and is writable, and that
   `dnsmasq-manager` in it is not a directory. Any failed check exits 1
   before anything is downloaded.
2. Downloads `dnsmasq-manager-linux-amd64` and
   `dnsmasq-manager-linux-amd64.sha256` into a temporary directory.
3. Refuses a checksum file that is not exactly one well-formed line, then
   verifies the binary with `sha256sum -c`.
4. Copies the binary to a `mktemp` staging file in the install directory,
   sets mode `0755`, runs it with `version`, and only then renames it over
   `dnsmasq-manager`, so the swap is atomic.
5. Prints the previous version (`none` on a first install) and the version
   the new binary reports.

Messages start with `dnsmasq-manager install:`; errors go to stderr. A
failed check, download, checksum or run of the new binary leaves any
installed binary as it was,
and the temporary directory and staging file are removed on every exit.
Running the script again reinstalls or upgrades in place.

What the script does not do: it changes nothing but that one file. It
creates no service, user or config file, and does not touch dnsmasq or its
settings.

To uninstall, remove the binary:

```sh
rm /usr/local/bin/dnsmasq-manager
```

## Building and testing

Needs Go 1.27 (from `go.mod`). Lint needs golangci-lint at the version pinned
in the `Makefile`; `make tools` installs it with `go install`. The integration
probes need either Linux with `dnsmasq` 2.91 or later and `dig`, or Docker.

| Target | What it does |
| --- | --- |
| `make build` | Static `linux/amd64` binary at `bin/dnsmasq-manager`, with the version from `git describe` |
| `make dist` | The release files in `dist/`: the binary as `dnsmasq-manager-linux-amd64`, its `.sha256` checksum and `install.sh`; `VERSION=v0.1.0` stamps the version |
| `make test` | Unit tests with `-race` |
| `make lint` | `golangci-lint run ./...` |
| `make tools` | Installs the pinned golangci-lint |
| `make test-hook` | Tests the session-start hook against stub commands; needs only bash |
| `make test-install` | Tests `install.sh` against stub commands; needs bash, and runs the installer under `TEST_SH` (default `/bin/dash`) |
| `make test-install-docker` | Runs `install.sh` end to end in `alpine:3.23` against the real binary served over local HTTP, then the stub tests under BusyBox ash; needs Docker |
| `make integration` | Integration probes against a real `dnsmasq`; Linux only, with `dnsmasq` 2.91 or later and `dig` on PATH |
| `make integration-docker` | The same probes in `alpine:3.23` with dnsmasq 2.91, the production release and version and the supported minimums; use this on macOS |

## Releasing

Releases come from Conventional Commits on `main`, through
[release-please](https://github.com/googleapis/release-please) in
`.github/workflows/release.yml`. Its settings are in
`release-please-config.json`.

- Each push to `main` opens or updates a release pull request titled
  `build: release <version>`, with the next version and the changelog.
  Before 1.0 a breaking change bumps the minor version.
- Merging that pull request tags `v<version>` and creates the GitHub
  release. A second job in the same workflow checks out the tag, runs
  `make dist VERSION=<tag>` and uploads the three files:
  `dnsmasq-manager-linux-amd64`, `dnsmasq-manager-linux-amd64.sha256` and
  `install.sh`.
- Known gap: between the release being created and the upload finishing,
  the latest release has no assets, so an install in that window fails at
  download and changes nothing. Run it again once the assets are there.
- If the `assets` job fails, re-run that job on its own; the upload uses
  `--clobber`, so overwriting files from a partial run is safe.

One-time setup by the repository owner:

1. In the repository's Actions settings, enable "Allow GitHub Actions to
   create and approve pull requests", so release-please can open its pull
   request.
2. Make the repository public. The installer downloads release assets
   without a token, which only works for a public repository.

After a release, check that it has the three assets, then smoke-test it in
a throwaway container, never on the live DNS host:

```sh
docker run --rm --platform linux/amd64 alpine:3.23 sh -c 'wget -qO- https://github.com/gsdevme/dnsmasq-manager/releases/latest/download/install.sh | sh && dnsmasq-manager version'
```

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
- [Release path design](docs/superpowers/specs/2026-11-17-release-path-design.md)
  and [plan](docs/superpowers/plans/2026-11-17-release-path.md): the CLI on
  cobra, `make dist`, the installer and the release workflow.
- [Foundation design](docs/superpowers/specs/2026-11-17-agentic-repo-foundation-design.md)
  and [plan](docs/superpowers/plans/2026-11-17-agentic-repo-foundation.md):
  how this repository was set up.
