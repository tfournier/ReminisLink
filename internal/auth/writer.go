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
type WriteLease struct {
	mu      sync.Mutex
	now     func() time.Time
	holder  string // client id, "" when free
	expires time.Time
}

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
// true. It returns the holder after the call and whether it is clientID.
func (l *WriteLease) Acquire(clientID string, force bool) (LeaseHolder, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.currentLocked(now) && l.holder != clientID && !force {
		return LeaseHolder{ClientID: l.holder, ExpiresAt: l.expires}, false
	}
	l.holder, l.expires = clientID, now.Add(WriteLeaseTTL)
	return LeaseHolder{ClientID: l.holder, ExpiresAt: l.expires}, true
}

// Renew pushes the expiry of the lease to WriteLeaseTTL from now if
// clientID holds it, and reports whether it does.
func (l *WriteLease) Renew(clientID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if !l.currentLocked(now) || l.holder != clientID {
		return false
	}
	l.expires = now.Add(WriteLeaseTTL)
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
