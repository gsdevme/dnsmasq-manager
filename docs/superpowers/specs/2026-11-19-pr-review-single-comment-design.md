# Pull request review: the hook posts the findings

Date: 2026-11-19
Status: approved 2026-11-19

Changes one part of
[the pull request review hook](2026-11-19-pr-review-hook-design.md): how the
review's findings reach the pull request. Everything else in that design
stands.

## Intent

The review run posts its own findings as inline comments, so it has to be
allowed `gh api`, which can do far more than comment. A deny list was added
to refuse the worst of it, and that list is a guard, not a limit: it is a
set of patterns to keep right, and the run still acts with the owner's
login.

The owner would rather have a review run that cannot write to GitHub at
all, and accepts one comment in place of inline ones. So the run only reads
and reports, and the hook, which is a fixed script, posts what it reported.

## Goals

1. The review run has no tool that writes to GitHub or to the repository.
   A short deny list names the writing tools outright (`Edit`, `Write`,
   `NotebookEdit`, `gh api`, `git push`, and git's `--output` option), so
   that an allow rule in the owner's other settings files cannot hand one
   to the run: a deny rule wins over an allow rule.
2. When the run finishes, the hook posts its findings as one comment on the
   pull request.
3. Nothing private is posted: the findings go through the same check as a
   progress comment.
4. A failed run or a failed post leaves the pull request unclaimed and the
   reason in the log, as now.

## Non-goals

- Inline comments. Findings name their file and line in the text.
- Updating the comment later. One review, one comment.

## Approaches

**A. The run reads, the hook posts (chosen).** The run's tools are
read-only. Its output is the findings; the hook posts them with one fixed
REST call. Simple to reason about, and a wrong step by the model cannot
reach GitHub. Costs inline comments.

**B. Keep inline comments and the deny list.** What is merged today. Better
placed comments, but the run holds `gh api` and the deny list has to be
kept right.

**C. Give the run a narrow MCP tool that can only create review comments.**
Inline comments with a real limit, but it needs a tool server written,
tested and kept running for one job.

The owner picked A.

## Design

### The review run

```sh
claude -p "/code-review <effort> <n>" \
  --settings '{"disableAllHooks": true}' \
  --permission-mode dontAsk --disallowedTools "<denied>" \
  --allowedTools "<allowed>"
```

No `--comment`. The allowed tools are what the
merged hook allows, without `gh api` and `jq`: the read-only tools,
`gh pr view`, `gh pr diff`, `gh pr checks`, the read-only `git` commands
and the text filters. In `dontAsk` mode anything not on the list is denied,
so the file-editing tools need no rule.

The run's standard output goes to `review-<n>.md` in the state directory,
its standard error to `review-<n>.log`.

### Posting

In print mode without `--comment`, the review prints a fenced `json` block
holding a list of findings (`file`, `line`, `summary`, `failure_scenario`),
sometimes followed by notes. The hook posts only output that has such a
block: anything else, such as a message that a tool was denied, is not a
review, is not posted, and stays in `review-<n>.md` with the claim kept.

When `jq` is on PATH the list is rendered as Markdown: a count, then a
numbered section per finding with its summary, `file:line` and failure
scenario, then the notes under a rule. An empty list gives `No findings.`
Without `jq`, or when the block is not the expected shape, the output is
posted as it was printed.

The comment is:

```
<!-- pr-review -->
**Automated review.** A separate Claude Code run reviewed this pull request at commit <sha> (`/code-review <effort>`). It has not seen the session that wrote the change.

<the findings>
```

The commit is the checkout's HEAD when the review was launched.

- The findings are cut at the last whole line within 60,000 bytes, with a
  line saying so, to stay under GitHub's comment limit.
- The checkout's own absolute path is removed from the body first, so a
  finding that names a file by its full path becomes repo-relative and does
  not cost the whole review.
- If `pm_private_content` then finds a local path, a private address or a
  token in the body, nothing is posted. The log says which line of
  `review-<n>.body.md`, and the claim is kept: running the review again
  would likely repeat it.
- Otherwise it is posted with
  `gh api -X POST "repos/<slug>/issues/<n>/comments" --input <file>`, the
  body encoded by `pm_body_json`.

The claim is removed, with a line in the log, when the run exits non-zero,
when its output is empty, or when the post fails.

### What the session sees

Unchanged, except that it says the findings will be posted as one comment.

### Tests

Added to `test/hook/pr_review_test.sh`, with the `claude` stub printing
findings and the `gh` stub recording the post:

- a finished review posts one comment with the header, the findings and the
  expected JSON body; the launch arguments have no `--comment`, the one
  deny rule, and a tool list with no `gh api`;
- findings over the limit are cut on a whole line with the cut line;
- findings with a home path, a private address or a token are not posted,
  the claim is kept and the log names the line;
- a failed run, empty findings and a failed post each release the claim,
  post nothing more, and leave a line in the log;
- the post is made from the detached run, not before the hook returns.

## Left to the builder

- The wording of the log lines.
- How the wrapper is split into functions.

## Settled by the trial

The pull request for this change (#15) was opened from a clone with this
hook live, on 2026-11-19. The review ran with the read-only tools and the
hook posted one comment. It showed that the run prints a JSON list, which
was posted raw and read badly; the rendering, the check that the output is
a findings list, the path rewrite before the privacy check, the named
deny list and the commit in the header all came from that review.

Left as they are: a failed post is not retried, though the body stays on
disk and the log names it; and a post that GitHub accepted but `gh` reported
as failed could lead to a second comment after a later trigger.
