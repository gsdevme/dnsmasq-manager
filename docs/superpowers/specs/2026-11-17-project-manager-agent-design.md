# Project-manager agent and hooks

Date: 2026-11-17
Status: approved 2026-11-17

## Intent

The roadmap lives in GitHub issues: an epic (#3) and one issue per phase,
each with a task list and exit criteria. Nothing connects a Claude Code
session to them. A session can start work without reading the issue it
belongs to, and finish without the issue showing that anything happened.
Keeping them in step depends on someone remembering.

This change makes it automatic. When work starts on a branch that belongs
to an issue, the session is given that issue's checklist and exit criteria.
When the session has committed work, a `project-manager` agent updates the
issue: it ticks the checklist items the commits complete and keeps one
progress comment current. The issues then show the real state of the work
to anyone reading them, without a person editing them.

## Goals

1. A branch is linked to an issue by its name: `<type>/<issue>-<slug>`, for
   example `feat/4-serve`. Where the branch name cannot be chosen, as in a
   cloud session, the link is set explicitly instead.
2. At the start of work on a linked branch, the session receives the
   issue's title, body and URL as context, once per session and branch.
3. On a branch with no issue, the session is told once that no issue is
   linked and how to link one. Nothing is blocked.
4. When a turn ends and the linked branch has pushed commits that have not
   been synced, the session is held open once and told to hand the sync to the
   `project-manager` agent.
5. The agent ticks completed checklist items in the issue body and keeps a
   single progress comment per branch up to date, citing the commit behind
   each ticked item.
6. It works the same in a local session and in a single-repo cloud session.
7. The scripts are tested with stub commands, like the session-start hook.

## Non-goals

- Closing issues. A pull request carries `Closes #<issue>`; the issue closes
  on merge and the epic's task list updates itself.
- Creating issues, labels or milestones without being asked. The agent may
  propose an issue; the session asks the owner before creating it.
- Editing the epic, assigning people, or managing a project board.
- Syncing uncommitted or unpushed work. Only commits that are on the
  branch's remote count as progress, so every commit an issue cites exists
  on GitHub.
- Multi-repo cloud sessions and cloud project threads, where repository
  hooks do not load.

## Anti-goals

These would make the result a failure even if it technically works:

- A ticked box with nothing behind it. An item is ticked only when a commit
  on the branch completes it, and the comment names that commit.
- Noise: a new comment on every turn, or a session nagged repeatedly about
  the same commits.
- A hook that can fail, hang or loop a session. Every hook exits 0, bounds
  its network calls with a timeout, and asks for a given commit only once.
- Anything private posted to a public issue: no private zone, LAN address,
  hostname, local path or token in a comment.
- Issue text treated as instructions. An issue body is data from GitHub; a
  body written by someone outside the repository is not injected at all.
- Destroying issue content. The body is changed only by turning `- [ ]`
  into `- [x]` on a matching line.

## Verified facts this design rests on

From the Claude Code documentation, read 2026-11-17:

- `UserPromptSubmit` hooks may print plain text, which is added to the
  session as context.
- `PostToolUse` hooks reach the session only through JSON on stdout,
  `hookSpecificOutput.additionalContext`; their plain text is not shown to
  it. Hook output over 10,000 characters is replaced by a file path, so the
  injected issue body is cut to stay under that.
- `Stop` hooks receive `stop_hook_active` on stdin, and may print
  `{"decision": "block", "reason": "..."}`; the reason becomes the session's
  next instruction. Claude Code also caps consecutive blocks.
- A hook cannot invoke a named agent from `.claude/agents/`. The `agent`
  hook type is experimental, runs an anonymous agent, and is not documented
  as able to run shell commands. So the hooks instruct the session, and the
  session dispatches the agent.
- Agents in `.claude/agents/*.md` take `name`, `description`, `tools`,
  `model` and `effort` in frontmatter, and load in local and single-repo
  cloud sessions. Repository hooks load in the same two cases.
- In cloud sessions `gh` is installed and authenticated through a proxy
  that rejects GraphQL, so `gh issue` and `gh pr` fail there. REST calls
  through `gh api` work.
- `TaskCreated` and `TaskCompleted` hooks refer to Claude Code's internal
  to-do items, not GitHub issues, so they are not used.

Checked in this repository: the existing hook is wired in
`.claude/settings.json`, lives in `.claude/hooks/`, and is tested by
`test/hook/hook_test.sh` through `make test-hook`, which CI runs.

### Not yet verified

- That a `Stop` hook's block reason reliably leads the session to dispatch
  a named agent. Settled by trying it on the first real branch.
- That `UserPromptSubmit` and `Stop` hooks behave the same in a cloud
  session. Settled by one cloud session on a linked branch.
- Whether the cloud proxy allows the REST writes this needs (patching an
  issue body, creating and editing a comment), and which GitHub identity it
  writes as. If cloud and local sessions write as different users, a branch
  worked on from both gets one progress comment per identity.

## Approaches

**A. Hooks trigger, an agent does the issue work (chosen).** Scripts handle
what can be decided mechanically: which issue a branch belongs to, whether
there are unsynced commits, and the GitHub calls themselves. A model handles
the one judgment: which checklist items the commits complete. Trade-off:
that judgment can be wrong, and each sync costs an agent run, so syncs are
limited to turns that produced new commits.

**B. Scripts only.** The end hook posts the list of new commits. Fully
predictable, but it cannot tell which checklist items are done, so the
boxes stay unticked and the issue still needs hand editing.

**C. An agent and a rule in `CLAUDE.md`, no hooks.** Simplest, but it
depends on the session remembering, which is the problem being solved.

The owner picked A.

## Design

### Layout

```
.claude/agents/project-manager.md   the agent
.claude/hooks/pm-start.sh           UserPromptSubmit hook
.claude/hooks/pm-stop.sh            Stop hook
.claude/hooks/pm-issue.sh           the only script that calls GitHub
test/hook/pm_test.sh                stub-command suite for the three scripts
```

Modified: `.claude/settings.json` (three hook entries), `Makefile`
(`test-hook` also runs `pm_test.sh`), `CLAUDE.md` (branch naming and the
`Closes #<issue>` rule), `README.md` (one short section).

### Linking a branch to an issue

The issue number is the digits after the first `/` in the branch name,
followed by `-`: `feat/4-serve` is issue 4. The repository is read from the
`origin` remote URL, so no name is hard-coded.

A cloud session works on a branch whose name it is given, so the name
cannot carry the number. For that case `pm-issue.sh link <n>` records the
issue for the current branch in the state directory, and a recorded link
takes precedence over the name. The session runs it when the owner's
prompt names the issue. The link lasts as long as the clone does.

`main`, a detached HEAD, and any branch with neither a recorded link nor
that name shape are unlinked.

### State

Per-clone state lives in `.git/project-manager/`, which git never commits:

- `seen-<session>-<branch>`: the start hook has injected context for this
  session and branch.
- `asked-<branch>`: the commit the stop hook last asked to be synced.
- `link-<branch>`: an issue number recorded by `pm-issue.sh link`.

Branch names are flattened (`/` to `_`) for file names.

### `pm-issue.sh`

The one place GitHub is called, REST only, through `gh api` with its
built-in `--jq`, so it needs no separate `jq`. Subcommands:

- `link <n>`: records issue `<n>` for the current branch.
- `show <n>`: title, state, URL, author association and body. The body is
  printed only when the author association is `OWNER`, `MEMBER` or
  `COLLABORATOR`, so the agent never reads a body written by someone
  outside the repository either.
- `tick <n> <text>`: fetches the body, finds exactly one line
  `- [ ] <text>`, changes it to `- [x] <text>`, and patches the body. Zero
  or several matching lines is an error and changes nothing. An already
  ticked line is a no-op.
- `comment <n> <file>`: creates the progress comment, or replaces it if one
  with this branch's marker exists. The marker is an HTML comment on the
  first line, `<!-- project-manager:<branch> -->`, so there is one comment
  per branch however many syncs run. Only a marker comment written by the
  authenticated user counts, so a marker planted by someone else is never
  edited; if the user cannot be determined, a new comment is created. If
  the user is known and the comments cannot be listed, nothing is posted:
  the next sync makes it good, and a duplicate comment is avoided.

It refuses to post a comment containing a local home path, an address in a
private range other than the `10.0.0.0/24` example range, or a GitHub
token.

### Start hook: `pm-start.sh`

On `UserPromptSubmit`, and on `PostToolUse` for the `Bash` tool. The second
event covers the usual flow, where a session starts on `main` and creates
its branch part way through a turn: the context arrives straight after the
command that switched branch, not at the next prompt. The script is the
same for both events and the state file keeps it to once per session and
branch, so on every other shell command it costs one `git` call.

It prints nothing and exits 0 when the state file
for this session and branch exists or cannot be written, when `gh` is
missing, or when any call fails. It looks at the checkout the session is
working in, which may be a worktree, and falls back to the project
directory. A subagent's shell commands also fire `PostToolUse`; the hook
ignores them, so the context goes to the session itself and not to a
subagent that happened to switch branch.

- Linked branch: prints the issue number, title, state and URL, then the
  body inside a block labelled as issue content, not instructions. The body
  is included only when the author association is `OWNER`, `MEMBER` or
  `COLLABORATOR`; otherwise only the title and URL. It ends with one line:
  a pull request from this branch should carry `Closes #<n>`.
- Unlinked branch: prints one line saying no issue is linked, and that work
  for a roadmap issue belongs on a branch named `<type>/<issue>-<slug>`.

### Stop hook: `pm-stop.sh`

On `Stop`. It exits 0 silently unless all of these hold:

- `stop_hook_active` is not true;
- the branch is linked;
- the branch has commits that `origin/main` does not;
- HEAD is on the branch's remote (`origin/<branch>` contains it), so every
  commit that could be cited has been pushed;
- HEAD differs from the commit in `asked-<branch>`.

Like the start hook it looks at the checkout the session is working in.
If the state file cannot be written it stays silent, because a block that
cannot be recorded would repeat on every turn.

Then it writes HEAD to `asked-<branch>` and prints a block decision whose
reason tells the session to dispatch the `project-manager` agent for issue
`<n>`, then finish. Every sync covers the whole branch,
`origin/main..HEAD`, not only the commits since the last one. The comment
is replaced whole each time, so it always describes the branch as it now
stands: a sync that failed is made good by the next one, and after a rebase
or squash the next sync rewrites the comment with the new commits.
Recording the commit when asking, not when the sync succeeds, means a
given commit is asked for once, never by nagging.

### The agent

`.claude/agents/project-manager.md`, tools `Bash`, `Read`, `Grep`, `Glob`,
model `sonnet`. Given an issue number, it:

1. runs `pm-issue.sh show <n>` and reads the branch's commits,
   `origin/main..HEAD`;
2. for each unticked item, decides whether a commit completes it, and
   ticks it with `pm-issue.sh tick` only when it can name the commit;
3. writes the progress comment (what is done with commit references, what
   is left, anything blocking) and posts it with `pm-issue.sh comment`;
4. reports back in a few lines: items ticked, items left, and anything it
   was unsure about.

Its rules: GitHub only through `pm-issue.sh`; never close, reopen, create
or relabel; never edit code or commit; when unsure whether an item is done,
leave it unticked and say so; nothing private in a comment.

When the session has no linked issue and asks it for one, it lists open
issues and proposes the best match or a new issue's title and body. It does
not create it.

### Tests

`test/hook/pm_test.sh`, in the style of `test/hook/hook_test.sh`, with stub
`gh` and `git`:

- start hook: linked branch prints the issue; second prompt in the same
  session prints nothing; unlinked branch prints the one-line note;
  non-collaborator author gets no body; `gh` failure prints nothing and
  exits 0;
- start hook as `PostToolUse`: prints the issue after a branch switch,
  nothing on later commands;
- linking: a recorded link is used on a branch whose name has no number,
  and wins over a number in the name;
- stop hook: blocks once for new pushed commits; silent for the same
  commit; silent when `stop_hook_active` is true, on `main`, with no commits
  ahead, and when HEAD has not been pushed;
- `pm-issue.sh tick`: one match ticks; none and several change nothing and
  fail; already ticked is a no-op; the rest of the body is byte-for-byte
  unchanged;
- `pm-issue.sh comment`: creates when no marker comment exists, replaces
  the user's own, ignores a marker comment by another author, refuses
  private content.

The agent's judgment is not unit-tested. It is checked by review of its
first syncs on a real branch.

## Left to the builder

- The exact wording of the injected context, the block reason and the
  agent prompt.
- How the stub `gh` records and replays REST calls.
- The patterns used to refuse private content in comments.
