# Pull Request Review Hook Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When a Claude Code session opens a pull request, start a fresh-context `/code-review` of it in the background that posts its findings on the pull request.

**Architecture:** One bash `PostToolUse` hook, `.claude/hooks/pr-review.sh`. It checks that the shell command opened a pull request, asks GitHub for the open pull request on the current branch, claims it with a state file, and launches a detached `claude -p` run with this repository's hooks turned off and a fixed list of allowed tools.

**Tech Stack:** bash 3.2 or newer, `git`, `gh` (REST through `gh api`), the `claude` CLI in print mode, POSIX `sed`/`grep`/`awk`, GNU Make, shellcheck.

**Spec:** `docs/superpowers/specs/2026-11-19-pr-review-hook-design.md`.

**Context:** Branch `feat/pr-review-hook`, cut from `main`. `.claude/hooks/pm-start.sh` and `test/hook/pm_test.sh` are the pattern: how a hook reads stdin, picks its checkout, sources `pm-issue.sh`, wraps `PostToolUse` output, and how the stub suite is built.

## Global Constraints

- The hook exits 0 whatever happens, has no `set -e`, and prints nothing unless it launched a review.
- It makes no `gh` call and starts no process for a command that did not open a pull request.
- GitHub only through `gh api` (REST).
- It uses bash builtins and only: `git`, `gh`, `claude`, `nohup`, `awk`, `sed`, `grep`, `tr`, `cat`, `mkdir`. The tests run it with a PATH that holds nothing else.
- State lives in `<git-common-dir>/pr-review/`: `reviewed-<n>` and `review-<n>.log`.
- No cost cap on the review run. No `max` or `ultra` effort.
- No private value in any committed file: tests use the slug `example/dnsmasq-manager`.
- `shellcheck --shell=bash` clean. A header comment and a comment above each function; no narrating comments.
- Commits: Conventional Commits, never `chore`, subject at most 72 characters, ending with the session's `Co-Authored-By` trailer.
- TDD: failing cases first.
- The hook goes live in this clone as soon as `.claude/settings.json` changes. Do not run `gh pr create`, or any `gh api` call that creates a pull request, while implementing.

## Review Focus

1. A command that mentions `gh pr create` without running it (`echo`, a commit message, `grep`). It may reach the lookup, but must never launch a second review of a reviewed pull request. Case `look-alike after a review`.
2. `gh pr create` inside a compound command or after `VAR=value` assignments. Case `compound command`.
3. Two hook runs for one pull request at the same moment. Case `second claim loses`.
4. The nested run's own shell commands reaching this hook. Case `nested run is ignored`.
5. A branch whose name needs URL encoding in the lookup (`feat/a+b`, `feat/a#b`). Case `branch name is encoded`.

---

### Task 1: the hook

**Files:**
- Create: `.claude/hooks/pr-review.sh` (mode 755), `test/hook/pr_review_test.sh` (mode 755)
- Modify: `.claude/settings.json`, `Makefile`

**Interfaces:**
- Consumes from `.claude/hooks/pm-issue.sh` (sourced as `pm-start.sh` does): `pm_repo_slug`, `pm_branch`, `pm_json_string`.

Behaviour, in this order:

1. Exit when `PR_REVIEW_NESTED` is non-empty or `CLAUDE_CODE_REMOTE` is `true`.
2. Read stdin, then `exec </dev/null`. Take the command: the value of the first `"command"` string in the payload, left JSON-escaped.
3. Exit unless the command opened a pull request: it holds the words `gh pr create`, or it holds `gh api`, a path ending in `/pulls` (then a quote, space or end), and one of `-X POST`, `--method POST`, `-f`, `-F`, `--field`, `--raw-field`, `--input`. This check is loose on purpose; steps 5 and 6 make a false match harmless.
4. Exit when `claude` or `gh` is not on PATH. Work in the current directory when it is inside a git repository, otherwise in `CLAUDE_PROJECT_DIR` (as `pm-start.sh` does).
5. Find the pull request: with `<owner>` the first part of the slug and the branch percent-encoded,

   ```bash
   gh api "repos/$slug/pulls?head=$owner:$branch&state=open&per_page=1" --jq '.[0].number // empty'
   ```

   Exit unless the result matches `^[1-9][0-9]*$`.
6. Claim it: create `<state>/reviewed-<n>` with noclobber. Exit if that fails.
7. Launch, from `pr_review_launch <n> <log>`:

   ```bash
   PR_REVIEW_NESTED=1 nohup claude -p "/code-review $effort --comment $n" \
   	--settings '{"disableAllHooks": true}' \
   	--permission-mode dontAsk \
   	--allowedTools "$pr_review_tools" >"$log" 2>&1 </dev/null &
   ```

   Without `nohup` on PATH the same command runs without it. With `PR_REVIEW_FOREGROUND=1` (a test seam) it runs in the foreground. `$effort` is `PR_REVIEW_EFFORT` when that is `low`, `medium`, `high` or `xhigh`, else `medium`.

   `pr_review_tools` is one comma-separated string, defined once at the top of the script:

   ```
   Read,Grep,Glob,Agent,Skill,TodoWrite,Bash(gh pr view *),Bash(gh pr diff *),Bash(gh pr checks *),Bash(gh api *),Bash(git diff *),Bash(git log *),Bash(git show *),Bash(git status *),Bash(git blame *),Bash(git fetch *),Bash(git rev-parse *),Bash(git merge-base *),Bash(git ls-files *),Bash(git cat-file *)
   ```

8. Print, through the `PostToolUse` wrapper `{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":<pm_json_string>}}`:

   ```
   Code review: a review of pull request #<n> was started in the background. Findings will be posted on the pull request. Log: <log>
   ```

`.claude/settings.json`: the existing `PostToolUse` entry (matcher `Bash`) gains two handlers after the `pm-start.sh` one:

```json
{ "type": "command", "if": "Bash(gh pr create*)", "command": "bash \"$CLAUDE_PROJECT_DIR\"/.claude/hooks/pr-review.sh", "timeout": 20 },
{ "type": "command", "if": "Bash(gh api *)", "command": "bash \"$CLAUDE_PROJECT_DIR\"/.claude/hooks/pr-review.sh", "timeout": 20 }
```

`Makefile`: `test-hook` also runs `bash test/hook/pr_review_test.sh`.

**Harness:** self-contained, the shape of `pm_test.sh`: `new_case`, `stub`, `problem`, `finish_case`. Stubs: `git` (answers from files, as in `pm_test.sh`), `gh` (logs; prints `$tmp/gh/pulls` when it exists, else exits 1), `claude` (logs `claude <args>` and the value of `PR_REVIEW_NESTED`), `nohup` (logs, then `exec "$@"`). `run_review <command>` builds the payload `{"session_id":"s","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":<JSON string>},"tool_response":{"stdout":"","stderr":""}}` and runs the hook with `PR_REVIEW_FOREGROUND=1`.

- [ ] **Step 1: Write the harness and the failing cases**

| Case | Arrange | Assert |
|---|---|---|
| `gh pr create launches a review` | pulls fixture `7` | one `claude` call with `-p`, `/code-review medium --comment 7`, the `--settings` JSON, `--permission-mode dontAsk` and the tools string; `PR_REVIEW_NESTED=1` in its environment; stdout is exactly the wrapper line for #7; `reviewed-7` exists |
| `the same command again does nothing` | run twice | one `claude` call; second stdout empty |
| `compound command` | `FOO=1 git push -u origin x && gh pr create --fill` | launches |
| `REST create launches` | `gh api -X POST repos/example/dnsmasq-manager/pulls -f title=x -f head=y -f base=main`; again with `--method POST` and with `--input f` | launches each time (fresh case each) |
| `reads and other commands do nothing` | `gh api repos/example/dnsmasq-manager/pulls`, `gh pr view 7`, `gh pr list`, `gh api -X POST repos/example/dnsmasq-manager/pulls/7/comments -f body=x`, `git status` | no `gh` call, no `claude` call, stdout empty |
| `look-alike after a review` | after a review of #7, `echo "gh pr create"` | no second `claude` call |
| `no open pull request` | fixture empty | no `claude` call, no state file |
| `lookup fails` | no fixture | no `claude` call, exit 0 |
| `a non-number from the lookup` | fixture `7; rm -rf x` | no `claude` call |
| `detached HEAD` | no branch | no `gh` call |
| `nested run is ignored` | `PR_REVIEW_NESTED=1` | no `gh` call, no `claude` call |
| `cloud session is ignored` | `CLAUDE_CODE_REMOTE=true` | no `gh` call |
| `claude missing` | remove the stub | no `gh` call, stdout empty |
| `effort from the environment` | `PR_REVIEW_EFFORT=high`; then `max`; then `ultra`; then `x; y` | `high` passed on; the other three give `medium` |
| `second claim loses` | `reviewed-7` already exists | no `claude` call, stdout empty |
| `state cannot be written` | a file where the state directory should be | no `claude` call, exit 0 |
| `branch name is encoded` | branch `feat/a+b#c` | the `gh` call has `head=example:feat/a%2Bb%23c` |
| `works without nohup` | remove the `nohup` stub | launches |
| `detached launch returns` | without `PR_REVIEW_FOREGROUND`, a `claude` stub that waits for a file the test creates afterwards | the hook returns and prints the line before the stub finishes; the stub's log line appears after the test releases it |

- [ ] **Step 2: Run and see them fail.** `bash test/hook/pr_review_test.sh`
- [ ] **Step 3: Write `.claude/hooks/pr-review.sh`**, add the settings handlers and the Makefile line.
- [ ] **Step 4: Run.** `make test-hook`: three suites print `all cases passed`. `jq . .claude/settings.json` parses.
- [ ] **Step 5: Lint.** `docker run --rm -v "$PWD:/mnt" koalaman/shellcheck:stable --shell=bash -x .claude/hooks/pr-review.sh test/hook/pr_review_test.sh`: no output.
- [ ] **Step 6: Commit**

```bash
git add .claude/hooks/pr-review.sh test/hook/pr_review_test.sh .claude/settings.json Makefile
git commit -m "feat(hooks): review each pull request a session opens"
```

---

### Task 2: documentation

**Files:** Modify `CLAUDE.md`, `README.md`

- [ ] **Step 1: `CLAUDE.md`.** In `## Issues`, or a new `## Pull requests` section after it: opening a pull request from a local session starts a background `/code-review` that comments on it; read its findings before merging; the log is in `.git/pr-review/`. Update the `make test-hook` line.
- [ ] **Step 2: `README.md`.** A short section, `## Automatic review of pull requests`: what triggers it, what the review may do, the two environment variables (`PR_REVIEW_EFFORT`, and `PR_REVIEW_NESTED` as internal), what it does not cover, and how to review again by hand. Link the spec and this plan under Documents; update the `make test-hook` row.
- [ ] **Step 3: Commit** `docs: document the pull request review hook`

---

### Task 3: review, pull request, trial

- [ ] **Step 1: Verify.** `make test-hook`, `make test`, `make lint`, shellcheck, `jq . .claude/settings.json`.
- [ ] **Step 2: Review** the branch against the spec and this plan; squash fixes into their commits.
- [ ] **Step 3: Push and open the pull request with `gh pr create`.** This is the trial: the hook should launch a review of it.
- [ ] **Step 4: Read `.git/pr-review/review-<n>.log` and the pull request.** Confirm the review ran, posted its comments, and was denied no tool it needed. Correct `pr_review_tools` from what the log shows, squash, push, and record the result in the spec's "Not yet verified" list.

---

## Changes made after the trial

The pull request for this change was the trial (Task 3). The hook launched a
review of it, and the review's findings changed these points. The spec has
the resulting behaviour; the task text above is as first written.

- The pull request number comes from the last `https://github.com/<owner>/<repo>/pull/<n>` URL in the command's output (the part of the payload after `"tool_response"`), when its repository is the origin's. The branch lookup is the fallback; a URL for another repository means nothing is reviewed.
- A `gh api` command with an explicit `GET` method is never a create.
- The review runs through `pr_review_run`: when it exits non-zero, `reviewed-<n>` is removed and the log says so. The hook therefore also uses `rm`.
- The two settings handlers end in `pr-review.sh create` and `pr-review.sh api`; the script ignores its arguments.
- The allowed tools lost `git fetch` and gained `git remote get-url`, `git branch --show-current` and the filters `head`, `tail`, `grep`, `wc`, `jq`, `cat`, `ls`. A deny list (`--disallowedTools`) refuses the editing tools, merge, the contents and git-data endpoints, the `PUT`, `PATCH` and `DELETE` methods, and the `--output`, `--upload-pack` and `--exec` options.
- `pm_claim <file>` and `pm_state_dir [name]` in `pm-issue.sh` replace the hook's own copies and `pm_start_claim`.
