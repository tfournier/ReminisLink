#!/usr/bin/env bash
# Smoke test of milestone M14 (event stream GET /events) against the real
# pak, with TWO clients and curl and openssl only (the RL2 client of
# scripts/lib/rl2.sh, M13: pairing v2, TLS pinned to the certificate pin of
# the pairing link, requests signed with RL2-ED25519). Clients A and B
# pair, then:
#   - A opens GET /events (signed, curl -N): 200 text/event-stream, the
#     first event is `hello` (keepAliveSeconds 15), then a `: keep-alive`
#     comment within 16 s;
#   - B creates a save backup of Starlight Courier (GBA): A receives a
#     `library` event naming the GBA systemId;
#   - B downloads a ROM served on 127.0.0.1 (POST /downloads, kind rom,
#     needs python3): A receives `job` events of that job up to
#     `succeeded`, then a `library` event naming the GBA systemId;
#   - A opens 3 more streams (4 in all): the 5th gets 429
#     too_many_attempts with Retry-After; once one of them is closed, a new
#     one is accepted;
#   - the pak gets SIGTERM (the console's Quit needs the UI, not
#     reachable headless): A receives `shutdown` {"reason":"stopped"} as
#     its last event, then the stream ends (EOF) within 2 s.
# There is no write lease since M13: both clients write whenever they
# want, and the pak runs the writes one at a time (a write that waits more
# than 10 s gets 429 device_busy, covered by internal/server/writes_test.go).
#
# Usage:
#   scripts/smoke-events.sh
#       builds build/reminislink-host, starts it headless with
#       --pairing-open on a temporary copy of testdata/sdcard, runs the
#       checks and stops it (SIGTERM, by PID).
#   UI_DELETE=1 PAK_LOG=<file> scripts/smoke-events.sh
#       helper of EventsUITests/testLibraryRefreshFromAnotherClient-
#       AgainstHeadlessPak (ios/README.md): with a pak already running
#       with --pairing-open whose standard output goes to <file>, pairs a
#       client B with the LATEST link the pak printed (run it once the app
#       has paired), waits for $READY_FILE (at most $READY_TIMEOUT s,
#       default 60; without READY_FILE, $DELAY s, default 15), then
#       deletes "Lantern Keeper [b1]" (GBA, to the trash) while the app
#       stays connected, and writes the time of the deletion (epoch
#       seconds) to $TRIGGER_FILE.
#
# The pairing links (single-use secrets) are read from the pak output.
# Secrets only go through pipes and bash variables, the private keys stay
# in a private temporary folder, and nothing secret is printed. Exits 1 if
# any check fails. Needs OpenSSL 3 or later.
# The pairing links, secrets and keys must never reach a shell trace:
# tracing (bash -x, set -x, SHELLOPTS) is turned off for the whole run.
{ set +x; } 2>/dev/null
set -euo pipefail
cd "$(dirname "$0")/.."

if [ $# -gt 0 ]; then
	sed -n '2,44p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
fi

failures=0
pass() { echo "PASS: $*"; }
failed() { echo "FAIL: $*"; failures=$((failures + 1)); }
die() { echo "smoke-events: $*" >&2; exit 1; }
# check DESCRIPTION EXPECTED ACTUAL
check() {
	if [ "$2" = "$3" ]; then pass "$1"; else failed "$1 (expected '$2', got '$3')"; fi
}
now_ms() { python3 -c 'import time; print("%.3f" % time.time())' 2>/dev/null || date +%s; }

# --- the pak ----------------------------------------------------------------

pak_pid=""
http_pid=""
stream_pids=()
workdir=$(mktemp -d "${TMPDIR:-/tmp}/smoke-events.XXXXXX")
cleanup() {
	local p
	for p in "${stream_pids[@]+"${stream_pids[@]}"}" $http_pid; do kill "$p" 2>/dev/null || true; done
	if [ -n "$pak_pid" ]; then
		kill -TERM "$pak_pid" 2>/dev/null || true
		wait "$pak_pid" 2>/dev/null || true
	fi
	rm -rf "$workdir"
}
trap cleanup EXIT
# Without these, SIGINT or SIGTERM would skip the EXIT trap (bash 3.2) and
# leave the temporary folder with the private keys.
trap 'exit 130' INT
trap 'exit 143' TERM

if [ -n "${PAK_LOG:-}" ]; then
	log=$PAK_LOG
	[ -r "$log" ] || die "cannot read PAK_LOG $log"
	[ "${UI_DELETE:-0}" = 1 ] || die "PAK_LOG is only for UI_DELETE=1 (the full check stops its own pak)"
else
	[ "${UI_DELETE:-0}" != 1 ] || die "UI_DELETE=1 needs PAK_LOG"
	mkdir -p build
	CGO_ENABLED=0 go build -o build/reminislink-host ./cmd/reminislink
	sd=$workdir/sdcard
	mkdir -p "$sd"
	cp -R testdata/sdcard/. "$sd/"
	log=$workdir/pak.log
	build/reminislink-host --headless --sd "$sd" --pairing-open --name "smoke-events pak" >"$log" 2>&1 &
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

# pair X NAME: pairs client X (A or B) with the next pairing link (pairing
# v2, TLS pinned to the link's "k"); sets X_id, $base and $api.
pair() {
	next_link
	rl2_use_link "$link"
	base=$RL2_BASE
	api=$base/api/v1
	for ((i = 0; i < 50; i++)); do pcurl -s -o /dev/null "$api/hello" && break; sleep 0.1; done
	rl2_pair "$1" "$2" apple || die "POST /pair ($1): $RL2_ERROR"
	pass "client $1 \"$2\" paired (pairing v2), device proof verified"
}

# --- signed requests -------------------------------------------------------

# sign X METHOD PATH: fills the array $sig_headers with the RL2-ED25519
# headers of a request of client X without body nor query string (for the
# streams, which rl2_req cannot leave open).
sign() {
	local who=$1 method=$2 path=$3 id time nonce content signature
	id=${who}_id
	id=${!id}
	time=$(date +%s)
	nonce=$(hex_to_b64u "$("$OPENSSL" rand -hex 16)")
	content=$(printf '' | sha256_hex)
	signature=$(printf 'RL2-ED25519\n%s\n%s\n\n%s\n%s\n%s' "$method" "$path" "$time" "$nonce" "$content" |
		ed25519_sign_b64u "$RL2_DIR/$who.pem")
	sig_headers=(-H "X-RL-Client: $id" -H "X-RL-Time: $time" -H "X-RL-Nonce: $nonce" -H 'X-RL-Platform: mac'
		-H "X-RL-Content-SHA256: $content" -H "X-RL-Signature: $signature")
}

# req X METHOD PATH [JSON BODY]: signed request of client X (rl2_req); sets
# $status and $body.
req() { rl2_req "$@"; }

# --- event streams ------------------------------------------------------------

# open_stream X FILE: opens GET /events for client X in the background;
# the stream goes to FILE, the response headers to FILE.h; sets $spid, the
# PID of curl itself (not of a subshell running pcurl), so that killing it
# closes the stream.
open_stream() {
	sign "$1" GET /api/v1/events
	curl -k --pinnedpubkey "sha256//$RL2_PIN_STD" -sS -N -D "$2.h" -o "$2" -w '%{http_code}\n' "${sig_headers[@]}" "$base/api/v1/events" >"$2.code" 2>"$2.err" &
	spid=$!
	stream_pids+=("$spid")
}

# events FILE: one line per complete event, "TYPE<TAB>DATA".
events() {
	awk 'BEGIN { t = ""; d = "" }
		/^event: / { t = substr($0, 8) }
		/^data: / { d = substr($0, 7) }
		/^$/ { if (t != "") print t "\t" d; t = ""; d = "" }' "$1" 2>/dev/null
}
count_events() { events "$1" | wc -l | tr -d ' '; }

# wait_event FILE FROM TYPE PATTERN SECONDS: waits until an event numbered
# above FROM (count before the action) has TYPE and a data matching the
# extended regex PATTERN; prints its data and the delay.
wait_event() {
	local file=$1 from=$2 type=$3 pattern=$4 timeout=$5 start t line
	start=$(now_ms)
	for ((t = 0; t < timeout * 20; t++)); do
		line=$(events "$file" | tail -n +$((from + 1)) | awk -F'\t' -v ty="$type" '$1 == ty' | grep -E -- "$pattern" | head -n 1 || true)
		if [ -n "$line" ]; then
			waited=$(awk -v a="$start" -v b="$(now_ms)" 'BEGIN { printf "%.2f", b - a }')
			event_data=${line#*$'\t'}
			return 0
		fi
		sleep 0.05
	done
	return 1
}

# find_ids X: the GBA systemId in $gba and Starlight Courier's gameId in
# $game, read by X.
find_ids() {
	req "$1" GET /api/v1/systems
	gba=$(printf '%s' "$body" | tr '{' '\n' | grep '"tag":"GBA"' | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
	[ -n "$gba" ] || die "no GBA system: $status $body"
	req "$1" GET "/api/v1/systems/$gba/games"
	games_json=$body
	game=$(printf '%s' "$body" | tr '{' '\n' | grep '"fileName":"Starlight Courier.gba"' | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
	[ -n "$game" ] || die "no Starlight Courier.gba in GBA: $status $body"
}

# --- UI helper mode ------------------------------------------------------------

if [ "${UI_DELETE:-0}" = 1 ]; then
	pair B "${CLIENT_NAME:-smoke-events B}"
	find_ids B
	victim=$(printf '%s' "$games_json" | tr '{' '\n' | grep '"fileName":"Lantern Keeper \[b1\].gba"' | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
	[ -n "$victim" ] || die "no Lantern Keeper [b1].gba in GBA"
	if [ -n "${READY_FILE:-}" ]; then
		for ((t = 0; t < ${READY_TIMEOUT:-60} * 10; t++)); do [ -e "$READY_FILE" ] && break; sleep 0.1; done
		if [ -e "$READY_FILE" ]; then echo "ready file seen"; else echo "WARN: no ready file after ${READY_TIMEOUT:-60} s, deleting anyway"; fi
	else
		sleep "${DELAY:-15}"
	fi
	# No write lease since M13: B writes as a second iPhone would; the pak
	# runs the writes one at a time (429 device_busy only after 10 s).
	at=$(now_ms)
	req B DELETE "/api/v1/games/$victim"
	[ -z "${TRIGGER_FILE:-}" ] || printf '%s\n' "$at" >"$TRIGGER_FILE"
	check "B deleted Lantern Keeper [b1] (DELETE /games/{id}) at $at" 200 "$status"
	[ "$failures" -eq 0 ] || exit 1
	exit 0
fi

# --- the checks --------------------------------------------------------------

pair A "smoke-events A"
pair B "smoke-events B"
find_ids A

sa=$workdir/a1
open_stream A "$sa"
a1_pid=$spid
if wait_event "$sa" 0 hello '"keepAliveSeconds":15' 5; then
	pass "A GET /events: hello within ${waited} s ($event_data)"
else
	failed "A GET /events: no hello within 5 s (status $(cat "$sa.code" 2>/dev/null), $(head -c 300 "$sa" 2>/dev/null))"
fi
check "A GET /events status" 200 "$(sed -n '1s/^HTTP\/[0-9.]* \([0-9]*\).*/\1/p' "$sa.h" | tr -d '\r')"
ctype=$(grep -i '^content-type:' "$sa.h" | head -n 1 | tr -d '\r' | sed 's/^[^:]*: *//')
case $ctype in text/event-stream*) pass "Content-Type: $ctype" ;; *) failed "Content-Type (expected text/event-stream, got '$ctype')" ;; esac
check "first event of the stream" hello "$(events "$sa" | head -n 1 | cut -f1)"
first_id=$(sed -n 's/^id: //p' "$sa" | head -n 1)
check "id of the first event" 1 "$first_id"

start=$(now_ms)
ka=""
for ((t = 0; t < 170; t++)); do
	if grep -q '^: keep-alive$' "$sa"; then ka=$(awk -v a="$start" -v b="$(now_ms)" 'BEGIN { printf "%.1f", b - a }'); break; fi
	sleep 0.1
done
if [ -n "$ka" ]; then pass "keep-alive comment after ${ka} s (at most 16 s)"; else failed "no keep-alive within 17 s"; fi

# B writes: a save backup of Starlight Courier.
n=$(count_events "$sa")
req B POST "/api/v1/games/$game/save/backups" '{"label":"smoke-events B"}'
check "B POST save backup (Starlight Courier)" 201 "$status"
if wait_event "$sa" "$n" library "\"$gba\"" 3; then
	pass "A received library naming the GBA systemId ${waited} s after B's write ($event_data)"
else
	failed "A: no library event naming GBA within 3 s of B's write (events: $(events "$sa" | tail -n +$((n + 1)) | tr '\t\n' ' |'))"
fi

# B downloads a ROM from 127.0.0.1: job events, then library.
if command -v python3 >/dev/null 2>&1; then
	www=$workdir/www
	mkdir -p "$www"
	head -c 3145728 /dev/urandom >"$www/Smoke Events.gba"
	hport=$(python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')
	python3 -m http.server "$hport" --bind 127.0.0.1 --directory "$www" >"$workdir/http.log" 2>&1 &
	http_pid=$!
	for ((t = 0; t < 50; t++)); do curl -s -o /dev/null "http://127.0.0.1:$hport/" && break; sleep 0.1; done
	n=$(count_events "$sa")
	req B POST /api/v1/downloads "{\"url\":\"http://127.0.0.1:$hport/Smoke%20Events.gba\",\"target\":{\"kind\":\"rom\",\"systemId\":\"$gba\",\"folderId\":null,\"fileName\":\"Smoke Events.gba\"},\"sha256\":null,\"onConflict\":\"rename\"}"
	check "B POST /downloads (ROM served on 127.0.0.1)" 202 "$status"
	job=$(json_field jobId "$body")
	if [ -n "$job" ] && wait_event "$sa" "$n" job "\"id\":\"$job\".*\"state\":\"succeeded\"|\"state\":\"succeeded\".*\"id\":\"$job\"" 15; then
		pass "A received job $job succeeded after ${waited} s"
		states=$(events "$sa" | tail -n +$((n + 1)) | awk -F'\t' '$1 == "job"' | grep -- "\"$job\"" | sed -En 's/.*"state":"([a-z]+)".*/\1/p' | tr '\n' ' ')
		pass "job states seen by A: $states"
		case $(events "$sa" | tail -n +$((n + 1)) | awk -F'\t' '$1 == "job"' | grep -- "\"$job\"" | head -n 1) in
		*'"kind":"download"'*) pass "job event carries kind download" ;;
		*) failed "job event without kind download" ;;
		esac
		if wait_event "$sa" "$n" library "\"$gba\"" 3; then
			pass "A received library naming GBA after the download ($event_data)"
		else
			failed "A: no library event after the download installed a ROM"
		fi
	else
		failed "A: no job event 'succeeded' for job '$job' within 15 s (events: $(events "$sa" | tail -n +$((n + 1)) | cut -c1-160 | tr '\t\n' ' |'))"
	fi
	kill "$http_pid" 2>/dev/null || true
	http_pid=""
else
	echo "SKIP: job events (python3 not found to serve a file on 127.0.0.1)"
fi

# Stream limit: 4 per client.
extra=()
for k in 2 3 4; do
	open_stream A "$workdir/a$k"
	extra+=("$spid")
done
for k in 2 3 4; do
	if wait_event "$workdir/a$k" 0 hello . 5; then pass "A stream $k accepted (hello)"; else failed "A stream $k: no hello ($(cat "$workdir/a$k.code" 2>/dev/null))"; fi
done
sign A GET /api/v1/events
fifth=$(pcurl -sS -m 5 -D "$workdir/a5.h" -w '\n%{http_code}' "${sig_headers[@]}" "$base/api/v1/events" || true)
check "5th stream of A" 429 "${fifth##*$'\n'}"
check "5th stream error code" too_many_attempts "$(json_field code "${fifth%$'\n'*}")"
ra=$(grep -i '^retry-after:' "$workdir/a5.h" | tr -d '\r' | sed 's/^[^:]*: *//')
# docs/specs/04-api-v1.md (GET /events, limits): Retry-After 5 s.
check "5th stream Retry-After (seconds)" 5 "$ra"
# Closing one frees a place.
kill "${extra[2]}" 2>/dev/null || true
wait "${extra[2]}" 2>/dev/null || true
reopened=""
for ((t = 0; t < 20; t++)); do
	sleep 0.25
	sign A GET /api/v1/events
	code=$(pcurl -sS -m 1 -o /dev/null -w '%{http_code}' "${sig_headers[@]}" "$base/api/v1/events" 2>/dev/null || true)
	# 200 then cut by -m 1 (curl exit 28) prints 200.
	if [ "$code" = 200 ]; then reopened=$(awk -v t="$t" 'BEGIN { printf "%.2f", (t + 1) * 0.25 }'); break; fi
done
if [ -n "$reopened" ]; then pass "after closing a stream, a new one is accepted (within ${reopened} s)"; else failed "after closing a stream, a new one is still refused (last status $code)"; fi
kill "${extra[0]}" "${extra[1]}" 2>/dev/null || true

# Shutdown: SIGTERM (the system path; the console's Quit needs the UI).
sleep 1.2 # let the closed streams go
n=$(count_events "$sa")
term_at=$(now_ms)
kill -TERM "$pak_pid"
if wait_event "$sa" "$n" shutdown '"reason":"stopped"' 3; then
	pass "A received shutdown ${waited} s after SIGTERM ($event_data)"
else
	failed "A: no shutdown {\"reason\":\"stopped\"} within 3 s of SIGTERM (tail: $(tail -c 300 "$sa" | tr '\n' '|'))"
fi
eof=""
for ((t = 0; t < 40; t++)); do
	if ! kill -0 "$a1_pid" 2>/dev/null; then eof=$(awk -v a="$term_at" -v b="$(now_ms)" 'BEGIN { printf "%.2f", b - a }'); break; fi
	sleep 0.05
done
if [ -n "$eof" ]; then
	wait "$a1_pid" && rc=0 || rc=$?
	pass "A's stream ended (EOF) ${eof} s after SIGTERM (curl exit $rc$([ "$rc" = 0 ] && echo ', clean end' || echo ", $(tr '\n' ' ' <"$sa.err")"))"
	check "curl exit code at the end of the stream (0 = clean end of the chunked body)" 0 "$rc"
else
	failed "A's stream still open 2 s after SIGTERM"
fi
check "last event of A's stream" shutdown "$(events "$sa" | tail -n 1 | cut -f1)"
wait "$pak_pid" 2>/dev/null && prc=0 || prc=$?
pak_pid=""
check "pak exit status after SIGTERM" 0 "$prc"
grep -q 'event streams closed' "$log" && pass "pak log: $(grep -o 'msg="event streams closed".*' "$log" | head -n 1)" ||
	echo "info: no 'event streams closed' line on the pak output (the log file is on the card)"

if [ "$failures" -gt 0 ]; then
	echo "smoke-events: $failures check(s) FAILED"
	exit 1
fi
echo "smoke-events: OK"
