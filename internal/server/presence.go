package server

import (
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
)

// ConnectedWindow is how long a client counts as connected after its last
// accepted signed request, or after its last event stream closed
// (docs/specs/04-api-v1.md GET /device, and the "Connected" state of
// 05 §7.1). A client with an open event stream (M14) is connected.
const ConnectedWindow = 30 * time.Second

// ConnectedClient is an item of GET /device connectedClients.
type ConnectedClient struct {
	ClientName string    `json:"clientName"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

// presence remembers, in memory only, when each client last had a signed
// request accepted, and how many event streams it has open. It is empty
// at launch and never written to the SD card. Its changes are made under
// the access lock (see access), except the read-side cleanup.
type presence struct {
	mu      sync.Mutex
	seen    map[string]time.Time // client id -> last accepted signed request
	streams map[string]int       // client id -> open event streams (M14)
}

func newPresence() *presence {
	return &presence{seen: map[string]time.Time{}, streams: map[string]int{}}
}

// streamOpened counts an open event stream of id.
// It reports whether id was not connected before (at: now).
func (p *presence) streamOpened(id string, at time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	was := p.presentLocked(id, at)
	p.streams[id]++
	return !was
}

// presentLocked reports whether id counts as connected at this time.
func (p *presence) presentLocked(id string, at time.Time) bool {
	if p.streams[id] > 0 {
		return true
	}
	seen, ok := p.seen[id]
	return ok && !seen.Before(at.Add(-ConnectedWindow))
}

// streamClosed uncounts a stream of id; when it was the last one, the
// ConnectedWindow starts at the given time.
func (p *presence) streamClosed(id string, at time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.streams[id] <= 0 {
		return
	}
	if p.streams[id]--; p.streams[id] == 0 {
		delete(p.streams, id)
		p.seen[id] = at
	}
}

// touch records an accepted request of id at this time; it reports
// whether id was not connected before.
func (p *presence) touch(id string, at time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	was := p.presentLocked(id, at)
	p.seen[id] = at
	return !was
}

func (p *presence) forget(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.seen, id)
	delete(p.streams, id)
}

func (p *presence) clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	clear(p.seen)
	clear(p.streams)
}

// seenSince returns the clients seen at or after since, and those with an
// open event stream (seen now), and drops the older entries.
func (p *presence) seenSince(since, now time.Time) map[string]time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]time.Time, len(p.seen)+len(p.streams))
	for id, at := range p.seen {
		if at.Before(since) {
			delete(p.seen, id)
			continue
		}
		out[id] = at
	}
	for id := range p.streams {
		out[id] = now
	}
	return out
}

// markSeen marks a signed request as accepted for the access log. Its
// client was recorded as seen by access.accept before the handler runs,
// so the caller of GET /device is in its own list.
func (s *Server) markSeen(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, ok := auth.ClientFromContext(r.Context()); ok {
			// A client disconnected while its request was being verified
			// (or calling POST /clients/me/connect) is not marked seen.
			// Presence is recorded by the gate's commit (access.accept),
			// under the M10 lock.
			markAccepted(r)
			s.notePlatform(c, r.Header.Get(auth.HeaderPlatform))
		}
		next.ServeHTTP(w, r)
	})
}

// ConnectedClients returns the paired clients with a signed request
// accepted in the last ConnectedWindow or an open event stream (its
// lastSeenAt is now), most recent first. A client that
// is no longer paired is left out.
func (s *Server) ConnectedClients() []ConnectedClient {
	now := s.now()
	out := []ConnectedClient{}
	for id, at := range s.presence.seenSince(now.Add(-ConnectedWindow), now) {
		c, ok := s.opts.Clients.Lookup(id)
		if !ok {
			s.presence.forget(id)
			continue
		}
		out = append(out, ConnectedClient{ClientName: c.Name, LastSeenAt: timestamp(at)})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastSeenAt.Equal(out[j].LastSeenAt) {
			return out[i].LastSeenAt.After(out[j].LastSeenAt)
		}
		return out[i].ClientName < out[j].ClientName
	})
	return out
}

// notePlatform records the X-RL-Platform of an accepted request (M13,
// informative): clients.json changes only when a platform is new for the
// client. Unknown values are ignored.
func (s *Server) notePlatform(c auth.Client, p string) {
	if p == "" || slices.Contains(c.Platforms, p) || !auth.ValidDevicePlatform(p) {
		return
	}
	if _, err := s.opts.Clients.NotePlatform(c.ID, p); err != nil {
		s.log.Warn("cannot record the client platform", "client", c.ID, "error", err)
	}
}
