#!/bin/sh
# Prints the kind of GitHub release a pak tag makes (milestone M17):
#
#   vX.Y.Z               latest      (the release the NextUI Pak Store reads)
#   vX.Y.Z-beta.N        prerelease  (marked "Pre-release", never "Latest")
#   vX.Y.Z-rc.N          prerelease
#
# Anything else exits 1, app tags (ios-vX.Y.Z...) included: only a pak
# release can ever be "Latest". Used by the Makefile (make vX.Y.Z,
# check-version) and by the release job of .github/workflows/ci.yml.
#
# Usage: scripts/pak-release-kind.sh <tag>
set -eu

tag="${1:-}"
fail() {
	echo "Not a pak release tag: '$tag' (expected vX.Y.Z, vX.Y.Z-beta.N or vX.Y.Z-rc.N)" >&2
	exit 1
}
case "$tag" in
*[!A-Za-z0-9.-]* | "") fail ;;
esac
if printf '%s\n' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(\.[0-9]+)?$'; then
	echo latest
elif printf '%s\n' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(\.[0-9]+)?-(beta|rc)\.[0-9]+$'; then
	echo prerelease
else
	fail
fi
