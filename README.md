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
- a session-start hook that prepares Claude Code cloud sessions;
- hooks and a `project-manager` agent that keep the roadmap issues in step
  with the work (see
  [How issues stay in step with the work](#how-issues-stay-in-step-with-the-work)).

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
| `make test-hook` | Tests the Claude Code hooks (session start, project manager and pull request review) against stub commands; needs only bash |
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

## How issues stay in step with the work

The roadmap is in GitHub issues: an epic and one issue per phase, each with
a task list and exit criteria. Hooks and an agent keep them current, so
nobody edits them by hand.

- A branch named `<type>/<issue>-<slug>`, such as `feat/4-serve`, belongs to
  that issue. Where the name cannot be chosen, as in a cloud session,
  `bash .claude/hooks/pm-issue.sh link <issue>` records the link instead.
- When work starts on a linked branch, `.claude/hooks/pm-start.sh` gives the
  session the issue's title, state, URL and body, once per session and
  branch. The body is shown only when a collaborator wrote it. On any other
  branch the session is told once how to link one.
- When a turn ends with pushed commits that have not been synced,
  `.claude/hooks/pm-stop.sh` holds the session open once and asks it to
  dispatch the `project-manager` agent (`.claude/agents/project-manager.md`).
- The agent ticks a checklist item only when a commit on the branch completes
  it, and keeps one progress comment per branch that names the commit behind
  each ticked item. It never closes, creates or relabels an issue.
- A pull request carries `Closes #<issue>`, so the issue closes on merge and
  the epic's task list follows.

All GitHub calls go through `.claude/hooks/pm-issue.sh`, over REST with
`gh api`, which also works in cloud sessions. State is kept per clone in
`.git/project-manager/`. Not yet tried on a real issue: the first roadmap
branch is the trial, and the design lists what it has to settle.

## Automatic review of pull requests

A pull request opened by a local Claude Code session is reviewed by a
second Claude Code run that has not seen the session's conversation.

- `.claude/hooks/pr-review.sh` runs after each shell command. When the
  command opened a pull request (`gh pr create`, or a REST `POST` to
  `/pulls`), it asks GitHub for the open pull request on the current branch
  and starts `claude -p "/code-review medium --comment <n>"` in the
  background. The session is told in one line and carries on.
- The review posts its findings as comments on the pull request, under the
  `gh` login of whoever runs the session. It is given the tools to read the
  repository and to comment, and denied the ones that edit files, push or
  merge; this repository's hooks are turned off inside it. That is a guard
  against a mistaken step, not a sandbox.
- Each pull request is reviewed once. State and the run's log are kept per
  clone in `.git/pr-review/` (`reviewed-<n>`, `review-<n>.log`). A run that
  fails removes its `reviewed-<n>` file and says so in the log. To review
  again after more commits, run `/code-review <n>` in a session.
- `PR_REVIEW_EFFORT` sets the review's effort level: `low`, `medium` (the
  default), `high` or `xhigh`.

Not covered: pull requests opened in the browser, by release-please or from
a cloud session.

## Documents

- [Service requirements](docs/requirements/dnsmasq-api.md): the input to the
  service design.
- [dnsmasq findings](docs/dnsmasq-findings.md): what the probes observed on
  dnsmasq 2.91, the minimum supported version, and why older ones are not
  supported.
- [Cloud session findings](docs/cloud-session-findings.md): the cloud test
  checklist, not yet answered.
- [Project-manager agent design](docs/superpowers/specs/2026-11-17-project-manager-agent-design.md)
  and [plan](docs/superpowers/plans/2026-11-17-project-manager-agent.md): the
  hooks and agent that keep issues in step with the work.
- [Pull request review hook design](docs/superpowers/specs/2026-11-19-pr-review-hook-design.md)
  and [plan](docs/superpowers/plans/2026-11-19-pr-review-hook.md): the hook
  that starts a fresh-context review of each pull request a session opens.
- [Release path design](docs/superpowers/specs/2026-11-17-release-path-design.md)
  and [plan](docs/superpowers/plans/2026-11-17-release-path.md): the CLI on
  cobra, `make dist`, the installer and the release workflow.
- [Foundation design](docs/superpowers/specs/2026-11-17-agentic-repo-foundation-design.md)
  and [plan](docs/superpowers/plans/2026-11-17-agentic-repo-foundation.md):
  how this repository was set up.
