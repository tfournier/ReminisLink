package server

import (
	"bufio"
	"crypto/ed25519"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

// pairNamed adds a paired client with this name.
func (f *fixture) pairNamed(name string) *testutil.Client {
	f.t.Helper()
	c := testutil.PairClient(f.t, f.clients, f.http.URL)
	f.rename(c, name)
	c.Now = f.clock.Now
	return c
}

func (f *fixture) rename(c *testutil.Client, name string) {
	f.t.Helper()
	stored, ok := f.clients.Lookup(c.ClientID)
	if !ok {
		f.t.Fatal("client not stored")
	}
	stored.Name = name
	if err := f.clients.Put(stored); err != nil {
		f.t.Fatal(err)
	}
}

// as sends a signed request from c, validates it against the contract and
// checks its status.
func (f *fixture) as(c *testutil.Client, method, target, path string, body []byte, want int) testutil.Response {
	f.t.Helper()
	r := c.Do(f.t, method, target, body)
	f.check(method, path, r)
	if r.Status != want {
		f.t.Fatalf("%s %s: status %d, want %d: %s", method, target, r.Status, want, r.Body)
	}
	return r
}

// snapshot lists every file of the SD card with its size and mtime,
// except the client store (changed by Forget).
func (f *fixture) snapshot() []string {
	f.t.Helper()
	var out []string
	err := filepath.WalkDir(f.sd, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), "clients.json") || (d.IsDir() && p == filepath.Dir(f.layout.ClientsFile())) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, fmt.Sprintf("%s %d %d %v", p, info.Size(), info.ModTime().UnixNano(), d.IsDir()))
		return nil
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func TestDisconnectByTheConsole(t *testing.T) {
	f := newFixture(t)
	f.rename(f.client, "iPhone A")
	b := f.pairNamed("iPhone B")
	g := gamePath(scPath)

	f.as(f.client, "GET", "/api/v1/systems", "/systems", nil, 200)
	f.as(b, "GET", "/api/v1/systems", "/systems", nil, 200)
	if err := f.server.Disconnect(f.client.ClientID); err != nil {
		t.Fatal(err)
	}
	if err := f.server.Disconnect("00000000-0000-4000-8000-000000000000"); err != ErrUnknownClient {
		t.Fatalf("unknown client: %v", err)
	}
	// Every signed route answers 401 disconnected, reads, writes and
	// DELETE /clients/me included.
	for _, req := range []struct{ method, target, path string }{
		{"GET", "/api/v1/systems", "/systems"},
		{"POST", g + "/save/backups", "/games/{gameId}/save/backups"},
		{"DELETE", "/api/v1/clients/me", "/clients/me"},
	} {
		wantCode(t, f.as(f.client, req.method, req.target, req.path, nil, 401), CodeDisconnected)
	}
	if _, ok := f.clients.Lookup(f.client.ClientID); !ok {
		t.Fatal("a disconnected client could unpair")
	}
	// It is no longer connected.
	if got := connected(t, f, b); len(got) != 1 || got[0].ClientName != "iPhone B" {
		t.Fatalf("connected: %+v", got)
	}
	devs := f.server.Devices()
	if len(devs) != 2 {
		t.Fatalf("devices %+v", devs)
	}
	for _, d := range devs {
		if d.ID == f.client.ClientID && (!d.Disconnected || d.Connected || d.Platform != auth.PlatformApple) {
			t.Errorf("A: %+v", d)
		}
		if d.ID == b.ClientID && (d.Disconnected || !d.Connected) {
			t.Errorf("B: %+v", d)
		}
	}
	// B writes as usual (no lease since M13).
	f.as(b, "POST", g+"/save/backups", "/games/{gameId}/save/backups", nil, http.StatusCreated)

	// POST /clients/me/connect lifts it, and the device is connected again
	// at once.
	f.as(f.client, "POST", "/api/v1/clients/me/connect", "/clients/me/connect", nil, 204)
	if got := f.server.ConnectedClients(); len(got) != 2 {
		t.Fatalf("connected right after connect: %+v", got)
	}
	f.as(f.client, "GET", "/api/v1/systems", "/systems", nil, 200)
	f.as(f.client, "POST", g+"/save/backups", "/games/{gameId}/save/backups", nil, http.StatusCreated)
	// No effect when not disconnected.
	f.as(f.client, "POST", "/api/v1/clients/me/connect", "/clients/me/connect", nil, 204)
	f.as(f.client, "GET", "/api/v1/systems", "/systems", nil, 200)
}

func TestForgetByTheConsole(t *testing.T) {
	f := newFixture(t)
	f.rename(f.client, "iPhone A")
	b := f.pairNamed("iPhone B")

	ok, err := f.server.Forget(f.client.ClientID)
	if err != nil || !ok {
		t.Fatalf("forget: %v %v", ok, err)
	}
	wantCode(t, f.as(f.client, "GET", "/api/v1/systems", "/systems", nil, 401), auth.CodeUnknownClient)
	wantCode(t, f.as(f.client, "POST", "/api/v1/clients/me/connect", "/clients/me/connect", nil, 401), auth.CodeUnknownClient)
	// Removed from clients.json.
	reopened, err := auth.OpenClientStore(f.layout.ClientsFile())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.Lookup(f.client.ClientID); ok {
		t.Fatal("still in clients.json")
	}
	if _, ok := reopened.Lookup(b.ClientID); !ok {
		t.Fatal("the other client was removed")
	}
	if ok, err := f.server.Forget(f.client.ClientID); ok || err != nil {
		t.Fatalf("forget twice: %v %v", ok, err)
	}

	// Forgetting drops the disconnection: paired again, it is accepted.
	if err := f.server.Disconnect(b.ClientID); err != nil {
		t.Fatal(err)
	}
	stored, _ := f.clients.Lookup(b.ClientID)
	if _, err := f.server.Forget(b.ClientID); err != nil {
		t.Fatal(err)
	}
	if err := f.clients.Put(stored); err != nil {
		t.Fatal(err)
	}
	f.as(b, "GET", "/api/v1/systems", "/systems", nil, 200)

	// Forget all.
	if n, err := f.server.ForgetAll(); n != 1 || err != nil {
		t.Fatalf("forget all: %d %v", n, err)
	}
	wantCode(t, f.as(b, "GET", "/api/v1/systems", "/systems", nil, 401), auth.CodeUnknownClient)
}

// rawHead returns the request line and the headers of a request signed by
// c, announcing body (not included).
func rawHead(t *testing.T, c *testutil.Client, method, target string, body []byte, unsigned bool) []byte {
	t.Helper()
	req := c.Request(t, method, target, body)
	if unsigned {
		req = c.RequestUnsigned(t, method, target, body)
		req.Header.Set("Upload-Offset", "0")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\nHost: x\r\nContent-Length: %d\r\n", method, target, len(body))
	for k, v := range req.Header {
		fmt.Fprintf(&b, "%s: %s\r\n", k, v[0])
	}
	b.WriteString("\r\n")
	return []byte(b.String())
}

// heldBody sends the headers of a request and keeps its body back. It
// returns the connection and a reader for the response.
func (f *fixture) heldBody(c *testutil.Client, method, target string, body []byte, unsigned bool) (net.Conn, *bufio.Reader) {
	f.t.Helper()
	conn := dial(f.t, f.http.Listener.Addr().String())
	f.t.Cleanup(func() { conn.Close() })
	if _, err := conn.Write(rawHead(f.t, c, method, target, body, unsigned)); err != nil {
		f.t.Fatal(err)
	}
	return conn, bufio.NewReader(conn)
}

func readResponse(t *testing.T, conn net.Conn, br *bufio.Reader) (*http.Response, testutil.Response) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("no response while the body is held back: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, testutil.Response{Status: resp.StatusCode, Header: resp.Header, Body: data}
}

// TestRejectedWithoutWaitingForTheBody: a 401 disconnected arrives while
// the client still holds its body back (a small body would otherwise be
// drained by net/http first), over a real connection.
func TestRejectedWithoutWaitingForTheBody(t *testing.T) {
	f := newFixture(t)
	b := f.pairNamed("iPhone B")
	g := gamePath(scPath)
	body := make([]byte, 100<<10) // under the 256 KiB net/http drains
	if err := f.server.Disconnect(b.ClientID); err != nil {
		t.Fatal(err)
	}
	for _, unsigned := range []bool{false, true} {
		target, path := g+"/save", "/games/{gameId}/save"
		if unsigned {
			target, path = "/api/v1/uploads/00000000-0000-4000-8000-000000000000", "/uploads/{uploadId}"
		}
		conn, br := f.heldBody(b, "PUT", target, body, unsigned)
		resp, r := readResponse(t, conn, br)
		f.check("PUT", path, r)
		if r.Status != 401 || r.ErrorCode() != CodeDisconnected || !resp.Close {
			t.Fatalf("unsigned=%v: %d %s close=%v", unsigned, r.Status, r.Body, resp.Close)
		}
	}
}

// gateReached makes the next gate decision signal the returned channel.
func (f *fixture) gateReached() <-chan struct{} {
	ch := make(chan struct{}, 1)
	fn := func() {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	f.server.gateHook.Store(&fn)
	return ch
}

// TestRevocationWhileTheBodyIsRead: a request admitted by the gate whose
// body is still coming is refused if the console disconnects or forgets
// its client in the meantime, and writes nothing. Deterministic: the body
// is sent only after the revocation.
func TestRevocationWhileTheBodyIsRead(t *testing.T) {
	g := gamePath(scPath)
	cases := []struct {
		name   string
		revoke func(f *fixture, b *testutil.Client)
		code   string
	}{
		{"disconnect", func(f *fixture, b *testutil.Client) { f.server.Disconnect(b.ClientID) }, CodeDisconnected},
		{"forget", func(f *fixture, b *testutil.Client) { f.server.Forget(b.ClientID) }, auth.CodeUnknownClient},
		{"forget all", func(f *fixture, b *testutil.Client) { f.server.ForgetAll() }, auth.CodeUnknownClient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			b := f.pairNamed("iPhone B")
			before := f.snapshot()
			body := []byte("new save")
			conn, br := f.waitGate(b, "PUT", g+"/save", body)
			tc.revoke(f, b)
			if _, err := conn.Write(body); err != nil {
				t.Fatal(err)
			}
			_, r := readResponse(t, conn, br)
			f.check("PUT", "/games/{gameId}/save", r)
			wantCode(t, r, tc.code)
			if !slices.Equal(before, f.snapshot()) {
				t.Fatal("the SD card changed")
			}
		})
	}
}

// waitGate sends the headers of a request of c with its body held back
// and returns once the gate admitted it.
func (f *fixture) waitGate(c *testutil.Client, method, target string, body []byte) (net.Conn, *bufio.Reader) {
	f.t.Helper()
	reached := f.gateReached()
	conn, br := f.heldBody(c, method, target, body, false)
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		f.t.Fatal("the gate was not reached")
	}
	f.server.gateHook.Store(nil)
	return conn, br
}

// Codex round 2, finding 3: a write admitted before a disconnection is
// refused even if the client reconnected before its body arrived; one
// admitted before a forget is refused even after pairing again with the
// same client id.
func TestRevokedThenRestoredWhileTheBodyIsRead(t *testing.T) {
	g := gamePath(scPath)
	for _, tc := range []struct {
		name    string
		restore func(f *fixture, b *testutil.Client)
		code    string
	}{
		{"disconnect then reconnect", func(f *fixture, b *testutil.Client) {
			f.server.Disconnect(b.ClientID)
			f.as(b, "POST", "/api/v1/clients/me/connect", "/clients/me/connect", nil, 204)
		}, CodeDisconnected},
		{"forget then pair again", func(f *fixture, b *testutil.Client) {
			stored, _ := f.clients.Lookup(b.ClientID)
			f.server.Forget(b.ClientID)
			if err := f.server.access.pair(stored); err != nil {
				t.Fatal(err)
			}
		}, auth.CodeUnknownClient},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			b := f.pairNamed("iPhone B")
			before := f.snapshot()
			body := []byte("new save")
			conn, br := f.waitGate(b, "PUT", g+"/save", body)
			tc.restore(f, b)
			conn.Write(body)
			_, r := readResponse(t, conn, br)
			f.check("PUT", "/games/{gameId}/save", r)
			wantCode(t, r, tc.code)
			if !slices.Equal(before, f.snapshot()) {
				t.Fatal("the revoked request wrote")
			}
			// Its next request is accepted.
			f.as(b, "PUT", g+"/save", "/games/{gameId}/save", body, 200)
		})
	}
}

// Codex round 2, finding 4: the other pre-body rejections answer while the
// body is held back too (bad signature, announced size over the limit).
func TestPreBodyRejectionsDoNotWait(t *testing.T) {
	f := newFixture(t)
	g := gamePath(scPath)
	forger := &testutil.Client{BaseURL: f.http.URL, ClientID: f.client.ClientID, Key: testutil.NewKey(t), Now: f.clock.Now}
	conn, br := f.heldBody(forger, "PUT", g+"/save", make([]byte, 100<<10), false)
	resp, r := readResponse(t, conn, br)
	if r.Status != 401 || r.ErrorCode() != auth.CodeBadSignature || !resp.Close {
		t.Fatalf("bad signature: %d %s close=%v", r.Status, r.Body, resp.Close)
	}
	conn, br = f.heldBody(f.client, "PUT", g+"/cheats", make([]byte, auth.BodyLimitJSON+1), false)
	resp, r = readResponse(t, conn, br)
	if r.Status != 413 || !resp.Close {
		t.Fatalf("too large: %d %s close=%v", r.Status, r.Body, resp.Close)
	}
}

// Codex round 2, finding 5: a request admitted before a disconnection does
// not put the device back among the connected clients.
func TestDisconnectedWhileTheBodyIsReadStaysOut(t *testing.T) {
	f := newFixture(t)
	b := f.pairNamed("iPhone B")
	body := []byte(`{"label":"x"}`)
	conn, br := f.waitGate(b, "POST", gamePath(scPath)+"/save/backups", body)
	f.server.Disconnect(b.ClientID)
	conn.Write(body)
	_, r := readResponse(t, conn, br)
	wantCode(t, r, CodeDisconnected)
	for _, c := range f.server.ConnectedClients() {
		if c.ClientName == "iPhone B" {
			t.Fatal("the disconnected device is listed as connected")
		}
	}
}

// Codex round 3, finding 1: the console forgets the device and it is
// paired again with the same id (new key) right after the verifier read
// the old key, before the gate: the request signed with the old key is
// refused and writes nothing.
func TestRepairedBetweenKeyLoadAndGate(t *testing.T) {
	f := newFixture(t)
	b := f.pairNamed("iPhone B")
	g := gamePath(scPath)
	before := f.snapshot()
	var once sync.Once
	hook := func(id string) {
		if id != b.ClientID {
			return
		}
		once.Do(func() {
			stored, _ := f.clients.Lookup(b.ClientID)
			if _, err := f.server.Forget(b.ClientID); err != nil {
				t.Error(err)
			}
			stored.PublicKey = testutil.NewKey(t).Public().(ed25519.PublicKey) // the new pairing's key
			if err := f.server.access.pair(stored); err != nil {
				t.Error(err)
			}
		})
	}
	f.server.access.lookupHook.Store(&hook)
	r := b.Do(t, "PUT", g+"/save", []byte("new save"))
	f.server.access.lookupHook.Store(nil)
	f.check("PUT", "/games/{gameId}/save", r)
	wantCode(t, r, auth.CodeUnknownClient)
	if !slices.Equal(before, f.snapshot()) {
		t.Fatal("the old pairing wrote")
	}
	// The old key is refused from now on.
	wantCode(t, b.Do(t, "GET", "/api/v1/systems", nil), auth.CodeBadSignature)
}

// Codex round 3, finding 2: a chunked body that goes over the limit and
// then stalls gets its 413 at once (no drain of the rest), over a real
// connection.
func TestTooLargeChunkedBodyThatStalls(t *testing.T) {
	f := newFixture(t)
	target := gamePath(scPath) + "/cheats"
	req := f.client.Request(t, "PUT", target, []byte(`{"cheats":[]}`))
	var head strings.Builder
	fmt.Fprintf(&head, "PUT %s HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n", target)
	for k, v := range req.Header {
		fmt.Fprintf(&head, "%s: %s\r\n", k, v[0])
	}
	head.WriteString("\r\n")
	conn := dial(t, f.http.Listener.Addr().String())
	defer conn.Close()
	chunk := make([]byte, auth.BodyLimitJSON+10)
	if _, err := fmt.Fprintf(conn, "%s%x\r\n%s\r\n", head.String(), len(chunk), chunk); err != nil {
		t.Fatal(err)
	}
	// No final chunk: the client stalls.
	resp, r := readResponse(t, conn, bufio.NewReader(conn))
	if r.Status != 413 || r.ErrorCode() != auth.CodeTooLarge || !resp.Close {
		t.Fatalf("got %d %s close=%v", r.Status, r.Body, resp.Close)
	}
}
