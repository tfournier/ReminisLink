#!/usr/bin/env bash
# Runs the pak in the userland of each platform, without the consoles
# (milestone M20, docs/specs/08-tests-qualite.md "Plateformes sans console").
#
# In linux/arm64 containers (native on an Apple Silicon Mac and on an arm64
# CI runner; qemu-user through binfmt on an x86_64 host):
#   1. the zips (dist/ReminisLink.pak.zip, dist/ReminisLink.knulli.zip,
#      when built) hold the files each firmware needs;
#   2. the Go unit tests run on a case-sensitive file system (ext4-like,
#      as Knulli's /userdata may be);
#   3. per layout, the server binary (CGO_ENABLED=0) runs headless on the
#      card of the layout and TestSmokeLayouts pairs, lists the library,
#      uploads a game and backs up a save;
#   4. per layout, the release binary (build/reminislink, or the one in the
#      NextUI zip) needs a glibc the userland has, and every library it
#      needs is in the userland, in the zip, or shipped by the firmware
#      (assumptions P3 and K2 of docs/specs/05-console-pak.md); a missing
#      one fails;
#   5. per layout with a zip, the same smoke test unzips the zip where the
#      firmware keeps it and starts the shipped binary through its launcher
#      (launch.sh or ReminisLink.sh), in an image of the userland with the
#      SDL libraries of its distribution added (h700: without SDL2_ttf, so
#      the copy in the zip must be loaded). tg5040 runs this step in the
#      Debian 11 userland: the stock rootfs has no SDL, NextUI ships it.
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
# Their file system is the container's (overlayfs, case-sensitive); the Go
# tests on macOS cover a case-insensitive one (APFS).
#
# Usage: scripts/emulation/run.sh [layout...]   (default: every layout)
#        REQUIRE_ZIPS=1: fail when a zip is missing (the CI; build them with
#        make docker-release docker-knulli-release)
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

# The libraries the firmware ships for the pak (not in the stock userland
# images): NextUI's $SYSTEM_PATH/lib or the stock OS, Knulli's buildroot.
# h700 has no SDL2_ttf: the pak bundles it (resources/lib/h700).
os_libs_of() {
	case $1 in
	h700) echo "libSDL2-2.0.so.0 libSDL2_image-2.0.so.0" ;;
	*) echo "libSDL2-2.0.so.0 libSDL2_image-2.0.so.0 libSDL2_ttf-2.0.so.0" ;;
	esac
}

# The SDL packages added to the userland for the launcher run (h700 without
# SDL2_ttf: the one of the zip is used, with the FreeType of the system,
# assumed on the stock OS as the Pak Store bundles SDL2_ttf only).
sdl_packages_of() {
	case $1 in
	h700) echo "libsdl2-2.0-0 libsdl2-image-2.0-0 libfreetype6" ;;
	*) echo "libsdl2-2.0-0 libsdl2-image-2.0-0 libsdl2-ttf-2.0-0" ;;
	esac
}

layouts=("$@")
[ ${#layouts[@]} -gt 0 ] || layouts=(tg5040 tg5050 h700 my355 knulli)
for l in "${layouts[@]}"; do
	image_of "$l" >/dev/null || { echo "unknown layout $l (tg5040 tg5050 h700 my355 knulli)" >&2; exit 2; }
done

failed=()
summary=()
fail() { failed+=("$1"); summary+=("$1: FAILED ($2)"); echo "FAIL: $1: $2"; }

out=build/emulation
mkdir -p "$out/tests"
echo "== building the linux/arm64 server binary and the tests"
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o "$out/reminislink" ./cmd/reminislink
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c -o "$out/smoke.test" ./cmd/reminislink
for p in $(go list ./internal/... ./cmd/... | grep -v '/internal/ui$'); do
	n=$(echo "${p#github.com/tfournier/ReminisLink/}" | tr / _)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c -o "$out/tests/$n.test" "$p" >/dev/null
done

# --- 1. the zips -----------------------------------------------------------

nextui_zip=dist/ReminisLink.pak.zip
knulli_zip=dist/ReminisLink.knulli.zip
# zip_entries ZIP: the files of the zip, as the Pak Store extracts them
# into Tools/<PLATFORM>/ReminisLink.pak/ (no folder stripped: launch.sh
# must be at the root).
zip_entries() { unzip -Z1 "$1" | grep -v '/$'; }
check_zip() {
	local zip=$1 name=$2 missing=""
	shift 2
	for f in "$@"; do
		zip_entries "$zip" | grep -qxF "$f" || missing="$missing $f"
	done
	if [ -n "$missing" ]; then fail "$name" "missing in $zip:$missing"; else summary+=("$name: OK"); fi
}
echo
echo "== 1. zips"
if [ -f "$nextui_zip" ]; then
	check_zip "$nextui_zip" "NextUI zip" launch.sh reminislink pak.json resources/lib/libSDL2_gfx-1.0.so.0 resources/lib/h700/libSDL2_ttf-2.0.so.0 resources/lib/my355/libSDL2_gfx-1.0.so.0
elif [ "${REQUIRE_ZIPS:-0}" = 1 ]; then
	fail "NextUI zip" "missing: $nextui_zip"
else
	summary+=("NextUI zip: skipped (no $nextui_zip: make docker-release)")
fi
if [ -f "$knulli_zip" ]; then
	check_zip "$knulli_zip" "Knulli zip" ReminisLink.sh .ReminisLink/reminislink .ReminisLink/lib/libSDL2_gfx-1.0.so.0
elif [ "${REQUIRE_ZIPS:-0}" = 1 ]; then
	fail "Knulli zip" "missing: $knulli_zip"
else
	summary+=("Knulli zip: skipped (no $knulli_zip: make docker-knulli-release)")
fi

# The release binary: build/reminislink, else the one of the NextUI zip.
release=build/reminislink
if [ ! -f "$release" ] && [ -f "$nextui_zip" ]; then
	unzip -p "$nextui_zip" reminislink >"$out/reminislink-release"
	release=$out/reminislink-release
fi
needs=""
if [ -f "$release" ]; then
	needs=$(go run ./scripts/emulation/elfneeds "$release")
fi
# Extract the libraries the zips bundle, to read what they need too.
# A new folder per run (under build/, left for inspection).
libs_dir=$(mktemp -d "$out/libs.XXXXXX")
mkdir -p "$libs_dir/nextui" "$libs_dir/knulli"
[ -f "$nextui_zip" ] && unzip -q -o "$nextui_zip" 'resources/lib/*' -d "$libs_dir/nextui" 2>/dev/null || true
[ -f "$knulli_zip" ] && unzip -q -o "$knulli_zip" '.ReminisLink/lib/*' -d "$libs_dir/knulli" 2>/dev/null || true
# bundled_libs LAYOUT: the bundled libraries the launcher of the layout
# loads, a platform folder first (as LD_LIBRARY_PATH does).
bundled_libs() {
	if [ "$1" = knulli ]; then
		find "$libs_dir/knulli" -name '*.so*' -type f
		return
	fi
	local base
	base=$(find "$libs_dir/nextui" -type d -path '*resources/lib' | head -n1)
	[ -n "$base" ] || return 0
	for f in "$base/$1"/*.so* "$base"/*.so*; do
		[ -f "$f" ] || continue
		n=$(basename "$f")
		case " $seen " in *" $n "*) continue ;; esac
		seen="$seen $n"
		echo "$f"
	done
}
# newest_glibc FILE...: the newest glibc version the ELF files need.
newest_glibc() {
	for f in "$@"; do go run ./scripts/emulation/elfneeds "$f" | sed -n 's/^glibc //p'; done |
		sort -t. -k1,1n -k2,2n | tail -n1
}

# Every docker run: the repository mounted read-only at /repo, where the
# tests find testdata/ (RL_REPO_ROOT). The default bridge network gives an
# IPv4 address, which Bonjour needs; the pak never connects out.
# run_in IMAGE [docker options...] -- COMMAND...
run_in() {
	local image=$1 opts=()
	shift
	while [ "$1" != "--" ]; do
		opts+=("$1")
		shift
	done
	shift
	"$DOCKER" run --rm --platform linux/arm64 -v "$repo:/repo:ro" -e RL_REPO_ROOT=/repo -e TMPDIR=/tmp -w /tmp \
		${opts[@]+"${opts[@]}"} "$image" "$@"
}

# --- 2. unit tests on a case-sensitive file system ---------------------------

echo
echo "== 2. unit tests on a case-sensitive file system"
if run_in "$(image_of knulli)" -- sh -c 'cd /repo && rc=0; for t in build/emulation/tests/*.test; do
	n=$(basename "$t" .test); d=$(echo "$n" | tr _ /)
	if (cd "/repo/$d" && "/repo/$t" -test.short -test.count=1 >"/tmp/$n.log" 2>&1); then echo "ok   $n"; else echo "FAIL $n"; tail -n 30 "/tmp/$n.log"; rc=1; fi
done; exit $rc' 2>&1 | sed 's/^/    /'; then
	summary+=("unit tests (case-sensitive): OK")
else
	fail "unit tests (case-sensitive)" "see above"
fi

# launcher_smoke LAYOUT: step 5, appending to $result (1 when it failed).
# tg5040 runs in the Debian 11 userland of tg5050 (glibc 2.31, older than
# the Brick's 2.33): its stock rootfs has no SDL (NextUI ships it), and is
# not in the CI anyway; this covers launch.sh with PLATFORM=tg5040.
launcher_smoke() {
	local l=$1 zip=$nextui_zip base sdl_image apt_fix=""
	[ "$l" = knulli ] && zip=$knulli_zip
	if [ ! -f "$zip" ]; then
		result="$result; launcher: skipped (no $zip)"
		return 0
	fi
	base=$(image_of "$l")
	[ "$l" = tg5040 ] && base=$(image_of tg5050)
	sdl_image="reminislink-emulation-$l"
	# Debian: the release only (its security mirror may lag behind its
	# index, and the libraries only need to load); Debian 10 is archived
	# on archive.debian.org.
	case $l in
	my355) apt_fix='echo "deb http://archive.debian.org/debian buster main" >/etc/apt/sources.list && rm -f /etc/apt/sources.list.d/* && ' ;;
	tg5040 | tg5050 | knulli) apt_fix='echo "deb http://deb.debian.org/debian $(. /etc/os-release && echo $VERSION_CODENAME) main" >/etc/apt/sources.list && rm -f /etc/apt/sources.list.d/* && ' ;;
	esac
	printf 'FROM %s\nRUN %sapt-get update -qq && apt-get install -y -qq --no-install-recommends %s >/dev/null && rm -rf /var/lib/apt/lists/*\n' \
		"$base" "$apt_fix" "$(sdl_packages_of "$l")" |
		"$DOCKER" build -q --platform linux/arm64 -t "$sdl_image" - >/dev/null
	if run_in "$sdl_image" -e RL_LAYOUT_ZIP="/repo/$zip" -e RL_LAYOUTS="$l" -v "$repo/$out:/rl:ro" \
		-- /rl/smoke.test -test.run '^TestSmokeLayouts$' -test.v -test.count=1 -test.timeout 5m 2>&1 | sed 's/^/    /'; then
		result="$result; launcher with the shipped binary: smoke OK"
		[ "$l" = tg5040 ] && result="$result (in the Debian 11 userland)"
		return 0
	fi
	fail "$l" "smoke through the launcher"
	return 1
}

# --- 3 to 5. per layout ------------------------------------------------------

for l in "${layouts[@]}"; do
	image=$(image_of "$l")
	echo
	echo "== $l in $image"
	if ! "$DOCKER" image inspect "$image" >/dev/null 2>&1 && [ "$l" = tg5040 ]; then
		echo "SKIP: $image is not on this machine (stock rootfs of the Brick, imported by hand)"
		result="smoke in the stock rootfs: skipped (no image)"
		launcher_smoke "$l" || continue
		summary+=("$l: $result")
		continue
	fi
	result=""

	# 3. The server binary on the card of the layout.
	if run_in "$image" -v "$repo/$out:/rl:ro" -e RL_LAYOUT_BIN=/rl/reminislink -e RL_LAYOUTS="$l" \
		-- /rl/smoke.test -test.run '^TestSmokeLayouts$' -test.v -test.count=1 -test.timeout 5m 2>&1 | sed 's/^/    /'; then
		result="smoke OK"
	else
		fail "$l" "smoke of the server binary"
		continue
	fi

	# 4. What the release binary needs.
	if [ -n "$needs" ]; then
		seen=""
		libs_of_layout=$(bundled_libs "$l")
		# shellcheck disable=SC2086
		want=$(newest_glibc "$release" $libs_of_layout)
		# glibc's libc.so.6 is executable and prints its version.
		have=$(run_in "$image" -- sh -c 'for f in /lib/aarch64-linux-gnu/libc.so.6 /lib/libc.so.6 /lib64/libc.so.6; do [ -x "$f" ] && { "$f" | head -n1; break; }; done' |
			sed -n 's/.*version \([0-9][0-9.]*[0-9]\).*/\1/p')
		if [ -z "$have" ] || [ "$(printf '%s\n%s\n' "$want" "$have" | sort -t. -k1,1n -k2,2n | head -n1)" != "$want" ]; then
			fail "$l" "the release binary or a bundled library needs glibc $want, userland has ${have:-unknown}"
			continue
		fi
		libs=$(printf '%s\n' "$needs" | sed -n 's/^needed //p' | tr '\n' ' ')
		absent=$(run_in "$image" -- sh -c 'for l in '"$libs"'; do found=; for d in /lib /lib64 /usr/lib /lib/aarch64-linux-gnu /usr/lib/aarch64-linux-gnu; do [ -e "$d/$l" ] && found=1; done; [ -n "$found" ] || printf "%s " "$l"; done')
		bundled=""
		for f in $libs_of_layout; do bundled="$bundled $(basename "$f")"; done
		missing=""
		for lib in $absent; do
			case " $(os_libs_of "$l") $(echo $bundled) " in
			*" $lib "*) ;;
			*) missing="$missing $lib" ;;
			esac
		done
		if [ -n "$missing" ]; then
			fail "$l" "release binary needs$missing: not in the userland, the zip or the firmware"
			continue
		fi
		result="$result; release binary and bundled libraries: glibc $want <= $have, libraries found (bundled:$bundled; firmware: $(os_libs_of "$l"))"
	fi

	# 5. The shipped binary through its launcher.
	launcher_smoke "$l" || continue
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
