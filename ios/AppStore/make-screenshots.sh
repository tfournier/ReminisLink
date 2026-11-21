#!/bin/zsh
# App Store screenshots from the demo mode (milestones M16, M15).
#
#   ios/AppStore/make-screenshots.sh <simulator id> <folder suffix> <width> <height>
#   ios/AppStore/make-screenshots.sh <iPad Pro 13-inch id> ipad-13 2064 2752
#
# For each language (en-US, fr-FR): sets the language of the simulator (so
# the status bar date matches), 9:41 and a full battery in the status bar,
# and runs AppStoreScreenshotsUITests. Both series are checked (the six
# names exactly once, the expected size, no alpha) before
# docs/screenshots/appstore/<locale>-<suffix>/ is replaced; nothing is
# replaced if a check fails.
#
# The simulator must be shut down: simctl cannot read status bar overrides
# back, so a booted one (whose overrides would be lost) is refused. At the
# end its language and region are put back (only those that were read),
# the overrides set here are cleared and it is shut down again.
set -uo pipefail

SIM=${1:?simulator id}
SUFFIX=${2:?folder suffix, for example ipad-13}
WIDTH=${3:?width in pixels}
HEIGHT=${4:?height in pixels}
IOS=${0:A:h:h}
OUT=$IOS/../docs/screenshots/appstore
NAMES=(1-library 2-game 3-states 4-cheats 5-apps 6-consoles)

simstate() { xcrun simctl list devices | command grep -F "$SIM" | command grep -q "(Booted)"; }
simdefault() { xcrun simctl spawn "$SIM" defaults "$@"; }

if simstate; then
  echo "error: shut the simulator down first (xcrun simctl shutdown $SIM): its status bar overrides cannot be read back" >&2
  exit 1
fi

WORK=$(mktemp -d)
LANGUAGES_READ=0; OLD_LANGUAGES=""
LOCALE_READ=0; OLD_LOCALE=""

# Installed first: every step runs even if another fails.
cleanup() {
  if simstate; then
    if (( LANGUAGES_READ )); then
      if [[ -n $OLD_LANGUAGES ]]; then
        simdefault write -g AppleLanguages -array ${(s:,:)OLD_LANGUAGES} || echo "warning: AppleLanguages not restored" >&2
      else
        simdefault delete -g AppleLanguages 2>/dev/null || true
      fi
    fi
    if (( LOCALE_READ )); then
      if [[ -n $OLD_LOCALE ]]; then
        simdefault write -g AppleLocale "$OLD_LOCALE" || echo "warning: AppleLocale not restored" >&2
      else
        simdefault delete -g AppleLocale 2>/dev/null || true
      fi
    fi
    xcrun simctl status_bar "$SIM" clear || echo "warning: status bar not cleared" >&2
    xcrun simctl shutdown "$SIM" 2>/dev/null || echo "warning: simulator not shut down" >&2
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT INT TERM

# Reads a global preference: sets VALUE and returns 0 when it was read
# (VALUE empty when it does not exist), 1 when the read failed.
readpref() {
  local out
  if out=$(simdefault read -g "$1" 2>&1); then
    VALUE=$out; return 0
  fi
  [[ $out == *"does not exist"* ]] && { VALUE=""; return 0; }
  return 1
}

fail() { echo "error: $*" >&2; exit 1; }

xcrun simctl boot "$SIM" 2>/dev/null || true
xcrun simctl bootstatus "$SIM" >/dev/null || fail "the simulator did not boot"
# Only what was read is put back at the end.
readpref AppleLanguages || fail "AppleLanguages not read"
OLD_LANGUAGES=$(print -r -- "$VALUE" | tr -d ' \n()"'); LANGUAGES_READ=1
readpref AppleLocale || fail "AppleLocale not read"
OLD_LOCALE=$VALUE; LOCALE_READ=1

for pair in en:en-US:en_US fr:fr-FR:fr_FR; do
  lang=${pair%%:*}; rest=${pair#*:}; locale=${rest%%:*}; region=${rest#*:}
  simdefault write -g AppleLanguages -array "$locale" || fail "language not set"
  simdefault write -g AppleLocale "$region" || fail "region not set"
  # The status bar takes the language at boot.
  xcrun simctl shutdown "$SIM" 2>/dev/null
  xcrun simctl boot "$SIM" || fail "the simulator did not boot"
  xcrun simctl bootstatus "$SIM" >/dev/null || fail "the simulator did not boot"
  xcrun simctl status_bar "$SIM" override --time 9:41 --batteryState discharging --batteryLevel 100 \
    --wifiBars 3 --cellularMode notSupported || fail "status bar not set"
  (cd "$IOS" && TEST_RUNNER_RL_SCREENSHOTS=$lang xcodebuild test -scheme ReminisLink -destination "id=$SIM" \
    -only-testing:ReminisLinkUITests/AppStoreScreenshotsUITests -resultBundlePath "$WORK/$lang.xcresult" -quiet) \
    || fail "the screenshot test failed ($lang)"
  xcrun xcresulttool export attachments --path "$WORK/$lang.xcresult" --output-path "$WORK/$lang" >/dev/null \
    || fail "attachments not exported ($lang)"
  mkdir -p "$WORK/out/$locale"
  # Each expected name exactly once, then the size and no alpha.
  python3 -I - "$WORK/$lang" "$WORK/out/$locale" "$WIDTH" "$HEIGHT" "${NAMES[@]}" <<'EOF' || fail "bad series ($locale)"
import json, re, shutil, subprocess, sys
src, dst, width, height, *names = sys.argv[1:]
found = {}
for test in json.load(open(f"{src}/manifest.json")):
    for a in test["attachments"]:
        m = re.fullmatch(r"([1-6]-[a-z]+)_\d+_[0-9A-F-]+\.png", a["suggestedHumanReadableName"])
        if m:
            found.setdefault(m.group(1), []).append(a["exportedFileName"])
problems = [f"{n}: {len(found.get(n, []))} found" for n in names if len(found.get(n, [])) != 1]
problems += [f"unexpected {n}" for n in found if n not in names]
for n in names:
    if len(found.get(n, [])) == 1:
        path = f"{src}/{found[n][0]}"
        info = subprocess.run(["sips", "-g", "pixelWidth", "-g", "pixelHeight", "-g", "hasAlpha", path],
                              capture_output=True, text=True, check=True).stdout
        values = dict(re.findall(r"(\w+): (\w+)", info))
        if (values.get("pixelWidth"), values.get("pixelHeight"), values.get("hasAlpha")) != (width, height, "no"):
            problems.append(f"{n}: {values}")
        shutil.copy(path, f"{dst}/{n}.png")
if problems:
    sys.exit("\n".join(problems))
EOF
done

# Both series are complete: replace the folders.
for locale in en-US fr-FR; do
  target="$OUT/$locale-$SUFFIX"
  rm -rf "$target.new" "$target.old"
  # Copied next to the folder first, so the swap is two renames.
  cp -R "$WORK/out/$locale" "$target.new" || fail "$target.new not written"
  [[ -d $target ]] && { mv "$target" "$target.old" || fail "$target not moved aside"; }
  mv "$target.new" "$target" || { [[ -d $target.old ]] && mv "$target.old" "$target"; fail "$target not replaced"; }
  rm -rf "$target.old"
  ls "$target"
done
