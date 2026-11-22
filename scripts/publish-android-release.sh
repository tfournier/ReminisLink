#!/bin/bash
# Publishes the GitHub release of an Android app tag (milestone M21), from
# .github/workflows/android-release.yml:
#
#   android-vX.Y.Z              a release, never "Latest"
#   android-vX.Y.Z-beta.N/rc.N  "Pre-release", never "Latest"
#
# "Latest" belongs to the pak's release: the NextUI Pak Store reads it. Any
# other tag stops here. The notes are the English and French changelogs of
# the F-Droid metadata for the tag's versionCode
# (android/app/fastlane/metadata/android/<locale>/changelogs/<versionCode>.txt),
# and say whether the APK is signed with the release key. On a re-run the
# release exists: its flags and notes are set again before its APK is
# replaced; a signed APK replaces a debug-signed one, never the reverse.
#
# Usage: scripts/publish-android-release.sh <tag> <apk> [signed|debug-signed]
#        (needs gh, GH_TOKEN)
set -euo pipefail

tag="${1:?usage: publish-android-release.sh <tag> <apk> [signed|debug-signed]}"
apk="${2:?usage: publish-android-release.sh <tag> <apk> [signed|debug-signed]}"
signing="${3:-signed}"
root="$(cd "$(dirname "$0")/.." && pwd)"
notes="$(mktemp "${TMPDIR:-/tmp}/rl-android-notes.XXXXXX")"
trap 'rm -f "$notes"' EXIT

kind="$("$root/scripts/android-release-kind.sh" "$tag")"
code="$("$root/scripts/android-version-code.sh" "$tag")"
metadata="$root/android/app/fastlane/metadata/android"
{
	if [ "$kind" = prerelease ]; then
		echo "Pre-release of the Android app for testers."
		echo
	fi
	if [ "$signing" = debug-signed ]; then
		echo "**Not for daily use:** this APK is signed with a throwaway debug key (the release key is not configured in this repository's secrets). Install the signed release from F-Droid or a later GitHub release instead."
		echo
	fi
	for locale in en-US fr-FR; do
		if [ -f "$metadata/$locale/changelogs/$code.txt" ]; then
			echo "### $locale"
			echo
			cat "$metadata/$locale/changelogs/$code.txt"
			echo
		fi
	done
} >"$notes"

if [ "$kind" = release ]; then
	flags=(--prerelease=false --latest=false)
else
	flags=(--prerelease --latest=false)
fi

if gh release view "$tag" >/dev/null 2>&1; then
	# A re-run. Never degrade a release that has a signed APK with a debug-signed one;
	# the notes are written again (signed or not), and a signed upload removes the
	# debug-signed APK of an earlier run without the key.
	assets="$(gh release view "$tag" --json assets -q '.assets[].name')"
	if [ "$signing" = debug-signed ] && printf '%s\n' "$assets" | grep -v -- '-debug-signed\.apk$' | grep -q '\.apk$'; then
		echo "Release $tag already has a signed APK: refusing to replace it with a debug-signed one (configure the release key secrets and run again)." >&2
		exit 1
	fi
	gh release edit "$tag" --notes-file "$notes" "${flags[@]}"
	gh release upload "$tag" "$apk" --clobber
	if [ "$signing" = signed ]; then
		{ printf '%s\n' "$assets" | grep -- '-debug-signed\.apk$' || true; } | while IFS= read -r old; do
			gh release delete-asset "$tag" "$old" --yes
		done
	fi
else
	gh release create "$tag" "$apk" --verify-tag --title "ReminisLink for Android ${tag#android-v}" --notes-file "$notes" "${flags[@]}"
fi
echo "Published $tag as an Android $kind ($signing)."
