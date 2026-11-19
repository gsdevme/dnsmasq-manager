#!/usr/bin/env bash
#
# Tests for .claude/hooks/pr-review.sh, the PostToolUse hook that starts a
# background code review of a pull request a session's command opened.
#
# Each case runs the hook in a clean environment (env -i) whose PATH is a
# per-case temp bin/ directory and nothing else. That directory holds symlinks
# to the basic commands the hook may use (awk, mkdir, rm) and stubs for git,
# gh, claude and nohup, so no real repository, GitHub or Claude can be
# reached; the stubs use only bash builtins. The git stub answers from files
# in the case's git/ directory; the gh stub prints the gh/pulls fixture, or
# exits 1 when there is none; the claude stub writes its arguments, one per
# line, to claude.args, logs its PR_REVIEW_NESTED and LC_ALL, and exits with
# the status new_case or claude_exits gave it; the nohup stub runs its
# arguments. Every stub appends "<name> <args>" to a call log. Cases run the
# hook with PR_REVIEW_FOREGROUND=1, so the review run has finished when the
# hook returns, except the one case that checks the detached launch.
#
# Needs only bash (3.2 or newer) and a POSIX userland. Run it with
# `make test-hook`. Prints PASS/FAIL per case and exits non-zero if any fail.

root=$(cd "$(dirname "$0")/../.." && pwd)
pr_review="$root/.claude/hooks/pr-review.sh"
tools='Read,Grep,Glob,Agent,Skill,TodoWrite,Bash(gh pr view *),Bash(gh pr diff *),Bash(gh pr checks *),Bash(gh api *),Bash(git diff *),Bash(git log *),Bash(git show *),Bash(git status *),Bash(git blame *),Bash(git rev-parse *),Bash(git merge-base *),Bash(git ls-files *),Bash(git cat-file *),Bash(git remote get-url *),Bash(git branch --show-current),Bash(head *),Bash(tail *),Bash(grep *),Bash(wc *),Bash(jq *),Bash(cat *),Bash(ls *)'
denied='Edit,Write,NotebookEdit,Bash(gh api *merge*),Bash(gh api */contents/*),Bash(gh api */git/*),Bash(gh api *-X DELETE*),Bash(gh api *--method DELETE*),Bash(gh api *-X PUT*),Bash(gh api *--method PUT*),Bash(gh api *-X PATCH*),Bash(gh api *--method PATCH*),Bash(* --output*),Bash(* --upload-pack*),Bash(* --exec*)'
failed_line='pr-review: the review run exited with status 1; the claim was removed'
sleep_cmd=$(command -v sleep)
failures=0

# Prints the body of the git stub. It answers the git calls the hook makes
# from files in $tmp/git/ (origin, branch) and exits 1 for any other call.
git_stub() {
	printf "state='%s'\n" "$tmp/git"
	printf "common='%s'\n" "$tmp/project/.git"
	cat <<'EOF'
case "$*" in
"remote get-url origin")
	[ -f "$state/origin" ] || exit 1
	printf '%s\n' "$(<"$state/origin")"
	;;
"symbolic-ref --quiet --short HEAD")
	[ -f "$state/branch" ] || exit 1
	printf '%s\n' "$(<"$state/branch")"
	;;
"rev-parse --git-common-dir")
	printf '%s\n' "$common"
	;;
*)
	exit 1
	;;
esac
EOF
}

# Prints the body of the gh stub: it prints $tmp/gh/pulls, which holds what
# the lookup's --jq expression would print, or exits 1 when there is none.
gh_stub() {
	printf "pulls='%s'\n" "$tmp/gh/pulls"
	cat <<'EOF'
[ -f "$pulls" ] || exit 1
printf '%s\n' "$(<"$pulls")"
EOF
}

# Prints the body of a claude stub that exits with <status>: it writes its
# arguments, one per line, to $tmp/claude.args and logs the PR_REVIEW_NESTED
# and LC_ALL it was given.
claude_stub() {
	printf "args='%s'\n" "$tmp/claude.args"
	printf "log='%s'\n" "$log"
	cat <<'EOF'
printf '%s\n' "$@" >"$args"
printf 'nested=%s lc_all=%s\n' "${PR_REVIEW_NESTED-unset}" "${LC_ALL-unset}" >>"$log"
EOF
	printf 'exit %s\n' "$1"
}

# Creates the temp tree for a case: bin/ with the allowed commands and the
# stubs, project/.git, git/ and gh/, and an empty call log. The git state
# defaults to origin git@github.com:example/dnsmasq-manager.git on branch
# feat/x, and the claude stub exits 0. Sets tmp, bin, log, project and resets
# problems.
new_case() {
	local tmp_root=${TMPDIR:-/tmp} cmd
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
	stub git "$(git_stub)"
	stub gh "$(gh_stub)"
	claude_exits 0
	stub nohup 'exec "$@"'
	git_file origin git@github.com:example/dnsmasq-manager.git
	git_file branch feat/x
}

# Writes an executable stub bin/<name> that logs its call and then runs
# <body> (shell code; defaults to `exit 0`). Replaces any existing entry.
stub() {
	local name=$1 body=${2:-exit 0}
	rm -f "$bin/$name"
	{
		printf '#!%s\n' "$BASH"
		printf 'printf "%%s\\n" "%s $*" >>"%s"\n' "$name" "$log"
		printf '%s\n' "$body"
	} >"$bin/$name"
	chmod +x "$bin/$name"
}

# Replaces the claude stub with one that exits with <status>.
claude_exits() {
	stub claude "$(claude_stub "$1")"
}

# Replaces the claude stub with one that logs nothing at first: it waits up
# to five seconds for $tmp/release to exist, prints "review output" to its
# stdout, and only then logs "claude <args>".
waiting_claude_stub() {
	rm -f "$bin/claude"
	{
		printf '#!%s\n' "$BASH"
		printf "release='%s'\n" "$tmp/release"
		printf "log='%s'\n" "$log"
		printf "sleep_cmd='%s'\n" "$sleep_cmd"
		cat <<'EOF'
i=0
while [ ! -e "$release" ] && [ "$i" -lt 50 ]; do
	"$sleep_cmd" 0.1
	i=$((i + 1))
done
echo "review output"
printf '%s\n' "claude $*" >>"$log"
EOF
	} >"$bin/claude"
	chmod +x "$bin/claude"
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
	printf '{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"Code review: a review of pull request #%s was started in the background. Findings will be posted on the pull request. Log: %s"}}\n' \
		"$1" "$project/.git/pr-review/review-$1.log"
}

# Prints the arguments the claude stub should have been given for a review
# of pull request <n> at <effort>, one per line.
claude_args() {
	printf '%s\n' -p "/code-review $2 --comment $1" --settings '{"disableAllHooks": true}' \
		--permission-mode dontAsk --disallowedTools "$denied" --allowedTools "$tools"
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
# printed the wrapper line, and holds the claim reviewed-<n>.
expect_launched() {
	local n=$1 effort=${2:-medium}
	expect_exit 0
	[ "$(claude_calls)" -eq 1 ] || problem "claude was called $(claude_calls) times, want 1"
	claude_args "$n" "$effort" >"$tmp/want-args"
	cmp -s "$tmp/claude.args" "$tmp/want-args" || problem "claude arguments differ; want:
$(cat "$tmp/want-args")"
	expect_log_has "nested=1 lc_all=unset"
	expect_wrapper_line "$n"
	[ -f "$project/.git/pr-review/reviewed-$n" ] || problem "reviewed-$n was not created"
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

problem() {
	problems="$problems  - $1
"
}

expect_exit() {
	[ "$status" -eq "$1" ] || problem "exit code was $status, want $1"
}

expect_stdout_empty() {
	[ -s "$tmp/stdout" ] && problem "stdout is not empty"
}

expect_stdout_lacks() {
	grep -qF -- "$1" "$tmp/stdout" && problem "stdout has: $1"
}

expect_no_gh_call() {
	[ "$(gh_calls)" -eq 0 ] || problem "gh was called $(gh_calls) times, want 0"
}

# Fails unless some line of the call log contains <text>.
expect_log_has() {
	grep -qF -- "$1" "$log" || problem "call log lacks: $1"
}

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

# Prints the path of the review run's log for pull request <n>.
review_log() {
	printf '%s\n' "$project/.git/pr-review/review-$1.log"
}

new_case
pulls 7
run_review "gh pr create --fill"
expect_launched 7
expect_log_has "nohup claude -p"
expect_log_has "gh api repos/example/dnsmasq-manager/pulls?head=example:feat/x&state=open&per_page=1 --jq .[0].number // empty"
grep -qF "pr-review:" "$(review_log 7)" && problem "review-7.log has a pr-review line"
finish_case "gh pr create launches a review"

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
expect_no_gh_call
finish_case "the URL in the output names the pull request"

new_case
pulls 9
git_file branch
run_hook "$(payload "gh pr create --fill" "https://github.com/Example/DNSmasq-Manager/pull/7")"
expect_launched 7
expect_no_gh_call
finish_case "the URL is matched case-insensitively, without a branch"

new_case
pulls 9
run_hook "$(payload "gh pr view 3 && gh pr create --fill" "title:	Older
url:	https://github.com/example/dnsmasq-manager/pull/3
https://github.com/example/dnsmasq-manager/pull/7
")"
expect_launched 7
expect_no_gh_call
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
expect_no_gh_call
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
[ -e "$project/.git/pr-review/reviewed-7" ] && problem "reviewed-7 is still there after a failed run"
grep -qxF "$failed_line" "$(review_log 7)" 2>/dev/null || problem "review-7.log lacks: $failed_line"
run_review "gh pr create --fill"
expect_exit 0
expect_wrapper_line 7
[ "$(claude_calls)" -eq 2 ] || problem "claude was called $(claude_calls) times, want 2"
finish_case "a failed review releases the claim"

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
waiting_claude_stub
run_review "gh pr create --fill" PR_REVIEW_FOREGROUND=
expect_exit 0
expect_wrapper_line 7
[ "$(claude_calls)" -eq 0 ] || problem "the hook waited for the review run"
: >"$tmp/release"
i=0
while [ "$(claude_calls)" -eq 0 ] && [ "$i" -lt 50 ]; do
	"$sleep_cmd" 0.1
	i=$((i + 1))
done
[ "$(claude_calls)" -eq 1 ] || problem "the review run did not finish after it was released"
grep -qF "review output" "$(review_log 7)" 2>/dev/null ||
	problem "the review run's output is not in review-7.log"
expect_stdout_lacks "review output"
finish_case "detached launch returns"

if [ "$failures" -ne 0 ]; then
	echo "$failures case(s) failed"
	exit 1
fi
echo "all cases passed"
