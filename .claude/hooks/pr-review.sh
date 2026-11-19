#!/usr/bin/env bash
#
# Claude Code PostToolUse (Bash) hook, wired up in .claude/settings.json.
#
# When a shell command of the session opened a pull request, starts a review
# of it in the background: a separate `claude -p "/code-review <effort> <n>"`
# run with a fresh context, this repository's hooks turned off and fixed
# lists of allowed and denied tools. The run only reads and reports: it is
# allowed only read-only tools and the gh and git subcommands that read, and
# denied the editing tools, `gh api`, `git push` and any --output option, so
# that an allow rule in the owner's other settings files cannot hand it a
# tool that writes (a deny rule wins over an allow rule). When it finishes,
# the hook posts what it printed as one comment on the pull request. The
# session is told so in one line, with the path of the run's log.
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
# racing runs never review one pull request twice. Beside it, the run's
# standard output goes to review-<n>.md and its standard error to
# review-<n>.log. After the run, in the same detached process, the hook
# builds the comment and posts it with one `gh api` POST.
#
# The output is a review only when it holds a fenced block opened by a line
# that is exactly ```json; the first such block is the findings list. When
# jq is on PATH and the block is an array of objects with string file,
# summary and failure_scenario fields, the body is that list rendered as
# Markdown, followed by any text after the block; otherwise it is the output
# as printed. Every occurrence of the checkout's path and a / is removed, so
# paths read repo-relative, and the result is kept in review-<n>.body.md.
# The comment is a marker line, a header line naming the commit HEAD was at
# when the review was launched, and that body cut at the last whole line
# within 60,000 bytes.
#
# Each outcome is appended to the log as a `pr-review:` line. A run that
# exits non-zero, that printed only whitespace, or whose comment cannot be
# posted releases the claim, so the next trigger reviews again. Output with
# no findings list, and a body that pm_private_content flags, are not posted
# and keep the claim, since a new run would likely repeat them.
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
# runs only git, gh, claude, nohup, awk, mkdir and rm, and jq when it is
# there. Needs bash 3.2 or newer; uses the helpers in pm-issue.sh.

pr_review_tools='Read,Grep,Glob,Agent,Skill,TodoWrite,Bash(gh pr view *),Bash(gh pr diff *),Bash(gh pr checks *),Bash(git diff *),Bash(git log *),Bash(git show *),Bash(git status *),Bash(git blame *),Bash(git rev-parse *),Bash(git merge-base *),Bash(git ls-files *),Bash(git cat-file *),Bash(git remote get-url *),Bash(git branch --show-current),Bash(head *),Bash(tail *),Bash(grep *),Bash(wc *),Bash(cat *),Bash(ls *)'
pr_review_denied='Edit,Write,NotebookEdit,Bash(gh api *),Bash(git push *),Bash(* --output*)'
# The backticks are literal Markdown and \( is jq interpolation.
# shellcheck disable=SC2016
pr_review_jq='if type != "array" or any(.[]; type != "object" or (.file | type) != "string" or (.summary | type) != "string" or (.failure_scenario | type) != "string") then error("not a findings list") else . end
| if length == 0 then "No findings."
  else
    (if length == 1 then "**1 finding.**" else "**\(length) findings.**" end),
    "",
    (to_entries[] | .key as $i | .value
      | "### \($i + 1). \(.summary)",
        "",
        "`\(.file)\(if .line == null then "" else ":\(.line)" end)`",
        "",
        .failure_scenario,
        "")
  end'

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

# Returns 0 when <file> holds a byte that is not whitespace; non-zero when it
# is empty, holds only whitespace, or cannot be read.
pr_review_has_findings() {
	awk '/[^[:space:]]/ { found = 1; exit } END { exit (found ? 0 : 1) }' "$1" 2>/dev/null
}

# Prints the lines of the first fenced block in <file> opened by a line that
# is exactly ```json and closed by a line that is exactly ```. Returns 1 when
# there is no such block.
pr_review_block() {
	awk '
	!open && $0 == "```json" {
		open = 1
		next
	}
	open && $0 == "```" {
		closed = 1
		exit
	}
	open { print }
	END { exit (closed ? 0 : 1) }
	' "$1"
}

# Prints the text of <file> after its first ```json block, from its first
# line that is not whitespace on.
pr_review_notes() {
	awk '
	part == 0 && $0 == "```json" {
		part = 1
		next
	}
	part == 1 && $0 == "```" {
		part = 2
		next
	}
	part == 2 && !started && $0 !~ /[^[:space:]]/ { next }
	part == 2 {
		started = 1
		print
	}
	' "$1"
}

# Prints the body of the comment, without its header, for the run output in
# <file>, whose findings list is <block>: the list rendered by pr_review_jq,
# then, when text follows the block, an empty line, a --- line, an empty
# line and that text. Prints <file> as it is when jq is missing or cannot
# render the list.
pr_review_render() {
	local rendered notes
	if command -v jq >/dev/null 2>&1 &&
		rendered=$(printf '%s\n' "$2" | jq -r "$pr_review_jq" 2>/dev/null) && [ -n "$rendered" ]; then
		printf '%s\n' "$rendered"
		notes=$(pr_review_notes "$1")
		[[ $notes =~ [^[:space:]] ]] && printf '\n---\n\n%s\n' "$notes"
		return 0
	fi
	awk '{ print }' "$1"
}

# Copies stdin to stdout with every occurrence of <dir> followed by / removed,
# matched as plain text, not as a pattern. With an empty <dir>, copies stdin
# unchanged.
pr_review_relative() {
	PR_REVIEW_PREFIX=${1:+$1/} awk '
	BEGIN {
		p = ENVIRON["PR_REVIEW_PREFIX"]
		n = length(p)
	}
	n == 0 {
		print
		next
	}
	{
		s = $0
		out = ""
		while ((i = index(s, p)) > 0) {
			out = out substr(s, 1, i - 1)
			s = substr(s, i + n)
		}
		print out s
	}
	'
}

# Prints the comment for a review at <effort> of commit <sha> (empty when
# unknown) of pull request <n>, whose body is in <file>: the marker line, the
# header line, an empty line, then the body cut at the last whole line within
# 60,000 bytes. A cut adds a closing ``` line when the kept part has an odd
# number of lines starting with ```, then an empty line and a line saying so.
# Counts bytes because pm-issue.sh sets LC_ALL=C.
pr_review_comment() {
	local when="when it was opened"
	[ -n "$2" ] && when="at commit $2"
	printf '%s\n' '<!-- pr-review -->' \
		"**Automated review.** A separate Claude Code run reviewed this pull request $when (\`/code-review $1\`). It has not seen the session that wrote the change." \
		''
	PR_REVIEW_CUT="The review was cut at 60,000 bytes; the full findings are in review-$3.md in the clone that ran the review." awk '
	{
		total += length($0) + 1
		if (total > 60000) {
			cut = 1
			exit
		}
		if (substr($0, 1, 3) == "```")
			fences++
		print
	}
	END {
		if (cut) {
			if (fences % 2)
				print "```"
			print ""
			print ENVIRON["PR_REVIEW_CUT"]
		}
	}
	' "$4"
}

# Builds and posts the comment for pull request <n>'s review at <effort> of
# commit <sha> in the checkout <top> (either may be empty), from the run
# output in <state>/review-<n>.md, on the pull request in repository <slug>.
# The body goes to <state>/review-<n>.body.md and the request to
# review-<n>.json. Appends the outcome to <state>/review-<n>.log unless the
# post failed. Returns 0 when posted, 1 when the post failed, 2 when
# pm_private_content flagged the body, and 3 when the output holds no
# findings list; nothing is sent in the last two cases.
pr_review_post() {
	local n=$1 slug=$2 effort=$3 state=$4 sha=$5 top=$6 md body log json block line
	md="$state/review-$n.md"
	body="$state/review-$n.body.md"
	log="$state/review-$n.log"
	json="$state/review-$n.json"
	if ! block=$(pr_review_block "$md"); then
		printf "pr-review: the run's output is not a findings list and was not posted; it is in %s\n" "$md" >>"$log"
		return 3
	fi
	pr_review_render "$md" "$block" | pr_review_relative "$top" >"$body" || return 1
	if line=$(pm_private_content "$body"); then
		printf 'pr-review: the findings were not posted: line %s of %s has a local path, a private address or a token\n' "$line" "$body" >>"$log"
		return 2
	fi
	pr_review_comment "$effort" "$sha" "$n" "$body" | pm_body_json >"$json" || return 1
	gh api -X POST "repos/$slug/issues/$n/comments" --input "$json" >/dev/null 2>>"$log" || return 1
	printf 'pr-review: the findings were posted on pull request #%s\n' "$n" >>"$log"
}

# Removes <claim> and appends "pr-review: <text>" to <log>.
pr_review_release() {
	rm -f "$1"
	printf 'pr-review: %s\n' "$3" >>"$2"
}

# Runs <command> [args...] through pr_review_exec, in a subshell that ignores
# SIGHUP, with stdout to <state>/review-<n>.md, stderr to
# <state>/review-<n>.log and stdin from /dev/null, then posts the comment
# through pr_review_post with <slug>, <effort>, <sha> and <top>. Releases
# <claim>, so that a later trigger can review the pull request again, when
# the command exits non-zero, when it printed only whitespace, and when the
# post fails.
pr_review_run() (
	local n=$1 slug=$2 effort=$3 claim=$4 state=$5 sha=$6 top=$7 log md status
	shift 7
	log="$state/review-$n.log"
	md="$state/review-$n.md"
	trap '' HUP
	pr_review_exec "$@" >"$md" 2>"$log" </dev/null
	status=$?
	if [ "$status" -ne 0 ]; then
		pr_review_release "$claim" "$log" "the review run exited with status $status; the claim was removed; its output is in $md"
		return 0
	fi
	if ! pr_review_has_findings "$md"; then
		pr_review_release "$claim" "$log" "the review printed no findings; the claim was removed"
		return 0
	fi
	pr_review_post "$n" "$slug" "$effort" "$state" "$sha" "$top"
	[ $? -eq 1 ] && pr_review_release "$claim" "$log" "the comment could not be posted; the claim was removed; the body is in $state/review-$n.body.md"
	return 0
)

# Prints the first 7 characters of the commit HEAD is at, or nothing when it
# cannot be read.
pr_review_short_sha() {
	local sha
	sha=$(git rev-parse HEAD 2>/dev/null) || return 0
	[[ $sha =~ ^[0-9a-f]+$ ]] && [ "${#sha}" -ge 7 ] && printf '%s\n' "${sha:0:7}"
}

# Launches the review run for pull request <n> of repository <slug>, holding
# the claim <claim>, with its files in <state>: under nohup when that is on
# PATH, detached in the background unless PR_REVIEW_FOREGROUND is set. The
# commit and the checkout's path are read here, at launch. Returns at once in
# the background case; the post is made by the detached process when the run
# ends.
pr_review_launch() {
	local n=$1 slug=$2 claim=$3 state=$4 effort sha top
	case ${PR_REVIEW_EFFORT:-} in
	low | medium | high | xhigh) effort=$PR_REVIEW_EFFORT ;;
	*) effort=medium ;;
	esac
	sha=$(pr_review_short_sha)
	top=$(git rev-parse --show-toplevel 2>/dev/null) || top=""
	set -- claude -p "/code-review $effort $n" \
		--settings '{"disableAllHooks": true}' \
		--permission-mode dontAsk \
		--disallowedTools "$pr_review_denied" \
		--allowedTools "$pr_review_tools"
	command -v nohup >/dev/null 2>&1 && set -- nohup "$@"
	if [ -n "${PR_REVIEW_FOREGROUND:-}" ]; then
		pr_review_run "$n" "$slug" "$effort" "$claim" "$state" "$sha" "$top" "$@"
	else
		pr_review_run "$n" "$slug" "$effort" "$claim" "$state" "$sha" "$top" "$@" >/dev/null 2>&1 </dev/null &
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
	pr_review_launch "$n" "$slug" "$claim" "$state"
	json=$(printf '%s\n' "Code review: a review of pull request #$n was started in the background. Findings will be posted on the pull request as one comment. Log: $log" | pm_json_string)
	printf '{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":%s}}\n' "$json"
}

pr_review_main
exit 0
