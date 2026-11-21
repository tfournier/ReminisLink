#!/bin/bash
# Xcode Cloud: runs once the repository is cloned (milestone M17).
#
# Derives the app version from the tag (CI_TAG, ios-vX.Y.Z or ios-beta-vX.Y.Z-beta.N) and the
# build number from CI_BUILD_NUMBER into Config/CI.xcconfig, and stops the
# build (exit 1) when the tag is malformed or does not fit the workflow: a
# pre-release tag never goes through the Release workflow, hence never to
# App Review. The Submit workflow then submits the release (see
# submit_for_review.sh).
set -euo pipefail
# shellcheck source=common.sh
. "$(dirname "$0")/common.sh"

rl_configure

if [ "${CI_WORKFLOW:-}" = "$RL_SUBMIT_WORKFLOW" ]; then
	"$RL_CI_DIR/submit_for_review.sh"
fi
