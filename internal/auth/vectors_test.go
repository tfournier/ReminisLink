package auth

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
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
		Method             string `json:"method"`
		Secret             string `json:"secret"`
		PIN                string `json:"pin"`
		IKMHex             string `json:"ikmHex"`
		DeviceID           string `json:"deviceId"`
		ClientID           string `json:"clientId"`
		ClientNonce        string `json:"clientNonce"`
		DeviceNonce        string `json:"deviceNonce"`
		ClientProofMessage string `json:"clientProofMessage"`
		ClientProof        string `json:"clientProof"`
		SaltHex            string `json:"saltHex"`
		KeyInfo            string `json:"keyInfo"`
		Key                string `json:"key"`
		DeviceProofMessage string `json:"deviceProofMessage"`
		DeviceProof        string `json:"deviceProof"`
	} `json:"cases"`
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
	qr, pin := 0, 0
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var ikm []byte
			switch PairingMethod(c.Method) {
			case MethodQR:
				qr++
				ikm = mustB64U(t, c.Secret)
			case MethodPIN:
				pin++
				ikm = PINIKM(c.PIN)
			default:
				t.Fatalf("unknown method %q", c.Method)
			}
			if hex.EncodeToString(ikm) != c.IKMHex {
				t.Fatalf("ikm mismatch")
			}
			if got := ClientProofMessage(c.DeviceID, c.ClientID, c.ClientNonce); got != c.ClientProofMessage {
				t.Fatalf("client proof message = %q", got)
			}
			if got := ClientProof(ikm, c.DeviceID, c.ClientID, c.ClientNonce); got != c.ClientProof {
				t.Fatalf("client proof = %q, want %q", got, c.ClientProof)
			}
			if !VerifyClientProof(ikm, c.DeviceID, c.ClientID, c.ClientNonce, c.ClientProof) {
				t.Fatal("VerifyClientProof rejected the vector")
			}
			cn, dn := mustB64U(t, c.ClientNonce), mustB64U(t, c.DeviceNonce)
			if hex.EncodeToString(append(append([]byte{}, cn...), dn...)) != c.SaltHex {
				t.Fatal("salt mismatch")
			}
			if got := KeyInfo(c.DeviceID, c.ClientID); got != c.KeyInfo {
				t.Fatalf("key info = %q", got)
			}
			key, err := DeriveKey(ikm, cn, dn, c.DeviceID, c.ClientID)
			if err != nil {
				t.Fatal(err)
			}
			if EncodeB64U(key) != c.Key {
				t.Fatalf("key = %s, want %s", EncodeB64U(key), c.Key)
			}
			if got := DeviceProofMessage(c.DeviceID, c.ClientID); got != c.DeviceProofMessage {
				t.Fatalf("device proof message = %q", got)
			}
			if got := DeviceProof(key, c.DeviceID, c.ClientID); got != c.DeviceProof {
				t.Fatalf("device proof = %q, want %q", got, c.DeviceProof)
			}
			if !VerifyDeviceProof(key, c.DeviceID, c.ClientID, c.DeviceProof) {
				t.Fatal("VerifyDeviceProof rejected the vector")
			}
		})
	}
	if qr < 3 || pin < 2 {
		t.Fatalf("need at least 3 qr and 2 pin cases, got %d and %d", qr, pin)
	}
}

type signingVectorFile struct {
	Cases []struct {
		Name             string            `json:"name"`
		Key              string            `json:"key"`
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
			key := mustB64U(t, c.Key)
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
			canonical, err := CanonicalRequest(RequestParts{
				Method: c.Method, Path: c.Path, RawQuery: c.Query,
				Time: c.Time, Nonce: c.Nonce, ContentSHA256: c.ContentSHA256,
			})
			if err != nil {
				t.Fatal(err)
			}
			if canonical != c.CanonicalRequest {
				t.Fatalf("canonical request =\n%s\nwant\n%s", canonical, c.CanonicalRequest)
			}
			if got := Sign(key, canonical); got != c.Signature {
				t.Fatalf("signature = %s, want %s", got, c.Signature)
			}
			if !Verify(key, canonical, c.Signature) {
				t.Fatal("Verify rejected the vector")
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
