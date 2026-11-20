#!/usr/bin/env bash
# Smoke test of milestone M13 (TLS pinning, pairing v2, RL2-ED25519, writes
# one at a time) against the real pak, with TWO clients and curl and
# openssl only (scripts/lib/rl2.sh). Clients A and B pair, then:
#   - TLS: the certificate the pak presents (openssl s_client) has the pin
#     of the pairing link; curl with another pin is refused; plain HTTP
#     gets no API answer;
#   - GET /hello: apiVersion 2, no writerHeld (the M10 lease is gone);
#   - A and B both write (a manual save backup of Starlight Courier, GBA):
#     no 423, no lease; GET/PUT /writer answer 404; GET /device has no
#     "writer";
#   - six writes sent at the same time by A and B all succeed (the pak runs
#     them one at a time) and six backups are added;
#   - a request with a bad signature gets 401 bad_signature, one replayed
#     gets 401 replayed_request;
#   - both clients unpair (DELETE /clients/me).
# "Device busy" (a write waiting more than 10 s) and the job install wait
# are covered by the Go server tests (internal/server/writes_test.go).
#
# Usage:
#   scripts/smoke-writes.sh
#       builds build/reminislink-host, starts it headless with
#       --pairing-open on a temporary copy of testdata/sdcard, runs the
#       checks and stops it (by PID).
#   PAK_LOG=<file> scripts/smoke-writes.sh
#       uses a pak already running with --pairing-open whose standard
#       output goes to <file>; its card must have the games of
#       testdata/sdcard.
#
# The pairing links (single-use secrets) are read from the pak output.
# Secrets only go through pipes and bash variables, the private keys stay
# in a private temporary folder, and nothing secret is printed. Exits 1 if
# any check fails. Needs OpenSSL 3 or later.
set -euo pipefail
cd "$(dirname "$0")/.."

if [ $# -gt 0 ]; then
	sed -n '2,33p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
fi

failures=0
pass() { echo "PASS: $*"; }
failed() { echo "FAIL: $*"; failures=$((failures + 1)); }
die() { echo "smoke-writes: $*" >&2; exit 1; }
# check DESCRIPTION EXPECTED ACTUAL
check() {
	if [ "$2" = "$3" ]; then pass "$1"; else failed "$1 (expected '$2', got '$3')"; fi
}

# --- the pak ----------------------------------------------------------------

pak_pid=""
workdir=$(mktemp -d "${TMPDIR:-/tmp}/smoke-writes.XXXXXX")
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
else
	mkdir -p build
	CGO_ENABLED=0 go build -o build/reminislink-host ./cmd/reminislink
	sd=$workdir/sdcard
	mkdir -p "$sd"
	cp -R testdata/sdcard/. "$sd/"
	log=$workdir/pak.log
	build/reminislink-host --headless --sd "$sd" --pairing-open --name "smoke-writes pak" >"$log" 2>&1 &
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

# shellcheck source=lib/rl2.sh
. scripts/lib/rl2.sh
RL2_DIR=$workdir/keys
mkdir -m 700 "$RL2_DIR"
rl2_selftest

# pair X NAME: pairs client X with the next pairing link.
pair() {
	next_link
	rl2_use_link "$link"
	rl2_pair "$1" "$2" apple || die "POST /pair ($1): $RL2_ERROR"
	pass "client $1 \"$2\" paired (pairing v2), device proof verified"
}

# --- TLS ----------------------------------------------------------------------

next_link
last_link="" # the first link is used by the first pairing
rl2_use_link "$link"
api=$RL2_BASE/api/v1
hostport=${RL2_BASE#https://}
for ((i = 0; i < 50; i++)); do pcurl -s -o /dev/null "$api/hello" && break; sleep 0.1; done
check "TLS: the certificate of the pak has the pin of the pairing link" "$RL2_PIN" "$(rl2_pin_of_server "$hostport")"
wrong=$(b64u_to_std "$(hex_to_b64u "$("$OPENSSL" rand -hex 32)")")
if curl -sS -k --pinnedpubkey "sha256//$wrong" -o /dev/null "$api/hello" 2>/dev/null; then
	failed "TLS: curl with another pin was accepted"
else
	pass "TLS: curl with another pin is refused"
fi
if curl -sS --max-time 5 "http://$hostport/api/v1/hello" 2>/dev/null | grep -q '"deviceId"'; then
	failed "plain HTTP answered"
else
	pass "plain HTTP gets no API answer"
fi
hello=$(pcurl -sS "$api/hello")
check "GET /hello apiVersion" 2 "$(json_num apiVersion "$hello")"
case $hello in *writerHeld*) failed "GET /hello still has writerHeld" ;; *) pass "GET /hello has no writerHeld" ;; esac

pair A "smoke-writes A"
pair B "smoke-writes B"

# --- writes -------------------------------------------------------------------

rl2_req A GET /api/v1/systems
gba=$(printf '%s' "$body" | tr '{' '\n' | grep '"tag":"GBA"' | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
rl2_req A GET "/api/v1/systems/$gba/games"
game=$(printf '%s' "$body" | tr '{' '\n' | grep '"fileName":"Starlight Courier.gba"' | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
[ -n "$game" ] || die "no Starlight Courier.gba in GBA: $body"

# backups X: number of backups of the test game, read by client X.
backups() {
	rl2_req "$1" GET "/api/v1/games/$game/save/backups"
	[ "$status" = 200 ] || die "GET backups: $status $body"
	{ printf '%s' "$body" | grep -o '"id":' || true; } | wc -l | tr -d ' '
}

n0=$(backups A)
rl2_req A POST "/api/v1/games/$game/save/backups" '{"label":"smoke-writes A"}'
check "A writes (backup)" 201 "$status"
rl2_req B POST "/api/v1/games/$game/save/backups" '{"label":"smoke-writes B"}'
check "B writes too, no lease (backup)" 201 "$status"
for m in GET PUT DELETE; do
	rl2_req A "$m" /api/v1/writer
	check "$m /writer is gone" 404 "$status"
done
rl2_req B GET /api/v1/device
check "GET /device" 200 "$status"
case $body in *'"writer"'*) failed "GET /device still has writer" ;; *) pass "GET /device has no writer" ;; esac

# Six writes at the same time, three from each client (wait for them
# only: the pak is a child too).
burst=()
for i in 1 2 3; do
	for who in A B; do
		(rl2_req "$who" POST "/api/v1/games/$game/save/backups" "{\"label\":\"burst $who $i\"}" && echo "$status" >"$workdir/burst.$who.$i") &
		burst+=($!)
	done
done
wait "${burst[@]}"
ok=0
for f in "$workdir"/burst.*; do [ "$(cat "$f")" = 201 ] && ok=$((ok + 1)); done
check "six concurrent writes of A and B succeed (serialized by the pak)" 6 "$ok"
check "backups added (2 + 6)" $((n0 + 8)) "$(backups B)"

# --- signatures -----------------------------------------------------------------

# A request of A signed with B's key.
cp "$RL2_DIR/A.pem" "$RL2_DIR/A.pem.bak"
cp "$RL2_DIR/B.pem" "$RL2_DIR/A.pem"
rl2_req A GET /api/v1/systems
check "a request signed with another client's key -> 401" 401 "$status"
check "... code bad_signature" bad_signature "$(json_field code "$body")"
mv "$RL2_DIR/A.pem.bak" "$RL2_DIR/A.pem"
# A replayed request (same headers twice).
time=$(date +%s)
nonce=$(hex_to_b64u "$("$OPENSSL" rand -hex 16)")
content=$(printf '' | sha256_hex)
signature=$(printf 'RL2-ED25519\nGET\n/api/v1/systems\n\n%s\n%s\n%s' "$time" "$nonce" "$content" | ed25519_sign_b64u "$RL2_DIR/A.pem")
replay() {
	pcurl -sS -o "$workdir/replay" -w '%{http_code}' -H "X-RL-Client: $A_id" -H "X-RL-Time: $time" -H "X-RL-Nonce: $nonce" \
		-H "X-RL-Content-SHA256: $content" -H "X-RL-Signature: $signature" "$api/systems"
}
check "signed request (first time)" 200 "$(replay)"
check "the same request replayed -> 401" 401 "$(replay)"
check "... code replayed_request" replayed_request "$(json_field code "$(cat "$workdir/replay")")"

# --- unpair ---------------------------------------------------------------------

for who in A B; do
	rl2_req "$who" DELETE /api/v1/clients/me
	check "client $who unpaired (DELETE /clients/me)" 204 "$status"
done

if [ "$failures" -gt 0 ]; then
	echo "smoke-writes: $failures check(s) FAILED"
	exit 1
fi
echo "smoke-writes: OK"
