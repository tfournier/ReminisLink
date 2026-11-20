#!/usr/bin/env bash
# Smoke test of milestone M11 (flat library and file downloads) against the
# real pak, with curl and openssl only (RL2 and TLS pinning of M13, see
# scripts/lib/rl2.sh). One client pairs, then:
#   - GET /games: its number of games equals the sum of gameCount of
#     GET /systems, truncated is false, the games of a subfolder have
#     folderId and folderPath (Hacks, Hacks/Deeper of GBA), those of the
#     root have them null;
#   - GET /games/{gameId} of Starlight Courier (GBA): every file has a
#     fileId;
#   - GET /games/{gameId}/files/{fileId} of its ROM and of its .cht: the
#     SHA-256 and the size equal those of the files of the SD card;
#   - an unknown fileId, and the fileId of another game, get 404;
#   - the client unpairs (DELETE /clients/me).
#
# Usage:
#   scripts/smoke-library.sh
#       builds build/reminislink-host, starts it headless with
#       --pairing-open on a temporary copy of testdata/sdcard, runs the
#       checks and stops it (by PID).
#   PAK_LOG=<file> SD=<card> scripts/smoke-library.sh
#       uses a pak already running with --pairing-open on the card <card>
#       (a copy of testdata/sdcard) whose standard output goes to <file>.
#
# The pairing link (a single-use secret) is read from the pak output.
# Secrets only go through pipes and bash variables, never through process
# arguments, the private key stays in a private temporary file, and
# nothing secret is printed. Exits 1 if any check fails. Needs OpenSSL 3.
set -euo pipefail
cd "$(dirname "$0")/.."

if [ $# -gt 0 ]; then
	sed -n '2,27p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
fi

failures=0
pass() { echo "PASS: $*"; }
failed() { echo "FAIL: $*"; failures=$((failures + 1)); }
die() { echo "smoke-library: $*" >&2; exit 1; }
# check DESCRIPTION EXPECTED ACTUAL
check() {
	if [ "$2" = "$3" ]; then pass "$1"; else failed "$1 (expected '$2', got '$3')"; fi
}

# --- the pak ----------------------------------------------------------------

pak_pid=""
workdir=$(mktemp -d "${TMPDIR:-/tmp}/smoke-library.XXXXXX")
cleanup() {
	if [ -n "$pak_pid" ]; then
		kill -INT "$pak_pid" 2>/dev/null || true
		wait "$pak_pid" 2>/dev/null || true
	fi
	rm -rf "$workdir"
}
trap cleanup EXIT

if [ -n "${PAK_LOG:-}" ]; then
	log=$PAK_LOG
	[ -r "$log" ] || die "cannot read PAK_LOG $log"
	sd=${SD:-}
	[ -d "$sd/Roms" ] || die "set SD to the card of the running pak"
else
	mkdir -p build
	CGO_ENABLED=0 go build -o build/reminislink-host ./cmd/reminislink
	sd=$workdir/sdcard
	mkdir -p "$sd"
	cp -R testdata/sdcard/. "$sd/"
	log=$workdir/pak.log
	build/reminislink-host --headless --sd "$sd" --pairing-open --name "smoke-library pak" >"$log" 2>&1 &
	pak_pid=$!
	echo "pak started (PID $pak_pid) on a copy of testdata/sdcard"
fi

last_link=""
# next_link: waits (10 s at most) until the pak prints a pairing link other
# than the last one used, and stores it in $link.
next_link() {
	local i l
	for ((i = 0; i < 100; i++)); do
		l=$(sed -n 's/^Pairing link: //p' "$log" | tail -n 1)
		if [ -n "$l" ] && [ "$l" != "$last_link" ]; then
			link=$l
			last_link=$l
			return 0
		fi
		if [ -n "$pak_pid" ] && ! kill -0 "$pak_pid" 2>/dev/null; then die "the pak stopped (log: $log)"; fi
		sleep 0.1
	done
	die "no new pairing link in the pak output (is pairing open? run the pak with --pairing-open)"
}

# --- RL2 client (scripts/lib/rl2.sh) ---------------------------------------

# shellcheck source=lib/rl2.sh
. scripts/lib/rl2.sh
RL2_DIR=$workdir/keys
mkdir -m 700 "$RL2_DIR"
rl2_selftest

# pair X NAME: pairs client X with the next pairing link.
pair() {
	next_link
	rl2_use_link "$link"
	base=$RL2_BASE
	api=$base/api/v1
	rl2_pair "$1" "$2" || die "POST /pair ($1): $RL2_ERROR"
	pass "client $1 \"$2\" paired, device proof verified"
}
req() { rl2_req "$@"; }
# fetch X PATH FILE: signed GET of client X, the body written to FILE.
fetch() { rl2_req "$1" GET "$2" "" "$3"; }

file_sha() { sha256_hex <"$1"; }
file_size() { wc -c <"$1" | tr -d ' '; }

# --- the checks --------------------------------------------------------------

next_link
last_link="" # the first link is used by the pairing
rl2_use_link "$link"
api=$RL2_BASE/api/v1
for ((i = 0; i < 50; i++)); do pcurl -s -o /dev/null "$api/hello" && break; sleep 0.1; done

pair A "smoke-library"

# GET /systems: the sum of gameCount, and the id of GBA.
req A GET /api/v1/systems
check "GET /systems" 200 "$status"
systems=$body
sum=0
for n in $(printf '%s' "$systems" | grep -o '"gameCount":[0-9]*' | cut -d: -f2); do sum=$((sum + n)); done
gba=$(printf '%s' "$systems" | tr '{' '\n' | grep '"tag":"GBA"' | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
[ -n "$gba" ] || die "no GBA system: $systems"

# GET /games: every game, flat.
req A GET /api/v1/games
check "GET /games" 200 "$status"
games=$body
count=$({ printf '%s' "$games" | grep -o '"systemId":' || true; } | wc -l | tr -d ' ')
check "GET /games count ($count) equals the sum of gameCount of GET /systems" "$sum" "$count"
check "GET /games truncated" false "$(json_bool truncated "$games")"
[ -n "$(json_field generatedAt "$games")" ] && pass "GET /games generatedAt present" || failed "GET /games generatedAt missing"
# game_line FILE_NAME: the object of the GBA game with that file name.
game_line() { printf '%s' "$games" | tr '{' '\n' | grep "\"systemId\":\"$gba\"" | grep "\"fileName\":\"$1\"" || true; }
for spec in "Starlight Courier Remix.gba|Hacks" "Tiny Trial.gba|Hacks/Deeper"; do
	fname=${spec%|*}
	fpath=${spec#*|}
	line=$(game_line "$fname")
	if [ -z "$line" ]; then
		failed "subfolder game $fname not in GET /games"
		continue
	fi
	check "GET /games $fname folderPath" "$fpath" "$(json_field folderPath "$line")"
	fid=$(json_field folderId "$line")
	expected_fid=$(printf '%s' "Game Boy Advance (GBA)/$fpath" | openssl base64 -A | tr '+/' '-_' | tr -d '=')
	check "GET /games $fname folderId (opaque id of its folder)" "$expected_fid" "$fid"
done
root=$(game_line "Starlight Courier.gba")
[ -n "$root" ] || die "no Starlight Courier.gba (GBA) in GET /games"
if printf '%s' "$root" | grep -q '"folderId":null' && printf '%s' "$root" | grep -q '"folderPath":null'; then
	pass "GET /games root game folderId and folderPath null"
else
	failed "GET /games root game folderId/folderPath not null: $root"
fi
game=$(json_field id "$root")
other=$(json_field id "$(game_line "Starlight Courier Remix.gba")")

# GET /games/{gameId}: a fileId on every file.
req A GET "/api/v1/games/$game"
check "GET /games/{id} Starlight Courier" 200 "$status"
detail=$body
files=$(printf '%s' "$detail" | sed 's/.*"files":\[//' | tr '{' '\n' | grep '"role":' || true)
nfiles=$(printf '%s\n' "$files" | grep -c '"role":' || true)
nids=$(printf '%s\n' "$files" | grep -c '"fileId":"[^"]' || true)
if [ "$nfiles" -gt 0 ] && [ "$nfiles" = "$nids" ]; then
	pass "GET /games/{id}: fileId on every file ($nids of $nfiles: $(printf '%s\n' "$files" | sed -n 's/.*"role":"\([^"]*\)".*/\1/p' | tr '\n' ' '))"
else
	failed "GET /games/{id}: $nids fileId for $nfiles files"
fi

# download ROLE LOCAL: downloads the file of that role, compares it with
# the file of the SD card.
download() {
	local role=$1 local_file=$2 line fid out
	line=$(printf '%s\n' "$files" | grep "\"role\":\"$role\"" | head -n 1)
	fid=$(json_field fileId "$line")
	[ -n "$fid" ] || { failed "no $role file in GET /games/{id}"; return; }
	check "$role path" "${local_file#"$sd"/}" "$(json_field path "$line")"
	out=$workdir/download.$role
	fetch A "/api/v1/games/$game/files/$fid" "$out"
	check "GET /games/{id}/files/{fileId} ($role)" 200 "$status"
	check "$role download size ($(file_size "$local_file") bytes)" "$(file_size "$local_file")" "$(file_size "$out")"
	check "$role download SHA-256 equals the SD card file" "$(file_sha "$local_file")" "$(file_sha "$out")"
}
download rom "$sd/Roms/Game Boy Advance (GBA)/Starlight Courier.gba"
download cheats "$sd/Cheats/GBA/Starlight Courier.gba.cht"

fetch A "/api/v1/games/$game/files/$(hex_to_b64u "$(openssl rand -hex 8)")" "$workdir/unknown"
check "unknown fileId -> 404" 404 "$status"
check "unknown fileId -> code not_found" not_found "$(json_field code "$(cat "$workdir/unknown")")"
req A GET "/api/v1/games/$other"
other_fid=$(json_field fileId "$(printf '%s' "$body" | sed 's/.*"files":\[//' | tr '{' '\n' | grep '"role":"rom"')")
[ -n "$other_fid" ] || die "no ROM fileId for Starlight Courier Remix: $body"
fetch A "/api/v1/games/$game/files/$other_fid" "$workdir/other"
check "fileId of another game -> 404" 404 "$status"

req A DELETE /api/v1/clients/me
check "client unpaired (DELETE /clients/me)" 204 "$status"

if [ "$failures" -gt 0 ]; then
	echo "smoke-library: $failures check(s) FAILED"
	exit 1
fi
echo "smoke-library: OK"
