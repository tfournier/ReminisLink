package auth

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readVectors(t *testing.T, name string, v any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "protocol", "test-vectors", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

type pairingVectorFile struct {
	Cases []struct {
		Name               string `json:"name"`
		Secret             string `json:"secret"`
		SecretHex          string `json:"secretHex"`
		DeviceID           string `json:"deviceId"`
		ClientID           string `json:"clientId"`
		ClientSeed         string `json:"clientSeed"`
		ClientPublicKey    string `json:"clientPublicKey"`
		ClientNonce        string `json:"clientNonce"`
		DeviceNonce        string `json:"deviceNonce"`
		ClientProofMessage string `json:"clientProofMessage"`
		ClientProof        string `json:"clientProof"`
		DeviceProofMessage string `json:"deviceProofMessage"`
		DeviceProof        string `json:"deviceProof"`
	} `json:"cases"`
	Links []struct {
		Name       string `json:"name"`
		DeviceID   string `json:"deviceId"`
		DeviceName string `json:"deviceName"`
		Secret     string `json:"secret"`
		Host       string `json:"host"`
		Port       int    `json:"port"`
		Pin        string `json:"pin"`
		Link       string `json:"link"`
	} `json:"links"`
}

func mustB64U(t *testing.T, s string) []byte {
	t.Helper()
	b, err := DecodeB64U(s)
	if err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return b
}

func TestPairingVectors(t *testing.T) {
	var f pairingVectorFile
	readVectors(t, "pairing.json", &f)
	if len(f.Cases) < 3 || len(f.Links) < 2 {
		t.Fatalf("need at least 3 cases and 2 links, got %d and %d", len(f.Cases), len(f.Links))
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			secret := mustB64U(t, c.Secret)
			if hex.EncodeToString(secret) != c.SecretHex || len(secret) != SecretSize {
				t.Fatal("secret mismatch")
			}
			priv := ed25519.NewKeyFromSeed(mustB64U(t, c.ClientSeed))
			if EncodeB64U(priv.Public().(ed25519.PublicKey)) != c.ClientPublicKey {
				t.Fatal("public key does not match the seed")
			}
			if _, err := DecodePublicKey(c.ClientPublicKey); err != nil {
				t.Fatal(err)
			}
			if got := PairClientMessage(c.DeviceID, c.ClientID, c.ClientPublicKey, c.ClientNonce); got != c.ClientProofMessage {
				t.Fatalf("client proof message = %q", got)
			}
			if got := PairClientProof(secret, c.DeviceID, c.ClientID, c.ClientPublicKey, c.ClientNonce); got != c.ClientProof {
				t.Fatalf("client proof = %q, want %q", got, c.ClientProof)
			}
			if !VerifyPairClientProof(secret, c.DeviceID, c.ClientID, c.ClientPublicKey, c.ClientNonce, c.ClientProof) {
				t.Fatal("VerifyPairClientProof rejected the vector")
			}
			if got := PairDeviceMessage(c.DeviceID, c.ClientID, c.ClientPublicKey, c.ClientNonce, c.DeviceNonce); got != c.DeviceProofMessage {
				t.Fatalf("device proof message = %q", got)
			}
			if got := PairDeviceProof(secret, c.DeviceID, c.ClientID, c.ClientPublicKey, c.ClientNonce, c.DeviceNonce); got != c.DeviceProof {
				t.Fatalf("device proof = %q, want %q", got, c.DeviceProof)
			}
			if !VerifyPairDeviceProof(secret, c.DeviceID, c.ClientID, c.ClientPublicKey, c.ClientNonce, c.DeviceNonce, c.DeviceProof) {
				t.Fatal("VerifyPairDeviceProof rejected the vector")
			}
			// Independent HMAC.
			h := hmac.New(sha256.New, secret)
			h.Write([]byte(c.ClientProofMessage))
			if EncodeB64U(h.Sum(nil)) != c.ClientProof {
				t.Fatal("client proof is not HMAC-SHA256(secret, message)")
			}
		})
	}
	for _, l := range f.Links {
		if got := PairingLink(l.DeviceID, l.DeviceName, mustB64U(t, l.Secret), l.Host, l.Port, l.Pin); got != l.Link {
			t.Errorf("%s: link = %q, want %q", l.Name, got, l.Link)
		}
	}
}

type signingVectorFile struct {
	Cases []struct {
		Name             string            `json:"name"`
		Seed             string            `json:"seed"`
		PublicKey        string            `json:"publicKey"`
		ClientID         string            `json:"clientId"`
		Method           string            `json:"method"`
		Path             string            `json:"path"`
		Query            string            `json:"query"`
		Time             string            `json:"time"`
		Nonce            string            `json:"nonce"`
		Body             string            `json:"body"`
		ContentSHA256    string            `json:"contentSha256"`
		CanonicalQuery   string            `json:"canonicalQuery"`
		CanonicalRequest string            `json:"canonicalRequest"`
		Signature        string            `json:"signature"`
		Headers          map[string]string `json:"headers"`
	} `json:"cases"`
	InvalidQueries []struct {
		Name  string `json:"name"`
		Query string `json:"query"`
	} `json:"invalidQueries"`
}

func TestSigningVectors(t *testing.T) {
	var f signingVectorFile
	readVectors(t, "signing.json", &f)
	if len(f.Cases) < 8 {
		t.Fatalf("need at least 8 signing cases, got %d", len(f.Cases))
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			priv := ed25519.NewKeyFromSeed(mustB64U(t, c.Seed))
			pub, err := DecodePublicKey(c.PublicKey)
			if err != nil || !pub.Equal(priv.Public()) {
				t.Fatal("public key does not match the seed")
			}
			if c.ContentSHA256 != UnsignedPayload && ContentSHA256([]byte(c.Body)) != c.ContentSHA256 {
				t.Fatal("body hash mismatch")
			}
			cq, err := CanonicalQuery(c.Query)
			if err != nil {
				t.Fatal(err)
			}
			if cq != c.CanonicalQuery {
				t.Fatalf("canonical query = %q, want %q", cq, c.CanonicalQuery)
			}
			canonical, err := CanonicalRequestRL2(RequestParts{
				Method: c.Method, Path: c.Path, RawQuery: c.Query,
				Time: c.Time, Nonce: c.Nonce, ContentSHA256: c.ContentSHA256,
			})
			if err != nil {
				t.Fatal(err)
			}
			if canonical != c.CanonicalRequest {
				t.Fatalf("canonical request =\n%s\nwant\n%s", canonical, c.CanonicalRequest)
			}
			// Ed25519 is deterministic: the same signature.
			if got := SignRL2(priv, canonical); got != c.Signature {
				t.Fatalf("signature = %s, want %s", got, c.Signature)
			}
			if !VerifyRL2(pub, canonical, c.Signature) {
				t.Fatal("VerifyRL2 rejected the vector")
			}
			if len(mustB64U(t, c.Signature)) != SignatureSize {
				t.Fatal("signature is not 64 bytes")
			}
			want := map[string]string{
				HeaderClient: c.ClientID, HeaderTime: c.Time, HeaderNonce: c.Nonce,
				HeaderContentSHA256: c.ContentSHA256, HeaderSignature: c.Signature,
			}
			if len(c.Headers) != len(want) {
				t.Fatalf("headers = %v", c.Headers)
			}
			for k, v := range want {
				if c.Headers[k] != v {
					t.Fatalf("header %s = %q, want %q", k, c.Headers[k], v)
				}
			}
		})
	}
	for _, q := range f.InvalidQueries {
		if _, err := CanonicalQuery(q.Query); err == nil {
			t.Errorf("%s: %q accepted", q.Name, q.Query)
		}
	}
}

type tlsVectorFile struct {
	PrivateKeyHex    string `json:"privateKeyHex"`
	PublicKeyX963Hex string `json:"publicKeyX963Hex"`
	SPKIDERHex       string `json:"spkiDerHex"`
	CertificatePEM   string `json:"certificatePem"`
	Pin              string `json:"pin"`
	PinStdBase64     string `json:"pinStdBase64"`
}

func TestTLSPinVectors(t *testing.T) {
	var f tlsVectorFile
	readVectors(t, "tls.json", &f)
	block, _ := pem.Decode([]byte(f.CertificatePEM))
	if block == nil {
		t.Fatal("bad PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(cert.RawSubjectPublicKeyInfo) != f.SPKIDERHex {
		t.Fatal("SPKI mismatch")
	}
	if !strings.HasSuffix(f.SPKIDERHex, f.PublicKeyX963Hex) {
		t.Fatal("the SPKI does not end with the X9.63 point")
	}
	if got := CertificatePin(cert.RawSubjectPublicKeyInfo); got != f.Pin {
		t.Fatalf("pin = %s, want %s", got, f.Pin)
	}
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	if base64.StdEncoding.EncodeToString(sum[:]) != f.PinStdBase64 {
		t.Fatal("standard base64 pin mismatch")
	}
}
