#!/usr/bin/env bash
# Headless demonstration of milestone M22 against the real pak binary
# (cmd/reminislink/smoke_screenshots_test.go): the screenshots of the
# NextUI card (Screenshots/) are listed (hidden files, other files, links
# and names without an identifier left out), downloaded byte for byte,
# previewed (a forged huge PNG gets none), moved to the trash in one entry
# with a screenshots event, and restored by the M18 restore (409 over a
# new file of the same name, then a free name). The same checks run on the
# five card layouts (TestSmokeLayouts, also inside make emulation).
#
# Usage: scripts/smoke-screenshots.sh
set -euo pipefail
cd "$(dirname "$0")/.."
CGO_ENABLED=0 exec go test -count=1 -v -timeout 5m -run '^(TestSmokeScreenshots|TestSmokeLayouts)$' ./cmd/reminislink
