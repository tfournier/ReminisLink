package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/device"
	"github.com/tfournier/ReminisLink/internal/events"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

// fastEvents shortens the intervals of the event stream for a test.
func (f *fixture) fastEvents(opts events.Options) {
	if opts.LibraryInterval == 0 {
		opts.LibraryInterval = 50 * time.Millisecond
	}
	if opts.JobInterval == 0 {
		opts.JobInterval = 50 * time.Millisecond
	}
	f.server.events = f.server.newEventHub(opts)
}

// openEvents opens a stream from c, checks the answer and its hello.
func (f *fixture) openEvents(c *testutil.Client) *testutil.SSE {
	f.t.Helper()
	s := testutil.OpenSSE(f.t, c)
	if s.Resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(s.Resp.Body)
		f.t.Fatalf("GET /events: %d %s", s.Resp.StatusCode, body)
	}
	if err := contract(f.t).ValidateResponse("GET", "/events", s.Resp.StatusCode, s.Resp.Header, nil); err != nil {
		f.t.Error(err)
	}
	ev := f.nextEvent(s)
	if ev.ID != 1 || ev.Type != events.TypeHello {
		f.t.Fatalf("first event %+v", ev)
	}
	return s
}

// nextEvent returns the next event (comments skipped) and validates its
// data against the contract.
// Device events (a client connecting marks every stream) are skipped,
// unless the test is about them (fastDevice).
func (f *fixture) nextEvent(s *testutil.SSE) testutil.SSEEvent {
	f.t.Helper()
	for {
		ev, err := s.NextEvent(5 * time.Second)
		if err != nil {
			f.t.Fatalf("next event: %v", err)
		}
		checkEventData(f.t, ev)
		if ev.Type == events.TypeDevice && !f.devices {
			continue
		}
		return ev
	}
}

var eventSchemas = map[string]string{
	events.TypeHello:       "EventHello",
	events.TypeShutdown:    "EventShutdown",
	events.TypeDevice:      "EventDevice",
	events.TypeLibrary:     "EventLibrary",
	events.TypeJob:         "Job",
	events.TypePaks:        "EventPaks",
	events.TypeScreenshots: "EventScreenshots",
}

func checkEventData(t *testing.T, ev testutil.SSEEvent) {
	t.Helper()
	name, ok := eventSchemas[ev.Type]
	if !ok {
		t.Fatalf("unknown event type %q", ev.Type)
	}
	var v any
	if err := json.Unmarshal([]byte(ev.Data), &v); err != nil {
		t.Fatalf("%s data %q: %v", ev.Type, ev.Data, err)
	}
	if err := contract(t).ValidateSchema(name, v); err != nil {
		t.Errorf("%s data %s: %v", ev.Type, ev.Data, err)
	}
}

// wantEnd checks that the stream ends (EOF) without another event.
func wantEnd(t *testing.T, s *testutil.SSE) {
	t.Helper()
	for {
		ev, err := s.Next(5 * time.Second)
		switch {
		case errors.Is(err, io.EOF):
			return
		case err != nil:
			t.Fatalf("stream did not end: %v", err)
		case ev.Comment == "" && ev.Type != events.TypeDevice:
			t.Fatalf("event before the end: %+v", ev)
		}
	}
}

// wantCut checks that a stream ended by a revocation is cut off without
// another event: the pak breaks the connection (unexpected EOF) or, if no
// write was in progress, ends it.
func wantCut(t *testing.T, s *testutil.SSE) {
	t.Helper()
	for {
		ev, err := s.Next(5 * time.Second)
		switch {
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			return
		case err != nil:
			t.Fatalf("stream not cut: %v", err)
		case ev.Comment == "" && ev.Type != events.TypeDevice:
			t.Fatalf("event after the revocation: %+v", ev)
		}
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestEventsHello(t *testing.T) {
	f := newFixture(t)
	s := testutil.OpenSSE(t, f.client)
	if s.Resp.StatusCode != 200 || s.Resp.Header.Get("Content-Type") != "text/event-stream" || s.Resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v", s.Resp.StatusCode, s.Resp.Header)
	}
	ev := f.nextEvent(s)
	var hello struct {
		ServerTime       time.Time `json:"serverTime"`
		KeepAliveSeconds int       `json:"keepAliveSeconds"`
	}
	json.Unmarshal([]byte(ev.Data), &hello)
	if ev.ID != 1 || ev.Type != "hello" || hello.KeepAliveSeconds != 15 || !hello.ServerTime.Equal(f.clock.Now().UTC()) {
		t.Fatalf("hello %+v %+v", ev, hello)
	}
	if f.server.EventStreams() != 1 {
		t.Fatalf("%d streams", f.server.EventStreams())
	}
	// Closing the connection ends the stream on the pak side.
	s.Resp.Body.Close()
	waitFor(t, "stream closed", func() bool { return f.server.EventStreams() == 0 })
	if KeepAliveInterval != 15*time.Second || DeviceSampleEvery != 5*time.Second {
		t.Error("spec intervals changed")
	}
}

func TestEventsUnsigned(t *testing.T) {
	f := newFixture(t)
	r := f.unsigned("GET", "/api/v1/events", nil)
	f.check("GET", "/events", r)
	if r.Status != http.StatusUnauthorized {
		t.Fatalf("%d", r.Status)
	}
}

func TestEventsKeepAlive(t *testing.T) {
	f := newFixture(t)
	f.server.timeouts.keepAlive = 50 * time.Millisecond
	s := f.openEvents(f.client)
	for i := 0; i < 3; i++ {
		ev, err := s.Next(time.Second)
		if err != nil || ev.Comment != "keep-alive" {
			t.Fatalf("keep-alive %d: %+v %v", i, ev, err)
		}
	}
}

// A change made by another client reaches the stream as a library event.
func TestEventsLibraryAfterAnotherClientWrites(t *testing.T) {
	f := newFixture(t)
	f.fastEvents(events.Options{})
	other := f.pairNamed("iPad")
	s := f.openEvents(other)
	game := gbaFolder + "/Starlight Courier.gba"
	f.do("DELETE", "/api/v1/games/"+id(game), "/games/{gameId}", nil, http.StatusOK)
	ev := f.nextEvent(s)
	if ev.ID < 2 || ev.Type != "library" || ev.Data != `{"systemIds":["`+id(gbaFolder)+`"]}` {
		t.Fatalf("library event %+v", ev)
	}
	// The trash names no system: every system is refreshed.
	f.do("DELETE", "/api/v1/trash", "/trash", nil, http.StatusBadRequest) // no confirmation: nothing changed
	req := f.client.Request(t, "DELETE", "/api/v1/trash", nil)
	req.Header.Set(HeaderConfirm, ConfirmEmptyTrash)
	if r := f.client.Send(t, req); r.Status != http.StatusOK && r.Status != http.StatusNoContent {
		t.Fatalf("empty trash: %d %s", r.Status, r.Body)
	}
	ev = f.nextEvent(s)
	if ev.Data != `{"systemIds":[]}` {
		t.Fatalf("trash event %+v", ev)
	}
}

// changeExempt are the write routes that change nothing visible when they
// answer: their job or a later request publishes the library event.
var changeExempt = map[string]bool{
	"POST " + Prefix + "/uploads":              true, // an upload opens
	"PUT " + Prefix + "/uploads/{uploadId}":    true, // staging area
	"DELETE " + Prefix + "/uploads/{uploadId}": true,
	"DELETE " + Prefix + "/jobs/{jobId}":       true,
	"POST " + Prefix + "/games/{gameId}/patch": true, // the job publishes on success
	// M12: a pak change publishes a paks event, never a library one.
	"DELETE " + Prefix + "/paks/{pakId}": true,
	"POST " + Prefix + "/paks/install":   true, // the job publishes on success
	// M19: the import job publishes on success (its result has a gameId).
	"POST " + Prefix + "/archives/{stagingId}/import": true,
	"DELETE " + Prefix + "/archives/{stagingId}":      true, // the staging area only
	// M22: moving screenshots publishes a screenshots event, not library.
	"POST " + Prefix + "/screenshots/trash": true,
}

// Every write route (M13: every former M10 write route) publishes a library
// event when it succeeds, besides the exempt ones; no other route does.
func TestWriteRoutesPublishLibraryEvents(t *testing.T) {
	f := newFixture(t)
	n := 0
	for _, r := range f.server.routeTable {
		want := r.write != writeNone && !changeExempt[r.pattern]
		if r.library != want {
			t.Errorf("%s: library event %v, want %v", r.pattern, r.library, want)
		}
		if changeExempt[r.pattern] && r.write == writeNone {
			t.Errorf("%s: exempt but not a write route", r.pattern)
		}
		if r.library {
			n++
		}
		if r.pattern == eventsPattern && (r.write != writeNone || r.opts.WhileDisconnected) {
			t.Errorf("%s: options %+v", r.pattern, r.opts)
		}
	}
	if n != 18 { // 17, and the restore from the trash (M18)
		t.Errorf("%d routes publish library events, want 18", n)
	}
}

// A patch job sends job events (queued/running... succeeded) then a
// library event; the staging upload before it sends none.
func TestEventsDuringAPatchJob(t *testing.T) {
	f := newFixture(t)
	f.fastEvents(events.Options{})
	other := f.pairNamed("iPad")
	s := f.openEvents(other)
	game := gbaFolder + "/Starlight Courier.gba"
	src := []byte(strings.Repeat("rom data ", 20_000))
	if err := os.WriteFile(filepath.Join(f.sd, "Roms", filepath.FromSlash(game)), src, 0o644); err != nil {
		t.Fatal(err)
	}
	tgt := append([]byte("patched "), src...)
	stagingID := f.stageFile("hack.bps", testutil.EncodeBPS(src, tgt))
	var accepted struct {
		JobID string `json:"jobId"`
	}
	f.patch(game, map[string]any{"patch": map[string]any{"type": "staging", "stagingId": stagingID}, "output": map[string]any{"mode": "new-file"}}, http.StatusAccepted).JSON(t, &accepted)
	f.waitJob(accepted.JobID)
	var states []string
	lastID := 1
	for {
		ev := f.nextEvent(s)
		if ev.ID <= lastID {
			t.Fatalf("id %d after %d", ev.ID, lastID)
		}
		lastID = ev.ID
		if ev.Type == "library" {
			if ev.Data != `{"systemIds":["`+id(gbaFolder)+`"]}` {
				t.Fatalf("library %s", ev.Data)
			}
			break
		}
		if ev.Type != "job" {
			t.Fatalf("event %+v", ev)
		}
		var j jobState
		json.Unmarshal([]byte(ev.Data), &j)
		if j.Kind != "patch" {
			t.Fatalf("job %s", ev.Data)
		}
		states = append(states, j.State)
	}
	if len(states) < 2 || states[0] != "queued" || states[len(states)-1] != "succeeded" {
		t.Fatalf("job states %v", states)
	}
}

// The pak shutdown sends the shutdown event, then the stream ends, and
// the server stops without waiting for the streams.
func TestEventsShutdown(t *testing.T) {
	for _, reason := range []string{"", events.ReasonQuit} {
		t.Run("reason="+reason, func(t *testing.T) {
			f := newFixture(t)
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- f.server.Serve(ctx, ln) }()
			f.client.BaseURL = "http://" + ln.Addr().String()
			other := f.pairNamed("iPad")
			other.BaseURL = f.client.BaseURL
			s1, s2 := f.openEvents(f.client), f.openEvents(other)

			start := time.Now()
			want := events.ReasonStopped
			if reason != "" {
				f.server.BeginShutdown()
				f.server.CloseEvents(reason)
				want = reason
			}
			cancel()
			for _, s := range []*testutil.SSE{s1, s2} {
				ev := f.nextEvent(s)
				if ev.Type != "shutdown" || ev.Data != `{"reason":"`+want+`"}` {
					t.Fatalf("shutdown event %+v", ev)
				}
				wantEnd(t, s)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Serve did not return")
			}
			if d := time.Since(start); d > 2*time.Second {
				t.Errorf("shutdown took %v", d)
			}
		})
	}
}

// A new stream during the shutdown is refused.
func TestEventsRefusedAfterShutdown(t *testing.T) {
	f := newFixture(t)
	f.server.CloseEvents(events.ReasonStopped)
	r := f.client.Do(t, "GET", "/api/v1/events", nil)
	f.check("GET", "/events", r)
	if r.Status != http.StatusServiceUnavailable {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
}

// Disconnect, Forget, ForgetAll, unpairing and re-pairing end the streams
// of the client, without a shutdown event; the others go on.
func TestEventsClosedOnRevocation(t *testing.T) {
	f := newFixture(t)
	bystander := f.pairNamed("iPad")
	b := f.openEvents(bystander)

	s := f.openEvents(f.client)
	if err := f.server.Disconnect(f.client.ClientID); err != nil {
		t.Fatal(err)
	}
	wantCut(t, s)
	r := f.client.Do(t, "GET", "/api/v1/events", nil)
	f.check("GET", "/events", r)
	if r.Status != http.StatusUnauthorized || r.ErrorCode() != CodeDisconnected {
		t.Fatalf("disconnected client opens a stream: %d %s", r.Status, r.Body)
	}
	f.do("POST", "/api/v1/clients/me/connect", "/clients/me/connect", nil, http.StatusNoContent)

	s = f.openEvents(f.client)
	f.do("DELETE", "/api/v1/clients/me", "/clients/me", nil, http.StatusNoContent)
	wantCut(t, s)

	c := f.pairNamed("Mac")
	s = f.openEvents(c)
	if _, err := f.server.Forget(c.ClientID); err != nil {
		t.Fatal(err)
	}
	wantCut(t, s)

	if f.server.EventStreams() != 1 {
		t.Fatalf("%d streams, want the bystander only", f.server.EventStreams())
	}
	if _, err := f.server.ForgetAll(); err != nil {
		t.Fatal(err)
	}
	wantCut(t, b)
	waitFor(t, "no stream", func() bool { return f.server.EventStreams() == 0 })
}

func TestEventsLimits(t *testing.T) {
	f := newFixture(t)
	clients := []*testutil.Client{f.client}
	for i := 1; i < 4; i++ {
		clients = append(clients, f.pairNamed(fmt.Sprint("client ", i)))
	}
	tooMany := func(c *testutil.Client) {
		t.Helper()
		r := c.Do(t, "GET", "/api/v1/events", nil)
		f.check("GET", "/events", r)
		if r.Status != http.StatusTooManyRequests || r.ErrorCode() != "too_many_attempts" || r.Header.Get("Retry-After") == "" {
			t.Fatalf("want 429 with Retry-After, got %d %v %s", r.Status, r.Header, r.Body)
		}
	}
	var first *testutil.SSE
	for i := 0; i < 4; i++ {
		s := f.openEvents(f.client)
		if first == nil {
			first = s
		}
	}
	tooMany(f.client)
	for _, c := range clients[1:] {
		for i := 0; i < 4; i++ {
			f.openEvents(c)
		}
	}
	tooMany(f.pairNamed("seventeenth"))
	// A closed stream frees its place.
	first.Resp.Body.Close()
	waitFor(t, "a free place", func() bool { return f.server.EventStreams() == 15 })
	f.openEvents(f.client)
}

// Another client opens the stream and gets the changes of the first one;
// opening it never waits for a write in progress (M13).
func TestEventsOtherClient(t *testing.T) {
	f := newFixture(t)
	f.fastEvents(events.Options{})
	reader := f.pairNamed("iPad")
	if !f.server.writes().TryAcquire() {
		t.Fatal("write lock held")
	}
	s := f.openEvents(reader)
	f.server.writes().Release()
	f.do("DELETE", "/api/v1/games/"+id(gbaFolder+"/Starlight Courier.gba"), "/games/{gameId}", nil, http.StatusOK)
	if ev := f.nextEvent(s); ev.Type != "library" {
		t.Fatalf("%+v", ev)
	}
}

// fastDevice samples the device every 20 ms, with at most one device
// event per rate for each stream; the result of each sample (changed)
// goes to the returned channel.
func (f *fixture) fastDevice(rate time.Duration) chan bool {
	f.server.timeouts.deviceSample = 20 * time.Millisecond
	f.server.timeouts.deviceRate = rate
	f.devices = true
	samples := make(chan bool, 10000)
	hook := func(changed bool) {
		select {
		case samples <- changed:
		default:
		}
	}
	f.server.sampleHook.Store(&hook)
	return samples
}

// waitSample waits for a sample that marked the streams (changed) or,
// with changed false, for any sample.
func waitSample(t *testing.T, samples chan bool, changed bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case c := <-samples:
			if c || !changed {
				return
			}
		case <-deadline:
			t.Fatal("no device sample")
		}
	}
}

func TestEventsDevice(t *testing.T) {
	f := newFixture(t)
	f.fastEvents(events.Options{})
	samples := f.fastDevice(0)
	s := f.openEvents(f.client)
	waitSample(t, samples, false) // the reference
	// Small changes of the free space are ignored.
	f.storage.Add(-1000)
	if ev, err := s.NextEvent(200 * time.Millisecond); !errors.Is(err, testutil.ErrSSETimeout) {
		t.Fatalf("event for a small change: %+v %v", ev, err)
	}
	f.storage.Add(-2 << 20)
	ev := f.nextEvent(s)
	var d struct {
		Battery          *struct{ Percent int } `json:"battery"`
		ConnectedClients []ConnectedClient      `json:"connectedClients"`
		Storage          []storageVolume        `json:"storage"`
	}
	json.Unmarshal([]byte(ev.Data), &d)
	if ev.Type != "device" || d.Battery != nil || len(d.Storage) != 1 || d.Storage[0].FreeBytes != f.storage.Load() || len(d.ConnectedClients) != 1 {
		t.Fatalf("device %+v", ev)
	}
	// The battery.
	dir := filepath.Join(f.sd, "no-sysfs", "axp2202-battery")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "capacity"), []byte("80\n"), 0o644)
	if ev := f.nextEvent(s); ev.Type != "device" || !strings.Contains(ev.Data, `"battery":{"percent":80,"charging":false}`) {
		t.Fatalf("battery %+v", ev)
	}
	// Another client connects, then stops being connected; the client
	// of the stream stays connected (M14).
	other := f.pairNamed("iPad")
	f.as(other, "GET", "/api/v1/systems", "/systems", nil, http.StatusOK)
	if ev := f.nextEvent(s); ev.Type != "device" || !strings.Contains(ev.Data, `"clientName":"iPad"`) {
		t.Fatalf("clients %+v", ev)
	}
	// Several marks (the request, its presence, a sample) may each give
	// an event with the iPad, built and written before the clock moves:
	// they are skipped. An event is built when it is written, so once the
	// clock moved no event lists the iPad, and the sampler, which sees it
	// leave, marks the stream for the event that must come (no timing).
	f.clock.Advance(ConnectedWindow + time.Second)
	for n := 0; ; n++ {
		ev := f.nextEvent(s)
		if ev.Type != "device" {
			t.Fatalf("event %+v", ev)
		}
		if !strings.Contains(ev.Data, `"iPad"`) {
			if !strings.Contains(ev.Data, `"clientName":"Test iPhone"`) {
				t.Fatalf("clients %+v", ev)
			}
			break
		}
		if n == 10 {
			t.Fatalf("the iPad is still connected: %+v", ev)
		}
	}
	// No sampling once nobody listens.
	s.Resp.Body.Close()
	waitFor(t, "sampler stopped", func() bool {
		f.server.sampler.mu.Lock()
		defer f.server.sampler.mu.Unlock()
		return f.server.sampler.stop == nil
	})
}

// A client that stops reading is cut off after the write deadline; the
// producers and the other streams never wait for it.
func TestEventsSlowClientIsCut(t *testing.T) {
	f := newFixture(t)
	f.fastEvents(events.Options{Buffer: 512})
	f.server.timeouts.sendIdle = 200 * time.Millisecond
	addr := strings.TrimPrefix(f.http.URL, "http://")
	conn := dial(t, addr)
	defer conn.Close()
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetReadBuffer(4096)
	}
	conn.Write(signedRaw(t, f, "GET", "/api/v1/events", nil)) // and never reads
	waitFor(t, "slow stream open", func() bool { return f.server.EventStreams() == 1 })
	other := f.pairNamed("iPad")
	s := f.openEvents(other)

	big := strings.Repeat("y", 64<<10)
	const n = 300
	produced := make(chan struct{})
	go func() {
		defer close(produced)
		for i := 0; i < n; i++ {
			f.server.events.Job(fmt.Sprint("job_", i), "queued", false, map[string]any{"i": i, "pad": big})
			time.Sleep(time.Millisecond)
		}
	}()
	for i := 0; i < n; i++ {
		ev, err := s.NextEvent(5 * time.Second)
		if err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
		var v struct{ I int }
		json.Unmarshal([]byte(ev.Data), &v)
		if v.I != i {
			t.Fatalf("event %d is %d", i, v.I)
		}
	}
	select {
	case <-produced:
	case <-time.After(5 * time.Second):
		t.Fatal("the producer waited")
	}
	waitFor(t, "slow stream cut", func() bool { return f.server.EventStreams() == 1 })
	if !strings.Contains(f.logs.String(), "event stream cut") && !strings.Contains(f.logs.String(), "too far behind") {
		t.Errorf("cut not logged")
	}
}

func TestSystemOfGame(t *testing.T) {
	for in, want := range map[string]string{
		id(gbaFolder + "/Hacks/x.gba"): id(gbaFolder),
		id(gbaFolder + "/x.gba"):       id(gbaFolder),
		"not/an id":                    "",
		"":                             "",
	} {
		if got := systemOfGame(in); got != want {
			t.Errorf("systemOfGame(%q) = %q, want %q", in, got, want)
		}
	}
	_ = library.EncodeID
}

// connectedNames returns the connected clients of GET /device and of the
// console's Devices screen.
func (f *fixture) connectedNames() (api, console []string) {
	f.t.Helper()
	for _, c := range f.server.ConnectedClients() {
		api = append(api, c.ClientName)
	}
	for _, d := range f.server.Devices() {
		if d.Connected {
			console = append(console, d.Name)
		}
	}
	return api, console
}

func (f *fixture) wantConnected(want string) {
	f.t.Helper()
	api, console := f.connectedNames()
	if fmt.Sprint(api) != want || fmt.Sprint(console) != want {
		f.t.Fatalf("connected: api %v, console %v, want %s", api, console, want)
	}
}

// An open event stream counts as presence (M14): the client is connected
// whatever the time since its last request; after its last stream closes,
// the 30 s rule starts again; a revocation removes it at once.
func TestEventStreamCountsAsPresence(t *testing.T) {
	f := newFixture(t)
	c := f.pairNamed("iPad")
	s1, s2 := f.openEvents(c), f.openEvents(c)
	f.clock.Advance(10 * ConnectedWindow)
	f.wantConnected("[iPad]")
	var dev struct {
		ConnectedClients []ConnectedClient `json:"connectedClients"`
	}
	f.get("/api/v1/device", "/device", http.StatusOK).JSON(t, &dev)
	if len(dev.ConnectedClients) != 2 || dev.ConnectedClients[0].ClientName != "Test iPhone" && dev.ConnectedClients[1].ClientName != "Test iPhone" {
		t.Fatalf("GET /device %+v", dev)
	}
	for _, cc := range dev.ConnectedClients {
		if cc.ClientName == "iPad" && !cc.LastSeenAt.Equal(f.clock.Now().UTC()) {
			t.Errorf("lastSeenAt of a streaming client %v, want now", cc.LastSeenAt)
		}
	}
	f.clock.Advance(10 * ConnectedWindow) // the caller ages out
	f.wantConnected("[iPad]")

	// One stream left: still connected.
	s1.Resp.Body.Close()
	waitFor(t, "one stream", func() bool { return f.server.EventStreams() == 1 })
	f.clock.Advance(10 * ConnectedWindow)
	f.wantConnected("[iPad]")
	// The last one closes: 30 s from now.
	s2.Resp.Body.Close()
	waitFor(t, "no stream", func() bool { return f.server.EventStreams() == 0 })
	f.clock.Advance(ConnectedWindow - time.Second)
	f.wantConnected("[iPad]")
	f.clock.Advance(2 * time.Second)
	f.wantConnected("[]")

	// Disconnect removes the presence at once, and the end of the stream
	// does not bring it back.
	s := f.openEvents(c)
	f.wantConnected("[iPad]")
	if err := f.server.Disconnect(c.ClientID); err != nil {
		t.Fatal(err)
	}
	f.wantConnected("[]")
	wantCut(t, s)
	waitFor(t, "no stream", func() bool { return f.server.EventStreams() == 0 })
	f.wantConnected("[]")
	for _, d := range f.server.Devices() {
		if d.Name == "iPad" && (!d.Disconnected || d.Connected) {
			t.Fatalf("device %+v", d)
		}
	}

	// Forget too; a stream opened after a reconnection is counted anew.
	f.as(c, "POST", "/api/v1/clients/me/connect", "/clients/me/connect", nil, http.StatusNoContent)
	s = f.openEvents(c)
	f.clock.Advance(10 * ConnectedWindow)
	f.wantConnected("[iPad]")
	if _, err := f.server.Forget(c.ClientID); err != nil {
		t.Fatal(err)
	}
	f.wantConnected("[]")
	wantCut(t, s)
}

// takeHook makes the streams stop after taking an event of type typ (once)
// until release is closed; taken is closed then.
func (f *fixture) takeHook(typ string) (taken, release chan struct{}) {
	taken, release = make(chan struct{}), make(chan struct{})
	var once sync.Once
	hook := func(ev events.Event) {
		if ev.Type == typ {
			once.Do(func() {
				close(taken)
				<-release
			})
		}
	}
	f.server.streamHook.Store(&hook)
	return taken, release
}

// A revocation wins over an event the stream already took (and over the
// events still queued): nothing is written after Disconnect returned.
func TestEventsRevocationWithAnEventQueued(t *testing.T) {
	f := newFixture(t)
	f.fastEvents(events.Options{})
	c := f.pairNamed("iPad")
	s := f.openEvents(c)
	taken, release := f.takeHook(events.TypeLibrary)
	f.server.events.Library(id(gbaFolder))
	<-taken
	f.server.events.Job("job_000000000000000000000001", "queued", false, map[string]any{"queued": true})
	if err := f.server.Disconnect(c.ClientID); err != nil {
		t.Fatal(err)
	}
	close(release)
	wantCut(t, s)
	waitFor(t, "stream gone", func() bool { return f.server.EventStreams() == 0 })
}

// slowStream opens a stream on a raw connection that never reads, and
// fills it until its write blocks (the stream stops taking events);
// readers is the number of other open streams, which take every event.
func (f *fixture) slowStream(addr string, n, readers int) net.Conn {
	f.t.Helper()
	conn := dial(f.t, addr)
	f.t.Cleanup(func() { conn.Close() })
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetReadBuffer(4096)
	}
	before := f.server.EventStreams()
	conn.Write(signedRaw(f.t, f, "GET", "/api/v1/events", nil))
	waitFor(f.t, "slow stream open", func() bool { return f.server.EventStreams() == before+1 })
	var taken atomic.Int64
	hook := func(events.Event) { taken.Add(1) }
	f.server.streamHook.Store(&hook)
	big := strings.Repeat("y", 64<<10)
	for i := 0; i < n; i++ {
		f.server.events.Job(fmt.Sprint("job_", i), "queued", false, map[string]any{"i": i, "pad": big})
	}
	// Blocked: it stopped taking events before the last one.
	last, still := int64(-1), 0
	waitFor(f.t, "a blocked write", func() bool {
		time.Sleep(20 * time.Millisecond)
		cur := taken.Load()
		if cur == last && cur < int64(n*(readers+1)) {
			still++
		} else {
			still = 0
		}
		last = cur
		return still >= 10
	})
	f.server.streamHook.Store(nil)
	return conn
}

// A revocation interrupts a write in progress: the stream ends at once,
// not after the write deadline (60 s here), and its place in the limits
// is kept until then.
func TestEventsRevocationInterruptsABlockedWrite(t *testing.T) {
	f := newFixture(t)
	f.fastEvents(events.Options{Buffer: 512})
	f.slowStream(strings.TrimPrefix(f.http.URL, "http://"), 300, 0)
	start := time.Now()
	if err := f.server.Disconnect(f.client.ClientID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "stream gone", func() bool { return f.server.EventStreams() == 0 })
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("stream cut after %v", d)
	}
}

// A device change marked while a stream is busy writing another event,
// then a revocation: the device event is built when it is written, so it
// never shows the revoked client again.
func TestEventsDeviceAfterARevocation(t *testing.T) {
	f := newFixture(t)
	f.fastEvents(events.Options{})
	samples := f.fastDevice(0)
	b := f.pairNamed("iPad")
	f.as(b, "GET", "/api/v1/systems", "/systems", nil, http.StatusOK)
	s := f.openEvents(f.client)
	waitSample(t, samples, false) // the reference, with B connected
	taken, release := f.takeHook(events.TypeLibrary)
	f.server.events.Library(id(gbaFolder))
	<-taken // the stream is busy with the library event
	f.storage.Add(-2 << 20)
	waitSample(t, samples, true) // the stream is marked: B still connected
	if err := f.server.Disconnect(b.ClientID); err != nil {
		t.Fatal(err)
	}
	close(release)
	if ev := f.nextEvent(s); ev.Type != "library" {
		t.Fatalf("%+v", ev)
	}
	ev := f.nextEvent(s)
	if ev.Type != "device" || strings.Contains(ev.Data, `"iPad"`) || !strings.Contains(ev.Data, `"freeBytes":`+fmt.Sprint(f.storage.Load())) {
		t.Fatalf("device event after the revocation %+v", ev)
	}
}

// Each stream gets at most one device event per interval, whatever the
// samples: changes within it are sent once, at its end.
func TestEventsDeviceRate(t *testing.T) {
	f := newFixture(t)
	f.fastEvents(events.Options{})
	const rate = 300 * time.Millisecond
	samples := f.fastDevice(rate)
	s := f.openEvents(f.client)
	waitSample(t, samples, false)
	stop := make(chan struct{})
	go func() { // a change at every sample
		for {
			select {
			case <-stop:
				return
			case <-samples:
				f.storage.Add(-2 << 20)
			}
		}
	}()
	var times []time.Time
	for len(times) < 4 {
		if ev := f.nextEvent(s); ev.Type == "device" {
			times = append(times, time.Now())
		}
	}
	close(stop)
	for i := 1; i < len(times); i++ {
		// The receive times jitter a little around the send times.
		if gap := times[i].Sub(times[i-1]); gap < rate-50*time.Millisecond {
			t.Errorf("device events %v apart, want at least %v", gap, rate)
		}
	}
}

// Unpairing during the shutdown (a request accepted just before it)
// still stops a stream writing its last events: revocation wins over
// the shutdown event.
func TestEventsRevocationDuringShutdown(t *testing.T) {
	f := newFixture(t)
	f.fastEvents(events.Options{})
	c := f.pairNamed("iPad")
	s := f.openEvents(c)
	taken, release := f.takeHook(events.TypeLibrary)
	f.server.events.Library(id(gbaFolder))
	<-taken
	f.server.events.Job("job_000000000000000000000001", "queued", false, map[string]any{"queued": true})
	f.server.CloseEvents(events.ReasonQuit)
	if _, err := f.server.Forget(c.ClientID); err != nil {
		t.Fatal(err)
	}
	close(release)
	wantCut(t, s)
	waitFor(t, "stream gone", func() bool { return f.server.EventStreams() == 0 })
}

// At shutdown, a stream whose write is blocked (the client stopped
// reading) does not delay the server: the other streams get the shutdown
// event and Serve returns within about the shutdown write budget.
func TestEventsShutdownWithASlowClient(t *testing.T) {
	f := newFixture(t)
	f.fastEvents(events.Options{Buffer: 512})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.server.Serve(ctx, ln) }()
	f.client.BaseURL = "http://" + ln.Addr().String()
	reader := f.pairNamed("iPad")
	reader.BaseURL = f.client.BaseURL
	s := f.openEvents(reader)
	gotShutdown := make(chan bool, 1)
	go func() { // the reader keeps up
		for {
			ev, err := s.NextEvent(10 * time.Second)
			if err != nil {
				gotShutdown <- false
				return
			}
			if ev.Type == "shutdown" {
				gotShutdown <- true
				return
			}
		}
	}()
	f.slowStream(ln.Addr().String(), 300, 1)
	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("shutdown took %v", d)
	}
	if !<-gotShutdown {
		t.Fatal("the reader did not get the shutdown event")
	}
}

// A client that becomes connected, then is revoked, without any sample in
// between: each presence change marks the streams, so the device events
// follow it.
func TestEventsDeviceFollowsPresence(t *testing.T) {
	f := newFixture(t)
	f.fastEvents(events.Options{})
	samples := f.fastDevice(0)
	f.server.timeouts.deviceSample = time.Hour // the reference only
	s := f.openEvents(f.client)
	waitSample(t, samples, false)
	b := f.pairNamed("iPad")
	f.as(b, "GET", "/api/v1/systems", "/systems", nil, http.StatusOK)
	if ev := f.nextEvent(s); ev.Type != "device" || !strings.Contains(ev.Data, `"clientName":"iPad"`) {
		t.Fatalf("after B connected: %+v", ev)
	}
	if err := f.server.Disconnect(b.ClientID); err != nil {
		t.Fatal(err)
	}
	if ev := f.nextEvent(s); ev.Type != "device" || strings.Contains(ev.Data, `"iPad"`) {
		t.Fatalf("after B was disconnected: %+v", ev)
	}
}

// The interval between two device events of a stream runs from the
// write: building the first one may wait (for the access lock).
func TestEventsDeviceRateFromTheWrite(t *testing.T) {
	f := newFixture(t)
	f.fastEvents(events.Options{})
	const rate = 300 * time.Millisecond
	samples := f.fastDevice(rate)
	f.server.timeouts.deviceSample = time.Hour
	var once sync.Once
	slow := func() { once.Do(func() { time.Sleep(250 * time.Millisecond) }) }
	f.server.deviceHook.Store(&slow)
	s := f.openEvents(f.client)
	waitSample(t, samples, false)
	b := f.pairNamed("iPad")
	f.as(b, "GET", "/api/v1/systems", "/systems", nil, http.StatusOK)
	if ev := f.nextEvent(s); ev.Type != "device" {
		t.Fatalf("%+v", ev)
	}
	first := time.Now()
	if err := f.server.Disconnect(b.ClientID); err != nil {
		t.Fatal(err)
	}
	if ev := f.nextEvent(s); ev.Type != "device" {
		t.Fatalf("%+v", ev)
	}
	if gap := time.Since(first); gap < rate-50*time.Millisecond {
		t.Fatalf("device events %v apart, want at least %v", gap, rate)
	}
}

// A device event built before the first sample of the run reads the
// state itself and sets the reference: a first sample that sees another
// state marks the streams, so the stream is corrected.
func TestEventsDeviceBeforeTheFirstSample(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var armed atomic.Bool
	armed.Store(true)
	f := newFixture(t, func(o *Options) {
		inner := o.Storage
		o.Storage = func(p string) (device.Storage, error) {
			if armed.CompareAndSwap(true, false) { // the first sample
				close(entered)
				<-release
			}
			return inner(p)
		}
	})
	f.fastEvents(events.Options{})
	samples := f.fastDevice(0)
	f.server.timeouts.deviceSample = time.Hour // the first sample only
	s := f.openEvents(f.client)
	<-entered // the first sample waits in its storage read
	b := f.pairNamed("iPad")
	f.as(b, "GET", "/api/v1/systems", "/systems", nil, http.StatusOK)
	before := f.storage.Load()
	ev := f.nextEvent(s) // built with its own read
	if ev.Type != "device" || !strings.Contains(ev.Data, `"freeBytes":`+fmt.Sprint(before)) {
		t.Fatalf("first device event %+v", ev)
	}
	f.storage.Add(-2 << 20)
	close(release) // the first sample sees the new space
	waitSample(t, samples, true)
	if ev := f.nextEvent(s); ev.Type != "device" || !strings.Contains(ev.Data, `"freeBytes":`+fmt.Sprint(f.storage.Load())) {
		t.Fatalf("corrected device event %+v", ev)
	}
}
