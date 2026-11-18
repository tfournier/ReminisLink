#!/bin/sh
# Copy the shared test vectors (protocol/test-vectors/*.json) to the test
# resources of the ReminisLinkKit Swift package. Never edit the copies by hand.
#
#   scripts/sync-test-vectors.sh          copy
#   scripts/sync-test-vectors.sh --check  fail if the copies differ
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SRC="$ROOT/protocol/test-vectors"
DST="$ROOT/ios/ReminisLinkKit/Tests/ReminisLinkKitTests/Resources/TestVectors"

check=0
case "${1:-}" in
  --check) check=1 ;;
  "") ;;
  *) echo "usage: $0 [--check]" >&2; exit 2 ;;
esac

status=0
if [ "$check" -eq 0 ]; then
  mkdir -p "$DST"
  # Remove copies whose source is gone.
  for f in "$DST"/*.json; do
    [ -e "$f" ] || continue
    [ -e "$SRC/$(basename "$f")" ] || rm -f "$f"
  done
fi
for f in "$SRC"/*.json; do
  name=$(basename "$f")
  if [ "$check" -eq 1 ]; then
    if ! cmp -s "$f" "$DST/$name"; then
      echo "out of date: $DST/$name (run scripts/sync-test-vectors.sh)" >&2
      status=1
    fi
  else
    cp "$f" "$DST/$name"
    echo "copied $name"
  fi
done
if [ "$check" -eq 1 ]; then
  for f in "$DST"/*.json; do
    [ -e "$f" ] || continue
    if [ ! -e "$SRC/$(basename "$f")" ]; then
      echo "stale copy: $f" >&2
      status=1
    fi
  done
  [ "$status" -eq 0 ] && echo "Swift test vectors are up to date"
fi
exit "$status"
