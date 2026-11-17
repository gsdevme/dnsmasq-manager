#!/usr/bin/env bash
#
# Tests for the project-manager scripts in .claude/hooks/: pm-issue.sh.
#
# Each case runs a script in a clean environment (env -i) whose PATH is a
# per-case temp bin/ directory and nothing else. That directory holds symlinks
# to the few basic commands the scripts may use, plus stubs for git and gh, so
# no real repository or GitHub can be reached. The git stub answers from files
# in the case's git/ directory; the gh stub replays fixtures from its gh/
# directory, keyed by method and endpoint, and keeps a copy of every request
# body it accepts in gh/sent/. Every stub appends "<name> <args>" to a call
# log. A case asserts on the exit code, stdout, the log and the bodies sent.
#
# Needs only bash (3.2 or newer) and a POSIX userland. Run it with
# `make test-hook`. Prints PASS/FAIL per case and exits non-zero if any fail.

root=$(cd "$(dirname "$0")/../.." && pwd)
pm_issue="$root/.claude/hooks/pm-issue.sh"
issue_key=GET_repos_example_dnsmasq_manager_issues_4
patch_key=PATCH_repos_example_dnsmasq_manager_issues_4
user_key=GET_user
list_key=GET_repos_example_dnsmasq_manager_issues_4_comments
post_key=POST_repos_example_dnsmasq_manager_issues_4_comments
edit_key=PATCH_repos_example_dnsmasq_manager_issues_comments_11
marker="<!-- project-manager:feat/4-serve -->"
failures=0

# Prints the body of the git stub. It answers the handful of git calls the
# scripts make from files in $tmp/git/ (origin, branch, head, ahead, pushed)
# and exits 1 for any other call.
git_stub() {
	printf "state='%s'\n" "$tmp/git"
	printf "common='%s'\n" "$tmp/project/.git"
	cat <<'EOF'
case "$*" in
"remote get-url origin")
	[ -f "$state/origin" ] || exit 1
	cat "$state/origin"
	;;
"symbolic-ref --quiet --short HEAD")
	[ -f "$state/branch" ] || exit 1
	cat "$state/branch"
	;;
"rev-parse --git-common-dir")
	printf '%s\n' "$common"
	;;
"rev-parse HEAD")
	[ -f "$state/head" ] || exit 1
	cat "$state/head"
	;;
"rev-list --count origin/main..HEAD")
	if [ -f "$state/ahead" ]; then cat "$state/ahead"; else echo 0; fi
	;;
"merge-base --is-ancestor HEAD origin/"*)
	[ -f "$state/branch" ] && [ -f "$state/pushed" ] || exit 1
	[ "$*" = "merge-base --is-ancestor HEAD origin/$(cat "$state/branch")" ]
	;;
*)
	exit 1
	;;
esac
EOF
}

# Prints the body of the gh stub. It handles `gh api` only: the fixture file
# $tmp/gh/<METHOD>_<endpoint> (query string dropped, every character outside
# [A-Za-z0-9] written as _) is printed with exit 0, or the stub exits 1 when
# there is none. When the fixture exists, an --input file is first copied to
# $tmp/gh/sent/<key>. --jq and --paginate are ignored: fixtures hold what the
# --jq expression would print.
gh_stub() {
	printf "dir='%s'\n" "$tmp/gh"
	cat <<'EOF'
[ "$1" = api ] || exit 1
shift
method=GET endpoint="" input=""
while [ $# -gt 0 ]; do
	case $1 in
	-X) method=$2; shift; shift ;;
	--input) input=$2; shift; shift ;;
	--jq) shift; shift ;;
	--paginate) shift ;;
	*) endpoint=$1; shift ;;
	esac
done
key=$(printf '%s' "${method}_${endpoint%%\?*}" | tr -c 'A-Za-z0-9' '_')
[ -f "$dir/$key" ] || exit 1
if [ -n "$input" ]; then
	mkdir -p "$dir/sent"
	cat "$input" >"$dir/sent/$key"
fi
cat "$dir/$key"
EOF
}

# Creates the temp tree for a case: bin/ with the allowed commands and the git
# and gh stubs, project/.git, git/ and gh/, and an empty call log. The git
# state defaults to origin git@github.com:example/dnsmasq-manager.git on
# branch feat/4-serve. Sets tmp, bin, log, project and resets problems.
new_case() {
	local tmp_root=${TMPDIR:-/tmp}
	tmp=$(mktemp -d "${tmp_root%/}/pm-test.XXXXXX")
	bin="$tmp/bin"
	log="$tmp/calls.log"
	project="$tmp/project"
	problems=""
	mkdir -p "$bin" "$project/.git" "$tmp/git" "$tmp/gh"
	: >"$log"
	ln -s "$BASH" "$bin/bash"
	local cmd
	for cmd in cat grep mkdir sed awk tr rm mktemp head tail; do
		ln -s "$(command -v "$cmd")" "$bin/$cmd"
	done
	stub git "$(git_stub)"
	stub gh "$(gh_stub)"
	git_file origin git@github.com:example/dnsmasq-manager.git
	git_file branch feat/4-serve
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

# Sets the git stub's state file <name> to <value>, or removes it when no
# value is given.
git_file() {
	if [ $# -ge 2 ]; then
		printf '%s\n' "$2" >"$tmp/git/$1"
	else
		rm -f "$tmp/git/$1"
	fi
}

# Writes stdin to the gh fixture <key>.
fixture() {
	cat >"$tmp/gh/$1"
}

# Writes the issue 4 fixture in the show format, with <body> as the body and
# the trailing newline gh --jq adds.
issue_fixture() {
	printf 'title: Serve records\nstate: open\nurl: https://github.com/example/dnsmasq-manager/issues/4\nauthor_association: OWNER\n\n%s\n' "$1" |
		fixture "$issue_key"
}

# Writes the fixtures a comment case needs: the user login (skipped when
# <login> is empty), the comment list holding <rows>, and successful POST and
# PATCH replies, so any request the script makes is recorded.
comment_fixtures() {
	local login=$1 rows=${2:-}
	[ -n "$login" ] && printf '%s\n' "$login" | fixture "$user_key"
	if [ -n "$rows" ]; then
		printf '%s\n' "$rows" | fixture "$list_key"
	else
		: | fixture "$list_key"
	fi
	echo '{}' | fixture "$post_key"
	echo '{}' | fixture "$edit_key"
}

# Writes <text> and a newline to the case's comment file, $tmp/comment.md.
comment_file() {
	printf '%s\n' "$1" >"$tmp/comment.md"
}

# Sources pm-issue.sh in the same minimal environment as run_pm and runs
# <function> with <args>. Sets status; stdout and stderr land in the case's
# temp dir.
run_fn() {
	# $1 and $@ are meant to expand in the inner shell.
	# shellcheck disable=SC2016
	(cd "$project" && env -i PATH="$bin" TMPDIR="$tmp" "$BASH" -c '. "$1"; shift; "$@"' run_fn "$pm_issue" "$@") \
		>"$tmp/stdout" 2>"$tmp/stderr" </dev/null
	status=$?
}

# Writes <text> and a newline to $tmp/want, for expect_stdout_file.
want() {
	printf '%s\n' "$1" >"$tmp/want"
}

# Runs `comment 4` on the standard comment text plus the line <line> and
# fails unless it is refused, naming line 5, with nothing sent.
expect_comment_refused() {
	comment_file "$comment_text
$1"
	run_pm comment 4 "$tmp/comment.md"
	[ "$status" -eq 1 ] || problem "not refused: $1"
	expect_stderr_has "line 5 of"
	expect_nothing_sent
}

# Runs `comment 4` on the standard comment text plus the line <line> and
# fails unless it is posted.
expect_comment_allowed() {
	rm -rf "$tmp/gh/sent"
	comment_file "$comment_text
$1"
	run_pm comment 4 "$tmp/comment.md"
	[ "$status" -eq 0 ] || problem "refused or failed (exit $status): $1"
	[ -f "$tmp/gh/sent/$post_key" ] || problem "not posted: $1"
}

# Runs pm-issue.sh with <args> in a minimal environment, from the project
# directory. Sets status; stdout and stderr land in the case's temp dir.
run_pm() {
	(cd "$project" && env -i PATH="$bin" TMPDIR="$tmp" "$BASH" "$pm_issue" "$@") \
		>"$tmp/stdout" 2>"$tmp/stderr" </dev/null
	status=$?
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

# Fails unless stdout is byte for byte the content of <file>.
expect_stdout_file() {
	cmp -s "$tmp/stdout" "$1" || problem "stdout differs from $1"
}

expect_stderr_has() {
	grep -qF -- "$1" "$tmp/stderr" || problem "stderr lacks: $1"
}

# Fails unless some line of the call log contains <text>.
expect_log_has() {
	grep -qF -- "$1" "$log" || problem "call log lacks: $1"
}

# Fails unless the body sent for <key> is byte for byte <expected> plus a
# newline.
expect_sent() {
	if [ ! -f "$tmp/gh/sent/$1" ]; then
		problem "nothing was sent for $1"
		return
	fi
	printf '%s\n' "$2" >"$tmp/expected"
	cmp -s "$tmp/gh/sent/$1" "$tmp/expected" ||
		problem "sent for $1: $(cat "$tmp/gh/sent/$1"); want: $2"
}

expect_not_sent() {
	[ -f "$tmp/gh/sent/$1" ] && problem "a body was sent for $1"
}

expect_nothing_sent() {
	if [ -d "$tmp/gh/sent" ] && [ -n "$(ls -A "$tmp/gh/sent")" ]; then
		problem "bodies were sent: $(ls "$tmp/gh/sent")"
	fi
}

# Fails when any link-* file exists in the state directory.
expect_no_link_file() {
	local f
	for f in "$project/.git/project-manager"/link-*; do
		[ -e "$f" ] && problem "link file written: $f"
	done
}

# Reports the case as PASS or FAIL; on failure shows the problems, the
# script's stdout and stderr, and the call log. Removes the case's temp dir.
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

body='Serve the records over HTTP.

- [ ] Serve A records
- [ ] Serve AAAA records
- [ ] Reload dnsmasq

Exit criteria: dig answers for home.example.test.'

comment_text='## Progress on feat/4-serve

Done
- Serve A records (abc1234)'
comment_json='{"body":"<!-- project-manager:feat/4-serve -->\n## Progress on feat/4-serve\n\nDone\n- Serve A records (abc1234)"}'

new_case
for origin in git@github.com:o/r.git https://github.com/o/r http://local_proxy@127.0.0.1:1234/git/o/r; do
	git_file origin "$origin"
	printf 'title: t\nstate: open\nurl: u\nauthor_association: OWNER\n\nb\n' | fixture GET_repos_o_r_issues_4
	: >"$log"
	run_pm show 4
	[ "$status" -eq 0 ] || problem "$origin: exit code was $status, want 0"
	grep -qF -- "gh api repos/o/r/issues/4 " "$log" || problem "$origin: no call to repos/o/r/issues/4"
done
finish_case "slug: URL shapes"

new_case
git_file branch claude/session-abc
run_pm link 7
expect_exit 0
expect_stdout_empty
[ "$(cat "$project/.git/project-manager/link-claude_session-abc" 2>/dev/null)" = 7 ] ||
	problem "link-claude_session-abc does not hold 7"
finish_case "link: records the issue"

new_case
git_file branch main
run_pm link 7
expect_exit 1
expect_no_link_file
git_file branch
run_pm link 7
expect_exit 1
expect_no_link_file
finish_case "link: refuses main and detached"

new_case
run_pm link 4x
expect_exit 1
expect_no_link_file
finish_case "link: rejects a non-number"

new_case
run_fn pm_issue_for_branch feat/4-serve
expect_exit 0
want 4
expect_stdout_file "$tmp/want"
run_fn pm_issue_for_branch feat/04-serve
expect_exit 1
expect_stdout_empty
run_fn pm_issue_for_branch feat/0-serve
expect_exit 1
expect_stdout_empty
finish_case "issue: a leading zero in the name is unlinked"

new_case
issue_fixture "$body"
run_pm show 4
expect_exit 0
expect_stdout_file "$tmp/gh/$issue_key"
finish_case "show: prints the issue"

new_case
printf 'title: Serve records\nstate: open\nurl: https://github.com/example/dnsmasq-manager/issues/4\nauthor_association: NONE\n\nSECRET-BODY\n' |
	fixture "$issue_key"
run_pm show 4
expect_exit 0
want 'title: Serve records
state: open
url: https://github.com/example/dnsmasq-manager/issues/4
author_association: NONE

The issue body is not shown: its author is not a collaborator on this repository.'
expect_stdout_file "$tmp/want"
grep -qF SECRET-BODY "$tmp/stdout" && problem "stdout has the body"
finish_case "show: outside author gets no body"

new_case
printf 'title: Serve\nrecords\nstate: open\nurl: u\nauthor_association: OWNER\n\n- [ ] Serve A records\n' | fixture "$issue_key"
echo '{}' | fixture "$patch_key"
run_pm show 4
expect_exit 1
expect_stdout_empty
run_pm tick 4 "Serve A records"
expect_exit 1
expect_nothing_sent
finish_case "show: a title over two lines is refused"

new_case
run_pm show 4
expect_exit 1
expect_stdout_empty
finish_case "show: gh fails"

new_case
issue_fixture "$body"
echo '{}' | fixture "$patch_key"
run_pm tick 4 "Serve AAAA records"
expect_exit 0
expect_stdout_empty
expect_sent "$patch_key" '{"body":"Serve the records over HTTP.\n\n- [ ] Serve A records\n- [x] Serve AAAA records\n- [ ] Reload dnsmasq\n\nExit criteria: dig answers for home.example.test."}'
finish_case "tick: one match"

new_case
printf -v crlf_body '%s\r\n' "Serve the records over HTTP." "" "- [ ] Serve A records" "- [ ] Serve AAAA records" "- [ ] Reload dnsmasq"
issue_fixture "$crlf_body"
echo '{}' | fixture "$patch_key"
run_pm tick 4 "Serve AAAA records"
expect_exit 0
expect_sent "$patch_key" '{"body":"Serve the records over HTTP.\r\n\r\n- [ ] Serve A records\r\n- [x] Serve AAAA records\r\n- [ ] Reload dnsmasq\r\n"}'
finish_case "tick: CRLF body"

# The backticks, backslash and $HOME are the literal item text under test.
# shellcheck disable=SC2016
special='Reject `*.[a]` \d & $HOME'
# shellcheck disable=SC2016
special_json='Reject `*.[a]` \\d & $HOME'
new_case
issue_fixture "- [ ] Reject \`*.[b]\` x
- [ ] $special
- [ ] $special too
- [ ] Reject a"
echo '{}' | fixture "$patch_key"
run_pm tick 4 "$special"
expect_exit 0
expect_sent "$patch_key" "{\"body\":\"- [ ] Reject \`*.[b]\` x\\n- [x] $special_json\\n- [ ] $special_json too\\n- [ ] Reject a\"}"
finish_case "tick: special characters"

new_case
issue_fixture '- [ ] Serve A records
  - [ ] Nested check
- [ ] Reload dnsmasq'
echo '{}' | fixture "$patch_key"
run_pm tick 4 "Nested check"
expect_exit 0
expect_sent "$patch_key" '{"body":"- [ ] Serve A records\n  - [x] Nested check\n- [ ] Reload dnsmasq"}'
finish_case "tick: nested item"

new_case
issue_fixture "$body"
echo '{}' | fixture "$patch_key"
run_pm tick 4 "Serve MX records"
expect_exit 1
expect_stdout_empty
expect_nothing_sent
finish_case "tick: no match"

new_case
issue_fixture "$body"
echo '{}' | fixture "$patch_key"
run_pm tick 4 "Serve A"
expect_exit 1
expect_nothing_sent
finish_case "tick: prefix is not a match"

new_case
issue_fixture '- [ ] Serve A records
- [ ] Reload dnsmasq
- [ ] Serve A records'
echo '{}' | fixture "$patch_key"
run_pm tick 4 "Serve A records"
expect_exit 1
expect_nothing_sent
finish_case "tick: several matches"

new_case
issue_fixture '- [x] Serve A records
- [ ] Reload dnsmasq'
echo '{}' | fixture "$patch_key"
run_pm tick 4 "Serve A records"
expect_exit 0
expect_stdout_empty
expect_nothing_sent
finish_case "tick: already ticked"

new_case
echo '{}' | fixture "$patch_key"
run_pm tick 4 "Serve A records"
expect_exit 1
expect_nothing_sent
finish_case "tick: gh fails on fetch"

new_case
issue_fixture "$body"
run_pm tick 4 "Serve A records"
expect_exit 1
finish_case "tick: gh fails on patch"

new_case
printf -v json_body '%s\n%s\t%s\n\n' '- [ ] Quote "x"' 'Path C:\dir' 'café'
issue_fixture "$json_body"
echo '{}' | fixture "$patch_key"
run_pm tick 4 'Quote "x"'
expect_exit 0
expect_sent "$patch_key" '{"body":"- [x] Quote \"x\"\nPath C:\\dir\tcafé\n\n"}'
finish_case "json: escapes"

new_case
issue_fixture "$(printf -- '- [ ] Serve A records\nbell \001 here')"
echo '{}' | fixture "$patch_key"
run_pm tick 4 "Serve A records"
expect_exit 0
expect_sent "$patch_key" '{"body":"- [x] Serve A records\nbell \u0001 here"}'
finish_case "json: control byte"

new_case
new_case
comment_fixtures octocat
comment_file "$comment_text"
run_pm comment 4 "$tmp/comment.md"
expect_exit 0
want created
expect_stdout_file "$tmp/want"
expect_sent "$post_key" "$comment_json"
expect_not_sent "$edit_key"
finish_case "comment: creates"

new_case
comment_fixtures octocat "$(printf '10\toctocat\t## Notes\n11\toctocat\t%s' "$marker")"
comment_file "$comment_text"
run_pm comment 4 "$tmp/comment.md"
expect_exit 0
want replaced
expect_stdout_file "$tmp/want"
expect_sent "$edit_key" "$comment_json"
expect_not_sent "$post_key"
finish_case "comment: replaces its own"

new_case
comment_fixtures octocat "$(printf '11\tmallory\t%s' "$marker")"
comment_file "$comment_text"
run_pm comment 4 "$tmp/comment.md"
expect_exit 0
want created
expect_stdout_file "$tmp/want"
expect_sent "$post_key" "$comment_json"
expect_not_sent "$edit_key"
finish_case "comment: ignores another author's marker"

new_case
comment_fixtures octocat "$(printf '11\toctocat\t<!-- project-manager:feat/5-other -->')"
comment_file "$comment_text"
run_pm comment 4 "$tmp/comment.md"
expect_exit 0
want created
expect_stdout_file "$tmp/want"
expect_sent "$post_key" "$comment_json"
expect_not_sent "$edit_key"
finish_case "comment: ignores another branch's marker"

new_case
comment_fixtures "" "$(printf '11\toctocat\t%s' "$marker")"
comment_file "$comment_text"
run_pm comment 4 "$tmp/comment.md"
expect_exit 0
want created
expect_stdout_file "$tmp/want"
expect_sent "$post_key" "$comment_json"
expect_not_sent "$edit_key"
finish_case "comment: unknown user creates"

new_case
comment_fixtures octocat
rm -f "$tmp/gh/$list_key"
comment_file "$comment_text"
run_pm comment 4 "$tmp/comment.md"
expect_exit 1
expect_stdout_empty
expect_stderr_has "cannot list the comments"
expect_nothing_sent
finish_case "comment: listing fails"

tilde='~'
new_case
comment_fixtures octocat
for line in "Logs are in /Users/someone/x." "Logs are in /home/someone/x." "Logs are in /root/x." \
	"$tilde/x holds the logs." "Logs are in $tilde/x." "Logs are in ($tilde/x)."; do
	expect_comment_refused "$line"
done
finish_case "comment: refuses a local path"

new_case
comment_fixtures octocat
for address in 192.168.1.10 172.16.0.1 10.1.2.3 169.254.1.1 100.64.0.1 100.127.255.1; do
	expect_comment_refused "Resolver at $address answered."
done
finish_case "comment: refuses a private address"

new_case
comment_fixtures octocat
for address in fd12:3456::1 FC00:abcd::1 fd00::53 "(fdab:1::2)"; do
	expect_comment_refused "Resolver at $address answered."
done
expect_comment_refused "fd12:3456::1 answered."
finish_case "comment: refuses a unique local IPv6 address"

new_case
comment_fixtures octocat
for token in ghp_x1 gho_x1 ghu_x1 ghs_x1 ghr_x1 github_pat_x1; do
	expect_comment_refused "Token $token leaked."
done
finish_case "comment: refuses a token"

new_case
comment_fixtures octocat
comment_file "$comment_text
Resolver at 10.0.0.53 answered."
run_pm comment 4 "$tmp/comment.md"
expect_exit 0
expect_sent "$post_key" '{"body":"<!-- project-manager:feat/4-serve -->\n## Progress on feat/4-serve\n\nDone\n- Serve A records (abc1234)\nResolver at 10.0.0.53 answered."}'
finish_case "comment: allows the example range"

new_case
comment_fixtures octocat
for line in "Commit 0bce7c6fd12 serves it." "Zone home.example.test answers." \
	"See https://github.com/home/x and https://github.com/root/x." "Needs v1.10.2." \
	"Word fdab alone." "Not abcfd12:3::1 either." "Ranges 100.63.0.1 100.128.0.1 169.253.0.1." \
	"Uses a_~/x and ghp alone."; do
	expect_comment_allowed "$line"
done
finish_case "comment: no false positives"

new_case
comment_fixtures octocat
rm -f "$tmp/gh/$post_key"
comment_file "$comment_text"
run_pm comment 4 "$tmp/comment.md"
expect_exit 1
expect_stdout_empty
finish_case "comment: gh fails on post"

if [ "$failures" -ne 0 ]; then
	echo "$failures case(s) failed"
	exit 1
fi
echo "all cases passed"
