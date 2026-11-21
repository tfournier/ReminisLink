#!/usr/bin/env bash
# Headless demonstration of milestone M18 against the real pak binary
# (cmd/reminislink/smoke_trash_test.go): a deleted game (with its clock
# file, states, cheats and art), a replaced save (its before-replace copy,
# deleted to the trash) and a deleted emulator pak come back from the
# trash; a restore over a present file answers 409, then goes under a free
# name; a restore cut by a power loss is finished at the next start.
#
# Usage: scripts/smoke-trash.sh
set -euo pipefail
cd "$(dirname "$0")/.."
CGO_ENABLED=0 exec go test -count=1 -v -timeout 5m -run '^TestSmokeTrash$' ./cmd/reminislink
