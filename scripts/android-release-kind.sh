#!/bin/sh
# Prints the kind of GitHub release an Android app tag makes (milestone M21):
#
#   android-vX.Y.Z             release     (never "Latest": the NextUI Pak
#                                           Store reads the latest release,
#                                           which must stay the pak's)
#   android-vX.Y.Z-beta.N      prerelease  (marked "Pre-release")
#   android-vX.Y.Z-rc.N        prerelease
#
# Anything else exits 1, pak tags (vX.Y.Z) and iOS tags included. Used by
# .github/workflows/android-release.yml and scripts/publish-android-release.sh.
#
# Usage: scripts/android-release-kind.sh <tag>
set -eu

tag="${1:-}"
fail() {
	echo "Not an Android release tag: '$tag' (expected android-vX.Y.Z, android-vX.Y.Z-beta.N or android-vX.Y.Z-rc.N)" >&2
	exit 1
}
case "$tag" in
*[!A-Za-z0-9.-]* | "") fail ;;
esac
# The version itself is checked by the versionCode formula (limits included).
"$(dirname "$0")/android-version-code.sh" "$tag" >/dev/null 2>&1 || fail
if printf '%s\n' "$tag" | grep -Eq '^android-v[0-9]+\.[0-9]+\.[0-9]+$'; then
	echo release
elif printf '%s\n' "$tag" | grep -Eq '^android-v[0-9]+\.[0-9]+\.[0-9]+-(beta|rc)\.[0-9]+$'; then
	echo prerelease
else
	fail
fi
