#!/usr/bin/env bash
# Smoke test of a running pak with curl and openssl only: pairs with the
# link of the QR code (docs/specs/03-protocole.md §3), checks the device
# proof, then sends a signed GET /api/v1/systems (§4). The client is
# unpaired at the end (DELETE /api/v1/clients/me) unless KEEP_CLIENT=1.
#
# Usage:
#   make headless            # in another terminal: prints "Pairing link: ..."
#   echo '<pairing link>' | scripts/smoke.sh [base URL]
#
# The link is read from the standard input so the pairing secret never
# shows in the process list; the HMAC is computed with "openssl dgst"
# reading the key from a pipe, never from its arguments. The base URL
# defaults to http://<h>:<p> from the link.
set -euo pipefail

if [ $# -gt 1 ] || [ -t 0 ]; then
	sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
fi
IFS= read -r link || [ -n "$link" ]

fail() { echo "smoke: $*" >&2; exit 1; }

param() { printf '%s' "$link" | sed -n "s/.*[?&]$1=\([^&]*\).*/\1/p"; }
device_id=$(param id)
secret_b64u=$(param s)
host=$(param h)
port=$(param p)
[ -n "$device_id" ] && [ -n "$secret_b64u" ] && [ -n "$host" ] && [ -n "$port" ] || fail "not a pairing link: $link"
base=${1:-http://$host:$port}
api=$base/api/v1

# --- encodings -------------------------------------------------------------

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
# hmac_hex KEY_HEX < message: HMAC-SHA256 (RFC 2104) in hex. Built on the
# plain digest so the key never appears in the arguments of a process
# (printf and the string handling are bash builtins).
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
# Self-test with RFC 4231 test case 2 (public key "Jefe").
[ "$(printf 'what do ya want for nothing?' | hmac_hex 4a656665)" = \
	5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843 ] || fail "HMAC self-test failed"
json_field() { printf '%s' "$2" | sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p"; }
uuid() {
	if [ -r /proc/sys/kernel/random/uuid ]; then cat /proc/sys/kernel/random/uuid; else uuidgen; fi | tr 'A-F' 'a-f'
}

# --- GET /hello ------------------------------------------------------------

hello=$(curl -sS --fail-with-body "$api/hello") || fail "GET /hello: $hello"
[ "$(json_field deviceId "$hello")" = "$device_id" ] || fail "GET /hello answered another device: $hello"
echo "hello: $(json_field deviceName "$hello") ($device_id)"

# --- POST /pair (QR mode) --------------------------------------------------

secret_hex=$(b64u_to_hex "$secret_b64u")
[ ${#secret_hex} -eq 64 ] || fail "the secret is not 32 bytes"
client_id=$(uuid)
client_nonce_hex=$(openssl rand -hex 32)
client_nonce=$(hex_to_b64u "$client_nonce_hex")
proof=$(printf '%s' "RL1-PAIR-CLIENT|$device_id|$client_id|$client_nonce" | hmac_hex "$secret_hex")
body=$(printf '{"method":"qr","clientId":"%s","clientName":"smoke.sh","clientNonce":"%s","proof":"%s"}' \
	"$client_id" "$client_nonce" "$(hex_to_b64u "$proof")")
pair=$(curl -sS --fail-with-body -X POST -H 'Content-Type: application/json' --data "$body" "$api/pair") ||
	fail "POST /pair: $pair"
device_nonce=$(json_field deviceNonce "$pair")
device_proof=$(json_field proof "$pair")
[ -n "$device_nonce" ] && [ -n "$device_proof" ] || fail "POST /pair: $pair"

# key = HKDF-SHA256(ikm = secret, salt = clientNonce || deviceNonce,
#                   info = "RL1-KEY|" + deviceId + "|" + clientId, L = 32)
prk=$(hex_to_bin "$secret_hex" | hmac_hex "$client_nonce_hex$(b64u_to_hex "$device_nonce")")
key=$({ printf '%s' "RL1-KEY|$device_id|$client_id"; printf '\001'; } | hmac_hex "$prk")
expected=$(printf '%s' "RL1-PAIR-DEVICE|$device_id|$client_id" | hmac_hex "$key")
[ "$(hex_to_b64u "$expected")" = "$device_proof" ] || fail "the device proof does not match: wrong device or wrong key"
echo "paired: client $client_id, device proof verified"

# --- signed requests -------------------------------------------------------

# signed METHOD PATH: prints the body, then the status on the last line.
signed() {
	local method=$1 path=$2 time nonce content canonical signature
	time=$(date +%s)
	nonce=$(hex_to_b64u "$(openssl rand -hex 16)")
	content=e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855 # SHA-256 of an empty body
	canonical=$(printf 'RL1-HMAC-SHA256\n%s\n%s\n\n%s\n%s\n%s' "$method" "$path" "$time" "$nonce" "$content")
	signature=$(printf '%s' "$canonical" | hmac_hex "$key")
	curl -sS -X "$method" -w '\n%{http_code}' \
		-H "X-RL-Client: $client_id" -H "X-RL-Time: $time" -H "X-RL-Nonce: $nonce" \
		-H "X-RL-Content-SHA256: $content" -H "X-RL-Signature: $(hex_to_b64u "$signature")" \
		"$base$path"
}

response=$(signed GET /api/v1/systems)
status=${response##*$'\n'}
systems=${response%$'\n'*}
[ "$status" = 200 ] || fail "signed GET /systems: $status $systems"
case $systems in *'"systems":['*) ;; *) fail "signed GET /systems: $systems" ;; esac
echo "signed GET /systems: $status"
echo "$systems"

if [ "${KEEP_CLIENT:-0}" != 1 ]; then
	response=$(signed DELETE /api/v1/clients/me)
	[ "${response##*$'\n'}" = 204 ] || fail "DELETE /clients/me: $response"
	echo "unpaired: client $client_id"
fi
echo "smoke: OK"
