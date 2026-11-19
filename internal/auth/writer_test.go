package auth

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type leaseClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *leaseClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *leaseClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestWriteLease(t *testing.T) {
	clk := &leaseClock{t: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}
	l := NewWriteLease(clk.Now)
	if _, ok := l.Holder(); ok {
		t.Fatal("held at launch")
	}

	// The first client takes it.
	h, ok := l.Acquire("a", false)
	if !ok || h.ClientID != "a" || !h.ExpiresAt.Equal(clk.Now().Add(WriteLeaseTTL)) {
		t.Fatalf("acquire a = %+v %v", h, ok)
	}
	// Another one is refused and told who holds it.
	if h, ok := l.Acquire("b", false); ok || h.ClientID != "a" {
		t.Fatalf("acquire b = %+v %v", h, ok)
	}
	if l.Renew("b") || l.Release("b") {
		t.Fatal("b renewed or released a lease it does not hold")
	}

	// Renewal pushes the expiry.
	clk.Advance(50 * time.Second)
	if !l.Renew("a") {
		t.Fatal("renew a")
	}
	clk.Advance(59 * time.Second)
	if h, ok := l.Holder(); !ok || h.ClientID != "a" {
		t.Fatalf("59 s after the renewal: %+v %v", h, ok)
	}
	// Expired 60 s after the last renewal.
	clk.Advance(time.Second)
	if h, ok := l.Holder(); ok {
		t.Fatalf("60 s after the renewal: %+v", h)
	}
	if l.Renew("a") {
		t.Fatal("an expired lease was renewed")
	}
	if _, ok := l.Acquire("b", false); !ok {
		t.Fatal("b cannot take an expired lease")
	}

	// Force takes it from the holder.
	if h, ok := l.Acquire("a", true); !ok || h.ClientID != "a" {
		t.Fatalf("force = %+v %v", h, ok)
	}
	if !l.Release("a") {
		t.Fatal("release")
	}
	if _, ok := l.Holder(); ok {
		t.Fatal("held after release")
	}
	if l.Release("a") {
		t.Fatal("released twice")
	}
}

// TestWriteLeaseRace: many clients race for a free lease; exactly one
// wins. Run with -race.
func TestWriteLeaseRace(t *testing.T) {
	l := NewWriteLease(nil)
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, ok := l.Acquire(id, false); ok {
				wins.Add(1)
			}
			l.Renew(id)
			l.Holder()
		}(fmt.Sprint("client-", i))
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d winners", wins.Load())
	}
}
