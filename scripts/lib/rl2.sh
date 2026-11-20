# shellcheck shell=bash
# RL2 client helpers for the smoke scripts (M13, docs/specs/03-protocole.md
# §1, §3 and §4), with bash, curl and openssl only:
#   - TLS pinned to the certificate pin "k" of the pairing link
#     (curl -k --pinnedpubkey sha256//<standard base64>: -k only skips the
#     CA and host name checks, the pin is still enforced);
#   - pairing v2 (POST /pair with an Ed25519 public key and HMAC proofs);
#   - requests signed with Ed25519 (openssl pkeyutl -sign -rawin).
# Secrets stay out of process arguments: the pairing secret and the HMAC
# keys only go through pipes and bash variables, and each private key is a
# PEM file in a private temporary folder (only its path is an argument).
# Nothing secret is printed.
#
# Bash 3.2 compatible (macOS /bin/bash): no BASHPID, and no expansion of
# an empty array under set -u.
#
# Source it, then set RL2_DIR to a private folder (mktemp -d) and call
# rl2_selftest once. Needs OpenSSL 3 or later (Ed25519 with -rawin); set
# OPENSSL to choose the binary.

OPENSSL=${OPENSSL:-openssl}

# Never trace: the functions below handle the pairing secret, HMAC keys
# and signatures (a caller with set -x would print them).
{ set +x; } 2>/dev/null

rl2_die() { echo "rl2: $*" >&2; exit 1; }

# --- encodings -------------------------------------------------------------

hex_to_bin() {
	local hex=$1 out="" i
	for ((i = 0; i < ${#hex}; i += 2)); do out+="\\x${hex:i:2}"; done
	printf "$out"
}
bin_to_hex() { od -An -v -tx1 | tr -d ' \n'; }
bin_to_b64u() { "$OPENSSL" base64 -A | tr '+/' '-_' | tr -d '='; }
hex_to_b64u() { hex_to_bin "$1" | bin_to_b64u; }
b64u_to_std() {
	local s
	s=$(printf '%s' "$1" | tr -- '-_' '+/')
	while [ $((${#s} % 4)) -ne 0 ]; do s="$s="; done
	printf '%s' "$s"
}
b64u_to_hex() { b64u_to_std "$1" | "$OPENSSL" base64 -d -A | bin_to_hex; }
sha256_hex() { "$OPENSSL" dgst -sha256 -binary | bin_to_hex; }
json_field() { printf '%s' "$2" | sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p"; }
json_num() { printf '%s' "$2" | sed -En "s/.*\"$1\":([0-9]+).*/\1/p"; }
json_bool() { printf '%s' "$2" | sed -En "s/.*\"$1\":(true|false).*/\1/p"; }
uuid() {
	if [ -r /proc/sys/kernel/random/uuid ]; then cat /proc/sys/kernel/random/uuid; else uuidgen; fi | tr 'A-F' 'a-f'
}

# hmac_hex KEY_HEX < message: HMAC-SHA256 (RFC 2104) in hex, built on the
# plain digest so the key never appears in the arguments of a process.
hmac_hex() {
	local key=$1 ipad="" opad="" i b x inner
	if [ ${#key} -gt 128 ]; then key=$(hex_to_bin "$key" | sha256_hex); fi
	while [ ${#key} -lt 128 ]; do key="${key}00"; done
	for ((i = 0; i < 128; i += 2)); do
		b=$((16#${key:i:2}))
		printf -v x '%02x' $((b ^ 0x36)); ipad+=$x
		printf -v x '%02x' $((b ^ 0x5c)); opad+=$x
	done
	inner=$({ hex_to_bin "$ipad"; cat; } | sha256_hex)
	{ hex_to_bin "$opad"; hex_to_bin "$inner"; } | sha256_hex
}

# --- Ed25519 keys (RFC 8032) -----------------------------------------------

# ed25519_from_seed SEED_HEX FILE: writes the private key of a 32-byte seed
# (PKCS#8: a fixed 16-byte prefix and the seed). The seed goes through a
# pipe.
ed25519_from_seed() {
	(umask 077 && hex_to_bin "302e020100300506032b657004220420$1" |
		"$OPENSSL" pkey -inform DER -out "$2" 2>/dev/null) || rl2_die "cannot build an Ed25519 key (OpenSSL 3 needed)"
}

# ed25519_new FILE: a new random private key.
ed25519_new() {
	(umask 077 && "$OPENSSL" genpkey -algorithm ed25519 -out "$1" 2>/dev/null) || rl2_die "cannot create an Ed25519 key"
}

# ed25519_pub_b64u FILE: the raw 32-byte public key, base64url.
ed25519_pub_b64u() { "$OPENSSL" pkey -in "$1" -pubout -outform DER | tail -c 32 | bin_to_b64u; }

# ed25519_sign_b64u FILE < message: the 64-byte signature, base64url.
ed25519_sign_b64u() {
	local msg sig
	# mktemp: unique names even for concurrent calls (Bash 3.2 has no
	# BASHPID, and $$ is the same in every subshell).
	msg=$(mktemp "$RL2_DIR/msg.XXXXXX") && sig=$(mktemp "$RL2_DIR/sig.XXXXXX") || rl2_die "mktemp failed"
	cat >"$msg"
	"$OPENSSL" pkeyutl -sign -inkey "$1" -rawin -in "$msg" -out "$sig" || rl2_die "Ed25519 signature failed"
	bin_to_b64u <"$sig"
	rm -f "$msg" "$sig"
}

# rl2_selftest: HMAC (RFC 4231 case 2) and Ed25519 (the "get-no-query"
# vector of protocol/test-vectors/signing.json: Ed25519 is deterministic).
rl2_selftest() {
	[ -n "${RL2_DIR:-}" ] && [ -d "$RL2_DIR" ] || rl2_die "set RL2_DIR to a private folder"
	[ "$(printf 'what do ya want for nothing?' | hmac_hex 4a656665)" = \
		5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843 ] || rl2_die "HMAC self-test failed"
	local k=$RL2_DIR/selftest.pem
	ed25519_from_seed "$(b64u_to_hex L3fx27ZayfYjBVbataayCCiWy-klurmAkgZSRxH58QI)" "$k"
	[ "$(ed25519_pub_b64u "$k")" = efRBFoXm7AOZqWQrstMx1qKlcahnAu40Qd6ytCs7tcY ] || rl2_die "Ed25519 public key self-test failed"
	[ "$(printf 'RL2-ED25519\nGET\n/api/v1/systems\n\n1795539600\nfRcnvGM1KB6G5R8dIJTzVA\ne3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855' |
		ed25519_sign_b64u "$k")" = J13aK5jZaaeP4nWOCQIYbsyomKMgDKhvu-BR3Sqxg2GK6r1frWzwIdo_-ptox0Es5zDkjbZK5aU8-I_K9BwgBw ] ||
		rl2_die "Ed25519 signature self-test failed"
	rm -f "$k"
}

# --- pairing link and TLS -------------------------------------------------

# link_param LINK NAME: a parameter of the pairing link.
link_param() { printf '%s' "$1" | sed -n "s/.*[?&]$2=\([^&]*\).*/\1/p"; }

# rl2_use_link LINK: reads id, s, h, p and k of a pairing link v2; sets
# RL2_DEVICE_ID, RL2_SECRET_HEX, RL2_BASE (https://h:p), RL2_PIN (b64u) and
# RL2_PIN_STD (standard base64, for curl).
rl2_use_link() {
	[ "$(link_param "$1" v)" = 2 ] || rl2_die "not a pairing link v2 (update the pak?)"
	RL2_DEVICE_ID=$(link_param "$1" id)
	local s h p
	s=$(link_param "$1" s)
	h=$(link_param "$1" h)
	p=$(link_param "$1" p)
	RL2_PIN=$(link_param "$1" k)
	[ -n "$RL2_DEVICE_ID" ] && [ -n "$s" ] && [ -n "$h" ] && [ -n "$p" ] && [ -n "$RL2_PIN" ] || rl2_die "incomplete pairing link (masked)"
	RL2_SECRET_HEX=$(b64u_to_hex "$s")
	[ ${#RL2_SECRET_HEX} -eq 64 ] || rl2_die "the secret is not 32 bytes"
	[ ${#RL2_PIN} -eq 43 ] || rl2_die "the pin is not 32 bytes"
	RL2_PIN_STD=$(b64u_to_std "$RL2_PIN")
	RL2_BASE=https://$h:$p
}

# pcurl ARGS...: curl over TLS pinned to RL2_PIN.
pcurl() { curl -k --pinnedpubkey "sha256//$RL2_PIN_STD" "$@"; }

# rl2_pin_of_server HOST:PORT: the pin of the certificate the server
# presents (openssl s_client), base64url.
rl2_pin_of_server() {
	"$OPENSSL" s_client -connect "$1" -servername reminislink </dev/null 2>/dev/null |
		"$OPENSSL" x509 -pubkey -noout | "$OPENSSL" pkey -pubin -outform DER | "$OPENSSL" dgst -sha256 -binary | bin_to_b64u
}

# --- pairing v2 and signed requests -----------------------------------------

# rl2_pair WHO NAME [PLATFORM]: pairs client WHO with the current link (a
# new Ed25519 key in $RL2_DIR/WHO.pem); checks the device proof; sets
# WHO_id and WHO_name. Returns 1 (with $RL2_ERROR) if the pak refuses.
rl2_pair() {
	local who=$1 name=$2 platform=${3:-other} key client_id cn pub proof body response dn dp expected
	key=$RL2_DIR/$who.pem
	ed25519_new "$key"
	pub=$(ed25519_pub_b64u "$key")
	client_id=$(uuid)
	cn=$(hex_to_b64u "$("$OPENSSL" rand -hex 32)")
	proof=$(printf '%s' "RL2-PAIR-CLIENT|$RL2_DEVICE_ID|$client_id|$pub|$cn" | hmac_hex "$RL2_SECRET_HEX")
	body=$(printf '{"clientId":"%s","clientName":"%s","platform":"%s","clientPublicKey":"%s","clientNonce":"%s","proof":"%s"}' \
		"$client_id" "$name" "$platform" "$pub" "$cn" "$(hex_to_b64u "$proof")")
	if ! response=$(pcurl -sS --fail-with-body -X POST -H 'Content-Type: application/json' -H 'X-RL-Platform: mac' \
		--data-binary @- "$RL2_BASE/api/v1/pair" <<<"$body"); then
		RL2_ERROR=$response
		return 1
	fi
	dn=$(json_field deviceNonce "$response")
	dp=$(json_field proof "$response")
	expected=$(printf '%s' "RL2-PAIR-DEVICE|$RL2_DEVICE_ID|$client_id|$pub|$cn|$dn" | hmac_hex "$RL2_SECRET_HEX")
	[ "$(hex_to_b64u "$expected")" = "$dp" ] || rl2_die "the device proof of the pairing of $who does not match"
	printf -v "${who}_id" '%s' "$client_id"
	printf -v "${who}_name" '%s' "$name"
}

# rl2_req WHO METHOD PATH [JSON BODY] [OUTFILE]: request signed by client
# WHO (path with its query, already canonical: no query here); sets
# $status, and $body unless OUTFILE is given (the body goes there).
rl2_req() {
	local who=$1 method=$2 path=$3 data=${4-} out=${5-} id time nonce content canonical signature response
	local -a opts=()
	id=${who}_id
	id=${!id}
	time=$(date +%s)
	nonce=$(hex_to_b64u "$("$OPENSSL" rand -hex 16)")
	content=$(printf '%s' "$data" | sha256_hex)
	canonical=$(printf 'RL2-ED25519\n%s\n%s\n\n%s\n%s\n%s' "$method" "$path" "$time" "$nonce" "$content")
	signature=$(printf '%s' "$canonical" | ed25519_sign_b64u "$RL2_DIR/$who.pem")
	if [ -n "$data" ]; then opts=(-H 'Content-Type: application/json' --data-binary @-); fi
	if [ -n "$out" ]; then
		status=$(printf '%s' "$data" | pcurl -sS -X "$method" -o "$out" -w '%{http_code}' ${opts[@]+"${opts[@]}"} \
			-H "X-RL-Client: $id" -H "X-RL-Time: $time" -H "X-RL-Nonce: $nonce" -H 'X-RL-Platform: mac' \
			-H "X-RL-Content-SHA256: $content" -H "X-RL-Signature: $signature" "$RL2_BASE$path")
		return
	fi
	response=$(printf '%s' "$data" | pcurl -sS -X "$method" -w '\n%{http_code}' ${opts[@]+"${opts[@]}"} \
		-H "X-RL-Client: $id" -H "X-RL-Time: $time" -H "X-RL-Nonce: $nonce" -H 'X-RL-Platform: mac' \
		-H "X-RL-Content-SHA256: $content" -H "X-RL-Signature: $signature" "$RL2_BASE$path")
	status=${response##*$'\n'}
	body=${response%$'\n'*}
	[ "$body" != "$status" ] || body=""
}
