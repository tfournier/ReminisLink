// Package events is the hub of the event stream GET /events (M14,
// protocol/openapi.yaml): producers publish device, library and job
// events, the hub coalesces them and hands them to every open stream.
//
// Producers never wait for a stream: each subscriber has a bounded buffer
// and a subscriber whose buffer is full has fallen too far behind, so its
// stream is closed (the app reconnects and reloads what it shows). The
// coalescing is done once, in the hub, before the events reach the
// buffers:
//
//   - library: at most one per LibraryInterval; the systemIds of the
//     changes in between are merged (an empty list, "unknown", wins: the
//     app refreshes everything);
//   - paks (M12): at most one per LibraryInterval, the changes in
//     between merged (the event carries no data);
//   - job: at most one per JobInterval for each job, the latest state
//     winning, except that a change of state (or of cancelRequested) is
//     sent at once;
//   - device: not an event in the buffers. The sampler marks every
//     stream "device dirty" (MarkDevice) and each stream builds the
//     event when it writes it, with the presence of that moment, at most
//     one per interval for that stream.
//
// A subscription ended by the hub (revoked, lagging, shutdown) keeps its
// place in the limits, and stays reachable by CloseClient, until its
// owner calls Close, when its stream is really gone: a client cannot
// open more streams while the old ones are still being cut off, and a
// revocation during the shutdown still stops a stream writing its last
// events.
//
// Event ids are not set here: each stream numbers its own events.
package events

import (
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"
)

// Event types of the stream (protocol/openapi.yaml GET /events).
const (
	TypeHello    = "hello"
	TypeShutdown = "shutdown"
	TypeDevice   = "device"
	TypeLibrary  = "library"
	TypeJob      = "job"
	// TypePaks (M12): the installed paks changed (data: {}).
	TypePaks = "paks"
)

// Reasons of the shutdown event.
const (
	ReasonQuit    = "quit"
	ReasonStopped = "stopped"
)

// Defaults of Options.
const (
	DefaultBuffer          = 256
	DefaultMaxPerClient    = 4
	DefaultMaxTotal        = 16
	DefaultLibraryInterval = time.Second
	DefaultJobInterval     = time.Second
)

// Errors of Subscribe.
var (
	// ErrTooManyStreams: the client already has MaxPerClient streams, or
	// MaxTotal streams are open.
	ErrTooManyStreams = errors.New("events: too many streams")
	// ErrClosed: the hub was shut down.
	ErrClosed = errors.New("events: closed")
)

// Event is one event of the stream: its type and its JSON data.
type Event struct {
	Type string
	Data []byte
}

// CloseReason tells why the hub ended a subscription.
type CloseReason int

// Reasons a subscription ends.
const (
	Open      CloseReason = iota // not closed by the hub
	Lagging                      // its buffer was full: too far behind
	Revoked                      // its client was disconnected, forgotten or unpaired
	Shutdown                     // the pak is closing: Final holds the shutdown event
	Cancelled                    // closed by its owner (Close)
)

// Options configure a Hub; zero values take the defaults.
type Options struct {
	Buffer          int // events buffered per subscriber
	MaxPerClient    int
	MaxTotal        int
	LibraryInterval time.Duration
	JobInterval     time.Duration
	// OnActive is called with true when the first stream opens and false
	// when the last one closes (the device sampler runs in between). It
	// is called with the hub lock held: it must not block nor call the
	// hub.
	OnActive func(active bool)
}

// Hub fans events out to the open streams. Create it with New.
type Hub struct {
	opts Options

	mu     sync.Mutex
	subs   map[*Subscription]struct{} // receiving events
	live   map[*Subscription]struct{} // not yet Closed by their owner
	count  map[string]int             // live streams per client
	closed bool

	library throttle
	libAll  bool                // a pending library change of unknown systems
	libIDs  map[string]struct{} // pending systemIds
	paks    throttle            // paks events (M12)
	jobs    map[string]*jobThrottle
}

// throttle sends at most one event per interval: an event that comes too
// early is kept pending and sent by a timer at the end of the interval.
type throttle struct {
	last    time.Time // when the last event was sent
	pending bool
	timer   *time.Timer
}

type jobThrottle struct {
	throttle
	id       string
	key      string // state and cancelRequested of the last sent event
	data     []byte // pending data
	finished bool   // the job will not change anymore
}

// New returns an empty hub.
func New(opts Options) *Hub {
	if opts.Buffer <= 0 {
		opts.Buffer = DefaultBuffer
	}
	if opts.MaxPerClient <= 0 {
		opts.MaxPerClient = DefaultMaxPerClient
	}
	if opts.MaxTotal <= 0 {
		opts.MaxTotal = DefaultMaxTotal
	}
	if opts.LibraryInterval <= 0 {
		opts.LibraryInterval = DefaultLibraryInterval
	}
	if opts.JobInterval <= 0 {
		opts.JobInterval = DefaultJobInterval
	}
	return &Hub{opts: opts, subs: map[*Subscription]struct{}{}, live: map[*Subscription]struct{}{}, count: map[string]int{},
		libIDs: map[string]struct{}{}, jobs: map[string]*jobThrottle{}}
}

// Subscription is one open stream. Read C until Done is closed; then, if
// Reason is Shutdown, write what is left in C and the Final event.
type Subscription struct {
	hub      *Hub
	clientID string
	ch       chan Event
	done     chan struct{}
	reason   CloseReason // set before done is closed
	final    Event       // the shutdown event (Reason Shutdown)
	onEnd    func(CloseReason)
	device   chan struct{} // "device dirty" (capacity 1)
}

// Device is signalled when the device state changed (MarkDevice): the
// stream then builds and writes a device event.
func (s *Subscription) Device() <-chan struct{} { return s.device }

// OnEnd sets the function called, with the hub lock held, when the hub
// ends the subscription (Lagging, Revoked or Shutdown; not Close): the
// stream interrupts its write in progress. If the hub already ended it,
// fn is called at once. fn must not block nor call the hub.
func (s *Subscription) OnEnd(fn func(CloseReason)) {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	s.onEnd = fn
	if s.reason != Open && s.reason != Cancelled {
		fn(s.reason)
	}
}

// C delivers the events in order.
func (s *Subscription) C() <-chan Event { return s.ch }

// Done is closed when the hub ends the subscription (or Close is called).
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Reason tells why the subscription ended; read it after Done is closed.
func (s *Subscription) Reason() CloseReason {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	return s.reason
}

// Final returns the shutdown event, when Reason is Shutdown.
func (s *Subscription) Final() (Event, bool) {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	return s.final, s.reason == Shutdown
}

// Close ends the subscription (its stream is gone) and frees its place in
// the limits. Safe to call more than once and after the hub ended it.
func (s *Subscription) Close() {
	h := s.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	h.removeLocked(s, Cancelled)
	if _, ok := h.live[s]; ok {
		delete(h.live, s)
		if h.count[s.clientID]--; h.count[s.clientID] <= 0 {
			delete(h.count, s.clientID)
		}
	}
}

// Subscribe opens a stream for a client.
func (h *Hub) Subscribe(clientID string) (*Subscription, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case h.closed:
		return nil, ErrClosed
	case len(h.live) >= h.opts.MaxTotal, h.count[clientID] >= h.opts.MaxPerClient:
		return nil, ErrTooManyStreams
	}
	s := &Subscription{hub: h, clientID: clientID, ch: make(chan Event, h.opts.Buffer), done: make(chan struct{}),
		device: make(chan struct{}, 1)}
	h.subs[s] = struct{}{}
	h.live[s] = struct{}{}
	h.count[clientID]++
	if len(h.subs) == 1 && h.opts.OnActive != nil {
		h.opts.OnActive(true)
	}
	return s, nil
}

// removeLocked ends a subscription (no-op when already ended).
func (h *Hub) removeLocked(s *Subscription, reason CloseReason) {
	if _, ok := h.subs[s]; !ok {
		return
	}
	delete(h.subs, s)
	s.reason = reason
	close(s.done)
	if reason != Cancelled && s.onEnd != nil {
		s.onEnd(reason)
	}
	if len(h.subs) == 0 {
		h.idleLocked()
		if h.opts.OnActive != nil {
			h.opts.OnActive(false)
		}
	}
}

// idleLocked drops the pending events once nobody listens.
func (h *Hub) idleLocked() {
	h.library.stop()
	h.paks.stop()
	h.libAll = false
	clear(h.libIDs)
	for id, j := range h.jobs {
		j.stop()
		delete(h.jobs, id)
	}
}

func (t *throttle) stop() {
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	t.pending = false
}

// Count returns the number of open streams: not yet Closed by their
// owner, even if the hub ended them.
func (h *Hub) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.live)
}

// Active reports whether at least one stream receives events: producers
// can skip building their events otherwise.
func (h *Hub) Active() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs) > 0
}

// CloseClient ends the streams of a client (disconnected, forgotten or
// unpaired) and returns how many there were. It never blocks.
func (h *Hub) CloseClient(clientID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for s := range h.live {
		if s.clientID != clientID {
			continue
		}
		n++
		switch {
		case s.reason == Open:
			h.removeLocked(s, Revoked)
		case s.reason == Shutdown:
			// Still writing its last events: the revocation wins, no
			// shutdown event.
			s.reason, s.final = Revoked, Event{}
			if s.onEnd != nil {
				s.onEnd(Revoked)
			}
		}
	}
	return n
}

// MarkDevice tells every stream that the device state changed. It never
// blocks.
func (h *Hub) MarkDevice() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		select {
		case s.device <- struct{}{}:
		default: // already marked
		}
	}
}

// Shutdown refuses new streams and ends every stream with the shutdown
// event (reason ReasonQuit or ReasonStopped), after the pending events.
// Only the first call does something; it returns whether it was the
// first.
func (h *Hub) Shutdown(reason string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	// What was waiting for the end of an interval goes first, without
	// waiting for it (documented in 04).
	h.flushLibraryLocked()
	h.flushPaksLocked()
	for _, j := range h.jobs {
		h.flushJobLocked(j)
	}
	h.closed = true
	data, _ := json.Marshal(struct {
		Reason string `json:"reason"`
	}{reason})
	final := Event{Type: TypeShutdown, Data: data}
	for s := range h.subs {
		s.final = final
		h.removeLocked(s, Shutdown)
	}
	h.idleLocked()
	return true
}

// broadcastLocked hands an event to every stream; a stream whose buffer
// is full is closed (Lagging).
func (h *Hub) broadcastLocked(ev Event) {
	for s := range h.subs {
		select {
		case s.ch <- ev:
		default:
			h.removeLocked(s, Lagging)
		}
	}
}

// ready reports whether an event of this throttle can go now; otherwise
// it marks it pending and arms the timer that calls flush.
func (h *Hub) ready(t *throttle, interval time.Duration, flush func()) bool {
	now := time.Now()
	if !t.pending && (t.last.IsZero() || now.Sub(t.last) >= interval) {
		t.last = now
		return true
	}
	t.pending = true
	if t.timer == nil {
		// The callback waits for the lock held here, so tm is set when it
		// runs; a timer stopped too late (replaced since) does nothing.
		var tm *time.Timer
		tm = time.AfterFunc(max(interval-now.Sub(t.last), 0), func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if t.timer != tm {
				return
			}
			t.timer = nil
			if !h.closed {
				flush()
			}
		})
		t.timer = tm
	}
	return false
}

// Library publishes a change of the SD card: the systems whose games
// changed, none when unknown (the app refreshes everything).
func (h *Hub) Library(systemIDs ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || len(h.subs) == 0 {
		return
	}
	if len(systemIDs) == 0 {
		h.libAll = true
	}
	for _, id := range systemIDs {
		if id == "" {
			h.libAll = true
			continue
		}
		h.libIDs[id] = struct{}{}
	}
	if h.ready(&h.library, h.opts.LibraryInterval, h.flushLibraryLocked) {
		h.sendLibraryLocked()
	}
}

func (h *Hub) flushLibraryLocked() {
	if h.library.pending {
		h.library.stop()
		h.library.last = time.Now()
		h.sendLibraryLocked()
	}
}

func (h *Hub) sendLibraryLocked() {
	ids := []string{}
	if !h.libAll {
		for id := range h.libIDs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
	}
	h.libAll = false
	clear(h.libIDs)
	data, _ := json.Marshal(struct {
		SystemIDs []string `json:"systemIds"`
	}{ids})
	h.broadcastLocked(Event{Type: TypeLibrary, Data: data})
}

// Paks publishes a change of the installed paks (M12): installed or
// deleted.
func (h *Hub) Paks() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || len(h.subs) == 0 {
		return
	}
	if h.ready(&h.paks, h.opts.LibraryInterval, h.flushPaksLocked) {
		h.sendPaksLocked()
	}
}

func (h *Hub) flushPaksLocked() {
	if h.paks.pending {
		h.paks.stop()
		h.paks.last = time.Now()
		h.sendPaksLocked()
	}
}

func (h *Hub) sendPaksLocked() {
	h.broadcastLocked(Event{Type: TypePaks, Data: []byte("{}")})
}

// Job publishes the state of a job (a Job of the contract, marshalled
// here). key is what makes a change go at once: its state and its
// cancelRequested flag; finished tells the job will not change anymore.
func (h *Hub) Job(id, key string, finished bool, v any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || len(h.subs) == 0 {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	j := h.jobs[id]
	if j == nil {
		j = &jobThrottle{id: id}
		h.jobs[id] = j
	}
	j.data, j.finished = data, finished
	if key != j.key {
		// A change of state goes at once, replacing a pending progress.
		j.stop()
		j.key, j.last = key, time.Now()
		h.sendJobLocked(j)
	} else if h.ready(&j.throttle, h.opts.JobInterval, func() { h.flushJobLocked(j) }) {
		h.sendJobLocked(j)
	}
	h.forgetJobLocked(j)
}

// forgetJobLocked drops a finished job once nothing is pending.
func (h *Hub) forgetJobLocked(j *jobThrottle) {
	if j.finished && !j.pending && h.jobs[j.id] == j {
		delete(h.jobs, j.id)
	}
}

func (h *Hub) flushJobLocked(j *jobThrottle) {
	if j.pending {
		j.stop()
		j.last = time.Now()
		h.sendJobLocked(j)
	}
	h.forgetJobLocked(j)
}

func (h *Hub) sendJobLocked(j *jobThrottle) {
	if j.data != nil {
		h.broadcastLocked(Event{Type: TypeJob, Data: j.data})
		j.data = nil
	}
}
