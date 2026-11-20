// Command drive plays a paired iPhone for "make screenshots"
// (scripts/screenshots/run.sh): "register <clients.json>" adds a client
// before the pak starts, "start" begins the upload of a 12 MiB ROM made of
// zeros (2 chunks of 3), "finish" sends the last chunk and completes it.
// It talks to the desktop pak on 127.0.0.1:47821. Development tool only.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/library"
)

// The fake client (its key is a constant: test data only).
const id = "3b1c8d2a-5e6f-4a7b-8c9d-0e1f2a3b4c5d"

var key = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))

// client talks to the local pak over TLS (M13). This screenshot tool only
// drives a pak on localhost: it does not check the certificate (the app
// and the smoke tests pin it).
var client = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}

func sign(req *http.Request, content string) {
	nonce := make([]byte, auth.RequestNonceSize)
	rand.Read(nonce)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	c, _ := auth.CanonicalRequest(auth.RequestParts{Method: req.Method, Path: req.URL.EscapedPath(), RawQuery: req.URL.RawQuery,
		Time: ts, Nonce: auth.EncodeB64U(nonce), ContentSHA256: content})
	req.Header.Set(auth.HeaderClient, id)
	req.Header.Set(auth.HeaderTime, ts)
	req.Header.Set(auth.HeaderNonce, auth.EncodeB64U(nonce))
	req.Header.Set(auth.HeaderContentSHA256, content)
	req.Header.Set(auth.HeaderSignature, auth.Sign(key, c))
}

func do(method, path string, body []byte, unsigned bool, hdr map[string]string) []byte {
	req, _ := http.NewRequest(method, "https://127.0.0.1:47821"+path, bytes.NewReader(body))
	if unsigned {
		sign(req, auth.UnsignedPayload)
	} else {
		sign(req, auth.ContentSHA256(body))
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if body != nil && !unsigned {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	fmt.Fprintln(os.Stderr, method, path, resp.StatusCode)
	return out
}

func main() {
	data := make([]byte, 12<<20)
	sum := sha256.Sum256(data)
	switch os.Args[1] {
	case "register":
		s, err := auth.OpenClientStore(os.Args[2])
		if err != nil {
			panic(err)
		}
		if err := s.Put(auth.Client{ID: id, Name: "iPhone", Platform: auth.PlatformApple,
			PublicKey: key.Public().(ed25519.PublicKey), PairedAt: time.Now()}); err != nil {
			panic(err)
		}
	case "start":
		body, _ := json.Marshal(map[string]any{"target": map[string]any{"kind": "rom", "systemId": library.EncodeID("Game Boy Advance (GBA)"), "fileName": "Sky Garden Quest DX.gba"},
			"sizeBytes": len(data), "sha256": hex.EncodeToString(sum[:])})
		var c struct {
			UploadID  string `json:"uploadId"`
			ChunkSize int    `json:"chunkSize"`
		}
		json.Unmarshal(do("POST", "/api/v1/uploads", body, false, nil), &c)
		for off := 0; off < 8<<20; off += c.ChunkSize {
			do("PUT", "/api/v1/uploads/"+c.UploadID, data[off:off+c.ChunkSize], true, map[string]string{"Upload-Offset": strconv.Itoa(off), "Content-Type": "application/octet-stream"})
		}
		os.WriteFile("/tmp/upload-id", []byte(c.UploadID), 0o644)
	case "finish":
		up, _ := os.ReadFile("/tmp/upload-id")
		do("PUT", "/api/v1/uploads/"+string(up), data[8<<20:], true, map[string]string{"Upload-Offset": strconv.Itoa(8 << 20), "Content-Type": "application/octet-stream"})
		do("POST", "/api/v1/uploads/"+string(up)+"/complete", nil, false, nil)
	}
}
