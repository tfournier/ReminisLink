#!/bin/sh
# Smoke test of the real release APK (R8 with the release rules only, no
# test APK inside the app): builds assembleRelease signed with the debug key,
# installs it on the running emulator or device, and drives it from outside
# with adb and uiautomator: "Try Without a Console", the demo dashboard
# (Tink signs, kotlinx.serialization decodes), the library of made-up games
# (OkHttp through the fake pak's interceptor). The event stream's `hello` is
# covered by ReleaseSmokeTest on the "minified" build type.
#
#   android/scripts/release-smoke.sh     (an emulator must be running: adb devices)
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
ANDROID="$ROOT/android"
ADB=${ADB:-adb}
command -v "$ADB" >/dev/null 2>&1 || ADB="${ANDROID_HOME:-$HOME/Library/Android/sdk}/platform-tools/adb"
APP=link.reminis.app
work=$(mktemp -d "${TMPDIR:-/tmp}/reminislink-smoke.XXXXXX")
trap 'rm -rf "$work"' EXIT

(cd "$ANDROID" && ./gradlew --console=plain -q :app:assembleRelease -Preminislink.signWithDebugKey=true)
"$ADB" uninstall "$APP" >/dev/null 2>&1 || true
"$ADB" install -r "$ANDROID/app/build/outputs/apk/release/app-release.apk" >/dev/null
"$ADB" shell cmd locale set-app-locales "$APP" --locales en-US >/dev/null 2>&1 || true
"$ADB" shell am start -W -n "$APP/.MainActivity" >/dev/null

dump() {
	"$ADB" shell uiautomator dump /sdcard/rl-smoke.xml >/dev/null 2>&1 || true
	"$ADB" shell cat /sdcard/rl-smoke.xml >"$work/ui.xml" 2>/dev/null || : >"$work/ui.xml"
}

# wait_for <text>: until a node with that text is on screen (20 s).
wait_for() {
	i=0
	while :; do
		dump
		grep -q "text=\"$1\"" "$work/ui.xml" && return 0
		[ $i -lt 20 ] || { echo "release smoke: '$1' never shown" >&2; exit 1; }
		sleep 1
		i=$((i + 1))
	done
}

# tap <text>: taps the centre of the node with that text.
tap() {
	wait_for "$1"
	bounds=$(tr '>' '\n' <"$work/ui.xml" | grep "text=\"$1\"" | head -n 1 | sed -n 's/.*bounds="\[\([0-9]*\),\([0-9]*\)\]\[\([0-9]*\),\([0-9]*\)\]".*/\1 \2 \3 \4/p')
	set -- $bounds
	"$ADB" shell input tap $((($1 + $3) / 2)) $((($2 + $4) / 2))
}

tap "Try Without a Console"
wait_for "Demo Handheld"
wait_for "Demo mode"
tap "Library"
wait_for "Thistledown Isles"
wait_for "Brambleway Dash"
tap "Thistledown Isles"
wait_for "States"
"$ADB" shell cmd locale set-app-locales "$APP" --locales "" >/dev/null 2>&1 || true
"$ADB" uninstall "$APP" >/dev/null
echo "Release smoke test passed (demo, dashboard, library, game on the R8 release APK)."
