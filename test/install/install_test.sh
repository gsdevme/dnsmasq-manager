#!/usr/bin/env bash
#
# Tests for install.sh.
#
# Each case runs the installer under $TEST_SH (default /bin/dash) in a clean
# environment (env -i) whose PATH is a per-case temp bin/ directory and
# nothing else. That directory holds symlinks to the basic commands the
# installer uses plus stub scripts for id, uname, wget and sha256sum, so no
# real network, architecture or user check is reached. Every stub appends
# "<name> <args>" to a call log.
#
# The wget stub serves files from the case's server/ directory, laid out
# like the release URLs under https://example.test/releases/. The released
# "binary" is a small shell script that prints `dnsmasq-manager <tag>`, and
# the sha256sum stub accepts a checksum file only when its hex is 64 "a"
# characters, so a case controls a checksum match by what it writes.
#
# Each case asserts on the installer's exit code, stdout, stderr, the call
# log and the install directory, and every case checks that the work
# directory and the staging file are gone afterwards.
#
# Needs bash (3.2 or newer) to run the harness and the shell named in
# TEST_SH to run install.sh. Run it with `make test-install`, or
# `TEST_SH=/bin/sh make test-install` to test another shell. Prints
# PASS/FAIL per case and exits non-zero if any fail.

root=$(cd "$(dirname "$0")/../.." && pwd)
script="$root/install.sh"
TEST_SH=${TEST_SH:-/bin/dash}
base_url="https://example.test/releases"
asset="dnsmasq-manager-linux-amd64"
hex_a=$(printf '%064d' 0 | tr 0 a)
hex_b=$(printf '%064d' 0 | tr 0 b)
failures=0

if [ ! -x "$TEST_SH" ]; then
	echo "TEST_SH=$TEST_SH is not an executable shell" >&2
	exit 1
fi
echo "running install.sh under $TEST_SH"

# Body of the wget stub, after the lines that set server and base: saves
# server/<path> for a URL base/<path> to the -O file, or fails like a 404.
IFS= read -r -d '' wget_body <<'EOF'
out="" url=""
while [ $# -gt 0 ]; do
	case $1 in
	-O) out=$2; shift 2 ;;
	*) url=$1; shift ;;
	esac
done
rest=${url#"$base"}
if [ "$rest" = "$url" ] || [ ! -f "$server/$rest" ]; then
	echo "wget: server returned error: HTTP/1.1 404 Not Found" >&2
	exit 1
fi
cp "$server/$rest" "$out"
EOF

# Body of the sha256sum stub, after the line that sets good: `-c <file>`
# passes only when the file's hex is the good one.
IFS= read -r -d '' sha256sum_body <<'EOF'
[ "$1" = -c ] || exit 2
read -r hex name <"$2"
if [ "$hex" = "$good" ]; then
	echo "$name: OK"
	exit 0
fi
echo "$name: FAILED"
exit 1
EOF

# Creates the temp tree for a case: bin/, server/, install/, work/ (the
# installer's TMPDIR), an Alpine 3.23 os-release file and an empty call log.
# Stubs id as root, uname as x86_64, and wget and sha256sum as described in
# the file comment. Sets tmp, bin, log, server and resets problems.
new_case() {
	local tmp_root=${TMPDIR:-/tmp}
	tmp=$(mktemp -d "${tmp_root%/}/install-test.XXXXXX")
	bin="$tmp/bin"
	log="$tmp/calls.log"
	server="$tmp/server"
	problems=""
	mkdir -p "$bin" "$server" "$tmp/install" "$tmp/work" "$tmp/home"
	: >"$log"
	os_release "ID=alpine" "VERSION_ID=3.23.8"
	local cmd
	for cmd in cat chmod cp env grep head mkdir mktemp mv rm sed wc; do
		ln -s "$(command -v "$cmd")" "$bin/$cmd"
	done
	stub id "echo 0"
	stub uname "echo x86_64"
	stub wget "server='$server'
base='$base_url/'
$wget_body"
	stub sha256sum "good='$hex_a'
$sha256sum_body"
}

# Writes the case's os-release file, one argument per line.
os_release() {
	printf '%s\n' "$@" >"$tmp/os-release"
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

# Writes the fake binary for <tag> to <file>: a script printing
# `dnsmasq-manager <tag>`.
fake_binary() {
	printf '#!/bin/sh\necho "dnsmasq-manager %s"\n' "$1" >"$2"
	chmod 0755 "$2"
}

# Publishes release <tag> under server/download/<tag>/ with a matching
# checksum file.
release() {
	local dir="$server/download/$1"
	mkdir -p "$dir"
	fake_binary "$1" "$dir/$asset"
	printf '%s  %s\n' "$hex_a" "$asset" >"$dir/$asset.sha256"
}

# Publishes release <tag> and also serves it as the latest release.
latest() {
	release "$1"
	mkdir -p "$server/latest/download"
	cp "$server/download/$1/$asset" "$server/download/$1/$asset.sha256" "$server/latest/download/"
}

# Puts an existing install of <tag> in install/ and keeps a copy to compare
# against with expect_unchanged.
existing() {
	fake_binary "$1" "$tmp/install/dnsmasq-manager"
	cp "$tmp/install/dnsmasq-manager" "$tmp/existing-copy"
}

# Runs <script> (default install.sh) with only the given VAR=value pairs
# added to the installer's settings. Sets status; stdout and stderr land in
# the case's temp dir.
run_script() {
	local file=$1
	shift
	env -i PATH="$bin" HOME="$tmp/home" TMPDIR="$tmp/work" \
		DNSMASQ_MANAGER_BASE_URL="$base_url" \
		DNSMASQ_MANAGER_INSTALL_DIR="$tmp/install" \
		DNSMASQ_MANAGER_OS_RELEASE="$tmp/os-release" \
		"$@" "$TEST_SH" "$file" >"$tmp/stdout" 2>"$tmp/stderr" </dev/null
	status=$?
}

run_install() {
	run_script "$script" "$@"
}

problem() {
	problems="$problems  - $1
"
}

expect_exit() {
	[ "$status" -eq "$1" ] || problem "exit code was $status, want $1"
}

expect_stdout_has() {
	grep -qF -- "$1" "$tmp/stdout" || problem "stdout lacks: $1"
}

expect_stdout_lacks() {
	grep -qF -- "$1" "$tmp/stdout" && problem "stdout has: $1"
}

expect_stderr_has() {
	grep -qF -- "$1" "$tmp/stderr" || problem "stderr lacks: $1"
}

expect_log_has() {
	grep -qF -- "$1" "$log" || problem "call log lacks: $1"
}

expect_log_lacks() {
	grep -q -- "$1" "$log" && problem "call log has a line matching: $1"
}

expect_log_empty() {
	[ -s "$log" ] && problem "call log is not empty"
}

# The installed binary runs and reports <tag>.
expect_installed() {
	local target="$tmp/install/dnsmasq-manager" got
	if [ ! -x "$target" ]; then
		problem "no executable installed at install/dnsmasq-manager"
		return
	fi
	got=$("$target" version)
	[ "$got" = "dnsmasq-manager $1" ] || problem "installed binary prints '$got', want 'dnsmasq-manager $1'"
}

expect_not_installed() {
	[ -e "$tmp/install/dnsmasq-manager" ] && problem "install/dnsmasq-manager exists"
}

# The binary put in place by `existing` is byte-for-byte unchanged.
expect_unchanged() {
	cmp -s "$tmp/existing-copy" "$tmp/install/dnsmasq-manager" ||
		problem "existing install/dnsmasq-manager was changed"
}

expect_mode_0755() {
	[ -n "$(find "$tmp/install/dnsmasq-manager" -type f -perm 0755 2>/dev/null)" ] ||
		problem "install/dnsmasq-manager is not a file with mode 0755"
}

# The work directory is empty and install/ holds nothing but the binary, so
# neither the mktemp work dir nor the staging file was left behind.
expect_clean() {
	local left
	left=$(find "$tmp/work" -mindepth 1)
	[ -z "$left" ] || problem "work dir not cleaned up: $left"
	left=$(find "$tmp/install" -mindepth 1 ! -name dnsmasq-manager)
	[ -z "$left" ] || problem "install dir holds extra files: $left"
}

# Reports the case as PASS or FAIL; on failure shows the problems, the
# installer's stdout and stderr, and the call log. Removes the case's temp
# dir.
finish_case() {
	if [ -z "$problems" ]; then
		echo "PASS: $1"
	else
		failures=$((failures + 1))
		echo "FAIL: $1"
		printf '%s' "$problems"
		echo "  --- install stdout ---"
		cat "$tmp/stdout"
		echo "  --- install stderr ---"
		cat "$tmp/stderr"
		echo "  --- call log ---"
		cat "$log"
	fi
	chmod -R u+w "$tmp"
	rm -rf "$tmp"
}

new_case
latest v0.2.0
run_install
expect_exit 0
expect_installed v0.2.0
expect_log_has "$base_url/latest/download/$asset"
expect_log_has "$base_url/latest/download/$asset.sha256"
expect_stdout_has "v0.2.0"
expect_stdout_lacks ": OK"
expect_clean
finish_case "latest: installs the latest release"

new_case
release v0.1.0
latest v0.2.0
run_install DNSMASQ_MANAGER_VERSION=v0.1.0
expect_exit 0
expect_installed v0.1.0
expect_log_has "$base_url/download/v0.1.0/$asset"
expect_log_has "$base_url/download/v0.1.0/$asset.sha256"
expect_log_lacks "latest/download"
expect_clean
finish_case "pinned: installs the given version"

new_case
release v0.1.0
latest v0.2.0
run_install DNSMASQ_MANAGER_VERSION=0.1.0
expect_exit 0
expect_installed v0.1.0
expect_log_has "$base_url/download/v0.1.0/$asset"
expect_clean
finish_case "version without v: is normalised to v0.1.0"

new_case
existing v0.1.0
latest v0.2.0
printf '%s  %s\n' "$hex_b" "$asset" >"$server/latest/download/$asset.sha256"
run_install
expect_exit 1
expect_unchanged
expect_log_has "sha256sum -c"
expect_stderr_has "dnsmasq-manager install:"
expect_stderr_has "checksum"
expect_stdout_lacks "FAILED"
expect_clean
finish_case "checksum mismatch: installs nothing, existing binary untouched"

new_case
latest v0.2.0
printf '%s  %s\n%s  %s\n' "$hex_a" "$asset" "$hex_a" "$asset" >"$server/latest/download/$asset.sha256"
run_install
expect_exit 1
expect_not_installed
expect_log_lacks "sha256sum"
expect_stderr_has "dnsmasq-manager install:"
expect_clean
finish_case "malformed checksum: two lines refused before sha256sum"

new_case
latest v0.2.0
printf '%s  %s\n' "$hex_a" "other" >"$server/latest/download/$asset.sha256"
run_install
expect_exit 1
expect_not_installed
expect_log_lacks "sha256sum"
expect_stderr_has "dnsmasq-manager install:"
expect_clean
finish_case "malformed checksum: another file name refused before sha256sum"

new_case
latest v0.2.0
printf '%s  %s\njunk' "$hex_a" "$asset" >"$server/latest/download/$asset.sha256"
run_install
expect_exit 1
expect_not_installed
expect_log_lacks "sha256sum"
expect_stderr_has "malformed checksum"
expect_clean
finish_case "malformed checksum: unterminated second line refused before sha256sum"

new_case
latest v0.2.0
printf '%s  %s' "$hex_a" "$asset" >"$server/latest/download/$asset.sha256"
run_install
expect_exit 0
expect_installed v0.2.0
expect_log_has "sha256sum -c"
expect_clean
finish_case "checksum without a final newline: one well-formed line accepted"

new_case
existing v0.1.0
latest v0.2.0
printf '#!/bin/sh\nexit 1\n' >"$server/latest/download/$asset"
run_install
expect_exit 1
expect_unchanged
expect_stderr_has "failed to run"
expect_stdout_lacks "installed version"
expect_clean
finish_case "new binary fails to run: existing binary untouched"

new_case
latest v0.2.0
mkdir "$tmp/install/dnsmasq-manager"
run_install
expect_exit 1
expect_log_lacks wget
expect_stderr_has "directory"
[ -d "$tmp/install/dnsmasq-manager" ] || problem "install/dnsmasq-manager is no longer a directory"
[ -z "$(find "$tmp/install/dnsmasq-manager" -mindepth 1)" ] || problem "installer wrote into install/dnsmasq-manager/"
expect_clean
finish_case "target is a directory: refused before any download"

new_case
latest v0.2.0
stub id "echo 1000"
run_install
expect_exit 1
expect_not_installed
expect_log_lacks wget
expect_stderr_has "root"
expect_clean
finish_case "non-root: refused before any download"

new_case
latest v0.2.0
stub uname "echo aarch64"
run_install
expect_exit 1
expect_not_installed
expect_log_lacks wget
expect_stderr_has "x86_64"
expect_clean
finish_case "aarch64: refused before any download"

new_case
latest v0.2.0
os_release "ID=debian" "VERSION_ID=13"
run_install
expect_exit 1
expect_not_installed
expect_log_lacks wget
expect_stderr_has "Alpine"
expect_clean
finish_case "Debian: refused before any download"

new_case
latest v0.2.0
os_release "ID=alpine" "VERSION_ID=3.22.4"
run_install
expect_exit 1
expect_not_installed
expect_log_lacks wget
expect_stderr_has "3.23"
expect_clean
finish_case "Alpine 3.22: refused before any download"

new_case
latest v0.2.0
os_release "ID=alpine" "VERSION_ID=3.3.0"
run_install
expect_exit 1
expect_not_installed
expect_log_lacks wget
expect_clean
finish_case "Alpine 3.3: refused (numeric, not string, compare)"

new_case
latest v0.2.0
os_release "ID=alpine" "VERSION_ID=4.0.0"
run_install
expect_exit 0
expect_installed v0.2.0
expect_clean
finish_case "Alpine 4.0: accepted (major above 3)"

new_case
latest v0.2.0
os_release "ID=alpine" "VERSION_ID=3.24.0"
run_install
expect_exit 0
expect_installed v0.2.0
expect_clean
finish_case "Alpine 3.24: accepted"

new_case
latest v0.2.0
os_release 'NAME="Alpine Linux"' 'ID="alpine"' 'VERSION_ID="3.23.0"'
run_install
expect_exit 0
expect_installed v0.2.0
expect_clean
finish_case "quoted VERSION_ID: accepted"

new_case
latest v0.2.0
os_release "ID='alpine'" "VERSION_ID='3.23.0'"
run_install
expect_exit 0
expect_installed v0.2.0
expect_clean
finish_case "single-quoted VERSION_ID: accepted"

new_case
latest v0.2.0
os_release "ID=alpine"
run_install
expect_exit 1
expect_not_installed
expect_log_lacks wget
expect_clean
finish_case "no VERSION_ID: refused before any download"

for os_version in '3.' '.5' '3.x'; do
	new_case
	latest v0.2.0
	os_release "ID=alpine" "VERSION_ID=$os_version"
	run_install
	expect_exit 1
	expect_not_installed
	expect_log_lacks wget
	expect_stderr_has "cannot read the Alpine version"
	expect_clean
	finish_case "VERSION_ID=$os_version: refused before any download"
done

new_case
latest v0.2.0
rm -f "$tmp/os-release"
run_install
expect_exit 1
expect_not_installed
expect_log_lacks wget
expect_stderr_has "$tmp/os-release"
expect_clean
finish_case "missing os-release: refused before any download"

new_case
existing v0.1.0
run_install
expect_exit 1
expect_unchanged
expect_stderr_has "dnsmasq-manager install:"
expect_stderr_has "download"
expect_clean
finish_case "download failure: existing binary untouched"

new_case
existing v0.1.0
latest v0.2.0
rm -f "$server/latest/download/$asset.sha256"
run_install
expect_exit 1
expect_unchanged
expect_stderr_has "download"
expect_clean
finish_case "checksum download failure: existing binary untouched"

new_case
existing v0.1.0
latest v0.2.0
run_install
expect_exit 0
expect_installed v0.2.0
expect_stdout_has "v0.1.0"
expect_stdout_has "v0.2.0"
expect_clean
finish_case "upgrade: prints previous and new version"

new_case
latest v0.2.0
run_install
first=$status
run_install
expect_exit 0
[ "$first" -eq 0 ] || problem "first run exit code was $first, want 0"
expect_installed v0.2.0
expect_clean
finish_case "re-run: second install succeeds"

new_case
latest v0.2.0
run_install
expect_exit 0
expect_mode_0755
expect_clean
finish_case "mode and staging: 0755, no staging file left"

new_case
existing v0.1.0
latest v0.2.0
stub mktemp "exec '$(command -v mktemp)' \"\$@\""
run_install
expect_exit 0
expect_installed v0.2.0
expect_mode_0755
expect_log_has "mktemp $tmp/install/.dnsmasq-manager.new.XXXXXX"
expect_clean
finish_case "staging file: unpredictable mktemp name in the install dir"

for version in 'v0.1.0/../x' 'v0.1 0' 'latest/../x' 'v' '../v0.1.0' 'v0.1.0?x=1'; do
	new_case
	release v0.1.0
	run_install "DNSMASQ_MANAGER_VERSION=$version"
	expect_exit 1
	expect_not_installed
	expect_log_lacks wget
	expect_stderr_has "dnsmasq-manager install:"
	expect_clean
	finish_case "unsafe version '$version': refused before any download"
done

new_case
latest v0.2.0
run_install DNSMASQ_MANAGER_INSTALL_DIR="$tmp/nope"
expect_exit 1
expect_log_lacks wget
expect_stderr_has "$tmp/nope"
[ -e "$tmp/nope" ] && problem "installer created $tmp/nope"
expect_clean
finish_case "missing install dir: refused before any download"

# Root can write to any directory, so this case needs a non-root harness.
if [ "$(id -u)" -ne 0 ]; then
	new_case
	latest v0.2.0
	chmod 0555 "$tmp/install"
	run_install
	expect_exit 1
	expect_log_lacks wget
	expect_stderr_has "$tmp/install"
	expect_clean
	finish_case "unwritable install dir: refused before any download"
else
	echo "SKIP: unwritable install dir (the harness runs as root)"
fi

for signal in HUP INT TERM; do
	new_case
	existing v0.1.0
	latest v0.2.0
	stub wget "kill -$signal \"\$PPID\"
exit 1"
	run_install
	[ "$status" -ne 0 ] || problem "exit code was 0, want non-zero"
	expect_unchanged
	[ "$(grep -c '^wget' "$log")" -eq 1 ] || problem "wget called more than once"
	expect_clean
	finish_case "SIG$signal during download: cleans up, existing binary untouched"
done

new_case
latest v0.2.0
total=$(wc -l <"$script")
n=1
while [ "$n" -lt "$total" ]; do
	head -n "$n" "$script" >"$tmp/truncated.sh"
	run_script "$tmp/truncated.sh"
	if [ -s "$log" ] || [ -e "$tmp/install/dnsmasq-manager" ]; then
		problem "script cut after line $n of $total ran a command"
		break
	fi
	n=$((n + 1))
done
rm -f "$tmp/truncated.sh"
[ "$total" -gt 1 ] 2>/dev/null || problem "install.sh has $total lines"
expect_log_empty
expect_not_installed
expect_clean
finish_case "truncated script: no cut point runs anything"

if [ "$failures" -ne 0 ]; then
	echo "$failures case(s) failed"
	exit 1
fi
echo "all cases passed"
