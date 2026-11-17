#!/bin/sh
#
# End-to-end test of install.sh in Alpine 3.23, run by `make
# test-install-docker` inside the image from test/install/Dockerfile.
#
# Expects /dist, a read-only mount of `make dist VERSION=v0.0.0-e2e` output,
# and /src, the image's copy of install.sh and test/install/. It serves the
# release files from BusyBox httpd on 127.0.0.1, laid out like GitHub's
# release download URLs, and runs the real installer under BusyBox ash
# against the real binary: a tampered checksum, the happy path, a re-run, a
# pinned version given without the v, and a non-root user. It finishes by
# running the stub suite with TEST_SH=/bin/sh, so that suite also covers
# BusyBox ash.
#
# Prints one line per step and `e2e: all steps passed` at the end; exits 1
# at the first failed step.

set -eu

tag=v0.0.0-e2e
asset=dnsmasq-manager-linux-amd64
target=/usr/local/bin/dnsmasq-manager
www=/srv/www
server=http://127.0.0.1:8080

# Prints the failure, then the last installer run's output, and exits 1.
fail() {
	printf 'e2e: FAIL: %s\n' "$*" >&2
	if [ -f /tmp/e2e.out ]; then
		echo "--- install stdout ---" >&2
		cat /tmp/e2e.out >&2
		echo "--- install stderr ---" >&2
		cat /tmp/e2e.err >&2
	fi
	exit 1
}

step() {
	printf 'e2e: step %s\n' "$*"
}

# Runs /src/install.sh as root with the given VAR=value settings. Sets
# status; stdout and stderr land in /tmp/e2e.out and /tmp/e2e.err.
run_install() {
	status=0
	env "$@" sh /src/install.sh >/tmp/e2e.out 2>/tmp/e2e.err || status=$?
}

# The installed binary reports the e2e tag, is mode 0755, and neither a
# staging file nor a work directory is left behind.
expect_installed() {
	got=$("$target" version) || fail "installed binary does not run"
	[ "$got" = "dnsmasq-manager $tag" ] || fail "installed binary prints '$got'"
	mode=$(find "$target" -type f -perm 0755)
	[ -n "$mode" ] || fail "$target is not a file with mode 0755"
	left=$(find /usr/local/bin -name '.dnsmasq-manager.new.*')
	[ -z "$left" ] || fail "staging file left: $left"
	left=$(find /tmp -maxdepth 1 -name 'dnsmasq-manager.*')
	[ -z "$left" ] || fail "work dir left: $left"
}

step "1: serve the release files"
for f in "$asset" "$asset.sha256" install.sh; do
	[ -f "/dist/$f" ] || fail "/dist/$f is missing; mount make dist output at /dist"
done
cmp -s /dist/install.sh /src/install.sh || fail "/dist/install.sh differs from /src/install.sh"
mkdir -p "$www/releases/latest/download" "$www/releases/download/$tag" "$www/tampered/latest/download"
cp "/dist/$asset" "/dist/$asset.sha256" /dist/install.sh "$www/releases/latest/download/"
cp "/dist/$asset" "/dist/$asset.sha256" /dist/install.sh "$www/releases/download/$tag/"
cp "/dist/$asset" "$www/tampered/latest/download/"
sum=$(cat "/dist/$asset.sha256")
rest=${sum#?}
case $sum in
0*) first=1 ;;
*) first=0 ;;
esac
printf '%s%s\n' "$first" "$rest" >"$www/tampered/latest/download/$asset.sha256"
httpd -p 127.0.0.1:8080 -h "$www"
tries=0
until wget -q -O /dev/null "$server/releases/latest/download/$asset.sha256"; do
	tries=$((tries + 1))
	[ "$tries" -lt 20 ] || fail "httpd did not start"
	sleep 1
done

step "2: tampered checksum installs nothing"
run_install DNSMASQ_MANAGER_BASE_URL="$server/tampered"
[ "$status" -ne 0 ] || fail "install with a tampered checksum exited 0"
[ ! -e "$target" ] || fail "$target exists after a tampered checksum"
grep -q 'dnsmasq-manager install: checksum mismatch' /tmp/e2e.err || fail "no checksum mismatch message"

step "3: happy path"
run_install DNSMASQ_MANAGER_BASE_URL="$server/releases"
[ "$status" -eq 0 ] || fail "install exited $status"
expect_installed
grep -q "previous version: none" /tmp/e2e.out || fail "no previous version line"
grep -q "installed version: $tag" /tmp/e2e.out || fail "no installed version line"

step "4: re-run"
run_install DNSMASQ_MANAGER_BASE_URL="$server/releases"
[ "$status" -eq 0 ] || fail "re-run exited $status"
expect_installed
grep -q "previous version: $tag" /tmp/e2e.out || fail "re-run did not report the previous version"

step "5: pinned version without the v"
rm -rf "$www/releases/latest"
run_install DNSMASQ_MANAGER_BASE_URL="$server/releases" DNSMASQ_MANAGER_VERSION="${tag#v}"
[ "$status" -eq 0 ] || fail "pinned install exited $status"
expect_installed

step "6: non-root is refused"
adduser -D tester
status=0
su -s /bin/sh tester -c "DNSMASQ_MANAGER_BASE_URL=$server/releases sh /src/install.sh" \
	>/tmp/e2e.out 2>/tmp/e2e.err || status=$?
[ "$status" -ne 0 ] || fail "non-root install exited 0"
grep -q root /tmp/e2e.err || fail "non-root refusal does not name root"
cmp -s "/dist/$asset" "$target" || fail "non-root run changed $target"
rm -f /tmp/e2e.out /tmp/e2e.err

step "7: stub suite under BusyBox ash"
TEST_SH=/bin/sh bash /src/test/install/install_test.sh || fail "stub suite failed under /bin/sh"

echo "e2e: all steps passed"
