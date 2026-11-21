#!/bin/bash
# App Store Connect steps of an app release (milestone M17), through the App
# Store Connect API (AppStoreConnect.swift: Foundation and CryptoKit only,
# run with the Swift of Xcode; no dependency).
#
#   asc.sh prepare <X.Y.Z>               create or update the App Store version: "What's New", automatic release
#   asc.sh submit <X.Y.Z>                prepare, wait for the build of this release, attach it, submit to App Review
#   asc.sh instructions <X.Y.Z> [build]  print the same steps to do by hand in App Store Connect
#   asc.sh selftest                      offline checks: key parsing, JWT, API address guard, build eligibility
#
# submit takes the build of the Xcode Cloud run of the Release workflow
# (RL_RELEASE_WORKFLOW) on the tag CI_TAG (ios-vX.Y.Z), found through the
# Xcode Cloud API: the newest run of the tag or, when ASC_BUILD_NUMBER is
# set, the run of the tag with that number (it only chooses among those
# runs; any other build is refused). Never just the newest upload of X.Y.Z:
# a TestFlight beta or a manual upload has the same version.
#
# The API key comes from the environment (secret variables of the Xcode
# Cloud workflow, or set by hand): ASC_KEY_ID, ASC_ISSUER_ID, and
# ASC_PRIVATE_KEY (the content of the .p8 file) or ASC_PRIVATE_KEY_PATH.
# The key and the tokens are never printed.
set -euo pipefail
# shellcheck source=common.sh
. "$(dirname "$0")/common.sh"

bundle_id="${ASC_BUNDLE_ID:-${CI_BUNDLE_ID:-link.reminis.app}}"
command="${1:-}"
version="${2:-}"

swift_tool() {
	xcrun --sdk macosx swift "$RL_CI_DIR/AppStoreConnect.swift" "$@"
}

needs_version() {
	[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || rl_fail "usage: asc.sh $command <X.Y.Z>"
}

case "$command" in
instructions)
	needs_version
	build="${3:-}"
	if [ -n "$build" ]; then
		build_line="choose build $build of version $version, after checking in Xcode Cloud that it is the build of the $RL_RELEASE_WORKFLOW run of ios-v$version, once Apple has processed it."
	else
		build_line="choose the build of the $RL_RELEASE_WORKFLOW run of ios-v$version (its build number is shown in Xcode Cloud), once Apple has processed it; not a beta build of the same version."
	fi
	cat <<EOF
ReminisLink: in App Store Connect (https://appstoreconnect.apple.com), app ReminisLink:
  1. Distribution > iOS App > "+" : version $version (if it does not exist yet).
  2. "What's New in This Version": paste ios/AppStore/en-US/release_notes.txt (English)
     and ios/AppStore/fr-FR/release_notes.txt (French).
  3. Version Release: "Automatically release this version".
  4. Build: $build_line
  5. "Add for Review", then "Submit for Review" (check that the submission holds nothing else).
Or set the App Store Connect API key (ASC_KEY_ID, ASC_ISSUER_ID, ASC_PRIVATE_KEY) and run
ios/ci_scripts/submit_for_review.sh with CI_TAG=ios-v$version (ASC_BUILD_NUMBER, optional, only
chooses among the builds of the $RL_RELEASE_WORKFLOW runs of that tag; see ios/README.md, "Release").
EOF
	;;
prepare | submit)
	needs_version
	rl_has_changelog_section "$version" || rl_fail "ios/CHANGELOG.md has no '## $version' section"
	notes="$(mktemp -d "${TMPDIR:-/tmp}/rl-whatsnew.XXXXXX")"
	trap 'rm -r "$notes"' EXIT
	args=()
	for lang in en-US fr-FR; do
		"$RL_CI_DIR/release_notes.sh" print "$version" "$lang" >"$notes/$lang.txt"
		args+=(--whats-new "$lang=$notes/$lang.txt")
	done
	if [ "$command" = submit ]; then
		# The Release run of the tag is always checked; a build number only
		# chooses among its builds.
		[ "${CI_TAG:-}" = "ios-v$version" ] || rl_fail "submit needs CI_TAG=ios-v$version"
		args+=(--tag "$CI_TAG" --release-workflow "$RL_RELEASE_WORKFLOW")
		[ -z "${ASC_BUILD_NUMBER:-}" ] || args+=(--build-number "$ASC_BUILD_NUMBER")
	fi
	swift_tool "$command" --bundle-id "$bundle_id" --version "$version" \
		--wait-minutes "${ASC_WAIT_MINUTES:-90}" "${args[@]}"
	;;
selftest)
	swift_tool selftest
	;;
*)
	rl_fail "usage: asc.sh prepare|submit|instructions <X.Y.Z> [build] | selftest"
	;;
esac
