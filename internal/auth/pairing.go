// Package auth implements pairing, HMAC request signing, the nonce cache and
// the store of paired clients (docs/specs/03-protocole.md §3 and §4).
package auth

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
)

// Pairing exchange strings, byte for byte as in docs/specs/03-protocole.md §3.2.
const (
	pairClientPrefix = "RL1-PAIR-CLIENT|"
	pairDevicePrefix = "RL1-PAIR-DEVICE|"
	keyInfoPrefix    = "RL1-KEY|"
)

const (
	// KeySize is the length of the key shared by a client and the pak.
	KeySize = 32
	// PairingNonceSize is the length of clientNonce and deviceNonce.
	PairingNonceSize = 32
	// SecretSize is the length of the pairing secret carried by the QR code.
	SecretSize = 32
	// PINLength is the number of digits of the fallback PIN.
	PINLength = 6
)

// PairingMethod is the "method" field of POST /pair.
type PairingMethod string

const (
	MethodQR  PairingMethod = "qr"
	MethodPIN PairingMethod = "pin"
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

// ClientProofMessage is the message the client MACs with ikm.
func ClientProofMessage(deviceID, clientID, clientNonceB64U string) string {
	return pairClientPrefix + deviceID + "|" + clientID + "|" + clientNonceB64U
}

// DeviceProofMessage is the message the pak MACs with the derived key.
func DeviceProofMessage(deviceID, clientID string) string {
	return pairDevicePrefix + deviceID + "|" + clientID
}

// KeyInfo is the HKDF info string.
func KeyInfo(deviceID, clientID string) string {
	return keyInfoPrefix + deviceID + "|" + clientID
}

// ClientProof returns b64u(HMAC(ikm, ClientProofMessage)). clientNonceB64U is
// the nonce exactly as sent in the request.
func ClientProof(ikm []byte, deviceID, clientID, clientNonceB64U string) string {
	return EncodeB64U(mac(ikm, ClientProofMessage(deviceID, clientID, clientNonceB64U)))
}

// DeviceProof returns b64u(HMAC(key, DeviceProofMessage)).
func DeviceProof(key []byte, deviceID, clientID string) string {
	return EncodeB64U(mac(key, DeviceProofMessage(deviceID, clientID)))
}

// VerifyClientProof checks a client proof in constant time.
func VerifyClientProof(ikm []byte, deviceID, clientID, clientNonceB64U, proof string) bool {
	return verifyMAC(mac(ikm, ClientProofMessage(deviceID, clientID, clientNonceB64U)), proof)
}

// VerifyDeviceProof checks a device proof in constant time.
func VerifyDeviceProof(key []byte, deviceID, clientID, proof string) bool {
	return verifyMAC(mac(key, DeviceProofMessage(deviceID, clientID)), proof)
}

// DeriveKey derives the shared key:
//
//	HKDF-SHA256(ikm, salt = clientNonce || deviceNonce,
//	            info = "RL1-KEY|" + deviceId + "|" + clientId, L = 32)
func DeriveKey(ikm, clientNonce, deviceNonce []byte, deviceID, clientID string) ([]byte, error) {
	if len(ikm) == 0 {
		return nil, errors.New("derive key: empty ikm")
	}
	if len(clientNonce) != PairingNonceSize || len(deviceNonce) != PairingNonceSize {
		return nil, fmt.Errorf("derive key: nonces must be %d bytes", PairingNonceSize)
	}
	salt := make([]byte, 0, 2*PairingNonceSize)
	salt = append(salt, clientNonce...)
	salt = append(salt, deviceNonce...)
	key, err := hkdf.Key(sha256.New, ikm, salt, KeyInfo(deviceID, clientID), KeySize)
	if err != nil {
		return nil, fmt.Errorf("derive key: %w", err)
	}
	return key, nil
}

// PINIKM returns the input keying material of the PIN mode (its UTF-8 bytes).
func PINIKM(pin string) []byte { return []byte(pin) }

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

// PairingURL returns the link of the QR code (docs/specs/03-protocole.md
// §3.1):
//
//	reminislink://pair?v=1&id=<deviceId>&n=<name>&s=<b64u secret>&h=<IPv4>&p=<port>
//
// The name is percent-encoded per RFC 3986 (a space is %20).
func PairingURL(deviceID, name string, secret []byte, host string, port int) string {
	return fmt.Sprintf("%s://pair?v=1&id=%s&n=%s&s=%s&h=%s&p=%d",
		PairingURLScheme, EncodeRFC3986(deviceID), EncodeRFC3986(name), EncodeB64U(secret), EncodeRFC3986(host), port)
}
