package server

import (
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
)

// The single writer (M10, protocol/openapi.yaml "Single writer (M10)"):
// the write lease, the disconnection by the console and forgetting a
// client. Everything here is in memory only, except the client store.

// Error codes of M10.
const (
	CodeReadOnly     = "read_only"
	CodeDisconnected = "disconnected"
)

// clientSet is a set of client ids, safe for concurrent use.
type clientSet struct {
	mu  sync.Mutex
	ids map[string]bool
}

func newClientSet() *clientSet { return &clientSet{ids: map[string]bool{}} }

func (c *clientSet) add(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ids[id] = true
}

func (c *clientSet) remove(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	ok := c.ids[id]
	delete(c.ids, id)
	return ok
}

func (c *clientSet) has(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ids[id]
}

func (c *clientSet) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.ids)
}

// gate is the auth.Gate of every signed route, the one place where the
// disconnection and the write lease are enforced. The verifier calls it
// after the signature check and the nonce reservation, before reading the
// body, so a refused request changes nothing and its body is never read.
//
// A write route takes a free lease provisionally (first writer wins); the
// admission confirms it once the request is accepted (body verified) or
// rolls it back if the verifier rejects the request. The checks run under
// s.access, like the revocations (Disconnect, Forget, ForgetAll,
// DELETE /clients/me), so a revoked client never gets the lease back.
func (s *Server) gate(r *http.Request, c auth.Client, opts auth.RouteOptions) (*auth.Admission, *auth.Rejection) {
	adm, rej := s.admit(c.ID, opts)
	if hook := s.gateHook.Load(); hook != nil {
		(*hook)()
	}
	return adm, rej
}

func (s *Server) admit(id string, opts auth.RouteOptions) (*auth.Admission, *auth.Rejection) {
	s.access.Lock()
	defer s.access.Unlock()
	if rej := s.revokedLocked(id, opts); rej != nil {
		return nil, rej
	}
	var ticket auth.LeaseTicket
	if opts.TakesLease {
		h, t, ok := s.lease.TryAcquire(id)
		if !ok {
			return nil, s.readOnly(h)
		}
		ticket = t
	}
	return &auth.Admission{
		Commit:   func() *auth.Rejection { return s.commit(id, opts) },
		Rollback: func() { s.lease.Rollback(id, ticket) },
	}, nil
}

// commit accepts a verified request: the client must still be paired and
// connected, a write route must still hold the lease (taken again if it
// expired while the body was read), and any accepted request of the holder
// renews (confirms) the lease.
func (s *Server) commit(id string, opts auth.RouteOptions) *auth.Rejection {
	s.access.Lock()
	defer s.access.Unlock()
	if rej := s.revokedLocked(id, opts); rej != nil {
		return rej
	}
	if opts.TakesLease {
		if h, _, ok := s.lease.TryAcquire(id); !ok {
			return s.readOnly(h)
		}
	}
	s.lease.Renew(id)
	return nil
}

// revokedLocked refuses a client disconnected by the console or no longer
// paired. Call it with s.access held.
func (s *Server) revokedLocked(id string, opts auth.RouteOptions) *auth.Rejection {
	if !opts.WhileDisconnected && s.disconnected.has(id) {
		return &auth.Rejection{Status: http.StatusUnauthorized, Code: CodeDisconnected,
			Message: "Disconnected by the console"}
	}
	if _, ok := s.opts.Clients.Lookup(id); !ok {
		return &auth.Rejection{Status: http.StatusUnauthorized, Code: auth.CodeUnknownClient, Message: "Unknown client"}
	}
	return nil
}

// readOnly is the 423 answer when h holds the lease.
func (s *Server) readOnly(h auth.LeaseHolder) *auth.Rejection {
	return &auth.Rejection{Status: http.StatusLocked, Code: CodeReadOnly,
		Message: "Another device has write access", Details: map[string]any{"writer": s.clientName(h.ClientID)}}
}

// clientName is the name of a paired client ("" when it is gone).
func (s *Server) clientName(id string) string {
	c, _ := s.opts.Clients.Lookup(id)
	return c.Name
}

// writerJSON is the Writer schema.
type writerJSON struct {
	ClientName string    `json:"clientName"`
	You        bool      `json:"you"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// writerState is the WriterState schema.
type writerState struct {
	Writer *writerJSON `json:"writer"`
}

// writerFor is the holder of the lease as seen by the caller, nil when
// the lease is free.
func (s *Server) writerFor(callerID string) *writerJSON {
	h, ok := s.lease.Holder()
	if !ok {
		return nil
	}
	return &writerJSON{ClientName: s.clientName(h.ClientID), You: h.ClientID == callerID, ExpiresAt: timestamp(h.ExpiresAt)}
}

func (s *Server) handleGetWriter(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.ClientFromContext(r.Context())
	writeJSON(w, http.StatusOK, writerState{s.writerFor(c.ID)})
}

type takeWriterRequest struct {
	Force bool `json:"force"`
}

func (s *Server) handleTakeWriter(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.ClientFromContext(r.Context())
	var req takeWriterRequest
	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}
	// Taken under s.access: a client disconnected or forgotten while
	// its body was read does not get the lease back.
	s.access.Lock()
	if rej := s.revokedLocked(c.ID, auth.RouteOptions{}); rej != nil {
		s.access.Unlock()
		auth.WriteError(w, rej.Status, rej.Code, rej.Message, rej.Details)
		return
	}
	before, held := s.lease.Holder()
	h, ok := s.lease.Acquire(c.ID, req.Force)
	s.access.Unlock()
	if !ok {
		rej := s.readOnly(h)
		auth.WriteError(w, rej.Status, rej.Code, rej.Message, rej.Details)
		return
	}
	if held && before.ClientID != c.ID {
		s.log.Info("write access taken over", "client", c.ID, "name", c.Name, "from", s.clientName(before.ClientID))
	} else if !held {
		s.log.Info("write access taken", "client", c.ID, "name", c.Name)
	}
	writeJSON(w, http.StatusOK, writerState{&writerJSON{ClientName: c.Name, You: true, ExpiresAt: timestamp(h.ExpiresAt)}})
}

func (s *Server) handleReleaseWriter(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.ClientFromContext(r.Context())
	if s.lease.Release(c.ID) {
		s.log.Info("write access released", "client", c.ID, "name", c.Name)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.ClientFromContext(r.Context())
	s.access.Lock()
	lifted := s.disconnected.remove(c.ID)
	// Connected again from now on (without taking the lease): markSeen
	// skipped it while it was disconnected.
	s.presence.touch(c.ID, s.now())
	s.access.Unlock()
	if lifted {
		s.log.Info("client reconnected", "client", c.ID, "name", c.Name)
	}
	w.WriteHeader(http.StatusNoContent)
}

// dropClient forgets the in-memory state of a client: its lease, its
// disconnection and its presence.
func (s *Server) dropClient(id string) {
	s.lease.Release(id)
	s.disconnected.remove(id)
	s.presence.forget(id)
}

// WriterName returns the name of the client holding the write lease, and
// whether one holds it.
func (s *Server) WriterName() (string, bool) {
	h, ok := s.lease.Holder()
	if !ok {
		return "", false
	}
	return s.clientName(h.ClientID), true
}

// ErrUnknownClient is returned for a client that is not paired.
var ErrUnknownClient = errors.New("unknown client")

// Disconnect disconnects a paired client from the console (Settings >
// Devices): its lease is dropped, it leaves the connected clients, and its
// signed requests get 401 disconnected until it calls
// POST /clients/me/connect. In memory only.
func (s *Server) Disconnect(id string) error {
	s.access.Lock()
	defer s.access.Unlock()
	c, ok := s.opts.Clients.Lookup(id)
	if !ok {
		return ErrUnknownClient
	}
	s.disconnected.add(id)
	s.lease.Release(id)
	s.presence.forget(id)
	s.log.Info("client disconnected by the console", "client", id, "name", c.Name)
	return nil
}

// Forget removes a paired client (Settings > Devices > Forget): its
// pairing is removed from the client store, so its next requests get
// 401 unknown_client, and its lease and disconnection are dropped. It
// reports whether the client existed.
func (s *Server) Forget(id string) (bool, error) {
	s.access.Lock()
	defer s.access.Unlock()
	ok, err := s.opts.Clients.Remove(id)
	if err != nil {
		return false, err
	}
	s.dropClient(id)
	return ok, nil
}

// ForgetAll removes every paired client and returns how many there were.
func (s *Server) ForgetAll() (int, error) {
	s.access.Lock()
	defer s.access.Unlock()
	n, err := s.opts.Clients.RemoveAll()
	if err != nil {
		return 0, err
	}
	if h, ok := s.lease.Holder(); ok {
		s.lease.Release(h.ClientID)
	}
	s.disconnected.clear()
	s.presence.clear()
	return n, nil
}

// DeviceStatus is a paired client as the Devices screen shows it.
type DeviceStatus struct {
	ID       string
	Name     string
	PairedAt time.Time
	// Connected: a signed request accepted in the last ConnectedWindow.
	Connected bool
	// Writer: it holds the write lease.
	Writer bool
	// Disconnected by the console, until it reconnects from the app.
	Disconnected bool
}

// Devices returns the paired clients with their state, the most recently
// paired first.
func (s *Server) Devices() []DeviceStatus {
	seen := s.presence.seenSince(s.now().Add(-ConnectedWindow))
	h, held := s.lease.Holder()
	list := s.opts.Clients.List()
	out := make([]DeviceStatus, 0, len(list))
	for _, c := range list {
		_, connected := seen[c.ID]
		out = append(out, DeviceStatus{
			ID: c.ID, Name: c.Name, PairedAt: c.PairedAt,
			Connected:    connected,
			Writer:       held && h.ClientID == c.ID,
			Disconnected: s.disconnected.has(c.ID),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].PairedAt.Equal(out[j].PairedAt) {
			return out[i].PairedAt.After(out[j].PairedAt)
		}
		return out[i].Name < out[j].Name
	})
	return out
}
