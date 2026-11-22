#!/usr/bin/env bash
# Headless demonstration of milestone M23 against the real pak binary
# (cmd/reminislink/smoke_files_test.go): a power cut between the renames
# of a game is put back at start (journal); a folder is made in a system
# folder, a file uploaded into it (409, then a free name), both moved to
# the trash and restored; the protected places are refused; a game is
# renamed with its save and renamed back. The same checks run on the five
# card layouts (TestSmokeLayouts, also inside make emulation).
#
# Usage: scripts/smoke-files.sh
set -euo pipefail
cd "$(dirname "$0")/.."
CGO_ENABLED=0 exec go test -count=1 -v -timeout 5m -run '^(TestSmokeFiles|TestSmokeLayouts)$' ./cmd/reminislink
