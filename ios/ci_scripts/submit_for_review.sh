#!/bin/bash
# Submits the app release of CI_TAG (ios-vX.Y.Z) to App Review (milestone
# M17). Run by ci_post_clone.sh in the Submit workflow of Xcode Cloud, or by
# hand on a Mac:
#
#   ASC_KEY_ID=... ASC_ISSUER_ID=... ASC_PRIVATE_KEY_PATH=~/AuthKey_XXXX.p8 \
#     CI_TAG=ios-v1.0.1 ios/ci_scripts/submit_for_review.sh
#
# Waits (ASC_WAIT_MINUTES, default 90) until the newest build of version
# X.Y.Z is processed, attaches it to the App Store version (created if
# needed, with its "What's New") and submits it. A pre-release tag submits
# nothing; without the API key the steps to do by hand are printed. Never
# prints a key or a token.
set -euo pipefail
# shellcheck source=common.sh
. "$(dirname "$0")/common.sh"

rl_parse_tag "${CI_TAG:-}" || rl_fail "malformed or missing app tag '${CI_TAG:-}' (expected ios-vX.Y.Z)"
if [ "$RL_KIND" != release ]; then
	rl_log "${CI_TAG} is a pre-release (TestFlight only): nothing to submit to App Review."
	exit 0
fi
rl_has_changelog_section "$RL_VERSION" || rl_fail "ios/CHANGELOG.md has no '## $RL_VERSION' section (the App Store \"What's New\")"
if ! rl_has_asc_key; then
	rl_log "no App Store Connect API key (ASC_KEY_ID, ASC_ISSUER_ID, ASC_PRIVATE_KEY): submit version $RL_VERSION by hand."
	"$RL_CI_DIR/asc.sh" instructions "$RL_VERSION"
	exit 0
fi
"$RL_CI_DIR/asc.sh" submit "$RL_VERSION"
