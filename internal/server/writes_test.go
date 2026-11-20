package server

import (
	"bytes"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

// formerLeaseRoutes are the 23 routes that took the M10 write lease, with
// how each one takes the M13 write lock. Written by hand: a new write
// route must be added here on purpose.
var formerLeaseRoutes = map[string]writeMode{
	"PUT /games/{gameId}/art":                               writeHandler,
	"DELETE /games/{gameId}/art":                            writeHandler,
	"DELETE /games/{gameId}":                                writeHandler,
	"DELETE /trash":                                         writeHandler,
	"DELETE /trash/{entryId}":                               writeHandler,
	"PUT /games/{gameId}/save":                              writeHandler,
	"POST /games/{gameId}/save/backups":                     writeHandler,
	"DELETE /games/{gameId}/save/backups/{backupId}":        writeHandler,
	"POST /games/{gameId}/save/backups/{backupId}/restore":  writeHandler,
	"DELETE /games/{gameId}/states/{slot}":                  writeHandler,
	"PUT /games/{gameId}/states/{slot}/lock":                writeHandler,
	"DELETE /games/{gameId}/states/{slot}/lock":             writeHandler,
	"POST /games/{gameId}/states/{slot}/restore":            writeHandler,
	"POST /games/{gameId}/resume-history/{entryId}/restore": writeHandler,
	"PUT /games/{gameId}/cheats":                            writeHandler,
	"POST /games/{gameId}/cheats/import":                    writeInner,
	"POST /uploads/{uploadId}/complete":                     writeInstall,
	"POST /downloads":                                       writeInstall,
	"POST /games/{gameId}/patch":                            writeInstall,
	"POST /uploads":                                         writeStaging,
	"PUT /uploads/{uploadId}":                               writeStaging,
	"DELETE /uploads/{uploadId}":                            writeStaging,
	"DELETE /jobs/{jobId}":                                  writeStaging,
}

// TestWriteRoutesTakeTheWriteLock walks the route table: every signed
// route that is not a GET is one of the former lease routes (with its
// mode) or exempt; reads never take the lock; and the contract no longer
// declares 423.
func TestWriteRoutesTakeTheWriteLock(t *testing.T) {
	f := newFixture(t)
	exempt := map[string]bool{"DELETE /clients/me": true, "POST /clients/me/connect": true}
	var seen []string
	for _, r := range f.server.routeTable {
		op := strings.Replace(r.pattern, " "+Prefix, " ", 1)
		method, _, _ := strings.Cut(op, " ")
		want, listed := formerLeaseRoutes[op]
		switch {
		case method == http.MethodGet && r.write != writeNone:
			t.Errorf("%s: a read takes the write lock", op)
		case method != http.MethodGet && exempt[op] && r.write != writeNone:
			t.Errorf("%s: must not take the write lock", op)
		case method != http.MethodGet && !exempt[op] && !listed:
			t.Errorf("%s: a new write route: add it to formerLeaseRoutes", op)
		case listed && r.write != want:
			t.Errorf("%s: write mode %d, want %d", op, r.write, want)
		}
		if r.opts.WhileDisconnected != (op == "POST /clients/me/connect") {
			t.Errorf("%s: WhileDisconnected = %v", op, r.opts.WhileDisconnected)
		}
		if listed {
			seen = append(seen, op)
		}
	}
	sort.Strings(seen)
	if len(seen) != len(formerLeaseRoutes) || len(seen) != 23 {
		t.Errorf("%d former lease routes registered, want 23: %v", len(seen), seen)
	}
	if ops := contract(t).OperationsWithStatus("423"); len(ops) != 0 {
		t.Errorf("the contract still declares 423: %v", ops)
	}
	// Every write route declares the 429 of "Device busy".
	with429 := contract(t).OperationsWithStatus("429")
	for op := range formerLeaseRoutes {
		if !slices.Contains(with429, op) {
			t.Errorf("%s does not declare 429", op)
		}
	}
}

// holdWrites takes the write lock as a write in progress would, with a
// short request wait.
func (f *fixture) holdWrites(wait time.Duration) {
	f.t.Helper()
	f.server.writes().SetWait(wait)
	if !f.server.writes().TryAcquire() {
		f.t.Fatal("write lock already held")
	}
}

// wantBusy checks a 429 Device busy with Retry-After.
func wantBusy(t *testing.T, r testutil.Response) {
	t.Helper()
	var e struct {
		Error struct {
			Code, Message string
		} `json:"error"`
	}
	r.JSON(t, &e)
	if r.Status != http.StatusTooManyRequests || e.Error.Code != "too_many_attempts" || e.Error.Message != "Device busy" ||
		r.Header.Get("Retry-After") != "2" {
		t.Fatalf("want 429 Device busy, got %d %s Retry-After=%q", r.Status, r.Body, r.Header.Get("Retry-After"))
	}
}

// A write that waits longer than the wait gets 429 Device busy, changes
// nothing, and goes through once the write in progress is over.
func TestWriteBusyAfterTheWait(t *testing.T) {
	f := newFixture(t)
	g := gamePath(scPath)
	before := f.snapshot()
	f.holdWrites(80 * time.Millisecond)
	start := time.Now()
	r := f.client.Do(t, "POST", g+"/save/backups", []byte(`{"label":"x"}`))
	f.check("POST", "/games/{gameId}/save/backups", r)
	wantBusy(t, r)
	if d := time.Since(start); d < 80*time.Millisecond {
		t.Fatalf("answered after %v, before the wait", d)
	}
	// The cheat import takes the lock itself, after reading its source.
	r = f.client.Do(t, "POST", g+"/cheats/import", []byte(`{"source":{"type":"inline","text":"cheats = 0\n"},"mode":"merge"}`))
	f.check("POST", "/games/{gameId}/cheats/import", r)
	wantBusy(t, r)
	if !slices.Equal(before, f.snapshot()) {
		t.Fatal("a busy write changed the SD card")
	}
	// Reads go on.
	f.do("GET", g, "/games/{gameId}", nil, http.StatusOK)
	f.server.writes().Release()
	f.do("POST", g+"/save/backups", "/games/{gameId}/save/backups", []byte(`{"label":"x"}`), http.StatusCreated)
}

// Two writes that arrive while a third runs both wait, then run one after
// the other (the wait is long enough).
func TestConcurrentWritesSerialize(t *testing.T) {
	f := newFixture(t)
	g := gamePath(scPath)
	b := f.pairNamed("iPhone B")
	f.holdWrites(5 * time.Second)
	before := f.server.writes().Acquisitions()
	var wg sync.WaitGroup
	results := make([]testutil.Response, 2)
	for i, c := range []*testutil.Client{f.client, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = c.Do(t, "POST", g+"/save/backups", []byte(`{"label":"concurrent"}`))
		}()
	}
	time.Sleep(100 * time.Millisecond)
	if n := f.server.writes().Acquisitions(); n != before {
		t.Fatalf("a write ran while another held the lock (%d acquisitions)", n-before)
	}
	f.server.writes().Release()
	wg.Wait()
	for i, r := range results {
		f.check("POST", "/games/{gameId}/save/backups", r)
		if r.Status != http.StatusCreated {
			t.Fatalf("write %d: %d %s", i, r.Status, r.Body)
		}
	}
	if n := f.server.writes().Acquisitions() - before; n != 2 {
		t.Fatalf("%d acquisitions, want 2", n)
	}
}

// Upload chunks never wait for a write; the final step does, and an
// upload refused as busy stays and completes later.
func TestUploadWaitsOnlyAtTheEnd(t *testing.T) {
	f := newFixture(t)
	data := make([]byte, 100_000)
	rand.Read(data)
	target := map[string]any{"kind": "rom", "systemId": library.EncodeID(gbaFolder), "folderId": nil, "fileName": "Busy Quest.gba"}
	f.holdWrites(80 * time.Millisecond)
	c, _ := f.createUpload(map[string]any{"target": target, "sizeBytes": len(data), "sha256": sha(data), "onConflict": "fail"}, http.StatusCreated)
	for off := 0; off < len(data); off += c.ChunkSize {
		if r := f.chunk(c.UploadID, off, data[off:min(off+c.ChunkSize, len(data))]); r.Status != http.StatusNoContent {
			t.Fatalf("chunk at %d while a write runs: %d %s", off, r.Status, r.Body)
		}
	}
	r := f.client.Do(t, "POST", "/api/v1/uploads/"+c.UploadID+"/complete", nil)
	f.check("POST", "/uploads/{uploadId}/complete", r)
	wantBusy(t, r)
	if f.exists("Roms/" + gbaFolder + "/Busy Quest.gba") {
		t.Fatal("installed while busy")
	}
	f.do("GET", "/api/v1/uploads/"+c.UploadID, "/uploads/{uploadId}", nil, http.StatusOK)
	f.server.writes().Release()
	f.do("POST", "/api/v1/uploads/"+c.UploadID+"/complete", "/uploads/{uploadId}/complete", nil, http.StatusOK)
	if !f.exists("Roms/" + gbaFolder + "/Busy Quest.gba") {
		t.Fatal("not installed")
	}
}

// A job waits for its install step as long as a write runs, without
// failing (no request wait for jobs).
func TestJobWaitsForItsInstall(t *testing.T) {
	f := newFixture(t)
	data := []byte("downloaded while busy")
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	}))
	defer src.Close()
	f.holdWrites(20 * time.Millisecond)
	body := mustJSON(t, map[string]any{"url": src.URL + "/hack.bps", "target": map[string]any{"kind": "staging"}, "sha256": nil, "onConflict": "rename"})
	var accepted struct {
		JobID string `json:"jobId"`
	}
	f.do(http.MethodPost, "/api/v1/downloads", "/downloads", body, http.StatusAccepted).JSON(t, &accepted)
	time.Sleep(200 * time.Millisecond) // ten times the request wait
	var j jobState
	f.do(http.MethodGet, "/api/v1/jobs/"+accepted.JobID, "/jobs/{jobId}", nil, http.StatusOK).JSON(t, &j)
	if j.State != "running" {
		t.Fatalf("job while busy: %+v", j)
	}
	f.server.writes().Release()
	if j = f.waitJob(accepted.JobID); j.State != "succeeded" {
		t.Fatalf("job %+v", j)
	}
}

// Codex review (M13): a write that waits for the lock is refused if the
// console revokes its client during the wait, on every write path; and a
// job of a revoked client is cancelled before its install.
func TestRevokedWhileWaitingForTheWriteLock(t *testing.T) {
	g := gamePath(scPath)
	uploadOf := func(f *fixture, b *testutil.Client, name string) string {
		data := []byte("waiting upload " + name)
		target := map[string]any{"kind": "rom", "systemId": library.EncodeID(gbaFolder), "folderId": nil, "fileName": name}
		var c uploadCreated
		f.as(b, http.MethodPost, "/api/v1/uploads", "/uploads", mustJSON(t, map[string]any{"target": target, "sizeBytes": len(data), "sha256": sha(data)}), http.StatusCreated).JSON(t, &c)
		req := b.RequestUnsigned(t, http.MethodPut, "/api/v1/uploads/"+c.UploadID, data)
		req.Header.Set("Upload-Offset", "0")
		if r := b.Send(t, req); r.Status != http.StatusNoContent {
			t.Fatalf("chunk: %d %s", r.Status, r.Body)
		}
		return c.UploadID
	}
	for _, revoke := range []struct {
		name string
		do   func(f *fixture, id string)
		code string
	}{
		{"disconnect", func(f *fixture, id string) { f.server.Disconnect(id) }, CodeDisconnected},
		{"forget", func(f *fixture, id string) { f.server.Forget(id) }, auth.CodeUnknownClient},
	} {
		for _, p := range []string{"handler", "inner", "install"} {
			t.Run(revoke.name+"/"+p, func(t *testing.T) {
				f := newFixture(t)
				b := f.pairNamed("iPhone B")
				var method, target, route string
				var body []byte
				installed := func() bool { return false }
				switch p {
				case "handler":
					method, target, route = "POST", g+"/save/backups", "/games/{gameId}/save/backups"
					body = []byte(`{"label":"revoked"}`)
				case "inner":
					method, target, route = "POST", g+"/cheats/import", "/games/{gameId}/cheats/import"
					body = []byte(`{"source":{"type":"inline","text":"cheats = 0\n"},"mode":"replace"}`)
				case "install":
					id := uploadOf(f, b, "Revoked.gba")
					method, target, route = "POST", "/api/v1/uploads/"+id+"/complete", "/uploads/{uploadId}/complete"
					installed = func() bool { return f.exists("Roms/" + gbaFolder + "/Revoked.gba") }
				}
				before := f.snapshot()
				f.holdWrites(5 * time.Second)
				done := make(chan testutil.Response, 1)
				go func() { done <- b.Do(t, method, target, body) }()
				time.Sleep(150 * time.Millisecond) // the write waits for the lock
				revoke.do(f, b.ClientID)
				f.server.writes().Release()
				r := <-done
				f.check(method, route, r)
				wantCode(t, r, revoke.code)
				if installed() || (p != "install" && !slices.Equal(before, f.snapshot())) {
					t.Fatal("the revoked write changed the card")
				}
				if !f.server.writes().TryAcquire() {
					t.Fatal("the write lock was kept")
				}
				f.server.writes().Release()
			})
		}
	}
}

func TestJobOfARevokedClientIsCancelled(t *testing.T) {
	f := newFixture(t)
	b := f.pairNamed("iPhone B")
	data := []byte("downloaded for a revoked client")
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	}))
	defer src.Close()
	f.holdWrites(20 * time.Millisecond)
	body := mustJSON(t, map[string]any{"url": src.URL + "/hack.bps", "target": map[string]any{"kind": "staging"}, "sha256": nil, "onConflict": "rename"})
	var accepted struct {
		JobID string `json:"jobId"`
	}
	f.as(b, http.MethodPost, "/api/v1/downloads", "/downloads", body, http.StatusAccepted).JSON(t, &accepted)
	time.Sleep(100 * time.Millisecond) // the job waits for its install
	if err := f.server.Disconnect(b.ClientID); err != nil {
		t.Fatal(err)
	}
	f.server.writes().Release()
	if j := f.waitJob(accepted.JobID); j.State != "cancelled" {
		t.Fatalf("job of a revoked client: %+v", j)
	}
	// Another client's job is not affected.
	var other struct {
		JobID string `json:"jobId"`
	}
	f.do(http.MethodPost, "/api/v1/downloads", "/downloads", body, http.StatusAccepted).JSON(t, &other)
	f.server.Disconnect(b.ClientID)
	if j := f.waitJob(other.JobID); j.State != "succeeded" {
		t.Fatalf("job of another client: %+v", j)
	}
}

// downloadOrigin serves a small file for the job tests.
func downloadOrigin(t *testing.T) (url string, body []byte) {
	t.Helper()
	data := []byte("downloaded by a job")
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	}))
	t.Cleanup(src.Close)
	return src.URL, mustJSON(t, map[string]any{"url": src.URL + "/hack.bps", "target": map[string]any{"kind": "staging"}, "sha256": nil, "onConflict": "rename"})
}

// holdJobHandler makes the next request that queued a job stop before it
// checks its client again, until the returned release is called. reached
// gets the job id.
func (f *fixture) holdJobHandler() (reached chan string, release func()) {
	reached = make(chan string, 1)
	gate := make(chan struct{})
	var once sync.Once
	hook := func(id string) {
		first := false
		once.Do(func() { first = true })
		if first {
			reached <- id
			<-gate
		}
	}
	f.server.jobHook.Store(&hook)
	return reached, func() { close(gate) }
}

func (f *fixture) jobState(id string) string {
	f.t.Helper()
	j, apiErr := f.server.opts.Transfers.Jobs().Get(id)
	if apiErr != nil {
		f.t.Fatal(apiErr)
	}
	return j.State
}

// waitWriteWaiter waits until a caller waits for the write lock.
func (f *fixture) waitWriteWaiter() {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for f.server.writes().Waiting() == 0 {
		if time.Now().After(deadline) {
			f.t.Fatal("nobody waits for the write lock")
		}
		time.Sleep(time.Millisecond)
	}
}

// Codex round 2, race 1: the queue starts the job before the request that
// queued it answers; the client is revoked while the job waits for the
// write lock: the job, tied to its client at submission, ends cancelled
// without installing, and the request gets 401.
func TestJobTiedToItsClientFromTheStart(t *testing.T) {
	f := newFixture(t)
	b := f.pairNamed("iPhone B")
	origin, _ := downloadOrigin(t)
	body := mustJSON(t, map[string]any{"url": origin + "/hack.gba", "onConflict": "fail",
		"target": map[string]any{"kind": "rom", "systemId": library.EncodeID(gbaFolder), "fileName": "Revoked Download.gba"}})
	f.holdWrites(20 * time.Millisecond)
	reached, release := f.holdJobHandler()
	answer := make(chan testutil.Response, 1)
	go func() { answer <- b.Do(t, http.MethodPost, "/api/v1/downloads", body) }()
	id := <-reached
	f.waitWriteWaiter() // the job downloaded and waits for its install
	if err := f.server.Disconnect(b.ClientID); err != nil {
		t.Fatal(err)
	}
	f.server.writes().Release()
	if j := f.waitJob(id); j.State != "cancelled" || j.Result != nil {
		t.Fatalf("job of a revoked client: %+v", j)
	}
	if f.exists("Roms/" + gbaFolder + "/Revoked Download.gba") {
		t.Fatal("the job of a revoked client installed its file")
	}
	release()
	r := <-answer
	f.check(http.MethodPost, "/downloads", r)
	wantCode(t, r, CodeDisconnected)
}

// Codex round 3: a client revoked after its request was accepted but
// before the job is queued gets 401, no job, and no outbound request.
func TestRevokedBeforeTheJobIsQueued(t *testing.T) {
	f := newFixture(t)
	b := f.pairNamed("iPhone B")
	var requests atomic.Int32
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Write([]byte("x"))
	}))
	defer src.Close()
	game := gbaFolder + "/Starlight Courier.gba"
	stagingID := f.stageFile("hack.bps", testutil.EncodeBPS([]byte("abcdef"), []byte("abXdef")))
	hook := func() { f.server.Disconnect(b.ClientID) }
	f.server.enqueueHook.Store(&hook)
	for _, tc := range []struct {
		target, route string
		body          []byte
	}{
		{"/api/v1/downloads", "/downloads", mustJSON(t, map[string]any{"url": src.URL + "/x.bps", "target": map[string]any{"kind": "staging"}})},
		{"/api/v1/games/" + library.EncodeID(game) + "/patch", "/games/{gameId}/patch",
			mustJSON(t, map[string]any{"patch": map[string]any{"type": "staging", "stagingId": stagingID}, "output": map[string]any{"mode": "new-file"}})},
	} {
		f.as(b, http.MethodPost, "/api/v1/clients/me/connect", "/clients/me/connect", nil, http.StatusNoContent)
		r := b.Do(t, http.MethodPost, tc.target, tc.body)
		f.check(http.MethodPost, tc.route, r)
		wantCode(t, r, CodeDisconnected)
	}
	f.server.enqueueHook.Store(nil)
	if jobs := f.server.opts.Transfers.Jobs().List(); len(jobs) != 0 {
		t.Fatalf("jobs created for a revoked client: %+v", jobs)
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("%d outbound requests for a revoked client", n)
	}
}

// Codex round 2, race 2: an old request that queued a job is revoked; the
// client reconnects and queues another job; the old request then answers
// 401 and cancels only its own job, never the new one.
func TestOldJobRequestDoesNotCancelANewJob(t *testing.T) {
	f := newFixture(t)
	b := f.pairNamed("iPhone B")
	_, body := downloadOrigin(t)
	f.holdWrites(20 * time.Millisecond) // both jobs wait for their install
	reached, release := f.holdJobHandler()
	answer := make(chan testutil.Response, 1)
	go func() { answer <- b.Do(t, http.MethodPost, "/api/v1/downloads", body) }()
	oldID := <-reached
	if err := f.server.Disconnect(b.ClientID); err != nil {
		t.Fatal(err)
	}
	f.as(b, http.MethodPost, "/api/v1/clients/me/connect", "/clients/me/connect", nil, http.StatusNoContent)
	var fresh struct {
		JobID string `json:"jobId"`
	}
	f.as(b, http.MethodPost, "/api/v1/downloads", "/downloads", body, http.StatusAccepted).JSON(t, &fresh)
	release()
	r := <-answer
	f.check(http.MethodPost, "/downloads", r)
	wantCode(t, r, CodeDisconnected)
	f.server.writes().Release()
	if j := f.waitJob(oldID); j.State != "cancelled" {
		t.Fatalf("old job: %+v", j)
	}
	if j := f.waitJob(fresh.JobID); j.State != "succeeded" {
		t.Fatalf("new job: %+v", j)
	}
}
