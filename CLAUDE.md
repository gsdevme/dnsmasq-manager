# dnsmasq-manager

## Project

A Go service, not yet written: an HTTP API for LAN DNS records served by dnsmasq, with an external-dns webhook provider on top.
The service requirements are in `docs/requirements/dnsmasq-api.md`; start any service work there.

## Workflow

Every change to the service goes through these steps, in order:

1. Requirements: `docs/requirements/dnsmasq-api.md`, read with `docs/dnsmasq-findings.md`
   (what dnsmasq 2.91, the production and minimum version, was observed to do).
2. A design doc in `docs/superpowers/specs/`, named `YYYY-MM-DD-<topic>-design.md`.
3. A plan in `docs/superpowers/plans/`, named `YYYY-MM-DD-<topic>.md`.
4. Test-driven implementation: a failing test first, then the code that makes it pass.
5. Review of the finished change against its design doc and plan before it lands.

When the superpowers plugin is loaded, use its skill for each step (brainstorming,
writing-plans, test-driven-development, requesting-code-review). When it is not loaded,
for example in a cloud session where the hook has only just installed it, follow the
same steps by hand.

## Commands

- `make build`: static `linux/amd64` binary at `bin/dnsmasq-manager`, for the DNS host.
- `make dist`: the release files in `dist/` (binary, `.sha256` checksum, `install.sh`);
  `VERSION=v0.1.0` stamps the version.
- `make test`: unit tests with `-race`.
- `make lint`: golangci-lint at the version pinned in the Makefile (`make tools` installs it).
- `make test-hook`: tests the session-start hook against stub commands; needs only bash.
- `make test-install`: tests `install.sh` against stub commands. Needs bash, and dash as
  the default `TEST_SH`.
- `make test-install-docker`: runs `install.sh` end to end in `alpine:3.23` against the
  real binary, then the stub tests under BusyBox ash. Needs Docker.
- `make integration`: probes a real dnsmasq with `dig`. Needs Linux with `dnsmasq` and
  `dig` on PATH. Use it in cloud sessions and CI.
- `make integration-docker`: the same probes in `alpine:3.23`, pinned to dnsmasq 2.91.
  Use it on the Mac, and whenever the result must match production dnsmasq 2.91.

## Rules

- Minimums are Alpine 3.23 and dnsmasq 2.91; do not add code or probes for older ones.
- Standard library first. A new third-party dependency needs a stated reason in the
  design doc that introduces it.
- Verify locally before pushing: run the make targets that cover the change.
- No private zone, LAN address or token in the repo: not in code, tests, docs or commit
  messages. Write the zone as `<private-zone>` in prose and use `home.example.test` in
  configs and tests; example record addresses are in `10.0.0.0/24`.
- The live DNS host is reachable only from Gavin's machine, never from a cloud session.
  It is never a test bed. Read-only inspection for sense checks is allowed. Any change to
  it needs Gavin's explicit say each time; one approval does not cover the next change.

## Commits

- Conventional Commits: `type(scope): subject`, scope optional, imperative mood, no
  trailing period.
- Types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`,
  `revert`. Never `chore`.
- Breaking changes are marked with a `BREAKING CHANGE:` footer only, never `!`.
- Subject at most 72 characters; detail goes in the body after a blank line.
- One atomic commit per logical change; squash fixups into the commit they belong to.
- Never force-push `main`.

## Comments

- Doc comments on packages, types and functions: what they are for and what callers
  need to know.
- No inline comments that narrate what the code does, no change history in comments,
  no commented-out code. An inline comment is only for a constraint the code cannot show.
- YAML may be commented freely, as may shell config.

## UI changes

The service will have a small read-only web page. Check any change to it in a real
browser, and attach before and after screenshots to the pull request.
