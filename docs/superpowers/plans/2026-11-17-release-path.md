# Release Path Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Publish a versioned static `linux/amd64` binary from every release of `dnsmasq-manager`, and ship a POSIX `install.sh` that installs it on Alpine 3.23 after checking its SHA-256 checksum.

**Architecture:** The CLI moves onto cobra so later commands share one tree. `make dist` stays the only build recipe for release files. release-please keeps a release pull request from Conventional Commits; merging it creates the release, and a second job in the same workflow uploads `make dist` output. The installer is a single POSIX sh script tested by a stub-command suite and by an end-to-end run in `alpine:3.23` against a local HTTP server.

**Tech Stack:** Go 1.27, `github.com/spf13/cobra` v1.10.2, GNU Make, POSIX sh (dash, BusyBox ash), bash for the test harness, Docker, BusyBox `httpd` from `busybox-extras`, shellcheck, GitHub Actions, `googleapis/release-please-action` v5.0.0.

**Spec:** `docs/superpowers/specs/2026-11-17-release-path-design.md`.

**Context:** Branch `feat/release-path`, cut from `main`. The binary today only prints its version (`cmd/dnsmasq-manager/main.go`, `internal/version`). `test/hook/hook_test.sh` is the pattern for the installer's stub suite. The service (`setup`, `serve`, OpenRC, user, config) is out of scope and tracked as GitHub issues.

**Execution:** Tasks run in order; each is one commit, and Tasks 5 to 10 build on the Makefile and files of earlier tasks.

## Global Constraints

- Minimums: Alpine 3.23 and dnsmasq 2.91. No code or probes for older ones. Architecture `x86_64` only.
- One new dependency: `github.com/spf13/cobra v1.10.2` (with `github.com/spf13/pflag v1.0.9` and `github.com/inconshreveable/mousetrap v1.1.0` indirect). No other third-party Go module.
- Every cobra command uses `RunE`, never `Run`.
- Version output is exactly `dnsmasq-manager <version>\n`; an unstamped build reports `dev`. The ldflags path `github.com/gsdevme/dnsmasq-manager/internal/version.Version` does not change.
- Release asset names: `dnsmasq-manager-linux-amd64`, `dnsmasq-manager-linux-amd64.sha256` (one line, `<64 lowercase hex>  dnsmasq-manager-linux-amd64`), `install.sh`.
- Default release base URL: `https://github.com/gsdevme/dnsmasq-manager/releases`.
- `install.sh` is POSIX sh, passes `shellcheck --shell=sh`, and changes nothing but the one binary.
- No private value in any committed file: no real zone, LAN address, hostname, local machine path or token. Use `<private-zone>`, `home.example.test`, addresses in `10.0.0.0/24`, and `example.test` hosts in test URLs.
- Commits: Conventional Commits, never `chore`, no `!`, subject at most 72 characters, one commit per task, ending with the session's `Co-Authored-By` trailer. Squash fixups into the task's commit.
- Go comments: doc comments on packages, types and functions; no narrating inline comments. Shell and YAML may be commented freely.
- TDD: each task's failing test is written and run before the code that makes it pass.
- The live DNS host is never used to test anything in this plan.

## Review Focus

Each line has a test in the task named.

1. A piped download cut off mid-script must run nothing: no `wget`, no install. (Task 6, case "truncated script")
2. A `VERSION_ID` written with quotes (`VERSION_ID="3.23.0"`, allowed by the os-release format) or as `3.24.0` must pass; `3.22.4` and `3.3` must fail. (Task 6, cases "quoted VERSION_ID", "Alpine 3.24", "Alpine 3.22", "Alpine 3.3")
3. A version with characters that could change the URL (`v0.1.0/../x`, a space) must be refused before any download. (Task 6, case "unsafe version")
4. A missing install directory must fail with a clear message before any download, not after. (Task 6, case "missing install dir")
5. Every exit path, success or failure, removes the `mktemp -d` work directory and the staging file. (Task 6, the `expect_clean` assertion on every case)

---

### Task 1: Commit the design

**Files:**
- Create: `docs/superpowers/specs/2026-11-17-release-path-design.md`

- [ ] **Step 1:** Confirm the status line reads `Status: approved 2026-11-17`.
- [ ] **Step 2:** Run `grep -nE '/Us[e]rs/|192[.]168[.]|172[.](1[6-9]|2[0-9]|3[01])[.]' docs/superpowers/specs/2026-11-17-release-path-design.md`. Expected: no output.
- [ ] **Step 3:** Commit: `docs: add release path design`.

### Task 2: Commit this plan

**Files:**
- Create: `docs/superpowers/plans/2026-11-17-release-path.md`

- [ ] **Step 1:** Run the Task 1 grep on this file. Expected: no output.
- [ ] **Step 2:** Commit: `docs: add release path plan`.

### Task 3: CLI on cobra

**Files:**
- Create: `internal/cli/root.go`, `internal/cli/version.go`, `internal/cli/cli_test.go`, `go.sum`
- Modify: `cmd/dnsmasq-manager/main.go`, `cmd/dnsmasq-manager/main_test.go`, `go.mod`

**Interfaces:**
- Consumes: `version.String() string` from `internal/version`.
- Produces: `cli.NewRootCommand(stdout, stderr io.Writer) *cobra.Command`; unexported `newVersionCommand() *cobra.Command` in `internal/cli/version.go`; `run(args []string, stdout, stderr io.Writer) int` in package `main`. The `version` subcommand and `--version` flag that Tasks 6 and 7 call on the installed binary.

- [ ] **Step 1: Write the failing CLI tests** in `internal/cli/cli_test.go` (package `cli_test`). A helper `execute(t, args ...string) (stdout, stderr string, err error)` builds `cli.NewRootCommand(&out, &errOut)`, calls `SetArgs(args)` and `Execute()`. `TestRootCommand` is table-driven:

```go
{name: "version subcommand", args: []string{"version"}, wantOut: "dnsmasq-manager dev\n"},
{name: "version flag", args: []string{"--version"}, wantOut: "dnsmasq-manager dev\n"},
{name: "no args prints help", args: nil, outContains: []string{"Usage:", "version"}},
{name: "unknown command", args: []string{"bogus"}, wantErr: `unknown command "bogus" for "dnsmasq-manager"`},
{name: "version takes no args", args: []string{"version", "extra"}, wantErr: `unknown command "extra" for "dnsmasq-manager version"`},
{name: "no -v shorthand", args: []string{"-v"}, wantErr: "unknown shorthand flag: 'v' in -v"},
{name: "no completion command", args: []string{"completion", "bash"}, wantErr: `unknown command "completion" for "dnsmasq-manager"`},
```

  Rows with `wantErr` assert `err.Error() == wantErr` and that stdout is empty; rows without assert `err == nil`, exact `wantOut` when set, and each `outContains` substring. Every row asserts stderr is empty (errors are returned, not printed).

- [ ] **Step 2:** Replace `cmd/dnsmasq-manager/main_test.go` with one table-driven `TestRun`:

```go
{args: []string{"version"}, code: 0, stdout: "dnsmasq-manager dev\n", stderr: ""},
{args: []string{"bogus"}, code: 1, stdout: "", stderr: "dnsmasq-manager: unknown command \"bogus\" for \"dnsmasq-manager\"\n"},
```

- [ ] **Step 3:** Run `go test ./...`. Expected: FAIL, package `internal/cli` has no non-test files and `run` has the wrong signature.
- [ ] **Step 4:** Implement `internal/cli/root.go` (package doc comment saying the package builds the command tree) with the values from the spec: `Use: "dnsmasq-manager"`, `Version: version.String()`, `SilenceUsage: true`, `SilenceErrors: true`, `Args: cobra.NoArgs`, `RunE` returning `cmd.Help()`, `CompletionOptions.DisableDefaultCmd = true`, `SetVersionTemplate("dnsmasq-manager {{.Version}}\n")`, `Flags().Bool("version", false, ...)` declared before `Execute` so cobra adds no `-v`, `SetOut(stdout)`, `SetErr(stderr)`, `AddCommand(newVersionCommand())`.
- [ ] **Step 5:** Implement `newVersionCommand` in `internal/cli/version.go`: `Use: "version"`, `Args: cobra.NoArgs`, `RunE` writing `dnsmasq-manager <version.String()>\n` to `cmd.OutOrStdout()` and returning the write error.
- [ ] **Step 6:** Rewrite `cmd/dnsmasq-manager/main.go`: `main` calls `os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))`; `run` builds the root command, `SetArgs(args)`, and on an `Execute` error writes `dnsmasq-manager: <err>\n` to stderr with `_, _ = fmt.Fprintf(...)` (errcheck flags a bare call) and returns 1. Update the package doc comment.
- [ ] **Step 7:** Run `go get github.com/spf13/cobra@v1.10.2 && go mod tidy`. Expected: `go.mod` requires cobra v1.10.2 with pflag v1.0.9 and mousetrap v1.1.0 indirect; `go.sum` created.
- [ ] **Step 8:** Run `go test -race ./...`. Expected: PASS.
- [ ] **Step 9:** Run `make lint && make build && stat -f %z bin/dnsmasq-manager 2>/dev/null || stat -c %s bin/dnsmasq-manager`. Expected: 0 lint issues; size about 4,000,000 bytes (spec measured 4,001,952 with `VERSION=dev`).
- [ ] **Step 10:** Run `docker run --rm --platform linux/amd64 -v "$PWD/bin:/b:ro" alpine:3.23 /b/dnsmasq-manager version`. Expected: `dnsmasq-manager <git describe output>`.
- [ ] **Step 11:** Commit: `feat(cli): move the command line onto cobra`. The body names cobra as the first third-party dependency and points to the spec for the reason.

### Task 4: Go module caching in CI

**Files:**
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1:** In the `lint-and-unit`, `integration-ubuntu` and `build` jobs, remove `cache: false` and the three-line comment above it under `actions/setup-go@v7`.
- [ ] **Step 2:** Run `grep -n 'cache' .github/workflows/ci.yml`. Expected: no output.
- [ ] **Step 3:** Run `go run github.com/rhysd/actionlint/cmd/actionlint@latest .github/workflows/ci.yml`. Expected: no output.
- [ ] **Step 4:** Commit: `ci: enable Go module caching now go.sum exists`.

### Task 5: `make dist`

**Files:**
- Modify: `Makefile`, `.gitignore`, `.dockerignore`

**Interfaces:**
- Produces: Make target `dist`, writing `dist/dnsmasq-manager-linux-amd64` and `dist/dnsmasq-manager-linux-amd64.sha256`; `VERSION` passes through to `build`. Task 6 adds `dist/install.sh`.

- [ ] **Step 1: Write the failing check.** Run `make dist VERSION=v0.0.0-test`. Expected: FAIL, `No rule to make target 'dist'`.
- [ ] **Step 2:** Add `dist` to `.PHONY` and a `dist: build` target with a comment naming it as the release asset set. Recipe: `rm -rf dist`, `mkdir -p dist`, `cp bin/dnsmasq-manager dist/dnsmasq-manager-linux-amd64`, then `cd dist && sha256sum dnsmasq-manager-linux-amd64 > dnsmasq-manager-linux-amd64.sha256` so the line carries no directory.
- [ ] **Step 3:** Add `dist/` to `.gitignore` and `dist` to `.dockerignore`.
- [ ] **Step 4:** Run `make dist VERSION=v0.0.0-test && ls dist && grep -cE '^[0-9a-f]{64}  dnsmasq-manager-linux-amd64$' dist/dnsmasq-manager-linux-amd64.sha256 && wc -l < dist/dnsmasq-manager-linux-amd64.sha256 && (cd dist && sha256sum -c dnsmasq-manager-linux-amd64.sha256)`. Expected: two files listed, `1`, `1`, `dnsmasq-manager-linux-amd64: OK`.
- [ ] **Step 5:** Run `git status --short`. Expected: `dist/` not listed.
- [ ] **Step 6:** Commit: `build: add dist target with SHA-256 checksum`.

### Task 6: Installer and its stub suite

**Files:**
- Create: `test/install/install_test.sh`, `install.sh`
- Modify: `Makefile` (target `test-install`; `dist` also copies `install.sh`)

**Interfaces:**
- Consumes: asset names and URL layout from Global Constraints.
- Produces: `install.sh` reading `DNSMASQ_MANAGER_VERSION`, `DNSMASQ_MANAGER_BASE_URL`, `DNSMASQ_MANAGER_INSTALL_DIR`, `DNSMASQ_MANAGER_OS_RELEASE`; exit 0 on install, 1 on any refusal or failure; Make target `test-install`; `TEST_SH` (default `/bin/dash`) as the shell that runs `install.sh`, used again by Task 7.

- [ ] **Step 1: Write the harness** in `test/install/install_test.sh`, bash, with a file-level comment like `test/hook/hook_test.sh`'s. Reuse its `new_case`, `stub`, `problem`, `finish_case` shape. Decisions the pattern does not settle:
  - `new_case` makes `bin/`, `server/`, `install/`, `work/` (passed as `TMPDIR`) and an os-release file holding `ID=alpine` and `VERSION_ID=3.23.8`. It symlinks `cat chmod cp env grep head mkdir mktemp mv rm sed wc` into `bin/`, and stubs `id` (`echo 0`), `uname` (`echo x86_64`), `wget` and `sha256sum`.
  - `wget` stub: logs `wget <args>`, takes the `-O` file and the last argument as URL, strips `https://example.test/releases/` and copies `server/<rest>` to the `-O` file; exits 1 without creating the file when that path does not exist.
  - `sha256sum` stub: for `-c <file>`, exits 0 printing `<name>: OK` when the hex in `<file>` is 64 `a` characters, else prints `<name>: FAILED` and exits 1.
  - `release <tag>` writes `server/download/<tag>/dnsmasq-manager-linux-amd64` (a `#!/bin/sh` script printing `dnsmasq-manager <tag>`) and its `.sha256` with 64 `a` characters; `latest <tag>` also copies both into `server/latest/download/`.
  - `run_install [VAR=value ...]` runs `env -i PATH="$bin" HOME=... TMPDIR="$tmp/work" DNSMASQ_MANAGER_BASE_URL=https://example.test/releases DNSMASQ_MANAGER_INSTALL_DIR="$tmp/install" DNSMASQ_MANAGER_OS_RELEASE="$tmp/os-release" "$@" "$TEST_SH" "$script"`, recording status, stdout and stderr.
  - Assertions: `expect_exit`, `expect_stdout_has`, `expect_stderr_has`, `expect_log_lacks wget`, `expect_installed <tag>` (target runs and prints `dnsmasq-manager <tag>`), `expect_not_installed`, `expect_mode_0755`, and `expect_clean` (`work/` empty and no file other than `dnsmasq-manager` in `install/`), called in every case.

- [ ] **Step 2: Write the cases**, each with its expected result:

| Case | Setup | Expect |
| --- | --- | --- |
| latest | `latest v0.2.0` | exit 0, installed v0.2.0, `latest/download/` URLs in log |
| pinned | `release v0.1.0`, `latest v0.2.0`, `VERSION=v0.1.0` | installed v0.1.0, `download/v0.1.0/` URLs |
| version without v | as pinned, `VERSION=0.1.0` | installed v0.1.0 |
| checksum mismatch | `.sha256` hex 64 `b` | exit 1, not installed |
| malformed checksum | `.sha256` with two lines, then with the name `other` | exit 1, not installed, `sha256sum` never called |
| non-root | `id` prints `1000` | exit 1, stderr names root, `wget` never called |
| aarch64 | `uname` prints `aarch64` | exit 1, `wget` never called |
| Debian | `ID=debian`, `VERSION_ID=13` | exit 1, `wget` never called |
| Alpine 3.22 | `VERSION_ID=3.22.4` | exit 1, `wget` never called |
| Alpine 3.3 | `VERSION_ID=3.3.0` | exit 1 (numeric, not string, compare) |
| Alpine 3.24 | `VERSION_ID=3.24.0` | exit 0 |
| quoted VERSION_ID | `VERSION_ID="3.23.0"` | exit 0 |
| download failure | existing v0.1.0 installed, no release on server | exit 1, v0.1.0 still installed |
| upgrade | existing v0.1.0, `latest v0.2.0` | exit 0, stdout has `v0.1.0` and `v0.2.0` |
| re-run | install `latest v0.2.0` twice | both exit 0, v0.2.0 installed |
| mode and staging | after latest | mode 0755, `expect_clean` |
| unsafe version | `VERSION=v0.1.0/../x`, then `VERSION='v0.1 0'` | exit 1, `wget` never called |
| missing install dir | `INSTALL_DIR=$tmp/nope` | exit 1, stderr names the dir, `wget` never called |
| truncated script | run the first half of `install.sh` (`head -n <half>`) | `wget` never called, not installed |

  The harness exits non-zero if any case fails, prints `all cases passed` otherwise.

- [ ] **Step 3:** Add a `test-install` Make target (`bash test/install/install_test.sh`) with a comment saying it needs bash and the shell in `TEST_SH`. Run `make test-install`. Expected: FAIL, every case, `install.sh` not found.
- [ ] **Step 4:** Write `install.sh`. A header comment block: what it does, what it does not change, the four variables, and the one-liner. Body entirely inside `main() { ... }`, with `main "$@"` as the last line and `set -eu` first in the body. Decisions the tests do not fix:
  - The version check takes `VERSION_ID` with `sed -n 's/^VERSION_ID=//p'`, strips one pair of surrounding double quotes, splits major and minor on `.`, and compares them as integers.
  - Version normalisation: `latest` stays; a value starting with a digit gets `v` prepended; the result must match `v[0-9][0-9A-Za-z.-]*` in full (check with `case` or `grep -qE`).
  - Install-dir check: refuse unless it is an existing writable directory.
  - Downloads: `wget -q -T 30 -O "$work/<asset>" "<url>"` for the binary, then the `.sha256` file.
  - Checksum file: `wc -l` is 1 and `grep -qE '^[0-9a-f]{64}  dnsmasq-manager-linux-amd64$'`; then `(cd "$work" && sha256sum -c dnsmasq-manager-linux-amd64.sha256)`.
  - Staging file `"$install_dir/.dnsmasq-manager.new.$$"`; one `trap ... EXIT` removes the work dir and the staging file.
  - Messages prefixed `dnsmasq-manager install:`; errors to stderr.
- [ ] **Step 5:** Run `make test-install`. Expected: `all cases passed`. Then `TEST_SH=/bin/sh make test-install` on Linux, or `TEST_SH=/bin/bash make test-install` on macOS. Expected: `all cases passed`.
- [ ] **Step 6:** Run `shellcheck --shell=sh install.sh`. Expected: no output. If shellcheck is not installed locally, run it with `docker run --rm -v "$PWD:/mnt" koalaman/shellcheck:stable --shell=sh /mnt/install.sh`.
- [ ] **Step 7:** Add `cp install.sh dist/install.sh` to the `dist` recipe. Run `make dist VERSION=v0.0.0-test && ls dist`. Expected: three files.
- [ ] **Step 8:** Commit: `feat(install): add installer for Alpine with stub-command tests`.

### Task 7: End-to-end installer test in Alpine

**Files:**
- Create: `test/install/Dockerfile`, `test/install/e2e.sh`
- Modify: `Makefile` (target `test-install-docker`)

**Interfaces:**
- Consumes: `make dist` (Task 5, 6), `install.sh` and `test/install/install_test.sh` (Task 6), `dnsmasq-manager version` (Task 3).
- Produces: Make target `test-install-docker`, used by Task 8's CI job.

- [ ] **Step 1: Write the failing test.** Write `test/install/e2e.sh`, POSIX sh with a header comment, `set -eu`, a `fail` helper that prints and exits 1. It expects `/dist` (read-only mount of `make dist` output built with `VERSION=v0.0.0-e2e`) and `/src` (the image's copy of `install.sh` and `test/install/`). Steps, each asserting its result:
  1. Lay out `/srv/www/releases/latest/download/` and `/srv/www/releases/download/v0.0.0-e2e/` with copies of the three dist files, plus `/srv/www/tampered/latest/download/` whose `.sha256` has its first hex digit changed. Start `httpd -p 127.0.0.1:8080 -h /srv/www`.
  2. Tampered checksum: `DNSMASQ_MANAGER_BASE_URL=http://127.0.0.1:8080/tampered sh /src/install.sh` exits non-zero and `/usr/local/bin/dnsmasq-manager` does not exist.
  3. Happy path: with `DNSMASQ_MANAGER_BASE_URL=http://127.0.0.1:8080/releases`, the script exits 0 and `dnsmasq-manager version` prints `dnsmasq-manager v0.0.0-e2e`; mode is `-rwxr-xr-x`.
  4. Re-run: the same command exits 0 again.
  5. Pinned: `DNSMASQ_MANAGER_VERSION=0.0.0-e2e` exits 0.
  6. Non-root: `adduser -D tester`; `su -s /bin/sh tester -c '... sh /src/install.sh'` exits non-zero.
  7. Stub suite under BusyBox ash: `TEST_SH=/bin/sh bash /src/test/install/install_test.sh` passes.
  8. Print `e2e: all steps passed`.
- [ ] **Step 2:** Write `test/install/Dockerfile` with a header comment: `FROM alpine:3.23`, `RUN apk add --no-cache busybox-extras bash`, `COPY install.sh /src/install.sh`, `COPY test/install/ /src/test/install/`, `CMD ["sh", "/src/test/install/e2e.sh"]`.
- [ ] **Step 3:** Run the image without `/dist`: `docker build --platform linux/amd64 -f test/install/Dockerfile -t dnsmasq-manager-install-e2e . && docker run --rm --platform linux/amd64 dnsmasq-manager-install-e2e`. Expected: FAIL at step 1, `/dist` missing.
- [ ] **Step 4:** Add the `test-install-docker` Make target, with a comment that `dist/` is excluded from the build context and so is bind-mounted, and that `--platform linux/amd64` makes `uname -m` report `x86_64` on arm64 hosts: `$(MAKE) dist VERSION=v0.0.0-e2e`, the `docker build` above, then `docker run --rm --platform linux/amd64 -v "$(CURDIR)/dist:/dist:ro" dnsmasq-manager-install-e2e`.
- [ ] **Step 5:** Run `make test-install-docker`. Expected: `e2e: all steps passed`, including the stub suite's `all cases passed` under BusyBox ash.
- [ ] **Step 6:** Commit: `test(install): run the installer end to end in Alpine 3.23`.

### Task 8: Installer checks in CI

**Files:**
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1:** In `lint-and-unit`, after `make test-hook`, add `make test-install` and a `shellcheck --shell=sh install.sh` step, each with a comment (the second says shellcheck is expected on the runner and the step fails if it is missing).
- [ ] **Step 2:** Add an `install-alpine` job on `ubuntu-24.04` with a comment block like `integration-alpine`'s: checkout, `actions/setup-go@v7` with `go-version-file: go.mod` (`make dist` builds on the runner), `make test-install-docker`.
- [ ] **Step 3:** Run `go run github.com/rhysd/actionlint/cmd/actionlint@latest .github/workflows/ci.yml`. Expected: no output.
- [ ] **Step 4:** Commit: `ci: test the installer and shellcheck it`.
- [ ] **Step 5:** When the branch is pushed, check the pull request's CI run. Expected: `lint-and-unit` and `install-alpine` pass. If shellcheck is missing on the runner, add `sudo apt-get install -y --no-install-recommends shellcheck` before the step, record it in the spec's verified facts, and squash both into this task's commit.

### Task 9: Release workflow

**Files:**
- Create: `release-please-config.json`, `.release-please-manifest.json`, `.github/workflows/release.yml`

**Interfaces:**
- Consumes: `make dist VERSION=<tag>` (Tasks 5, 6).
- Produces: on merge of a release pull request, tag `v<version>` and a GitHub release with the three assets.

- [ ] **Step 1:** Write `release-please-config.json` exactly as in the spec's Release workflow section, and `.release-please-manifest.json` as `{}`.
- [ ] **Step 2:** Run `jq . release-please-config.json .release-please-manifest.json`. Expected: both parse; `jq -r '.packages["."]["pull-request-title-pattern"]' release-please-config.json` prints `build: release ${version}`.
- [ ] **Step 3:** Write `.github/workflows/release.yml`, commented like `ci.yml`: `on: push: branches: [main]`; top-level `permissions: {}`; `concurrency: { group: release, cancel-in-progress: false }`.
  - Job `release-please` on `ubuntu-24.04`: permissions `contents: write`, `pull-requests: write`, `issues: write`; step `id: release` using `googleapis/release-please-action@45996ed1f6d02564a971a2fa1b5860e934307cf7 # v5.0.0` with `config-file: release-please-config.json` and `manifest-file: .release-please-manifest.json`; job outputs `release_created: ${{ steps.release.outputs.release_created }}` and `tag_name: ${{ steps.release.outputs.tag_name }}`.
  - Job `assets`: `needs: release-please`, `if: needs.release-please.outputs.release_created == 'true'`, permissions `contents: write`; `actions/checkout@v7` with `ref: ${{ needs.release-please.outputs.tag_name }}`; `actions/setup-go@v7` with `go-version-file: go.mod`; a step with `env: TAG: ${{ needs.release-please.outputs.tag_name }}` and `GH_TOKEN: ${{ github.token }}` running `make dist VERSION="$TAG"` then `gh release upload "$TAG" dist/* --clobber`.
  - A comment on `assets` explains why it is in this workflow (a release created by `GITHUB_TOKEN` starts no other workflow) and records the accepted gap before assets exist.
- [ ] **Step 4:** Run `go run github.com/rhysd/actionlint/cmd/actionlint@latest .github/workflows/release.yml`. Expected: no output.
- [ ] **Step 5:** Run `grep -n 'release-please-action@' .github/workflows/release.yml`. Expected: one line with the full 40-character SHA.
- [ ] **Step 6:** Commit: `ci: publish releases with release-please`.

### Task 10: Installation and release docs

**Files:**
- Modify: `README.md`, `CLAUDE.md`

- [ ] **Step 1:** In `README.md`, add an Installation section with the spec's content: requirements (Alpine 3.23 or later, x86_64, root); the one-liner `wget -qO- https://github.com/gsdevme/dnsmasq-manager/releases/latest/download/install.sh | sh`; the safer form (`wget -O install.sh ...`, read it, `sh install.sh`); pinning with `DNSMASQ_MANAGER_VERSION=0.1.0`; what the script does (checks, download, checksum, atomic install to `/usr/local/bin`) and does not do (no service, user, config or dnsmasq change).
- [ ] **Step 2:** Update Status: the CLI is on cobra (its one third-party dependency), releases are published by release-please, the binary still only reports its version. Add a short Releasing section: Conventional Commits drive the release pull request, merging it publishes the release, and the owner steps from the spec (Actions permission, first release check, smoke test in a throwaway `alpine:3.23` container, never the live DNS host). Add `make dist`, `make test-install` and `make test-install-docker` to the targets table.
- [ ] **Step 3:** In `CLAUDE.md` Commands, add `make dist`, `make test-install` (needs bash and dash) and `make test-install-docker` (Docker; the end-to-end installer run in `alpine:3.23`).
- [ ] **Step 4:** Run `grep -nE '/Us[e]rs/|192[.]168[.]|172[.](1[6-9]|2[0-9]|3[01])[.]' README.md CLAUDE.md`. Expected: no output.
- [ ] **Step 5:** Run `make lint test test-hook test-install test-install-docker build`. Expected: all pass.
- [ ] **Step 6:** Commit: `docs: document installation and releases`.

## After the branch lands

The deferred-work epic and its issues are created when the pull request is
opened. The rest are owner steps from the spec, not tasks for the implementer:

1. Enable "Allow GitHub Actions to create and approve pull requests".
2. Make the repository public.
3. On the first release pull request, approve workflow runs if asked, confirm the title `build: release 0.1.0`, squash-merge.
4. Confirm three assets on `v0.1.0`, then smoke-test in a throwaway `alpine:3.23` container with `--platform linux/amd64`.
5. Move the spec's "Not yet verified" items into verified facts once the first release settles them.
