# Agentic Repo Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Set up the empty `dnsmasq-manager` repo so Claude Code sessions, local or cloud, can build the Go service from committed specs with the superpowers workflow, and can verify dnsmasq behaviour locally.

**Architecture:** A dependency-free Go skeleton with a Makefile as the single entry point for build, test, lint and integration. Integration tests drive a real dnsmasq against a mock config, natively on Linux or in an Alpine container pinned to the production dnsmasq version. A committed session-start hook prepares cloud sessions (superpowers plugin plus tools) and does nothing locally.

**Tech Stack:** Go 1.27 standard library only, GNU Make, bash, Docker, golangci-lint v2, GitHub Actions, dnsmasq 2.85, dig.

**Spec:** `docs/superpowers/specs/2026-11-17-agentic-repo-foundation-design.md` (approved by Gavin 2026-11-17).

**Context:** The repo at `/Users/gavin/Sites/dnsmasq-manager` has no commits and no remote. This plan is the first of two projects. The service itself (REST API, external-dns webhook, read-only web page on port 80, replacing `lan-dns`) gets its own design and plan after this lands.

**Execution:** Subagent-driven, per Gavin's delegation rules: one opus worker per task, sonnet workers for plain test and lint runs, the session model only orchestrates and reviews. Tasks 1 to 5 run in order because each builds on the Makefile from Task 1.

## Global Constraints

- Go version `1.27` in `go.mod`. Module path `github.com/gsdevme/dnsmasq-manager`.
- No third-party Go dependencies. `go.mod` has no `require` block.
- No private value in any committed file: no real DNS zone, no LAN IP address, no token. Use `<private-zone>` in prose and `home.example.test` in configs and tests. Record addresses are in `10.0.0.0/24`.
- Commits: Conventional Commits, never the `chore` type, subject at most 72 characters, one atomic commit per task. End every commit message with `Co-Authored-By: Claude Fable 5.2 <noreply@anthropic.com>`.
- Work lands directly on `main` for this plan only. Never force-push `main`; after Task 6 a fix is a new `fix:` commit.
- Comments: doc comments on packages, types and functions; no inline narration; YAML and shell config may be commented freely.
- The live DNS host is never used as a test bed and is not changed by this plan.
- The session-start hook always exits 0 and prints nothing when `CLAUDE_CODE_REMOTE` is not `true`.

## Review Focus

Each line has a test or check in the task named.

1. The hook runs where the `claude` command is not on the path: it still exits 0 and reports `superpowers: NOT installed`. (Task 3, case 5)
2. The hook runs as a non-root user without passwordless sudo: it skips `apt-get`, exits 0, and reports the tools as missing. (Task 3, case 6)
3. The hook runs on every resume: a second run with everything present installs nothing. (Task 3, case 3)
4. Integration tests run as root in the container and as a normal user in CI: dnsmasq must be able to read the temp hosts directory in both. (Task 2, harness rule on `--user=root`)
5. Someone runs `make integration` on macOS, where dnsmasq has no inotify: it fails at once with a message naming `make integration-docker`. (Task 2, Step 7)

---

### Task 0: Commit the design and this plan

**Files:**
- Create: `docs/superpowers/plans/2026-11-17-agentic-repo-foundation.md` (copy of this plan)
- Modify: `docs/superpowers/specs/2026-11-17-agentic-repo-foundation-design.md` (status line)

- [ ] **Step 1:** Copy this plan to `docs/superpowers/plans/2026-11-17-agentic-repo-foundation.md`.
- [ ] **Step 2:** In the design doc change `Status: draft, awaiting approval` to `Status: approved 2026-11-17`.
- [ ] **Step 3:** Run `grep -rnE 'gsdev\.me|172\.16\.' docs/`. Expected: no output.
- [ ] **Step 4:** Commit: `docs: add repo foundation design and plan`.

### Task 1: Go skeleton

**Files:**
- Create: `go.mod`, `cmd/dnsmasq-manager/main.go`, `cmd/dnsmasq-manager/main_test.go`, `internal/version/version.go`, `internal/version/version_test.go`, `Makefile`, `.golangci.yml`, `.gitignore`

**Interfaces:**
- Produces: `version.Version` (package var, default `"dev"`), `version.String() string`; `run(w io.Writer) int` in package `main`; Make targets `build`, `test`, `lint`, `tools`; Make variable `GOLANGCI_LINT_VERSION`.

- [ ] **Step 1:** Create `go.mod` with `module github.com/gsdevme/dnsmasq-manager` and `go 1.27`.
- [ ] **Step 2: Write the failing tests.**

```go
// internal/version/version_test.go
func TestStringDefaultsToDev(t *testing.T) {
	if got := version.String(); got != "dev" {
		t.Fatalf("String() = %q, want %q", got, "dev")
	}
}

// cmd/dnsmasq-manager/main_test.go
func TestRunPrintsVersion(t *testing.T) {
	var out bytes.Buffer
	if code := run(&out); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if got, want := out.String(), "dnsmasq-manager dev\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
```

- [ ] **Step 3:** Run `go test ./...`. Expected: FAIL, undefined `version.String` and `run`.
- [ ] **Step 4:** Implement `var Version = "dev"` and `func String() string` in `internal/version/version.go`; `func run(w io.Writer) int` and `main` (calls `os.Exit(run(os.Stdout))`) in `cmd/dnsmasq-manager/main.go`. Each package gets a package doc comment.
- [ ] **Step 5:** Run `go test -race ./...`. Expected: PASS.
- [ ] **Step 6:** Write the `Makefile`:
  - `VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)`
  - `GOLANGCI_LINT_VERSION := <latest v2 release that builds with Go 1.27>`
  - `build`: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X github.com/gsdevme/dnsmasq-manager/internal/version.Version=$(VERSION)" -o bin/dnsmasq-manager ./cmd/dnsmasq-manager`
  - `test`: `go test -race ./...`
  - `tools`: `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)`
  - `lint`: `golangci-lint run ./...` (resolve the binary from `$(shell go env GOPATH)/bin` when it is not on the path)
- [ ] **Step 7:** Write `.golangci.yml` (v2 format, `standard` defaults plus `errorlint`, `gocritic`, `misspell`, `revive`; formatter `gofmt`) and `.gitignore` (`bin/`, `*.test`, `coverage.*`, `.DS_Store`).
- [ ] **Step 8:** Run `make tools && make lint && make test && make build && file bin/dnsmasq-manager`. Expected: lint reports 0 issues, tests pass, `file` reports an x86-64 statically linked ELF.
- [ ] **Step 9:** Commit: `feat: add Go module skeleton with version command`.

### Task 2: Mock config and dnsmasq behaviour probes

**Files:**
- Create: `testdata/dnsmasq/dnsmasq.conf`, `test/integration/harness_test.go`, `test/integration/probes_test.go`, `test/integration/Dockerfile`, `docs/dnsmasq-findings.md`
- Modify: `Makefile` (targets `integration`, `integration-docker`)

**Interfaces:**
- Consumes: Makefile from Task 1.
- Produces, in package `integration` (build tag `integration`), for the service's later tests:
  - `func startDnsmasq(t *testing.T) *server`
  - `func (s *server) lookup(t *testing.T, name string) []string`
  - `func (s *server) writeHosts(t *testing.T, content string)`
  - `func (s *server) reload(t *testing.T)`
  - `func (s *server) eventually(t *testing.T, name string, want ...string)`
  - `func dnsmasqVersion(t *testing.T) string`

- [ ] **Step 1:** Create `testdata/dnsmasq/dnsmasq.conf` with exactly this content:

```
# Mock of the production dnsmasq.conf, for tests only.
# Same directives and shape as the live file; names and addresses are fake.
# Upstreams are TEST-NET addresses that never answer, so tests must only
# query names this file (or the hosts directory) answers itself.
cache-size=4096
bogus-priv
domain-needed
no-resolv
strict-order
server=192.0.2.1
server=192.0.2.2
address=/.zip/127.0.0.1
address=/.mov/127.0.0.1
address=/nas.home.example.test/10.0.0.40
address=/dns01.lan/10.0.0.51
address=/mysql/10.0.0.90
address=/mysql.lan/10.0.0.90
address=/.home.example.test/10.0.0.17
address=/.prod.example.test/10.0.0.17
```

- [ ] **Step 2: Write the failing probes** in `test/integration/probes_test.go` (`//go:build integration`):

```go
func TestMockConfigAnswersLocalNames(t *testing.T) {
	s := startDnsmasq(t)
	s.eventually(t, "nas.home.example.test", "10.0.0.40")
	s.eventually(t, "anything.home.example.test", "10.0.0.17")
	s.eventually(t, "example.zip", "127.0.0.1")
}

func TestHostsDirRecordBeatsWildcardWithoutReload(t *testing.T) {
	s := startDnsmasq(t)
	s.eventually(t, "app.home.example.test", "10.0.0.17")
	s.writeHosts(t, "10.0.0.80 app.home.example.test\n")
	s.eventually(t, "app.home.example.test", "10.0.0.80")
}

func TestHostsDirUpdateWithoutReload(t *testing.T) {
	s := startDnsmasq(t)
	s.writeHosts(t, "10.0.0.80 app.home.example.test\n")
	s.eventually(t, "app.home.example.test", "10.0.0.80")
	s.writeHosts(t, "10.0.0.81 app.home.example.test\n")
	s.eventually(t, "app.home.example.test", "10.0.0.81")
}

func TestHostsDirRemovalNeedsReload(t *testing.T) {
	s := startDnsmasq(t)
	s.writeHosts(t, "10.0.0.80 app.home.example.test\n")
	s.eventually(t, "app.home.example.test", "10.0.0.80")
	s.writeHosts(t, "")
	time.Sleep(2 * time.Second)
	if got := s.lookup(t, "app.home.example.test"); !slices.Equal(got, []string{"10.0.0.80"}) {
		t.Fatalf("before reload = %v, want the stale record [10.0.0.80]", got)
	}
	s.reload(t)
	s.eventually(t, "app.home.example.test", "10.0.0.17")
}
```

- [ ] **Step 3:** Run `go vet -tags integration ./test/integration/`. Expected: FAIL, undefined `startDnsmasq`.
- [ ] **Step 4:** Implement the harness in `test/integration/harness_test.go`. Decisions the signatures do not settle:
  - `startDnsmasq` fails the test (not skip) when `dnsmasq` or `dig` is not on the path. It logs `dnsmasqVersion(t)`.
  - Command line: `dnsmasq --keep-in-foreground --conf-file=<abs path of ../../testdata/dnsmasq/dnsmasq.conf> --hostsdir=<t.TempDir()> --port=<free port> --listen-address=127.0.0.1 --bind-interfaces --no-hosts --pid-file= --log-facility=-`. Add `--user=root` when `os.Geteuid() == 0`, otherwise dnsmasq drops privileges and cannot read the temp directory.
  - The free port comes from binding UDP `127.0.0.1:0` and closing it.
  - `startDnsmasq` waits up to 5s for `nas.home.example.test` to resolve, and registers `t.Cleanup` to kill the process and dump its output on failure.
  - `lookup` runs `dig +short +time=1 +tries=1 @127.0.0.1 -p <port> <name> A` and returns the sorted non-empty lines.
  - `writeHosts` writes the single managed file `managed.hosts` atomically: temp file in the hosts directory whose name starts with `.` (dnsmasq ignores dotfiles there), then rename.
  - `reload` sends `SIGHUP`. `eventually` polls `lookup` every 100ms for up to 5s and fails with the last answer.
- [ ] **Step 5:** Write `test/integration/Dockerfile`, two stages:
  - Build stage `golang:1.27-alpine`: copy the repo, `CGO_ENABLED=0 go test -c -tags integration -o /out/integration.test ./test/integration/`.
  - Run stage `alpine:3.12`: `apk add --no-cache dnsmasq bind-tools`, then `RUN dnsmasq --version | grep -q 'version 2.85'` so the build fails on any other version. Copy the test binary to `/src/test/integration/` and `testdata/` to `/src/testdata/`, `WORKDIR /src/test/integration`, `CMD ["./integration.test", "-test.v"]`.
  - If `alpine:3.12` does not provide 2.85, use the oldest Alpine release whose package is 2.85 (start with `alpine:3.14`) and say so in the findings.
- [ ] **Step 6:** Add Make targets:
  - `integration`: fail with `integration tests need Linux; run 'make integration-docker'` when `uname -s` is not `Linux`; fail with a clear message when `dnsmasq` or `dig` is missing; then `go test -tags integration -count=1 -v ./test/integration/`.
  - `integration-docker`: `docker build -f test/integration/Dockerfile -t dnsmasq-manager-integration . && docker run --rm dnsmasq-manager-integration`.
- [ ] **Step 7:** On the Mac run `make integration`. Expected: exits non-zero with the `make integration-docker` message.
- [ ] **Step 8:** Run `make integration-docker`. The log must show `Dnsmasq version 2.85`. These tests are probes: for any test that fails because dnsmasq really behaves differently (not because of a harness bug), change the assertion to what dnsmasq does, and keep the test name truthful by renaming it. Repeat until the run passes.
- [ ] **Step 9:** Write `docs/dnsmasq-findings.md`: one row per probe with the expected behaviour, the observed behaviour on 2.85, and what it means for the service design (precedence over wildcards, whether an update leaves the old address behind, whether removal needs SIGHUP, the dotfile rule for temp files). Leave an "Ubuntu CI version" column to fill in Task 6.
- [ ] **Step 10:** Run `make lint && make test`. Expected: PASS (the tagged files are excluded from the default build).
- [ ] **Step 11:** Commit: `test: add mock dnsmasq config and behaviour probes`.

### Task 3: Session-start hook

**Files:**
- Create: `.claude/settings.json`, `.claude/hooks/session-start.sh`, `test/hook/hook_test.sh`
- Modify: `Makefile` (target `test-hook`)

**Interfaces:**
- Consumes: `make tools` from Task 1.
- Produces: the status block below on stdout in cloud sessions; Make target `test-hook`.

- [ ] **Step 1:** Create `.claude/settings.json` with exactly:

```json
{
  "enabledPlugins": {
    "superpowers@claude-plugins-official": true
  },
  "hooks": {
    "SessionStart": [
      {
        "matcher": "startup|resume",
        "hooks": [
          {
            "type": "command",
            "command": "bash \"$CLAUDE_PROJECT_DIR\"/.claude/hooks/session-start.sh",
            "timeout": 300
          }
        ]
      }
    ]
  }
}
```

- [ ] **Step 2: Write the failing test** `test/hook/hook_test.sh` (bash, no dependencies). It builds a temp `bin/` holding symlinks to the basic commands the hook needs (`bash`, `cat`, `grep`, `id`, `mkdir`, `sed`, `uname`, `env`) plus stub scripts, and runs the hook with `PATH` set to that directory only, so the real machine's tools cannot leak in. Each stub appends its name and arguments to a call log and exits with a code the case sets. Cases, each asserting exit code 0:
  1. `CLAUDE_CODE_REMOTE` unset: stdout is empty and the call log is empty.
  2. Cloud, nothing installed, running as root (`id -u` stub prints `0`): the log contains `claude plugin marketplace add anthropics/claude-plugins-official`, `claude plugin install superpowers@claude-plugins-official --scope user`, `apt-get install -y --no-install-recommends dnsmasq-base dnsutils` and `make -C <project dir> tools`; stdout contains `- superpowers: installed this session`.
  3. Cloud, everything present (`claude plugin list` prints `superpowers@claude-plugins-official`; stubs for `dnsmasq`, `dig`, `golangci-lint` exist): the log has no `install` and no `marketplace add` line; stdout contains `- superpowers: already installed`, `- dnsmasq: present`, `- dig: present`, `- golangci-lint: present`.
  4. Cloud, `claude plugin install` exits 1: stdout contains `- superpowers: NOT installed`.
  5. Cloud, no `claude` stub at all: stdout contains `- superpowers: NOT installed`.
  6. Cloud, `id -u` prints `1000` and `sudo -n true` exits 1: the log has no `apt-get` line; stdout contains `- dnsmasq: missing` and `- dig: missing`.
  7. Cloud with `CLAUDE_ENV_FILE` set to a temp file: the file contains an `export PATH=` line that includes the `go env GOPATH` stub's value followed by `/bin`.
- [ ] **Step 3:** Add Make target `test-hook`: `bash test/hook/hook_test.sh`. Run it. Expected: FAIL, hook script missing.
- [ ] **Step 4:** Implement `.claude/hooks/session-start.sh` with a file-level comment block (purpose, that it is cloud-only, that it never fails the session). Behaviour:
  - Exit 0 silently unless `CLAUDE_CODE_REMOTE` is `true`.
  - No `set -e`. All command output goes to stderr. Only the status block goes to stdout. Always `exit 0`.
  - Plugin: skip when `claude plugin list` output contains `superpowers@claude-plugins-official`; otherwise run the marketplace add, then the install with `--scope user`.
  - Tools: when `dnsmasq` or `dig` is missing run `apt-get install -y --no-install-recommends dnsmasq-base dnsutils`, directly as root or through `sudo -n` when `sudo -n true` succeeds, otherwise skip. When `golangci-lint` is missing (also check `$(go env GOPATH)/bin`) run `make -C "$CLAUDE_PROJECT_DIR" tools`. Run `go mod download` in the project directory.
  - When `CLAUDE_ENV_FILE` is set, append `export PATH="$PATH:<GOPATH>/bin"` to it.
  - Print exactly this block, with each value filled in:

```
dnsmasq-manager session setup (cloud)
- go: <output of `go version`, or "missing">
- dnsmasq: <present|missing>
- dig: <present|missing>
- golangci-lint: <present|missing>
- superpowers: <already installed|installed this session (loads after /reload-plugins or in the next session)|NOT installed (install command failed or claude CLI unavailable)>
Workflow: see CLAUDE.md. If superpowers is not loaded, follow the steps there by hand.
```

- [ ] **Step 5:** Run `make test-hook`. Expected: all 7 cases pass.
- [ ] **Step 6:** Run `bash .claude/hooks/session-start.sh; echo "exit=$?"` on the Mac. Expected: only `exit=0`.
- [ ] **Step 7:** Run `python3 -m json.tool .claude/settings.json >/dev/null && echo ok`. Expected: `ok`.
- [ ] **Step 8:** Commit: `feat: add cloud session-start hook`.

### Task 4: CLAUDE.md, requirements and README

**Files:**
- Create: `CLAUDE.md`, `README.md`, `docs/requirements/dnsmasq-api.md`, `docs/cloud-session-findings.md`

- [ ] **Step 1:** Write `CLAUDE.md`, under 80 lines, with these sections and nothing else:
  - **Project:** two lines, and a pointer to `docs/requirements/dnsmasq-api.md`.
  - **Workflow:** requirements, then a design doc in `docs/superpowers/specs/`, then a plan in `docs/superpowers/plans/`, then test-driven implementation, then review. Use the superpowers skills when the plugin is loaded; follow the same steps by hand when it is not.
  - **Commands:** `make build`, `make test`, `make lint`, `make test-hook`, `make integration` (Linux with dnsmasq and dig: cloud and CI), `make integration-docker` (the Mac, and the run that matches production dnsmasq 2.85).
  - **Rules:** standard library first, a new dependency needs a stated reason in a design doc; verify locally before pushing; no private zone, LAN address or token in the repo, use `<private-zone>` and `home.example.test`; the live DNS host is reachable only from Gavin's machine, read-only for sense checks, and any change to it needs his explicit say each time.
  - **Commits:** Conventional Commits, no `chore`, breaking changes via a `BREAKING CHANGE:` footer only, subject at most 72 characters, one atomic commit per logical change, never force-push `main`.
  - **Comments:** doc comments on packages, types and functions; no narrating inline comments; YAML may be commented freely.
  - **UI changes:** check in a real browser, with before and after screenshots on the pull request.
- [ ] **Step 2:** Write `docs/requirements/dnsmasq-api.md` in this order:
  1. A short intro saying this is the input to the service design.
  2. **Corrections from verification:** the seven bullets from the design doc section "`docs/requirements/dnsmasq-api.md`", updated with the probe results from `docs/dnsmasq-findings.md` (replace "must be tested" and "still unverified" with what Task 2 found).
  3. **Added by the owner:** the read-only web page on port 80 (current records, last 50 API requests), the aim of retiring `lan-dns` with hand-written rules in the service's config file, and the standard-library rule, plus the open points listed in the design doc.
  4. **Original requirements:** the appendix of this plan, verbatim.
- [ ] **Step 3:** Write `docs/cloud-session-findings.md` as an unanswered checklist taken from the design doc section "The cloud test", including the decision rule, for a cloud session to fill in.
- [ ] **Step 4:** Write `README.md`: what the project is, current status (foundation only, no service yet), the make targets, how the session-start hook works and its known limit (the plugin may only load after `/reload-plugins` or in a second session, pending the cloud test), and links to the design, requirements and findings.
- [ ] **Step 5:** Run `grep -rnE 'gsdev\.me|172\.16\.' . --exclude-dir=.git`. Expected: no output.
- [ ] **Step 6:** Commit: `docs: add CLAUDE.md, service requirements and README`.

### Task 5: CI workflow

**Files:**
- Create: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: Make targets `tools`, `lint`, `test`, `test-hook`, `integration`, `integration-docker`, `build`.

- [ ] **Step 1:** Write the workflow, commented generously. Triggers: `push` to `main` and `pull_request`. Top-level `permissions: contents: read`. `concurrency` cancels superseded runs per ref. Use the current major versions of `actions/checkout` and `actions/setup-go`, with `go-version-file: go.mod`. Jobs, all on `ubuntu-24.04`:
  - `lint-and-unit`: `make tools`, `make lint`, `make test`, `make test-hook`.
  - `integration-ubuntu`: `sudo apt-get update && sudo apt-get install -y --no-install-recommends dnsmasq-base dnsutils`, then `make integration`.
  - `integration-alpine`: `make integration-docker`.
  - `build`: `make build`, then fail unless `file bin/dnsmasq-manager` reports `statically linked`.
- [ ] **Step 2:** Run `docker run --rm -v "$PWD":/repo -w /repo rhysd/actionlint:latest`. Expected: no output. If the image cannot be pulled, say so and rely on Task 6.
- [ ] **Step 3:** Commit: `ci: add lint, test, integration and build workflow`.

### Task 6: Publish and confirm CI

This task creates a private GitHub repo, as Gavin approved.

- [ ] **Step 1:** Run the full local check first: `make lint test test-hook build integration-docker`. Expected: all pass.
- [ ] **Step 2:** Run `gh repo create gsdevme/dnsmasq-manager --private --source . --remote origin --push`.
- [ ] **Step 3:** Run `gh run watch --exit-status` on the run for `main`. Expected: all four jobs green.
- [ ] **Step 4:** Read the dnsmasq version from the `integration-ubuntu` log, fill the "Ubuntu CI version" column in `docs/dnsmasq-findings.md`, and note any probe that behaved differently there. Commit: `docs: record dnsmasq probe results from CI`. Push.
- [ ] **Step 5:** If a job fails, fix it in a new `fix:` or `ci:` commit and push. Do not amend pushed commits.

### Task 7: Cloud test (needs Gavin)

- [ ] **Step 1:** Gavin starts a Claude Code cloud session on `gsdevme/dnsmasq-manager` and asks it to fill in `docs/cloud-session-findings.md`.
- [ ] **Step 2:** Apply the decision rule from the design doc: keep approach A, or add the environment setup script to the README, or fall back to committed skills. Commit the findings and any README change as `docs: record cloud session findings`.

Until this task is done, the plugin install in cloud sessions is reported as not yet verified.

## Verification (end to end)

- `make lint test test-hook build` pass on the Mac.
- `make integration-docker` passes on the Mac and logs dnsmasq 2.85.
- `make integration` on the Mac fails fast with the pointer to `make integration-docker`.
- The hook prints nothing locally and exits 0; its seven test cases pass.
- No private value in the repo (`grep` in Task 4, Step 5).
- All four GitHub Actions jobs are green on `main`.
- `docs/dnsmasq-findings.md` states what dnsmasq actually does for each probe.

## Not in this plan

- Any service behaviour. That starts with a new design doc once this lands.
- Any change to the DNS host or the `lan-dns` repo.

## Appendix: requirements text to commit verbatim

Source: Gavin's homelab session. `<private-zone>` stays a placeholder.

# Build: an HTTP API for managing local dnsmasq records, with an external-dns webhook provider

## Goal
A small service that lets a Kubernetes cluster register and remove LAN-only DNS names in dnsmasq automatically. It has two faces:
1. A simple REST API for managing A/AAAA/CNAME records in dnsmasq.
2. An external-dns "webhook provider" API on top of it, so external-dns in the cluster can drive it without custom code on the cluster side.

## Environment
- dnsmasq runs in an LXC container on the LAN, outside the cluster. The service runs in the same container (it must write files dnsmasq reads).
- The cluster has a private ingress controller (Traefik, IngressClass `traefik-private`) on a single node at a fixed LAN IP. Every private hostname resolves to that one IP. Private hostnames have no public DNS record.
- Private hosts all live under one zone: <private-zone> (supplied as config; never hard-code it).
- Expected scale: tens of records, a handful of changes per week.

## How records must reach dnsmasq
- Use dnsmasq's `hostsdir=<dir>` (e.g. /etc/dnsmasq.hosts.d). dnsmasq watches that directory with inotify and loads new or changed files without a restart or SIGHUP.
- Known limitation to design around: with hostsdir, deleting a file or removing a line does NOT remove the record from dnsmasq's cache until it is reloaded. So adds/updates are file writes only; deletes must write the file and then send SIGHUP to dnsmasq (or `systemctl reload dnsmasq`). Verify this behaviour against the installed dnsmasq version and document what you found.
- A/AAAA records go in hosts-format files. CNAMEs cannot be expressed in a hosts file; either put them in a managed conf-dir file (`cname=`, needs a restart) or declare CNAME unsupported in v1. State which you chose and why.
- Write atomically (temp file in the same directory + rename), one managed file owned by this service, with a header saying it is generated. Never touch files it does not own.
- The service is the source of truth for its file: keep state in the file itself (parse on start), no separate database.

## REST API (management)
- GET /v1/records -> list {name, type, value, ttl?}.
- PUT /v1/records/{name} -> create or replace (idempotent).
- DELETE /v1/records/{name} -> remove (idempotent; 204 if absent).
- GET /healthz -> 200 when the hosts dir is writable and dnsmasq is running.
- Validation: names must be valid hostnames inside the configured allowed zone(s); values must be valid IPs inside configured allowed CIDRs (default RFC1918 only). Reject anything else with 422. This is the main safety control: the API must be incapable of pointing an arbitrary name at an arbitrary address.
- No wildcard records through the API.

## external-dns webhook provider API
Implement the external-dns webhook provider contract (see the external-dns docs, "Webhook provider" tutorial; check the current version of the contract before coding - do not rely on this summary alone):
- GET / -> negotiation; returns the domain filter (the private zone). Media type `application/external.dns.webhook+json;version=1` for requests and responses.
- GET /records -> current endpoints as a JSON array of {dnsName, targets[], recordType, recordTTL?, labels?, providerSpecific?}.
- POST /records -> body {Create[], UpdateOld[], UpdateNew[], Delete[]}; apply and return 204. Must be idempotent and apply the whole change set or none of it.
- POST /adjustendpoints -> return the endpoints unchanged (or normalised).
- external-dns normally expects the webhook as a sidecar on localhost. Here it is remote, called over the LAN via --webhook-provider-url. That makes authentication and TLS your responsibility (below).
- Ownership: support external-dns's TXT registry. dnsmasq can serve TXT via `txt-record=` in a conf file, which needs a restart, so the pragmatic option is to store TXT ownership records in the service's own state and return them from GET /records without publishing them in DNS. Alternatively document that external-dns must run with --registry=noop. Pick one, justify it.

## Security
- Listen only on the LAN interface; never on a public or wildcard address unless configured.
- Bearer-token auth on every endpoint except /healthz. Token from an env var or a root-only file, compared in constant time. Note that external-dns's webhook client may not be able to send custom auth headers - check the current version; if it cannot, put the service behind a reverse proxy that enforces mTLS or an IP allowlist limited to the cluster nodes, and document the chosen approach.
- TLS: accept a cert/key path; plain HTTP only if explicitly enabled.
- Run as an unprivileged user with write access to the hosts dir only, plus the narrowest possible way to signal dnsmasq (a sudoers rule for one command or a systemd ExecReload hook).
- Log every mutation (who, what, before/after) to stdout/journald.

## Non-functional
- Single static binary (Go preferred) or a single-file service with minimal dependencies; systemd unit included; config via env vars or one config file.
- Survives restart with no loss of state; safe under concurrent requests (serialise writes).
- Unit tests for validation, file rendering/parsing round-trip, and the change-set logic; an integration test that runs a real dnsmasq against a temp hostsdir and asserts with dig that add, update and delete take effect.

## Deliverables
1. Source, tests, systemd unit, example config.
2. README: install in the LXC, the dnsmasq config lines required (hostsdir=...), how to rotate the token, how to roll back (delete the managed file + reload).
3. The exact external-dns arguments the cluster side needs, as a values snippet: --provider=webhook, --webhook-provider-url=..., --source=ingress, --ingress-class=traefik-private, --domain-filter=<private-zone>, registry and policy flags matching your ownership decision. Targets come from the Ingress status address, which the private ingress controller already publishes as its node's LAN IP.
4. A short list of anything in this spec that turned out to be wrong or unsupported when checked against the real dnsmasq / external-dns versions.

## Out of scope
- Public DNS, DHCP management, a UI, multi-tenant access control.
- Deploying external-dns into the cluster (that is done from the GitOps repo using your values snippet).

## Open questions to answer before building
- Which dnsmasq version is installed, and does delete-without-reload behave as described?
- Does the installed external-dns version allow auth headers or client certs on the webhook call?
- Is CNAME support needed in v1?
