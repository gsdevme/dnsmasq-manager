#!/usr/bin/env bash
#
# Tests for .claude/hooks/pr-review.sh, the PostToolUse hook that starts a
# background code review of a pull request a session's command opened.
#
# Each case runs the hook in a clean environment (env -i) whose PATH is a
# per-case temp bin/ directory and nothing else. That directory holds symlinks
# to the basic commands the hook may use (awk, mkdir, rm) and to stubs for
# git, gh, claude and nohup, so no real repository, GitHub or Claude can be
# reached. jq, which the hook uses when it is there, is linked in only by the
# cases that render findings, and those cases print SKIP when the machine has
# no jq. The stubs are written once per run into a shared directory and use
# bash builtins and, by absolute path, cat and sleep; each finds its case's
# temp dir from the path it was run by (<case>/bin/<name>). Writing them once
# matters on macOS, where the first run of each newly written executable file
# costs a few hundred milliseconds.
#
# The git stub answers from files in the case's git/ directory. The gh stub
# answers the lookup GET with the gh/pulls fixture, or exits 1 when there is
# none; it accepts a POST, appending the endpoint to gh/posts and copying the
# --input file to gh/sent, unless gh/post-fails exists. The claude stub, when
# claude-wait exists, first waits up to five seconds for release to exist;
# then it logs its call, writes its arguments one per line to claude.args,
# logs its PR_REVIEW_NESTED and LC_ALL, prints the findings file to stdout and
# a line to stderr, and exits with the status in claude-status (default 0).
# The nohup stub runs its arguments. Every stub appends "<name> <args>" to the
# case's call log. Cases run the hook with PR_REVIEW_FOREGROUND=1, so the
# review run and the post have finished when the hook returns, except the
# cases that check the detached launch.
#
# Needs only bash (3.2 or newer) and a POSIX userland. Run it with
# `make test-hook`. Prints PASS/FAIL/SKIP per case and exits non-zero if any
# fail.

root=$(cd "$(dirname "$0")/../.." && pwd)
pr_review="$root/.claude/hooks/pr-review.sh"
tools='Read,Grep,Glob,Agent,Skill,TodoWrite,Bash(gh pr view *),Bash(gh pr diff *),Bash(gh pr checks *),Bash(git diff *),Bash(git log *),Bash(git show *),Bash(git status *),Bash(git blame *),Bash(git rev-parse *),Bash(git merge-base *),Bash(git ls-files *),Bash(git cat-file *),Bash(git remote get-url *),Bash(git branch --show-current),Bash(head *),Bash(tail *),Bash(grep *),Bash(wc *),Bash(cat *),Bash(ls *)'
denied='Edit,Write,NotebookEdit,Bash(gh api *),Bash(git push *),Bash(* --output*)'
head_sha=0123456789abcdef0123456789abcdef01234567
empty_line='pr-review: the review printed no findings; the claim was removed'
fence=$(printf '\140\140\140')
default_findings="${fence}json"'
[{"file":"main.go","line":3,"summary":"Returns before closing the file.","failure_scenario":"A read error leaves the file open."}]
'"$fence"
default_findings_json="${fence}"'json\n[{\"file\":\"main.go\",\"line\":3,\"summary\":\"Returns before closing the file.\",\"failure_scenario\":\"A read error leaves the file open.\"}]\n'"${fence}"
sample_findings="${fence}json"'
[
  {
    "file": "cmd/serve.go",
    "line": 42,
    "summary": "The listener is never closed.",
    "failure_scenario": "A reload opens a second listener on the same port and fails."
  },
  {
    "file": "README.md",
    "line": null,
    "summary": "The install step names the wrong target.",
    "failure_scenario": "A reader runs make install, which does not exist."
  }
]
'"$fence"'

Notes for the caller:

- **Scope:** the diff against main.
'
# The backticks are literal Markdown in the expected rendering.
# shellcheck disable=SC2016
sample_markdown='**2 findings.**

### 1. The listener is never closed.

`cmd/serve.go:42`

A reload opens a second listener on the same port and fails.

### 2. The install step names the wrong target.

`README.md`

A reader runs make install, which does not exist.

---

Notes for the caller:

- **Scope:** the diff against main.'
# shellcheck disable=SC2016
sample_markdown_json='**2 findings.**\n\n### 1. The listener is never closed.\n\n`cmd/serve.go:42`\n\nA reload opens a second listener on the same port and fails.\n\n### 2. The install step names the wrong target.\n\n`README.md`\n\nA reader runs make install, which does not exist.\n\n---\n\nNotes for the caller:\n\n- **Scope:** the diff against main.'
sleep_cmd=$(command -v sleep)
cat_cmd=$(command -v cat)
jq_cmd=$(command -v jq)
failures=0
skips=0

# Writes the executable <dir>/<name>: a stub that finds its case dir c from
# the path it was run by, appends "<name> <args>" to $c/calls.log (except
# claude, which logs its own call), then runs its body, read from stdin:
# shell code that may use $c, $cat_cmd and $sleep_cmd.
# The single-quoted $ expressions are stub code, expanded when the stub runs.
# shellcheck disable=SC2016
write_stub() {
	{
		printf '#!%s\n' "$BASH"
		printf "cat_cmd='%s'\n" "$cat_cmd"
		printf "sleep_cmd='%s'\n" "$sleep_cmd"
		printf '%s\n' 'c=${0%/bin/*}'
		[ "$2" = claude ] || printf 'printf "%%s\\n" "%s $*" >>"$c/calls.log"\n' "$2"
		cat
	} >"$1/$2"
	chmod +x "$1/$2"
}

# Writes the shared stubs into <dir>. The git stub answers the git calls the
# hook makes from files in the case's git/ directory (origin, branch, head,
# toplevel) and exits 1 for any other call; the others are as the file
# header describes.
write_stubs() {
	write_stub "$1" git <<'EOF'
case "$*" in
"remote get-url origin")
	[ -f "$c/git/origin" ] || exit 1
	printf '%s\n' "$(<"$c/git/origin")"
	;;
"symbolic-ref --quiet --short HEAD")
	[ -f "$c/git/branch" ] || exit 1
	printf '%s\n' "$(<"$c/git/branch")"
	;;
"rev-parse --git-common-dir")
	printf '%s\n' "$c/project/.git"
	;;
"rev-parse HEAD")
	[ -f "$c/git/head" ] || exit 1
	printf '%s\n' "$(<"$c/git/head")"
	;;
"rev-parse --show-toplevel")
	[ -f "$c/git/toplevel" ] || exit 1
	printf '%s\n' "$(<"$c/git/toplevel")"
	;;
*)
	exit 1
	;;
esac
EOF
	write_stub "$1" gh <<'EOF'
method=GET endpoint="" input=""
shift
while [ $# -gt 0 ]; do
	case $1 in
	-X) method=$2; shift ;;
	--input) input=$2; shift ;;
	--jq) shift ;;
	*) endpoint=$1 ;;
	esac
	shift
done
if [ "$method" = POST ]; then
	[ -e "$c/gh/post-fails" ] && exit 1
	printf '%s\n' "$endpoint" >>"$c/gh/posts"
	"$cat_cmd" "$input" >"$c/gh/sent" || exit 1
	echo '{}'
	exit 0
fi
[ -f "$c/gh/pulls" ] || exit 1
printf '%s\n' "$(<"$c/gh/pulls")"
EOF
	write_stub "$1" claude <<'EOF'
if [ -e "$c/claude-wait" ]; then
	i=0
	while [ ! -e "$c/release" ] && [ "$i" -lt 50 ]; do
		"$sleep_cmd" 0.1
		i=$((i + 1))
	done
fi
printf '%s\n' "claude $*" >>"$c/calls.log"
printf '%s\n' "$@" >"$c/claude.args"
printf 'nested=%s lc_all=%s\n' "${PR_REVIEW_NESTED-unset}" "${LC_ALL-unset}" >>"$c/calls.log"
[ -f "$c/findings" ] && "$cat_cmd" "$c/findings"
echo "claude stderr" >&2
status=0
[ -f "$c/claude-status" ] && status=$(<"$c/claude-status")
exit "$status"
EOF
	# The $@ is meant to expand in the stub.
	# shellcheck disable=SC2016
	printf '%s\n' 'exec "$@"' | write_stub "$1" nohup
}

tmp_root=${TMPDIR:-/tmp}
stubs=$(mktemp -d "${tmp_root%/}/pr-review-stubs.XXXXXX")
trap 'rm -rf "$stubs"' EXIT
write_stubs "$stubs"

# Creates the temp tree for a case: bin/ with links to the allowed commands
# and the stubs, project/.git, git/ and gh/, and an empty call log. The git
# state defaults to origin git@github.com:example/dnsmasq-manager.git on
# branch feat/x at HEAD $head_sha with the project as the checkout, and the
# claude stub prints the default findings and exits 0. Sets tmp, bin, log,
# project and resets problems.
new_case() {
	local cmd
	tmp=$(mktemp -d "${tmp_root%/}/pr-review-test.XXXXXX")
	bin="$tmp/bin"
	log="$tmp/calls.log"
	project="$tmp/project"
	problems=""
	mkdir -p "$bin" "$project/.git" "$tmp/git" "$tmp/gh"
	: >"$log"
	: >"$tmp/stdout"
	: >"$tmp/stderr"
	for cmd in awk mkdir rm; do
		ln -s "$(command -v "$cmd")" "$bin/$cmd"
	done
	for cmd in git gh claude nohup; do
		ln -s "$stubs/$cmd" "$bin/$cmd"
	done
	findings "$default_findings"
	git_file origin git@github.com:example/dnsmasq-manager.git
	git_file branch feat/x
	git_file head "$head_sha"
	git_file toplevel "$project"
}

# Links the machine's jq into the case's bin/ and returns 0. When the machine
# has no jq, prints "SKIP: <name>", removes the case's temp dir and returns 1,
# so the caller skips the case.
with_jq() {
	if [ -z "$jq_cmd" ]; then
		echo "SKIP: $1 (no jq on this machine)"
		skips=$((skips + 1))
		rm -rf "$tmp"
		return 1
	fi
	ln -s "$jq_cmd" "$bin/jq"
}

# Sets what the claude stub prints to <text> and a newline; with no argument
# it prints nothing.
findings() {
	if [ $# -ge 1 ]; then
		printf '%s\n' "$1" >"$tmp/findings"
	else
		rm -f "$tmp/findings"
	fi
}

# Makes the claude stub exit with <status>.
claude_exits() {
	printf '%s\n' "$1" >"$tmp/claude-status"
}

# Makes the claude stub wait for $tmp/release before it logs its call and
# prints the findings.
claude_waits() {
	: >"$tmp/claude-wait"
}

# Sets the git stub's state file <name> to <value>, or removes it when no
# value is given.
git_file() {
	if [ $# -ge 2 ]; then
		printf '%s\n' "$2" >"$tmp/git/$1"
	else
		rm -f "$tmp/git/$1"
	fi
}

# Writes <text> and a newline to the gh lookup fixture; with no argument the
# fixture is an empty file, as when no pull request is open.
pulls() {
	if [ $# -ge 1 ]; then
		printf '%s\n' "$1" >"$tmp/gh/pulls"
	else
		: >"$tmp/gh/pulls"
	fi
}

# Prints <text> as a JSON string literal, quotes included: backslash, quote,
# newline and tab are escaped.
json_string() {
	local s=$1 nl=$'\n' tab=$'\t'
	s=${s//\\/\\\\}
	s=${s//\"/\\\"}
	s=${s//$nl/\\n}
	s=${s//$tab/\\t}
	printf '"%s"' "$s"
}

# Prints the PostToolUse payload Claude Code sends after the Bash command
# <command> printed <stdout> (default: nothing).
payload() {
	printf '{"session_id":"s","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":%s},"tool_response":{"stdout":%s,"stderr":""}}' \
		"$(json_string "$1")" "$(json_string "${2:-}")"
}

# Runs the hook with <json> on stdin and the argument in hook_arg (default
# create), from the case's temp dir with CLAUDE_PROJECT_DIR set to the
# project and PR_REVIEW_FOREGROUND=1. Further arguments are VAR=value pairs
# added to the environment after those, so they can override them. Sets
# status; stdout and stderr land in the case's temp dir.
run_hook() {
	local json=$1
	shift
	(cd "$tmp" && printf '%s\n' "$json" |
		env -i PATH="$bin" TMPDIR="$tmp" CLAUDE_PROJECT_DIR="$project" PR_REVIEW_FOREGROUND=1 "$@" \
			"$BASH" "$pr_review" "${hook_arg:-create}") \
		>"$tmp/stdout" 2>"$tmp/stderr"
	status=$?
}

# Runs the hook as Claude Code would after the Bash command <command>, which
# printed nothing. Further arguments are as for run_hook.
run_review() {
	local command=$1
	shift
	run_hook "$(payload "$command")" "$@"
}

# Prints the line the hook emits after launching a review of pull request
# <n>, in the PostToolUse JSON wrapper.
wrapper_line() {
	printf '{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"Code review: a review of pull request #%s was started in the background. Findings will be posted on the pull request as one comment. Log: %s"}}\n' \
		"$1" "$project/.git/pr-review/review-$1.log"
}

# Prints the arguments the claude stub should have been given for a review
# of pull request <n> at <effort>, one per line.
claude_args() {
	printf '%s\n' -p "/code-review $2 $1" --settings '{"disableAllHooks": true}' \
		--permission-mode dontAsk --disallowedTools "$denied" \
		--allowedTools "$tools"
}

# Prints the request body, plus a newline, the hook should post for a review
# at <effort> whose body after the header is <json>, already JSON-escaped.
# The header names commit <sha> (default: the first 7 characters of
# $head_sha); an empty <sha> gives the wording for an unknown commit.
comment_json() {
	local sha=${3-${head_sha:0:7}} when
	when="when it was opened"
	[ -n "$sha" ] && when="at commit $sha"
	printf '%s%s%s%s%s%s"}\n' '{"body":"<!-- pr-review -->\n**Automated review.** A separate Claude Code run reviewed this pull request ' \
		"$when" ' (`/code-review ' "$1" '`). It has not seen the session that wrote the change.\n\n' "$2"
}

# Prints the path of the review run's log for pull request <n>.
review_log() {
	printf '%s\n' "$project/.git/pr-review/review-$1.log"
}

# Prints the path of the review run's findings file for pull request <n>.
review_md() {
	printf '%s\n' "$project/.git/pr-review/review-$1.md"
}

# Prints the path of the comment body file, without its header, for pull
# request <n>.
review_body() {
	printf '%s\n' "$project/.git/pr-review/review-$1.body.md"
}

# Prints the cut line for pull request <n>.
cut_line() {
	printf '%s\n' "The review was cut at 60,000 bytes; the full findings are in review-$1.md in the clone that ran the review."
}

# Prints how many comments the gh stub accepted.
posts() {
	if [ -f "$tmp/gh/posts" ]; then
		grep -c '' "$tmp/gh/posts"
	else
		echo 0
	fi
}

# Prints how many pull request lookups the call log holds.
lookup_calls() {
	grep -c '^gh api repos/[^ ]*/pulls?head=' "$log"
}

# Fails unless the gh stub accepted exactly one comment, on pull request <n>,
# whose body is byte for byte <json> (which ends with a newline).
expect_posted() {
	[ "$(posts)" -eq 1 ] || problem "$(posts) comments were posted, want 1"
	[ "$(cat "$tmp/gh/posts" 2>/dev/null)" = "repos/example/dnsmasq-manager/issues/$1/comments" ] ||
		problem "posted to: $(cat "$tmp/gh/posts" 2>/dev/null)"
	printf '%s' "$2" >"$tmp/want-body"
	cmp -s "$tmp/gh/sent" "$tmp/want-body" || problem "posted body differs; got:
$(cat "$tmp/gh/sent" 2>/dev/null)
want:
$2"
}

# Fails when the gh stub accepted any comment.
expect_no_post() {
	[ "$(posts)" -eq 0 ] || problem "$(posts) comments were posted, want 0"
}

# Fails unless the claim for pull request <n> is held.
expect_claim_kept() {
	[ -f "$project/.git/pr-review/reviewed-$1" ] || problem "the claim reviewed-$1 is not held"
}

# Fails when the claim for pull request <n> is held.
expect_claim_released() {
	[ -e "$project/.git/pr-review/reviewed-$1" ] && problem "the claim reviewed-$1 is still held"
}

# Waits up to five seconds for the detached wrapper of pull request <n> to
# finish, which it shows by appending a pr-review: line to its log.
wait_for_wrapper() {
	local i=0
	while ! grep -q '^pr-review: ' "$(review_log "$1")" 2>/dev/null && [ "$i" -lt 50 ]; do
		"$sleep_cmd" 0.1
		i=$((i + 1))
	done
}

# Fails unless the last line of the review log of pull request <n> is <line>.
expect_log_ends() {
	local last=""
	[ -f "$(review_log "$1")" ] && last=$(awk 'END { print }' "$(review_log "$1")")
	[ "$last" = "$2" ] || problem "review-$1.log ends with: $last; want: $2"
}

# Prints how many claude calls the call log holds.
claude_calls() {
	grep -c '^claude ' "$log"
}

# Prints how many gh calls the call log holds.
gh_calls() {
	grep -c '^gh ' "$log"
}

# Fails unless the hook launched exactly one review, of pull request <n> at
# <effort> (default medium), with the expected arguments and environment,
# printed the wrapper line, kept the run's stdout in review-<n>.md and its
# stderr in review-<n>.log, posted the default findings raw (no jq) as one
# comment, logged the post, and holds the claim reviewed-<n>.
expect_launched() {
	local n=$1 effort=${2:-medium}
	expect_exit 0
	[ "$(claude_calls)" -eq 1 ] || problem "claude was called $(claude_calls) times, want 1"
	claude_args "$n" "$effort" >"$tmp/want-args"
	cmp -s "$tmp/claude.args" "$tmp/want-args" || problem "claude arguments differ; want:
$(cat "$tmp/want-args")"
	expect_log_has "nested=1 lc_all=unset"
	expect_wrapper_line "$n"
	cmp -s "$(review_md "$n")" "$tmp/findings" || problem "review-$n.md does not hold the findings"
	grep -qxF "claude stderr" "$(review_log "$n")" 2>/dev/null || problem "review-$n.log lacks the run's stderr"
	expect_posted "$n" "$(comment_json "$effort" "$default_findings_json")
"
	expect_log_ends "$n" "pr-review: the findings were posted on pull request #$n"
	expect_claim_kept "$n"
}

# Fails unless stdout is exactly the wrapper line for pull request <n>.
expect_wrapper_line() {
	wrapper_line "$1" >"$tmp/want"
	cmp -s "$tmp/stdout" "$tmp/want" || problem "stdout differs; want: $(cat "$tmp/want")"
}

# Fails when the hook launched a review or printed anything.
expect_not_launched() {
	expect_exit 0
	[ "$(claude_calls)" -eq 0 ] || problem "claude was called $(claude_calls) times, want 0"
	expect_stdout_empty
}

# Fails when the state directory holds any reviewed-* entry.
expect_no_state_file() {
	local f
	for f in "$project/.git/pr-review"/reviewed-*; do
		[ -e "$f" ] && problem "state file written: $f"
	done
}

# Records <text> as a problem of the current case.
problem() {
	problems="$problems  - $1
"
}

# Fails unless the hook exited with <code>.
expect_exit() {
	[ "$status" -eq "$1" ] || problem "exit code was $status, want $1"
}

# Fails when the hook printed anything.
expect_stdout_empty() {
	[ -s "$tmp/stdout" ] && problem "stdout is not empty"
}

# Fails when the hook's stdout contains <text>.
expect_stdout_lacks() {
	grep -qF -- "$1" "$tmp/stdout" && problem "stdout has: $1"
}

# Fails when the hook ran gh at all.
expect_no_gh_call() {
	[ "$(gh_calls)" -eq 0 ] || problem "gh was called $(gh_calls) times, want 0"
}

# Fails when the hook looked the pull request up by branch.
expect_no_lookup() {
	[ "$(lookup_calls)" -eq 0 ] || problem "the pull request was looked up $(lookup_calls) times, want 0"
}

# Fails unless some line of the call log contains <text>.
expect_log_has() {
	grep -qF -- "$1" "$log" || problem "call log lacks: $1"
}

# Fails when some line of the call log contains <text>.
expect_log_lacks() {
	grep -qF -- "$1" "$log" && problem "call log has: $1"
}

# Fails when the call log holds any call at all: the hook ran no stub.
expect_log_empty() {
	[ -s "$log" ] && problem "the hook ran commands"
}

# Reports the case as PASS or FAIL; on failure shows the problems, the
# hook's stdout and stderr, and the call log. Removes the case's temp dir.
finish_case() {
	if [ -z "$problems" ]; then
		echo "PASS: $1"
	else
		failures=$((failures + 1))
		echo "FAIL: $1"
		printf '%s' "$problems"
		echo "  --- stdout ---"
		cat "$tmp/stdout"
		echo "  --- stderr ---"
		cat "$tmp/stderr"
		echo "  --- call log ---"
		cat "$log"
	fi
	rm -rf "$tmp"
}

new_case
pulls 7
run_review "gh pr create --fill"
expect_launched 7
expect_log_has "nohup claude -p"
expect_log_has "gh api repos/example/dnsmasq-manager/pulls?head=example:feat/x&state=open&per_page=1 --jq .[0].number // empty"
[ "$(grep -c '^pr-review:' "$(review_log 7)")" -eq 1 ] || problem "review-7.log has more than the posted line"
finish_case "gh pr create launches a review"

new_case
pulls 7
run_review "gh pr create --fill"
# The backticks are literal Markdown in the expected body.
# shellcheck disable=SC2016
expect_posted 7 '{"body":"<!-- pr-review -->\n**Automated review.** A separate Claude Code run reviewed this pull request at commit 0123456 (`/code-review medium`). It has not seen the session that wrote the change.\n\n```json\n[{\"file\":\"main.go\",\"line\":3,\"summary\":\"Returns before closing the file.\",\"failure_scenario\":\"A read error leaves the file open.\"}]\n```"}
'
expect_log_has "gh api -X POST repos/example/dnsmasq-manager/issues/7/comments --input "
expect_claim_kept 7
expect_log_ends 7 "pr-review: the findings were posted on pull request #7"
finish_case "a finished review posts one comment"

new_case
pulls 7
run_review "gh pr create --fill"
grep -qx -- "/code-review medium 7" "$tmp/claude.args" || problem "the prompt is not /code-review medium 7"
grep -qF -- "--comment" "$tmp/claude.args" && problem "the run was given --comment"
grep -qx -- "--disallowedTools" "$tmp/claude.args" || problem "the run was not given --disallowedTools"
grep -qxF -- "$denied" "$tmp/claude.args" || problem "the deny list is not $denied"
grep -qxF -- "$tools" "$tmp/claude.args" || problem "the allow list is not $tools"
case $tools in *"gh api"*) problem "the run's allowed tools include gh api" ;; esac
finish_case "launch arguments"

new_case
pulls 7
git_file head
run_review "gh pr create --fill"
expect_posted 7 "$(comment_json medium "$default_findings_json" "")
"
finish_case "the header falls back when HEAD cannot be read"

new_case
pulls 7
findings "$(printf '%s\n%s\n%s\nSay "hi" to C:\\dir\tnow\ncaf\303\251 is fine' "${fence}json" '[]' "$fence")"
run_review "gh pr create --fill"
expect_posted 7 "$(comment_json medium "${fence}json\\n[]\\n${fence}\\nSay \\\"hi\\\" to C:\\\\dir\\tnow\\ncafé is fine")
"
finish_case "findings are encoded"

new_case
pulls 7
printf '%s' "$default_findings" >"$tmp/findings"
run_review "gh pr create --fill"
expect_posted 7 "$(comment_json medium "$default_findings_json")
"
expect_claim_kept 7
finish_case "findings without a final newline"

new_case
pulls 7
findings
run_review "gh pr create --fill"
expect_exit 0
expect_wrapper_line 7
expect_no_post
expect_claim_released 7
expect_log_ends 7 "$empty_line"
printf '  \n\n \t \n' >"$tmp/findings"
run_review "gh pr create --fill"
expect_wrapper_line 7
expect_no_post
expect_claim_released 7
expect_log_ends 7 "$empty_line"
finish_case "empty findings"

for text in "Permission was denied; I need git fetch to continue." \
	"$(printf '%s\n%s' "${fence}json" '[]')" \
	"$(printf '%s\n%s\n%s' "$fence" '[]' "$fence")"; do
	new_case
	pulls 7
	findings "$text"
	run_review "gh pr create --fill"
	expect_exit 0
	expect_wrapper_line 7
	expect_no_post
	expect_claim_kept 7
	expect_log_ends 7 "pr-review: the run's output is not a findings list and was not posted; it is in $(review_md 7)"
	finish_case "no json block is not posted: $(printf '%s' "$text" | awk 'NR == 1')"
done

new_case
pulls 7
touch "$tmp/gh/post-fails"
run_review "gh pr create --fill"
expect_exit 0
expect_wrapper_line 7
expect_no_post
expect_log_has "gh api -X POST repos/example/dnsmasq-manager/issues/7/comments --input "
expect_claim_released 7
cmp -s "$(review_md 7)" "$tmp/findings" || problem "review-7.md was not kept"
cmp -s "$(review_body 7)" "$tmp/findings" || problem "review-7.body.md does not hold the body"
expect_log_ends 7 "pr-review: the comment could not be posted; the claim was removed; the body is in $(review_body 7)"
finish_case "post fails"

for line in "See /Users/someone/x for the log." "Resolver at 192.168.1.10 answered." "Token ghp_x1 leaked."; do
	new_case
	pulls 7
	findings "$default_findings
$line"
	run_review "gh pr create --fill"
	expect_exit 0
	expect_wrapper_line 7
	expect_no_post
	expect_claim_kept 7
	expect_log_ends 7 "pr-review: the findings were not posted: line 4 of $(review_body 7) has a local path, a private address or a token"
	finish_case "private findings are not posted: $line"
done

new_case
pulls 7
git_file toplevel /Users/someone/src/dnsmasq-manager
findings "$default_findings
See /Users/someone/src/dnsmasq-manager/internal/x.go:3 and /Users/someone/src/dnsmasq-manager/README.md."
run_review "gh pr create --fill"
expect_posted 7 "$(comment_json medium "$default_findings_json\\nSee internal/x.go:3 and README.md.")
"
expect_claim_kept 7
finish_case "a checkout path in a finding is made repo-relative"

new_case
pulls 7
git_file toplevel /Users/someone/src/dnsmasq-manager
findings "$default_findings
See /Users/someone/src/dnsmasq-manager/internal/x.go:3.
See /Users/someone/other/y.go too."
run_review "gh pr create --fill"
expect_no_post
expect_claim_kept 7
expect_log_ends 7 "pr-review: the findings were not posted: line 5 of $(review_body 7) has a local path, a private address or a token"
awk 'NR == 4' "$(review_body 7)" | grep -qxF "See internal/x.go:3." || problem "line 4 of review-7.body.md was not made repo-relative"
finish_case "a home path outside the checkout is still refused"

new_case
pulls 7
git_file toplevel
findings "$default_findings
See /Users/someone/src/dnsmasq-manager/internal/x.go:3."
run_review "gh pr create --fill"
expect_no_post
expect_claim_kept 7
expect_log_ends 7 "pr-review: the findings were not posted: line 4 of $(review_body 7) has a local path, a private address or a token"
finish_case "no checkout path: nothing is made relative"

new_case
if with_jq "renders the findings list"; then
	pulls 7
	findings "$sample_findings"
	run_review "gh pr create --fill"
	printf '%s\n' "$sample_markdown" >"$tmp/want-md"
	cmp -s "$(review_body 7)" "$tmp/want-md" || problem "review-7.body.md differs; got:
$(cat "$(review_body 7)" 2>/dev/null)"
	expect_posted 7 "$(comment_json medium "$sample_markdown_json")
"
	expect_claim_kept 7
	finish_case "renders the findings list"
fi

new_case
if with_jq "renders one finding without notes"; then
	pulls 7
	run_review "gh pr create --fill"
	# shellcheck disable=SC2016
	expect_posted 7 "$(comment_json medium '**1 finding.**\n\n### 1. Returns before closing the file.\n\n`main.go:3`\n\nA read error leaves the file open.')
"
	finish_case "renders one finding without notes"
fi

new_case
if with_jq "an empty list renders as no findings"; then
	pulls 7
	findings "$(printf '%s\n%s\n%s' "${fence}json" '[]' "$fence")"
	run_review "gh pr create --fill"
	expect_posted 7 "$(comment_json medium 'No findings.')
"
	expect_claim_kept 7
	finish_case "an empty list renders as no findings"
fi

for block in '[{"file": "a.go", "line": 1,' '{"file": "a.go"}' '["a.go"]' '[{"file": "a.go", "line": 1}]'; do
	new_case
	if with_jq "a block jq cannot render is posted raw: $block"; then
		pulls 7
		findings "$(printf '%s\n%s\n%s\n%s' "${fence}json" "$block" "$fence" 'Notes.')"
		run_review "gh pr create --fill"
		expect_posted 7 "$(comment_json medium "${fence}json\\n$(json_string "$block" | sed 's/^"//; s/"$//')\\n${fence}\\nNotes.")
"
		expect_claim_kept 7
		finish_case "a block jq cannot render is posted raw: $block"
	fi
done

new_case
pulls 7
findings "$sample_findings"
run_review "gh pr create --fill"
expect_posted 7 "$(comment_json medium "$(json_string "$sample_findings" | sed 's/^"//; s/"$//')")
"
expect_claim_kept 7
finish_case "without jq the findings are posted raw"

new_case
pulls 7
{
	printf '%s\n%s\n%s\n' "${fence}json" '[]' "$fence"
	i=1
	while [ "$i" -le 2000 ]; do
		printf 'line %04d xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n' "$i"
		i=$((i + 1))
	done
} >"$tmp/findings"
run_review "gh pr create --fill"
[ "$(posts)" -eq 1 ] || problem "$(posts) comments were posted, want 1"
size=$(($(wc -c <"$tmp/gh/sent")))
[ "$size" -le 65000 ] || problem "the body is $size bytes, want at most 65,000"
sent=$(cat "$tmp/gh/sent")
[[ $sent == *'\nline 1463 xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n\n'"$(cut_line 7)"'"}' ]] ||
	problem "the body does not end with line 1463, an empty line and the cut line: ${sent:${#sent}-200}"
[[ $sent == *"line 1464"* ]] && problem "the body has line 1464"
expect_claim_kept 7
finish_case "findings are cut"

new_case
pulls 7
{
	printf '%s\n%s\n%s\n' "${fence}json" '[]' "$fence"
	i=1
	while [ "$i" -le 2000 ]; do
		printf 'line %04d %s\n' "$i" "$(printf '\303\251\303\251\303\251\303\251\303\251\303\251\303\251\303\251\303\251\303\251\303\251\303\251\303\251\303\251\303\251')"
		i=$((i + 1))
	done
} >"$tmp/findings"
run_review "gh pr create --fill" LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8
[ "$(posts)" -eq 1 ] || problem "$(posts) comments were posted, want 1"
sent=$(cat "$tmp/gh/sent")
[[ $sent == *'\nline 1463 '*'\n\n'"$(cut_line 7)"'"}' ]] ||
	problem "the body does not end with line 1463, an empty line and the cut line"
[[ $sent == *"line 1464"* ]] && problem "the body has line 1464"
finish_case "the cut counts bytes, not characters"

new_case
pulls 7
{
	printf '%s\n%s\n%s\n' "${fence}json" '[]' "$fence"
	printf 'Found 1 issue:\n\n%sgo\n' "$fence"
	i=1
	while [ "$i" -le 2000 ]; do
		printf 'line %04d xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n' "$i"
		i=$((i + 1))
	done
	printf '%s\n' "$fence"
} >"$tmp/findings"
run_review "gh pr create --fill"
sent=$(cat "$tmp/gh/sent" 2>/dev/null)
[[ $sent == *'\nline 1462 xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n'"$fence"'\n\n'"$(cut_line 7)"'"}' ]] ||
	problem "the body does not end with line 1462, a closing fence, an empty line and the cut line: ${sent:${#sent}-200}"
finish_case "cut inside a code block"

new_case
pulls 7
{
	printf '%s\n%s\n%s\n' "${fence}json" '[]' "$fence"
	printf 'Found 1 issue:\n\n%sgo\nx := 1\n%s\n' "$fence" "$fence"
	i=1
	while [ "$i" -le 2000 ]; do
		printf 'line %04d xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n' "$i"
		i=$((i + 1))
	done
} >"$tmp/findings"
run_review "gh pr create --fill"
sent=$(cat "$tmp/gh/sent" 2>/dev/null)
[[ $sent == *'xxxxxxxxxxxxxxxxxxxxxxxxxxxx\n\n'"$(cut_line 7)"'"}' ]] ||
	problem "a closed code block gained a fence line before the cut line: ${sent:${#sent}-200}"
finish_case "cut after a closed code block"

new_case
pulls 7
run_review "gh pr create --fill"
run_review "gh pr create --fill"
expect_exit 0
expect_stdout_empty
[ "$(claude_calls)" -eq 1 ] || problem "claude was called $(claude_calls) times, want 1"
finish_case "the same command again does nothing"

for hook_arg in api "x y"; do
	new_case
	pulls 7
	run_review "gh pr create --fill"
	expect_launched 7
	finish_case "the argument '$hook_arg' is ignored"
done
unset hook_arg

new_case
pulls 7
run_review "FOO=1 git push -u origin x && gh pr create --fill"
expect_launched 7
finish_case "compound command"

new_case
pulls 7
run_review 'git commit -m "docs: say \"hi\" in C:\dir"
gh pr create --title "Serve \"A\" records" --body "x"'
expect_launched 7
finish_case "quotes and a newline before gh pr create"

for command in "gh api -X POST repos/example/dnsmasq-manager/pulls -f title=x -f head=y -f base=main" \
	"gh api --method POST repos/example/dnsmasq-manager/pulls -f title=x" \
	"gh api repos/example/dnsmasq-manager/pulls --input f" \
	"gh api \"repos/example/dnsmasq-manager/pulls\" -F draft=true"; do
	new_case
	pulls 7
	run_review "$command"
	expect_launched 7
	finish_case "REST create launches: $command"
done

new_case
pulls 7
for command in "gh api repos/example/dnsmasq-manager/pulls" "gh pr view 7" "gh pr list" \
	"gh api -X POST repos/example/dnsmasq-manager/pulls/7/comments -f body=x" "git status" \
	"gh api -X PATCH repos/example/dnsmasq-manager/pulls/7 -f state=closed" "sigh pr create" "gh pr created"; do
	run_review "$command"
	expect_not_launched
	expect_no_gh_call
	expect_log_empty
done
finish_case "reads and other commands do nothing"

new_case
pulls 7
for command in "gh api -X GET repos/example/dnsmasq-manager/pulls -f state=open" \
	"gh api --method GET repos/example/dnsmasq-manager/pulls -f state=open" \
	"gh api -X get repos/example/dnsmasq-manager/pulls -F per_page=1" \
	"gh api --method=Get repos/example/dnsmasq-manager/pulls --raw-field state=all" \
	"gh api -X=GET repos/example/dnsmasq-manager/pulls --field state=open"; do
	run_review "$command"
	expect_not_launched
	expect_no_gh_call
	expect_log_empty
done
finish_case "a GET with fields is a read"

new_case
pulls 7
run_review "gh pr create --fill"
run_review 'echo "gh pr create"'
expect_exit 0
expect_stdout_empty
[ "$(claude_calls)" -eq 1 ] || problem "claude was called $(claude_calls) times, want 1"
finish_case "look-alike after a review"

new_case
pulls 9
run_hook "$(payload "gh pr create --fill" "https://github.com/example/dnsmasq-manager/pull/7")"
expect_launched 7
expect_no_lookup
finish_case "the URL in the output names the pull request"

new_case
pulls 9
git_file branch
run_hook "$(payload "gh pr create --fill" "https://github.com/Example/DNSmasq-Manager/pull/7")"
expect_launched 7
expect_no_lookup
finish_case "the URL is matched case-insensitively, without a branch"

new_case
pulls 9
run_hook "$(payload "gh pr view 3 && gh pr create --fill" "title:	Older
url:	https://github.com/example/dnsmasq-manager/pull/3
https://github.com/example/dnsmasq-manager/pull/7
")"
expect_launched 7
expect_no_lookup
finish_case "the last pull request URL in the output wins"

new_case
pulls 9
run_hook "$(payload "gh api -X POST repos/example/dnsmasq-manager/pulls -f title=x -f head=y -f base=main" '{
  "url": "https://api.github.com/repos/example/dnsmasq-manager/pulls/7",
  "html_url": "https://github.com/example/dnsmasq-manager/pull/7",
  "diff_url": "https://github.com/example/dnsmasq-manager/pull/7.diff",
  "body": "Follows https://github.com/example/dnsmasq-manager/pull/3.",
  "_links": {"html": {"href": "https://github.com/example/dnsmasq-manager/pull/7"}}
}')"
expect_launched 7
expect_no_lookup
finish_case "the REST response names the pull request"

new_case
pulls 7
run_hook "$(payload "gh pr create --fill" "https://github.com/other/dnsmasq-manager/pull/7")"
expect_not_launched
expect_no_gh_call
expect_no_state_file
finish_case "a URL for another repository does nothing"

new_case
pulls 7
run_hook "$(payload 'gh pr create --body "Follows https://github.com/example/dnsmasq-manager/pull/3; \"tool_response\": https://github.com/example/dnsmasq-manager/pull/4"')"
expect_launched 7
expect_log_has "gh api repos/example/dnsmasq-manager/pulls?head=example:feat/x"
finish_case "a URL only in the command is ignored"

new_case
pulls 7
run_hook '{"session_id":"s","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"gh pr create --body \"\\\"tool_response\\\": https://github.com/example/dnsmasq-manager/pull/3\""}}'
expect_launched 7
expect_log_has "gh api repos/example/dnsmasq-manager/pulls?head=example:feat/x"
finish_case "no tool_response falls back to the lookup"

new_case
pulls
run_review "gh pr create --fill"
expect_not_launched
expect_no_state_file
finish_case "no open pull request"

new_case
run_review "gh pr create --fill"
expect_not_launched
expect_no_state_file
finish_case "lookup fails"

new_case
pulls "7; rm -rf x"
run_review "gh pr create --fill"
expect_not_launched
expect_no_state_file
pulls "07"
run_review "gh pr create --fill"
expect_not_launched
expect_no_state_file
printf '7\n8\n' >"$tmp/gh/pulls"
run_review "gh pr create --fill"
expect_not_launched
expect_no_state_file
finish_case "a non-number from the lookup"

new_case
pulls 7
git_file branch
run_review "gh pr create --fill"
expect_not_launched
expect_no_gh_call
finish_case "detached HEAD"

new_case
pulls 7
run_review "gh pr create --fill" PR_REVIEW_NESTED=1
expect_not_launched
expect_no_gh_call
expect_log_empty
finish_case "nested run is ignored"

new_case
pulls 7
run_review "gh pr create --fill" CLAUDE_CODE_REMOTE=true
expect_not_launched
expect_no_gh_call
expect_log_empty
finish_case "cloud session is ignored"

new_case
pulls 7
rm -f "$bin/claude"
run_review "gh pr create --fill"
expect_not_launched
expect_no_gh_call
finish_case "claude missing"

new_case
pulls 7
rm -f "$bin/gh"
run_review "gh pr create --fill"
expect_not_launched
expect_log_empty
finish_case "gh missing"

new_case
pulls 7
run_review "gh pr create --fill" PR_REVIEW_EFFORT=high
expect_launched 7 high
finish_case "effort from the environment: high"

for effort in max ultra "x; y" ""; do
	new_case
	pulls 7
	run_review "gh pr create --fill" PR_REVIEW_EFFORT="$effort"
	expect_launched 7 medium
	finish_case "effort from the environment: '$effort' gives medium"
done

new_case
pulls 7
mkdir -p "$project/.git/pr-review"
: >"$project/.git/pr-review/reviewed-7"
run_review "gh pr create --fill"
expect_not_launched
finish_case "second claim loses"

new_case
pulls 7
: >"$project/.git/pr-review"
run_review "gh pr create --fill"
expect_not_launched
finish_case "state cannot be written"

new_case
pulls 7
claude_exits 1
run_review "gh pr create --fill"
expect_exit 0
expect_wrapper_line 7
expect_claim_released 7
expect_log_ends 7 "pr-review: the review run exited with status 1; the claim was removed; its output is in $(review_md 7)"
expect_no_post
cmp -s "$(review_md 7)" "$tmp/findings" || problem "review-7.md does not hold the failed run's output"
run_review "gh pr create --fill"
expect_exit 0
expect_wrapper_line 7
[ "$(claude_calls)" -eq 2 ] || problem "claude was called $(claude_calls) times, want 2"
expect_no_post
finish_case "failed run: the claim is released and nothing is posted"

new_case
pulls 7
git_file branch "feat/a+b#c"
run_review "gh pr create --fill"
expect_launched 7
expect_log_has "gh api repos/example/dnsmasq-manager/pulls?head=example:feat/a%2Bb%23c&state=open&per_page=1 "
finish_case "branch name is encoded"

new_case
pulls 7
git_file branch "feat/$(printf 'caf\303\251')&x=1 ~_.-"
run_review "gh pr create --fill"
expect_launched 7
expect_log_has "head=example:feat/caf%C3%A9%26x%3D1%20~_.-&state=open"
finish_case "branch name is encoded: bytes above 0x7f"

new_case
pulls 7
rm -f "$bin/nohup"
run_review "gh pr create --fill"
expect_launched 7
expect_log_lacks "nohup"
finish_case "works without nohup"

new_case
pulls 7
claude_waits
run_review "gh pr create --fill" PR_REVIEW_FOREGROUND=
expect_exit 0
expect_wrapper_line 7
[ "$(claude_calls)" -eq 0 ] || problem "the hook waited for the review run"
: >"$tmp/release"
wait_for_wrapper 7
[ "$(claude_calls)" -eq 1 ] || problem "the review run did not finish after it was released"
cmp -s "$(review_md 7)" "$tmp/findings" || problem "the review run's output is not in review-7.md"
expect_stdout_lacks "main.go"
expect_stdout_lacks "claude stderr"
finish_case "detached launch returns"

new_case
pulls 7
claude_waits
run_review "gh pr create --fill" PR_REVIEW_FOREGROUND=
expect_exit 0
expect_wrapper_line 7
[ "$(posts)" -eq 0 ] || problem "a comment was posted before the review run finished"
expect_log_lacks "gh api -X POST"
: >"$tmp/release"
wait_for_wrapper 7
expect_posted 7 "$(comment_json medium "$default_findings_json")
"
expect_stdout_lacks "main.go"
expect_stdout_lacks "pr-review:"
expect_stdout_lacks "{}"
finish_case "the post comes from the detached run"

[ "$skips" -ne 0 ] && echo "$skips case(s) skipped: no jq on this machine"
if [ "$failures" -ne 0 ]; then
	echo "$failures case(s) failed"
	exit 1
fi
echo "all cases passed"
