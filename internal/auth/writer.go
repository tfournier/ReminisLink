package auth

import (
	"sync"
	"time"
)

// WriteLeaseTTL is how long the write lease lasts after its last renewal
// (protocol/openapi.yaml "Single writer (M10)").
const WriteLeaseTTL = 60 * time.Second

// LeaseHolder is the client holding the write lease.
type LeaseHolder struct {
	ClientID  string
	ExpiresAt time.Time
}

// WriteLease is the single-writer lease (M10): at most one paired client
// may change the SD card at a time. It is kept in memory only, so it is
// free at launch and dropped when the pak closes. Safe for concurrent use.
//
// A lease taken by a request that is not accepted yet (its body still
// unread) is provisional: TryAcquire takes it, Renew confirms it once the
// request is accepted, and Rollback frees it if the request is rejected.
type WriteLease struct {
	mu        sync.Mutex
	now       func() time.Time
	holder    string // client id, "" when free
	expires   time.Time
	gen       uint64 // changes each time the lease is taken
	confirmed bool   // false while the take is provisional
}

// LeaseTicket identifies a provisional take of the lease (the zero ticket:
// nothing was taken).
type LeaseTicket struct{ gen uint64 }

// NewWriteLease returns a free lease on the given clock (nil: time.Now).
func NewWriteLease(now func() time.Time) *WriteLease {
	if now == nil {
		now = time.Now
	}
	return &WriteLease{now: now}
}

// currentLocked drops an expired holder and reports whether one remains.
func (l *WriteLease) currentLocked(now time.Time) bool {
	if l.holder != "" && !now.Before(l.expires) {
		l.holder, l.expires = "", time.Time{}
	}
	return l.holder != ""
}

// Acquire takes the lease for clientID if it is free, expired or already
// held by clientID (a renewal), or held by another client and force is
// true, for an accepted request (the take is confirmed). It returns the
// holder after the call and whether it is clientID.
func (l *WriteLease) Acquire(clientID string, force bool) (LeaseHolder, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.currentLocked(now) && l.holder != clientID && !force {
		return LeaseHolder{ClientID: l.holder, ExpiresAt: l.expires}, false
	}
	if l.holder != clientID {
		l.gen++
	}
	l.holder, l.expires, l.confirmed = clientID, now.Add(WriteLeaseTTL), true
	return LeaseHolder{ClientID: l.holder, ExpiresAt: l.expires}, true
}

// TryAcquire is the arbitration of a write request whose body is not read
// yet. If another client holds the lease, it returns that holder and false.
// If clientID holds it, nothing changes (it is renewed only once the
// request is accepted). If it is free, clientID takes it provisionally:
// the ticket lets Rollback free it if the request is rejected.
func (l *WriteLease) TryAcquire(clientID string) (LeaseHolder, LeaseTicket, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.currentLocked(now) {
		h := LeaseHolder{ClientID: l.holder, ExpiresAt: l.expires}
		return h, LeaseTicket{}, l.holder == clientID
	}
	l.gen++
	l.holder, l.expires, l.confirmed = clientID, now.Add(WriteLeaseTTL), false
	return LeaseHolder{ClientID: l.holder, ExpiresAt: l.expires}, LeaseTicket{l.gen}, true
}

// Rollback frees a provisional take of clientID that nothing confirmed
// since (no accepted request, no other take in between).
func (l *WriteLease) Rollback(clientID string, t LeaseTicket) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if t.gen != 0 && l.holder == clientID && l.gen == t.gen && !l.confirmed {
		l.holder, l.expires = "", time.Time{}
	}
}

// Renew pushes the expiry of the lease to WriteLeaseTTL from now if
// clientID holds it (confirming a provisional take), and reports whether
// it does. Call it for accepted requests only.
func (l *WriteLease) Renew(clientID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if !l.currentLocked(now) || l.holder != clientID {
		return false
	}
	l.expires, l.confirmed = now.Add(WriteLeaseTTL), true
	return true
}

// Holder returns the current holder, if any.
func (l *WriteLease) Holder() (LeaseHolder, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.currentLocked(l.now()) {
		return LeaseHolder{}, false
	}
	return LeaseHolder{ClientID: l.holder, ExpiresAt: l.expires}, true
}

// Release frees the lease if clientID holds it, and reports whether it
// did.
func (l *WriteLease) Release(clientID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.currentLocked(l.now()) || l.holder != clientID {
		return false
	}
	l.holder, l.expires = "", time.Time{}
	return true
}
