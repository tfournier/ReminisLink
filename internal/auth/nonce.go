package auth

import (
	"errors"
	"sync"
	"time"
)

const (
	// NonceTTL is how long a request nonce is remembered. It must stay
	// above 2 × MaxClockSkew so a request cannot be replayed while its
	// timestamp is still accepted.
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

// NonceCache remembers request nonces per client for a fixed time, in
// memory, with a bounded number of entries and an injectable clock.
type NonceCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	capacity int
	now      func() time.Time
	expiry   map[string]time.Time
	order    []nonceEntry // insertion order, which is expiry order
	head     int
}

type nonceEntry struct {
	key     string
	expires time.Time
}

// NewNonceCache returns a cache. A nil now uses time.Now.
func NewNonceCache(ttl time.Duration, capacity int, now func() time.Time) *NonceCache {
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

// Seen reports whether the nonce is currently remembered for the client.
func (c *NonceCache) Seen(clientID, nonce string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	exp, ok := c.expiry[nonceKey(clientID, nonce)]
	return ok && c.now().Before(exp)
}

// Add records the nonce for the client. It returns ErrReplay if it is
// already remembered and ErrNonceCacheFull if there is no room. Check and
// insertion are atomic.
func (c *NonceCache) Add(clientID, nonce string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.evictLocked(now)
	key := nonceKey(clientID, nonce)
	if exp, ok := c.expiry[key]; ok && now.Before(exp) {
		return ErrReplay
	}
	if len(c.expiry) >= c.capacity {
		return ErrNonceCacheFull
	}
	exp := now.Add(c.ttl)
	c.expiry[key] = exp
	c.order = append(c.order, nonceEntry{key: key, expires: exp})
	return nil
}

// Len returns the number of remembered nonces (expired ones included until
// the next Add).
func (c *NonceCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.expiry)
}

func (c *NonceCache) evictLocked(now time.Time) {
	for c.head < len(c.order) {
		e := c.order[c.head]
		if now.Before(e.expires) {
			break
		}
		if exp, ok := c.expiry[e.key]; ok && exp.Equal(e.expires) {
			delete(c.expiry, e.key)
		}
		c.order[c.head] = nonceEntry{}
		c.head++
	}
	// Compact the queue once half of it is consumed.
	if c.head > 0 && c.head*2 >= len(c.order) {
		c.order = append(c.order[:0], c.order[c.head:]...)
		c.head = 0
	}
}
