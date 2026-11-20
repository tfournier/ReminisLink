package auth

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
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

// keyedClients serves fixed public keys by client id.
type keyedClients map[string][]byte

func (k keyedClients) Lookup(id string) (Client, bool) {
	key, ok := k[id]
	return Client{ID: id, PublicKey: key}, ok
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
				Clients: keyedClients{c.ClientID: mustB64U(t, c.PublicKey)},
				Nonces:  NewNonceCache(NonceTTL, 10, nil),
				Now:     func() time.Time { return time.Unix(ts, 0) },
			}
			var gotBody string
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				gotBody = string(b)
				w.WriteHeader(http.StatusNoContent)
			})
			mux := http.NewServeMux()
			mux.Handle("PUT /api/v1/uploads/{uploadId}", v.Require(RouteChunk, h))
			mux.Handle("/", v.Require(RouteJSON, h))
			srv := httptest.NewServer(mux)
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
			// The pak refuses repeated query keys after checking the
			// signature (03 §4.2): 400 invalid_request, not 401, proves
			// the vector's signature was accepted.
			if dup, _ := HasDuplicateQueryKey(c.Query); dup {
				if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), CodeInvalidRequest) {
					t.Fatalf("duplicate keys: status %d: %s", resp.StatusCode, body)
				}
				return
			}
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("status %d: %s", resp.StatusCode, body)
			}
			if gotBody != c.Body {
				t.Fatalf("handler body = %q", gotBody)
			}
		})
	}
}

// A canonical string written by hand from 03-protocole §4.2, and its
// signature checked directly with crypto/ed25519.
func TestCanonicalRequestHandWritten(t *testing.T) {
	nonce := EncodeB64U(bytes.Repeat([]byte{0xab}, 16))
	body := `{"label":"Avant"}`
	sum := sha256.Sum256([]byte(body))
	bodyHash := hex.EncodeToString(sum[:])
	want := "RL2-ED25519\n" +
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
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x11}, 32))
	sig, err := DecodeB64U(Sign(priv, got))
	if err != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(priv.Public().(ed25519.PublicKey), []byte(want), sig) {
		t.Fatal("Sign is not Ed25519 of the canonical string")
	}
}
