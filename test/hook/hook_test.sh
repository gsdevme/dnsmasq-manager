#!/usr/bin/env bash
#
# Tests for .claude/hooks/session-start.sh.
#
# Each case runs the hook in a clean environment (env -i) whose PATH is a
# per-case temp bin/ directory and nothing else. That directory holds symlinks
# to a handful of basic commands plus stub scripts for everything the hook
# drives (claude, apt-get, sudo, id, go, make, dnsmasq, dig, golangci-lint),
# so no real tool on the machine can be reached. Every stub appends
# "<name> <args>" to a call log; the case then asserts on the hook's exit
# code, its stdout and that log.
#
# Needs only bash (3.2 or newer) and a POSIX userland. Run it with
# `make test-hook`. Prints PASS/FAIL per case and exits non-zero if any fail.

root=$(cd "$(dirname "$0")/../.." && pwd)
hook="$root/.claude/hooks/session-start.sh"
plugin="superpowers@claude-plugins-official"
failures=0

# Creates the temp tree for a case: bin/, home/, project/, gopath/ and an
# empty call log. Sets tmp, bin, log, project, gopath and resets problems.
new_case() {
	local tmp_root=${TMPDIR:-/tmp}
	tmp=$(mktemp -d "${tmp_root%/}/hook-test.XXXXXX")
	bin="$tmp/bin"
	log="$tmp/calls.log"
	project="$tmp/project"
	gopath="$tmp/gopath"
	problems=""
	mkdir -p "$bin" "$tmp/home" "$project" "$gopath"
	: >"$log"
	ln -s "$BASH" "$bin/bash"
	local cmd
	for cmd in cat grep id mkdir sed uname env; do
		ln -s "$(command -v "$cmd")" "$bin/$cmd"
	done
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

# Stubs for the commands every cloud case needs: a go that reports a version
# and the case's GOPATH, make, apt-get, and a claude with no plugins listed.
base_stubs() {
	stub go "case \"\$1\" in
version) echo 'go version go1.27.0 linux/amd64' ;;
env) echo '$gopath' ;;
esac
exit 0"
	stub make
	stub apt-get
	stub claude
}

# Runs the hook with only the given VAR=value pairs added to a minimal
# environment. Sets status; stdout and stderr land in the case's temp dir.
run_hook() {
	env -i PATH="$bin" HOME="$tmp/home" CLAUDE_PROJECT_DIR="$project" "$@" \
		"$BASH" "$hook" >"$tmp/stdout" 2>"$tmp/stderr" </dev/null
	status=$?
}

problem() {
	problems="$problems  - $1
"
}

expect_exit_0() {
	[ "$status" -eq 0 ] || problem "exit code was $status, want 0"
}

expect_stdout_empty() {
	[ -s "$tmp/stdout" ] && problem "stdout is not empty"
}

expect_log_empty() {
	[ -s "$log" ] && problem "call log is not empty"
}

expect_stdout_line() {
	grep -qxF -- "$1" "$tmp/stdout" || problem "stdout lacks line: $1"
}

expect_stdout_lacks() {
	grep -qF -- "$1" "$tmp/stdout" && problem "stdout has command output: $1"
}

expect_log_line() {
	grep -qxF -- "$1" "$log" || problem "call log lacks line: $1"
}

expect_log_lacks() {
	grep -q -- "$1" "$log" && problem "call log has a line matching: $1"
}

# Reports the case as PASS or FAIL; on failure shows the problems, the hook's
# stdout and stderr, and the call log. Removes the case's temp dir.
finish_case() {
	if [ -z "$problems" ]; then
		echo "PASS: $1"
	else
		failures=$((failures + 1))
		echo "FAIL: $1"
		printf '%s' "$problems"
		echo "  --- hook stdout ---"
		cat "$tmp/stdout"
		echo "  --- hook stderr ---"
		cat "$tmp/stderr"
		echo "  --- call log ---"
		cat "$log"
	fi
	rm -rf "$tmp"
}

new_case
base_stubs
stub id "echo 0"
stub dnsmasq
stub dig
stub golangci-lint
run_hook
expect_exit_0
expect_stdout_empty
expect_log_empty
finish_case "1 local session (CLAUDE_CODE_REMOTE unset) does nothing"

new_case
base_stubs
stub id "echo 0"
stub apt-get "echo apt-get-output"
stub make "echo make-output"
run_hook CLAUDE_CODE_REMOTE=true
expect_exit_0
expect_stdout_lacks "-output"
expect_log_line "claude plugin marketplace add anthropics/claude-plugins-official"
expect_log_line "claude plugin install $plugin --scope user"
expect_log_line "apt-get install -y --no-install-recommends dnsmasq-base dnsutils"
expect_log_line "make -C $project tools"
expect_stdout_line "- superpowers: installed this session (loads after /reload-plugins or in the next session)"
finish_case "2 cloud, nothing installed, root: installs everything"

new_case
base_stubs
stub claude "[ \"\$1 \$2\" = 'plugin list' ] && echo '$plugin'
exit 0"
stub dnsmasq
stub dig
stub golangci-lint
run_hook CLAUDE_CODE_REMOTE=true
expect_exit_0
expect_log_lacks "install"
expect_log_lacks "marketplace add"
expect_log_lacks "^make "
expect_log_lacks "apt-get"
expect_stdout_line "- superpowers: already installed"
expect_stdout_line "- dnsmasq: present"
expect_stdout_line "- dig: present"
expect_stdout_line "- golangci-lint: present"
finish_case "3 cloud, everything present: installs nothing"

new_case
base_stubs
stub claude "[ \"\$1 \$2\" = 'plugin install' ] && exit 1
exit 0"
run_hook CLAUDE_CODE_REMOTE=true
expect_exit_0
expect_stdout_line "- superpowers: NOT installed (install command failed or claude CLI unavailable)"
finish_case "4 cloud, plugin install fails: reports NOT installed"

new_case
base_stubs
rm -f "$bin/claude"
run_hook CLAUDE_CODE_REMOTE=true
expect_exit_0
expect_stdout_line "- superpowers: NOT installed (install command failed or claude CLI unavailable)"
finish_case "5 cloud, no claude CLI: reports NOT installed"

new_case
base_stubs
stub id "echo 1000"
stub sudo "exit 1"
run_hook CLAUDE_CODE_REMOTE=true
expect_exit_0
expect_log_lacks "apt-get"
expect_stdout_line "- dnsmasq: missing"
expect_stdout_line "- dig: missing"
finish_case "6 cloud, non-root without sudo: skips apt-get"

new_case
base_stubs
: >"$tmp/env-file"
run_hook CLAUDE_CODE_REMOTE=true CLAUDE_ENV_FILE="$tmp/env-file"
expect_exit_0
grep -qxF -- "export PATH=\"\$PATH:$gopath/bin\"" "$tmp/env-file" ||
	problem "CLAUDE_ENV_FILE lacks the GOPATH/bin export; it holds: $(cat "$tmp/env-file")"
finish_case "7 cloud, CLAUDE_ENV_FILE set: adds GOPATH/bin to PATH"

if [ "$failures" -ne 0 ]; then
	echo "$failures case(s) failed"
	exit 1
fi
echo "all cases passed"
