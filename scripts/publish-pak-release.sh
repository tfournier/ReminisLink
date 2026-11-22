#!/bin/bash
# Publishes the GitHub release of a pak tag (milestone M17), from the release
# job of .github/workflows/ci.yml:
#
#   vX.Y.Z              release marked "Latest" (the one the Pak Store reads),
#                       notes: the pak.json changelog entry of the version
#   vX.Y.Z-beta.N/rc.N  "Pre-release", never "Latest", notes: the
#                       "Unreleased" entry (pak.json is not changed for it)
#
# Both zips are required and attached: ReminisLink.pak.zip (NextUI, the asset
# the Pak Store downloads, release_filename in pak.json) and, from
# v1.1.0-beta.1 on, ReminisLink.knulli.zip. Any other tag (an app tag above
# all), a missing zip or a zip of another name stops here, before gh is
# called.
#
# On a re-run the release exists: its pre-release and Latest flags are set
# again from the tag (a wrongly marked release is corrected), then each zip
# is compared with the asset of the same name by its SHA-256 (the digest
# given by the API, or the asset downloaded when the API gives none). A
# published zip is never deleted nor replaced: an identical asset is left
# as it is, a missing one is uploaded, and a different one stops the script
# with an error before anything is uploaded (to be sorted out by hand).
#
# Usage: scripts/publish-pak-release.sh <tag> <ReminisLink.pak.zip> <ReminisLink.knulli.zip>
#        (needs gh, GH_TOKEN, jq)
set -euo pipefail

usage="usage: publish-pak-release.sh <tag> <ReminisLink.pak.zip> <ReminisLink.knulli.zip>"
[ "$#" -eq 3 ] || { echo "$usage" >&2; exit 1; }
tag="$1"
zips=("$2" "$3")
names=(ReminisLink.pak.zip ReminisLink.knulli.zip)
for i in 0 1; do
	[ "$(basename "${zips[$i]}")" = "${names[$i]}" ] || { echo "$usage (got ${zips[$i]})" >&2; exit 1; }
	[ -f "${zips[$i]}" ] || { echo "No such zip: ${zips[$i]}" >&2; exit 1; }
done
root="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/rl-release.XXXXXX")"
trap 'rm -rf "$work"' EXIT
notes="$work/notes"

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

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

if ! gh release view "$tag" >/dev/null 2>&1; then
	gh release create "$tag" "${zips[@]}" --verify-tag --title "$tag" --notes-file "$notes" "${flags[@]}"
	echo "Published $tag as $kind."
	exit 0
fi

# Re-run: the flags first (a pre-release must never stay "Latest").
gh release edit "$tag" "${flags[@]}"

# The assets of the release, one "<name><TAB><digest>" line each (digest:
# "sha256:<hex>", or empty when the API gives none).
gh api "repos/{owner}/{repo}/releases/tags/$tag" --jq '.assets[] | [.name, (.digest // "")] | @tsv' >"$work/assets"

# Every zip is checked before anything is uploaded.
missing=()
for i in 0 1; do
	zip="${zips[$i]}"
	name="${names[$i]}"
	line="$(awk -F'\t' -v n="$name" '$1 == n { print; exit }' "$work/assets")"
	if [ -z "$line" ]; then
		missing+=("$zip")
		continue
	fi
	built="$(sha256_of "$zip")"
	digest="$(printf '%s\n' "$line" | cut -f2)"
	case "$digest" in
	sha256:?*) published="${digest#sha256:}" ;;
	*)
		mkdir -p "$work/download"
		gh release download "$tag" --pattern "$name" --dir "$work/download" ||
			{ echo "Cannot download the published $name of $tag" >&2; exit 1; }
		[ -f "$work/download/$name" ] || { echo "Cannot download the published $name of $tag" >&2; exit 1; }
		published="$(sha256_of "$work/download/$name")"
		;;
	esac
	if [ "$published" != "$built" ]; then
		echo "The published $name of $tag differs from the zip built now (sha256 $published, built $built)." >&2
		echo "Nothing was uploaded. Sort it out by hand: keep the published zip, or delete it on GitHub and run the job again." >&2
		exit 1
	fi
	echo "$name of $tag already published and identical: kept."
done
if [ "${#missing[@]}" -gt 0 ]; then
	gh release upload "$tag" "${missing[@]}"
fi
echo "Published $tag as $kind."
