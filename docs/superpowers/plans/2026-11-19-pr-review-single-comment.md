# Pull Request Review Single Comment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The review run only reads and reports; the hook posts its findings as one pull request comment.

**Architecture:** `pr_review_run` in `.claude/hooks/pr-review.sh` already wraps the detached `claude -p` run. It gains a second half: after a clean exit it builds the comment from the run's output and posts it with one fixed `gh api` call. The run loses `gh api`, `--comment` and the deny list.

**Tech Stack:** bash 3.2 or newer, `gh` (REST), the `claude` CLI in print mode, POSIX `awk`.

**Spec:** `docs/superpowers/specs/2026-11-19-pr-review-single-comment-design.md`, which amends `docs/superpowers/specs/2026-11-19-pr-review-hook-design.md`.

**Context:** Branch `feat/pr-review-single-comment`, cut from `main`. The hook and its suite (`test/hook/pr_review_test.sh`, 40 cases) are merged.

## Global Constraints

- Everything in the first plan's Global Constraints still holds (`docs/superpowers/plans/2026-11-19-pr-review-hook.md`): exit 0 always, no `set -e`, nothing started for a command that opened no pull request, stub-only PATH in tests, shellcheck `-x` clean, house comments, no private values.
- The hook may now also use `cat`, `head` and `wc` if it needs them; say which in its header.
- The hook is live in this clone. Do not open a pull request, and do not run the hook outside the harness.

## Review Focus

1. Findings whose last line has no newline, or that are only whitespace. Case `findings without a final newline`; whitespace-only counts as empty.
2. Findings with `"`, `\`, tabs and non-ASCII text: the posted JSON must decode to the same text. Case `findings are encoded`.
3. The cut landing inside a fenced code block. The cut line is preceded by a closing fence when the kept part has an odd number of fence lines. Case `cut inside a code block`.
4. `gh` failing only on the post, after a long review. The claim is released and the findings file is kept. Case `post fails`.
5. The session ending while the run is in flight. The wrapper already ignores SIGHUP; the post must happen inside the same wrapper. Case `the post comes from the detached run`.

---

### Task 1: the hook posts the findings

**Files:**
- Modify: `.claude/hooks/pr-review.sh`, `test/hook/pr_review_test.sh`

**Interfaces:**
- Consumes from `pm-issue.sh`: `pm_private_content <file>` (prints the offending line number, returns 0 when found), `pm_body_json` (stdin to `{"body":…}`).
- `pr_review_run <n> <slug> <effort> <claim> <state> <command…>`: runs the command with stdout to `<state>/review-<n>.md` and stderr to `<state>/review-<n>.log`, then calls `pr_review_post`. Releases the claim on a non-zero exit, on empty findings and on a failed post.
- `pr_review_post <n> <slug> <effort> <state>`: returns 0 when posted, 1 when the post failed, 2 when the findings were refused as private (claim kept).

Changes:

1. `pr_review_tools` becomes:

   ```
   Read,Grep,Glob,Agent,Skill,TodoWrite,Bash(gh pr view *),Bash(gh pr diff *),Bash(gh pr checks *),Bash(git diff *),Bash(git log *),Bash(git show *),Bash(git status *),Bash(git blame *),Bash(git rev-parse *),Bash(git merge-base *),Bash(git ls-files *),Bash(git cat-file *),Bash(git remote get-url *),Bash(git branch --show-current),Bash(head *),Bash(tail *),Bash(grep *),Bash(wc *),Bash(cat *),Bash(ls *)
   ```

   `pr_review_denied` and `--disallowedTools` are removed.
2. The prompt is `/code-review $effort $n`.
3. The comment body is exactly:

   ```
   <!-- pr-review -->
   **Automated review.** A separate Claude Code run reviewed this pull request when it was opened (`/code-review <effort>`). It has not seen the session that wrote the change.

   <findings>
   ```

   Findings are cut at the last whole line within 60,000 bytes. When cut, a closing ```` ``` ```` line is added if the kept part has an odd number of lines starting with ```` ``` ````, then an empty line and `The review was cut at 60,000 bytes; the rest is in the session's review log.`
4. Posted with `gh api -X POST "repos/$slug/issues/$n/comments" --input <file> >/dev/null`.
5. Log lines, appended to `review-<n>.log`, each starting `pr-review: `:
   - `the review run exited with status <s>; the claim was removed`
   - `the review printed no findings; the claim was removed`
   - `the findings were not posted: line <l> of <file> has a local path, a private address or a token`
   - `the comment could not be posted; the claim was removed`
   - `the findings were posted on pull request #<n>`
6. The session line ends `Findings will be posted on the pull request as one comment. Log: <log>`.

- [ ] **Step 1: Write the failing cases**

| Case | Arrange | Assert |
|---|---|---|
| `a finished review posts one comment` | `claude` stub prints three lines | one `POST` to `repos/example/dnsmasq-manager/issues/7/comments`; the sent body equals the expected JSON; claim kept; log ends with the posted line |
| `launch arguments` | as now | prompt `/code-review medium 7`; no `--comment`, no `--disallowedTools`; tools string has no `gh api` |
| `findings are encoded` | findings with `"`, `\`, a tab and `é` | sent body equals the hand-written JSON |
| `findings without a final newline` | stub prints with `printf '%s'` | posted, body ends with the last line |
| `empty findings` | stub prints nothing; again only spaces and newlines | no post; claim removed; log line |
| `failed run` | stub prints findings then exits 1 | no post; claim removed; log line |
| `post fails` | `gh` stub fails on `POST` | claim removed; `review-7.md` kept; log line |
| `private findings are not posted` | a line with `/Users/someone/x`; `192.168.1.10`; a `ghp_` token | no post; claim kept; log names the line |
| `findings are cut` | 2,000 lines of 40 characters | sent body within 65,000 bytes, ends with the cut line, last finding line whole |
| `cut inside a code block` | a fence opened before the cut point | a closing fence line before the cut line |
| `the post comes from the detached run` | no foreground seam; stub waits for a release file | no `gh` post before release; one after |

- [ ] **Step 2: Run and see them fail.** `bash test/hook/pr_review_test.sh`
- [ ] **Step 3: Change `.claude/hooks/pr-review.sh`.**
- [ ] **Step 4: Run.** `make test-hook`: three suites pass.
- [ ] **Step 5: Lint.** shellcheck `-x` on the hook scripts and both suites: no output.
- [ ] **Step 6: Commit** `feat(hooks): post review findings from the hook as one comment`

---

### Task 2: documentation

- [ ] **Step 1:** `README.md` and `CLAUDE.md`: findings arrive as one comment; the review run cannot write to GitHub or the repository; the findings file and log are in `.git/pr-review/`; findings that look private are not posted. In the first spec, add a line under "The review run" pointing at the amending spec.
- [ ] **Step 2: Commit** `docs: describe the single review comment`

---

### Task 3: pull request and trial

- [ ] **Step 1:** `make test-hook`, `make test`, `make lint`, shellcheck, `jq . .claude/settings.json`.
- [ ] **Step 2:** Push and open the pull request from this clone. The hook should start a review and post one comment when it finishes.
- [ ] **Step 3:** Read `.git/pr-review/review-<n>.log` and the comment. Confirm the run needed no tool it was denied, fix what the review found, and record the result in the amending spec.

---

## Changes made while building

- A deny list stays: `Edit,Write,NotebookEdit,Bash(gh api *),Bash(git push *),Bash(* --output*)`. It names the writing tools so an allow rule in another settings file cannot grant them, and `--output` because `git diff`, `git log` and `git show` accept it.
- The run prints a fenced `json` list of findings. Output without one is not posted. With `jq` on PATH the list is rendered as Markdown; otherwise it is posted as printed.
- The checkout's absolute path is removed from the body before the privacy check, and the body checked and posted is kept as `review-<n>.body.md`.
- The comment header names the commit reviewed.
- The suite's stubs are written once per run and linked into each case, which took it from about a minute to a few seconds on macOS.
- The JSON body sent is kept as `review-<n>.json` beside the findings and the log.
