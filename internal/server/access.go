package server

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
)

// access is the in-memory state of M10 behind ONE mutex: the
// disconnections by the console, the access generation of each client,
// and the presence updates of accepted requests. Every operation below
// takes the lock once and does all its checks and changes inside it, so no
// two operations interleave. (The M10 write lease is gone since M13: the
// writes are serialized by the write lock instead, see writes.go.)
//
// Access generation: a revocation (disconnect, forget, unpair, re-pairing)
// bumps the client's generation. A request captures it when the gate
// admits it, before its body is read; when the request is accepted, a
// different generation means the client was revoked in between, even if
// it reconnected or was paired again since, and the request is refused.
type access struct {
	mu       sync.Mutex
	now      func() time.Time
	clients  *auth.ClientStore
	presence *presence

	disconnected map[string]bool
	gen          map[string]uint64 // access generation; absent = 0
	revokedCode  map[string]string // 401 code of the last revocation
	gens         uint64            // generation counter

	// lookupHook, when set (tests), is called by Lookup after the client
	// is read, before the verifier goes on.
	lookupHook atomic.Pointer[func(id string)]
	// onRevoke is called, with mu held, on every revocation: it closes
	// the event streams of the client (M14) and cancels its jobs (M13).
	// It must not block nor call access.
	onRevoke func(id string)
	// onPresence is called, with mu held, when a client becomes connected
	// or is revoked: the device events follow (M14). It must not block
	// nor call access.
	onPresence func()
}

func (a *access) presenceChangedLocked() {
	if a.onPresence != nil {
		a.onPresence()
	}
}

// Lookup is the auth.ClientLookup of the verifier: the client and its
// access generation (Epoch) are read in one step, so admit can refuse a
// request verified with a pairing revoked since (forgotten and paired
// again with the same id, for instance).
func (a *access) Lookup(id string) (auth.Client, bool) {
	a.mu.Lock()
	c, ok := a.clients.Lookup(id)
	c.Epoch = a.gen[id]
	a.mu.Unlock()
	if hook := a.lookupHook.Load(); hook != nil {
		(*hook)(id)
	}
	return c, ok
}

func newAccess(now func() time.Time, clients *auth.ClientStore, p *presence) *access {
	return &access{now: now, clients: clients, presence: p,
		disconnected: map[string]bool{}, gen: map[string]uint64{}, revokedCode: map[string]string{}}
}

// admission is what the gate captured for a request.
type admission struct {
	id  string
	gen uint64
}

func unauthorized(code string) *auth.Rejection {
	msg := "Unknown client"
	if code == CodeDisconnected {
		msg = "Disconnected by the console"
	}
	return &auth.Rejection{Status: http.StatusUnauthorized, Code: code, Message: msg}
}

// --- helpers, called with mu held -----------------------------------------

func (a *access) revokeLocked(id, code string) {
	a.gens++
	a.gen[id] = a.gens
	a.revokedCode[id] = code
	a.presence.forget(id)
	if a.onRevoke != nil {
		a.onRevoke(id)
	}
	a.presenceChangedLocked()
}

// checkLocked refuses a disconnected client (unless allowed) or one no
// longer paired.
func (a *access) checkLocked(id string, whileDisconnected bool) *auth.Rejection {
	if !whileDisconnected && a.disconnected[id] {
		return unauthorized(CodeDisconnected)
	}
	if _, ok := a.clients.Lookup(id); !ok {
		return unauthorized(auth.CodeUnknownClient)
	}
	return nil
}

// checkGenLocked is checkLocked plus the generation captured at admission.
func (a *access) checkGenLocked(ad admission, whileDisconnected bool) *auth.Rejection {
	if a.gen[ad.id] != ad.gen {
		code := a.revokedCode[ad.id]
		if _, ok := a.clients.Lookup(ad.id); !ok || code == "" {
			code = auth.CodeUnknownClient
		}
		return unauthorized(code)
	}
	return a.checkLocked(ad.id, whileDisconnected)
}

// --- operations ------------------------------------------------------------

// admit is the pre-body check of the gate, for client c as read by Lookup
// (its Epoch is the generation of the pairing that verified the request).
func (a *access) admit(c auth.Client, opts auth.RouteOptions) (admission, *auth.Rejection) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ad := admission{id: c.ID, gen: c.Epoch}
	if rej := a.checkGenLocked(ad, opts.WhileDisconnected); rej != nil {
		return admission{}, rej
	}
	return ad, nil
}

// accept is the commit of an accepted request (body verified): checks the
// generation and records the presence. For POST /clients/me/connect
// (WhileDisconnected), the disconnection is lifted here, in the same step
// that validated the generation (a newer disconnection bumped it, so it is
// never cleared by an older request); lifted reports whether there was one.
func (a *access) accept(ad admission, opts auth.RouteOptions) (rej *auth.Rejection, lifted bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if rej := a.checkGenLocked(ad, opts.WhileDisconnected); rej != nil {
		return rej, false
	}
	if opts.WhileDisconnected {
		lifted = a.disconnected[ad.id]
		delete(a.disconnected, ad.id)
	}
	if !a.disconnected[ad.id] {
		if a.presence.touch(ad.id, a.now()) {
			a.presenceChangedLocked()
		}
	}
	return nil, lifted
}

// recheck checks, for a request accepted with this admission, that its
// client was not revoked since (disconnected, forgotten, unpaired, paired
// again). A write calls it once it holds the write lock, just before it
// changes the card (M13): a revocation during the wait refuses it.
func (a *access) recheck(ad admission) *auth.Rejection {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.checkGenLocked(ad, false)
}

// streamOpened records an open event stream (M14) for a request accepted
// with this admission: its client counts as connected until the stream
// closes. It refuses a client revoked since the acceptance (the stream
// subscribes after it, so the revocation did not close it).
func (a *access) streamOpened(ad admission) *auth.Rejection {
	a.mu.Lock()
	defer a.mu.Unlock()
	if rej := a.checkGenLocked(ad, false); rej != nil {
		return rej
	}
	if a.presence.streamOpened(ad.id, a.now()) {
		a.presenceChangedLocked()
	}
	return nil
}

// streamClosed is the end of a stream recorded by streamOpened: after the
// last one, the client stays connected for ConnectedWindow. Nothing
// happens if the client was revoked since (its presence is gone, and a
// stream opened after a reconnection is counted with the new generation).
func (a *access) streamClosed(ad admission) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.gen[ad.id] != ad.gen || a.disconnected[ad.id] {
		return
	}
	a.presence.streamClosed(ad.id, a.now())
}

// disconnect disconnects a paired client.
func (a *access) disconnect(id string) (auth.Client, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, ok := a.clients.Lookup(id)
	if !ok {
		return auth.Client{}, false
	}
	a.disconnected[id] = true
	a.revokeLocked(id, CodeDisconnected)
	return c, true
}

// forget removes a client from the store (Forget, DELETE /clients/me).
func (a *access) forget(id string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ok, err := a.clients.Remove(id)
	if err != nil {
		return false, err
	}
	delete(a.disconnected, id)
	a.revokeLocked(id, auth.CodeUnknownClient)
	return ok, nil
}

// forgetAll removes every client.
func (a *access) forgetAll() (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	list := a.clients.List()
	n, err := a.clients.RemoveAll()
	if err != nil {
		return 0, err
	}
	for _, c := range list {
		delete(a.disconnected, c.ID)
		a.revokeLocked(c.ID, auth.CodeUnknownClient)
	}
	a.presence.clear()
	return n, nil
}

// pair stores a newly paired client. Pairing again with the same id
// replaces the public key: requests admitted with the old pairing are revoked,
// and a disconnection is lifted (a deliberate action on the console).
func (a *access) pair(c auth.Client) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.clients.Put(c); err != nil {
		return err
	}
	a.revokeLocked(c.ID, auth.CodeUnknownClient)
	delete(a.disconnected, c.ID)
	return nil
}

// devices is the state of each paired client, for the console; presence
// is read under the same lock as the disconnections, so a row is never
// both connected and disconnected.
func (a *access) devices(connectedSince time.Time) []DeviceStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	seen := a.presence.seenSince(connectedSince, a.now())
	list := a.clients.List()
	out := make([]DeviceStatus, 0, len(list))
	for _, c := range list {
		_, connected := seen[c.ID]
		out = append(out, DeviceStatus{
			ID: c.ID, Name: c.Name, PairedAt: c.PairedAt,
			Platform: c.Platform, Platforms: c.Platforms,
			Connected:    connected,
			Disconnected: a.disconnected[c.ID],
		})
	}
	return out
}
