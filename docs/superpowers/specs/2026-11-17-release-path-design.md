# Release path for dnsmasq-manager

Date: 2026-11-17
Status: approved 2026-11-17

## Intent

The repository is about to be made public, and there is no way to install
what it builds. The service itself is not written yet: the binary prints its
version and exits. This change builds the release path only. A merge to
`main` leads to a versioned GitHub release with a static `linux/amd64`
binary and its checksum, and a script installs that binary on Alpine 3.23.

Everything that depends on the unwritten service design is deferred: the
`setup` command, the OpenRC service, the service user and the config file.
Those become a GitHub epic with one issue per phase, so the release path can
land now without guessing at the service.

## Goals

1. The command line moves onto `spf13/cobra`, so the `serve`, `setup` and
   `version` commands to come share one command tree. Today's behaviour,
   printing the version, stays available as `dnsmasq-manager version` and
   `dnsmasq-manager --version`.
2. `make dist` produces exactly the files a release publishes: the binary,
   its SHA-256 checksum and a copy of `install.sh`.
3. Every merge to `main` updates a release pull request kept by
   release-please. Merging that pull request tags a version, creates the
   GitHub release and uploads the three files from goal 2.
4. `install.sh` installs a chosen or the latest release on Alpine 3.23
   x86_64, checks the checksum before anything is installed, and refuses on
   any other system.
5. The installer is tested in two ways before it ships: a stub suite that
   runs anywhere with a POSIX shell, and an end-to-end run of the real
   script in `alpine:3.23` against a local HTTP server.
6. The README tells a reader how to install, pin a version and what the
   script changes on their machine.

## Non-goals

- The `setup` command, which will import the host's existing dnsmasq rules
  into the service's own config, after which the service owns the dnsmasq
  config.
- The `serve` command and any service behaviour.
- An OpenRC service, a service user, a config file, or any change to dnsmasq
  or its config. The installer installs one binary.
- `arm64` or any architecture other than `x86_64`, and any OS other than
  Alpine 3.23 or later.
- Signing releases beyond the SHA-256 checksum.

## Anti-goals

These would make the result a failure even if it technically works:

- An installer that can leave a half-written or unverified binary where the
  old one was. A failed download, a checksum mismatch or a truncated script
  must leave the existing install exactly as it was.
- An installer that changes anything other than the one binary: no service,
  no user, no config, no dnsmasq restart.
- A release workflow that gives a third-party action a write token through a
  moving tag.
- Release pull requests whose titles break the repo's commit rules (the
  default title uses `chore`, which the repo forbids).
- Anything private in the repo: the zone stays `<private-zone>`, test names
  use `home.example.test`, example addresses are in `10.0.0.0/24`.
- Using the live DNS host to test the installer or a release. The post-
  release check runs in a throwaway `alpine:3.23` container.

## Verified facts this design rests on

Measured on the owner's Mac (Go 1.27.1, cross-compiling with the flags of
`make build`: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath
-ldflags "-s -w -X ...Version=dev"`), 2026-11-17:

- Binary size today: 1,519,776 bytes.
- Binary size with a minimal cobra root command plus a `version`
  subcommand, built the same way: 4,001,952 bytes, an increase of 2,482,176
  bytes (about 2.6 times).
- `go list -m -versions github.com/spf13/cobra` lists v1.10.2 as the newest
  release. `go get github.com/spf13/cobra@v1.10.2` adds
  `github.com/spf13/pflag v1.0.9` and
  `github.com/inconshreveable/mousetrap v1.1.0` as indirect requirements.
  `go.sum` also gains `/go.mod`-only hash lines for `go-md2man`,
  `blackfriday`, `go.yaml.in/yaml/v3` and `gopkg.in/check.v1`; none of
  those is compiled in.
- With that minimal cobra build, every row of the CLI table below behaves as
  written, including the exact `unknown command "bogus" for
  "dnsmasq-manager"` text. `version extra` fails with
  `unknown command "extra" for "dnsmasq-manager version"`. Declaring the
  `--version` flag ourselves means cobra adds no `-v` shorthand: `-v` fails
  with `unknown shorthand flag: 'v' in -v`. `--version extra` prints the
  version and exits 0, because cobra handles the version flag before it
  checks arguments.
- golangci-lint v2.15.0 with the repo's `.golangci.yml` flags an unchecked
  `fmt.Fprintf(stderr, ...)` in `run` (errcheck), so the error print has to
  discard its results explicitly.

Checked in `docker run --rm alpine:3.23`, 2026-11-17:

- `/etc/os-release` has `ID=alpine` and `VERSION_ID=3.23.8`: the version has
  three parts, so the installer compares major and minor only.
- `/bin/sh`, `wget` and `sha256sum` are BusyBox v1.37.0 applets. `mktemp -d`,
  `sed`, `id`, `uname`, `cp`, `mv`, `chmod`, `grep` and `wc` are present.
  `/bin/dash` is not. `/usr/local/bin` exists.
- `httpd` is not in the stock image. `apk add busybox-extras` provides it
  (`busybox-extras` 1.37.0-r30) at `/usr/sbin/httpd`, and
  `httpd -p 127.0.0.1:8080 -h <dir>` serves a directory tree that BusyBox
  `wget` can fetch from. The `python3 -m http.server` fallback is not
  needed.
- Stock BusyBox `wget` fetches HTTPS URLs and follows redirects: the stock
  image has `ssl_client` and `ca-certificates-bundle`. A
  `releases/latest/download/<asset>` URL of a public GitHub project went
  through two `302` responses (to the versioned download URL, then to
  `release-assets.githubusercontent.com`) and saved the file.
- `wget -q -O <file>` exits 1 on a 404 or a refused connection and leaves no
  output file. `-T <seconds>` (read timeout) is supported.
- `sha256sum -c` prints `<file>: OK` and exits 0 on a match, prints
  `FAILED` and exits 1 on a mismatch.
- On the Mac, an `alpine:3.23` container reports `uname -m` as `aarch64`.
  With `--platform linux/amd64` it reports `x86_64` and runs the
  `linux/amd64` binary from `make build`. The end-to-end test therefore
  pins `--platform linux/amd64`.
- As root, `adduser -D tester` then `su -s /bin/sh tester -c 'id -u'` prints
  `1000`, so the non-root case can run in the same container.

Checked on macOS: `/bin/dash` and `/sbin/sha256sum` exist, and
`sha256sum <file>` writes `<hex>  <file>` with two spaces, the same format
as GNU coreutils and BusyBox.

Checked with `gh api repos/googleapis/release-please-action/tags`:
`googleapis/release-please-action` tag `v5` is
`v5.0.0`, commit `45996ed1f6d02564a971a2fa1b5860e934307cf7`.

### Not yet verified

- Read from release-please source only: with an empty manifest (`{}`) and
  `initial-version: 0.1.0`, the first release is `0.1.0`, tagged `v0.1.0`
  when `include-component-in-tag` is false. Settled by the first release.
- Read from release-please-action docs only: for a single package at the
  repo root, the action sets the top-level outputs `release_created` and
  `tag_name`.
- From GitHub docs, not re-checked: events caused by `GITHUB_TOKEN` (other
  than `workflow_dispatch` and `repository_dispatch`) start no workflow
  runs. This is why asset upload lives in the release workflow. The same
  rule probably means CI does not run on the release pull request that
  release-please opens. The release pull request only changes
  `CHANGELOG.md` and the manifest, so that is acceptable, but if branch
  protection later requires CI checks it will block the merge. The first
  release will show which.
- `shellcheck` is said to be preinstalled on `ubuntu-24.04` runners. Not
  checked from here. The CI step calls it directly, so if it is missing the
  job fails loudly rather than skipping the check.

## Approaches

Three ways to cut releases were considered.

**A. release-please (chosen).** A GitHub Action keeps a release pull request
open, built from Conventional Commits since the last release. Merging it
tags the version, writes `CHANGELOG.md` and creates the GitHub release. A
second job in the same workflow uploads the assets. Trade-offs: a
third-party action runs with a write token, so it is pinned by commit SHA;
the default pull request title uses `chore`, so the title pattern is set in
config; there is a short window in which the release exists without assets.

**B. goreleaser.** Builds, checksums and uploads in one tool, triggered by a
pushed tag. Trade-offs: a second build recipe beside the Makefile, so CI no
longer runs what developers run; versioning and changelog still need
something else (a person pushing tags, or release-please anyway); much of
it (archives, multiple platforms, package formats) is unused for one static
binary.

**C. A hand-rolled tag workflow.** A workflow on `push: tags: v*` runs
`make dist` and `gh release create`. Trade-offs: no changelog and no
version bump from commits; a person decides each version and pushes each
tag by hand, which is the step most likely to be done wrong.

The owner picked A: the repo already uses Conventional Commits, which is
what release-please reads, and the Makefile stays the only build recipe.

## Design

### Layout

```
internal/cli/root.go               root command
internal/cli/version.go            version subcommand
internal/cli/cli_test.go           table-driven CLI tests
cmd/dnsmasq-manager/main.go        run(args, stdout, stderr) int
cmd/dnsmasq-manager/main_test.go   one test through run
install.sh                         the installer, POSIX sh
test/install/install_test.sh       stub-command suite for install.sh
test/install/Dockerfile            alpine:3.23 image for the end-to-end run
test/install/e2e.sh                end-to-end cases, run in that image
release-please-config.json
.release-please-manifest.json
.github/workflows/release.yml
```

Modified: `go.mod`, `go.sum` (new), `Makefile`, `.github/workflows/ci.yml`,
`.gitignore`, `.dockerignore`, `README.md`, `CLAUDE.md`.

### Command line on cobra

**Why a third-party dependency.** The repo's rule is standard library first,
with a stated reason for any dependency. cobra is the first, for these
reasons:

- The owner asked for it.
- A `serve` / `setup` / `version` command tree is coming, with per-command
  flags. cobra gives that tree, generated help and usage text, unknown-
  command and unknown-flag errors, and flag parsing through `pflag`,
  without the project writing and testing its own dispatcher.
- It is the de facto Go CLI library, so a contributor is likely to know it.

**Cost.** `go.mod` requires `github.com/spf13/cobra v1.10.2`, with
`github.com/spf13/pflag v1.0.9` and `github.com/inconshreveable/mousetrap
v1.1.0` as indirect requirements. mousetrap is Windows-only and not
compiled into the Linux binary. The binary grows from 1,519,776 to
4,001,952 bytes. For a binary installed once on one host, that is
acceptable.

**Behaviour.** Tests are table-driven in `internal/cli`, plus one in
`cmd/dnsmasq-manager` that goes through `run`:

| Args | Exit | Output |
| --- | --- | --- |
| `version` | 0 | stdout `dnsmasq-manager dev\n` |
| `--version` | 0 | stdout `dnsmasq-manager dev\n` |
| (none) | 0 | help on stdout, contains `Usage:` and `version` |
| `bogus` | 1 | stderr `dnsmasq-manager: unknown command "bogus" for "dnsmasq-manager"`, stdout empty |
| `version extra` | 1 | error on stderr |

- `internal/cli/root.go`: `NewRootCommand(stdout, stderr io.Writer)
  *cobra.Command`. `Use: "dnsmasq-manager"`, `Version: version.String()`,
  `SilenceUsage` and `SilenceErrors` set, `Args: cobra.NoArgs`, `RunE`
  returns `cmd.Help()`. The default completion command is disabled. The
  version template is `dnsmasq-manager {{.Version}}\n`. The `--version` flag
  is declared on the root command before cobra would add its own, so there
  is no `-v` shorthand to collide with a future flag. Output goes to the
  writers passed in.
- `internal/cli/version.go`: the `version` subcommand, `Args:
  cobra.NoArgs`, `RunE` printing `dnsmasq-manager <version>` through
  `cmd.OutOrStdout()`.
- `cmd/dnsmasq-manager/main.go`: `main` calls
  `os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))`. `run` builds the root
  command, sets its arguments, executes it, and on error prints
  `dnsmasq-manager: <err>` to stderr and returns 1. Because errors are
  silenced in cobra, `run` is the only place an error is printed.
- Every command uses `RunE`, never `Run`, so errors reach `run`.
- `internal/version` is reused unchanged: `version.String()`, and the
  ldflags path `github.com/gsdevme/dnsmasq-manager/internal/version.Version`.

**CI caching.** The three jobs in `.github/workflows/ci.yml` that use
`actions/setup-go` set `cache: false` with a comment saying the module has
no `go.sum`. Once `go.sum` exists, both the setting and the comment go, so
the default module cache applies. This is its own commit.

### `make dist`

`make dist` builds through the existing `build` recipe, then writes `dist/`:

- `dnsmasq-manager-linux-amd64`, a copy of `bin/dnsmasq-manager`;
- `dnsmasq-manager-linux-amd64.sha256`, one line:
  `<hex>  dnsmasq-manager-linux-amd64` (two spaces, the file name with no
  directory), written by running `sha256sum` inside `dist/`;
- `install.sh`, a copy of the repo's installer.

`dist/` is emptied first, so it holds only these three files. `VERSION` is
passed through to `build`, so `make dist VERSION=v0.1.0` stamps the binary.
`dist/` goes in `.gitignore` and in `.dockerignore`, so the integration
image never copies it.

### Installer

`install.sh` at the repo root, POSIX sh, clean under `shellcheck
--shell=sh`, and run in tests under both dash and BusyBox ash.

**Structure.** The whole script body is a `main` function, and the last line
is `main "$@"`. If a piped download is cut short, the shell reaches end of
input inside the function definition and runs nothing. `set -eu` applies
throughout.

**Checks before any download.** The script refuses, with a message on stderr
and exit 1, unless:

- `id -u` is `0`;
- the os-release file has `ID=alpine` and a `VERSION_ID` whose major and
  minor are at least 3.23 (`3.23.8` and `3.24.0` pass, `3.22.4` fails). The
  values are read with `sed`, never by sourcing the file;
- `uname -m` is `x86_64`.

**Settings, all from the environment:**

| Variable | Default | Meaning |
| --- | --- | --- |
| `DNSMASQ_MANAGER_VERSION` | `latest` | `latest`, or a version; `0.1.0` is normalised to `v0.1.0`. Anything other than `latest` or `v` followed by digits, letters, dots and hyphens is refused. |
| `DNSMASQ_MANAGER_BASE_URL` | `https://github.com/gsdevme/dnsmasq-manager/releases` | Release base URL; tests point it at a local server or stub. |
| `DNSMASQ_MANAGER_INSTALL_DIR` | `/usr/local/bin` | Where the binary goes. |
| `DNSMASQ_MANAGER_OS_RELEASE` | `/etc/os-release` | Test seam for the OS check. |

Download URLs are `$BASE_URL/latest/download/<asset>` for `latest` and
`$BASE_URL/download/<tag>/<asset>` otherwise, the paths GitHub serves.

**Download and verify.** A `mktemp -d` work directory, removed by an `EXIT`
trap. BusyBox `wget -q -T 30 -O` fetches the binary and the `.sha256` file
into it. The checksum file must be exactly one line matching
`^[0-9a-f]{64}  dnsmasq-manager-linux-amd64$`; anything else is refused
before `sha256sum` runs, so a stray HTML error page or a second file name
cannot pass. Then `sha256sum -c` runs inside the work directory.

**Install.** The previous version is read from the existing binary's
`version` output when one is installed (`none` otherwise, `unknown` if it
fails to run). The new binary is copied to a staging file inside the
install directory (same file system, so the rename is atomic), set to mode
`0755`, then moved over the target with `mv -f`. The trap also removes the
staging file. The script prints the previous and the new version, the
latter from the installed binary's `version` output.

**Scope.** The binary only: no service, user, config or dnsmasq change.

**Tests, written before the script:**

- `test/install/install_test.sh` and `make test-install`: a stub-command
  harness on the pattern of `test/hook/hook_test.sh`. Each case runs the
  script under `$TEST_SH` (default `/bin/dash`) in `env -i` with a PATH
  holding symlinks to basic commands plus stubs for `id`, `uname`, `wget`
  and `sha256sum`. The `wget` stub logs each URL and serves files from a
  per-case fixture tree laid out like the release URLs; the downloaded
  "binary" is a small shell script that prints a version. Cases: latest;
  pinned `v0.1.0`; version without `v`; checksum mismatch (nothing
  installed); malformed checksum file; non-root (`wget` never called);
  `aarch64`; Debian; Alpine 3.22; download failure (existing binary
  untouched); upgrade (previous and new version printed); re-run; mode
  `0755` and no staging file left.
- `test/install/Dockerfile`, `test/install/e2e.sh` and
  `make test-install-docker`: an `alpine:3.23` image, `--platform
  linux/amd64`, with `busybox-extras` for `httpd` and `bash` for the stub
  harness. The target builds `dist/` first, and the container gets it as a
  read-only bind mount, because `dist/` is excluded from the Docker build
  context. `e2e.sh` lays the files out under `releases/latest/download/` and
  `releases/download/<tag>/`, starts `httpd` on `127.0.0.1`, and runs the
  real script against the real binary: tampered checksum (nothing
  installed), happy path, re-run, pinned version given without the `v`,
  non-root. It then runs the stub suite with `TEST_SH=/bin/sh`, BusyBox
  ash.
- CI: a new `install-alpine` job runs `make test-install-docker`;
  `make test-install` joins the `lint-and-unit` job beside `make test-hook`,
  with `shellcheck --shell=sh install.sh`.

### Release workflow

`release-please-config.json`:

```json
{
  "$schema": "https://raw.githubusercontent.com/googleapis/release-please/main/schemas/config.json",
  "packages": {
    ".": {
      "release-type": "go",
      "initial-version": "0.1.0",
      "include-component-in-tag": false,
      "bump-minor-pre-major": true,
      "pull-request-title-pattern": "build: release ${version}"
    }
  }
}
```

`.release-please-manifest.json` is `{}`.

`bump-minor-pre-major` keeps a breaking change before 1.0 to a minor bump.
The title pattern replaces the default `chore(...)` title; once the release
pull request is squash-merged, its title becomes a `build:` commit on
`main`, which release-please does not count as a change to release.

`.github/workflows/release.yml`, on push to `main`, with top-level
`permissions: {}` and a `release` concurrency group that never cancels:

- Job `release-please`: `contents: write`, `pull-requests: write`,
  `issues: write` (release-please manages labels through the issues API).
  Uses `googleapis/release-please-action@45996ed1f6d02564a971a2fa1b5860e934307cf7`
  with a `# v5.0.0` comment, pinned by SHA because it is a third-party
  action holding a write token. Exposes `release_created` and `tag_name` as
  job outputs.
- Job `assets`: `needs: release-please`, runs only when `release_created`
  is `true`, `contents: write`. Checks out the tag, sets up Go from
  `go.mod`, runs `make dist VERSION="$TAG"`, then
  `gh release upload "$TAG" dist/* --clobber`. The tag reaches the shell
  through `env:`, not by expanding an expression inside `run:`.

Asset upload lives in the same workflow because a release created with
`GITHUB_TOKEN` does not start other workflows.

**Accepted gap.** Between release creation and the end of the `assets` job,
`releases/latest` points at a release with no assets, so an install in that
window fails at download (and changes nothing). The fix, creating the
release as a draft and publishing it after upload, is a deferred issue.

### Documentation

- README: an Installation section (requirements: Alpine 3.23 or later,
  x86_64, root; the one-liner
  `wget -qO- https://github.com/gsdevme/dnsmasq-manager/releases/latest/download/install.sh | sh`;
  the safer form that downloads, reads, then runs the script; pinning with
  `DNSMASQ_MANAGER_VERSION`; what the script does and does not do), an
  updated Status, a short Releasing section, and `make dist`,
  `make test-install` and `make test-install-docker` in the targets table.
  The Status line about having no third-party dependencies changes.
- `CLAUDE.md`: the same three targets in the Commands list.

### Owner steps after merge

Not code; recorded in the README's Releasing section and the pull request:

1. In repository settings, enable "Allow GitHub Actions to create and
   approve pull requests".
2. Make the repository public.
3. On the first release pull request: approve workflow runs if GitHub asks;
   confirm the title is `build: release 0.1.0`; squash-merge it.
4. Confirm the `v0.1.0` release has three assets.
5. Smoke-test in a throwaway container, never the live DNS host:
   `docker run --rm --platform linux/amd64 alpine:3.23 sh -c 'wget -qO- https://github.com/gsdevme/dnsmasq-manager/releases/latest/download/install.sh | sh && dnsmasq-manager version'`.

### Deferred to GitHub issues

One epic with a dedicated label, and one issue per phase: the `setup`
command (import existing dnsmasq rules into the service's config); the
`serve` command; the installer's OpenRC service, service user and config
file; `arm64` release builds; pinning every action by commit SHA with
Dependabot to keep the pins current; and draft-then-publish releases to
close the assets gap. The first three depend on the service design, which
comes first.

## Left to the builder

- The exact staging file name and the wording of the installer's messages.
- How the stub harness lays out fixtures and asserts on its call log.
- README and `CLAUDE.md` wording.
