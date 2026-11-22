#!/bin/bash
# Publishes the GitHub release of a pak tag (milestone M17), from the release
# job of .github/workflows/ci.yml:
#
#   vX.Y.Z              release marked "Latest" (the one the Pak Store reads),
#                       notes: the pak.json changelog entry of the version
#   vX.Y.Z-beta.N/rc.N  "Pre-release", never "Latest", notes: the
#                       "Unreleased" entry (pak.json is not changed for it)
#
# Every zip given is attached: ReminisLink.pak.zip (NextUI, the asset the Pak
# Store downloads, release_filename in pak.json) and, from v1.1.0-beta.1 on,
# ReminisLink.knulli.zip. Any other tag (an app tag above all) stops here,
# and so does a missing zip, before gh is called. On a re-run the release
# exists: its pre-release and Latest flags are set again from the tag before
# its zips are replaced, so a wrongly marked release is corrected.
#
# Usage: scripts/publish-pak-release.sh <tag> <zip>...   (needs gh, GH_TOKEN, jq)
set -euo pipefail

usage="usage: publish-pak-release.sh <tag> <zip>..."
tag="${1:?$usage}"
shift
[ "$#" -gt 0 ] || { echo "$usage" >&2; exit 1; }
zips=("$@")
for zip in "${zips[@]}"; do
	[ -f "$zip" ] || { echo "No such zip: $zip" >&2; exit 1; }
done
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
	# then the zips.
	gh release edit "$tag" "${flags[@]}"
	gh release upload "$tag" "${zips[@]}" --clobber
else
	gh release create "$tag" "${zips[@]}" --verify-tag --title "$tag" --notes-file "$notes" "${flags[@]}"
fi
echo "Published $tag as $kind."
