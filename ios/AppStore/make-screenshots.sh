#!/bin/zsh
# App Store screenshots from the demo mode (milestones M16, M15).
#
#   ios/AppStore/make-screenshots.sh <simulator id> <folder suffix>
#   ios/AppStore/make-screenshots.sh <iPad Pro 13-inch id> ipad-13
#
# For each language (en-US, fr-FR): sets the language of the simulator (so
# the status bar date matches), 9:41 and a full battery in the status bar,
# runs AppStoreScreenshotsUITests and copies its six attachments to
# docs/screenshots/appstore/<locale>-<suffix>/. The language of the
# simulator is put back at the end.
set -euo pipefail

SIM=${1:?simulator id}
SUFFIX=${2:?folder suffix, for example ipad-13}
IOS=${0:A:h:h}
OUT=$IOS/../docs/screenshots/appstore
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

simdefault() { xcrun simctl spawn "$SIM" defaults "$@"; }
xcrun simctl boot "$SIM" 2>/dev/null || true
OLD_LANGUAGES=$(simdefault read -g AppleLanguages | tr -d ' \n()"' )
OLD_LOCALE=$(simdefault read -g AppleLocale)

restore() {
  simdefault write -g AppleLanguages -array ${(s:,:)OLD_LANGUAGES}
  simdefault write -g AppleLocale "$OLD_LOCALE"
  xcrun simctl status_bar "$SIM" clear || true
  xcrun simctl shutdown "$SIM" || true
}
trap 'restore; rm -rf "$WORK"' EXIT

for pair in en:en-US:en_US fr:fr-FR:fr_FR; do
  lang=${pair%%:*}; rest=${pair#*:}; locale=${rest%%:*}; region=${rest#*:}
  simdefault write -g AppleLanguages -array "$locale"
  simdefault write -g AppleLocale "$region"
  xcrun simctl shutdown "$SIM" 2>/dev/null || true
  xcrun simctl boot "$SIM"
  xcrun simctl bootstatus "$SIM" >/dev/null
  xcrun simctl status_bar "$SIM" override --time 9:41 --batteryState discharging --batteryLevel 100 \
    --wifiBars 3 --cellularMode notSupported
  (cd "$IOS" && TEST_RUNNER_RL_SCREENSHOTS=$lang xcodebuild test -scheme ReminisLink -destination "id=$SIM" \
    -only-testing:ReminisLinkUITests/AppStoreScreenshotsUITests -resultBundlePath "$WORK/$lang.xcresult" -quiet)
  xcrun xcresulttool export attachments --path "$WORK/$lang.xcresult" --output-path "$WORK/$lang" >/dev/null
  mkdir -p "$OUT/$locale-$SUFFIX"
  python3 -I - "$WORK/$lang" "$OUT/$locale-$SUFFIX" <<'EOF'
import json, re, shutil, sys
src, dst = sys.argv[1], sys.argv[2]
for test in json.load(open(f"{src}/manifest.json")):
    for a in test["attachments"]:
        m = re.match(r"([1-6]-[a-z]+)_", a["suggestedHumanReadableName"])
        if m:
            shutil.copy(f"{src}/{a['exportedFileName']}", f"{dst}/{m.group(1)}.png")
EOF
  ls "$OUT/$locale-$SUFFIX"
done
