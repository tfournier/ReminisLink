#!/usr/bin/env bash
# Smoke test of milestone M10 (single writer lease) against the real pak,
# with TWO clients and curl and openssl only (same signing code as
# scripts/smoke.sh). Clients A and B pair, then:
#   - A's first write (a manual save backup of Starlight Courier, GBA)
#     takes the free lease: GET /hello writerHeld, GET /writer for both;
#   - B's write gets 423 read_only naming A and changes nothing;
#   - B's PUT /writer gets 423 without force, 200 with {"force":true};
#     A's next write then gets 423 naming B; GET /device for both;
#   - B's DELETE /writer frees the lease, A's PUT /writer takes it;
#   - expiry: A stays silent for more than 60 s, then B's write succeeds
#     (skipped with SKIP_EXPIRY=1);
#   - both clients unpair (DELETE /clients/me).
# Disconnect and Forget are console actions without an API: they are
# covered by the Go server tests (internal/server/writer_test.go,
# access_test.go), not by this script.
#
# Usage:
#   scripts/smoke-writer.sh
#       builds build/reminislink-host, starts it headless with
#       --pairing-open on a temporary copy of testdata/sdcard, runs the
#       checks and stops it (by PID).
#   PAK_LOG=<file> scripts/smoke-writer.sh
#       uses a pak already running with --pairing-open whose standard
#       output goes to <file> (e.g. "build/reminislink-host --headless
#       --sd "$sd" --pairing-open > pak.log"); its card must have the
#       games of testdata/sdcard.
#   HOLD=1 PAK_LOG=<file> scripts/smoke-writer.sh
#       hold mode, for a check with the app: client B ("smoke-writer B",
#       or $WRITER_NAME) pairs, holds the lease (renewed every 5 s) until
#       another client takes over (at most $HOLD_SECONDS, default 300),
#       then checks that its writes get 423 naming that client. Pair the
#       app with the next link the pak prints (WriteAccessUITests/
#       testReadOnlyThenTakeOverAgainstHeadlessPak, ios/README.md).
#
# The pairing links (single-use secrets) are read from the pak output: the
# pak prints a new one after each pairing. Secrets and keys only go
# through pipes and bash variables, never through process arguments, and
# are never printed. Exits 1 if any check fails.
set -euo pipefail
cd "$(dirname "$0")/.."

if [ $# -gt 0 ]; then
	sed -n '2,39p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
fi

failures=0
pass() { echo "PASS: $*"; }
failed() { echo "FAIL: $*"; failures=$((failures + 1)); }
die() { echo "smoke-writer: $*" >&2; exit 1; }
# check DESCRIPTION EXPECTED ACTUAL
check() {
	if [ "$2" = "$3" ]; then pass "$1"; else failed "$1 (expected '$2', got '$3')"; fi
}

# --- the pak ----------------------------------------------------------------

pak_pid=""
workdir=$(mktemp -d "${TMPDIR:-/tmp}/smoke-writer.XXXXXX")
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
	build/reminislink-host --headless --sd "$sd" --pairing-open --name "smoke-writer pak" >"$log" 2>&1 &
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

# --- encodings (as in scripts/smoke.sh) -------------------------------------

hex_to_bin() {
	local hex=$1 out="" i
	for ((i = 0; i < ${#hex}; i += 2)); do out+="\\x${hex:i:2}"; done
	printf "$out"
}
bin_to_hex() { od -An -v -tx1 | tr -d ' \n'; }
hex_to_b64u() { hex_to_bin "$1" | openssl base64 -A | tr '+/' '-_' | tr -d '='; }
b64u_to_hex() {
	local s
	s=$(printf '%s' "$1" | tr -- '-_' '+/')
	while [ $((${#s} % 4)) -ne 0 ]; do s="$s="; done
	printf '%s' "$s" | openssl base64 -d -A | bin_to_hex
}
sha256_hex() { openssl dgst -sha256 -binary | bin_to_hex; }
# hmac_hex KEY_HEX < message: HMAC-SHA256 (RFC 2104) in hex, the key never
# in the arguments of a process.
hmac_hex() {
	local key=$1 ipad="" opad="" i b x
	if [ ${#key} -gt 128 ]; then key=$(hex_to_bin "$key" | sha256_hex); fi
	while [ ${#key} -lt 128 ]; do key="${key}00"; done
	for ((i = 0; i < 128; i += 2)); do
		b=$((16#${key:i:2}))
		printf -v x '%02x' $((b ^ 0x36)); ipad+=$x
		printf -v x '%02x' $((b ^ 0x5c)); opad+=$x
	done
	local inner
	inner=$({ hex_to_bin "$ipad"; cat; } | sha256_hex)
	{ hex_to_bin "$opad"; hex_to_bin "$inner"; } | sha256_hex
}
[ "$(printf 'what do ya want for nothing?' | hmac_hex 4a656665)" = \
	5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843 ] || die "HMAC self-test failed"
json_field() { printf '%s' "$2" | sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p"; }
json_bool() { printf '%s' "$2" | sed -En "s/.*\"$1\":(true|false).*/\1/p"; }
# writer_of JSON: the "writer" object (or "null") of /writer or /device.
writer_of() { printf '%s' "$1" | sed -En 's/.*"writer":(null|\{[^}]*\}).*/\1/p'; }
uuid() {
	if [ -r /proc/sys/kernel/random/uuid ]; then cat /proc/sys/kernel/random/uuid; else uuidgen; fi | tr 'A-F' 'a-f'
}

# --- pairing ---------------------------------------------------------------

param() { printf '%s' "$link" | sed -n "s/.*[?&]$1=\([^&]*\).*/\1/p"; }

# pair X NAME: pairs client X (A or B) with the next pairing link; sets
# X_id, X_key and X_name.
pair() {
	local who=$1 name=$2 device_id secret_b64u host port secret_hex client_id cn_hex cn proof body response dn dp prk key expected
	next_link
	device_id=$(param id)
	secret_b64u=$(param s)
	host=$(param h)
	port=$(param p)
	[ -n "$device_id" ] && [ -n "$secret_b64u" ] && [ -n "$host" ] && [ -n "$port" ] || die "not a pairing link (masked)"
	base=http://$host:$port
	api=$base/api/v1
	secret_hex=$(b64u_to_hex "$secret_b64u")
	client_id=$(uuid)
	cn_hex=$(openssl rand -hex 32)
	cn=$(hex_to_b64u "$cn_hex")
	proof=$(printf '%s' "RL1-PAIR-CLIENT|$device_id|$client_id|$cn" | hmac_hex "$secret_hex")
	body=$(printf '{"method":"qr","clientId":"%s","clientName":"%s","clientNonce":"%s","proof":"%s"}' \
		"$client_id" "$name" "$cn" "$(hex_to_b64u "$proof")")
	response=$(curl -sS --fail-with-body -X POST -H 'Content-Type: application/json' --data-binary @- "$api/pair" <<<"$body") ||
		die "POST /pair ($who): $response"
	dn=$(json_field deviceNonce "$response")
	dp=$(json_field proof "$response")
	prk=$(hex_to_bin "$secret_hex" | hmac_hex "$cn_hex$(b64u_to_hex "$dn")")
	key=$({ printf '%s' "RL1-KEY|$device_id|$client_id"; printf '\001'; } | hmac_hex "$prk")
	expected=$(printf '%s' "RL1-PAIR-DEVICE|$device_id|$client_id" | hmac_hex "$key")
	if [ "$(hex_to_b64u "$expected")" = "$dp" ]; then
		pass "client $who \"$name\" paired, device proof verified"
	else
		die "device proof of the pairing of $who does not match"
	fi
	printf -v "${who}_id" '%s' "$client_id"
	printf -v "${who}_key" '%s' "$key"
	printf -v "${who}_name" '%s' "$name"
}

# --- signed requests -------------------------------------------------------

# req X METHOD PATH [JSON BODY]: signed request of client X; sets $status
# and $body.
req() {
	local who=$1 method=$2 path=$3 data=${4-} id key time nonce content canonical signature response
	local -a opts=()
	id=${who}_id key=${who}_key
	id=${!id} key=${!key}
	time=$(date +%s)
	nonce=$(hex_to_b64u "$(openssl rand -hex 16)")
	content=$(printf '%s' "$data" | sha256_hex)
	canonical=$(printf 'RL1-HMAC-SHA256\n%s\n%s\n\n%s\n%s\n%s' "$method" "$path" "$time" "$nonce" "$content")
	signature=$(printf '%s' "$canonical" | hmac_hex "$key")
	if [ -n "$data" ]; then opts=(-H 'Content-Type: application/json' --data-binary @-); fi
	response=$(printf '%s' "$data" | curl -sS -X "$method" -w '\n%{http_code}' "${opts[@]}" \
		-H "X-RL-Client: $id" -H "X-RL-Time: $time" -H "X-RL-Nonce: $nonce" \
		-H "X-RL-Content-SHA256: $content" -H "X-RL-Signature: $(hex_to_b64u "$signature")" \
		"$base$path")
	status=${response##*$'\n'}
	body=${response%$'\n'*}
	[ "$body" != "$status" ] || body=""
}

hello_writer_held() { json_bool writerHeld "$(curl -sS "$api/hello")"; }

# backups X: number of backups of the test game, read by client X.
backups() {
	req "$1" GET "/api/v1/games/$game/save/backups"
	[ "$status" = 200 ] || die "GET backups: $status $body"
	{ printf '%s' "$body" | grep -o '"id":' || true; } | wc -l | tr -d ' '
}

# write X LABEL: creates a manual backup (the harmless write).
write() { req "$1" POST "/api/v1/games/$game/save/backups" "{\"label\":\"$2\"}"; }

# expect_read_only DESCRIPTION HOLDER: the last answer is 423 read_only
# naming HOLDER.
expect_read_only() {
	if [ "$status" = 423 ] && [ "$(json_field code "$body")" = read_only ] &&
		[ "$(json_field writer "$body")" = "$2" ]; then
		pass "$1 -> 423 read_only, details.writer \"$2\""
	else
		failed "$1 (expected 423 read_only naming \"$2\", got $status $body)"
	fi
}

# expect_writer DESCRIPTION NAME YOU: the "writer" of the last answer.
expect_writer() {
	local w
	w=$(writer_of "$body")
	if [ "$status" = 200 ] && [ "$(json_field clientName "$w")" = "$2" ] && [ "$(json_bool you "$w")" = "$3" ] &&
		[ -n "$(json_field expiresAt "$w")" ]; then
		pass "$1 -> writer \"$2\", you:$3"
	else
		failed "$1 (expected 200 writer \"$2\" you:$3, got $status $body)"
	fi
}

expect_no_writer() {
	if [ "$status" = 200 ] && [ "$(writer_of "$body")" = null ]; then
		pass "$1 -> writer null"
	else
		failed "$1 (expected 200 writer null, got $status $body)"
	fi
}

# --- the checks --------------------------------------------------------------

next_link
last_link="" # the first link is used by the first pairing
api=$(printf '%s' "$link" | sed -n 's/.*[?&]h=\([^&]*\).*[?&]p=\([^&]*\).*/http:\/\/\1:\2\/api\/v1/p')
[ -n "$api" ] || die "no host/port in the pairing link"
for ((i = 0; i < 50; i++)); do curl -s -o /dev/null "$api/hello" && break; sleep 0.1; done

check "GET /hello writerHeld at start" false "$(hello_writer_held)"

# find_game X: the id of Starlight Courier (GBA) in $game, read by X.
find_game() {
	local gba
	req "$1" GET /api/v1/systems
	gba=$(printf '%s' "$body" | tr '{' '\n' | grep '"tag":"GBA"' | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
	[ -n "$gba" ] || die "no GBA system: $status $body"
	req "$1" GET "/api/v1/systems/$gba/games"
	game=$(printf '%s' "$body" | tr '{' '\n' | grep '"fileName":"Starlight Courier.gba"' | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
	[ -n "$game" ] || die "no Starlight Courier.gba in GBA: $status $body"
}

if [ "${HOLD:-0}" = 1 ]; then
	# Hold mode: one client B holds the lease (renewed every 5 s) until
	# another client takes over, then checks that its writes are refused.
	pair B "${WRITER_NAME:-smoke-writer B}"
	find_game B
	req B PUT /api/v1/writer
	expect_writer "B PUT /writer" "$B_name" true
	echo "holding: B holds the lease for ${HOLD_SECONDS:-300} s at most; pair another client with the next link and take over"
	taker=""
	for ((t = 0; t < ${HOLD_SECONDS:-300}; t += 5)); do
		sleep 5
		req B PUT /api/v1/writer
		if [ "$status" = 423 ] && [ "$(json_field code "$body")" = read_only ]; then
			taker=$(json_field writer "$body")
			break
		fi
		[ "$status" = 200 ] || failed "B renewal (PUT /writer): $status $body"
	done
	if [ -n "$taker" ]; then
		pass "lease taken over by \"$taker\" (B's renewal PUT /writer -> 423 read_only)"
		req B GET /api/v1/writer
		expect_writer "GET /writer by B after the take-over" "$taker" false
		n=$(backups B)
		write B "smoke-writer B after take-over"
		expect_read_only "B write after the take-over" "$taker"
		check "B's refused write changed nothing (backup count)" "$n" "$(backups B)"
	else
		failed "nobody took over within ${HOLD_SECONDS:-300} s"
	fi
	req B DELETE /api/v1/clients/me
	check "B unpaired (DELETE /clients/me)" 204 "$status"
	if [ "$failures" -gt 0 ]; then
		echo "smoke-writer: $failures check(s) FAILED"
		exit 1
	fi
	echo "smoke-writer: OK"
	exit 0
fi

pair A "smoke-writer A"
pair B "smoke-writer B"

find_game A
check "reads (GET) by A do not take the lease" false "$(hello_writer_held)"

n0=$(backups A)
write A "smoke-writer A 1"
check "A write (POST save backup) with the lease free" 201 "$status"
check "GET /hello writerHeld after A's write" true "$(hello_writer_held)"
check "A's backup is on the card" $((n0 + 1)) "$(backups B)"
req A GET /api/v1/writer
expect_writer "GET /writer by A" "$A_name" true
req B GET /api/v1/writer
expect_writer "GET /writer by B" "$A_name" false

n1=$(backups B)
write B "smoke-writer B refused"
expect_read_only "B write while A holds the lease" "$A_name"
check "B's refused write changed nothing (backup count)" "$n1" "$(backups B)"

req B PUT /api/v1/writer
expect_read_only "B PUT /writer without force" "$A_name"
req B PUT /api/v1/writer '{"force":false}'
expect_read_only "B PUT /writer {\"force\":false}" "$A_name"
req B PUT /api/v1/writer '{"force":true}'
expect_writer "B PUT /writer {\"force\":true}" "$B_name" true
write A "smoke-writer A refused"
expect_read_only "A write after B took over" "$B_name"
check "A's refused write changed nothing (backup count)" "$n1" "$(backups A)"

req A GET /api/v1/device
expect_writer "GET /device by A (writer field)" "$B_name" false
req B GET /api/v1/device
expect_writer "GET /device by B (writer field)" "$B_name" true

req A DELETE /api/v1/writer
check "A DELETE /writer while B holds it (no effect)" 204 "$status"
req A GET /api/v1/writer
expect_writer "lease still held by B after A's DELETE" "$B_name" false
req B DELETE /api/v1/writer
check "B DELETE /writer" 204 "$status"
req B GET /api/v1/writer
expect_no_writer "GET /writer after B released"
check "GET /hello writerHeld after release" false "$(hello_writer_held)"
req A GET /api/v1/device
expect_no_writer "GET /device by A after release"
req A PUT /api/v1/writer
expect_writer "A PUT /writer (empty body) on a free lease" "$A_name" true

if [ "${SKIP_EXPIRY:-0}" = 1 ]; then
	echo "SKIP: lease expiry (SKIP_EXPIRY=1)"
else
	echo "expiry: A holds the lease and sends nothing for 65 s (B reads only)"
	sleep 30
	req B GET /api/v1/writer
	expect_writer "after 30 s of silence, GET /writer by B (does not renew A)" "$A_name" false
	write B "smoke-writer B early"
	expect_read_only "B write after 30 s, A's lease still valid" "$A_name"
	sleep 35
	req B GET /api/v1/writer
	expect_no_writer "after 65 s of silence, A's lease expired"
	check "GET /hello writerHeld after expiry" false "$(hello_writer_held)"
	n2=$(backups B)
	write B "smoke-writer B after expiry"
	check "B write after A's lease expired" 201 "$status"
	check "B's backup is on the card" $((n2 + 1)) "$(backups B)"
	req B GET /api/v1/writer
	expect_writer "GET /writer by B after its write" "$B_name" true
	write A "smoke-writer A refused again"
	expect_read_only "A write after expiry and B's write" "$B_name"
fi

req A DELETE /api/v1/clients/me
check "A unpaired (DELETE /clients/me)" 204 "$status"
req B DELETE /api/v1/clients/me
check "B unpaired (DELETE /clients/me)" 204 "$status"
req A GET /api/v1/writer
check "A's requests after unpairing" 401 "$status"
echo "info: GET /hello writerHeld after both unpaired: $(hello_writer_held)"

if [ "$failures" -gt 0 ]; then
	echo "smoke-writer: $failures check(s) FAILED"
	exit 1
fi
echo "smoke-writer: OK"
