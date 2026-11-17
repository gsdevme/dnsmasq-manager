# Agentic repo foundation for dnsmasq-manager

Date: 2026-11-17
Status: approved 2026-11-17

## Intent

dnsmasq-manager will be a small Go service (an HTTP API for LAN DNS records
in dnsmasq, with an external-dns webhook provider on top). Before the service
is built, the repository itself needs to be set up so that Claude Code
sessions build it from committed specs using the superpowers workflow
(brainstorm, design doc, plan, test-driven implementation, review).

The owner wants to run that work in Claude Code cloud sessions as well as
locally. A cloud session starts from a clean image with nothing from the
owner's machine, so everything a session needs has to come from the repo.

This document covers the repository foundation only. The service gets its own
design doc afterwards, written inside the repo this one produces.

## Goals

1. A fresh cloud session on this repo gets the superpowers plugin installed
   by a committed session-start hook, with no manual steps in the session.
2. The same hook leaves a cloud session able to run every check the project
   has: build, unit tests, lint, and an integration test that needs real
   `dnsmasq` and `dig` binaries.
3. The service requirements are committed in the repo, corrected against what
   was verified on the real host and against external-dns v0.24.0, so any
   session can design from them.
4. `CLAUDE.md` tells a session how work is done here, including the working
   rules that normally live in the owner's personal config and do not reach
   the cloud.
5. A Go skeleton that builds, tests and lints green, locally and in CI.
6. As much as possible can be verified on the owner's Mac without touching
   the DNS host: the integration tests run locally against the same dnsmasq
   version the host runs, using a mock config shaped like the live one.

## Non-goals

- Implementing any service behaviour (records, API, webhook). Next project.
- Deploying to the DNS host or configuring external-dns in the cluster.
- Changing the DNS host or the `lan-dns` repo.

## Anti-goals

These would make the result a failure even if it technically works:

- The private DNS zone, any token, or any credential appearing in the repo.
  Docs and tests use placeholders (`<private-zone>`) and reserved example
  names (`home.example.test`).
- The hook slowing down, changing, or printing noise into local sessions. The
  owner already has superpowers installed locally.
- The hook failing a session. If the network blocks a download, the session
  must still start, with a clear note about what is missing.
- The hook reinstalling things on every start. It must be idempotent and
  quick when everything is already present.

## Verified facts this design rests on

From the Claude Code docs (checked 2026-11-17):

- Cloud sessions ignore `enabledPlugins` and `extraKnownMarketplaces` in the
  repo's `.claude/settings.json`. Nothing from the user's `~/.claude` carries
  over. Repo hooks do run, in single-repo sessions.
- `claude plugin marketplace add anthropics/claude-plugins-official` followed
  by `claude plugin install superpowers@claude-plugins-official` works
  non-interactively. Plugins load at session start, so a plugin installed by
  a session-start hook is active after `/reload-plugins` or in the next
  session.
- `CLAUDE_CODE_REMOTE=true` is set in cloud sessions and never locally.
  `CLAUDE_PROJECT_DIR` is the repo root. Hook stdout is added to the
  session's context before the first prompt. Default hook timeout is 600s.
- The cloud image is Ubuntu 24.04 x86_64 with Go preinstalled (version not
  documented). The default network allowlist includes github.com,
  proxy.golang.org, sum.golang.org and archive.ubuntu.com.
- GitHub traffic goes through a proxy that serves repos attached to the
  session. Archive downloads of unattached repos return 403.

Not confirmed by the docs, to be settled by the cloud test below:

- Whether the proxy allows cloning the plugin marketplace repo.
- Whether `/reload-plugins` works in a cloud session.
- Whether the session runs as root (needed for `apt-get`).
- The Go version in the image.

From the DNS host (read-only inspection, 2026-11-17):

- One server only: hostname `dns01`, Alpine Linux 3.12, x86_64, OpenRC (no
  systemd). dnsmasq 2.85 with inotify compiled in, DNS only.
- Its config is a symlink into a checkout of the `lan-dns` git repo. The
  config contains wildcard `address=` entries that will coexist with the new
  service during rollout and be removed afterwards.
- The second address it answers on is a leftover VRRP floating address on the
  same machine. The other DNS servers named in the config are gone.

From external-dns v0.24.0 source and docs:

- The webhook client sends only `Accept` and `Content-Type`. No flags exist
  for custom headers, client certificates or a custom CA.
- Change-set JSON keys are `create`, `updateOld`, `updateNew`, `delete`.
- TXT ownership records are sent as ordinary endpoints through
  `POST /records` and must be returned from `GET /records`.

## Approaches

Three ways to get superpowers into a cloud session were considered.

**A. The session-start hook installs the plugin (chosen).** The hook runs the
two `claude plugin` commands when `CLAUDE_CODE_REMOTE=true`. Everything lives
in the repo. Trade-off: the plugin is probably not active on the first turn
of the first session, and the marketplace clone may be blocked by the proxy.

**B. Skills committed to the repo.** A pinned copy of the superpowers skills
in `.claude/skills/`, with the hook injecting the intro text. Active from the
first turn with no network. Trade-off: goes stale against upstream and shows
up twice locally beside the owner's installed plugin.

**C. Cloud environment setup script.** The same two install commands in the
environment's setup script, which runs before Claude starts, so the plugin
should be loaded from the first turn. Trade-off: configured in the cloud UI,
not committed, and repeated per environment.

The owner picked A: it is what was asked for and keeps the setup in the repo.
A real cloud session is the first test of the build. If the plugin is not
usable in the first session, fall back to C documented in the README, and to
B only if C also fails.

## Design

### Layout

```
.claude/settings.json            hook registration, enabledPlugins
.claude/hooks/session-start.sh   the hook
CLAUDE.md                        how work is done in this repo
README.md                        what this is, how to build and test
docs/requirements/dnsmasq-api.md corrected service requirements
docs/cloud-session-findings.md   results of the cloud test
docs/dnsmasq-findings.md         what the behaviour probes showed on 2.85
testdata/dnsmasq/dnsmasq.conf    mock config shaped like the live one
test/integration/Dockerfile      Alpine image with dnsmasq 2.85 and dig
docs/superpowers/specs/          design docs (this file)
docs/superpowers/plans/          implementation plans
cmd/dnsmasq-manager/main.go      entry point (prints version, nothing else)
internal/version/                one tiny package with one test
go.mod
Makefile
.golangci.yml
.github/workflows/ci.yml
.gitignore
```

### `.claude/settings.json`

- Registers one `SessionStart` hook, matcher `startup|resume`, command
  `bash "$CLAUDE_PROJECT_DIR"/.claude/hooks/session-start.sh`, timeout 300.
- Sets `enabledPlugins` for `superpowers@claude-plugins-official`. This has no
  effect in the cloud. It is there for anyone opening the repo locally.

### The hook

A bash script with a file-level comment block explaining its purpose.

1. If `CLAUDE_CODE_REMOTE` is not `true`, exit 0 silently. Local sessions are
   untouched.
2. Plugin step. If `claude plugin list` already shows
   `superpowers@claude-plugins-official`, skip. Otherwise add the marketplace,
   then install the plugin at user scope. Record whether it was installed in
   this run.
3. Tool step. Install only what is missing:
   - `dnsmasq` and `dig` via `apt-get install -y dnsmasq-base dnsutils`
     (`dnsmasq-base` provides the binary without starting a system service).
     Use `sudo -n` when not root; skip with a note when neither works.
   - `golangci-lint` at a pinned version via `go install`, because the Go
     module proxy is on the allowlist and GitHub release downloads may not be.
   - `go mod download`.
4. Report. Print a short plain-text status to stdout, which becomes session
   context: Go version, which tools are present or missing, and whether
   superpowers was installed in this run. When it was, say that it loads
   after `/reload-plugins` or in the next session, and that `CLAUDE.md`
   describes the workflow to follow by hand in the meantime.

Rules for the whole script: every step tolerates failure and carries on;
command output goes to stderr so only the final status reaches the context;
the script always exits 0; a second run with everything present makes no
network calls beyond `claude plugin list`.

### `CLAUDE.md`

Short, and limited to what a session cannot work out from the code:

- What the project is, in two lines, and where the requirements are.
- The workflow: requirements, then a design doc in `docs/superpowers/specs/`,
  then a plan in `docs/superpowers/plans/`, then test-driven implementation,
  then review. Use the superpowers skills for each step when the plugin is
  loaded; follow the same steps by hand when it is not.
- Commands: `make build`, `make test`, `make lint`, `make integration`,
  `make integration-docker`, and which one to use where.
- Verify locally before pushing, and never use the live DNS host as a test
  bed.
- Secrets and private values: the private zone and tokens never go in the
  repo; use `<private-zone>` and `home.example.test`.
- The live DNS host: reachable only from the owner's local machine, never
  from the cloud. Read-only inspection is allowed for sense checks. Any change
  to it needs the owner's explicit say each time.
- Commit rules carried over from the owner's personal config, since that does
  not reach the cloud: Conventional Commits, no `chore` type, breaking changes
  via a `BREAKING CHANGE:` footer only, one atomic commit per logical change.
- Comment rules carried over the same way: doc comments on packages, types
  and functions; no narrating inline comments; YAML may be commented freely.

- Browser check for UI changes, carried over the same way: the service will
  have a small web page, so any change to it is checked in a real browser
  with before and after screenshots attached to the pull request.

Design call (not the owner's): the owner's model-delegation rules are left
out. They are a personal cost preference.

### `docs/requirements/dnsmasq-api.md`

The requirements handed over from the owner's homelab session, committed as
the input to the service design, with a "Corrections from verification"
section at the top:

- The host is Alpine with OpenRC: an OpenRC init script and `rc-service`
  reload replace the systemd unit, `systemctl reload` and journald.
- dnsmasq is 2.85 with inotify, single instance. No replication is needed.
- Bearer-token auth cannot apply to the external-dns side. The auth choice
  for the webhook listener is an open decision for the service design.
- Change-set keys are lowercase camelCase.
- TXT ownership records can be held in the service's state and returned from
  `GET /records` without being published in DNS.
- Existing wildcard `address=` entries coexist during rollout. Whether a
  hosts-file record takes precedence over them on 2.85 must be tested.
- Still unverified: the claim that removing a hosts-dir entry needs a reload.

It also gets an "Added by the owner" section, which overrides the original
"no UI" line:

- A small read-only web page on port 80 showing the current DNS records and
  the last 50 API requests.
- The aim is to retire the `lan-dns` repo. The service takes over what that
  repo does today, and the few hand-written rules that are still wanted move
  into the service's own config file.
- Points the `lan-dns` aim raises for the service design, recorded as open:
  whether the service renders the whole dnsmasq config or only the records;
  how hand-written rules such as wildcards and blocked top-level domains are
  expressed in the config file, given that the API itself refuses wildcards;
  and the cutover steps that remove the symlink and the nightly pull.
- Points the web page raises for the service design, recorded as open: how an
  unprivileged process binds port 80 on Alpine, whether the page needs any
  login on the LAN, and whether the request history is kept in memory only
  (lost on restart) given the "no separate database" requirement.

The zone stays as `<private-zone>` throughout.

### Go skeleton

- Module path `github.com/gsdevme/dnsmasq-manager` (design call, taken from
  the account that hosts `lan-dns`).
- `cmd/dnsmasq-manager/main.go` prints the version and exits. No service
  code.
- `internal/version` holds the version string, set at build time with
  `-ldflags`, and has one unit test so the test and lint paths are exercised.
- `make build` produces a static binary: `CGO_ENABLED=0`, `linux/amd64`,
  matching the Alpine host. `make test` runs unit tests with `-race`.
  `make lint` runs `golangci-lint`. `make integration` runs tests behind the
  `integration` build tag and fails clearly if `dnsmasq` or `dig` is missing.
- Standard library first (owner's rule). The skeleton has no third-party
  dependencies. The service is expected to be built on `net/http`,
  `log/slog`, `html/template`, `encoding/json` and `testing`. Any dependency
  added later needs a stated reason in the design doc that introduces it.
  This rule also goes in `CLAUDE.md`.

### Local verification and the mock config

The owner's Mac has Go 1.27.1, `dig` and Docker, and no `dnsmasq` on the
path. The DNS host runs dnsmasq 2.85 on Alpine. So there are two ways to run
the same integration tests:

- `make integration` runs them natively when `dnsmasq` and `dig` are on the
  path. This is what cloud sessions and the Ubuntu CI job use.
- `make integration-docker` builds `test/integration/Dockerfile` (Alpine,
  with dnsmasq pinned to 2.85 and `bind-tools` for `dig`) and runs the tests
  inside it. This is the default on the Mac, and it is the run that matches
  the host. The tests print the dnsmasq version they ran against.

`testdata/dnsmasq/dnsmasq.conf` is a mock of the live config with the same
shape and no real names: the global flags, `no-resolv`, upstream `server=`
lines, explicit `address=` records, a wildcard `address=/.home.example.test/`
entry and blocked top-level domains. Addresses are RFC1918. Tests assert only
on names the mock answers itself, so they never need the internet.

Instead of a throwaway placeholder, the first integration tests are two
behaviour probes that answer the open dnsmasq questions. They use the mock
config plus a `hostsdir=` temp directory:

1. A name written to the hosts directory is served without a reload, and it
   wins over the wildcard `address=` entry that also matches it.
2. A name removed from the hosts directory is still served until dnsmasq is
   sent SIGHUP, and is gone afterwards.

The probes record what dnsmasq actually does. If a probe contradicts the
expectation, the test is changed to assert the real behaviour and the
difference is written up. Results for 2.85 and for the Ubuntu version go in
`docs/dnsmasq-findings.md`, which the service design then relies on.

### CI

One GitHub Actions workflow on push and pull request, Go version read from
`go.mod`, with these jobs:

- lint (`golangci-lint`) and unit tests with `-race`;
- integration tests natively on Ubuntu after
  `apt-get install dnsmasq-base dnsutils`;
- integration tests in the Alpine 2.85 container, the same
  `make integration-docker` the owner runs locally;
- a static `linux/amd64` build.

### The cloud test

This is the acceptance test for goal 1 and 2, and it needs the owner: once
the repo is pushed, the owner starts a cloud session on it.

In a fresh cloud session, record in `docs/cloud-session-findings.md`:

- Did the marketplace add and plugin install succeed?
- Was superpowers usable on the first turn? After `/reload-plugins`? In a
  second session?
- Go version, whether the session is root, whether `dnsmasq`, `dig` and
  `golangci-lint` were installed.
- Do `make test`, `make lint` and `make integration` pass?

Decision rule: if superpowers is usable in the first session (directly or
after reload), approach A stands. If it only appears in a second session,
add approach C to the README as the recommended extra step. If the install
itself is blocked, switch to C, then B.

## Left to the builder

- Exact Go version (the current stable release) and pinned `golangci-lint`
  version, and the linter set in `.golangci.yml`.
- Wording of `CLAUDE.md`, `README.md` and the hook's status message.
- Makefile details and `.gitignore` contents.

## Settled after review

- **GitHub repo (owner's decision):** private, at `gsdevme/dnsmasq-manager`,
  created with `gh repo create --private` as part of this work and pushed.
  The rule that no private value goes in the repo stands regardless, so the
  repo can be made public later without a history rewrite.
- **Source of the requirements text:** the full requirements were handed to
  the session that wrote this design, as a message from the owner's homelab
  session. The implementation plan must carry that text verbatim so
  `docs/requirements/dnsmasq-api.md` is the original plus the corrections and
  additions above, not a reconstruction.
- **How the work lands (design call):** the repo has no commits, so there is
  nothing to open a pull request against. This design doc is the first
  commit on `main`, followed by one atomic commit per logical piece (Go
  skeleton, hook and settings, `CLAUDE.md` and requirements, CI). `main` is
  then pushed and the cloud test runs against it. Later work uses branches
  and pull requests.
