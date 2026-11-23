#!/usr/bin/env bash
#
# Tests for scripts/publish-release.sh, and for the two release-please
# settings it depends on.
#
# Each case runs the script in a clean environment (env -i) whose PATH is a
# per-case temp bin/ directory holding bash and a stub gh, and nothing else,
# so the script can never reach GitHub. The stub appends its arguments as one
# line to $GH_LOG and exits 1 when its second argument equals $GH_FAIL
# (upload or edit), 0 otherwise. The case then asserts on the script's exit
# code, its stderr and that log.
#
# Needs only bash (3.2 or newer) and a POSIX userland. Run it with
# `make test-release`. Prints PASS/FAIL per case and exits non-zero if any fail.

root=$(cd "$(dirname "$0")/../.." && pwd)
script="$root/scripts/publish-release.sh"
config="$root/release-please-config.json"
tag="v9.9.9"
files="install.sh dnsmasq-manager-linux-amd64 dnsmasq-manager-linux-amd64.sha256"
view_call="release view $tag --json isDraft --jq .isDraft"
failures=0

# Creates the temp tree for a case: bin/ with bash and the stub gh, and a
# dist/ holding the three release files and one file that is not a release
# file. The call log is not created; only the stub writes it. Sets tmp, bin,
# log, dist and resets problems.
#
# The stub gh appends its arguments to GH_LOG, exits 7 when its second
# argument is GH_FAIL (view, upload or edit), and answers `release view`
# with GH_DRAFT (default true), which is how the script asks whether the
# release is still a draft.
new_case() {
	local tmp_root=${TMPDIR:-/tmp}
	tmp=$(mktemp -d "${tmp_root%/}/release-test.XXXXXX") || exit 1
	bin="$tmp/bin"
	log="$tmp/calls.log"
	dist="$tmp/dist"
	problems=""
	mkdir -p "$bin" "$dist"
	ln -s "$BASH" "$bin/bash"
	local f
	for f in $files; do
		printf '%s\n' "$f" >"$dist/$f"
	done
	printf 'stray\n' >"$dist/stray.txt"
	# shellcheck disable=SC2016
	stub gh 'printf "%s\n" "$*" >>"$GH_LOG"
[ "$2" = "${GH_FAIL:-}" ] && exit 7
[ "$2" = view ] && printf "%s\n" "${GH_DRAFT:-true}"
exit 0'
}

# Writes an executable stub bin/<name> that runs <body> (shell code).
# Replaces any existing entry.
stub() {
	local name=$1 body=$2
	rm -f "$bin/$name"
	{
		printf '#!%s\n' "$BASH"
		printf '%s\n' "$body"
	} >"$bin/$name"
	chmod +x "$bin/$name"
}

# Runs the script with the given arguments. GH_FAIL and GH_DRAFT, when set in
# the caller, are passed to the stub. Sets status; stdout and stderr land in the case's
# temp dir.
run_publish() {
	[ -f "$script" ] || problem "$script does not exist"
	env -i PATH="$bin" GH_LOG="$log" GH_FAIL="${GH_FAIL:-}" GH_DRAFT="${GH_DRAFT:-true}" \
		"$BASH" "$script" "$@" >"$tmp/stdout" 2>"$tmp/stderr" </dev/null
	status=$?
}

problem() {
	problems="$problems  - $1
"
}

expect_exit_0() {
	[ "$status" -eq 0 ] || problem "exit code was $status, want 0"
}

expect_exit() {
	[ "$status" -eq "$1" ] || problem "exit code was $status, want $1"
}

expect_log_empty() {
	[ -s "$log" ] && problem "call log is not empty"
}

expect_stderr_has() {
	grep -q -- "$1" "$tmp/stderr" 2>/dev/null || problem "stderr does not say: $1"
}

expect_log_has() {
	grep -q -- "$1" "$log" 2>/dev/null || problem "call log has no line matching: $1"
}

expect_log_lacks() {
	[ -f "$log" ] && grep -q -- "$1" "$log" && problem "call log has a line matching: $1"
}

# Asserts a refusal: exit 2, no gh call, and <reason> on stderr, so a case
# passes only through the check it is named for.
expect_refusal() {
	expect_exit 2
	expect_log_empty
	expect_stderr_has "$1"
}

# Asserts the call log is exactly: the draft question, one upload of the
# three release files and nothing else with --clobber, then one publish, all
# for the same tag.
expect_upload_then_publish() {
	local lines=0 first="" second="" third="" f
	if [ -f "$log" ]; then
		lines=$(grep -c '' "$log")
		first=$(sed -n 1p "$log")
		second=$(sed -n 2p "$log")
		third=$(sed -n 3p "$log")
	fi
	[ "$lines" -eq 3 ] || problem "call log has $lines line(s), want 3"
	[ "$first" = "$view_call" ] || problem "line 1 is not '$view_call': $first"
	case $second in
	"release upload $tag "*" --clobber") ;;
	*) problem "line 2 is not an upload of $tag ending --clobber: $second" ;;
	esac
	for f in $files; do
		case " $second " in
		*" $dist/$f "*) ;;
		*) problem "line 2 does not name $dist/$f" ;;
		esac
	done
	case $second in
	*stray.txt*) problem "line 2 uploads a file that is not a release file: $second" ;;
	esac
	[ "$third" = "release edit $tag --draft=false" ] ||
		problem "line 3 is not 'release edit $tag --draft=false': $third"
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
		echo "  --- script stdout ---"
		cat "$tmp/stdout" 2>/dev/null
		echo "  --- script stderr ---"
		cat "$tmp/stderr" 2>/dev/null
		echo "  --- call log ---"
		cat "$log" 2>/dev/null
	fi
	rm -rf "$tmp"
}

new_case
run_publish "$tag" "$dist"
expect_exit_0
expect_upload_then_publish
finish_case "1 upload then publish"

new_case
GH_FAIL=upload run_publish "$tag" "$dist"
expect_exit 7
expect_log_has "^release upload $tag "
expect_log_lacks "^release edit"
finish_case "2 upload fails: does not publish, exits with gh's status"

new_case
GH_FAIL=edit run_publish "$tag" "$dist"
expect_exit 7
expect_upload_then_publish
finish_case "3 publish fails: exits with gh's status"

new_case
GH_DRAFT=false run_publish "$tag" "$dist"
expect_exit_0
expect_log_has "^$view_call\$"
expect_log_lacks "^release upload"
expect_log_lacks "^release edit"
finish_case "3a already published: uploads nothing"

new_case
GH_FAIL=view run_publish "$tag" "$dist"
expect_exit 7
expect_log_lacks "^release upload"
expect_log_lacks "^release edit"
finish_case "3b draft question fails: uploads nothing"

new_case
run_publish
expect_refusal "no tag given"
finish_case "4 no tag: refuses"

new_case
run_publish "" "$dist"
expect_refusal "no tag given"
finish_case "5 empty tag: refuses"

new_case
run_publish "$tag" "$tmp/missing"
expect_refusal "is not a directory"
finish_case "6 directory missing: refuses"

for missing in $files; do
	new_case
	rm -f "$dist/$missing"
	run_publish "$tag" "$dist"
	expect_refusal "$missing is missing"
	finish_case "7 $missing missing: refuses"
done

new_case
: >"$tmp/stdout"
: >"$tmp/stderr"
for key in draft force-tag-creation; do
	matches=$(grep -cE "\"$key\"[[:space:]]*:[[:space:]]*true" "$config")
	[ "$matches" -eq 1 ] ||
		problem "release-please-config.json has $matches line(s) setting \"$key\": true, want 1"
done
finish_case "8 config drafts and forces the tag"

if [ "$failures" -ne 0 ]; then
	echo "$failures case(s) failed"
	exit 1
fi
echo "all cases passed"
