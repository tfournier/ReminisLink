package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func fast() Options {
	return Options{LibraryInterval: 100 * time.Millisecond, JobInterval: 100 * time.Millisecond}
}

// next waits for the next event of s.
func next(t *testing.T, s *Subscription) Event {
	t.Helper()
	select {
	case ev := <-s.C():
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("no event")
		return Event{}
	}
}

// none checks that s receives nothing for d.
func none(t *testing.T, s *Subscription, d time.Duration) {
	t.Helper()
	select {
	case ev := <-s.C():
		t.Fatalf("unexpected event %s %s", ev.Type, ev.Data)
	case <-time.After(d):
	}
}

func subscribe(t *testing.T, h *Hub, client string) *Subscription {
	t.Helper()
	s, err := h.Subscribe(client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func systems(t *testing.T, ev Event) []string {
	t.Helper()
	if ev.Type != TypeLibrary {
		t.Fatalf("event %s, want library", ev.Type)
	}
	var v struct {
		SystemIDs []string `json:"systemIds"`
	}
	if err := json.Unmarshal(ev.Data, &v); err != nil || v.SystemIDs == nil {
		t.Fatalf("library data %s: %v", ev.Data, err)
	}
	return v.SystemIDs
}

func TestLibraryCoalescing(t *testing.T) {
	h := New(fast())
	s := subscribe(t, h, "a")
	h.Library("B")
	if got := systems(t, next(t, s)); fmt.Sprint(got) != "[B]" {
		t.Fatalf("first: %v", got)
	}
	// Within the interval: merged into one event at its end.
	start := time.Now()
	h.Library("C")
	h.Library("A", "C")
	got := systems(t, next(t, s))
	if fmt.Sprint(got) != "[A C]" {
		t.Fatalf("merged: %v", got)
	}
	if d := time.Since(start); d < 50*time.Millisecond {
		t.Errorf("merged event after %v, want the end of the interval", d)
	}
	none(t, s, 150*time.Millisecond)
	// Unknown systems win over the known ones.
	h.Library("A")
	systems(t, next(t, s))
	h.Library("B")
	h.Library()
	if got := systems(t, next(t, s)); len(got) != 0 {
		t.Fatalf("unknown merged: %v", got)
	}
}

func TestNoEventWithoutStreams(t *testing.T) {
	h := New(fast())
	h.Library("A")
	h.Job("j", "running", false, map[string]int{"x": 1})
	s := subscribe(t, h, "a")
	none(t, s, 150*time.Millisecond)
}

// MarkDevice marks every stream once, without blocking.
func TestMarkDevice(t *testing.T) {
	h := New(fast())
	a, b := subscribe(t, h, "a"), subscribe(t, h, "b")
	h.MarkDevice()
	h.MarkDevice()
	for _, s := range []*Subscription{a, b} {
		select {
		case <-s.Device():
		default:
			t.Fatal("not marked")
		}
		select {
		case <-s.Device():
			t.Fatal("marked twice")
		default:
		}
	}
}

// A revocation during the shutdown reaches a stream still writing its
// last events: no shutdown event for it.
func TestRevocationDuringShutdown(t *testing.T) {
	h := New(fast())
	a, b := subscribe(t, h, "a"), subscribe(t, h, "b")
	var reasons []CloseReason
	a.OnEnd(func(r CloseReason) { reasons = append(reasons, r) })
	h.Shutdown(ReasonQuit)
	if n := h.CloseClient("a"); n != 1 {
		t.Fatalf("closed %d", n)
	}
	if fmt.Sprint(reasons) != fmt.Sprint([]CloseReason{Shutdown, Revoked}) {
		t.Fatalf("OnEnd %v", reasons)
	}
	if _, ok := a.Final(); ok || a.Reason() != Revoked {
		t.Fatalf("a keeps its shutdown event: %v", a.Reason())
	}
	if _, ok := b.Final(); !ok {
		t.Fatal("b lost its shutdown event")
	}
}

type job struct {
	State string `json:"state"`
	Done  int    `json:"done"`
}

func jobOf(t *testing.T, ev Event) job {
	t.Helper()
	if ev.Type != TypeJob {
		t.Fatalf("event %s, want job", ev.Type)
	}
	var j job
	if err := json.Unmarshal(ev.Data, &j); err != nil {
		t.Fatal(err)
	}
	return j
}

func TestJobCoalescing(t *testing.T) {
	h := New(fast())
	s := subscribe(t, h, "a")
	h.Job("j1", "queued", false, job{"queued", 0})
	h.Job("j1", "running", false, job{"running", 0})
	for i := 1; i <= 10; i++ {
		h.Job("j1", "running", false, job{"running", i})
	}
	// Another job is throttled on its own.
	h.Job("j2", "queued", false, job{"queued", 0})
	if j := jobOf(t, next(t, s)); j.State != "queued" {
		t.Fatalf("%+v", j)
	}
	if j := jobOf(t, next(t, s)); j.State != "running" || j.Done != 0 {
		t.Fatalf("%+v", j)
	}
	if j := jobOf(t, next(t, s)); j.State != "queued" {
		t.Fatalf("j2: %+v", j)
	}
	// The progress is merged: the latest one, at the end of the interval.
	if j := jobOf(t, next(t, s)); j.State != "running" || j.Done != 10 {
		t.Fatalf("progress: %+v", j)
	}
	// A state change goes at once and replaces a pending progress.
	h.Job("j1", "running", false, job{"running", 11})
	h.Job("j1", "succeeded", true, job{"succeeded", 12})
	if j := jobOf(t, next(t, s)); j.State != "succeeded" || j.Done != 12 {
		t.Fatalf("final: %+v", j)
	}
	none(t, s, 150*time.Millisecond)
	h.mu.Lock()
	_, kept := h.jobs["j1"]
	h.mu.Unlock()
	if kept {
		t.Error("a finished job is still tracked")
	}
}

func TestOrderAndFanOut(t *testing.T) {
	h := New(Options{JobInterval: time.Hour})
	a, b := subscribe(t, h, "a"), subscribe(t, h, "b")
	for i := 0; i < 20; i++ {
		h.Job(fmt.Sprint("j", i), "queued", false, job{"queued", i})
	}
	for _, s := range []*Subscription{a, b} {
		for i := 0; i < 20; i++ {
			if j := jobOf(t, next(t, s)); j.Done != i {
				t.Fatalf("event %d: %+v", i, j)
			}
		}
	}
}

// A subscriber that does not read is closed once its buffer is full; the
// producer never waits and the others get everything.
func TestSlowSubscriberDropped(t *testing.T) {
	h := New(Options{Buffer: 8, JobInterval: time.Hour})
	slow, fastSub := subscribe(t, h, "slow"), subscribe(t, h, "fast")
	got := make(chan int, 100)
	go func() {
		for ev := range fastSub.C() {
			var j job
			_ = json.Unmarshal(ev.Data, &j)
			got <- j.Done
			if j.Done == 49 {
				return
			}
		}
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			h.Job(fmt.Sprint("j", i), "queued", false, job{"queued", i})
			time.Sleep(time.Millisecond)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the producer was blocked")
	}
	select {
	case <-slow.Done():
	case <-time.After(time.Second):
		t.Fatal("the slow subscriber was not closed")
	}
	if slow.Reason() != Lagging {
		t.Errorf("reason %v", slow.Reason())
	}
	for i := 0; i < 50; i++ {
		select {
		case n := <-got:
			if n != i {
				t.Fatalf("fast subscriber: event %d is %d", i, n)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("fast subscriber stopped at %d", i)
		}
	}
	// Its place is kept until its owner closes it.
	if h.Count() != 2 {
		t.Errorf("%d streams", h.Count())
	}
	slow.Close()
	if h.Count() != 1 {
		t.Errorf("%d streams after Close", h.Count())
	}
}

func TestLimits(t *testing.T) {
	h := New(Options{MaxPerClient: 2, MaxTotal: 3})
	a1 := subscribe(t, h, "a")
	subscribe(t, h, "a")
	if _, err := h.Subscribe("a"); !errors.Is(err, ErrTooManyStreams) {
		t.Fatalf("third of a: %v", err)
	}
	subscribe(t, h, "b")
	if _, err := h.Subscribe("c"); !errors.Is(err, ErrTooManyStreams) {
		t.Fatalf("beyond the total: %v", err)
	}
	a1.Close()
	a1.Close() // twice is fine
	subscribe(t, h, "c")
}

func TestCloseClient(t *testing.T) {
	h := New(Options{})
	a1, a2, b := subscribe(t, h, "a"), subscribe(t, h, "a"), subscribe(t, h, "b")
	if n := h.CloseClient("a"); n != 2 {
		t.Fatalf("closed %d", n)
	}
	for _, s := range []*Subscription{a1, a2} {
		select {
		case <-s.Done():
		default:
			t.Fatal("not closed")
		}
		if s.Reason() != Revoked {
			t.Errorf("reason %v", s.Reason())
		}
	}
	select {
	case <-b.Done():
		t.Fatal("b closed")
	default:
	}
	// The client can open streams again (it reconnected).
	subscribe(t, h, "a")
}

func TestShutdown(t *testing.T) {
	h := New(fast())
	s := subscribe(t, h, "a")
	h.Library("A")
	h.Library("B") // pending: sent before the shutdown event
	if !h.Shutdown(ReasonQuit) {
		t.Fatal("first shutdown")
	}
	if h.Shutdown(ReasonStopped) {
		t.Fatal("second shutdown did something")
	}
	<-s.Done()
	if s.Reason() != Shutdown {
		t.Fatalf("reason %v", s.Reason())
	}
	if got := systems(t, next(t, s)); fmt.Sprint(got) != "[A]" {
		t.Fatal(got)
	}
	if got := systems(t, next(t, s)); fmt.Sprint(got) != "[B]" {
		t.Fatal(got)
	}
	final, ok := s.Final()
	if !ok || final.Type != TypeShutdown || string(final.Data) != `{"reason":"quit"}` {
		t.Fatalf("final %v %s %s", ok, final.Type, final.Data)
	}
	if _, err := h.Subscribe("a"); !errors.Is(err, ErrClosed) {
		t.Fatalf("subscribe after shutdown: %v", err)
	}
}

func TestOnActive(t *testing.T) {
	var mu sync.Mutex
	var calls []bool
	h := New(Options{OnActive: func(a bool) {
		mu.Lock()
		calls = append(calls, a)
		mu.Unlock()
	}})
	a, _ := h.Subscribe("a")
	b, _ := h.Subscribe("b")
	a.Close()
	b.Close()
	c, _ := h.Subscribe("c")
	h.Shutdown(ReasonStopped)
	_ = c
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(calls) != "[true false true false]" {
		t.Fatalf("calls %v", calls)
	}
}

// Producers and subscribers from many goroutines (run with -race).
func TestConcurrentUse(t *testing.T) {
	h := New(Options{LibraryInterval: time.Millisecond, JobInterval: time.Millisecond, Buffer: 4})
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				default:
				}
				h.Library(fmt.Sprint(i))
				h.MarkDevice()
				h.Job(fmt.Sprint("j", i), fmt.Sprint(n%3), n%7 == 0, n)
			}
		}(i)
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for k := 0; k < 20; k++ {
				s, err := h.Subscribe(fmt.Sprint("c", i%3))
				if err != nil {
					continue
				}
				for n := 0; n < 5; n++ {
					select {
					case <-s.C():
					case <-s.Done():
					case <-time.After(10 * time.Millisecond):
					}
				}
				s.Close()
			}
		}(i)
	}
	time.Sleep(200 * time.Millisecond)
	h.Shutdown(ReasonStopped)
	close(stop)
	wg.Wait()
}

// A subscription ended by the hub keeps its place in the limits until its
// owner closes it (its stream may still be cut off); OnEnd tells the
// owner at once, even when set after the end.
func TestEndedSubscriptionKeepsItsPlace(t *testing.T) {
	h := New(Options{MaxPerClient: 1})
	s, _ := h.Subscribe("a")
	var got []CloseReason
	s.OnEnd(func(r CloseReason) { got = append(got, r) })
	h.CloseClient("a")
	if fmt.Sprint(got) != fmt.Sprint([]CloseReason{Revoked}) {
		t.Fatalf("OnEnd %v", got)
	}
	if _, err := h.Subscribe("a"); !errors.Is(err, ErrTooManyStreams) {
		t.Fatalf("subscribe while the old stream runs: %v", err)
	}
	if h.Count() != 1 || h.Active() {
		t.Fatalf("count %d active %v", h.Count(), h.Active())
	}
	late := false
	s.OnEnd(func(r CloseReason) { late = r == Revoked })
	if !late {
		t.Error("OnEnd set after the end not called")
	}
	s.Close()
	if h.Count() != 0 {
		t.Fatalf("count %d after Close", h.Count())
	}
	s2 := subscribe(t, h, "a")
	called := false
	s2.OnEnd(func(CloseReason) { called = true })
	s2.Close()
	if called {
		t.Error("OnEnd called for Close")
	}
}

// Paks events (M12) are merged like the library events and carry {}.
func TestPaksCoalescing(t *testing.T) {
	h := New(fast())
	s := subscribe(t, h, "a")
	h.Paks()
	if ev := next(t, s); ev.Type != TypePaks || string(ev.Data) != "{}" {
		t.Fatalf("first: %s %s", ev.Type, ev.Data)
	}
	h.Paks()
	h.Paks()
	if ev := next(t, s); ev.Type != TypePaks {
		t.Fatalf("merged: %s", ev.Type)
	}
	none(t, s, 150*time.Millisecond)
	// A pending paks event goes before the shutdown.
	h.Paks()
	h.Paks()
	next(t, s)
	h.Shutdown(ReasonQuit)
	if ev := next(t, s); ev.Type != TypePaks {
		t.Fatalf("flushed at shutdown: %s", ev.Type)
	}
}
