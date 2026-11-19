# Code review on every pull request a session opens

Date: 2026-11-19
Status: approved 2026-11-19

## Intent

A pull request opened by a Claude Code session is reviewed by the session
that wrote it, or not at all. A review is worth more from a reader that has
not seen the work being done. This change starts one automatically: when a
session opens a pull request, a separate Claude Code run with a fresh
context reviews it with `/code-review` and posts its findings on the pull
request.

## Goals

1. When a session's shell command opens a pull request, a review of that
   pull request starts without anyone asking for it.
2. The review runs in its own process with its own context. It sees the
   pull request and the repository, not the session's conversation.
3. Findings are posted on the pull request as inline comments.
4. The session is not held up. It is told in one line that a review was
   started and where its log is.
5. A pull request is reviewed once, when it is opened.
6. The script is tested with stub commands, like the other hooks.

## Non-goals

- Pull requests opened any other way: in the browser, by release-please, or
  from a machine without this repository's hooks. A GitHub Action would
  cover those and is a separate change.
- Reviewing again when more commits are pushed. `/code-review <n>` can be
  run by hand.
- Cloud sessions. Whether a nested `claude` run works there is not
  documented, so the hook does nothing when `CLAUDE_CODE_REMOTE` is `true`.
- Fixing what the review finds. It comments; it does not edit or push.

## Anti-goals

- A review that starts reviews. The nested run must not fire this
  repository's hooks, or it could start another review or be asked to sync
  an issue.
- A session slowed or failed by the hook. It exits 0, returns as soon as
  the review is launched, and does nothing on any shell command that did not
  open a pull request.
- A second review of the same pull request because the command ran twice or
  two hooks raced.
- A background run with no limit on what it can do. It is given the tools
  to read the repository and comment on the pull request, and denied the
  ones that edit files, push or merge.

## Verified facts

From the Claude Code documentation, read 2026-11-19:

- `PostToolUse` fires only when the tool call succeeded, so a failed
  `gh pr create` never reaches the hook. The payload has the command
  (`tool_input.command`) and its output, but no exit status.
- A hook entry may carry `if`, one permission rule such as
  `Bash(gh pr create*)`. It is checked against each part of a compound
  command, and is best-effort: the script must check the command again.
- `claude -p "/code-review <n> --comment"` runs the bundled review skill
  without a terminal and posts inline comments. The effort level, if given,
  comes first. The review follows `CLAUDE.md`.
- A `claude -p` run loads the project's hooks unless told otherwise;
  `--settings '{"disableAllHooks": true}'` turns them off for that run.
- In `-p` mode a tool call that needs approval is denied. `--allowedTools`
  lists what may run.
- A hook that starts a background process must redirect its output and
  detach it, or the hook waits for it.

### Settled by the trial

The pull request for this change (#14) was opened from a clone with the
hook live, on 2026-11-19:

- The hook launched the review and told the session. The detached run
  finished and posted eight inline comments.
- The first tool list was too narrow for some commands the review tried
  (`git -C <path> diff`, `git remote get-url`, pipes into `head` or `jq`)
  and too wide in others (`gh api` with any method, `git fetch`). The list
  below is the corrected one.
- The review's findings on the hook itself were fixed in the same pull
  request: which pull request is reviewed, reads mistaken for creates, a
  failed run keeping its claim, and two handlers with one command string.

### Not yet verified

- That a process detached by a hook keeps running after the session ends.
  In the trial the session stayed open.

## Approaches

**A. The hook starts a separate headless Claude (chosen).** Fresh context,
findings on the pull request, the session carries on. Costs one review run
per pull request, and covers only pull requests opened by a session here.

**B. The hook tells the session to dispatch a reviewer subagent.** The
subagent's context is fresh, but its brief is written by the session that
wrote the code, and it depends on the session doing as asked.

**C. A GitHub Action on `pull_request`.** Covers every pull request however
it is opened, but is not a hook, and needs a secret in the repository.

The owner picked A.

## Design

### Files

```
.claude/hooks/pr-review.sh       PostToolUse hook
test/hook/pr_review_test.sh      stub-command suite
```

Modified: `.claude/settings.json` (two `PostToolUse` entries), `Makefile`
(`test-hook` runs the new suite), `CLAUDE.md` and `README.md` (a short
note each).

### Trigger

Two `PostToolUse` entries with matcher `Bash`, one with
`if: "Bash(gh pr create*)"` and one with `if: "Bash(gh api *)"`. The second
covers a pull request opened over REST. Their commands differ by one
ignored argument, so Claude Code cannot fold the two handlers into one. The
script then checks the command itself: it goes on only for `gh pr create`,
or for `gh api` on a path ending in `/pulls` that sends a `POST` or request
fields and is not an explicit `GET`. Anything else exits at once.

It also exits at once when `PR_REVIEW_NESTED` is set (it is running inside
a review), when `CLAUDE_CODE_REMOTE` is `true`, or when `claude` or `gh` is
missing. A pull request opened by a subagent's command is reviewed like any
other.

### Which pull request

The command's output names the pull request it opened: `gh pr create`
prints its URL and the REST response carries it. The script takes the
number from a `https://github.com/<owner>/<repo>/pull/<n>` URL in the
output part of the payload, never from the command text. A URL for another
repository means the pull request is not this clone's to review, and the
script does nothing.

When the output has no such URL, it asks GitHub for the open pull request
whose head is the current branch
(`gh api "repos/<slug>/pulls?head=<owner>:<branch>&state=open"`), using the
helpers in `pm-issue.sh` for the repository and branch. No such pull
request, or a failed call: it does nothing.

### Once per pull request

State is in `<git-common-dir>/pr-review/`. `reviewed-<n>` is created with
noclobber before the review is launched; if it already exists or cannot be
created, the script does nothing. `review-<n>.log` holds the review run's
output. If the review run exits with an error, the claim is removed and the
log says so, so the pull request is not left marked as reviewed.

### The review run

Amended by
[the single review comment design](2026-11-19-pr-review-single-comment-design.md):
the run no longer has `gh api` or a deny list, and the hook posts the
findings as one comment. The rest of this section is as first built.

Launched detached, with output to the log and stdin from `/dev/null`:

```sh
PR_REVIEW_NESTED=1 nohup claude -p "/code-review <effort> --comment <n>" \
  --settings '{"disableAllHooks": true}' \
  --permission-mode dontAsk --disallowedTools "<denied>" \
  --allowedTools "<allowed>" >"<log>" 2>&1 &
```

- Effort is `medium` unless `PR_REVIEW_EFFORT` says otherwise. `max` and
  `ultra` are refused: they are for a review someone asks for.
- There is no cost cap: the run uses the owner's Claude Code login, not
  metered API billing.
- The allowed tools are the read-only tools, the `gh` and `git` commands
  that read a pull request and a repository, a few text filters, and
  `gh api`, which the review uses to post its comments. No file-editing
  tool, `git push` or `git fetch` is allowed.
- `gh api` cannot be limited to comments by an allow rule, so a deny list
  refuses the calls that would do more: merge, the contents and git-data
  endpoints, and the `PUT`, `PATCH` and `DELETE` methods. This is a guard
  against a mistaken step, not a sandbox: the run still holds the owner's
  `gh` login.
- Comments are posted with the session owner's `gh` login, so they appear
  under that name.

### What the session sees

One line of context after the command, through the `PostToolUse` JSON
wrapper:

```
Code review: a review of pull request #<n> was started in the background. Findings will be posted on the pull request. Log: <path>
```

### Tests

`test/hook/pr_review_test.sh`, in the style of `pm_test.sh`, with stub `gh`,
`git`, `claude` and `nohup`:

- `gh pr create` launches one review with the expected arguments and prints
  the line; the same command again launches nothing;
- a compound command (`git push … && gh pr create …`) launches;
- `gh api -X POST repos/o/r/pulls` launches; `gh api` reading pulls, `gh pr
  view`, `gh pr list` and unrelated commands do not, and make no `gh` call;
- a command that only mentions `gh pr create` inside a quoted string or an
  `echo` does not launch a second review, because the lookup finds the
  already reviewed pull request;
- no open pull request for the branch, a failed lookup, a detached HEAD:
  nothing, exit 0;
- `PR_REVIEW_NESTED` set, `CLAUDE_CODE_REMOTE=true`, `claude` missing:
  nothing, and no `gh` call;
- `PR_REVIEW_EFFORT=high` is passed on; `max` and `ultra` fall back to
  `medium`;
- the state file cannot be written: nothing is launched.

## Left to the builder

- How the command check tells `gh pr create` and a `POST` to `/pulls` from
  look-alikes, within what a shell script can reasonably do.
- How the tests observe a detached launch without racing it.
- The wording of the line the session sees.
