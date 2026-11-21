#!/bin/bash
# Xcode Cloud: runs after each xcodebuild action (milestone M17).
#
# Release workflow, archive action: prepares the App Store version X.Y.Z in
# App Store Connect (created if needed, "What's New" from ios/CHANGELOG.md in
# English and French, released automatically after approval).
#
# It cannot submit the build itself: Xcode Cloud uploads the archive to App
# Store Connect only after this script, and Apple processes it afterwards
# (several minutes). The Submit workflow (submit_for_review.sh) does the
# submission once the build is processed; without the API key, the steps to
# do by hand are printed and the build succeeds.
#
# A tagged archive is a delivery: when Xcode Cloud's metadata (CI_WORKFLOW,
# CI_BUILD_NUMBER, CI_XCODEBUILD_EXIT_CODE) is missing, the hook fails
# rather than guess. A failed archive (non-zero exit code) prepares nothing.
set -euo pipefail
# shellcheck source=common.sh
. "$(dirname "$0")/common.sh"

tag="${CI_TAG:-}"
if [ -n "$tag" ] && [ -z "${CI_XCODEBUILD_ACTION:-}" ]; then
	rl_fail "tag $tag: CI_XCODEBUILD_ACTION is not set, cannot tell whether this is a delivery archive"
fi
[ "${CI_XCODEBUILD_ACTION:-}" = archive ] || exit 0
if [ -z "${CI_WORKFLOW:-}" ]; then
	[ -z "$tag" ] || rl_fail "archive of tag $tag: CI_WORKFLOW is not set, cannot tell which workflow delivers it"
	exit 0
fi
[ "$CI_WORKFLOW" = "$RL_RELEASE_WORKFLOW" ] || exit 0
rl_parse_tag "$tag" || rl_fail "malformed app tag '$tag'"
# Checked by ci_post_clone.sh already; checked again before anything is
# sent: a pre-release never goes to App Review.
[ "$RL_KIND" = release ] || rl_fail "$tag is a pre-release: nothing goes to App Review"
[[ "${CI_BUILD_NUMBER:-}" =~ ^[1-9][0-9]*$ ]] ||
	rl_fail "archive of tag $tag: CI_BUILD_NUMBER '${CI_BUILD_NUMBER:-}' is not set or not a build number"
case "${CI_XCODEBUILD_EXIT_CODE:-}" in
"") rl_fail "archive of tag $tag: CI_XCODEBUILD_EXIT_CODE is not set, cannot tell whether the archive succeeded" ;;
0) ;;
*)
	rl_log "archive result $CI_XCODEBUILD_EXIT_CODE: nothing prepared in App Store Connect"
	exit 0
	;;
esac

if ! rl_has_asc_key; then
	rl_log "no App Store Connect API key (ASC_KEY_ID, ASC_ISSUER_ID, ASC_PRIVATE_KEY): prepare version $RL_VERSION by hand."
	"$RL_CI_DIR/asc.sh" instructions "$RL_VERSION" "$CI_BUILD_NUMBER"
	exit 0
fi
"$RL_CI_DIR/asc.sh" prepare "$RL_VERSION"
rl_log "version $RL_VERSION is ready in App Store Connect; the Submit workflow (or the steps of ios/README.md) submits build $CI_BUILD_NUMBER once Apple has processed it."
