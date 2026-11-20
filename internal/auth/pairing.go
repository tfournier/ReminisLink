// Package auth implements pairing v2, Ed25519 request signing (RL2), the
// nonce cache and the store of paired clients (docs/specs/03-protocole.md
// §3 and §4).
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"regexp"
)

const (
	// PairingNonceSize is the length of clientNonce and deviceNonce.
	PairingNonceSize = 32
	// SecretSize is the length of the pairing secret carried by the QR code.
	SecretSize = 32
)

// b64u is base64url without padding. Decoding is strict (canonical only).
var b64u = base64.RawURLEncoding.Strict()

// EncodeB64U encodes b as base64url without padding.
func EncodeB64U(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// ErrBadBase64URL is returned for anything but canonical unpadded base64url.
var ErrBadBase64URL = errors.New("invalid base64url")

// DecodeB64U decodes canonical base64url without padding. Unlike the
// standard decoder, it also refuses '\r' and '\n' (which encoding/base64
// silently skips, even in strict mode).
func DecodeB64U(s string) ([]byte, error) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return nil, ErrBadBase64URL
		}
	}
	b, err := b64u.DecodeString(s)
	if err != nil {
		return nil, ErrBadBase64URL
	}
	return b, nil
}

// uuidPattern is the only accepted form of deviceId and clientId: a
// lowercase, hyphenated UUID. Keeping identifiers ASCII avoids any Unicode
// normalization question in the signed strings, and the absence of "|"
// keeps the "|"-separated messages unambiguous.
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidID reports whether s is a lowercase hyphenated UUID.
func ValidID(s string) bool { return uuidPattern.MatchString(s) }

func mac(key []byte, msg string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(msg))
	return h.Sum(nil)
}

// verifyMAC compares an expected MAC with a received b64u MAC in constant
// time. A malformed encoding simply fails.
func verifyMAC(expected []byte, receivedB64U string) bool {
	got, err := DecodeB64U(receivedB64U)
	if err != nil {
		return false
	}
	return hmac.Equal(expected, got)
}

// PairingURLScheme is the scheme of the link carried by the QR code.
const PairingURLScheme = "reminislink"
