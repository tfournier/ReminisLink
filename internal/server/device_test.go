package server

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/device"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

func TestHello(t *testing.T) {
	f := newFixture(t)
	r := f.unsigned("GET", "/api/v1/hello", nil)
	f.check("GET", "/hello", r)
	var h helloResponse
	r.JSON(t, &h)
	if r.Status != 200 || h.DeviceID != testDeviceID || h.Model != "brick" || h.Platform != "tg5040" || h.OS != "nextui" ||
		h.OSVersion != nil || h.APIVersion != 1 || h.MinClientVersion != MinClientVersion || !h.PairingOpen ||
		!h.ServerTime.Equal(f.clock.Now()) || h.PakVersion != "1.0.0-test" {
		t.Errorf("hello = %d %+v", r.Status, h)
	}
	if !strings.Contains(string(r.Body), `"osVersion":null`) {
		t.Errorf("osVersion must be null: %s", r.Body)
	}
}

func TestHelloRateLimit(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < HelloBurst; i++ {
		if r := f.unsigned("GET", "/api/v1/hello", nil); r.Status != 200 {
			t.Fatalf("request %d: %d", i, r.Status)
		}
	}
	r := f.unsigned("GET", "/api/v1/hello", nil)
	f.check("GET", "/hello", r)
	if r.Status != 429 || r.ErrorCode() != auth.CodeTooManyAttempts || r.Header.Get("Retry-After") != "1" {
		t.Fatalf("over the limit: %d %s Retry-After=%q", r.Status, r.Body, r.Header.Get("Retry-After"))
	}
	f.clock.Advance(time.Second)
	if r := f.unsigned("GET", "/api/v1/hello", nil); r.Status != 200 {
		t.Fatalf("after a second: %d", r.Status)
	}
}

func TestHelloPairingBlocked(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < auth.MaxPairingFailures; i++ {
		f.clock.Advance(3 * time.Second) // stay under the /pair rate limit
		r := f.pair(t, "qr", make([]byte, auth.SecretSize), "iPhone")
		if r.Status != 401 {
			t.Fatalf("failure %d: %d %s", i, r.Status, r.Body)
		}
	}
	var h helloResponse
	r := f.unsigned("GET", "/api/v1/hello", nil)
	r.JSON(t, &h)
	if h.PairingOpen {
		t.Error("pairingOpen while blocked")
	}
}

func TestPairingClosed(t *testing.T) {
	f := newFixture(t)
	if err := f.pairing.Close(); err != nil {
		t.Fatal(err)
	}
	var h helloResponse
	r := f.unsigned("GET", "/api/v1/hello", nil)
	f.check("GET", "/hello", r)
	r.JSON(t, &h)
	if r.Status != 200 || h.PairingOpen {
		t.Fatalf("hello while pairing is closed: %d %s", r.Status, r.Body)
	}
	// Even a proof made with the hidden secret fails, and the failures are
	// not counted: no block after many attempts.
	hidden, _ := f.pairing.Snapshot()
	for i := 0; i < 2*auth.MaxPairingFailures; i++ {
		f.clock.Advance(3 * time.Second) // stay under the /pair rate limit
		r := f.pair(t, "qr", hidden.Secret, "iPhone")
		if r.Status != 401 || r.ErrorCode() != CodePairingFailed {
			t.Fatalf("attempt %d while closed: %d %s", i, r.Status, r.Body)
		}
	}
	if _, ok := f.clients.Lookup("3b1c8d2a-5e6f-4a7b-8c9d-0e1f2a3b4c5d"); ok {
		t.Fatal("paired while pairing is closed")
	}
	// Opened again (new secret), pairing works at once: nothing blocked.
	if err := f.pairing.Open(); err != nil {
		t.Fatal(err)
	}
	snap, _ := f.pairing.Snapshot()
	if r := f.pair(t, "pin", auth.PINIKM(snap.PIN), "iPhone"); r.Status != 200 {
		t.Fatalf("pair once opened: %d %s", r.Status, r.Body)
	}
}

// pair sends POST /pair with a proof made from ikm and returns the
// response with the client nonce and id used.
func (f *fixture) pair(t *testing.T, method string, ikm []byte, name string) testutil.Response {
	t.Helper()
	r, _, _ := f.pairWith(t, method, ikm, name)
	return r
}

func (f *fixture) pairWith(t *testing.T, method string, ikm []byte, name string) (testutil.Response, string, []byte) {
	t.Helper()
	clientID := "3b1c8d2a-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	nonce := make([]byte, auth.PairingNonceSize)
	_, _ = rand.Read(nonce)
	nonceB64 := auth.EncodeB64U(nonce)
	body, _ := json.Marshal(map[string]string{
		"method": method, "clientId": clientID, "clientName": name, "clientNonce": nonceB64,
		"proof": auth.ClientProof(ikm, testDeviceID, clientID, nonceB64),
	})
	r := f.unsigned("POST", "/api/v1/pair", body)
	f.check("POST", "/pair", r)
	return r, clientID, nonce
}

func TestPairQRThenSignedRequest(t *testing.T) {
	f := newFixture(t)
	var paired []auth.Client
	f.server.opts.OnPaired = func(c auth.Client) { paired = append(paired, c) }
	snap, err := f.pairing.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	r, clientID, clientNonce := f.pairWith(t, "qr", snap.Secret, "iPhone de Thomas")
	if r.Status != 200 {
		t.Fatalf("pair: %d %s", r.Status, r.Body)
	}
	var resp pairResponse
	r.JSON(t, &resp)
	deviceNonce, err := auth.DecodeB64U(resp.DeviceNonce)
	if err != nil {
		t.Fatal(err)
	}
	key, err := auth.DeriveKey(snap.Secret, clientNonce, deviceNonce, testDeviceID, clientID)
	if err != nil {
		t.Fatal(err)
	}
	if resp.DeviceID != testDeviceID || resp.DeviceName != "Brick de test" || !auth.VerifyDeviceProof(key, testDeviceID, clientID, resp.Proof) {
		t.Fatalf("pair response = %+v", resp)
	}
	stored, ok := f.clients.Lookup(clientID)
	if !ok || stored.Name != "iPhone de Thomas" || string(stored.Key) != string(key) {
		t.Fatalf("stored client = %+v", stored)
	}
	if len(paired) != 1 || paired[0].ID != clientID {
		t.Errorf("OnPaired = %+v", paired)
	}
	// The secret is single use.
	if again := f.pair(t, "qr", snap.Secret, "iPhone"); again.Status != 401 || again.ErrorCode() != CodePairingFailed {
		t.Errorf("secret reused: %d %s", again.Status, again.Body)
	}
	// The derived key signs requests.
	c := &testutil.Client{BaseURL: f.http.URL, ClientID: clientID, Key: key, Now: f.clock.Now}
	if r := c.Do(t, "GET", "/api/v1/systems", nil); r.Status != 200 {
		t.Fatalf("signed request with the derived key: %d %s", r.Status, r.Body)
	}
	// Nothing secret reaches the log.
	logs := f.logs.String()
	for _, secret := range []string{auth.EncodeB64U(snap.Secret), snap.PIN, auth.EncodeB64U(key), resp.Proof, resp.DeviceNonce} {
		if strings.Contains(logs, secret) {
			t.Errorf("log contains a secret %q:\n%s", secret, logs)
		}
	}
	if !strings.Contains(logs, "client paired") {
		t.Errorf("pairing not logged:\n%s", logs)
	}
}

func TestPairPIN(t *testing.T) {
	f := newFixture(t)
	snap, _ := f.pairing.Snapshot()
	if r := f.pair(t, "pin", auth.PINIKM(snap.PIN), "iPad"); r.Status != 200 {
		t.Fatalf("pin pairing: %d %s", r.Status, r.Body)
	}
}

func TestPairErrors(t *testing.T) {
	f := newFixture(t)
	valid := func(mod func(map[string]any)) []byte {
		m := map[string]any{
			"method": "qr", "clientId": "3b1c8d2a-5e6f-4a7b-8c9d-0e1f2a3b4c5d", "clientName": "iPhone",
			"clientNonce": auth.EncodeB64U(make([]byte, 32)), "proof": auth.EncodeB64U(make([]byte, 32)),
		}
		mod(m)
		b, _ := json.Marshal(m)
		return b
	}
	for name, tc := range map[string]struct {
		body   []byte
		status int
		code   string
	}{
		"not JSON":          {[]byte("{"), 400, auth.CodeInvalidRequest},
		"trailing data":     {append(valid(func(map[string]any) {}), []byte(" {}")...), 400, auth.CodeInvalidRequest},
		"missing field":     {valid(func(m map[string]any) { delete(m, "proof") }), 400, auth.CodeInvalidRequest},
		"wrong field type":  {valid(func(m map[string]any) { m["clientName"] = 3 }), 400, auth.CodeInvalidRequest},
		"empty name":        {valid(func(m map[string]any) { m["clientName"] = "" }), 400, auth.CodeInvalidRequest},
		"blank name":        {valid(func(m map[string]any) { m["clientName"] = "   " }), 400, auth.CodeInvalidRequest},
		"name too long":     {valid(func(m map[string]any) { m["clientName"] = strings.Repeat("é", 65) }), 400, auth.CodeInvalidRequest},
		"control character": {valid(func(m map[string]any) { m["clientName"] = "a\nb" }), 400, auth.CodeInvalidRequest},
		"bad method":        {valid(func(m map[string]any) { m["method"] = "nfc" }), 400, auth.CodeInvalidRequest},
		"bad client id":     {valid(func(m map[string]any) { m["clientId"] = "ABC" }), 400, auth.CodeInvalidRequest},
		"short nonce":       {valid(func(m map[string]any) { m["clientNonce"] = "AAAA" }), 400, auth.CodeInvalidRequest},
		"wrong proof":       {valid(func(map[string]any) {}), 401, CodePairingFailed},
		"body too large":    {valid(func(m map[string]any) { m["clientName"] = strings.Repeat("x", MaxJSONBody) }), 413, auth.CodeTooLarge},
	} {
		f.clock.Advance(3 * time.Second) // stay under the /pair rate limit and the failure block
		r := f.unsigned("POST", "/api/v1/pair", tc.body)
		f.check("POST", "/pair", r)
		if r.Status != tc.status || r.ErrorCode() != tc.code {
			t.Errorf("%s: %d %s, want %d %s", name, r.Status, r.ErrorCode(), tc.status, tc.code)
		}
		if tc.code == CodePairingFailed {
			f.clock.Advance(auth.PairingBlock) // never reach the block
		}
	}
}

func TestPairBlockedAfterFailures(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < auth.MaxPairingFailures; i++ {
		f.clock.Advance(3 * time.Second)
		if r := f.pair(t, "pin", []byte("000000x"), "iPhone"); r.Status != 401 {
			t.Fatalf("failure %d: %d", i, r.Status)
		}
	}
	f.clock.Advance(3 * time.Second)
	snap, _ := f.pairing.Snapshot()
	r := f.pair(t, "qr", snap.Secret, "iPhone")
	if r.Status != 429 || r.ErrorCode() != auth.CodeTooManyAttempts || r.Header.Get("Retry-After") != "27" {
		t.Fatalf("blocked: %d %s Retry-After=%q", r.Status, r.Body, r.Header.Get("Retry-After"))
	}
	f.clock.Advance(auth.PairingBlock)
	snap, _ = f.pairing.Snapshot()
	if r := f.pair(t, "qr", snap.Secret, "iPhone"); r.Status != 200 {
		t.Fatalf("after the block: %d %s", r.Status, r.Body)
	}
}

func TestPairRateLimit(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < PairBurst; i++ {
		f.unsigned("POST", "/api/v1/pair", []byte("{"))
	}
	r := f.unsigned("POST", "/api/v1/pair", []byte("{"))
	f.check("POST", "/pair", r)
	if r.Status != 429 || r.ErrorCode() != auth.CodeTooManyAttempts || r.Header.Get("Retry-After") != "2" {
		t.Fatalf("over the limit: %d %s Retry-After=%q", r.Status, r.Body, r.Header.Get("Retry-After"))
	}
}

func TestUnpair(t *testing.T) {
	f := newFixture(t)
	r := f.client.Do(t, "DELETE", "/api/v1/clients/me", nil)
	f.check("DELETE", "/clients/me", r)
	if r.Status != 204 {
		t.Fatalf("unpair: %d %s", r.Status, r.Body)
	}
	if _, ok := f.clients.Lookup(f.client.ClientID); ok {
		t.Fatal("client still stored")
	}
	r = f.client.Do(t, "GET", "/api/v1/device", nil)
	f.check("GET", "/device", r)
	if r.Status != 401 || r.ErrorCode() != auth.CodeUnknownClient {
		t.Fatalf("after unpair: %d %s", r.Status, r.Body)
	}
}

func TestDevice(t *testing.T) {
	var asked string
	f := newFixture(t, func(o *Options) {
		o.IPv4 = func(iface string) (string, bool) { asked = iface; return "192.168.1.42", true }
	})
	f.clock.Advance(90 * time.Second)
	r := f.get("/api/v1/device", "/device", 200)
	var d deviceResponse
	r.JSON(t, &d)
	if d.DeviceID != testDeviceID || d.Model != "brick" || d.Network.IPv4 != "192.168.1.42" || d.Network.SSID != nil ||
		d.Battery != nil || d.UptimeSeconds != 90 || len(d.Storage) != 1 || d.Storage[0] != (storageVolume{"sd", "SD", 1 << 41, 1 << 40}) {
		t.Errorf("device = %+v", d)
	}
	if asked != "wlan0" {
		t.Errorf("IPv4 asked for interface %q, want the adapter's wlan0", asked)
	}
	for _, null := range []string{`"ssid":null`, `"battery":null`, `"osVersion":null`} {
		if !strings.Contains(string(r.Body), null) {
			t.Errorf("missing %s in %s", null, r.Body)
		}
	}
}

func TestDeviceWithoutNetworkNorStorage(t *testing.T) {
	f := newFixture(t, func(o *Options) {
		o.IPv4 = func(string) (string, bool) { return "", false }
		o.Storage = func(string) (device.Storage, error) { return device.Storage{}, fmt.Errorf("no statfs") }
	})
	r := f.get("/api/v1/device", "/device", 200)
	var d deviceResponse
	r.JSON(t, &d)
	if d.Network.IPv4 != unknownIPv4 || len(d.Storage) != 0 {
		t.Errorf("device = %+v", d)
	}
}

func TestSignedRoutesRefuseBadRequests(t *testing.T) {
	f := newFixture(t)
	routes := []struct{ method, target, path string }{
		{"DELETE", "/api/v1/clients/me", "/clients/me"},
		{"GET", "/api/v1/device", "/device"},
		{"GET", "/api/v1/systems", "/systems"},
		{"GET", "/api/v1/systems/R2FtZQ/games", "/systems/{systemId}/games"},
		{"GET", "/api/v1/games/R2FtZQ", "/games/{gameId}"},
		{"GET", "/api/v1/games/R2FtZQ/art", "/games/{gameId}/art"},
	}
	for _, rt := range routes {
		// No signature at all.
		r := f.unsigned(rt.method, rt.target, nil)
		f.check(rt.method, rt.path, r)
		if r.Status != 401 || r.ErrorCode() != auth.CodeUnknownClient {
			t.Errorf("%s %s unsigned: %d %s", rt.method, rt.target, r.Status, r.Body)
		}
		// Unknown client.
		other := &testutil.Client{BaseURL: f.http.URL, ClientID: "00000000-0000-4000-8000-000000000000", Key: f.client.Key, Now: f.clock.Now}
		r = other.Do(t, rt.method, rt.target, nil)
		f.check(rt.method, rt.path, r)
		if r.Status != 401 || r.ErrorCode() != auth.CodeUnknownClient {
			t.Errorf("%s %s unknown client: %d %s", rt.method, rt.target, r.Status, r.Body)
		}
		// Bad signature.
		req := f.client.Request(t, rt.method, rt.target, nil)
		req.Header.Set(auth.HeaderSignature, auth.EncodeB64U(make([]byte, 32)))
		r = f.client.Send(t, req)
		f.check(rt.method, rt.path, r)
		if r.Status != 401 || r.ErrorCode() != auth.CodeBadSignature {
			t.Errorf("%s %s bad signature: %d %s", rt.method, rt.target, r.Status, r.Body)
		}
		// Duplicate query key (03 §4.2).
		r = f.client.Do(t, rt.method, rt.target+"?x=1&x=2", nil)
		f.check(rt.method, rt.path, r)
		if r.Status != 400 || r.ErrorCode() != auth.CodeInvalidRequest {
			t.Errorf("%s %s duplicate key: %d %s", rt.method, rt.target, r.Status, r.Body)
		}
		// Clock skew.
		late := &testutil.Client{BaseURL: f.http.URL, ClientID: f.client.ClientID, Key: f.client.Key,
			Now: func() time.Time { return f.clock.Now().Add(-auth.MaxClockSkew - time.Second) }}
		r = late.Do(t, rt.method, rt.target, nil)
		f.check(rt.method, rt.path, r)
		if r.Status != 401 || r.ErrorCode() != auth.CodeClockSkew {
			t.Errorf("%s %s clock skew: %d %s", rt.method, rt.target, r.Status, r.Body)
		}
	}
	// A replayed request.
	req := f.client.Request(t, "GET", "/api/v1/systems", nil)
	if r := f.client.Send(t, req); r.Status != 200 {
		t.Fatalf("first: %d", r.Status)
	}
	replay, _ := http.NewRequest("GET", req.URL.String(), nil)
	replay.Header = req.Header.Clone()
	r := f.client.Send(t, replay)
	f.check("GET", "/systems", r)
	if r.Status != 401 || r.ErrorCode() != auth.CodeReplayedRequest {
		t.Errorf("replay: %d %s", r.Status, r.Body)
	}
}

func TestUnknownRoute(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct{ method, target string }{
		{"GET", "/api/v1/nope"},
		{"GET", "/"},
		{"POST", "/api/v1/systems"},
		{"GET", "/api/v2/hello"},
	} {
		r := f.unsigned(tc.method, tc.target, nil)
		if r.Status != 404 || r.ErrorCode() != CodeNotFound {
			t.Errorf("%s %s: %d %s", tc.method, tc.target, r.Status, r.Body)
		}
	}
}

// TestHelloOSVersion checks decision D4: osVersion is the NextUI release of
// .system/version.txt, as NextUI shows it.
func TestHelloOSVersion(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(filepath.Join(f.sd, ".system", "version.txt"), []byte("NextUI-20261013-0\n39a06de\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := f.unsigned("GET", "/api/v1/hello", nil)
	f.check("GET", "/hello", r)
	var h helloResponse
	r.JSON(t, &h)
	if h.OSVersion == nil || *h.OSVersion != "NextUI-20261013-039a06de" {
		t.Fatalf("osVersion = %s", r.Body)
	}
	r = f.client.Do(t, "GET", "/api/v1/device", nil)
	f.check("GET", "/device", r)
	if !strings.Contains(string(r.Body), `"osVersion":"NextUI-20261013-039a06de"`) {
		t.Fatalf("device = %s", r.Body)
	}
}
