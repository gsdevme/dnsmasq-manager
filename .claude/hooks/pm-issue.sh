#!/usr/bin/env bash
#
# The project manager's one way to GitHub, and the helpers the pm-start.sh and
# pm-stop.sh hooks source.
#
# Run as `bash .claude/hooks/pm-issue.sh <subcommand>`:
#
#   link <n>              records issue <n> for the current branch
#   show <n>              prints the issue's headers, and its body when the
#                         author is a collaborator
#   tick <n> <text>       ticks the one unticked checklist item "<text>"
#   comment <n> <file>    creates or replaces this branch's progress comment
#                         and prints "created" or "replaced"
#
# GitHub is reached only through `gh api` (REST) with its built-in --jq; no
# GraphQL, no `gh issue` or `gh pr`, no separate jq. It never closes, reopens,
# creates or relabels an issue, and the only change it makes to a body is
# `- [ ]` to `- [x]` on one line. Usage and errors go to stderr with exit 1.
#
# Sourced, it only defines the pm_* functions. Per-clone state lives in
# <git-common-dir>/project-manager/. Needs bash 3.2 or newer, git, gh and a
# POSIX awk.

export LC_ALL=C

# Prints the origin repository as owner/repo: the last two path components of
# the origin URL, with any trailing .git dropped. Handles scp-style, https and
# proxy URLs. Returns 1 when there is no origin.
pm_repo_slug() {
	local url owner repo slash=/
	url=$(git remote get-url origin 2>/dev/null) || return 1
	url=${url%/}
	url=${url%.git}
	url=${url//:/$slash}
	repo=${url##*/}
	url=${url%/*}
	owner=${url##*/}
	[ -n "$owner" ] && [ -n "$repo" ] || return 1
	printf '%s/%s\n' "$owner" "$repo"
}

# Prints the current branch name; returns 1 on a detached HEAD.
pm_branch() {
	local branch
	branch=$(git symbolic-ref --quiet --short HEAD 2>/dev/null) || return 1
	[ -n "$branch" ] || return 1
	printf '%s\n' "$branch"
}

# Prints <branch> with every / written as _, for use in a state file name.
pm_flat() {
	printf '%s\n' "${1//\//_}"
}

# Creates the state directory, <git-common-dir>/project-manager, and prints
# its path. Returns 1 outside a repository or when it cannot be created.
pm_state_dir() {
	local common
	common=$(git rev-parse --git-common-dir 2>/dev/null) || return 1
	[ -n "$common" ] || return 1
	mkdir -p "$common/project-manager" 2>/dev/null || return 1
	printf '%s\n' "$common/project-manager"
}

# Returns 0 when <value> is an issue number: digits, no leading zero.
pm_is_number() {
	case $1 in
	'' | 0* | *[!0-9]*) return 1 ;;
	esac
	return 0
}

# Prints the issue linked to <branch>: the number recorded by `link` when
# there is one, else the digits in a name shaped <type>/<n>-<slug>. Returns 1
# for main and for a branch with neither.
pm_issue_for_branch() {
	local branch=$1 dir n="" re='^[^/]+/([1-9][0-9]*)-'
	[ -n "$branch" ] && [ "$branch" != main ] || return 1
	if dir=$(pm_state_dir) && [ -f "$dir/link-$(pm_flat "$branch")" ]; then
		read -r n <"$dir/link-$(pm_flat "$branch")"
		if pm_is_number "$n"; then
			printf '%s\n' "$n"
			return 0
		fi
	fi
	[[ $branch =~ $re ]] || return 1
	printf '%s\n' "${BASH_REMATCH[1]}"
}

# Reads stdin and prints it as one JSON string literal, quotes included,
# followed by a newline. Backslash, quote, tab, CR and LF are escaped as
# \\ \" \t \r \n, other bytes below 0x20 as \u00XX, and everything else passes
# through as is. One trailing newline of the input is dropped.
pm_json_string() {
	awk '
	BEGIN {
		for (i = 1; i < 32; i++)
			esc[sprintf("%c", i)] = sprintf("\\u%04x", i)
		esc["\t"] = "\\t"
		esc["\r"] = "\\r"
		esc["\""] = "\\\""
		esc["\\"] = "\\\\"
		printf "\""
	}
	{
		if (NR > 1)
			printf "\\n"
		n = length($0)
		for (i = 1; i <= n; i++) {
			c = substr($0, i, 1)
			printf "%s", ((c in esc) ? esc[c] : c)
		}
	}
	END { printf "\"\n" }
	'
}

# Reads stdin and prints {"body":<stdin as a JSON string>}, the request body
# for an issue or comment update.
pm_body_json() {
	local json
	json=$(pm_json_string)
	printf '{"body":%s}\n' "$json"
}

# Writes issue <n> to <file>: title, state, url and author_association header
# lines, an empty line, then the body from line 6. CR and LF in the title are
# written as a space. Returns non-zero when the repository is unknown, gh
# fails, or the file does not start with those four headers and the empty
# line.
pm_fetch_issue() {
	local slug
	slug=$(pm_repo_slug) || return 1
	gh api "repos/$slug/issues/$1" --jq '"title: \(.title | gsub("[\r\n]+"; " "))\nstate: \(.state)\nurl: \(.html_url)\nauthor_association: \(.author_association)\n\n\(.body // "")"' >"$2" || return 1
	awk '
	NR == 1 { ok = /^title: / }
	NR == 2 { ok = ok && /^state: / }
	NR == 3 { ok = ok && /^url: / }
	NR == 4 { ok = ok && /^author_association: / }
	NR == 5 {
		ok = ok && $0 == ""
		exit
	}
	END { exit ((ok && NR == 5) ? 0 : 1) }
	' "$2"
}

# Returns 0 when the show-format <file> has an author association of OWNER,
# MEMBER or COLLABORATOR, whose issue body may be shown.
pm_trusted_author() {
	case $(sed -n '4s/^author_association: //p' "$1") in
	OWNER | MEMBER | COLLABORATOR) return 0 ;;
	esac
	return 1
}

# Prints the number of the first line of <file> that holds something private
# and returns 0; returns 1 when there is none. Private is:
#   - a local path: /Users/<name>, /home/<name> or /root/<name> at the start
#     of a line or after a character outside [A-Za-z0-9_.-], or ~/ likewise;
#   - a private IPv4 address: 10.0.0.0/8 outside the 10.0.0.0/24 example
#     range, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16 or 100.64.0.0/10;
#   - a unique local IPv6 address: fc or fd and two hex digits, a colon, and
#     a further hex digit or colon, at the start of a line or after a
#     character outside [0-9A-Za-z:];
#   - a GitHub token: ghp_, gho_, ghu_, ghs_, ghr_ or github_pat_ and a
#     further [A-Za-z0-9_] character.
pm_private_content() {
	awk '
	$0 ~ /^\/(Users|home|root)\/[A-Za-z0-9_.-]/ || $0 ~ /[^A-Za-z0-9_.-]\/(Users|home|root)\/[A-Za-z0-9_.-]/ ||
	$0 ~ /^~\// || $0 ~ /[^A-Za-z0-9_.-]~\// ||
	$0 ~ /^[fF][cCdD][0-9a-fA-F][0-9a-fA-F]:[0-9a-fA-F:]/ || $0 ~ /[^0-9A-Za-z:][fF][cCdD][0-9a-fA-F][0-9a-fA-F]:[0-9a-fA-F:]/ ||
	$0 ~ /gh[pousr]_[A-Za-z0-9_]/ || $0 ~ /github_pat_[A-Za-z0-9_]/ {
		print NR
		found = 1
		exit
	}
	{
		s = $0
		while (match(s, /[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+/)) {
			split(substr(s, RSTART, RLENGTH), o, ".")
			s = substr(s, RSTART + RLENGTH)
			a = o[1] + 0
			b = o[2] + 0
			c = o[3] + 0
			if ((a == 10 && (b != 0 || c != 0)) || (a == 172 && b >= 16 && b <= 31) || (a == 192 && b == 168) ||
				(a == 169 && b == 254) || (a == 100 && b >= 64 && b <= 127)) {
				print NR
				found = 1
				exit
			}
		}
	}
	END { exit (found ? 0 : 1) }
	' "$1"
}

# Prints a message to stderr, prefixed with the script name.
pm_error() {
	printf 'pm-issue: %s\n' "$1" >&2
}

# Prints the usage summary to stderr.
pm_usage() {
	cat >&2 <<'EOF'
usage: pm-issue.sh link <n>
       pm-issue.sh show <n>
       pm-issue.sh tick <n> <text>
       pm-issue.sh comment <n> <file>
EOF
}

# link <n>: records issue <n> for the current branch, which may not be main
# or a detached HEAD.
pm_cmd_link() {
	local n=$1 branch dir
	branch=$(pm_branch) || {
		pm_error "detached HEAD: no branch to link"
		return 1
	}
	if [ "$branch" = main ]; then
		pm_error "main is never linked to an issue"
		return 1
	fi
	dir=$(pm_state_dir) || {
		pm_error "cannot create the state directory"
		return 1
	}
	printf '%s\n' "$n" >"$dir/link-$(pm_flat "$branch")"
}

# show <n>: prints the issue in the show format. The body is replaced by a
# one-line notice when its author is not a collaborator.
pm_cmd_show() {
	pm_fetch_issue "$1" "$pm_work/issue" || {
		pm_error "cannot fetch issue #$1"
		return 1
	}
	if pm_trusted_author "$pm_work/issue"; then
		cat "$pm_work/issue"
	else
		head -n 5 "$pm_work/issue"
		printf '%s\n' "The issue body is not shown: its author is not a collaborator on this repository."
	fi
}

# tick <n> <text>: changes the one line "- [ ] <text>" (leading spaces,
# trailing spaces and a CR allowed) to "- [x] <text>" and patches the body.
# An item that is already ticked is a no-op; no match, or several, is an
# error and sends nothing.
pm_cmd_tick() {
	local n=$1 text=$2 slug rc
	if [ -z "$text" ]; then
		pm_error "tick needs the item text"
		return 1
	fi
	slug=$(pm_repo_slug) || {
		pm_error "no origin remote"
		return 1
	}
	pm_fetch_issue "$n" "$pm_work/issue" || {
		pm_error "cannot fetch issue #$n"
		return 1
	}
	tail -n +6 "$pm_work/issue" >"$pm_work/body"
	PM_TEXT=$text awk '
	BEGIN { want = ENVIRON["PM_TEXT"] }
	{
		line[NR] = $0
		s = $0
		if (substr(s, length(s), 1) == "\r")
			s = substr(s, 1, length(s) - 1)
		while (length(s) > 0 && substr(s, length(s), 1) == " ")
			s = substr(s, 1, length(s) - 1)
		i = 1
		while (substr(s, i, 1) == " ")
			i++
		s = substr(s, i)
		if (s == "- [ ] " want) {
			open++
			at = NR
			col = i
		} else if (s == "- [x] " want || s == "- [X] " want) {
			done++
		}
	}
	END {
		if (open > 1)
			exit 3
		if (open == 0)
			exit (done ? 2 : 1)
		line[at] = substr(line[at], 1, col + 2) "x" substr(line[at], col + 4)
		for (k = 1; k <= NR; k++)
			print line[k]
	}
	' "$pm_work/body" >"$pm_work/ticked"
	rc=$?
	case $rc in
	0) ;;
	2) return 0 ;;
	1)
		pm_error "issue #$n has no unticked item: $text"
		return 1
		;;
	3)
		pm_error "issue #$n has several unticked items: $text"
		return 1
		;;
	*)
		pm_error "cannot read the body of issue #$n"
		return 1
		;;
	esac
	pm_body_json <"$pm_work/ticked" >"$pm_work/patch.json"
	gh api -X PATCH "repos/$slug/issues/$n" --input "$pm_work/patch.json" >/dev/null || {
		pm_error "cannot update issue #$n"
		return 1
	}
}

# comment <n> <file>: posts <file> as this branch's progress comment, under a
# first-line marker <!-- project-manager:<branch> -->, and prints "created" or
# "replaced". Replaces the first comment by the authenticated user that
# carries the marker, and creates one when there is none or the user cannot
# be read. Fails, sending nothing, when the user is known but the comments
# cannot be listed, and refuses a file that pm_private_content flags.
pm_cmd_comment() {
	local n=$1 file=$2 slug branch marker line login id who first target="" tab=$'\t'
	if [ ! -f "$file" ] || [ ! -r "$file" ]; then
		pm_error "cannot read $file"
		return 1
	fi
	if line=$(pm_private_content "$file"); then
		pm_error "comment refused: line $line of $file has a local path, a private address or a token"
		return 1
	fi
	slug=$(pm_repo_slug) || {
		pm_error "no origin remote"
		return 1
	}
	branch=$(pm_branch) || {
		pm_error "detached HEAD: no branch for the comment marker"
		return 1
	}
	marker="<!-- project-manager:$branch -->"
	{
		printf '%s\n' "$marker"
		cat "$file"
	} | pm_body_json >"$pm_work/comment.json"
	login=$(gh api user --jq .login 2>/dev/null) || login=""
	if [ -n "$login" ]; then
		gh api --paginate "repos/$slug/issues/$n/comments?per_page=100" --jq '.[] | [.id, .user.login, ((.body // "") | split("\n")[0] | rtrimstr("\r"))] | @tsv' >"$pm_work/comments" || {
			pm_error "cannot list the comments on issue #$n"
			return 1
		}
		while IFS=$tab read -r id who first || [ -n "$id" ]; do
			if [ "$who" = "$login" ] && [ "$first" = "$marker" ] && pm_is_number "$id"; then
				target=$id
				break
			fi
			id=""
		done <"$pm_work/comments"
	fi
	if [ -n "$target" ]; then
		gh api -X PATCH "repos/$slug/issues/comments/$target" --input "$pm_work/comment.json" >/dev/null || {
			pm_error "cannot update comment $target on issue #$n"
			return 1
		}
		echo replaced
	else
		gh api -X POST "repos/$slug/issues/$n/comments" --input "$pm_work/comment.json" >/dev/null || {
			pm_error "cannot create a comment on issue #$n"
			return 1
		}
		echo created
	fi
}

# Runs the subcommand named by the first argument after checking the argument
# count and the issue number.
pm_main() {
	local want
	case ${1:-} in
	link | show) want=2 ;;
	tick | comment) want=3 ;;
	*)
		pm_usage
		return 1
		;;
	esac
	if [ $# -ne "$want" ]; then
		pm_usage
		return 1
	fi
	if ! pm_is_number "$2"; then
		pm_error "not an issue number: $2"
		return 1
	fi
	case $1 in
	link) pm_cmd_link "$2" ;;
	show) pm_cmd_show "$2" ;;
	tick) pm_cmd_tick "$2" "$3" ;;
	comment) pm_cmd_comment "$2" "$3" ;;
	esac
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
	pm_tmp_root=${TMPDIR:-/tmp}
	pm_work=$(mktemp -d "${pm_tmp_root%/}/pm-issue.XXXXXX") || exit 1
	trap 'rm -rf "$pm_work"' EXIT
	pm_main "$@"
	exit
fi
