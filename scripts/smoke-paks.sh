#!/usr/bin/env bash
# Smoke test of milestone M12 (pak manager and read-only SD browser)
# against the real pak (make headless), with curl, openssl and zip only
# (TLS pinned to the pairing link, RL2-ED25519 signing: scripts/lib/rl2.sh).
# One client pairs, then:
#   - GET /paks lists the emulator pak of the test card (MGBA, emu);
#   - a tiny pak (SmokeClock.pak, version 1) is zipped, uploaded with the
#     "pak" target (POST /uploads, one UNSIGNED chunk, complete -> jobId)
#     and installed by its pak-install job; GET /paks shows it;
#   - the same pak in version 2 with onConflict fail: the job fails with
#     409 conflict; with overwrite: installed, the old one in the trash
#     (replace-pak);
#   - DELETE /paks/{pakId}: moved to the trash (delete-pak), no longer
#     listed; a loose archive (launch.sh at its top) installs under the
#     name of the archive;
#   - GET /sd/browse lists the root of the card; README.md is downloaded
#     (GET /sd/browse/{sdEntryId}/content) and compared with the copy;
#     the ReminisLink data folder is not listed under .userdata/shared;
#   - the client unpairs.
# Archive protections, self protection, revocation and the SD browser
# refusals are covered by the Go tests (internal/paks, internal/library,
# internal/server/paks_test.go).
#
# Usage:
#   scripts/smoke-paks.sh
#       builds build/reminislink-host, starts it headless with
#       --pairing-open on a temporary copy of testdata/sdcard, runs the
#       checks and stops it (by PID).
#   PAK_LOG=<file> SD=<card> scripts/smoke-paks.sh
#       uses a pak already running with --pairing-open whose standard
#       output goes to <file> and whose card is <card> (README.md of
#       testdata/sdcard at its root).
#
# The pairing link (single-use secret) is read from the pak output; the
# private key stays in a private temporary folder and nothing secret is
# printed. Exits 1 if any check fails. Needs OpenSSL 3 or later and zip.
# Bash 3.2 compatible (macOS).
set -euo pipefail
cd "$(dirname "$0")/.."

if [ $# -gt 0 ]; then
	sed -n '2,38p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
fi

failures=0
pass() { echo "PASS: $*"; }
failed() { echo "FAIL: $*"; failures=$((failures + 1)); }
die() { echo "smoke-paks: $*" >&2; exit 1; }
# check DESCRIPTION EXPECTED ACTUAL
check() {
	if [ "$2" = "$3" ]; then pass "$1"; else failed "$1 (expected '$2', got '$3')"; fi
}
command -v zip >/dev/null || die "zip is required"

# --- the pak ----------------------------------------------------------------

pak_pid=""
workdir=$(mktemp -d "${TMPDIR:-/tmp}/smoke-paks.XXXXXX")
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
	sd=${SD:-testdata/sdcard}
else
	mkdir -p build
	CGO_ENABLED=0 go build -o build/reminislink-host ./cmd/reminislink
	sd=$workdir/sdcard
	mkdir -p "$sd"
	cp -R testdata/sdcard/. "$sd/"
	log=$workdir/pak.log
	build/reminislink-host --headless --sd "$sd" --pairing-open --name "smoke-paks pak" >"$log" 2>&1 &
	pak_pid=$!
	echo "pak started (PID $pak_pid) on a copy of testdata/sdcard"
fi

link=""
for ((i = 0; i < 100; i++)); do
	link=$(sed -n 's/^Pairing link: //p' "$log" | tail -n 1)
	[ -n "$link" ] && break
	if [ -n "$pak_pid" ] && ! kill -0 "$pak_pid" 2>/dev/null; then die "the pak stopped (log: $log)"; fi
	sleep 0.1
done
[ -n "$link" ] || die "no pairing link in the pak output (run the pak with --pairing-open)"

# shellcheck source=lib/rl2.sh
. scripts/lib/rl2.sh
RL2_DIR=$workdir/keys
mkdir -m 700 "$RL2_DIR"
rl2_selftest
rl2_use_link "$link"
api=$RL2_BASE/api/v1
for ((i = 0; i < 50; i++)); do pcurl -s -o /dev/null "$api/hello" && break; sleep 0.1; done
rl2_pair A "smoke-paks" apple || die "POST /pair: $RL2_ERROR"
pass "client paired (pairing v2, TLS pinned)"

# sreq METHOD PATH QUERY DATAFILE MODE OUT [HEADER]: request signed by A
# with a canonical QUERY (our ids need no encoding) and the body of
# DATAFILE ("" for none); MODE is "signed" or "unsigned" (upload chunk);
# the body of the answer goes to OUT, the status to $status.
sreq() {
	local method=$1 path=$2 query=$3 file=$4 mode=$5 out=$6 header=${7-} time nonce content signature url
	local -a opts=()
	time=$(date +%s)
	nonce=$(hex_to_b64u "$("$OPENSSL" rand -hex 16)")
	if [ "$mode" = unsigned ]; then
		content=UNSIGNED
	elif [ -n "$file" ]; then
		content=$(sha256_hex <"$file")
	else
		content=$(printf '' | sha256_hex)
	fi
	signature=$(printf 'RL2-ED25519\n%s\n%s\n%s\n%s\n%s\n%s' "$method" "$path" "$query" "$time" "$nonce" "$content" |
		ed25519_sign_b64u "$RL2_DIR/A.pem")
	url=$RL2_BASE$path
	if [ -n "$query" ]; then url="$url?$query"; fi
	if [ -n "$file" ]; then opts=(-H 'Content-Type: application/octet-stream' --data-binary "@$file"); fi
	if [ -n "$header" ]; then opts=(${opts[@]+"${opts[@]}"} -H "$header"); fi
	status=$(pcurl -sS -X "$method" -o "$out" -w '%{http_code}' ${opts[@]+"${opts[@]}"} \
		-H "X-RL-Client: $A_id" -H "X-RL-Time: $time" -H "X-RL-Nonce: $nonce" \
		-H "X-RL-Content-SHA256: $content" -H "X-RL-Signature: $signature" "$url")
}

# make_pak VERSION ZIP: zips SmokeClock.pak (launch.sh, pak.json).
make_pak() {
	local dir=$workdir/pak.$1
	rm -rf "$dir"
	mkdir -p "$dir/SmokeClock.pak"
	printf '#!/bin/sh\necho "SmokeClock %s"\n' "$1" >"$dir/SmokeClock.pak/launch.sh"
	printf '{"name":"SmokeClock","version":"%s","type":"TOOL"}\n' "$1" >"$dir/SmokeClock.pak/pak.json"
	rm -f "$2"
	(cd "$dir" && zip -qr "$2" SmokeClock.pak)
}

# upload_pak ZIP ONCONFLICT [FILENAME]: uploads ZIP with the pak target
# and completes it; sets $job.
upload_pak() {
	local size sum upload fname=${3:-SmokeClock.pak.zip}
	size=$(wc -c <"$1" | tr -d ' ')
	sum=$(sha256_hex <"$1")
	rl2_req A POST /api/v1/uploads "{\"target\":{\"kind\":\"pak\",\"pakKind\":\"auto\",\"fileName\":\"$fname\"},\"sizeBytes\":$size,\"sha256\":\"$sum\",\"onConflict\":\"$2\"}"
	[ "$status" = 201 ] || die "POST /uploads (pak): $status $body"
	upload=$(json_field uploadId "$body")
	sreq PUT "/api/v1/uploads/$upload" "" "$1" unsigned "$workdir/chunk" "Upload-Offset: 0"
	[ "$status" = 204 ] || die "PUT chunk: $status $(cat "$workdir/chunk")"
	rl2_req A POST "/api/v1/uploads/$upload/complete"
	[ "$status" = 200 ] || die "complete: $status $body"
	job=$(json_field jobId "$body")
	[ -n "$job" ] || die "complete of a pak upload without jobId: $body"
}

# wait_job ID: polls GET /jobs/{id} until it ends; sets $state and $body.
wait_job() {
	local i
	for ((i = 0; i < 100; i++)); do
		rl2_req A GET "/api/v1/jobs/$1"
		state=$(json_field state "$body")
		case $state in succeeded | failed | cancelled) return ;; esac
		sleep 0.1
	done
	die "job $1 stuck in $state"
}

# pak_field NAME FIELD: a string field of the pak NAME in the last GET /paks.
pak_field() { printf '%s' "$paks" | tr '{' '\n' | grep "\"name\":\"$1\"" | sed -n "s/.*\"$2\":\"\([^\"]*\)\".*/\1/p"; }
list_paks() {
	rl2_req A GET /api/v1/paks
	[ "$status" = 200 ] || die "GET /paks: $status $body"
	paks=$body
}

# --- paks ---------------------------------------------------------------------

list_paks
check "GET /paks lists the emulator pak MGBA" emu "$(pak_field MGBA kind)"

make_pak 1 "$workdir/v1.zip"
upload_pak "$workdir/v1.zip" fail
wait_job "$job"
check "upload of a pak (fail): job succeeded" succeeded "$state"
check "... kind pak-install" pak-install "$(json_field kind "$body")"
pak_id=$(json_field pakId "$body")
list_paks
check "GET /paks shows SmokeClock" "$pak_id" "$(pak_field SmokeClock pakId)"
check "... version 1 from pak.json" 1 "$(pak_field SmokeClock version)"
check "... in Tools/<platform>" Tools/tg5040/SmokeClock.pak "$(pak_field SmokeClock path)"

make_pak 2 "$workdir/v2.zip"
upload_pak "$workdir/v2.zip" fail
wait_job "$job"
check "same pak with onConflict fail: job failed" failed "$state"
check "... 409 conflict" conflict "$(json_field code "$body")"
upload_pak "$workdir/v2.zip" overwrite
wait_job "$job"
check "same pak with overwrite: job succeeded" succeeded "$state"
list_paks
check "GET /paks shows version 2" 2 "$(pak_field SmokeClock version)"
rl2_req A GET /api/v1/trash
case $body in *'"reason":"replace-pak"'*) pass "the old pak is in the trash (replace-pak)" ;; *) failed "no replace-pak trash entry: $body" ;; esac

pak_id=$(pak_field SmokeClock pakId)
rl2_req A DELETE "/api/v1/paks/$pak_id"
check "DELETE /paks/{pakId}" 200 "$status"
list_paks
check "SmokeClock no longer listed" "" "$(pak_field SmokeClock pakId)"
rl2_req A GET /api/v1/trash
case $body in *'"reason":"delete-pak"'*) pass "the deleted pak is in the trash (delete-pak)" ;; *) failed "no delete-pak trash entry: $body" ;; esac

# A loose archive (launch.sh at its top): named after the archive.
mkdir -p "$workdir/loose"
printf '#!/bin/sh\necho loose\n' >"$workdir/loose/launch.sh"
(cd "$workdir/loose" && zip -q "$workdir/loose.zip" launch.sh)
upload_pak "$workdir/loose.zip" fail "Smoke Loose.zip"
wait_job "$job"
check "loose archive: job succeeded" succeeded "$state"
list_paks
check "... installed as Tools/<platform>/Smoke Loose.pak" "Tools/tg5040/Smoke Loose.pak" "$(pak_field "Smoke Loose" path)"

# --- SD browser ---------------------------------------------------------------

rl2_req A GET /api/v1/sd/browse
check "GET /sd/browse (root)" 200 "$status"
check "... root folderId null" null "$(printf '%s' "$body" | sed -En 's/.*"folderId":(null).*/\1/p' | head -n 1)"
readme=$(printf '%s' "$body" | tr '{' '\n' | grep '"name":"README.md"' | sed -n 's/.*"sdEntryId":"\([^"]*\)".*/\1/p')
[ -n "$readme" ] || die "README.md not listed at the root: $body"
sreq GET "/api/v1/sd/browse/$readme/content" "" "" signed "$workdir/README.md"
check "GET /sd/browse/{sdEntryId}/content" 200 "$status"
if cmp -s "$workdir/README.md" "$sd/README.md"; then pass "... same bytes as the card"; else failed "downloaded README.md differs"; fi
userdata=$(printf '%s' "$body" | tr '{' '\n' | grep '"name":".userdata"' | sed -n 's/.*"sdEntryId":"\([^"]*\)".*/\1/p')
[ -n "$userdata" ] || die ".userdata not listed at the root"
sreq GET /api/v1/sd/browse "folderId=$userdata" "" signed "$workdir/userdata.json"
shared=$(tr '{' '\n' <"$workdir/userdata.json" | grep '"name":"shared"' | sed -n 's/.*"sdEntryId":"\([^"]*\)".*/\1/p')
[ -n "$shared" ] || die "shared not listed in .userdata: $(cat "$workdir/userdata.json")"
sreq GET /api/v1/sd/browse "folderId=$shared" "" signed "$workdir/shared.json"
check "GET /sd/browse?folderId=<.userdata/shared>" 200 "$status"
if grep -q '"name":"ReminisLink"' "$workdir/shared.json"; then
	failed "the ReminisLink data folder is listed"
else
	pass "the ReminisLink data folder is hidden"
fi

# --- unpair ---------------------------------------------------------------------

rl2_req A DELETE /api/v1/clients/me
check "client unpaired (DELETE /clients/me)" 204 "$status"

if [ "$failures" -gt 0 ]; then
	echo "smoke-paks: $failures check(s) FAILED"
	exit 1
fi
echo "smoke-paks: OK"
