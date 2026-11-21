#!/bin/bash
# ReminisLink - Knulli launcher (experimental, milestone M20).
#
# Installed as /userdata/roms/tools/ReminisLink.sh, next to the hidden
# folder .ReminisLink/ that holds the binary (EmulationStation does not list
# hidden folders). Knulli runs the scripts of the Tools system with
# /bin/bash (configgen generators/sh/shGenerator.py), in the foreground.
DIR="$(cd "$(dirname "$0")/.ReminisLink" && pwd)" || exit 1
cd "$DIR" || exit 1

# The pak reads the Knulli layout (/userdata) instead of NextUI's.
export REMINISLINK_OS=knulli
# libSDL2_gfx is bundled, as on NextUI.
export LD_LIBRARY_PATH="$DIR/lib:$LD_LIBRARY_PATH"

LOG_DIR=/userdata/system/logs
mkdir -p "$LOG_DIR"

# stdout is never redirected (see pak/launch.sh): only stderr goes to a
# file; the pak writes its own log to $LOG_DIR/ReminisLink.txt.
exec ./reminislink "$@" 2>>"$LOG_DIR/ReminisLink.stderr.txt"
