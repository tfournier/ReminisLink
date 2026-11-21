#!/bin/sh
# End-to-end test of the Android app against the real headless pak.
#
# Starts `make headless` on a free port, reads the pairing link it prints,
# rewrites its address to 10.0.2.2 (the host as seen from the Android
# emulator: mDNS does not cross the emulator's network), installs the debug
# and test APKs on the running emulator or device, and runs the
# instrumented tests (PakIntegrationTest pairs, opens the dashboard, the
# library and a game). The pak is stopped by PID.
#
# The pairing link and the QR code hold the pairing secret:
# - the pak's output goes to a private file (0600, in a 0700 temporary
#   directory removed on exit), from which the link is read once;
# - the link reaches the device through stdin into the
#   private files of the debug app (`run-as`), never through a process argument; the test
#   deletes it once read, and the script deletes it on exit too;
# - every diagnostic printed here (stdout and stderr) is redacted: no
#   link, no QR code;
# - tracing (`sh -x`) is turned off: it would print the secret.
#
#   android/scripts/pak-e2e.sh            (an emulator must be running: adb devices)
#   PORT=48100 android/scripts/pak-e2e.sh
set +x
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
ANDROID="$ROOT/android"
PORT=${PORT:-48100}
ADB=${ADB:-adb}
command -v "$ADB" >/dev/null 2>&1 || ADB="${ANDROID_HOME:-$HOME/Library/Android/sdk}/platform-tools/adb"
TEST_PACKAGE=link.reminis.app.test
APP_PACKAGE=link.reminis.app

umask 077
work=$(mktemp -d "${TMPDIR:-/tmp}/reminislink-e2e.XXXXXX")
chmod 700 "$work"
pak_out="$work/pak.out"
: >"$pak_out"
chmod 600 "$pak_out"

make -C "$ROOT" headless HEADLESS_ARGS="--port $PORT --name Android-E2E" >"$pak_out" 2>&1 &
make_pid=$!

# Diagnostics without secrets: drop the link, the QR code (block
# characters) and any reminislink:// URL.
redact() {
  grep -v -e 'reminislink://' -e 'Pairing link' -e '█' -e '▀' -e '▄' || true
}

cleanup() {
  set +e
  # The pak is the child of make (make headless execs it): stop both by PID.
  for pid in $(pgrep -P "$make_pid" 2>/dev/null); do
    for child in $(pgrep -P "$pid" 2>/dev/null); do kill "$child" 2>/dev/null; done
    kill "$pid" 2>/dev/null
  done
  kill "$make_pid" 2>/dev/null
  wait "$make_pid" 2>/dev/null
  "$ADB" shell run-as "$APP_PACKAGE" rm -f files/e2e-pairing-link >/dev/null 2>&1
  rm -rf "$work"
}
trap cleanup EXIT INT TERM

fail() {
  echo "$1" >&2
  echo "--- pak output (redacted) ---" >&2
  redact <"$pak_out" | tail -n 30 >&2
  exit 1
}

i=0
until grep -q '^Pairing link: ' "$pak_out"; do
  kill -0 "$make_pid" 2>/dev/null || fail "the pak stopped"
  [ $i -lt 120 ] || fail "no pairing link printed"
  sleep 1
  i=$((i + 1))
done
echo "Pak started; pairing link rewritten for the emulator"

# The build and the installation never see the link.
(cd "$ANDROID" && ./gradlew --console=plain -q :app:assembleDebug :app:assembleDebugAndroidTest)
"$ADB" install -r -t "$ANDROID/app/build/outputs/apk/debug/app-debug.apk" >/dev/null
"$ADB" install -r -t "$ANDROID/app/build/outputs/apk/androidTest/debug/app-debug-androidTest.apk" >/dev/null
"$ADB" shell pm clear link.reminis.app >/dev/null

# The link goes from the private file to the device through pipes only.
sed -n 's/^Pairing link: //p' "$pak_out" | tail -n 1 | sed "s/\([?&]h=\)[0-9.]*/\110.0.2.2/" |
  "$ADB" shell run-as "$APP_PACKAGE" sh -c "'umask 077; mkdir -p files; cat > files/e2e-pairing-link'"
"$ADB" shell run-as "$APP_PACKAGE" test -s files/e2e-pairing-link || fail "the pairing link could not be written on the device"

out=$("$ADB" shell am instrument -w "$TEST_PACKAGE/androidx.test.runner.AndroidJUnitRunner" 2>&1 | redact)
printf '%s\n' "$out"
printf '%s\n' "$out" | grep -q '^OK (' || fail "instrumented tests failed"
# PakIntegrationTest deletes the link once read: a file left means it was skipped.
if "$ADB" shell run-as "$APP_PACKAGE" test -e files/e2e-pairing-link; then fail "PakIntegrationTest did not read the link"; fi
grep -E 'Paired with' "$pak_out" | redact || true
