#!/bin/bash
# Tests of the release scripts (milestone M17), run by "make release-checks"
# and by the go job of the CI:
#   - pak tags: vX.Y.Z is "latest", vX.Y.Z-beta.N / -rc.N "prerelease", app
#     tags (ios-v...) are refused, and the CI workflow cannot start the pak
#     release for an app tag;
#   - Xcode Cloud scripts (ios/ci_scripts) with fake CI_TAG, CI_WORKFLOW and
#     CI_BUILD_NUMBER: versions written, malformed tags and pre-releases in
#     the Release workflow refused;
#   - the publication of a pak release (fake gh): flags from the tag, also
#     on a re-run over a wrongly marked release;
#   - "make ios-vX.Y.Z" and "make ios-vX.Y.Z-beta.N" (tag ios-beta-v...) on a copy of the files
#     (fake git);
#   - the App Store "What's New" files are in sync with ios/CHANGELOG.md;
#   - on a Mac, the App Store Connect client: offline self-test, and whole
#     submissions against a local mock of the API
#     (scripts/release-tests/asc_mock.py).
set -uo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
ci="$root/ios/ci_scripts"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/rl-release-tests.XXXXXX")" || {
	echo "cannot create a temporary directory" >&2
	exit 1
}
[ -d "$tmp" ] || exit 1
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
for tag in ios-v1.0.1 ios-v1.1.0-beta.1 ios-beta-v1.1.0-beta.1 v1.1 v1.1.0-alpha.1 v1.1.0-beta "v1.0.0 x" ""; do
	expect "tag '$tag' is not a pak release" 1 "$root/scripts/pak-release-kind.sh" "$tag"
done

workflow="$root/.github/workflows/ci.yml"
# The push trigger only takes tags matching the glob v* (anchored at the
# start: ios-v1.0.0 cannot match), and the release job only runs for
# refs/tags/v...
expect "CI tag filter is exactly [\"v*\"]" 0 grep -Eq '^    tags: \["v\*"\]$' "$workflow"
expect "CI has no other tag filter" 0 test "$(grep -c 'tags:' "$workflow")" = 1
expect "CI release job runs for refs/tags/v only" 0 grep -Fq "if: startsWith(github.ref, 'refs/tags/v')" "$workflow"
expect "CI release job publishes both zips through scripts/publish-pak-release.sh" 0 grep -Fq 'run: scripts/publish-pak-release.sh "$TAG" dist/ReminisLink.pak.zip dist/ReminisLink.knulli.zip' "$workflow"
expect "CI release job downloads the Knulli zip" 0 grep -Fq 'name: reminislink-knulli' <(sed -n '/^  release:/,$p' "$workflow")
expect "pak.json release_filename is still ReminisLink.pak.zip (read by the Pak Store)" 0 grep -Eq '"release_filename": *"ReminisLink.pak.zip"' "$root/pak.json"
glob_matches() { [[ "$1" == v* ]]; }
for tag in ios-v1.0.1 ios-beta-v1.1.0-beta.1; do expect "app tag $tag does not match the CI tag glob" 1 glob_matches "$tag"; done
expect "pak tag v1.0.1 matches the CI tag glob" 0 glob_matches v1.0.1

# publish <tag> [release exists: 0/1]: the release job's script with a fake gh.
fakes="$root/scripts/release-tests"
publish() {
	: >"$tmp/fake.log"
	env PATH="$fakes:$PATH" FAKE_LOG="$tmp/fake.log" FAKE_RELEASE_EXISTS="${2:-0}" \
		"$root/scripts/publish-pak-release.sh" "$1" "$tmp/ReminisLink.pak.zip" "$tmp/ReminisLink.knulli.zip"
}
logged() { grep -Fxq -- "$1" "$tmp/fake.log"; }
not_logged() { ! grep -Fq -- "$1" "$tmp/fake.log"; }
touch "$tmp/ReminisLink.pak.zip" "$tmp/ReminisLink.knulli.zip"
if command -v jq >/dev/null 2>&1; then
	expect "publish v1.0.1: new release" 0 publish v1.0.1
	expect "  created as Latest, not a pre-release" 0 grep -Eq '^gh release create v1.0.1 .* --prerelease=false --latest$' "$tmp/fake.log"
	expect "  with both zips, NextUI first" 0 grep -Eq '^gh release create v1.0.1 [^ ]*/ReminisLink.pak.zip [^ ]*/ReminisLink.knulli.zip --verify-tag ' "$tmp/fake.log"
	expect "publish v1.1.0-beta.1: new pre-release" 0 publish v1.1.0-beta.1
	expect "  created as a pre-release, never Latest" 0 grep -Eq '^gh release create v1.1.0-beta.1 .* --prerelease --latest=false$' "$tmp/fake.log"
	expect "  with both zips" 0 grep -Eq '^gh release create v1.1.0-beta.1 [^ ]*/ReminisLink.pak.zip [^ ]*/ReminisLink.knulli.zip ' "$tmp/fake.log"
	expect "  never marked Latest" 1 grep -Eq -- '--latest( |$)' "$tmp/fake.log"
	expect "publish v1.1.0-beta.1 again (release exists, maybe marked Latest)" 0 publish v1.1.0-beta.1 1
	expect "  flags set again before the zip" 0 logged "gh release edit v1.1.0-beta.1 --prerelease --latest=false"
	expect "  then both zips replaced" 0 grep -Eq '^gh release upload v1.1.0-beta.1 [^ ]*/ReminisLink.pak.zip [^ ]*/ReminisLink.knulli.zip --clobber$' "$tmp/fake.log"
	expect "  edit comes before upload" 0 test "$(grep -n 'release edit' "$tmp/fake.log" | cut -d: -f1)" -lt "$(grep -n 'release upload' "$tmp/fake.log" | cut -d: -f1)"
	expect "  nothing created" 0 not_logged "release create"
	expect "publish v1.0.1 again: back to Latest" 0 publish v1.0.1 1
	expect "  flags set again" 0 logged "gh release edit v1.0.1 --prerelease=false --latest"
	expect "publish ios-v1.0.1: refused" 1 publish ios-v1.0.1
	expect "  gh never called for an app tag" 0 test ! -s "$tmp/fake.log"
	: >"$tmp/fake.log"
	expect "publish without a zip: refused" 1 env PATH="$fakes:$PATH" FAKE_LOG="$tmp/fake.log" "$root/scripts/publish-pak-release.sh" v1.1.0
	expect "publish with a missing Knulli zip: refused" 1 env PATH="$fakes:$PATH" FAKE_LOG="$tmp/fake.log" "$root/scripts/publish-pak-release.sh" v1.1.0 "$tmp/ReminisLink.pak.zip" "$tmp/missing.knulli.zip"
	expect "  gh never called without its zips" 0 test ! -s "$tmp/fake.log"
else
	echo "skip  pak release publication (needs jq, as on the CI runner)"
fi

# --- Android tags (M21) -------------------------------------------------------

for script in android-version-code.sh android-release-kind.sh publish-android-release.sh; do
	expect "bash -n $script" 0 bash -n "$root/scripts/$script"
done
code_is() { [ "$("$root/scripts/android-version-code.sh" "$1" 2>/dev/null)" = "$2" ]; }
expect "versionCode of 0.1.0" 0 code_is 0.1.0 10099
expect "versionCode of android-v1.2.3" 0 code_is android-v1.2.3 1020399
expect "versionCode of 1.2.3-beta.4" 0 code_is 1.2.3-beta.4 1020304
expect "versionCode of 1.2.3-rc.2" 0 code_is 1.2.3-rc.2 1020352
android_order_ok() {
	local previous=0 v c
	for v in 0.9.9 1.0.0-beta.1 1.0.0-beta.49 1.0.0-rc.1 1.0.0-rc.48 1.0.0 1.0.1 1.1.0-beta.1 1.1.0 2.0.0; do
		c="$("$root/scripts/android-version-code.sh" "$v")" || return 1
		[ "$c" -gt "$previous" ] || return 1
		previous=$c
	done
}
expect "versionCodes grow with SemVer (pre-releases before their release)" 0 android_order_ok
for v in 1.2 1.100.0 1.2.100 01.2.3 1.2.3-beta.50 1.2.3-rc.49 1.2.3-beta.0 1.2.3-alpha.1 v1.2.3 ios-v1.2.3 ""; do
	expect "Android version '$v' refused" 1 "$root/scripts/android-version-code.sh" "$v"
done
expect "android/version.properties: versionCode matches versionName" 0 test \
	"$("$root/scripts/android-version-code.sh" "$(sed -n 's/^versionName=//p' "$root/android/version.properties")")" = \
	"$(sed -n 's/^versionCode=//p' "$root/android/version.properties")"
android_kind_is() { [ "$("$root/scripts/android-release-kind.sh" "$1" 2>/dev/null)" = "$2" ]; }
expect "android-v1.0.0 is a release" 0 android_kind_is android-v1.0.0 release
for tag in android-v1.1.0-beta.1 android-v1.1.0-rc.2; do expect "$tag is a pre-release" 0 android_kind_is "$tag" prerelease; done
for tag in v1.0.0 ios-v1.0.0 android-v1.0 android-v1.0.0-alpha.1 android-1.0.0 "android-v1.0.0 x"; do
	expect "tag '$tag' is not an Android release" 1 "$root/scripts/android-release-kind.sh" "$tag"
done
for tag in android-v1.0.0 android-v1.1.0-beta.1; do
	expect "Android tag $tag is not a pak release" 1 "$root/scripts/pak-release-kind.sh" "$tag"
	expect "Android tag $tag does not match the pak CI tag glob" 1 glob_matches "$tag"
done
android_workflow="$root/.github/workflows/android-release.yml"
expect "Android workflow tag filter is exactly [\"android-v*\"]" 0 grep -Eq '^    tags: \["android-v\*"\]$' "$android_workflow"
expect "Android workflow has no other trigger" 0 test "$(grep -c -E '^  (push|pull_request|workflow_dispatch|schedule):' "$android_workflow")" = 1
expect "Android workflow publishes through scripts/publish-android-release.sh" 0 grep -Fq 'run: scripts/publish-android-release.sh "$TAG" "$APK" "$SIGNING"' "$android_workflow"
android_glob() { [[ "$1" == android-v* ]]; }
for tag in v1.0.1 v1.1.0-beta.1 ios-v1.0.1; do expect "tag $tag does not start the Android workflow" 1 android_glob "$tag"; done

publish_android() {
	: >"$tmp/fake.log"
	env PATH="$fakes:$PATH" FAKE_LOG="$tmp/fake.log" FAKE_RELEASE_EXISTS="${2:-0}" FAKE_ASSETS="${4:-}" \
		"$root/scripts/publish-android-release.sh" "$1" "$tmp/app.apk" "${3:-signed}"
}
touch "$tmp/app.apk"
expect "publish android-v1.0.0: new release" 0 publish_android android-v1.0.0
expect "  never Latest, not a pre-release" 0 grep -Eq '^gh release create android-v1.0.0 .* --prerelease=false --latest=false$' "$tmp/fake.log"
expect "publish android-v1.1.0-beta.1: new pre-release" 0 publish_android android-v1.1.0-beta.1
expect "  a pre-release, never Latest" 0 grep -Eq '^gh release create android-v1.1.0-beta.1 .* --prerelease --latest=false$' "$tmp/fake.log"
expect "publish android-v1.0.0 again: flags set again" 0 publish_android android-v1.0.0 1
expect "  never Latest" 0 logged "gh release edit android-v1.0.0 --prerelease=false --latest=false"
expect "  then the APK replaced" 0 grep -Eq '^gh release upload android-v1.0.0 .* --clobber$' "$tmp/fake.log"
expect "  notes written again" 0 grep -Eq '^gh release edit android-v1.0.0 --notes-file ' "$tmp/fake.log"
expect "signed re-run over a debug-signed release" 0 publish_android android-v1.0.0 1 signed "ReminisLink-android-1.0.0-debug-signed.apk"
expect "  the debug-signed APK deleted" 0 logged "gh release delete-asset android-v1.0.0 ReminisLink-android-1.0.0-debug-signed.apk --yes"
expect "  after the signed upload" 0 test "$(grep -n 'release upload' "$tmp/fake.log" | cut -d: -f1)" -lt "$(grep -n 'delete-asset' "$tmp/fake.log" | cut -d: -f1)"
expect "  the notes rewritten last (after the upload and the deletion)" 0 test "$(grep -n 'delete-asset' "$tmp/fake.log" | cut -d: -f1)" -lt "$(grep -n 'release edit .* --notes-file' "$tmp/fake.log" | cut -d: -f1)"
expect "keyless re-run over a signed release: refused" 1 publish_android android-v1.0.0 1 debug-signed "ReminisLink-android-1.0.0.apk"
expect "  nothing edited nor uploaded" 0 not_logged "release upload"
expect "keyless re-run over a debug-signed release: allowed" 0 publish_android android-v1.0.0 1 debug-signed "ReminisLink-android-1.0.0-debug-signed.apk"
expect "  nothing deleted" 0 not_logged "delete-asset"
for tag in v1.0.1 ios-v1.0.1; do
	expect "publish_android $tag: refused" 1 publish_android "$tag"
	expect "  gh never called" 0 test ! -s "$tmp/fake.log"
done

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
	env -u CI_TAG -u CI_WORKFLOW -u CI_BUILD_NUMBER -u CI_XCODEBUILD_ACTION -u CI_XCODEBUILD_EXIT_CODE \
		-u ASC_KEY_ID -u ASC_ISSUER_ID -u ASC_PRIVATE_KEY -u ASC_PRIVATE_KEY_PATH -u ASC_BUILD_NUMBER \
		-u ASC_API_BASE -u ASC_TEST_MODE \
		RL_CHANGELOG="$changelog" RL_CI_XCCONFIG="$tmp/CI.xcconfig" "$@" "$ci/$script"
}
xcconfig_has() { grep -Fxq "$1" "$tmp/CI.xcconfig"; }
xcconfig_lacks() { ! grep -q "$1" "$tmp/CI.xcconfig"; }

rm -f "$tmp/CI.xcconfig"
expect "Beta workflow, ios-beta-v1.3.0-beta.2: accepted" 0 run_ci ci_post_clone.sh CI_WORKFLOW=Beta CI_TAG=ios-beta-v1.3.0-beta.2 CI_BUILD_NUMBER=17
expect "  MARKETING_VERSION = 1.3.0 (suffix stripped)" 0 xcconfig_has "MARKETING_VERSION = 1.3.0"
expect "  CURRENT_PROJECT_VERSION = 17 (Xcode Cloud build number)" 0 xcconfig_has "CURRENT_PROJECT_VERSION = 17"
expect "Beta workflow, ios-beta-v1.3.0-rc.1: accepted" 0 run_ci ci_pre_xcodebuild.sh CI_WORKFLOW=Beta CI_TAG=ios-beta-v1.3.0-rc.1 CI_BUILD_NUMBER=18

rm -f "$tmp/CI.xcconfig"
expect "Release workflow, ios-v1.2.0 (changelog section): accepted" 0 run_ci ci_post_clone.sh CI_WORKFLOW=Release CI_TAG=ios-v1.2.0 CI_BUILD_NUMBER=20
expect "  MARKETING_VERSION = 1.2.0" 0 xcconfig_has "MARKETING_VERSION = 1.2.0"
expect "  CURRENT_PROJECT_VERSION = 20" 0 xcconfig_has "CURRENT_PROJECT_VERSION = 20"

expect "Release workflow, pre-release tag: refused (never App Review)" 1 run_ci ci_post_clone.sh CI_WORKFLOW=Release CI_TAG=ios-beta-v1.2.0-beta.1 CI_BUILD_NUMBER=21
expect "Release workflow, pre-release tag, pre-xcodebuild: refused" 1 run_ci ci_pre_xcodebuild.sh CI_WORKFLOW=Release CI_TAG=ios-beta-v1.2.0-beta.1 CI_BUILD_NUMBER=21
expect "Release workflow, pre-release tag, post-xcodebuild: refused" 1 run_ci ci_post_xcodebuild.sh CI_WORKFLOW=Release CI_TAG=ios-beta-v1.2.0-beta.1 CI_XCODEBUILD_ACTION=archive
expect "Release workflow, no changelog section for 1.4.0: refused" 1 run_ci ci_post_clone.sh CI_WORKFLOW=Release CI_TAG=ios-v1.4.0 CI_BUILD_NUMBER=22
expect "Release workflow without a tag: refused" 1 run_ci ci_post_clone.sh CI_WORKFLOW=Release CI_BUILD_NUMBER=23
expect "Beta workflow, release tag: refused" 1 run_ci ci_post_clone.sh CI_WORKFLOW=Beta CI_TAG=ios-v1.2.0 CI_BUILD_NUMBER=24
for tag in ios-v1.2 ios-v1.2.0-alpha.1 ios-v01.2.0 v1.2.0 ios-v1.2.0-beta "ios-v1.2.0 " ios-v1.2.0-beta.1 ios-beta-v1.2.0 ios-beta-v1.2.0-beta ios-beta-v1.2.0-alpha.1; do
	expect "malformed tag '$tag': refused" 1 run_ci ci_post_clone.sh CI_WORKFLOW=Beta CI_TAG="$tag" CI_BUILD_NUMBER=25
	expect "malformed tag '$tag', other workflow: refused" 1 run_ci ci_post_clone.sh CI_WORKFLOW=Tests CI_TAG="$tag" CI_BUILD_NUMBER=25
done
expect "malformed build number: refused" 1 run_ci ci_post_clone.sh CI_WORKFLOW=Beta CI_TAG=ios-beta-v1.2.0-beta.1 CI_BUILD_NUMBER=abc
expect "tag without CI_BUILD_NUMBER: refused" 1 run_ci ci_post_clone.sh CI_WORKFLOW=Beta CI_TAG=ios-beta-v1.2.0-beta.1
expect "tag without CI_WORKFLOW: refused" 1 run_ci ci_post_clone.sh CI_TAG=ios-v1.2.0 CI_BUILD_NUMBER=25
expect "tag in another workflow: refused" 1 run_ci ci_pre_xcodebuild.sh CI_WORKFLOW=Tests CI_TAG=ios-v1.2.0 CI_BUILD_NUMBER=25

rm -f "$tmp/CI.xcconfig"
expect "other workflow, no tag: accepted" 0 run_ci ci_post_clone.sh CI_WORKFLOW=Tests CI_BUILD_NUMBER=26
expect "  build number only" 0 xcconfig_has "CURRENT_PROJECT_VERSION = 26"
expect "  no MARKETING_VERSION (Config.xcconfig's)" 0 xcconfig_lacks MARKETING_VERSION

expect "Submit workflow, pre-release tag: nothing submitted, success" 0 run_ci ci_post_clone.sh CI_WORKFLOW=Submit CI_TAG=ios-beta-v1.2.0-beta.1 CI_BUILD_NUMBER=27
expect "Submit workflow, no API key: instructions, success" 0 run_ci ci_post_clone.sh CI_WORKFLOW=Submit CI_TAG=ios-v1.2.0 CI_BUILD_NUMBER=28
expect "  the instructions name the version" 0 grep -q "version 1.2.0" "$tmp/out"
expect "Release post-xcodebuild, archive result unknown: refused" 1 run_ci ci_post_xcodebuild.sh CI_WORKFLOW=Release CI_TAG=ios-v1.2.0 CI_BUILD_NUMBER=29 CI_XCODEBUILD_ACTION=archive
expect "  says why" 0 grep -q "CI_XCODEBUILD_EXIT_CODE is not set" "$tmp/out"
expect "tag archive without CI_WORKFLOW, post-xcodebuild: refused" 1 run_ci ci_post_xcodebuild.sh CI_TAG=ios-v1.2.0 CI_BUILD_NUMBER=29 CI_XCODEBUILD_ACTION=archive CI_XCODEBUILD_EXIT_CODE=0
expect "  says why" 0 grep -q "CI_WORKFLOW is not set" "$tmp/out"
expect "Release post-xcodebuild without CI_BUILD_NUMBER: refused" 1 run_ci ci_post_xcodebuild.sh CI_WORKFLOW=Release CI_TAG=ios-v1.2.0 CI_XCODEBUILD_ACTION=archive CI_XCODEBUILD_EXIT_CODE=0
expect "  says why" 0 grep -q "CI_BUILD_NUMBER" "$tmp/out"
expect "tag without CI_XCODEBUILD_ACTION, post-xcodebuild: refused" 1 run_ci ci_post_xcodebuild.sh CI_WORKFLOW=Release CI_TAG=ios-v1.2.0 CI_BUILD_NUMBER=29 CI_XCODEBUILD_EXIT_CODE=0
expect "no tag, no CI_WORKFLOW, post-xcodebuild archive: does nothing" 0 run_ci ci_post_xcodebuild.sh CI_XCODEBUILD_ACTION=archive
expect "Release post-xcodebuild, archive failed: nothing prepared" 0 run_ci ci_post_xcodebuild.sh CI_WORKFLOW=Release CI_TAG=ios-v1.2.0 CI_BUILD_NUMBER=29 CI_XCODEBUILD_ACTION=archive CI_XCODEBUILD_EXIT_CODE=65
expect "  says so" 0 grep -q "nothing prepared" "$tmp/out"
expect "Release post-xcodebuild, no API key: instructions, success" 0 run_ci ci_post_xcodebuild.sh CI_WORKFLOW=Release CI_TAG=ios-v1.2.0 CI_BUILD_NUMBER=29 CI_XCODEBUILD_ACTION=archive CI_XCODEBUILD_EXIT_CODE=0
expect "  the instructions name the build of the Release run" 0 grep -q "choose build 29 of version 1.2.0" "$tmp/out"
expect "Beta post-xcodebuild: does nothing" 0 run_ci ci_post_xcodebuild.sh CI_WORKFLOW=Beta CI_TAG=ios-beta-v1.2.0-beta.1 CI_XCODEBUILD_ACTION=archive

# --- App changelog and App Store "What's New" --------------------------------

expect "release_notes.txt files match the first section of ios/CHANGELOG.md" 0 "$ci/release_notes.sh" check
expect "release notes: the 1.2.0 English text" 0 test "$(RL_CHANGELOG="$changelog" "$ci/release_notes.sh" print 1.2.0 en-US)" = "• A change."
expect "release notes: the 1.2.0 French text" 0 test "$(RL_CHANGELOG="$changelog" "$ci/release_notes.sh" print 1.2.0 fr-FR)" = "• Un changement."
expect "release notes: unknown section refused" 1 env RL_CHANGELOG="$changelog" "$ci/release_notes.sh" print 9.9.9 en-US

# --- make ios-vX.Y.Z (a copy of the files, a fake git) ----------------------

# make_app_release <tag>: the recipe in a copy of the Makefile and of the
# app's changelog, scripts and App Store texts; git calls go to the log.
make_app_release() {
	local copy="$tmp/repo"
	rm -rf "$copy"
	mkdir -p "$copy/ios"
	cp "$root/Makefile" "$root/pak.json" "$copy/"
	cp -R "$root/ios/ci_scripts" "$root/ios/AppStore" "$copy/ios/"
	cp "$root/ios/CHANGELOG.md" "$copy/ios/"
	# The release test needs an Unreleased section: add one to the copy
	# when the real changelog has none right now.
	if ! grep -q '^## Unreleased$' "$copy/ios/CHANGELOG.md"; then
		awk 'BEGIN { done = 0 }
			!done && /^## / { print "## Unreleased\n\n### English\n\n- A test change.\n\n### Français\n\n- Un changement de test.\n"; done = 1 }
			{ print }' "$copy/ios/CHANGELOG.md" >"$copy/ios/CHANGELOG.md.tmp"
		mv "$copy/ios/CHANGELOG.md.tmp" "$copy/ios/CHANGELOG.md"
	fi
	cp "$copy/ios/CHANGELOG.md" "$tmp/CHANGELOG-copy.md"
	: >"$tmp/fake.log"
	env FAKE_LOG="$tmp/fake.log" make -s -C "$copy" "$1" GIT="$fakes/fake-git"
}
expect "make ios-v9.8.7: a release" 0 make_app_release ios-v9.8.7
expect "  Unreleased renamed to 9.8.7" 0 grep -qx '## 9.8.7' "$tmp/repo/ios/CHANGELOG.md"
expect "  App Store notes synced" 0 env RL_CHANGELOG="$tmp/repo/ios/CHANGELOG.md" RL_APPSTORE_DIR="$tmp/repo/ios/AppStore" "$tmp/repo/ios/ci_scripts/release_notes.sh" check
expect "  committed as Release app 9.8.7" 0 grep -q '^git commit -q -m Release app 9.8.7 -- ios/CHANGELOG.md ios/AppStore$' "$tmp/fake.log"
expect "  tagged ios-v9.8.7" 0 grep -q '^git tag -a ios-v9.8.7 -m App release ios-v9.8.7$' "$tmp/fake.log"
expect "  pushed with the commit" 0 grep -q '^git push --atomic origin HEAD refs/tags/ios-v9.8.7$' "$tmp/fake.log"
expect "make ios-v9.8.7-beta.2: a pre-release" 0 make_app_release ios-v9.8.7-beta.2
expect "  changelog untouched" 0 cmp -s "$tmp/CHANGELOG-copy.md" "$tmp/repo/ios/CHANGELOG.md"
expect "  nothing committed" 0 not_logged "git commit"
expect "  tagged ios-beta-v9.8.7-beta.2 (the Beta workflow's prefix)" 0 grep -q '^git tag -a ios-beta-v9.8.7-beta.2 ' "$tmp/fake.log"
expect "  tag pushed alone" 0 grep -q '^git push origin refs/tags/ios-beta-v9.8.7-beta.2$' "$tmp/fake.log"
expect "make ios-beta-v9.8.7-rc.1: a pre-release" 0 make_app_release ios-beta-v9.8.7-rc.1
expect "  tag pushed alone" 0 grep -q '^git push origin refs/tags/ios-beta-v9.8.7-rc.1$' "$tmp/fake.log"
for name in ios-v9.8 ios-beta-v9.8.7 ios-v9.8.7-alpha.1; do
	expect "make $name: refused" 1 make_app_release "$name"
	expect "  git never called" 0 test ! -s "$tmp/fake.log"
done

# --- App Store Connect client (Mac only: Swift and CryptoKit) ---------------

# submit_against_mock <scenario> [VAR=value...]: submit_for_review.sh for
# ios-v1.1.0 against the mock. No key is needed: in test mode the client
# signs with a throwaway key of its own and never reads ASC_PRIVATE_KEY.
submit_against_mock() {
	local scenario="$1" pid port
	shift
	rm -f "$tmp/port" "$tmp/state.json"
	python3 "$fakes/asc_mock.py" "$scenario" "$tmp/port" "$tmp/state.json" &
	pid=$!
	# Python can take several seconds to start on a busy CI runner: wait up
	# to 30 s for the port, and fail clearly rather than call no server.
	for _ in $(seq 300); do [ -s "$tmp/port" ] && break; sleep 0.1; done
	if [ ! -s "$tmp/port" ]; then
		echo "the App Store Connect mock did not start ($scenario)"
		kill "$pid" 2>/dev/null
		wait "$pid" 2>/dev/null
		return 1
	fi
	port="$(cat "$tmp/port")"
	run_ci submit_for_review.sh CI_WORKFLOW=Submit CI_TAG=ios-v1.1.0 CI_BUILD_NUMBER=50 \
		ASC_TEST_MODE=1 ASC_API_BASE="http://127.0.0.1:$port" ASC_POLL_SECONDS=0 ASC_WAIT_MINUTES=1 \
		ASC_KEY_ID=TESTKEY ASC_ISSUER_ID=00000000-0000-0000-0000-000000000000 \
		RL_CHANGELOG="$tmp/CHANGELOG-1.1.0.md" "$@"
	local status=$?
	kill "$pid" 2>/dev/null
	wait "$pid" 2>/dev/null
	return "$status"
}
state() { python3 -c 'import json,sys; s=json.load(open(sys.argv[1])); print(eval(sys.argv[2]))' "$tmp/state.json" "$1"; }

if command -v xcrun >/dev/null 2>&1; then
	expect "App Store Connect client self-test (key parsing, JWT, address guard, eligibility)" 0 "$ci/asc.sh" selftest
	openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$tmp/AuthKey_TEST.p8" 2>/dev/null
	sed 's/^## 1.2.0$/## 1.1.0/' "$changelog" >"$tmp/CHANGELOG-1.1.0.md"

	# A real-looking key in the environment: test mode must ignore it, and
	# neither it nor a token may ever be printed.
	key_line="$(sed -n 2p "$tmp/AuthKey_TEST.p8")"
	no_secret_in() {
		! grep -Fq -e "PRIVATE KEY" -e "$key_line" -e "eyJ" -e "SECRET-DETAIL" "$1"
	}
	expect "mock: Release build arriving after a VALID beta of 1.1.0" 0 submit_against_mock release \
		ASC_PRIVATE_KEY="$(cat "$tmp/AuthKey_TEST.p8")"
	cp "$tmp/out" "$tmp/submit-release.out"
	expect "  the build of the Release run is attached (42), not the beta (41)" 0 test "$(state 's["attached"]')" = build-42
	expect "  submitted" 0 test "$(state 's["submitted"]')" = True
	expect "  What's New set in both languages" 0 test "$(state 'len([l for l in s["locs"] if l["attributes"]["whatsNew"]])')" = 2
	expect "  neither the key nor a token printed" 0 no_secret_in "$tmp/submit-release.out"
	expect "mock: test mode never reads ASC_PRIVATE_KEY (a malformed one is ignored)" 0 submit_against_mock release \
		ASC_PRIVATE_KEY="not a key" ASC_PRIVATE_KEY_PATH=/nonexistent/AuthKey.p8
	expect "mock: build runs on two pages, the run of the tag on the second" 0 submit_against_mock paged
	expect "  second page read" 0 test "$(state 's["second_pages"] > 0')" = True
	expect "  build 42 attached and submitted" 0 test "$(state 's["attached"], s["submitted"]')" = "('build-42', True)"
	expect "mock: build chosen by number, the Release run's (ASC_BUILD_NUMBER=42)" 0 submit_against_mock number ASC_BUILD_NUMBER=42
	expect "  build 42 attached and submitted" 0 test "$(state 's["attached"], s["submitted"]')" = "('build-42', True)"
	expect "  the Release run checked in Xcode Cloud" 0 test "$(state '"GET /v1/ciBuildRuns/run-42/builds" in s["requests"]')" = True
	expect "mock: a build number not from the Release run (41, App Store eligible) is refused" 1 submit_against_mock number-foreign ASC_BUILD_NUMBER=41
	expect "  says why" 0 grep -q "build 41 is not the build of a Release run of ios-v1.1.0" "$tmp/out"
	expect "  nothing attached or submitted" 0 test "$(state 's["attached"], s["submitted"]')" = "(None, False)"
	expect "mock: an internal-only build is refused" 1 submit_against_mock internal
	expect "  says why" 0 grep -q "internal-only build cannot be submitted" "$tmp/out"
	expect "  nothing attached or submitted" 0 test "$(state 's["attached"], s["submitted"]')" = "(None, False)"
	expect "mock: a draft submission with another item stops it" 1 submit_against_mock foreign-draft
	expect "  says why" 0 grep -q "already holds 1 other item" "$tmp/out"
	expect "  nothing attached or submitted" 0 test "$(state 's["attached"], s["submitted"]')" = "(None, False)"
	expect "mock: a draft holding this version only is submitted" 0 submit_against_mock own-draft
	expect "  no item added twice" 0 test "$(state 'len(s["items"]), s["submitted"]')" = "(1, True)"
	expect "mock: another item added to the draft after it was read: not submitted" 1 submit_against_mock late-foreign
	expect "  says why" 0 grep -q "now holds 1 other item" "$tmp/out"
	expect "  nothing submitted" 0 test "$(state 's["submitted"]')" = False
	expect "mock: an API error with a detail" 1 submit_against_mock error-detail ASC_PRIVATE_KEY="$(cat "$tmp/AuthKey_TEST.p8")"
	cp "$tmp/out" "$tmp/submit-error.out"
	expect "  shows the status, code and title" 0 grep -q "HTTP 403 - FORBIDDEN_ERROR: not allowed" "$tmp/submit-error.out"
	expect "  never the detail, the key or the token" 0 no_secret_in "$tmp/submit-error.out"
	expect "mock: version already waiting for review: nothing done" 0 submit_against_mock submitted
	expect "  nothing attached" 0 test "$(state 's["attached"]')" = None
	expect "ASC_API_BASE without ASC_TEST_MODE: refused before any request" 1 env -u ASC_TEST_MODE \
		ASC_API_BASE=http://127.0.0.1:9 ASC_KEY_ID=TESTKEY ASC_ISSUER_ID=x ASC_PRIVATE_KEY="$(cat "$tmp/AuthKey_TEST.p8")" \
		CI_TAG=ios-v1.1.0 RL_CHANGELOG="$tmp/CHANGELOG-1.1.0.md" "$ci/asc.sh" submit 1.1.0
else
	echo "skip  App Store Connect client tests (need a Mac)"
fi

if [ "$failures" -ne 0 ]; then
	echo "$failures release script test(s) failed"
	exit 1
fi
echo "All release script tests passed."
