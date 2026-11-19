#!/usr/bin/env bash
#
# Claude Code PostToolUse (Bash) hook, wired up in .claude/settings.json.
#
# When a shell command of the session opened a pull request, starts a review
# of it in the background: a separate `claude -p "/code-review <effort>
# --comment <n>"` run with a fresh context, this repository's hooks turned off
# and fixed lists of allowed and denied tools, which posts its findings on the
# pull request. The session is told so in one line, with the path of the
# run's log.
#
# A command opened a pull request when it holds the words `gh pr create`, or
# `gh api` with a path ending in /pulls and a POST method or a request field,
# and no GET method. The check is made on the command as the payload carries
# it, still JSON-escaped, and is loose on purpose. The pull request is the one
# whose https://github.com/<owner>/<repo>/pull/<n> URL comes last in the
# command's output (the tool_response part of the payload): gh prints it after
# a create, and the REST response carries it. A URL for another repository
# means the command opened nothing here. With no URL in the output, it is the
# open pull request whose head is the current branch, looked up on GitHub.
#
# The pull request is claimed by creating <git-common-dir>/pr-review/
# reviewed-<n> exclusively before the launch, so a look-alike command or two
# racing runs never review one pull request twice. The run's output goes to
# review-<n>.log beside it; when the run exits non-zero, a line saying so is
# appended there and the claim is removed, so the next trigger reviews again.
#
# It does nothing inside a review run (PR_REVIEW_NESTED set), in a cloud
# session (CLAUDE_CODE_REMOTE=true), or when claude or gh is missing.
# PR_REVIEW_EFFORT picks the effort: low, medium, high or xhigh; anything else
# gives medium. PR_REVIEW_FOREGROUND=1 runs the review in the foreground, for
# the tests. Its arguments are ignored: settings.json passes `create` or `api`
# only to tell its two handlers apart. It works in the checkout it runs in,
# and falls back to CLAUDE_PROJECT_DIR only outside a git repository.
#
# It never fails the session: there is no `set -e`, it exits 0 whatever
# happens, and prints nothing unless it launched a review. For a command that
# opened no pull request it runs no other program. Besides bash builtins it
# runs only git, gh, claude, nohup, awk, mkdir and rm. Needs bash 3.2 or
# newer; uses the helpers in pm-issue.sh.

pr_review_tools='Read,Grep,Glob,Agent,Skill,TodoWrite,Bash(gh pr view *),Bash(gh pr diff *),Bash(gh pr checks *),Bash(gh api *),Bash(git diff *),Bash(git log *),Bash(git show *),Bash(git status *),Bash(git blame *),Bash(git rev-parse *),Bash(git merge-base *),Bash(git ls-files *),Bash(git cat-file *),Bash(git remote get-url *),Bash(git branch --show-current),Bash(head *),Bash(tail *),Bash(grep *),Bash(wc *),Bash(jq *),Bash(cat *),Bash(ls *)'
pr_review_denied='Edit,Write,NotebookEdit,Bash(gh api *merge*),Bash(gh api */contents/*),Bash(gh api */git/*),Bash(gh api *-X DELETE*),Bash(gh api *--method DELETE*),Bash(gh api *-X PUT*),Bash(gh api *--method PUT*),Bash(gh api *-X PATCH*),Bash(gh api *--method PATCH*),Bash(* --output*),Bash(* --upload-pack*),Bash(* --exec*)'

[ -n "${PR_REVIEW_NESTED:-}" ] && exit 0
[ "${CLAUDE_CODE_REMOTE:-}" = true ] && exit 0

pr_review_lc_set=${LC_ALL+set}
pr_review_lc=${LC_ALL-}
pr_review_dir=${BASH_SOURCE[0]%/*}
[ "$pr_review_dir" = "${BASH_SOURCE[0]}" ] && pr_review_dir=.
# shellcheck source=.claude/hooks/pm-issue.sh
. "$pr_review_dir/pm-issue.sh" 2>/dev/null || exit 0

export GH_PROMPT_DISABLED=1 GIT_TERMINAL_PROMPT=0

pr_review_input=""
IFS= read -r -d '' pr_review_input
exec </dev/null

# Returns 0 when <command>, JSON-escaped as in the payload, looks like it
# opened a pull request: the words `gh pr create`; or `gh api` with a path
# ending in /pulls (then a quote, a space or the end), one of -X POST,
# --method POST, -f, -F, --field, --raw-field or --input, and no -X GET or
# --method GET in any case. A word may follow an escaped newline, tab or CR.
# False matches are expected and harmless.
pr_review_opens_pr() {
	local start='(^|[^A-Za-z0-9_-]|\\[nrt])' end='([^A-Za-z0-9_-]|$)'
	local create="${start}gh[[:space:]]+pr[[:space:]]+create$end"
	local api="${start}gh[[:space:]]+api$end"
	local pulls='/pulls(\\["nrt]|[[:space:]'\'']|$)'
	local write="(^|[[:space:]])(-X[[:space:]]*POST|--method[[:space:]=]*POST|-f|-F|--field|--raw-field|--input)$end"
	local get="(^|[[:space:]])(-X|--method)[[:space:]=]*[Gg][Ee][Tt]$end"
	[[ $1 =~ $create ]] && return 0
	[[ $1 =~ $api ]] && [[ $1 =~ $pulls ]] && [[ $1 =~ $write ]] && ! [[ $1 =~ $get ]]
}

# Prints the number in the last https://github.com/<owner>/<repo>/pull/<n>
# URL of the payload's tool_response part, the text after its unescaped
# "tool_response" key, and returns 0 when <owner>/<repo> is <slug>, compared
# without regard to case. Returns 1 when the payload has no tool_response or
# no such URL in it, and 2, printing nothing, when the URL is for another
# repository. Inside a JSON string every quote is escaped, so the quoted key
# text, unescaped, can only be the key itself.
pr_review_output_number() {
	local key='^.*"tool_response"[[:space:]]*:'
	local url='^.*https://github\.com/([A-Za-z0-9-]+/[A-Za-z0-9._-]+)/pull/([1-9][0-9]*)([^0-9]|$)'
	local rest repo n same=1
	[[ $pr_review_input =~ $key ]] || return 1
	rest=${pr_review_input:${#BASH_REMATCH[0]}}
	[[ $rest =~ $url ]] || return 1
	repo=${BASH_REMATCH[1]}
	n=${BASH_REMATCH[2]}
	shopt -s nocasematch
	[[ $repo == "$1" ]] && same=0
	shopt -u nocasematch
	[ "$same" -eq 0 ] || return 2
	printf '%s\n' "$n"
}

# Prints the number of the open pull request in repository <slug> whose head
# is the current branch, as GitHub reports it, which may be empty or not a
# number. Returns non-zero on a detached HEAD or when the lookup fails.
pr_review_branch_number() {
	local branch
	branch=$(pm_branch) || return 1
	branch=$(pr_review_encode "$branch")
	gh api "repos/$1/pulls?head=${1%%/*}:$branch&state=open&per_page=1" --jq '.[0].number // empty' 2>/dev/null
}

# Prints <text> percent-encoded for a URL query: every byte outside
# [A-Za-z0-9._~/-] is written as %XX. Relies on LC_ALL=C, set by pm-issue.sh,
# so that it walks bytes rather than characters.
pr_review_encode() {
	local s=$1 out="" c code hex i
	for ((i = 0; i < ${#s}; i++)); do
		c=${s:i:1}
		case $c in
		[A-Za-z0-9._~/-]) out+=$c ;;
		*)
			printf -v code '%d' "'$c"
			printf -v hex '%%%02X' $((code & 255))
			out+=$hex
			;;
		esac
	done
	printf '%s\n' "$out"
}

# Runs <command> [args...] in a subshell with the caller's LC_ALL restored and
# PR_REVIEW_NESTED=1 exported, replacing the subshell with the command.
pr_review_exec() {
	(
		if [ -n "$pr_review_lc_set" ]; then
			LC_ALL=$pr_review_lc
		else
			unset LC_ALL
		fi
		export PR_REVIEW_NESTED=1
		exec "$@"
	)
}

# Runs <command> [args...] through pr_review_exec, in a subshell that ignores
# SIGHUP, with its output and errors going to <log> and stdin from /dev/null.
# When it exits non-zero, removes <claim> and appends a line saying so to
# <log>, so that a later trigger can review the pull request again.
pr_review_run() (
	local claim=$1 log=$2 status
	shift 2
	trap '' HUP
	pr_review_exec "$@" >"$log" 2>&1 </dev/null
	status=$?
	if [ "$status" -ne 0 ]; then
		rm -f "$claim"
		printf 'pr-review: the review run exited with status %s; the claim was removed\n' "$status" >>"$log"
	fi
)

# Launches the review run for pull request <n>, holding the claim <claim>,
# with its output going to <log>: under nohup when that is on PATH, detached
# in the background unless PR_REVIEW_FOREGROUND is set. Returns at once in
# the background case.
pr_review_launch() {
	local n=$1 claim=$2 log=$3 effort
	case ${PR_REVIEW_EFFORT:-} in
	low | medium | high | xhigh) effort=$PR_REVIEW_EFFORT ;;
	*) effort=medium ;;
	esac
	set -- claude -p "/code-review $effort --comment $n" \
		--settings '{"disableAllHooks": true}' \
		--permission-mode dontAsk \
		--disallowedTools "$pr_review_denied" \
		--allowedTools "$pr_review_tools"
	command -v nohup >/dev/null 2>&1 && set -- nohup "$@"
	if [ -n "${PR_REVIEW_FOREGROUND:-}" ]; then
		pr_review_run "$claim" "$log" "$@"
	else
		pr_review_run "$claim" "$log" "$@" >/dev/null 2>&1 </dev/null &
	fi
}

# Runs the hook: checks the command, finds and claims the pull request, and
# launches its review.
pr_review_main() {
	local re='"command"[[:space:]]*:[[:space:]]*"(([^"\\]|\\.)*)"' slug n state claim log json
	[[ $pr_review_input =~ $re ]] || return 0
	pr_review_opens_pr "${BASH_REMATCH[1]}" || return 0
	command -v claude >/dev/null 2>&1 || return 0
	command -v gh >/dev/null 2>&1 || return 0
	if ! git rev-parse --git-common-dir >/dev/null 2>&1; then
		cd "${CLAUDE_PROJECT_DIR:-.}" 2>/dev/null || return 0
	fi
	slug=$(pm_repo_slug) || return 0
	n=$(pr_review_output_number "$slug")
	case $? in
	0) ;;
	1) n=$(pr_review_branch_number "$slug") || return 0 ;;
	*) return 0 ;;
	esac
	[[ $n =~ ^[1-9][0-9]*$ ]] || return 0
	state=$(pm_state_dir pr-review) || return 0
	claim="$state/reviewed-$n"
	pm_claim "$claim" || return 0
	log="$state/review-$n.log"
	pr_review_launch "$n" "$claim" "$log"
	json=$(printf '%s\n' "Code review: a review of pull request #$n was started in the background. Findings will be posted on the pull request. Log: $log" | pm_json_string)
	printf '{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":%s}}\n' "$json"
}

pr_review_main
exit 0
