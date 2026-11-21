#!/usr/bin/env bash
# Runs the pak in the userland of each platform, without the consoles
# (milestone M20, docs/specs/08-tests-qualite.md "Plateformes sans console").
#
# For each layout, in a linux/arm64 container (native on an Apple Silicon
# Mac; qemu-user through binfmt on an x86_64 host):
#   1. the server binary (CGO_ENABLED=0, linux/arm64) runs headless on the
#      card of the layout and TestSmokeLayouts (cross-compiled too) pairs,
#      lists the library, uploads a game and backs up a save;
#   2. when the release binary exists (build/reminislink, "make docker-build"),
#      the newest glibc version it needs is compared with the glibc of the
#      userland, and its libraries are looked up there.
#
# Userlands (pinned): tg5040 is the stock rootfs of the Brick (firmware
# 1.1.1, imported by hand as reminislink/brick-rootfs:1.1.1, see
# docs/specs/m0/source-analysis.md "Firmware et rootfs"; skipped when the
# image is missing). The others are Debian or Ubuntu images with the glibc
# of the firmware, or an older one (glibc is backward compatible):
#   tg5050  Debian 11, glibc 2.31 (NextUI tg5050 toolchain: glibc 2.33)
#   h700    Ubuntu 22.04, glibc 2.35 (the stock OS, h700-toolchain)
#   my355   Debian 10, glibc 2.28 (NextUI my355 toolchain: glibc 2.28)
#   knulli  Debian 12, glibc 2.36 (Knulli's buildroot 2024.11: glibc 2.40)
# Their file system is the container's (overlayfs, case-sensitive like
# ext4); the Go tests on macOS cover a case-insensitive one (APFS).
#
# Usage: scripts/emulation/run.sh [layout...]   (default: every layout)
#        layouts: tg5040 tg5050 h700 my355 knulli
set -euo pipefail
cd "$(dirname "$0")/../.."
repo=$(pwd)
DOCKER=${DOCKER:-docker}

image_of() {
	case $1 in
	tg5040) echo "reminislink/brick-rootfs:1.1.1" ;;
	tg5050) echo "debian:bullseye@sha256:6f519a81440354a85eb592c5f32109ab80605f6b892455983a6f618bf87fabe9" ;;
	h700) echo "ubuntu:22.04@sha256:f64b2ffc2f10ab8f8505aa56b2664bd846cbb9df87c7e2532e30f7667021c0e6" ;;
	my355) echo "debian:buster@sha256:58ce6f1271ae1c8a2006ff7d3e54e9874d839f573d8009c20154ad0f2fb0a225" ;;
	knulli) echo "debian:bookworm@sha256:268fb24c39bca92d326dd83ea3abebe4a3f7751c804ffef23bb6d857dd343ff8" ;;
	*) return 1 ;;
	esac
}

layouts=("$@")
[ ${#layouts[@]} -gt 0 ] || layouts=(tg5040 tg5050 h700 my355 knulli)
for l in "${layouts[@]}"; do
	image_of "$l" >/dev/null || { echo "unknown layout $l (tg5040 tg5050 h700 my355 knulli)" >&2; exit 2; }
done

out=build/emulation
mkdir -p "$out"
echo "== building the linux/arm64 server binary and the smoke test"
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o "$out/reminislink" ./cmd/reminislink
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c -o "$out/smoke.test" ./cmd/reminislink

release=build/reminislink
needs=""
if [ -f "$release" ]; then
	needs=$(go run ./scripts/emulation/elfneeds "$release")
fi

# version_le A B: A <= B for dotted versions.
version_le() { [ "$(printf '%s\n%s\n' "$1" "$2" | sort -t. -k1,1n -k2,2n -k3,3n | head -n1)" = "$1" ]; }

failed=()
summary=()
for l in "${layouts[@]}"; do
	image=$(image_of "$l")
	echo
	echo "== $l in $image"
	if ! $DOCKER image inspect "$image" >/dev/null 2>&1 && [ "$l" = tg5040 ]; then
		echo "SKIP: $image is not on this machine (stock rootfs of the Brick, imported by hand)"
		summary+=("$l: skipped (no rootfs image)")
		continue
	fi
	# The repository is mounted at its own path: the smoke test finds
	# testdata/ there (testutil.RepoRoot). The default bridge network gives
	# the container an IPv4 address, which Bonjour needs; the pak never
	# connects out.
	run=("$DOCKER" run --rm --platform linux/arm64
		-v "$repo:$repo:ro" -v "$repo/$out:/rl:ro" -e TMPDIR=/tmp -w /tmp
		-e RL_LAYOUT_BIN=/rl/reminislink -e RL_LAYOUTS="$l" "$image")
	if "${run[@]}" /rl/smoke.test -test.run '^TestSmokeLayouts$' -test.v -test.count=1 \
		-test.timeout 5m 2>&1 | sed 's/^/    /'; then
		result="smoke OK"
	else
		result="smoke FAILED"
		failed+=("$l")
	fi
	if [ -n "$needs" ]; then
		want=$(printf '%s\n' "$needs" | sed -n 's/^glibc //p')
		# glibc's libc.so.6 is executable and prints its version.
		have=$("${run[@]}" sh -c 'for f in /lib/aarch64-linux-gnu/libc.so.6 /lib/libc.so.6 /lib64/libc.so.6; do [ -x "$f" ] && { "$f" | head -n1; break; }; done' |
			sed -n 's/.*version \([0-9][0-9.]*[0-9]\).*/\1/p')
		libs=$(printf '%s\n' "$needs" | sed -n 's/^needed //p' | tr '\n' ' ')
		missing=$("${run[@]}" sh -c 'for l in '"$libs"'; do found=; for d in /lib /lib64 /usr/lib /lib/aarch64-linux-gnu /usr/lib/aarch64-linux-gnu; do [ -e "$d/$l" ] && found=1; done; [ -n "$found" ] || printf "%s " "$l"; done')
		if [ -n "$have" ] && version_le "$want" "$have"; then
			result="$result; release binary: needs glibc $want, userland has $have: OK"
		else
			result="$result; release binary: needs glibc $want, userland has ${have:-unknown}: TOO OLD"
			failed+=("$l (glibc)")
		fi
		[ -z "$missing" ] || result="$result; libraries not in the userland (shipped by the OS or the pak on the console): $missing"
	fi
	echo "$l: $result"
	summary+=("$l: $result")
done

echo
echo "== summary"
printf '%s\n' "${summary[@]}"
if [ ${#failed[@]} -gt 0 ]; then
	echo "FAILED: ${failed[*]}"
	exit 1
fi
echo "emulation: OK"
