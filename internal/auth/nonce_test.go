package auth

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 11, 18, 17, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestNonceTTLCoversClockSkew(t *testing.T) {
	if NonceTTL < 2*MaxClockSkew {
		t.Fatalf("NonceTTL %v must be >= 2 x MaxClockSkew %v", NonceTTL, MaxClockSkew)
	}
}

func TestNonceCacheReplayAndExpiry(t *testing.T) {
	clock := newFakeClock()
	c := NewNonceCache(NonceTTL, 10, clock.Now)

	if c.Seen("a", "n1") {
		t.Fatal("fresh nonce reported as seen")
	}
	if err := c.Add("a", "n1"); err != nil {
		t.Fatal(err)
	}
	if !c.Seen("a", "n1") {
		t.Fatal("nonce not remembered")
	}
	if err := c.Add("a", "n1"); !errors.Is(err, ErrReplay) {
		t.Fatalf("replay error = %v", err)
	}
	// Nonces are per client.
	if c.Seen("b", "n1") {
		t.Fatal("nonce leaked to another client")
	}
	if err := c.Add("b", "n1"); err != nil {
		t.Fatal(err)
	}

	clock.Advance(NonceTTL)
	if !c.Seen("a", "n1") {
		t.Fatal("nonce forgotten at its TTL (the bound is inclusive)")
	}
	if err := c.Add("a", "n1"); !errors.Is(err, ErrReplay) {
		t.Fatalf("replay at TTL: %v", err)
	}
	clock.Advance(time.Nanosecond)
	if c.Seen("a", "n1") {
		t.Fatal("nonce still remembered after its TTL")
	}
	if err := c.Add("a", "n1"); err != nil {
		t.Fatalf("expired nonce refused: %v", err)
	}
	if c.Len() != 1 {
		t.Fatalf("expired entries not evicted: len = %d", c.Len())
	}
}

func TestNonceCacheIsBoundedAndFailsClosed(t *testing.T) {
	clock := newFakeClock()
	c := NewNonceCache(NonceTTL, 3, clock.Now)
	for i := 0; i < 3; i++ {
		if err := c.Add("a", fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Add("a", "x"); !errors.Is(err, ErrNonceCacheFull) {
		t.Fatalf("error = %v, want ErrNonceCacheFull", err)
	}
	// Live nonces are never dropped to make room.
	for i := 0; i < 3; i++ {
		if !c.Seen("a", fmt.Sprint(i)) {
			t.Fatalf("nonce %d dropped", i)
		}
	}
	clock.Advance(NonceTTL + time.Nanosecond)
	if err := c.Add("a", "x"); err != nil {
		t.Fatalf("room not reclaimed after expiry: %v", err)
	}
}

func TestNonceCacheConcurrentAddIsAtomic(t *testing.T) {
	c := NewNonceCache(NonceTTL, 100, nil)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if c.Add("a", "same") == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("%d concurrent adds succeeded, want 1", ok)
	}
}

func TestNonceCacheQueueCompaction(t *testing.T) {
	clock := newFakeClock()
	c := NewNonceCache(time.Second, 5, clock.Now)
	for i := 0; i < 1000; i++ {
		if err := c.Add("a", fmt.Sprint(i)); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
		clock.Advance(time.Second)
	}
	if len(c.order)-c.head > 2 || cap(c.order) > 64 {
		t.Fatalf("queue grows without bound: len %d head %d cap %d", len(c.order), c.head, cap(c.order))
	}
}
