package server

import (
	"errors"
	"net/http"
	"sort"
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

// gate is the auth.Gate of every signed route, the one place where the
// disconnection, the access generation and the write lease are enforced
// (see access). The verifier calls it after the signature check and the
// nonce reservation, before reading the body, so a refused request changes
// nothing and its body is never read. The admission is committed once the
// request is accepted (body verified) and rolled back if it is rejected.
func (s *Server) gate(r *http.Request, c auth.Client, opts auth.RouteOptions) (*auth.Admission, *auth.Rejection) {
	adm, rej := s.admit(c.ID, opts)
	if hook := s.gateHook.Load(); hook != nil {
		(*hook)()
	}
	return adm, rej
}

func (s *Server) admit(id string, opts auth.RouteOptions) (*auth.Admission, *auth.Rejection) {
	ad, other, rej := s.access.admit(id, opts)
	if rej != nil {
		return nil, rej
	}
	if other != nil {
		return nil, s.readOnly(other.id)
	}
	return &auth.Admission{
		Commit: func() *auth.Rejection {
			other, rej := s.access.accept(ad, opts)
			if other != nil {
				return s.readOnly(other.id)
			}
			return rej
		},
		Rollback: func() { s.access.rollback(ad) },
		Value:    ad,
	}, nil
}

// readOnly is the 423 answer when holderID holds the lease.
func (s *Server) readOnly(holderID string) *auth.Rejection {
	return &auth.Rejection{Status: http.StatusLocked, Code: CodeReadOnly,
		Message: "Another device has write access", Details: map[string]any{"writer": s.clientName(holderID)}}
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
	h, ok := s.access.current()
	if !ok {
		return nil
	}
	return &writerJSON{ClientName: s.clientName(h.id), You: h.id == callerID, ExpiresAt: timestamp(h.expires)}
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
	ad, ok := auth.AdmissionValue(r.Context()).(admission)
	if !ok {
		s.internal(w, r, errors.New("no admission in context"))
		return
	}
	var req takeWriterRequest
	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}
	// One atomic step that also checks the access generation captured by
	// the gate: a client revoked since never gets the lease.
	h, previous, ok, rej := s.access.take(ad, req.Force)
	switch {
	case rej != nil:
		auth.WriteError(w, rej.Status, rej.Code, rej.Message, rej.Details)
		return
	case !ok:
		rej := s.readOnly(h.id)
		auth.WriteError(w, rej.Status, rej.Code, rej.Message, rej.Details)
		return
	case previous != "":
		s.log.Info("write access taken over", "client", c.ID, "name", c.Name, "from", s.clientName(previous))
	}
	writeJSON(w, http.StatusOK, writerState{&writerJSON{ClientName: c.Name, You: true, ExpiresAt: timestamp(h.expires)}})
}

func (s *Server) handleReleaseWriter(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.ClientFromContext(r.Context())
	if s.access.release(c.ID) {
		s.log.Info("write access released", "client", c.ID, "name", c.Name)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.ClientFromContext(r.Context())
	// Connected again from now on, without taking the lease.
	if s.access.connect(c.ID) {
		s.log.Info("client reconnected", "client", c.ID, "name", c.Name)
	}
	w.WriteHeader(http.StatusNoContent)
}

// WriterName returns the name of the client holding the write lease, and
// whether one holds it.
func (s *Server) WriterName() (string, bool) {
	h, ok := s.access.current()
	if !ok {
		return "", false
	}
	return s.clientName(h.id), true
}

// ErrUnknownClient is returned for a client that is not paired.
var ErrUnknownClient = errors.New("unknown client")

// Disconnect disconnects a paired client from the console (Settings >
// Devices): its lease is dropped, it leaves the connected clients, and its
// signed requests get 401 disconnected until it calls
// POST /clients/me/connect; its requests in flight are refused. In memory
// only.
func (s *Server) Disconnect(id string) error {
	c, ok := s.access.disconnect(id)
	if !ok {
		return ErrUnknownClient
	}
	s.log.Info("client disconnected by the console", "client", id, "name", c.Name)
	return nil
}

// Forget removes a paired client (Settings > Devices > Forget): its
// pairing is removed from the client store, so its next requests get
// 401 unknown_client, and its lease and disconnection are dropped. It
// reports whether the client existed.
func (s *Server) Forget(id string) (bool, error) { return s.access.forget(id) }

// ForgetAll removes every paired client and returns how many there were.
func (s *Server) ForgetAll() (int, error) { return s.access.forgetAll() }

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
	out := s.access.devices(s.presence.seenSince(s.now().Add(-ConnectedWindow)))
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].PairedAt.Equal(out[j].PairedAt) {
			return out[i].PairedAt.After(out[j].PairedAt)
		}
		return out[i].Name < out[j].Name
	})
	return out
}
