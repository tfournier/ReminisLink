#!/bin/sh
# The Android versionCode of an app version (milestone M21), the same formula
# as android/app/build.gradle.kts:
#
#   X.Y.Z          X*1000000 + Y*10000 + Z*100 + 99
#   X.Y.Z-beta.N   X*1000000 + Y*10000 + Z*100 + N        (N 1..49)
#   X.Y.Z-rc.N     X*1000000 + Y*10000 + Z*100 + 50 + N   (N 1..48)
#
# so that a pre-release always comes before its release and every release
# after the previous one (Y and Z at most 99). Accepts the version or its tag
# (android-vX.Y.Z...). Anything else exits 1.
#
# Usage: scripts/android-version-code.sh <version|tag>
set -eu

v="${1:-}"
v="${v#android-v}"
fail() {
	echo "Not an Android app version: '${1:-}' (expected X.Y.Z, X.Y.Z-beta.N or X.Y.Z-rc.N, or android-v...)" >&2
	exit 1
}
printf '%s\n' "$v" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-(beta|rc)\.[0-9]+)?$' || fail "$1"
core="${v%%-*}"
x="${core%%.*}"
rest="${core#*.}"
y="${rest%%.*}"
z="${rest#*.}"
# No leading zeros (01.2.3 is not SemVer).
for part in "$x" "$y" "$z"; do
	case "$part" in 0?*) fail "$1" ;; esac
done
[ "$y" -le 99 ] && [ "$z" -le 99 ] || fail "$1"
stage=99
case "$v" in
*-beta.*)
	n="${v##*-beta.}"
	[ "$n" -ge 1 ] && [ "$n" -le 49 ] || fail "$1"
	stage=$n
	;;
*-rc.*)
	n="${v##*-rc.}"
	[ "$n" -ge 1 ] && [ "$n" -le 48 ] || fail "$1"
	stage=$((50 + n))
	;;
esac
code=$((x * 1000000 + y * 10000 + z * 100 + stage))
[ "$code" -le 2100000000 ] || fail "$1"
echo "$code"
