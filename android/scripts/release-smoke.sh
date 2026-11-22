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
# Its own application id: the user's ReminisLink (pairings, identity) is never touched.
APP=link.reminis.app.smoke

# An emulator only, unless RL_SMOKE_ALLOW_DEVICE=1 (then still beside the real app, never over it).
serial=$("$ADB" get-serialno 2>/dev/null || true)
qemu=$("$ADB" shell getprop ro.kernel.qemu 2>/dev/null | tr -d '\r' || true)
case "$serial" in emulator-*) qemu=1 ;; esac
if [ "$qemu" != 1 ] && [ "${RL_SMOKE_ALLOW_DEVICE:-}" != 1 ]; then
	echo "release smoke: '$serial' is not an emulator; set RL_SMOKE_ALLOW_DEVICE=1 to run it on a real device anyway" >&2
	exit 1
fi

work=$(mktemp -d "${TMPDIR:-/tmp}/reminislink-smoke.XXXXXX")
cleanup() {
	"$ADB" uninstall "$APP" >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT INT TERM

(cd "$ANDROID" && ./gradlew --console=plain -q :app:assembleRelease -Preminislink.signWithDebugKey=true -Preminislink.smokeSuffix=true)
"$ADB" uninstall "$APP" >/dev/null 2>&1 || true
"$ADB" install -r "$ANDROID/app/build/outputs/apk/release/app-release.apk" >/dev/null
# The next release build (make android-release-apk) gets its own id back.
"$ADB" shell cmd locale set-app-locales "$APP" --locales en-US >/dev/null 2>&1 || true
"$ADB" shell am start -W -n "$APP/link.reminis.app.MainActivity" >/dev/null

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

# scroll_to <text>: swipes up until a node with that text is on screen (a short screen).
scroll_to() {
	n=0
	while :; do
		dump
		grep -q "text=\"$1\"" "$work/ui.xml" && return 0
		[ $n -lt 8 ] || { echo "release smoke: '$1' not found by scrolling" >&2; exit 1; }
		size=$("$ADB" shell wm size | sed -n 's/.*: \([0-9]*\)x\([0-9]*\).*/\1 \2/p' | tail -n 1)
		set -- $size
		"$ADB" shell input swipe $(($1 / 2)) $(($2 * 3 / 4)) $(($1 / 2)) $(($2 / 4)) 300
		sleep 1
		n=$((n + 1))
	done
}

tap "Try Without a Console"
wait_for "Demo Handheld"
wait_for "Demo mode"
scroll_to "Library"
tap "Library"
wait_for "Thistledown Isles"
wait_for "Brambleway Dash"
tap "Thistledown Isles"
wait_for "States"
echo "Release smoke test passed (demo, dashboard, library, game on the R8 release APK)."
