#!/usr/bin/env bash
#
# Attaches the release files to a draft GitHub release and then publishes it.
#
# Called by the assets job in .github/workflows/release.yml, after `make dist`:
#
#   scripts/publish-release.sh <tag> <dir>
#
# release-please creates the release as a draft, so `releases/latest` stays
# on the previous release until this script publishes the new one. It needs
# `gh` on PATH with a token that can write the repository's contents.
#
# It refuses, with exit 2 and before any gh call, when <tag> is empty or
# missing, when <dir> is not a directory, or when <dir> lacks any of the
# three release files (the binary, its checksum and install.sh): an empty or
# half-built dist/ must not become the latest release. The names are the
# ones `make dist` writes and install.sh downloads; a new release file is
# added here as well.
#
# Otherwise it uploads exactly those three files, whatever else <dir> holds,
# and then publishes, stopping at the first gh call that fails and exiting
# with its status, so a failed upload leaves the release a draft.
#
# A re-run is safe. While the release is a draft the upload uses --clobber,
# so files a partial run attached are replaced. Once the release is
# published the script uploads nothing and exits 0: replacing the files of a
# published release would leave it without them for a moment. Re-running the
# failed job finishes the release. Finish a failed release before the next
# one is cut: publishing an older draft afterwards may make it the latest
# release.

set -eu

tag=${1:-}
dir=${2:-}

if [ -z "$tag" ]; then
	echo "publish-release: no tag given; usage: publish-release.sh <tag> <dir>" >&2
	exit 2
fi
if [ ! -d "$dir" ]; then
	echo "publish-release: '$dir' is not a directory" >&2
	exit 2
fi
paths=()
for file in dnsmasq-manager-linux-amd64 dnsmasq-manager-linux-amd64.sha256 install.sh; do
	if [ ! -f "$dir/$file" ]; then
		echo "publish-release: $dir/$file is missing; refusing to publish an incomplete release" >&2
		exit 2
	fi
	paths+=("$dir/$file")
done

draft=$(gh release view "$tag" --json isDraft --jq .isDraft)
if [ "$draft" != true ]; then
	echo "publish-release: $tag is already published; nothing to upload" >&2
	exit 0
fi

gh release upload "$tag" "${paths[@]}" --clobber
gh release edit "$tag" --draft=false
