// Package writelock runs the writes of the SD card one at a time (M13,
// protocol/openapi.yaml "Writes one at a time"): every operation that
// changes the card holds the lock for its critical section. A request
// waits at most the lock's wait, then gets "Device busy"; a job waits as
// long as it takes.
package writelock

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// DefaultWait is how long a request waits for the write in progress.
const DefaultWait = 10 * time.Second

// ErrBusy means a request waited the whole wait without getting the lock.
var ErrBusy = errors.New("device busy")

// Lock is a mutex whose acquisition can time out or be cancelled.
type Lock struct {
	ch   chan struct{}
	wait atomic.Int64 // nanoseconds
	// held counts the acquisitions, for tests.
	held atomic.Uint64
}

// New returns a free lock whose requests wait at most wait (<= 0:
// DefaultWait).
func New(wait time.Duration) *Lock {
	l := &Lock{ch: make(chan struct{}, 1)}
	l.SetWait(wait)
	return l
}

// SetWait changes the longest wait of a request (<= 0: DefaultWait).
func (l *Lock) SetWait(wait time.Duration) {
	if wait <= 0 {
		wait = DefaultWait
	}
	l.wait.Store(int64(wait))
}

// Wait returns the longest wait of a request.
func (l *Lock) Wait() time.Duration { return time.Duration(l.wait.Load()) }

// AcquireRequest takes the lock for a request: it waits at most Wait,
// then returns ErrBusy, or ctx.Err() if ctx ends first.
func (l *Lock) AcquireRequest(ctx context.Context) error {
	select {
	case l.ch <- struct{}{}:
		l.held.Add(1)
		return nil
	default:
	}
	t := time.NewTimer(l.Wait())
	defer t.Stop()
	select {
	case l.ch <- struct{}{}:
		l.held.Add(1)
		return nil
	case <-t.C:
		return ErrBusy
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Acquire takes the lock without a time limit (a job's install step);
// it returns ctx.Err() if ctx ends first.
func (l *Lock) Acquire(ctx context.Context) error {
	select {
	case l.ch <- struct{}{}:
		l.held.Add(1)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TryAcquire takes the lock if it is free.
func (l *Lock) TryAcquire() bool {
	select {
	case l.ch <- struct{}{}:
		l.held.Add(1)
		return true
	default:
		return false
	}
}

// Release frees the lock. Releasing a free lock panics.
func (l *Lock) Release() {
	select {
	case <-l.ch:
	default:
		panic("writelock: release of a free lock")
	}
}

// Acquisitions returns how many times the lock was taken (tests).
func (l *Lock) Acquisitions() uint64 { return l.held.Load() }
