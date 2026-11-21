#!/bin/bash
# App Store Connect steps of an app release (milestone M17), through the App
# Store Connect API (AppStoreConnect.swift: Foundation and CryptoKit only,
# run with the Swift of Xcode; no dependency).
#
#   asc.sh prepare <X.Y.Z>       create or update the App Store version: "What's New", automatic release
#   asc.sh submit <X.Y.Z>        prepare, wait for the processed build, attach it, submit to App Review
#   asc.sh instructions <X.Y.Z>  print the same steps to do by hand in App Store Connect
#   asc.sh selftest              offline checks of the JWT signing and of the key parsing
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
	cat <<EOF
ReminisLink: in App Store Connect (https://appstoreconnect.apple.com), app ReminisLink:
  1. Distribution > iOS App > "+" : version $version (if it does not exist yet).
  2. "What's New in This Version": paste ios/AppStore/en-US/release_notes.txt (English)
     and ios/AppStore/fr-FR/release_notes.txt (French).
  3. Version Release: "Automatically release this version".
  4. Build: once Apple has processed the build (TestFlight shows it), choose it.
  5. "Add for Review", then "Submit for Review".
Or set the App Store Connect API key (ASC_KEY_ID, ASC_ISSUER_ID, ASC_PRIVATE_KEY) and run
ios/ci_scripts/submit_for_review.sh with CI_TAG=ios-v$version (see ios/README.md, "Release").
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
	swift_tool "$command" --bundle-id "$bundle_id" --version "$version" \
		--wait-minutes "${ASC_WAIT_MINUTES:-90}" "${args[@]}"
	;;
selftest)
	swift_tool selftest
	;;
*)
	rl_fail "usage: asc.sh prepare|submit|instructions <X.Y.Z> | selftest"
	;;
esac
