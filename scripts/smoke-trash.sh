#!/usr/bin/env bash
# Headless demonstration of milestone M18 against the real pak binary
# (cmd/reminislink/smoke_trash_test.go): a deleted game (with its clock
# file, states, cheats and art), a replaced save (its before-replace copy,
# deleted to the trash) and a deleted emulator pak come back from the
# trash; a restore over a present file answers 409, then goes under a free
# name; power cuts: before the first rename (cancelled), after one
# (finished, bytes checked), with a tampered journal (refused); a replaced
# save comes back over the current save (its backup copy: the current save
# kept as a backup copy; a save of the save folder: the current save and
# clock go to the trash first); a game cannot replace (422).
#
# Usage: scripts/smoke-trash.sh
set -euo pipefail
cd "$(dirname "$0")/.."
CGO_ENABLED=0 exec go test -count=1 -v -timeout 5m -run '^TestSmokeTrash$' ./cmd/reminislink
