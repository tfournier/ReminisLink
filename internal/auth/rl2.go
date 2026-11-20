package auth

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
)

// RL2 (M13): pairing v2 and Ed25519 request signing
// (docs/specs/03-protocole.md §3 and §4, protocol/openapi.yaml).

// RL2 strings, byte for byte as in the contract.
const (
	// SignatureAlgorithmRL2 is the first line of the RL2 canonical string.
	SignatureAlgorithmRL2 = "RL2-ED25519"
	pairClientPrefixRL2   = "RL2-PAIR-CLIENT|"
	pairDevicePrefixRL2   = "RL2-PAIR-DEVICE|"
)

const (
	// PublicKeySize is the length of a client's Ed25519 public key.
	PublicKeySize = ed25519.PublicKeySize
	// SignatureSize is the length of an Ed25519 signature (X-RL-Signature
	// once decoded).
	SignatureSize = ed25519.SignatureSize
	// PairingLinkVersion is the "v" of the pairing link (QR code).
	PairingLinkVersion = 2
)

// PairClientMessage is the message the client MACs with the QR secret:
//
//	"RL2-PAIR-CLIENT|" + deviceId + "|" + clientId + "|" + clientPublicKey + "|" + clientNonce
//
// clientPublicKey and clientNonce are the base64url strings exactly as sent.
func PairClientMessage(deviceID, clientID, publicKeyB64U, clientNonceB64U string) string {
	return pairClientPrefixRL2 + deviceID + "|" + clientID + "|" + publicKeyB64U + "|" + clientNonceB64U
}

// PairDeviceMessage is the message the pak MACs with the QR secret:
//
//	"RL2-PAIR-DEVICE|" + deviceId + "|" + clientId + "|" + clientPublicKey + "|" + clientNonce + "|" + deviceNonce
func PairDeviceMessage(deviceID, clientID, publicKeyB64U, clientNonceB64U, deviceNonceB64U string) string {
	return pairDevicePrefixRL2 + deviceID + "|" + clientID + "|" + publicKeyB64U + "|" + clientNonceB64U + "|" + deviceNonceB64U
}

// PairClientProof returns b64u(HMAC-SHA256(secret, PairClientMessage)).
func PairClientProof(secret []byte, deviceID, clientID, publicKeyB64U, clientNonceB64U string) string {
	return EncodeB64U(mac(secret, PairClientMessage(deviceID, clientID, publicKeyB64U, clientNonceB64U)))
}

// PairDeviceProof returns b64u(HMAC-SHA256(secret, PairDeviceMessage)).
func PairDeviceProof(secret []byte, deviceID, clientID, publicKeyB64U, clientNonceB64U, deviceNonceB64U string) string {
	return EncodeB64U(mac(secret, PairDeviceMessage(deviceID, clientID, publicKeyB64U, clientNonceB64U, deviceNonceB64U)))
}

// VerifyPairClientProof checks a client proof in constant time.
func VerifyPairClientProof(secret []byte, deviceID, clientID, publicKeyB64U, clientNonceB64U, proof string) bool {
	return verifyMAC(mac(secret, PairClientMessage(deviceID, clientID, publicKeyB64U, clientNonceB64U)), proof)
}

// VerifyPairDeviceProof checks a device proof in constant time.
func VerifyPairDeviceProof(secret []byte, deviceID, clientID, publicKeyB64U, clientNonceB64U, deviceNonceB64U, proof string) bool {
	return verifyMAC(mac(secret, PairDeviceMessage(deviceID, clientID, publicKeyB64U, clientNonceB64U, deviceNonceB64U)), proof)
}

// ErrBadPublicKey is returned for a clientPublicKey that is not 32 bytes of
// canonical base64url.
var ErrBadPublicKey = errors.New("invalid Ed25519 public key")

// DecodePublicKey decodes a clientPublicKey (32 bytes, base64url).
func DecodePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := DecodeB64U(s)
	if err != nil || len(b) != PublicKeySize {
		return nil, ErrBadPublicKey
	}
	return ed25519.PublicKey(b), nil
}

// CanonicalRequestRL2 is CanonicalRequest with the first line RL2-ED25519.
func CanonicalRequestRL2(p RequestParts) (string, error) {
	c, err := CanonicalRequest(p)
	if err != nil {
		return "", err
	}
	return SignatureAlgorithmRL2 + strings.TrimPrefix(c, SignatureAlgorithm), nil
}

// SignRL2 returns b64u(Ed25519(priv, canonical)), 64 bytes once decoded.
func SignRL2(priv ed25519.PrivateKey, canonical string) string {
	return EncodeB64U(ed25519.Sign(priv, []byte(canonical)))
}

// VerifyRL2 checks a b64u Ed25519 signature of canonical. A malformed
// encoding, a wrong length or a bad public key simply fails.
func VerifyRL2(pub ed25519.PublicKey, canonical, signature string) bool {
	if len(pub) != PublicKeySize {
		return false
	}
	sig, err := DecodeB64U(signature)
	if err != nil || len(sig) != SignatureSize {
		return false
	}
	return ed25519.Verify(pub, []byte(canonical), sig)
}

// CertificatePin returns the pin of a certificate: b64u(SHA-256(DER of its
// SubjectPublicKeyInfo)), the "k" of the pairing link.
func CertificatePin(spkiDER []byte) string {
	sum := sha256.Sum256(spkiDER)
	return EncodeB64U(sum[:])
}

// PairingLink returns the link of the QR code (docs/specs/03-protocole.md
// §3.1, M13):
//
//	reminislink://pair?v=2&id=<deviceId>&n=<name>&s=<b64u secret>&h=<IPv4>&p=<port>&k=<pin>
//
// The name is percent-encoded per RFC 3986 (a space is %20).
func PairingLink(deviceID, name string, secret []byte, host string, port int, pin string) string {
	return fmt.Sprintf("%s://pair?v=%d&id=%s&n=%s&s=%s&h=%s&p=%d&k=%s",
		PairingURLScheme, PairingLinkVersion, EncodeRFC3986(deviceID), EncodeRFC3986(name), EncodeB64U(secret),
		EncodeRFC3986(host), port, EncodeRFC3986(pin))
}
