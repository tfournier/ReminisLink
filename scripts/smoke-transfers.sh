#!/usr/bin/env bash
# Headless demonstration of milestone M4 against the real pak binary
# (cmd/reminislink/smoke_test.go): a file of RL_SMOKE_MB MiB (default 200)
# is uploaded in chunks, the pak is stopped with SIGINT in the middle and
# started again, the upload resumes at the offset and the SHA-256 of the
# installed file is checked; then a file is downloaded from a local HTTP
# server and a game is deleted to the trash, whose content is checked.
#
# Usage: scripts/smoke-transfers.sh [size in MiB]
set -euo pipefail
cd "$(dirname "$0")/.."
RL_SMOKE_MB=${1:-${RL_SMOKE_MB:-200}} CGO_ENABLED=0 \
	exec go test -count=1 -v -timeout 10m -run '^TestSmokeTransfers$' ./cmd/reminislink
