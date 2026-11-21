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
set -euo pipefail
# shellcheck source=common.sh
. "$(dirname "$0")/common.sh"

[ "${CI_WORKFLOW:-}" = "$RL_RELEASE_WORKFLOW" ] || exit 0
[ "${CI_XCODEBUILD_ACTION:-}" = archive ] || exit 0
if [ "${CI_XCODEBUILD_EXIT_CODE:-0}" != 0 ]; then
	rl_log "the archive failed: nothing prepared in App Store Connect"
	exit 0
fi
rl_parse_tag "${CI_TAG:-}" || rl_fail "malformed app tag '${CI_TAG:-}'"
# Checked by ci_post_clone.sh already; checked again before anything is
# sent: a pre-release never goes to App Review.
[ "$RL_KIND" = release ] || rl_fail "${CI_TAG:-} is a pre-release: nothing goes to App Review"

if ! rl_has_asc_key; then
	rl_log "no App Store Connect API key (ASC_KEY_ID, ASC_ISSUER_ID, ASC_PRIVATE_KEY): prepare version $RL_VERSION by hand."
	"$RL_CI_DIR/asc.sh" instructions "$RL_VERSION"
	exit 0
fi
"$RL_CI_DIR/asc.sh" prepare "$RL_VERSION"
rl_log "version $RL_VERSION is ready in App Store Connect; the Submit workflow (or the steps of ios/README.md) submits build ${CI_BUILD_NUMBER:-?} once Apple has processed it."
