#!/bin/sh
# Takes the README screenshots of the desktop UI (make screenshots): runs
# the pak with --desktop under Xvfb on a copy of testdata/sdcard, drives it
# with xdotool and a fake paired client, and saves the PNG files in /out.
# Runs inside the image of scripts/screenshots/Dockerfile, with the
# repository mounted read-only on /repo.
#
# Two sessions: the first one without paired device (language picker, main
# screen with the QR code: pairing is on demand, M8), the second one with
# the fake iPhone paired (main screen, settings, pairing screen, transfer,
# exit confirmation). The console has a neutral name and a fake battery.
set -eu
export GOFLAGS=-buildvcs=false
cd /repo
go build -tags sdl -o /tmp/reminislink ./cmd/reminislink
go build -o /tmp/drive ./scripts/screenshots/drive

SD="$(mktemp -d /tmp/sd.XXXXXX)"
cp -R /repo/testdata/sdcard/. "$SD/"
mkdir -p "$SD/.system" && printf 'NextUI-20261013-0\n39a06de\n' > "$SD/.system/version.txt"
# A fake battery, read off the console from <SD>/.sys (78 %, not charging).
mkdir -p "$SD/.sys/class/power_supply/axp2202-battery" "$SD/.sys/class/power_supply/axp2202-usb"
echo 78 > "$SD/.sys/class/power_supply/axp2202-battery/capacity"
echo 0 > "$SD/.sys/class/power_supply/axp2202-battery/time_to_full_now"
echo 0 > "$SD/.sys/class/power_supply/axp2202-usb/online"

Xvfb :99 -screen 0 1280x1024x24 >/dev/null 2>&1 &
XVFB=$!
export DISPLAY=:99
sleep 1

PAK=
start_pak() {
	/tmp/reminislink --desktop --sd "$SD" --name "Brick" --port 47821 >/dev/null 2>>/tmp/stderr.txt &
	PAK=$!
}
stop_pak() {
	sleep 4
	if kill -0 "$PAK" 2>/dev/null; then echo "the pak did not exit"; kill "$PAK"; exit 1; fi
	wait "$PAK"
}
win() { xdotool search --name ReminisLink | head -1; }
shot() { sleep "${2:-2}"; import -window "$(win)" "/out/$1.png"; }
key() { w=$(win); xdotool windowfocus --sync "$w" 2>/dev/null || true; xdotool key --window "$w" "$1"; }

# 1. No device paired yet.
start_pak
shot language 4          # first launch: language picker
key a                    # English
shot main 4              # QR code and PIN on the main screen
key b                    # quit
stop_pak

# 2. The fake iPhone is paired.
/tmp/drive register "$SD/.userdata/shared/ReminisLink/clients.json"
start_pak
sleep 4
key y
shot settings 2
key a                    # Pair a New Device
shot pairing 3
key b                    # back to the settings
sleep 2
key b                    # back to the main screen
sleep 2
/tmp/drive start         # the iPhone sends a ROM (2 chunks of 3)
shot transfer 2
key b
shot quit 2              # a transfer is in progress: confirmation
key b
/tmp/drive finish
sleep 2
key b                    # quit
stop_pak

kill "$XVFB" || true
if grep -E "level=(ERROR|WARN)" "$SD/.userdata/tg5040/logs/ReminisLink.txt"; then exit 1; fi
ls -l /out
