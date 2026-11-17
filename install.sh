#!/bin/sh
#
# Installs the dnsmasq-manager binary on Alpine Linux.
#
#   wget -qO- https://github.com/gsdevme/dnsmasq-manager/releases/latest/download/install.sh | sh
#
# What it does: checks that it runs as root on x86_64 Alpine 3.23 or later,
# downloads the static linux/amd64 binary and its SHA-256 checksum file for
# the chosen release, verifies the checksum, and moves the binary into place
# atomically (staged in the install directory, run once with `version`, then
# renamed over the old one). A failed check, download, checksum or run
# leaves any installed binary exactly as it was.
#
# What it does not change: nothing but that one binary. No service, user,
# config file or dnsmasq setting is created or touched.
#
# Settings, all from the environment:
#
#   DNSMASQ_MANAGER_VERSION      `latest` (default) or a release such as
#                                v0.1.0; 0.1.0 is read as v0.1.0.
#   DNSMASQ_MANAGER_BASE_URL     release base URL (default
#                                https://github.com/gsdevme/dnsmasq-manager/releases).
#   DNSMASQ_MANAGER_INSTALL_DIR  where the binary goes (default /usr/local/bin).
#   DNSMASQ_MANAGER_OS_RELEASE   os-release file to check (default
#                                /etc/os-release); a test seam.
#
# Exits 0 when the binary is installed, 1 on any refusal or failure, and
# 128+N when stopped by signal N (HUP, INT or TERM).
#
# The whole script is the body of main, called on the last line, so a
# download cut off part way through defines nothing runnable and runs
# nothing.

main() {
	set -eu

	asset=dnsmasq-manager-linux-amd64
	min_major=3
	min_minor=23
	work=""
	staging=""

	# Prints a progress or result line on stdout.
	say() {
		printf 'dnsmasq-manager install: %s\n' "$*"
	}

	# Prints an error on stderr and exits 1; the EXIT trap cleans up.
	die() {
		printf 'dnsmasq-manager install: %s\n' "$*" >&2
		exit 1
	}

	# Removes the work directory and the staging file. Safe to run more than
	# once and when neither exists yet.
	cleanup() {
		if [ -n "$staging" ]; then
			rm -f "$staging" || true
		fi
		if [ -n "$work" ]; then
			rm -rf "$work" || true
		fi
	}

	# Reads KEY from the os-release file without sourcing it: the first
	# KEY= line, with one pair of surrounding double or single quotes
	# removed.
	os_value() {
		os_value_v=$(sed -n "s/^$1=//p" "$os_release" | head -n 1)
		case $os_value_v in
		\"*\")
			os_value_v=${os_value_v#\"}
			os_value_v=${os_value_v%\"}
			;;
		\'*\')
			os_value_v=${os_value_v#\'}
			os_value_v=${os_value_v%\'}
			;;
		esac
		printf '%s\n' "$os_value_v"
	}

	# The EXIT trap does the cleanup. A signal trap exits with the usual
	# 128+N status, which runs the EXIT trap once in dash, BusyBox ash and
	# bash; a shell killed by an untrapped signal would skip it.
	trap cleanup EXIT
	trap 'exit 129' HUP
	trap 'exit 130' INT
	trap 'exit 143' TERM

	version=${DNSMASQ_MANAGER_VERSION:-latest}
	base_url=${DNSMASQ_MANAGER_BASE_URL:-https://github.com/gsdevme/dnsmasq-manager/releases}
	base_url=${base_url%/}
	install_dir=${DNSMASQ_MANAGER_INSTALL_DIR:-/usr/local/bin}
	os_release=${DNSMASQ_MANAGER_OS_RELEASE:-/etc/os-release}
	target="$install_dir/dnsmasq-manager"

	[ "$(id -u)" = 0 ] || die "must run as root"

	arch=$(uname -m)
	[ "$arch" = x86_64 ] || die "only x86_64 is supported, this machine is $arch"

	[ -r "$os_release" ] || die "cannot read $os_release"
	os_id=$(os_value ID)
	[ "$os_id" = alpine ] || die "only Alpine Linux is supported, this is '$os_id'"
	os_version=$(os_value VERSION_ID)
	major=${os_version%%.*}
	minor=${os_version#*.}
	minor=${minor%%.*}
	case $os_version in
	*.*) ;;
	*) major="" ;;
	esac
	for part in "$major" "$minor"; do
		case $part in
		'' | *[!0-9]*)
			die "cannot read the Alpine version from VERSION_ID='$os_version' in $os_release"
			;;
		esac
	done
	if [ "$major" -lt "$min_major" ] ||
		{ [ "$major" -eq "$min_major" ] && [ "$minor" -lt "$min_minor" ]; }; then
		die "needs Alpine $min_major.$min_minor or later, this is $os_version"
	fi

	case $version in
	latest) ;;
	[0-9]*) version="v$version" ;;
	esac
	case $version in
	latest) ;;
	v[0-9]*)
		case $version in
		*[!0-9A-Za-z.-]*) die "refusing unsafe version '$version'" ;;
		esac
		;;
	*) die "refusing unsafe version '$version': use latest or a release such as v0.1.0" ;;
	esac
	if [ "$version" = latest ]; then
		download_url="$base_url/latest/download"
	else
		download_url="$base_url/download/$version"
	fi

	[ -d "$install_dir" ] || die "install directory $install_dir does not exist"
	[ -w "$install_dir" ] || die "install directory $install_dir is not writable"
	[ ! -d "$target" ] || die "$target is a directory, not the binary"

	work=$(mktemp -d "${TMPDIR:-/tmp}/dnsmasq-manager.XXXXXX") ||
		die "cannot create a work directory"

	say "downloading $version from $download_url"
	wget -q -T 30 -O "$work/$asset" "$download_url/$asset" ||
		die "download failed: $download_url/$asset"
	wget -q -T 30 -O "$work/$asset.sha256" "$download_url/$asset.sha256" ||
		die "download failed: $download_url/$asset.sha256"

	lines=$(grep -c '' "$work/$asset.sha256" || true)
	if [ "$lines" -ne 1 ] ||
		! grep -qE "^[0-9a-f]{64}  $asset\$" "$work/$asset.sha256"; then
		die "malformed checksum file $download_url/$asset.sha256"
	fi
	if ! check=$(cd "$work" && sha256sum -c "$asset.sha256" 2>&1); then
		die "checksum mismatch for $asset, nothing installed: $check"
	fi

	previous=none
	if [ -e "$target" ]; then
		previous=$("$target" version 2>/dev/null) || previous=unknown
		previous=${previous#dnsmasq-manager }
	fi

	staging=$(mktemp "$install_dir/.dnsmasq-manager.new.XXXXXX") ||
		die "cannot create a staging file in $install_dir"
	cp "$work/$asset" "$staging" || die "cannot write $staging"
	chmod 0755 "$staging" || die "cannot set the mode of $staging"
	installed=$("$staging" version) ||
		die "the downloaded binary failed to run, nothing installed"
	mv -f "$staging" "$target" || die "cannot move the binary to $target"
	staging=""

	say "previous version: $previous"
	say "installed version: ${installed#dnsmasq-manager } at $target"
}

main "$@"
