#!/bin/bash
# Publishes the GitHub release of a pak tag (milestone M17), from the release
# job of .github/workflows/ci.yml:
#
#   vX.Y.Z              release marked "Latest" (the one the Pak Store reads),
#                       notes: the pak.json changelog entry of the version
#   vX.Y.Z-beta.N/rc.N  "Pre-release", never "Latest", notes: the
#                       "Unreleased" entry (pak.json is not changed for it)
#
# Any other tag (an app tag above all) stops here. On a re-run the release
# exists: its pre-release and Latest flags are set again from the tag before
# its zip is replaced, so a wrongly marked release is corrected.
#
# Usage: scripts/publish-pak-release.sh <tag> <zip>   (needs gh, GH_TOKEN, jq)
set -euo pipefail

tag="${1:?usage: publish-pak-release.sh <tag> <zip>}"
zip="${2:?usage: publish-pak-release.sh <tag> <zip>}"
root="$(cd "$(dirname "$0")/.." && pwd)"
notes="$(mktemp "${TMPDIR:-/tmp}/rl-release-notes.XXXXXX")"
trap 'rm -f "$notes"' EXIT

kind="$("$root/scripts/pak-release-kind.sh" "$tag")"
if [ "$kind" = latest ]; then
	jq -r --arg v "$tag" '.changelog[$v] // empty' "$root/pak.json" >"$notes"
	flags=(--prerelease=false --latest)
else
	{
		echo "Pre-release for testers, not offered by the Pak Store."
		echo
		jq -r '.changelog.Unreleased // empty' "$root/pak.json"
	} >"$notes"
	flags=(--prerelease --latest=false)
fi

if gh release view "$tag" >/dev/null 2>&1; then
	# Re-run: the flags first (a pre-release must never stay "Latest"),
	# then the zip.
	gh release edit "$tag" "${flags[@]}"
	gh release upload "$tag" "$zip" --clobber
else
	gh release create "$tag" "$zip" --verify-tag --title "$tag" --notes-file "$notes" "${flags[@]}"
fi
echo "Published $tag as $kind."
