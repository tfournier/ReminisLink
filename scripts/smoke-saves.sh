#!/usr/bin/env bash
# Headless demonstration of milestone M5 against the real pak binary
# (cmd/reminislink/smoke_saves_test.go): a locked state is changed by hand
# on a copy of the fake SD card while the pak is closed; at the next start
# the pak restores it and GET /states answers lockStatus "restored", then
# "intact". Then a save is backed up, replaced and restored, and cheats are
# imported and edited, with an ETag conflict (412).
#
# Usage: scripts/smoke-saves.sh
set -euo pipefail
cd "$(dirname "$0")/.."
CGO_ENABLED=0 exec go test -count=1 -v -timeout 5m -run '^TestSmokeSaves$' ./cmd/reminislink
