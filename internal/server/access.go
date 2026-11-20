package server

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
)

// WriteLeaseTTL is how long the write lease lasts after its last renewal
// (protocol/openapi.yaml "Single writer (M10)").
const WriteLeaseTTL = 60 * time.Second

// access is the in-memory state of M10 behind ONE mutex: the write lease,
// the disconnections by the console, the access generation of each client,
// and the presence updates of accepted requests. Every operation below
// takes the lock once and does all its checks and changes inside it, so no
// two operations interleave.
//
// Access generation: a revocation (disconnect, forget, unpair, re-pairing)
// bumps the client's generation. A request captures it when the gate
// admits it, before its body is read; when the request is accepted (or
// takes over with PUT /writer), a different generation means the client
// was revoked in between, even if it reconnected or was paired again
// since, and the request is refused.
//
// Lease: a write request admitted while the lease is free reserves it
// (unconfirmed, with a token). Accepting a write is one atomic
// acquire-or-confirm. Rolling back frees only an unconfirmed lease that
// still carries the request's token. Other accepted requests renew only a
// confirmed lease of their client.
type access struct {
	mu       sync.Mutex
	now      func() time.Time
	clients  *auth.ClientStore
	presence *presence

	holder    string // client id, "" when free
	expires   time.Time
	confirmed bool
	token     uint64 // of the reservation or take that set holder
	tokens    uint64 // token counter

	disconnected map[string]bool
	gen          map[string]uint64 // access generation; absent = 0
	revokedCode  map[string]string // 401 code of the last revocation
	gens         uint64            // generation counter

	// lookupHook, when set (tests), is called by Lookup after the client
	// is read, before the verifier goes on.
	lookupHook atomic.Pointer[func(id string)]
	// onRevoke is called, with mu held, on every revocation: it closes
	// the event streams of the client (M14). It must not block nor call
	// access.
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
	id     string
	gen    uint64
	ticket uint64 // token of the reservation it made, 0 when none
}

// leaseHolder is the current holder, for the answers.
type leaseHolder struct {
	id      string
	expires time.Time
}

func unauthorized(code string) *auth.Rejection {
	msg := "Unknown client"
	if code == CodeDisconnected {
		msg = "Disconnected by the console"
	}
	return &auth.Rejection{Status: http.StatusUnauthorized, Code: code, Message: msg}
}

// --- helpers, called with mu held -----------------------------------------

// currentLocked drops an expired lease and reports whether one is held.
func (a *access) currentLocked() bool {
	if a.holder != "" && !a.now().Before(a.expires) {
		a.holder, a.expires, a.confirmed = "", time.Time{}, false
	}
	return a.holder != ""
}

func (a *access) setLocked(id string, confirmed bool) uint64 {
	a.tokens++
	a.holder, a.expires, a.confirmed, a.token = id, a.now().Add(WriteLeaseTTL), confirmed, a.tokens
	return a.token
}

func (a *access) dropLeaseLocked(id string) {
	if a.holder == id {
		a.holder, a.expires, a.confirmed = "", time.Time{}, false
	}
}

func (a *access) revokeLocked(id, code string) {
	a.gens++
	a.gen[id] = a.gens
	a.revokedCode[id] = code
	a.dropLeaseLocked(id)
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

func (a *access) holderLocked() leaseHolder { return leaseHolder{a.holder, a.expires} }

// --- operations ------------------------------------------------------------

// admit is the pre-body check of the gate, for client c as read by Lookup
// (its Epoch is the generation of the pairing that verified the request).
// A write reserves a free lease.
func (a *access) admit(c auth.Client, opts auth.RouteOptions) (admission, *leaseHolder, *auth.Rejection) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := c.ID
	ad := admission{id: id, gen: c.Epoch}
	if rej := a.checkGenLocked(ad, opts.WhileDisconnected); rej != nil {
		return admission{}, nil, rej
	}
	if opts.TakesLease {
		switch {
		case !a.currentLocked():
			ad.ticket = a.setLocked(id, false)
		case a.holder != id:
			h := a.holderLocked()
			return admission{}, &h, nil
		}
	}
	return ad, nil, nil
}

// accept is the commit of an accepted request (body verified): checks the
// generation; a write acquires or confirms the lease atomically; any other
// request renews a confirmed lease of its client; presence is recorded.
// For POST /clients/me/connect (WhileDisconnected), the disconnection is
// lifted here, in the same step that validated the generation (a newer
// disconnection bumped it, so it is never cleared by an older request);
// lifted reports whether there was one.
func (a *access) accept(ad admission, opts auth.RouteOptions) (other *leaseHolder, rej *auth.Rejection, lifted bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if rej := a.checkGenLocked(ad, opts.WhileDisconnected); rej != nil {
		return nil, rej, false
	}
	held := a.currentLocked()
	switch {
	case opts.TakesLease && !held:
		a.setLocked(ad.id, true)
	case opts.TakesLease && a.holder != ad.id:
		h := a.holderLocked()
		return &h, nil, false
	case held && a.holder == ad.id && (opts.TakesLease || a.confirmed):
		a.expires, a.confirmed = a.now().Add(WriteLeaseTTL), true
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
	return nil, nil, lifted
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

// rollback gives back the reservation of a rejected request, if nothing
// confirmed or replaced it since.
func (a *access) rollback(ad admission) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if ad.ticket != 0 && a.holder == ad.id && a.token == ad.ticket && !a.confirmed {
		a.holder, a.expires = "", time.Time{}
	}
}

// take is PUT /writer for an accepted request: take, renew or force.
// It returns the holder after the call, the previous holder (if another
// one), and whether the caller holds the lease.
func (a *access) take(ad admission, force bool) (h leaseHolder, previous string, ok bool, rej *auth.Rejection) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if rej := a.checkGenLocked(ad, false); rej != nil {
		return leaseHolder{}, "", false, rej
	}
	held := a.currentLocked()
	switch {
	case held && a.holder == ad.id:
		a.expires, a.confirmed = a.now().Add(WriteLeaseTTL), true
	case held && !force:
		return a.holderLocked(), "", false, nil
	default:
		if held {
			previous = a.holder
		}
		a.setLocked(ad.id, true)
	}
	return a.holderLocked(), previous, true, nil
}

// release is DELETE /writer: frees the lease if id holds it.
func (a *access) release(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.currentLocked() || a.holder != id {
		return false
	}
	a.dropLeaseLocked(id)
	return true
}

// current returns the holder, if any.
func (a *access) current() (leaseHolder, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.currentLocked() {
		return leaseHolder{}, false
	}
	return a.holderLocked(), true
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
// replaces the key: requests admitted with the old pairing are revoked,
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
	held := a.currentLocked()
	list := a.clients.List()
	out := make([]DeviceStatus, 0, len(list))
	for _, c := range list {
		_, connected := seen[c.ID]
		out = append(out, DeviceStatus{
			ID: c.ID, Name: c.Name, PairedAt: c.PairedAt,
			Connected:    connected,
			Writer:       held && a.holder == c.ID,
			Disconnected: a.disconnected[c.ID],
		})
	}
	return out
}
