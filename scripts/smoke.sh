#!/usr/bin/env bash
# Smoke test of a running pak with curl and openssl only (M13): checks the
# certificate pin of the pairing link, pairs (pairing v2, an Ed25519 key),
# checks the device proof, then sends a signed GET /api/v1/systems
# (RL2-ED25519). The client is unpaired at the end (DELETE
# /api/v1/clients/me) unless KEEP_CLIENT=1.
#
# Usage:
#   make headless            # in another terminal: prints "Pairing link: ..."
#   echo '<pairing link>' | scripts/smoke.sh [base URL]
#
# The link is read from the standard input so the pairing secret never
# shows in the process list (see scripts/lib/rl2.sh). The base URL
# defaults to https://<h>:<p> from the link; every connection is pinned
# to its "k". Needs OpenSSL 3 or later.
set -euo pipefail

if [ $# -gt 1 ] || [ -t 0 ]; then
	sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
fi
IFS= read -r link || [ -n "$link" ]

# shellcheck source=lib/rl2.sh
. "$(dirname "$0")/lib/rl2.sh"
fail() { echo "smoke: $*" >&2; exit 1; }
RL2_DIR=$(mktemp -d "${TMPDIR:-/tmp}/smoke.XXXXXX")
trap 'rm -rf "$RL2_DIR"' EXIT
rl2_selftest

rl2_use_link "$link"
RL2_BASE=${1:-$RL2_BASE}
api=$RL2_BASE/api/v1
hostport=${RL2_BASE#https://}

# --- TLS: the certificate is the one of the QR code --------------------------

[ "$(rl2_pin_of_server "$hostport")" = "$RL2_PIN" ] || fail "the certificate of $hostport does not match the pin of the link"
echo "TLS: certificate pin verified (openssl s_client)"
if curl -sS -k --pinnedpubkey "sha256//$(b64u_to_std "$(hex_to_b64u "$("$OPENSSL" rand -hex 32)")")" "$api/hello" >/dev/null 2>&1; then
	fail "curl accepted a wrong pin"
fi
if curl -sS --max-time 5 "http://$hostport/api/v1/hello" 2>/dev/null | grep -q '"deviceId"'; then
	fail "the pak answers in plain HTTP"
fi
echo "TLS: wrong pin refused, no plain HTTP"

# --- GET /hello ------------------------------------------------------------

hello=$(pcurl -sS --fail-with-body "$api/hello") || fail "GET /hello: $hello"
[ "$(json_field deviceId "$hello")" = "$RL2_DEVICE_ID" ] || fail "GET /hello answered another device: $hello"
[ "$(json_num apiVersion "$hello")" = 2 ] || fail "GET /hello: apiVersion is not 2: $hello"
echo "hello: $(json_field deviceName "$hello") ($RL2_DEVICE_ID), apiVersion 2"

# --- POST /pair (v2) ---------------------------------------------------------

rl2_pair C smoke.sh || fail "POST /pair: $RL2_ERROR"
echo "paired: client $C_id, device proof verified"

# --- signed requests -------------------------------------------------------

rl2_req C GET /api/v1/systems
[ "$status" = 200 ] || fail "signed GET /systems: $status $body"
case $body in *'"systems":['*) ;; *) fail "signed GET /systems: $body" ;; esac
echo "signed GET /systems: $status"
echo "$body"

if [ "${KEEP_CLIENT:-0}" != 1 ]; then
	rl2_req C DELETE /api/v1/clients/me
	[ "$status" = 204 ] || fail "DELETE /clients/me: $status $body"
	echo "unpaired: client $C_id"
fi
echo "smoke: OK"
