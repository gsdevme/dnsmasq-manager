# Cloud session findings

Results of the cloud test: the first Claude Code cloud session on this
repository, which checks that the session-start hook
(`.claude/hooks/session-start.sh`) prepares a cloud session without manual
steps. The test is described in the section "The cloud test" of the
[foundation design](superpowers/specs/2026-11-17-agentic-repo-foundation-design.md).

**Not yet run.** Every item below is unanswered.

How to fill it in: the owner starts a fresh cloud session on `main`. The
session records each answer after the item, with its evidence (the hook's
status block, command output, or what the owner saw), and ticks the item
once it is answered. An unticked item means not yet answered, not "no".

## Session

- Date:
- Commit:
- Hook status block (paste it as printed at session start):

## Plugin

- [ ] Did `claude plugin marketplace add anthropics/claude-plugins-official` succeed?
- [ ] Did `claude plugin install superpowers@claude-plugins-official` succeed?
- [ ] Was superpowers usable on the first turn of the first session?
- [ ] Was superpowers usable after `/reload-plugins`?
- [ ] Was superpowers usable in a second session?

## Environment

- [ ] Go version in the image:
- [ ] Does the session run as root?
- [ ] Was `dnsmasq` installed (and which version)?
- [ ] Was `dig` installed?
- [ ] Was `golangci-lint` installed?

## Checks

- [ ] Does `make test` pass?
- [ ] Does `make lint` pass?
- [ ] Does `make integration` pass?

## Decision

The approaches are named in the section "Approaches" of the foundation
design: A is the session-start hook installing the plugin (current), B is
the superpowers skills committed to the repo, C is the same install commands
in the cloud environment's setup script.

Decision rule:

- If superpowers is usable in the first session, directly or after
  `/reload-plugins`, approach A stands.
- If it only appears in a second session, add approach C to the README as
  the recommended extra step.
- If the install itself is blocked, switch to C, and to B only if C also
  fails.

Outcome (which branch of the rule applies, and what was changed):
