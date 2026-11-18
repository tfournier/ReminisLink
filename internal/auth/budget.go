package auth

import (
	"context"
	"sync"
)

// byteBudget is a weighted semaphore counted in bytes: it bounds the memory
// used by all signed bodies read at the same time. Waiters are served in
// order and stop waiting when their context is cancelled. With HTTP/1.1 the
// request context is NOT cancelled when the client closes the connection
// while its body is unread: only server shutdown (BaseContext) or a read
// deadline frees such a waiter (see Verifier.Require).
type byteBudget struct {
	mu      sync.Mutex
	size    int64
	used    int64
	waiters []*budgetWaiter
}

type budgetWaiter struct {
	n     int64
	ready chan struct{}
}

func newByteBudget(size int64) *byteBudget { return &byteBudget{size: size} }

// acquire reserves n bytes and returns the amount reserved, to pass to
// release. n must not exceed the budget size (Verifier.Require ensures it);
// it is capped defensively.
func (b *byteBudget) acquire(ctx context.Context, n int64) (int64, error) {
	if n > b.size {
		n = b.size
	}
	b.mu.Lock()
	if len(b.waiters) == 0 && b.used+n <= b.size {
		b.used += n
		b.mu.Unlock()
		return n, nil
	}
	w := &budgetWaiter{n: n, ready: make(chan struct{})}
	b.waiters = append(b.waiters, w)
	b.mu.Unlock()

	select {
	case <-w.ready:
		return n, nil
	case <-ctx.Done():
		b.mu.Lock()
		select {
		case <-w.ready:
			// Granted while we were giving up: hand it back.
			b.mu.Unlock()
			b.release(n)
		default:
			for i, x := range b.waiters {
				if x == w {
					b.waiters = append(b.waiters[:i], b.waiters[i+1:]...)
					break
				}
			}
			b.notifyLocked()
			b.mu.Unlock()
		}
		return 0, ctx.Err()
	}
}

func (b *byteBudget) release(n int64) {
	b.mu.Lock()
	b.used -= n
	b.notifyLocked()
	b.mu.Unlock()
}

func (b *byteBudget) notifyLocked() {
	for len(b.waiters) > 0 && b.used+b.waiters[0].n <= b.size {
		w := b.waiters[0]
		b.used += w.n
		close(w.ready)
		b.waiters[0] = nil
		b.waiters = b.waiters[1:]
	}
}
