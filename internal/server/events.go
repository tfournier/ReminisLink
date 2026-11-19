package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/events"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/transfer"
)

// The event stream GET /events (M14, protocol/openapi.yaml): a signed
// read route that stays open while the pak runs. The hub (internal/events)
// coalesces the events; each stream numbers them and writes them under a
// write deadline, so a client that stops reading is cut off without
// blocking anyone.

// Settings of the event stream.
const (
	// KeepAliveInterval is the longest gap between two keep-alive
	// comments (EventHello.keepAliveSeconds).
	KeepAliveInterval = 15 * time.Second
	// EventsRetryAfter is the Retry-After (seconds) of a 429 answered when
	// too many streams are open.
	EventsRetryAfter = 5
	// DeviceSampleEvery is how often the device state is read while at
	// least one stream is open (device events: at most one every 5 s).
	DeviceSampleEvery = 5 * time.Second
	// FreeSpaceStep is the smallest change of the free space that sends a
	// device event: the log and small files change it all the time.
	FreeSpaceStep = 1 << 20
	// shutdownWriteTimeout bounds the writing of the last events at
	// shutdown, so a client that stopped reading does not delay it.
	shutdownWriteTimeout = time.Second
)

const eventsPattern = "GET " + Prefix + "/events"

// newEventHub builds the hub of the server.
func (s *Server) newEventHub(opts events.Options) *events.Hub {
	opts.OnActive = s.eventsActive
	return events.New(opts)
}

// CloseEvents sends the shutdown event to every stream (reason
// events.ReasonQuit or events.ReasonStopped) and ends them; new streams
// are refused. Only the first call counts. It never blocks: the streams
// write their last events within shutdownWriteTimeout.
func (s *Server) CloseEvents(reason string) {
	if n := s.events.Count(); s.events.Shutdown(reason) {
		s.log.Info("event streams closed", "reason", reason, "streams", n)
	}
}

// EventStreams returns the number of open event streams.
func (s *Server) EventStreams() int { return s.events.Count() }

// sseWriter writes the events of one stream, each under a write deadline
// and flushed.
type sseWriter struct {
	w   http.ResponseWriter
	ctl *http.ResponseController
	id  uint64
}

func (sw *sseWriter) write(b []byte, timeout time.Duration) error {
	_ = sw.ctl.SetWriteDeadline(time.Now().Add(timeout))
	if _, err := sw.w.Write(b); err != nil {
		return err
	}
	return sw.ctl.Flush()
}

func (sw *sseWriter) event(ev events.Event, timeout time.Duration) error {
	sw.id++
	return sw.write(fmt.Appendf(nil, "id: %d\nevent: %s\ndata: %s\n\n", sw.id, ev.Type, ev.Data), timeout)
}

var keepAlive = []byte(": keep-alive\n\n")

type eventHello struct {
	ServerTime       time.Time `json:"serverTime"`
	KeepAliveSeconds int64     `json:"keepAliveSeconds"`
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.ClientFromContext(r.Context())
	ad, ok := auth.AdmissionValue(r.Context()).(admission)
	if !ok {
		s.internal(w, r, errors.New("no admission in context"))
		return
	}
	sub, err := s.events.Subscribe(c.ID)
	switch {
	case errors.Is(err, events.ErrTooManyStreams):
		w.Header().Set("Retry-After", strconv.Itoa(EventsRetryAfter))
		auth.WriteError(w, http.StatusTooManyRequests, auth.CodeTooManyAttempts, "Too many event streams", nil)
		return
	case err != nil:
		w.Header().Set("Connection", "close")
		auth.WriteError(w, http.StatusServiceUnavailable, auth.CodeShuttingDown, "Shutting down", nil)
		return
	}
	defer sub.Close()
	// An open stream counts as presence (connectedClients, the console's
	// "Connected" state). The client may have been revoked between the
	// acceptance of the request and the subscription: the revocation
	// closed the streams it had then, not this one.
	if rej := s.access.streamOpened(ad); rej != nil {
		auth.WriteError(w, rej.Status, rej.Code, rej.Message, rej.Details)
		return
	}
	defer s.access.streamClosed(ad)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	sw := &sseWriter{w: w, ctl: http.NewResponseController(w)}
	// The connection may be reused after a stream ended by the pak.
	defer func() { _ = sw.ctl.SetWriteDeadline(time.Time{}) }()
	idle := s.timeouts.sendIdle
	every := s.timeouts.keepAlive
	hello, _ := json.Marshal(eventHello{ServerTime: timestamp(s.now()), KeepAliveSeconds: int64((every + time.Second - 1) / time.Second)})
	if err := sw.event(events.Event{Type: events.TypeHello, Data: hello}, idle); err != nil {
		s.log.Info("event stream cut", "client", c.ID, "error", err)
		return
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		var err error
		select {
		case <-r.Context().Done():
			return
		case ev := <-sub.C():
			err = sw.event(ev, idle)
		case <-ticker.C:
			err = sw.write(keepAlive, idle)
		case <-sub.Done():
			s.endStream(sw, sub, c.ID)
			return
		}
		if err != nil {
			// A client that stops reading is cut off after the deadline.
			s.log.Info("event stream cut", "client", c.ID, "error", err)
			return
		}
	}
}

// endStream finishes a stream ended by the hub: at shutdown, the events
// still buffered then the shutdown event.
func (s *Server) endStream(sw *sseWriter, sub *events.Subscription, clientID string) {
	switch sub.Reason() {
	case events.Lagging:
		s.log.Info("event stream closed: the client is too far behind", "client", clientID)
		return
	case events.Shutdown:
	default:
		return
	}
drain:
	for {
		select {
		case ev := <-sub.C():
			if sw.event(ev, shutdownWriteTimeout) != nil {
				return
			}
		default:
			break drain
		}
	}
	if final, ok := sub.Final(); ok {
		_ = sw.event(final, shutdownWriteTimeout)
	}
}

// --- Producers ---------------------------------------------------------------

// systemOfGame returns the systemId of a gameId ("" when invalid): the
// first component of its path. The path is never used on disk.
func systemOfGame(gameID string) string {
	p, err := library.DecodeID(gameID)
	if err != nil {
		return ""
	}
	folder, _, _ := strings.Cut(p, "/")
	return library.EncodeID(folder)
}

// changes publishes a library event after a successful (2xx) answer of a
// route that changed the SD card: the system of its {gameId}, or unknown
// (every system) without one.
func (s *Server) changes(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w}
		h(rec, r)
		if rec.status < 200 || rec.status >= 300 {
			return
		}
		if id := r.PathValue("gameId"); id != "" {
			s.events.Library(systemOfGame(id))
			return
		}
		s.events.Library()
	}
}

// changesUpload is changes for the completion of an upload: a file is
// installed, except for a staging upload (nothing visible changes).
func (s *Server) changesUpload(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target, ok := s.opts.Transfers.UploadTarget(r.PathValue("uploadId"))
		rec := &statusRecorder{ResponseWriter: w}
		h(rec, r)
		if !ok || rec.status < 200 || rec.status >= 300 {
			return
		}
		switch target.Kind {
		case transfer.KindStaging:
		case transfer.KindROM:
			s.events.Library(target.SystemID)
		default:
			s.events.Library(systemOfGame(target.GameID))
		}
	}
}

// jobUpdated is the update hook of the job queue: a job event for every
// change (coalesced by the hub), and a library event when a job installed
// a file. Called with the queue lock held: it never blocks.
func (s *Server) jobUpdated(j transfer.Job) {
	if !s.events.Active() {
		return
	}
	key := j.State
	if j.CancelRequested {
		key += "+cancel"
	}
	done := j.State == transfer.JobSucceeded || j.State == transfer.JobFailed || j.State == transfer.JobCancelled
	s.events.Job(j.ID, key, done, j)
	// A staging download installs nothing visible (no gameId).
	if id, _ := j.Result["gameId"].(string); j.State == transfer.JobSucceeded && id != "" {
		s.events.Library(systemOfGame(id))
	}
}

// eventDevice is the EventDevice schema.
type eventDevice struct {
	Battery          *batteryInfo      `json:"battery"`
	ConnectedClients []ConnectedClient `json:"connectedClients"`
	Storage          []storageVolume   `json:"storage"`
}

// deviceSampler reads the device state while at least one stream is open.
type deviceSampler struct {
	mu   sync.Mutex
	stop chan struct{}
}

// eventsActive is the OnActive hook of the hub (called with its lock
// held): the sampler runs while a stream is open, so nothing is read
// when nobody listens.
func (s *Server) eventsActive(active bool) {
	s.sampler.mu.Lock()
	defer s.sampler.mu.Unlock()
	switch {
	case active && s.sampler.stop == nil:
		s.sampler.stop = make(chan struct{})
		go s.sampleDevice(s.sampler.stop, s.timeouts.deviceSample)
	case !active && s.sampler.stop != nil:
		close(s.sampler.stop)
		s.sampler.stop = nil
	}
}

// sampleDevice publishes a device event when the battery, the connected
// clients or the free space (by FreeSpaceStep at least) changed since the
// last one. The first sample is the reference: the app reads GET /device
// when its stream opens.
func (s *Server) sampleDevice(stop <-chan struct{}, every time.Duration) {
	last := s.deviceState()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		cur := s.deviceState()
		if !deviceChanged(last, cur) {
			continue
		}
		last = cur
		s.events.Device(cur)
	}
}

func (s *Server) deviceState() eventDevice {
	d := eventDevice{ConnectedClients: s.ConnectedClients(), Storage: []storageVolume{}}
	if info, err := s.opts.Adapter.DeviceInfo(); err == nil && info.Battery != nil {
		d.Battery = &batteryInfo{Percent: info.Battery.Percent, Charging: info.Battery.Charging}
	}
	if st, err := s.opts.Storage(s.opts.Adapter.Roots().SD); err == nil {
		d.Storage = append(d.Storage, storageVolume{ID: "sd", Label: "SD", TotalBytes: st.TotalBytes, FreeBytes: st.FreeBytes})
	}
	return d
}

// deviceChanged compares what a device event is about: the battery, the
// set of connected clients (not their lastSeenAt, which changes with every
// request) and the space of the volumes.
func deviceChanged(a, b eventDevice) bool {
	switch {
	case (a.Battery == nil) != (b.Battery == nil):
		return true
	case a.Battery != nil && *a.Battery != *b.Battery:
		return true
	case !slices.Equal(clientNames(a.ConnectedClients), clientNames(b.ConnectedClients)):
		return true
	case len(a.Storage) != len(b.Storage):
		return true
	}
	for i := range a.Storage {
		x, y := a.Storage[i], b.Storage[i]
		if x.ID != y.ID || x.TotalBytes != y.TotalBytes || absDiff(x.FreeBytes, y.FreeBytes) >= FreeSpaceStep {
			return true
		}
	}
	return false
}

func clientNames(list []ConnectedClient) []string {
	out := make([]string, len(list))
	for i, c := range list {
		out[i] = c.ClientName
	}
	slices.Sort(out)
	return out
}

func absDiff(a, b int64) int64 {
	if a > b {
		return a - b
	}
	return b - a
}
