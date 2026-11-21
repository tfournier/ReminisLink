#!/bin/bash
# Tests of the release scripts (milestone M17), run by "make release-checks"
# and by the go job of the CI:
#   - pak tags: vX.Y.Z is "latest", vX.Y.Z-beta.N / -rc.N "prerelease", app
#     tags (ios-v...) are refused, and the CI workflow cannot start the pak
#     release for an app tag;
#   - Xcode Cloud scripts (ios/ci_scripts) with fake CI_TAG, CI_WORKFLOW and
#     CI_BUILD_NUMBER: versions written, malformed tags and pre-releases in
#     the Release workflow refused;
#   - the App Store "What's New" files are in sync with ios/CHANGELOG.md;
#   - on a Mac, the offline self-test of the App Store Connect client.
set -uo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
ci="$root/ios/ci_scripts"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/rl-release-tests.XXXXXX")"
trap 'rm -r "$tmp"' EXIT
failures=0

pass() { printf 'ok    %s\n' "$1"; }
fail() {
	printf 'FAIL  %s\n' "$1"
	failures=$((failures + 1))
}

# expect <description> <expected exit: 0 or 1> <command...>
expect() {
	local what="$1" want="$2" got
	shift 2
	# The output of the previous command stays readable in $tmp/out
	# while this one runs.
	"$@" >"$tmp/current" 2>&1
	got=$?
	mv "$tmp/current" "$tmp/out"
	[ "$got" -ne 0 ] && got=1
	if [ "$got" = "$want" ]; then pass "$what"; else
		fail "$what (exit $got, expected $want)"
		sed 's/^/      /' "$tmp/out"
	fi
}

# --- Syntax ------------------------------------------------------------------

for script in "$ci"/*.sh "$root"/scripts/pak-release-kind.sh "$0"; do
	expect "bash -n $(basename "$script")" 0 bash -n "$script"
done
for hook in ci_post_clone.sh ci_pre_xcodebuild.sh ci_post_xcodebuild.sh; do
	if [ -x "$ci/$hook" ]; then pass "$hook is executable (Xcode Cloud runs it as is)"; else fail "$hook is not executable"; fi
done

# --- Pak tags ----------------------------------------------------------------

kind_is() { [ "$("$root/scripts/pak-release-kind.sh" "$1" 2>/dev/null)" = "$2" ]; }
for tag in v1.0.1 v2.10.0 v1.0.0.1; do expect "pak tag $tag is latest" 0 kind_is "$tag" latest; done
for tag in v1.1.0-beta.1 v1.1.0-rc.2; do expect "pak tag $tag is a pre-release" 0 kind_is "$tag" prerelease; done
for tag in ios-v1.0.1 ios-v1.1.0-beta.1 v1.1 v1.1.0-alpha.1 v1.1.0-beta "v1.0.0 x" ""; do
	expect "tag '$tag' is not a pak release" 1 "$root/scripts/pak-release-kind.sh" "$tag"
done

workflow="$root/.github/workflows/ci.yml"
# The push trigger only takes tags matching the glob v* (anchored at the
# start: ios-v1.0.0 cannot match), and the release job only runs for
# refs/tags/v...
expect "CI tag filter is exactly [\"v*\"]" 0 grep -Eq '^    tags: \["v\*"\]$' "$workflow"
expect "CI has no other tag filter" 0 test "$(grep -c 'tags:' "$workflow")" = 1
expect "CI release job runs for refs/tags/v only" 0 grep -Fq "if: startsWith(github.ref, 'refs/tags/v')" "$workflow"
expect "CI release job classifies the tag" 0 grep -Fq 'scripts/pak-release-kind.sh "$TAG"' "$workflow"
glob_matches() { [[ "$1" == v* ]]; }
for tag in ios-v1.0.1 ios-v1.1.0-beta.1; do expect "app tag $tag does not match the CI tag glob" 1 glob_matches "$tag"; done
expect "pak tag v1.0.1 matches the CI tag glob" 0 glob_matches v1.0.1

# --- Xcode Cloud scripts -----------------------------------------------------

changelog="$tmp/CHANGELOG.md"
cat >"$changelog" <<'EOF'
# Test changelog

## 1.2.0

### English

- A change.

### Français

- Un changement.
EOF

# run_ci <script> VAR=value...: one Xcode Cloud script with only these CI_*
# variables (and no App Store Connect key), writing a temporary xcconfig.
run_ci() {
	local script="$1"
	shift
	env -u CI_TAG -u CI_WORKFLOW -u CI_BUILD_NUMBER -u CI_XCODEBUILD_ACTION \
		-u ASC_KEY_ID -u ASC_ISSUER_ID -u ASC_PRIVATE_KEY -u ASC_PRIVATE_KEY_PATH \
		RL_CHANGELOG="$changelog" RL_CI_XCCONFIG="$tmp/CI.xcconfig" "$@" "$ci/$script"
}
xcconfig_has() { grep -Fxq "$1" "$tmp/CI.xcconfig"; }
xcconfig_lacks() { ! grep -q "$1" "$tmp/CI.xcconfig"; }

rm -f "$tmp/CI.xcconfig"
expect "Beta workflow, ios-v1.3.0-beta.2: accepted" 0 run_ci ci_post_clone.sh CI_WORKFLOW=Beta CI_TAG=ios-v1.3.0-beta.2 CI_BUILD_NUMBER=17
expect "  MARKETING_VERSION = 1.3.0 (suffix stripped)" 0 xcconfig_has "MARKETING_VERSION = 1.3.0"
expect "  CURRENT_PROJECT_VERSION = 17 (Xcode Cloud build number)" 0 xcconfig_has "CURRENT_PROJECT_VERSION = 17"
expect "Beta workflow, ios-v1.3.0-rc.1: accepted" 0 run_ci ci_pre_xcodebuild.sh CI_WORKFLOW=Beta CI_TAG=ios-v1.3.0-rc.1 CI_BUILD_NUMBER=18

rm -f "$tmp/CI.xcconfig"
expect "Release workflow, ios-v1.2.0 (changelog section): accepted" 0 run_ci ci_post_clone.sh CI_WORKFLOW=Release CI_TAG=ios-v1.2.0 CI_BUILD_NUMBER=20
expect "  MARKETING_VERSION = 1.2.0" 0 xcconfig_has "MARKETING_VERSION = 1.2.0"
expect "  CURRENT_PROJECT_VERSION = 20" 0 xcconfig_has "CURRENT_PROJECT_VERSION = 20"

expect "Release workflow, pre-release tag: refused (never App Review)" 1 run_ci ci_post_clone.sh CI_WORKFLOW=Release CI_TAG=ios-v1.2.0-beta.1 CI_BUILD_NUMBER=21
expect "Release workflow, pre-release tag, pre-xcodebuild: refused" 1 run_ci ci_pre_xcodebuild.sh CI_WORKFLOW=Release CI_TAG=ios-v1.2.0-beta.1 CI_BUILD_NUMBER=21
expect "Release workflow, pre-release tag, post-xcodebuild: refused" 1 run_ci ci_post_xcodebuild.sh CI_WORKFLOW=Release CI_TAG=ios-v1.2.0-beta.1 CI_XCODEBUILD_ACTION=archive
expect "Release workflow, no changelog section for 1.4.0: refused" 1 run_ci ci_post_clone.sh CI_WORKFLOW=Release CI_TAG=ios-v1.4.0 CI_BUILD_NUMBER=22
expect "Release workflow without a tag: refused" 1 run_ci ci_post_clone.sh CI_WORKFLOW=Release CI_BUILD_NUMBER=23
expect "Beta workflow, release tag: refused" 1 run_ci ci_post_clone.sh CI_WORKFLOW=Beta CI_TAG=ios-v1.2.0 CI_BUILD_NUMBER=24
for tag in ios-v1.2 ios-v1.2.0-alpha.1 ios-v01.2.0 v1.2.0 ios-v1.2.0-beta "ios-v1.2.0 "; do
	expect "malformed tag '$tag': refused" 1 run_ci ci_post_clone.sh CI_WORKFLOW=Beta CI_TAG="$tag" CI_BUILD_NUMBER=25
	expect "malformed tag '$tag', other workflow: refused" 1 run_ci ci_post_clone.sh CI_WORKFLOW=Tests CI_TAG="$tag" CI_BUILD_NUMBER=25
done
expect "malformed build number: refused" 1 run_ci ci_post_clone.sh CI_WORKFLOW=Beta CI_TAG=ios-v1.2.0-beta.1 CI_BUILD_NUMBER=abc

rm -f "$tmp/CI.xcconfig"
expect "other workflow, no tag: accepted" 0 run_ci ci_post_clone.sh CI_WORKFLOW=Tests CI_BUILD_NUMBER=26
expect "  build number only" 0 xcconfig_has "CURRENT_PROJECT_VERSION = 26"
expect "  no MARKETING_VERSION (Config.xcconfig's)" 0 xcconfig_lacks MARKETING_VERSION

expect "Submit workflow, pre-release tag: nothing submitted, success" 0 run_ci ci_post_clone.sh CI_WORKFLOW=Submit CI_TAG=ios-v1.2.0-beta.1 CI_BUILD_NUMBER=27
expect "Submit workflow, no API key: instructions, success" 0 run_ci ci_post_clone.sh CI_WORKFLOW=Submit CI_TAG=ios-v1.2.0 CI_BUILD_NUMBER=28
expect "  the instructions name the version" 0 grep -q "version 1.2.0" "$tmp/out"
expect "Release post-xcodebuild, no API key: instructions, success" 0 run_ci ci_post_xcodebuild.sh CI_WORKFLOW=Release CI_TAG=ios-v1.2.0 CI_XCODEBUILD_ACTION=archive
expect "Beta post-xcodebuild: does nothing" 0 run_ci ci_post_xcodebuild.sh CI_WORKFLOW=Beta CI_TAG=ios-v1.2.0-beta.1 CI_XCODEBUILD_ACTION=archive

# --- App changelog and App Store "What's New" --------------------------------

expect "release_notes.txt files match the first section of ios/CHANGELOG.md" 0 "$ci/release_notes.sh" check
expect "release notes: the 1.2.0 English text" 0 test "$(RL_CHANGELOG="$changelog" "$ci/release_notes.sh" print 1.2.0 en-US)" = "• A change."
expect "release notes: the 1.2.0 French text" 0 test "$(RL_CHANGELOG="$changelog" "$ci/release_notes.sh" print 1.2.0 fr-FR)" = "• Un changement."
expect "release notes: unknown section refused" 1 env RL_CHANGELOG="$changelog" "$ci/release_notes.sh" print 9.9.9 en-US

# --- App Store Connect client (Mac only: Swift and CryptoKit) ---------------

if command -v xcrun >/dev/null 2>&1; then
	expect "App Store Connect client self-test (key parsing, ES256 JWT)" 0 "$ci/asc.sh" selftest
else
	echo "skip  App Store Connect client self-test (needs a Mac)"
fi

if [ "$failures" -ne 0 ]; then
	echo "$failures release script test(s) failed"
	exit 1
fi
echo "All release script tests passed."
