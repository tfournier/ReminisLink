#!/bin/bash
# Xcode Cloud: runs once the repository is cloned (milestone M17).
#
# Derives the app version from the tag (CI_TAG, ios-vX.Y.Z or ios-beta-vX.Y.Z-beta.N) and the
# build number from CI_BUILD_NUMBER into Config/CI.xcconfig, and stops the
# build (exit 1) when the tag is malformed or does not fit the workflow: a
# pre-release tag never goes through the Release workflow, hence never to
# App Review. The Submit workflow then submits the release (see
# submit_for_review.sh).
#
# Release gate: the Release and Beta workflows build a tag only when the
# GitHub CI (ci.yml, a push to main) passed the jobs "ios", "ios-ui-1",
# "ios-ui-2" and "contract" on the commit built (CI_COMMIT), read from the public GitHub API
# without a token (rl_require_ci_build in common.sh; the commit "Release app
# X.Y.Z" of make ios-vX.Y.Z is judged by its parent while its own CI runs,
# checked from the local clone). CI running, failed or absent, GitHub unreachable or
# rate limited: the build stops here, before the archive. RL_SKIP_CI_CHECK=1 (an
# environment variable of the workflow, set by hand for one exceptional
# build) skips the check with a warning.
set -euo pipefail
# shellcheck source=common.sh
. "$(dirname "$0")/common.sh"

rl_configure

case "${CI_WORKFLOW:-}" in
"$RL_RELEASE_WORKFLOW" | "$RL_BETA_WORKFLOW")
	[ -n "${CI_COMMIT:-}" ] || [ "${RL_SKIP_CI_CHECK:-}" = 1 ] ||
		rl_fail "tag ${CI_TAG:-}: CI_COMMIT is not set, cannot check the GitHub CI of the commit built"
	# shellcheck disable=SC2086 # the list of jobs splits on purpose
	rl_require_ci_build "${CI_COMMIT:-}" "Release app $RL_VERSION" $RL_CI_JOBS_IOS
	;;
esac

if [ "${CI_WORKFLOW:-}" = "$RL_SUBMIT_WORKFLOW" ]; then
	"$RL_CI_DIR/submit_for_review.sh"
fi
