#!/bin/bash
# Xcode Cloud: runs before each xcodebuild action (milestone M17).
#
# Checks the tag and the workflow again and rewrites Config/CI.xcconfig (the
# same as ci_post_clone.sh: an action of the workflow may run on a fresh
# copy), then shows the version that will be built.
set -euo pipefail
# shellcheck source=common.sh
. "$(dirname "$0")/common.sh"

rl_configure
rl_log "xcodebuild action: ${CI_XCODEBUILD_ACTION:-unknown}"
