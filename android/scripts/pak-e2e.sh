#!/bin/sh
# End-to-end test of the Android app against the real headless pak.
#
# Starts `make headless` on a free port, reads the pairing link it prints,
# rewrites its address to 10.0.2.2 (the host as seen from the Android
# emulator: mDNS does not cross the emulator's network), installs the debug
# and test APKs on the running emulator or device, and runs the
# instrumented tests with that link (PakIntegrationTest pairs, opens the
# dashboard, the library and a game). The pak is stopped by PID.
#
#   android/scripts/pak-e2e.sh            (an emulator must be running: adb devices)
#   PORT=48100 android/scripts/pak-e2e.sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
ANDROID="$ROOT/android"
PORT=${PORT:-48100}
ADB=${ADB:-adb}
command -v "$ADB" >/dev/null 2>&1 || ADB="${ANDROID_HOME:-$HOME/Library/Android/sdk}/platform-tools/adb"

log=$(mktemp "${TMPDIR:-/tmp}/reminislink-e2e.XXXXXX")
make -C "$ROOT" headless HEADLESS_ARGS="--port $PORT --name Android-E2E" >"$log" 2>&1 &
make_pid=$!

stop_pak() {
  # The pak is the child of make (make headless execs it): stop both by PID.
  for pid in $(pgrep -P "$make_pid" 2>/dev/null || true); do
    for child in $(pgrep -P "$pid" 2>/dev/null || true); do kill "$child" 2>/dev/null || true; done
    kill "$pid" 2>/dev/null || true
  done
  kill "$make_pid" 2>/dev/null || true
  wait "$make_pid" 2>/dev/null || true
}
trap stop_pak EXIT INT TERM

link=""
i=0
while [ $i -lt 120 ]; do
  link=$(sed -n 's/^Pairing link: //p' "$log" | tail -n 1)
  [ -n "$link" ] && break
  if ! kill -0 "$make_pid" 2>/dev/null; then cat "$log"; echo "the pak stopped" >&2; exit 1; fi
  sleep 1
  i=$((i + 1))
done
[ -n "$link" ] || { cat "$log"; echo "no pairing link printed" >&2; exit 1; }
port=$(printf '%s' "$link" | sed -n 's/.*[?&]p=\([0-9]*\).*/\1/p')
link=$(printf '%s' "$link" | sed "s/\([?&]h=\)[0-9.]*/\110.0.2.2/")
echo "Pak on port $port; pairing link rewritten for the emulator"

(cd "$ANDROID" && ./gradlew --console=plain -q :app:assembleDebug :app:assembleDebugAndroidTest)
"$ADB" install -r -t "$ANDROID/app/build/outputs/apk/debug/app-debug.apk" >/dev/null
"$ADB" install -r -t "$ANDROID/app/build/outputs/apk/androidTest/debug/app-debug-androidTest.apk" >/dev/null
"$ADB" shell pm clear link.reminis.app >/dev/null

out=$("$ADB" shell am instrument -w -e pairingLink "'$link'" link.reminis.app.test/androidx.test.runner.AndroidJUnitRunner)
printf '%s\n' "$out"
printf '%s\n' "$out" | grep -q '^OK (' || { echo "instrumented tests failed" >&2; tail -n 30 "$log"; exit 1; }
grep -E 'Paired with' "$log" || true
