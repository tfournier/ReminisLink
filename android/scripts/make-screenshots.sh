#!/bin/sh
# Store screenshots of the Android app (F-Droid and Google Play metadata),
# taken from the demo mode (made-up console and games) on the running
# emulator or device: ScreenshotsTest navigates the screens once per locale,
# and the PNG files are copied into
# android/fastlane/metadata/android/<locale>/images/phoneScreenshots/.
#
#   android/scripts/make-screenshots.sh     (an emulator must be running: adb devices)
#
# The status bar is put in the system's demo mode (fixed clock, full
# battery, no notifications) while the screenshots are taken.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
ANDROID="$ROOT/android"
ADB=${ADB:-adb}
command -v "$ADB" >/dev/null 2>&1 || ADB="${ANDROID_HOME:-$HOME/Library/Android/sdk}/platform-tools/adb"
APP=link.reminis.app
TEST=link.reminis.app.test

(cd "$ANDROID" && ./gradlew --console=plain -q :app:assembleDebug :app:assembleDebugAndroidTest)
"$ADB" install -r -t "$ANDROID/app/build/outputs/apk/debug/app-debug.apk" >/dev/null
"$ADB" install -r -t "$ANDROID/app/build/outputs/apk/androidTest/debug/app-debug-androidTest.apk" >/dev/null

demo_bar() {
	"$ADB" shell settings put global sysui_demo_allowed 1
	"$ADB" shell am broadcast -a com.android.systemui.demo -e command enter >/dev/null
	"$ADB" shell am broadcast -a com.android.systemui.demo -e command clock -e hhmm 0941 >/dev/null
	"$ADB" shell am broadcast -a com.android.systemui.demo -e command battery -e level 100 -e plugged false >/dev/null
	"$ADB" shell am broadcast -a com.android.systemui.demo -e command network -e wifi show -e level 4 >/dev/null
	"$ADB" shell am broadcast -a com.android.systemui.demo -e command notifications -e visible false >/dev/null
}
cleanup() {
	"$ADB" shell am broadcast -a com.android.systemui.demo -e command exit >/dev/null 2>&1 || true
	"$ADB" shell cmd locale set-app-locales "$APP" --locales "" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM
demo_bar

for locale in en-US fr-FR; do
	"$ADB" shell cmd locale set-app-locales "$APP" --locales "$locale"
	out=$("$ADB" shell am instrument -w -e screenshots true -e class link.reminis.app.ScreenshotsTest "$TEST/androidx.test.runner.AndroidJUnitRunner" 2>&1)
	printf '%s\n' "$out" | grep -q '^OK (1 test)' || { printf '%s\n' "$out" >&2; exit 1; }
	dest="$ANDROID/fastlane/metadata/android/$locale/images/phoneScreenshots"
	rm -rf "$dest"
	mkdir -p "$dest"
	for n in 1 2 3 4 5 6 7; do
		"$ADB" exec-out run-as "$APP" cat "files/screenshots/$n.png" >"$dest/$n.png"
		[ -s "$dest/$n.png" ] || { echo "screenshot $n missing for $locale" >&2; exit 1; }
	done
	echo "Screenshots for $locale in $dest"
done
