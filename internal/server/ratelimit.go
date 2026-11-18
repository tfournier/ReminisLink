package server

import (
	"sync"
	"time"
)

// Rate limits of the unsigned routes, per IP (docs/specs/04-api-v1.md and
// 07-securite.md §4).
const (
	// HelloRate and HelloBurst: GET /hello, 10 requests per second.
	HelloRate  = 10.0
	HelloBurst = 10
	// PairRate and PairBurst: POST /pair. The spec only says "per IP";
	// 5 quick attempts, then one every 2 seconds. Brute force is stopped
	// by the pairing session itself (5 failures, then 30 s block).
	PairRate  = 0.5
	PairBurst = 5
	// maxBuckets bounds the memory of a limiter; full buckets are dropped
	// first when it is reached.
	maxBuckets = 4096
)

// rateLimiter is a token bucket per key (client IP).
type rateLimiter struct {
	mu      sync.Mutex
	rate    float64 // tokens per second
	burst   float64
	now     func() time.Time
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(rate float64, burst int, now func() time.Time) *rateLimiter {
	return &rateLimiter{rate: rate, burst: float64(burst), now: now, buckets: map[string]*bucket{}}
}

// allow takes a token for key. When there is none, it returns false and
// the time until the next token.
func (l *rateLimiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= maxBuckets {
			l.pruneLocked(now)
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = min(l.burst, b.tokens+elapsed*l.rate)
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	return false, wait
}

// pruneLocked drops the buckets that are full again (idle clients), or
// all of them if none is.
func (l *rateLimiter) pruneLocked(now time.Time) {
	for k, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.rate >= l.burst {
			delete(l.buckets, k)
		}
	}
	if len(l.buckets) >= maxBuckets {
		clear(l.buckets)
	}
}
