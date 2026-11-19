package server

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

// leaseExempt are the non-GET routes that never need the write lease
// (protocol/openapi.yaml "Single writer (M10)").
var leaseExempt = map[string]bool{
	"POST " + Prefix + "/pair":               true, // unsigned
	"DELETE " + Prefix + "/clients/me":       true,
	"PUT " + Prefix + "/writer":              true,
	"DELETE " + Prefix + "/writer":           true,
	"POST " + Prefix + "/clients/me/connect": true,
}

// TestWriteRoutesTakeTheLease walks the route table: every signed route
// that is not a GET takes the write lease, except the exempt ones, so a
// new route cannot forget it. The set must also be the operations of the
// contract that declare 423 (besides PUT /writer, which takes the lease
// itself).
func TestWriteRoutesTakeTheLease(t *testing.T) {
	f := newFixture(t)
	var guarded []string
	for _, r := range f.server.routeTable {
		method, _, _ := strings.Cut(r.pattern, " ")
		switch {
		case method == http.MethodGet && r.opts.TakesLease:
			t.Errorf("%s: a read route takes the lease", r.pattern)
		case method != http.MethodGet && leaseExempt[r.pattern] && r.opts.TakesLease:
			t.Errorf("%s: must not take the lease", r.pattern)
		case method != http.MethodGet && !leaseExempt[r.pattern] && !r.opts.TakesLease:
			t.Errorf("%s: changes the SD card but does not take the write lease", r.pattern)
		}
		if r.opts.WhileDisconnected != (r.pattern == "POST "+Prefix+"/clients/me/connect") {
			t.Errorf("%s: WhileDisconnected = %v", r.pattern, r.opts.WhileDisconnected)
		}
		if r.opts.TakesLease {
			guarded = append(guarded, strings.Replace(r.pattern, " "+Prefix, " ", 1))
		}
	}
	sort.Strings(guarded)
	var want []string
	for _, op := range contract(t).OperationsWithStatus("423") {
		if op != "PUT /writer" {
			want = append(want, op)
		}
	}
	if !slices.Equal(guarded, want) {
		t.Errorf("lease-guarded routes:\n got  %v\n want %v (operations with 423 in the contract)", guarded, want)
	}
	if len(guarded) != 23 {
		t.Errorf("%d lease-guarded routes, want 23", len(guarded))
	}
}

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

type writerResp struct {
	Writer *struct {
		ClientName string    `json:"clientName"`
		You        bool      `json:"you"`
		ExpiresAt  time.Time `json:"expiresAt"`
	} `json:"writer"`
}

func (f *fixture) writerOf(c *testutil.Client) writerResp {
	f.t.Helper()
	var w writerResp
	f.as(c, "GET", "/api/v1/writer", "/writer", nil, 200).JSON(f.t, &w)
	return w
}

// wantReadOnly checks a 423 read_only naming the holder.
func wantReadOnly(t *testing.T, r testutil.Response, writer string) {
	t.Helper()
	var e struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	r.JSON(t, &e)
	if r.Status != http.StatusLocked || e.Error.Code != CodeReadOnly || e.Error.Details["writer"] != writer {
		t.Fatalf("want 423 read_only by %q, got %d %s", writer, r.Status, r.Body)
	}
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

func TestReadOnlyClient(t *testing.T) {
	f := newFixture(t)
	f.rename(f.client, "iPhone A")
	b := f.pairNamed("iPhone B")
	g := gamePath(scPath)

	// Free at launch.
	var h helloResponse
	f.unsigned("GET", "/api/v1/hello", nil).JSON(t, &h)
	if h.WriterHeld {
		t.Fatal("writerHeld at launch")
	}
	if w := f.writerOf(b); w.Writer != nil {
		t.Fatalf("writer at launch: %+v", w.Writer)
	}

	// The first write of A takes the lease.
	f.as(f.client, "POST", g+"/save/backups", "/games/{gameId}/save/backups", nil, http.StatusCreated)
	if w := f.writerOf(f.client); w.Writer == nil || !w.Writer.You || w.Writer.ClientName != "iPhone A" ||
		!w.Writer.ExpiresAt.Equal(f.clock.Now().Add(WriteLeaseTTL)) {
		t.Fatalf("writer for A: %+v", w.Writer)
	}
	if w := f.writerOf(b); w.Writer == nil || w.Writer.You || w.Writer.ClientName != "iPhone A" {
		t.Fatalf("writer for B: %+v", w.Writer)
	}
	r := f.unsigned("GET", "/api/v1/hello", nil)
	f.check("GET", "/hello", r)
	r.JSON(t, &h)
	if !h.WriterHeld || strings.Contains(string(r.Body), "iPhone A") {
		t.Fatalf("hello while held: %s", r.Body)
	}
	var d deviceResponse
	f.as(b, "GET", "/api/v1/device", "/device", nil, 200).JSON(t, &d)
	if d.Writer == nil || d.Writer.You || d.Writer.ClientName != "iPhone A" {
		t.Fatalf("device writer for B: %+v", d.Writer)
	}
	f.as(f.client, "GET", "/api/v1/device", "/device", nil, 200).JSON(t, &d)
	if d.Writer == nil || !d.Writer.You {
		t.Fatalf("device writer for A: %+v", d.Writer)
	}

	// B is read-only: every write is refused with the holder's name and
	// changes nothing on the SD card.
	before := f.snapshot()
	upload := mustJSON(t, map[string]any{"target": map[string]any{"kind": "rom", "systemId": library.EncodeID(gbaFolder),
		"folderId": nil, "fileName": "New.gba"}, "sizeBytes": 3, "sha256": sha([]byte("abc")), "onConflict": "fail"})
	writes := []struct {
		method, target, path string
		body                 []byte
	}{
		{"DELETE", g, "/games/{gameId}", nil},
		{"PUT", g + "/save", "/games/{gameId}/save", []byte("new save")},
		{"POST", g + "/save/backups", "/games/{gameId}/save/backups", nil},
		{"PUT", g + "/cheats", "/games/{gameId}/cheats", []byte(`{"cheats":[]}`)},
		{"DELETE", g + "/art", "/games/{gameId}/art", nil},
		{"DELETE", "/api/v1/trash", "/trash", nil},
		{"POST", "/api/v1/uploads", "/uploads", upload},
		{"POST", "/api/v1/downloads", "/downloads", []byte(`{}`)},
		{"DELETE", "/api/v1/jobs/00000000-0000-4000-8000-000000000000", "/jobs/{jobId}", nil},
	}
	for _, w := range writes {
		r := b.Do(t, w.method, w.target, w.body)
		f.check(w.method, w.path, r)
		wantReadOnly(t, r, "iPhone A")
	}
	// An upload chunk too, before its body is read.
	req := b.RequestUnsigned(t, http.MethodPut, "/api/v1/uploads/00000000-0000-4000-8000-000000000000", []byte("abc"))
	req.Header.Set("Upload-Offset", "0")
	r = b.Send(t, req)
	f.check("PUT", "/uploads/{uploadId}", r)
	wantReadOnly(t, r, "iPhone A")
	if after := f.snapshot(); !slices.Equal(before, after) {
		t.Fatal("a refused write changed the SD card")
	}
	// Reads, PUT /writer refused without force, and the lease is still A's.
	f.as(b, "GET", "/api/v1/systems", "/systems", nil, 200)
	f.as(b, "GET", g+"/save", "/games/{gameId}/save", nil, 200)
	wantReadOnly(t, f.as(b, "PUT", "/api/v1/writer", "/writer", nil, http.StatusLocked), "iPhone A")
	wantReadOnly(t, f.as(b, "PUT", "/api/v1/writer", "/writer", []byte(`{"force":false}`), http.StatusLocked), "iPhone A")
	if w := f.writerOf(f.client); w.Writer == nil || !w.Writer.You {
		t.Fatalf("A lost the lease: %+v", w.Writer)
	}
	// The refusal goes to the (sampled) log of rejected requests.
	if !strings.Contains(f.logs.String(), "code=read_only") {
		t.Error("the refusal is not logged")
	}
}

func TestTakeOver(t *testing.T) {
	f := newFixture(t)
	f.rename(f.client, "iPhone A")
	b := f.pairNamed("iPhone B")
	g := gamePath(scPath)

	// Empty body = {}: A takes the free lease.
	var w writerResp
	f.as(f.client, "PUT", "/api/v1/writer", "/writer", nil, 200).JSON(t, &w)
	if w.Writer == nil || !w.Writer.You || w.Writer.ClientName != "iPhone A" {
		t.Fatalf("take: %+v", w.Writer)
	}
	// B takes over with force.
	f.clock.Advance(5 * time.Second)
	f.as(b, "PUT", "/api/v1/writer", "/writer", []byte(`{"force":true}`), 200).JSON(t, &w)
	if w.Writer == nil || !w.Writer.You || w.Writer.ClientName != "iPhone B" || !w.Writer.ExpiresAt.Equal(f.clock.Now().Add(60*time.Second)) {
		t.Fatalf("force: %+v", w.Writer)
	}
	// A is read-only now.
	r := f.client.Do(t, "POST", g+"/save/backups", nil)
	f.check("POST", "/games/{gameId}/save/backups", r)
	wantReadOnly(t, r, "iPhone B")
	f.as(b, "POST", g+"/save/backups", "/games/{gameId}/save/backups", nil, http.StatusCreated)
	// B renews with an empty PUT.
	f.clock.Advance(30 * time.Second)
	f.as(b, "PUT", "/api/v1/writer", "/writer", []byte(`{}`), 200).JSON(t, &w)
	if !w.Writer.ExpiresAt.Equal(f.clock.Now().Add(60 * time.Second)) {
		t.Fatalf("renewed: %+v", w.Writer)
	}
	// Invalid body.
	wantCode(t, f.as(b, "PUT", "/api/v1/writer", "/writer", []byte(`{"force":`), 400), auth.CodeInvalidRequest)
	wantCode(t, f.as(b, "PUT", "/api/v1/writer", "/writer", []byte(`{"force":"yes"}`), 400), auth.CodeInvalidRequest)
}

func TestLeaseExpiryAndRenewal(t *testing.T) {
	f := newFixture(t)
	f.rename(f.client, "iPhone A")
	b := f.pairNamed("iPhone B")
	g := gamePath(scPath)

	f.as(f.client, "PUT", "/api/v1/writer", "/writer", nil, 200)
	// Any signed request of the holder renews it, a read included.
	f.clock.Advance(59 * time.Second)
	wantReadOnly(t, b.Do(t, "POST", g+"/save/backups", nil), "iPhone A")
	f.as(f.client, "GET", "/api/v1/systems", "/systems", nil, 200)
	f.clock.Advance(59 * time.Second)
	wantReadOnly(t, b.Do(t, "POST", g+"/save/backups", nil), "iPhone A")
	// A request of another client does not renew it.
	if w := f.writerOf(b); w.Writer == nil || !w.Writer.ExpiresAt.Equal(f.clock.Now().Add(time.Second)) {
		t.Fatalf("renewed by B: %+v", w.Writer)
	}
	// 60 s after A's last request, the lease is free: B's write takes it.
	f.clock.Advance(time.Second)
	var h helloResponse
	f.unsigned("GET", "/api/v1/hello", nil).JSON(t, &h)
	if h.WriterHeld {
		t.Fatal("still held after 60 s")
	}
	f.as(b, "POST", g+"/save/backups", "/games/{gameId}/save/backups", nil, http.StatusCreated)
	if w := f.writerOf(f.client); w.Writer == nil || w.Writer.You || w.Writer.ClientName != "iPhone B" {
		t.Fatalf("after expiry: %+v", w.Writer)
	}
}

func TestReleaseWriter(t *testing.T) {
	f := newFixture(t)
	f.rename(f.client, "iPhone A")
	b := f.pairNamed("iPhone B")

	f.as(f.client, "PUT", "/api/v1/writer", "/writer", nil, 200)
	// Releasing a lease held by another client changes nothing.
	f.as(b, "DELETE", "/api/v1/writer", "/writer", nil, 204)
	if w := f.writerOf(b); w.Writer == nil || w.Writer.ClientName != "iPhone A" {
		t.Fatalf("released by B: %+v", w.Writer)
	}
	f.as(f.client, "DELETE", "/api/v1/writer", "/writer", nil, 204)
	if w := f.writerOf(b); w.Writer != nil {
		t.Fatalf("after release: %+v", w.Writer)
	}
	f.as(f.client, "DELETE", "/api/v1/writer", "/writer", nil, 204) // not held: still 204
	f.as(b, "PUT", "/api/v1/writer", "/writer", nil, 200)
}

// TestConcurrentFirstWriters: clients race for a free lease through the
// API; exactly one gets it. Run with -race.
func TestConcurrentFirstWriters(t *testing.T) {
	f := newFixture(t)
	clients := []*testutil.Client{f.client}
	for i := 0; i < 7; i++ {
		clients = append(clients, f.pairNamed("iPhone "+strconv.Itoa(i)))
	}
	var mu sync.Mutex
	ok := 0
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go func(c *testutil.Client) {
			defer wg.Done()
			r := c.Do(t, "PUT", "/api/v1/writer", nil)
			if r.Status == 200 {
				mu.Lock()
				ok++
				mu.Unlock()
			} else if r.Status != http.StatusLocked {
				t.Errorf("status %d", r.Status)
			}
		}(c)
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("%d clients got the lease", ok)
	}
}

func TestDisconnectByTheConsole(t *testing.T) {
	f := newFixture(t)
	f.rename(f.client, "iPhone A")
	b := f.pairNamed("iPhone B")
	g := gamePath(scPath)

	f.as(f.client, "PUT", "/api/v1/writer", "/writer", nil, 200)
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
		{"GET", "/api/v1/writer", "/writer"},
		{"PUT", "/api/v1/writer", "/writer"},
		{"POST", g + "/save/backups", "/games/{gameId}/save/backups"},
		{"DELETE", "/api/v1/clients/me", "/clients/me"},
	} {
		wantCode(t, f.as(f.client, req.method, req.target, req.path, nil, 401), CodeDisconnected)
	}
	if _, ok := f.clients.Lookup(f.client.ClientID); !ok {
		t.Fatal("a disconnected client could unpair")
	}
	// Its lease is dropped and it is no longer connected.
	if w := f.writerOf(b); w.Writer != nil {
		t.Fatalf("lease kept: %+v", w.Writer)
	}
	if got := connected(t, f, b); len(got) != 1 || got[0].ClientName != "iPhone B" {
		t.Fatalf("connected: %+v", got)
	}
	devs := f.server.Devices()
	if len(devs) != 2 {
		t.Fatalf("devices %+v", devs)
	}
	for _, d := range devs {
		if d.ID == f.client.ClientID && (!d.Disconnected || d.Connected || d.Writer) {
			t.Errorf("A: %+v", d)
		}
		if d.ID == b.ClientID && (d.Disconnected || !d.Connected || d.Writer) {
			t.Errorf("B: %+v", d)
		}
	}
	// B can write now.
	f.as(b, "POST", g+"/save/backups", "/games/{gameId}/save/backups", nil, http.StatusCreated)

	// POST /clients/me/connect lifts it without taking the lease, and the
	// device is connected again at once.
	f.as(f.client, "POST", "/api/v1/clients/me/connect", "/clients/me/connect", nil, 204)
	if got := f.server.ConnectedClients(); len(got) != 2 {
		t.Fatalf("connected right after connect: %+v", got)
	}
	f.as(f.client, "GET", "/api/v1/systems", "/systems", nil, 200)
	if w := f.writerOf(f.client); w.Writer == nil || w.Writer.You {
		t.Fatalf("after connect: %+v", w.Writer)
	}
	// No effect when not disconnected.
	f.as(f.client, "POST", "/api/v1/clients/me/connect", "/clients/me/connect", nil, 204)
	f.as(f.client, "GET", "/api/v1/systems", "/systems", nil, 200)
}

func TestForgetByTheConsole(t *testing.T) {
	f := newFixture(t)
	f.rename(f.client, "iPhone A")
	b := f.pairNamed("iPhone B")

	f.as(f.client, "PUT", "/api/v1/writer", "/writer", nil, 200)
	ok, err := f.server.Forget(f.client.ClientID)
	if err != nil || !ok {
		t.Fatalf("forget: %v %v", ok, err)
	}
	wantCode(t, f.as(f.client, "GET", "/api/v1/systems", "/systems", nil, 401), auth.CodeUnknownClient)
	wantCode(t, f.as(f.client, "POST", "/api/v1/clients/me/connect", "/clients/me/connect", nil, 401), auth.CodeUnknownClient)
	if w := f.writerOf(b); w.Writer != nil {
		t.Fatalf("lease kept: %+v", w.Writer)
	}
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
	f.as(b, "PUT", "/api/v1/writer", "/writer", nil, 200)
	if n, err := f.server.ForgetAll(); n != 1 || err != nil {
		t.Fatalf("forget all: %d %v", n, err)
	}
	if _, held := f.server.WriterName(); held {
		t.Fatal("lease kept after forget all")
	}
	wantCode(t, f.as(b, "GET", "/api/v1/systems", "/systems", nil, 401), auth.CodeUnknownClient)
}

// TestUploadAfterTakeOver: the holder's next chunk after a take-over gets
// 423 before its body is read; the upload is not lost and resumes once the
// lease is taken back.
func TestUploadAfterTakeOver(t *testing.T) {
	f := newFixture(t)
	f.rename(f.client, "iPhone A")
	b := f.pairNamed("iPhone B")
	data := make([]byte, 200_000)
	rand.Read(data)
	target := map[string]any{"kind": "rom", "systemId": library.EncodeID(gbaFolder), "folderId": nil, "fileName": "Sky Garden Quest DX.gba"}
	c, _ := f.createUpload(map[string]any{"target": target, "sizeBytes": len(data), "sha256": sha(data), "onConflict": "fail"}, http.StatusCreated)
	if r := f.chunk(c.UploadID, 0, data[:c.ChunkSize]); r.Status != http.StatusNoContent {
		t.Fatalf("chunk: %d %s", r.Status, r.Body)
	}

	f.as(b, "PUT", "/api/v1/writer", "/writer", []byte(`{"force":true}`), 200)
	wantReadOnly(t, f.chunk(c.UploadID, c.ChunkSize, data[c.ChunkSize:2*c.ChunkSize]), "iPhone B")
	wantReadOnly(t, f.client.Do(t, "POST", "/api/v1/uploads/"+c.UploadID+"/complete", nil), "iPhone B")
	// Still resumable: the status is a read.
	var st struct {
		Offset int64 `json:"offset"`
	}
	f.do(http.MethodGet, "/api/v1/uploads/"+c.UploadID, "/uploads/{uploadId}", nil, http.StatusOK).JSON(t, &st)
	if st.Offset != int64(c.ChunkSize) {
		t.Fatalf("offset %d after the refused chunk", st.Offset)
	}

	// A takes the lease back and finishes.
	f.as(f.client, "PUT", "/api/v1/writer", "/writer", []byte(`{"force":true}`), 200)
	for off := c.ChunkSize; off < len(data); off += c.ChunkSize {
		end := min(off+c.ChunkSize, len(data))
		if r := f.chunk(c.UploadID, off, data[off:end]); r.Status != http.StatusNoContent {
			t.Fatalf("chunk at %d: %d %s", off, r.Status, r.Body)
		}
	}
	f.do(http.MethodPost, "/api/v1/uploads/"+c.UploadID+"/complete", "/uploads/{uploadId}/complete", nil, http.StatusOK)
	if got := f.read("Roms/" + gbaFolder + "/Sky Garden Quest DX.gba"); got != string(data) {
		t.Fatal("uploaded file differs")
	}
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

// TestRejectedWithoutWaitingForTheBody: a 423 read_only or 401
// disconnected arrives while the client still holds its body back (a
// small body would otherwise be drained by net/http first), over a real
// connection.
func TestRejectedWithoutWaitingForTheBody(t *testing.T) {
	f := newFixture(t)
	f.rename(f.client, "iPhone A")
	b := f.pairNamed("iPhone B")
	g := gamePath(scPath)
	f.as(f.client, "PUT", "/api/v1/writer", "/writer", nil, 200)
	body := make([]byte, 100<<10) // under the 256 KiB net/http drains

	for _, unsigned := range []bool{false, true} {
		target, path := g+"/save", "/games/{gameId}/save"
		if unsigned {
			target, path = "/api/v1/uploads/00000000-0000-4000-8000-000000000000", "/uploads/{uploadId}"
		}
		conn, br := f.heldBody(b, "PUT", target, body, unsigned)
		resp, r := readResponse(t, conn, br)
		f.check("PUT", path, r)
		wantReadOnly(t, r, "iPhone A")
		if !resp.Close {
			t.Errorf("unsigned=%v: the connection must close after a pre-body rejection", unsigned)
		}
	}

	if err := f.server.Disconnect(b.ClientID); err != nil {
		t.Fatal(err)
	}
	conn, br := f.heldBody(b, "PUT", g+"/save", body, false)
	resp, r := readResponse(t, conn, br)
	f.check("PUT", "/games/{gameId}/save", r)
	if r.Status != 401 || r.ErrorCode() != CodeDisconnected || !resp.Close {
		t.Fatalf("disconnected: %d %s close=%v", r.Status, r.Body, resp.Close)
	}
}

// TestRejectedWriteDoesNotTakeTheLease: the lease taken by a write request
// before its body is read is given back if the request is then rejected,
// and a rejected request of the holder does not renew it.
func TestRejectedWriteDoesNotTakeTheLease(t *testing.T) {
	f := newFixture(t)
	f.rename(f.client, "iPhone A")
	b := f.pairNamed("iPhone B")
	g := gamePath(scPath)
	badHash := func(c *testutil.Client) testutil.Response {
		req := c.Request(t, "PUT", g+"/save", []byte("signed body"))
		req.Body = io.NopCloser(strings.NewReader("forged body"))
		r := c.Send(t, req)
		f.check("PUT", "/games/{gameId}/save", r)
		return r
	}

	// Free lease: B's write with a wrong body hash is refused and B does
	// not keep the lease.
	before := f.snapshot()
	wantCode(t, badHash(b), auth.CodeBadSignature)
	if _, held := f.server.WriterName(); held {
		t.Fatal("a rejected write took the lease")
	}
	if !slices.Equal(before, f.snapshot()) {
		t.Fatal("the SD card changed")
	}
	// Too large: same.
	big := b.Do(t, "PUT", "/api/v1/games/"+library.EncodeID(scPath)+"/cheats", make([]byte, auth.BodyLimitJSON+1))
	if big.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("too large: %d", big.Status)
	}
	if _, held := f.server.WriterName(); held {
		t.Fatal("a too large write took the lease")
	}

	// A rejected request of the holder does not renew it.
	f.as(f.client, "PUT", "/api/v1/writer", "/writer", nil, 200)
	f.clock.Advance(50 * time.Second)
	wantCode(t, badHash(f.client), auth.CodeBadSignature)
	f.clock.Advance(10 * time.Second)
	if _, held := f.server.WriterName(); held {
		t.Fatal("the rejected request renewed the lease")
	}
	f.as(b, "POST", g+"/save/backups", "/games/{gameId}/save/backups", nil, http.StatusCreated)
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
// body is still coming does not (re)gain the lease if the console
// disconnects or forgets its client, or if another client takes over, in
// the meantime. Deterministic: the body is sent only after the revocation.
func TestRevocationWhileTheBodyIsRead(t *testing.T) {
	g := gamePath(scPath)
	cases := []struct {
		name   string
		aHolds bool // A holds the lease before B's request
		method string
		target string
		path   string
		body   string
		revoke func(f *fixture, b *testutil.Client)
		code   string
		wantA  bool // A holds the lease at the end
	}{
		{"take over then disconnect", true, "PUT", "/api/v1/writer", "/writer", `{"force":true}`,
			func(f *fixture, b *testutil.Client) { f.server.Disconnect(b.ClientID) }, CodeDisconnected, true},
		{"take over then forget", true, "PUT", "/api/v1/writer", "/writer", `{"force":true}`,
			func(f *fixture, b *testutil.Client) { f.server.Forget(b.ClientID) }, auth.CodeUnknownClient, true},
		{"first write then disconnect", false, "PUT", g + "/save", "/games/{gameId}/save", "new save",
			func(f *fixture, b *testutil.Client) { f.server.Disconnect(b.ClientID) }, CodeDisconnected, false},
		{"first write then forget all", false, "PUT", g + "/save", "/games/{gameId}/save", "new save",
			func(f *fixture, b *testutil.Client) { f.server.ForgetAll() }, auth.CodeUnknownClient, false},
		{"first write then take over by A", false, "PUT", g + "/save", "/games/{gameId}/save", "new save",
			func(f *fixture, b *testutil.Client) {
				f.as(f.client, "PUT", "/api/v1/writer", "/writer", []byte(`{"force":true}`), 200)
			}, CodeReadOnly, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.rename(f.client, "iPhone A")
			b := f.pairNamed("iPhone B")
			if tc.aHolds {
				f.as(f.client, "PUT", "/api/v1/writer", "/writer", nil, 200)
			}
			before := f.snapshot()
			reached := f.gateReached()
			conn, br := f.heldBody(b, tc.method, tc.target, []byte(tc.body), false)
			select {
			case <-reached:
			case <-time.After(5 * time.Second):
				t.Fatal("the gate was not reached")
			}
			f.server.gateHook.Store(nil)
			tc.revoke(f, b)
			if _, err := conn.Write([]byte(tc.body)); err != nil {
				t.Fatal(err)
			}
			_, r := readResponse(t, conn, br)
			f.check(tc.method, tc.path, r)
			wantCode(t, r, tc.code)
			if tc.code == CodeReadOnly {
				wantReadOnly(t, r, "iPhone A")
			}
			name, held := f.server.WriterName()
			if tc.wantA != (held && name == "iPhone A") || (!tc.wantA && held) {
				t.Fatalf("writer after the revocation: %q %v", name, held)
			}
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

// Codex round 2, finding 2: a read of the same client while a write waits
// for its body does not make the write's reservation permanent.
func TestReadDuringHeldWriteDoesNotKeepTheLease(t *testing.T) {
	f := newFixture(t)
	b := f.pairNamed("iPhone B")
	g := gamePath(scPath)
	signed := []byte("signed body")
	conn, br := f.waitGate(b, "PUT", g+"/save", signed)
	f.as(b, "GET", "/api/v1/device", "/device", nil, 200)
	conn.Write([]byte("forged body")) // same length, wrong hash
	_, r := readResponse(t, conn, br)
	wantCode(t, r, auth.CodeBadSignature)
	if name, held := f.server.WriterName(); held {
		t.Fatalf("the rejected write left the lease to %q", name)
	}
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
			if _, held := f.server.WriterName(); held {
				t.Fatal("the revoked request holds the lease")
			}
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
	forger := &testutil.Client{BaseURL: f.http.URL, ClientID: f.client.ClientID, Key: make([]byte, auth.KeySize), Now: f.clock.Now}
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
	if _, held := f.server.WriterName(); held {
		t.Fatal("a rejected write holds the lease")
	}
}

// Codex round 2, finding 5: a request admitted before a disconnection does
// not put the device back among the connected clients.
func TestDisconnectedWhileTheBodyIsReadStaysOut(t *testing.T) {
	f := newFixture(t)
	b := f.pairNamed("iPhone B")
	body := []byte(`{"force":false}`)
	conn, br := f.waitGate(b, "PUT", "/api/v1/writer", body)
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
