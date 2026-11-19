package server

import (
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
)

// ConnectedWindow is how long a client counts as connected after its last
// accepted signed request (docs/specs/04-api-v1.md GET /device, and the
// "Connected" state of 05 §7.1).
const ConnectedWindow = 30 * time.Second

// ConnectedClient is an item of GET /device connectedClients.
type ConnectedClient struct {
	ClientName string    `json:"clientName"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

// presence remembers, in memory only, when each client last had a signed
// request accepted. It is empty at launch and never written to the SD card.
type presence struct {
	mu   sync.Mutex
	seen map[string]time.Time // client id -> last accepted signed request
}

func newPresence() *presence { return &presence{seen: map[string]time.Time{}} }

func (p *presence) touch(id string, at time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seen[id] = at
}

func (p *presence) forget(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.seen, id)
}

func (p *presence) clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	clear(p.seen)
}

// seenSince returns the clients seen at or after since, and drops the
// older entries.
func (p *presence) seenSince(since time.Time) map[string]time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]time.Time, len(p.seen))
	for id, at := range p.seen {
		if at.Before(since) {
			delete(p.seen, id)
			continue
		}
		out[id] = at
	}
	return out
}

// markSeen records the authenticated client of a signed route before the
// handler runs, so the caller of GET /device is in its own list.
func (s *Server) markSeen(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, ok := auth.ClientFromContext(r.Context()); ok {
			// A client disconnected while its request was being verified
			// (or calling POST /clients/me/connect) is not marked seen.
			// (The write lease is renewed by the gate's commit.)
			if !s.disconnected.has(c.ID) {
				s.presence.touch(c.ID, s.now())
			}
			markAccepted(r)
		}
		next.ServeHTTP(w, r)
	})
}

// ConnectedClients returns the paired clients with a signed request
// accepted in the last ConnectedWindow, most recent first. A client that
// is no longer paired is left out.
func (s *Server) ConnectedClients() []ConnectedClient {
	now := s.now()
	out := []ConnectedClient{}
	for id, at := range s.presence.seenSince(now.Add(-ConnectedWindow)) {
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
