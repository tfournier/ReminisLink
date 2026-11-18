package auth

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// keyedClients serves fixed keys by client id.
type keyedClients map[string][]byte

func (k keyedClients) Lookup(id string) (Client, bool) {
	key, ok := k[id]
	return Client{ID: id, Key: key}, ok
}

// Every signing vector, sent as raw bytes on a TCP connection to a real
// server, must pass the middleware: this covers requestTarget (path and query
// exactly as on the request line), the lowercase method and UNSIGNED.
func TestSigningVectorsThroughHTTPServer(t *testing.T) {
	var f signingVectorFile
	readVectors(t, "signing.json", &f)
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			ts, _ := strconv.ParseInt(c.Time, 10, 64)
			v := &Verifier{
				Clients: keyedClients{c.ClientID: mustB64U(t, c.Key)},
				Nonces:  NewNonceCache(NonceTTL, 10, nil),
				Now:     func() time.Time { return time.Unix(ts, 0) },
			}
			var gotBody string
			srv := httptest.NewServer(v.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				gotBody = string(b)
				w.WriteHeader(http.StatusNoContent)
			})))
			defer srv.Close()

			target := c.Path
			if c.Query != "" {
				target += "?" + c.Query
			}
			var raw bytes.Buffer
			fmt.Fprintf(&raw, "%s %s HTTP/1.1\r\nHost: device\r\n", c.Method, target)
			for name, value := range c.Headers {
				fmt.Fprintf(&raw, "%s: %s\r\n", name, value)
			}
			fmt.Fprintf(&raw, "Content-Length: %d\r\nConnection: close\r\n\r\n%s", len(c.Body), c.Body)

			conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err := conn.Write(raw.Bytes()); err != nil {
				t.Fatal(err)
			}
			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("status %d: %s", resp.StatusCode, body)
			}
			if gotBody != c.Body {
				t.Fatalf("handler body = %q", gotBody)
			}
		})
	}
}

// hkdfOracle is a straight RFC 5869 implementation, independent of the
// code under test.
func hkdfOracle(ikm, salt, info []byte, length int) []byte {
	ext := hmac.New(sha256.New, salt)
	ext.Write(ikm)
	prk := ext.Sum(nil)
	var okm, prev []byte
	for i := byte(1); len(okm) < length; i++ {
		h := hmac.New(sha256.New, prk)
		h.Write(prev)
		h.Write(info)
		h.Write([]byte{i})
		prev = h.Sum(nil)
		okm = append(okm, prev...)
	}
	return okm[:length]
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The oracle reproduces RFC 5869 test case 1, then DeriveKey must agree
// with it on every pairing vector.
func TestDeriveKeyAgainstRFC5869Oracle(t *testing.T) {
	okm := hkdfOracle(
		mustHex(t, "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b"),
		mustHex(t, "000102030405060708090a0b0c"),
		mustHex(t, "f0f1f2f3f4f5f6f7f8f9"),
		42,
	)
	want := "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865"
	if hex.EncodeToString(okm) != want {
		t.Fatalf("oracle does not match RFC 5869 case 1: %x", okm)
	}

	var f pairingVectorFile
	readVectors(t, "pairing.json", &f)
	for _, c := range f.Cases {
		ikm := mustHex(t, c.IKMHex)
		cn, dn := mustB64U(t, c.ClientNonce), mustB64U(t, c.DeviceNonce)
		got, err := DeriveKey(ikm, cn, dn, c.DeviceID, c.ClientID)
		if err != nil {
			t.Fatal(err)
		}
		oracle := hkdfOracle(ikm, append(append([]byte{}, cn...), dn...), []byte("RL1-KEY|"+c.DeviceID+"|"+c.ClientID), 32)
		if !bytes.Equal(got, oracle) || EncodeB64U(oracle) != c.Key {
			t.Fatalf("%s: DeriveKey disagrees with the RFC 5869 oracle", c.Name)
		}
	}
}

// A canonical string written by hand from 03-protocole §4.2, and its HMAC
// computed directly with crypto/hmac.
func TestCanonicalRequestHandWritten(t *testing.T) {
	nonce := EncodeB64U(bytes.Repeat([]byte{0xab}, 16))
	body := `{"label":"Avant"}`
	sum := sha256.Sum256([]byte(body))
	bodyHash := hex.EncodeToString(sum[:])
	want := "RL1-HMAC-SHA256\n" +
		"POST\n" +
		"/api/v1/games/R2FtZQ/save/backups\n" +
		"a=1&a=2&b=x%20y&c=%2B&d=%C3%A9\n" +
		"1795539600\n" +
		nonce + "\n" +
		bodyHash
	got, err := CanonicalRequest(RequestParts{
		Method: "post", Path: "/api/v1/games/R2FtZQ/save/backups",
		RawQuery: "d=%c3%a9&c=%2B&b=x+y&a=2&a=1",
		Time:     "1795539600", Nonce: nonce, ContentSHA256: ContentSHA256([]byte(body)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("canonical =\n%q\nwant\n%q", got, want)
	}
	key := bytes.Repeat([]byte{0x11}, 32)
	h := hmac.New(sha256.New, key)
	h.Write([]byte(want))
	if Sign(key, got) != EncodeB64U(h.Sum(nil)) {
		t.Fatal("Sign is not HMAC-SHA256 of the canonical string")
	}
}
