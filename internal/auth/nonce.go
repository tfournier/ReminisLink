package auth

import (
	"errors"
	"sync"
	"time"
)

const (
	// NonceTTL is the minimum time a request nonce is remembered (spec:
	// 10 minutes). It must be at least 2 × MaxClockSkew.
	NonceTTL = 10 * time.Minute
	// DefaultNonceCapacity bounds the memory of the cache.
	DefaultNonceCapacity = 50000
)

var (
	// ErrReplay means the nonce was already used by this client.
	ErrReplay = errors.New("nonce already used")
	// ErrNonceCacheFull means the cache holds only live nonces and cannot
	// take a new one. The cache never forgets a live nonce to make room,
	// since that would allow a replay: it refuses the request instead.
	ErrNonceCacheFull = errors.New("nonce cache full")
)

// NonceCache remembers request nonces per client, in memory, with a
// bounded number of entries and an injectable clock.
//
// A nonce is kept until max(signed time + MaxClockSkew, added + ttl),
// bounds included: as long as the clock check could still accept the
// request (|X-RL-Time - now| <= MaxClockSkew), its nonce is known. All
// comparisons use the wall clock (monotonic reading stripped), the same
// clock as the skew check, so a wall clock change (NTP on Wi-Fi connection)
// moves both together.
type NonceCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	capacity int
	now      func() time.Time
	expiry   map[string]time.Time
	order    []nonceEntry // insertion order (usually expiry order)
	head     int
}

type nonceEntry struct {
	key     string
	expires time.Time
}

// NewNonceCache returns a cache. A nil now uses time.Now; pass the same
// clock as the Verifier. It panics if ttl < 2 × MaxClockSkew: a shorter
// TTL would let a request be replayed while its timestamp is accepted.
func NewNonceCache(ttl time.Duration, capacity int, now func() time.Time) *NonceCache {
	if ttl < 2*MaxClockSkew {
		panic("auth: nonce TTL must be at least 2 x MaxClockSkew")
	}
	if now == nil {
		now = time.Now
	}
	if capacity <= 0 {
		capacity = DefaultNonceCapacity
	}
	return &NonceCache{
		ttl:      ttl,
		capacity: capacity,
		now:      now,
		expiry:   make(map[string]time.Time),
	}
}

func nonceKey(clientID, nonce string) string { return clientID + "\x00" + nonce }

func (c *NonceCache) wallNow() time.Time { return c.now().Round(0) }

// Seen reports whether the nonce is currently remembered for the client.
func (c *NonceCache) Seen(clientID, nonce string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	exp, ok := c.expiry[nonceKey(clientID, nonce)]
	return ok && !c.wallNow().After(exp)
}

// Add records the nonce of a request signed at signedAt (X-RL-Time). It
// returns ErrReplay if the nonce is already remembered and
// ErrNonceCacheFull if there is no room. Check and insertion are atomic.
func (c *NonceCache) Add(clientID, nonce string, signedAt time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.wallNow()
	c.evictLocked(now)
	key := nonceKey(clientID, nonce)
	if exp, ok := c.expiry[key]; ok && !now.After(exp) {
		return ErrReplay
	}
	if len(c.expiry) >= c.capacity {
		// The queue may be out of expiry order after a wall clock change:
		// sweep everything before giving up.
		c.sweepLocked(now)
		if len(c.expiry) >= c.capacity {
			return ErrNonceCacheFull
		}
	}
	exp := now.Add(c.ttl)
	if bySignature := signedAt.Round(0).Add(MaxClockSkew); bySignature.After(exp) {
		exp = bySignature
	}
	c.expiry[key] = exp
	c.order = append(c.order, nonceEntry{key: key, expires: exp})
	return nil
}

// RetryAfter returns how long until the next nonce expires (at least one
// second), for the Retry-After header of a "cache full" answer.
func (c *NonceCache) RetryAfter() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.wallNow()
	var next time.Time
	for _, exp := range c.expiry {
		if next.IsZero() || exp.Before(next) {
			next = exp
		}
	}
	d := next.Sub(now) + time.Nanosecond // expiry is inclusive
	if next.IsZero() || d < time.Second {
		return time.Second
	}
	return d
}

// Len returns the number of remembered nonces (expired ones included until
// they are evicted).
func (c *NonceCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.expiry)
}

func (c *NonceCache) evictLocked(now time.Time) {
	for c.head < len(c.order) {
		e := c.order[c.head]
		if !now.After(e.expires) {
			break
		}
		if exp, ok := c.expiry[e.key]; ok && exp.Equal(e.expires) {
			delete(c.expiry, e.key)
		}
		c.order[c.head] = nonceEntry{}
		c.head++
	}
	c.compactLocked()
}

func (c *NonceCache) sweepLocked(now time.Time) {
	for key, exp := range c.expiry {
		if now.After(exp) {
			delete(c.expiry, key)
		}
	}
	kept := c.order[:0]
	for _, e := range c.order[c.head:] {
		if exp, ok := c.expiry[e.key]; ok && exp.Equal(e.expires) {
			kept = append(kept, e)
		}
	}
	clear(c.order[len(kept):])
	c.order = kept
	c.head = 0
}

// compactLocked drops the consumed head of the queue once it is half of it.
func (c *NonceCache) compactLocked() {
	if c.head > 0 && c.head*2 >= len(c.order) {
		n := copy(c.order, c.order[c.head:])
		clear(c.order[n:])
		c.order = c.order[:n]
		c.head = 0
	}
}
