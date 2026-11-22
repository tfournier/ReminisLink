#!/bin/bash
# Release gate before a tag (make vX.Y.Z, make ios-vX.Y.Z, make android-vX.Y.Z):
#   scripts/require-ci.sh <commit> <job>...
# succeeds only when a GitHub CI run (.github/workflows/ci.yml, event push) of
# <commit> passed every job named; otherwise it says why (CI running, failed,
# absent, GitHub unreachable) and fails. Reads the API with the gh CLI (your
# login: no rate limit to speak of); the logic is shared with the Xcode Cloud
# check (rl_require_ci in ios/ci_scripts/common.sh).
#
# RL_SKIP_CI_CHECK=1 skips the check (a warning is printed): for one
# exceptional release, by hand, never by default.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=../ios/ci_scripts/common.sh
. "$root/ios/ci_scripts/common.sh"

[ "$#" -ge 2 ] || rl_fail "usage: scripts/require-ci.sh <commit> <job>..."
sha="$1"
shift
if [ "${RL_SKIP_CI_CHECK:-}" != 1 ] && [[ ! "$sha" =~ ^[0-9a-f]{40}$ ]]; then
	sha="$(git -C "$root" rev-parse --verify "$sha^{commit}")" || rl_fail "unknown commit '$1'"
fi
RL_GITHUB_CLIENT="${RL_GITHUB_CLIENT:-gh}" rl_require_ci "$sha" "$@"
