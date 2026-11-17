#!/usr/bin/env bash
#
# Claude Code SessionStart hook, wired up in .claude/settings.json.
#
# Prepares a Claude Code cloud session for work on dnsmasq-manager: installs
# the superpowers plugin, dnsmasq and dig (for the integration tests) and
# golangci-lint (via `make tools`), downloads Go modules, puts the Go bin
# directory on PATH for the session, then prints a status block that Claude
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

have_golangci_lint() {
	have golangci-lint || { [ -n "$gopath" ] && [ -x "$gopath/bin/golangci-lint" ]; }
}

# Prints "present" or "missing" for a command on PATH.
presence() {
	if have "$1"; then echo present; else echo missing; fi
}

# Installs dnsmasq and dig with the given apt-get command prefix. Fresh images
# may have empty package lists, so a failed install is retried after an update.
apt_install() {
	"$@" install -y --no-install-recommends dnsmasq-base dnsutils && return 0
	"$@" update && "$@" install -y --no-install-recommends dnsmasq-base dnsutils
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

if ! have dnsmasq || ! have dig; then
	if [ "$(id -u)" = 0 ]; then
		apt_install apt-get
	elif have sudo && sudo -n true; then
		apt_install sudo -n apt-get
	else
		echo "session-start: not root and no passwordless sudo; skipping dnsmasq/dig install"
	fi
fi

if ! have_golangci_lint; then
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
if have_golangci_lint; then lint_status=present; else lint_status=missing; fi

{
	echo "dnsmasq-manager session setup (cloud)"
	echo "- go: $go_version"
	echo "- dnsmasq: $(presence dnsmasq)"
	echo "- dig: $(presence dig)"
	echo "- golangci-lint: $lint_status"
	echo "- superpowers: $superpowers"
	echo "Workflow: see CLAUDE.md. If superpowers is not loaded, follow the steps there by hand."
} >&3

exit 0
