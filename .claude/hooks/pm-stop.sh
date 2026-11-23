#!/usr/bin/env bash
#
# Claude Code Stop hook, wired up in .claude/settings.json.
#
# When a turn ends on a branch linked to an issue, and the branch has commits
# that origin/main lacks with HEAD already on origin/<branch>, it holds the
# turn open once and tells the session to dispatch the project-manager agent
# to sync the issue. A given HEAD is asked for once: it is recorded in the
# asked-<branch> state file before the block is printed, and if it cannot be
# recorded the hook stays silent rather than block on every turn.
#
# It works in the checkout it runs in, which may be a worktree or a
# subdirectory, and falls back to CLAUDE_PROJECT_DIR only outside a git
# repository.
#
# It reads only git state: no gh call, no network. It never fails the
# session: there is no `set -e`, it exits 0 whatever happens, and prints
# nothing unless it blocks. stop_hook_active is matched as a raw key on
# stdin, which text inside a JSON string (quotes escaped) cannot match.
#
# Needs bash 3.2 or newer; uses the helpers in pm-issue.sh.

pm_dir=${BASH_SOURCE[0]%/*}
[ "$pm_dir" = "${BASH_SOURCE[0]}" ] && pm_dir=.
# shellcheck source=.claude/hooks/pm-issue.sh
. "$pm_dir/pm-issue.sh" 2>/dev/null || exit 0

export GIT_TERMINAL_PROMPT=0

pm_input=$(cat)
exec </dev/null
if ! git rev-parse --git-common-dir >/dev/null 2>&1; then
	cd "${CLAUDE_PROJECT_DIR:-.}" 2>/dev/null || exit 0
fi

# Runs the hook: checks each condition in turn and, when all hold, records
# HEAD and prints the block decision.
pm_stop_main() {
	local branch n ahead head state asked last="" reason json
	printf '%s' "$pm_input" | tr '\n\r' '  ' |
		grep -q '"stop_hook_active"[[:space:]]*:[[:space:]]*true' && return 0
	branch=$(pm_branch) || return 0
	n=$(pm_issue_for_branch "$branch") || return 0
	ahead=$(git rev-list --count origin/main..HEAD 2>/dev/null) || return 0
	case $ahead in
	'' | *[!0-9]*) return 0 ;;
	esac
	[ "$ahead" -gt 0 ] || return 0
	git merge-base --is-ancestor HEAD "origin/$branch" 2>/dev/null || return 0
	head=$(git rev-parse HEAD 2>/dev/null) || return 0
	[ -n "$head" ] || return 0
	state=$(pm_state_dir project-manager) || return 0
	asked="$state/asked-$(pm_flat "$branch")"
	[ -f "$asked" ] && IFS= read -r last <"$asked"
	[ "$head" = "$last" ] && return 0
	printf '%s\n' "$head" 2>/dev/null >"$asked" || return 0
	reason="Commits on $branch are pushed and not yet synced to issue #$n. Dispatch the project-manager agent (Agent tool, subagent_type \"project-manager\") with the prompt \"Sync issue #$n with branch $branch\", relay its report in a line or two, then finish."
	json=$(printf '%s\n' "$reason" | pm_json_string)
	printf '{"decision":"block","reason":%s}\n' "$json"
}

pm_stop_main
exit 0
