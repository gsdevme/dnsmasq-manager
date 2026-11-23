#!/usr/bin/env bash
#
# Claude Code SessionStart hook, wired up in .claude/settings.json.
#
# Prepares a Claude Code cloud session for work on dnsmasq-manager: installs
# the superpowers plugin, dnsmasq and dig (for the integration tests), and
# what `make lint` runs (shellcheck with apt-get; golangci-lint and actionlint
# via `make tools`), downloads Go modules, puts the Go bin directory on PATH
# for the session, then prints a status block that Claude
# reads as session context. Anything already present is left alone, so a
# repeat start does no installing.
#
# Cloud-only: unless CLAUDE_CODE_REMOTE is "true" it exits at once, running
# nothing and printing nothing, so local sessions are unaffected.
#
# It never fails the session: there is no `set -e`, every step tolerates
# failure (a blocked network just leaves things missing) and the script always
# exits 0. Command output goes to stderr; only the status block goes to stdout.
#
# Needs bash 3.2 or newer; targets Ubuntu 24.04.

[ "${CLAUDE_CODE_REMOTE:-}" = "true" ] || exit 0

# Keep fd 3 as the real stdout for the status block; everything else goes to
# stderr, and no child can block reading the hook's JSON payload on stdin.
exec 3>&1 1>&2 </dev/null

project_dir=${CLAUDE_PROJECT_DIR:-$PWD}
plugin="superpowers@claude-plugins-official"

have() {
	command -v "$1" >/dev/null 2>&1
}

gopath=""
if have go; then
	gopath=$(go env GOPATH)
fi

# True when a tool `make tools` installs is on PATH or in GOPATH/bin, which
# is not on PATH until the session sources CLAUDE_ENV_FILE.
have_go_tool() {
	have "$1" || { [ -n "$gopath" ] && [ -x "$gopath/bin/$1" ]; }
}

# Prints "present" or "missing" for a command on PATH.
presence() {
	if have "$1"; then echo present; else echo missing; fi
}

# Prints "present" or "missing" for a tool `make tools` installs.
go_tool_presence() {
	if have_go_tool "$1"; then echo present; else echo missing; fi
}

# True when apt-get can be run as root: the hook is root or has passwordless
# sudo.
can_apt() {
	[ "$(id -u)" = 0 ] || { have sudo && sudo -n true; }
}

# Runs apt-get as root, through sudo when the hook is not root. Call it only
# after can_apt.
apt_get() {
	if [ "$(id -u)" = 0 ]; then apt-get "$@"; else sudo -n apt-get "$@"; fi
}

# Installs the given packages. Fresh images may have empty package lists, so
# a failed install is retried after an update.
apt_install() {
	apt_get install -y --no-install-recommends "$@" && return 0
	apt_get update && apt_get install -y --no-install-recommends "$@"
}

not_installed="NOT installed (install command failed or claude CLI unavailable)"
if ! have claude; then
	superpowers=$not_installed
else
	case "$(claude plugin list)" in
	*"$plugin"*)
		superpowers="already installed"
		;;
	*)
		claude plugin marketplace add anthropics/claude-plugins-official
		if claude plugin install "$plugin" --scope user; then
			superpowers="installed this session (loads after /reload-plugins or in the next session)"
		else
			superpowers=$not_installed
		fi
		;;
	esac
fi

if ! have dnsmasq || ! have dig || ! have shellcheck; then
	if can_apt; then
		# Separate installs, so a shellcheck package that cannot be found
		# does not take dnsmasq and dig down with it.
		if ! have dnsmasq || ! have dig; then
			apt_install dnsmasq-base dnsutils
		fi
		have shellcheck || apt_install shellcheck
	else
		echo "session-start: not root and no passwordless sudo; skipping dnsmasq/dig/shellcheck install"
	fi
fi

if ! have_go_tool golangci-lint || ! have_go_tool actionlint; then
	make -C "$project_dir" tools
fi

if have go; then
	(cd "$project_dir" && go mod download)
fi

if [ -n "${CLAUDE_ENV_FILE:-}" ] && [ -n "$gopath" ]; then
	# $PATH stays literal: it expands when Claude Code sources the file.
	# shellcheck disable=SC2016
	printf 'export PATH="$PATH:%s/bin"\n' "$gopath" >>"$CLAUDE_ENV_FILE"
fi

go_version="missing"
if have go; then
	go_version=$(go version)
fi

{
	echo "dnsmasq-manager session setup (cloud)"
	echo "- go: $go_version"
	echo "- dnsmasq: $(presence dnsmasq)"
	echo "- dig: $(presence dig)"
	echo "- shellcheck: $(presence shellcheck)"
	echo "- golangci-lint: $(go_tool_presence golangci-lint)"
	echo "- actionlint: $(go_tool_presence actionlint)"
	echo "- superpowers: $superpowers"
	echo "Workflow: see CLAUDE.md. If superpowers is not loaded, follow the steps there by hand."
} >&3

exit 0
