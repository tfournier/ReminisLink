#!/bin/sh
# Takes the README screenshots of the desktop UI (make screenshots): runs
# the pak with --desktop under Xvfb on a copy of testdata/sdcard, drives it
# with xdotool and a fake paired client, and saves the PNG files in /out.
# Runs inside the image of scripts/screenshots/Dockerfile, with the
# repository mounted read-only on /repo.
set -eu
export GOFLAGS=-buildvcs=false
cd /repo
go build -tags sdl -o /tmp/reminislink ./cmd/reminislink
go build -o /tmp/drive ./scripts/screenshots/drive

SD="$(mktemp -d /tmp/sd.XXXXXX)"
cp -R /repo/testdata/sdcard/. "$SD/"
mkdir -p "$SD/.system" && printf 'NextUI-20261013-0\n39a06de\n' > "$SD/.system/version.txt"
/tmp/drive register "$SD/.userdata/shared/ReminisLink/clients.json"

Xvfb :99 -screen 0 1280x1024x24 >/dev/null 2>&1 &
XVFB=$!
export DISPLAY=:99
sleep 1
/tmp/reminislink --desktop --sd "$SD" --name "Brick de Thomas" --port 47821 >/dev/null 2>/tmp/stderr.txt &
PAK=$!

win() { xdotool search --name ReminisLink | head -1; }
shot() { sleep "${2:-2}"; import -window "$(win)" "/out/$1.png"; }
key() { w=$(win); xdotool windowfocus --sync "$w" 2>/dev/null || true; xdotool key --window "$w" "$1"; }

shot language 4          # first launch: language picker
key a                    # English
shot main 4
key y
shot settings 2
key b
/tmp/drive start         # a paired iPhone sends a ROM (2 chunks of 3)
shot transfer 2
key b
shot quit 2              # a transfer is in progress: confirmation
key b
/tmp/drive finish
sleep 2
key b                    # quit
sleep 4
if kill -0 "$PAK" 2>/dev/null; then echo "the pak did not exit"; kill "$PAK"; exit 1; fi
wait "$PAK"
kill "$XVFB" || true
if grep -E "level=(ERROR|WARN)" "$SD/.userdata/tg5040/logs/ReminisLink.txt"; then exit 1; fi
ls -l /out
