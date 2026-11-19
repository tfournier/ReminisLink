#!/bin/sh
# ReminisLink - NextUI launcher (from Cheat Manager's pak/launch.sh).
#
# NextUI runs this script with eval from the folder of MinUI.pak, in the
# foreground and as root (docs/specs/05-console-pak.md §1): go to the pak
# folder first.
PAK_DIR="$(dirname "$0")"
cd "$PAK_DIR" || exit 1
PAK_DIR="$(pwd)"

# libSDL2_gfx is not shipped with NextUI: use the bundled copy.
export LD_LIBRARY_PATH="$PAK_DIR/resources/lib:$LD_LIBRARY_PATH"

LOG_DIR="${LOGS_PATH:-/mnt/SDCARD/.userdata/$PLATFORM/logs}"
mkdir -p "$LOG_DIR"

# Only stderr goes to a file. stdout is never redirected: in --headless mode
# the pak prints the pairing secret and the PIN there, and they must never
# end up in a log file. The pak writes its own log to $LOGS_PATH/ReminisLink.txt.
#
# exec: the pak takes the PID of this script, so a SIGTERM sent to the
# launcher (poweroff, a supervisor) reaches it and it shuts down cleanly.
# The final sync of Cheat Manager's launcher is done by the pak itself
# (app.Shutdown syncs the SD card before exiting).
exec ./reminislink "$@" 2>>"$LOG_DIR/ReminisLink.stderr.txt"
