package writelock

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestSerializes(t *testing.T) {
	l := New(time.Second)
	var mu sync.Mutex
	inside, maxInside := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.AcquireRequest(context.Background()); err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			inside++
			maxInside = max(maxInside, inside)
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			inside--
			mu.Unlock()
			l.Release()
		}()
	}
	wg.Wait()
	if maxInside != 1 || l.Acquisitions() != 8 {
		t.Fatalf("max inside %d, acquisitions %d", maxInside, l.Acquisitions())
	}
}

func TestRequestTimesOutAndJobWaits(t *testing.T) {
	l := New(30 * time.Millisecond)
	if !l.TryAcquire() {
		t.Fatal("free lock not taken")
	}
	if l.TryAcquire() {
		t.Fatal("held lock taken")
	}
	start := time.Now()
	if err := l.AcquireRequest(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d < 30*time.Millisecond {
		t.Fatalf("gave up after %v", d)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.AcquireRequest(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	// A job waits longer than the request wait.
	got := make(chan error, 1)
	go func() { got <- l.Acquire(context.Background()) }()
	time.Sleep(60 * time.Millisecond)
	select {
	case err := <-got:
		t.Fatalf("job did not wait: %v", err)
	default:
	}
	l.Release()
	if err := <-got; err != nil {
		t.Fatal(err)
	}
	l.Release()
	if err := l.Acquire(ctx); err == nil {
		// Free lock: Acquire may win the select against a done context.
		l.Release()
	}
}

// The generation is odd while the lock is held and changes at every
// acquisition and release, whatever the way the lock is taken.
func TestGeneration(t *testing.T) {
	l := New(time.Second)
	g := l.Generation()
	if g%2 != 0 {
		t.Fatalf("free lock: generation %d", g)
	}
	for i, take := range []func() error{
		func() error { return l.AcquireRequest(context.Background()) },
		func() error { return l.Acquire(context.Background()) },
		func() error {
			if !l.TryAcquire() {
				return errors.New("busy")
			}
			return nil
		},
	} {
		if err := take(); err != nil {
			t.Fatal(err)
		}
		if held := l.Generation(); held != g+1 {
			t.Fatalf("%d: held, generation %d after %d", i, held, g)
		}
		l.Release()
		if free := l.Generation(); free != g+2 {
			t.Fatalf("%d: released, generation %d after %d", i, free, g)
		}
		g += 2
	}
}

func TestDefaultsAndMisuse(t *testing.T) {
	if New(0).Wait() != DefaultWait {
		t.Fatal("default wait")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("release of a free lock did not panic")
		}
	}()
	New(0).Release()
}
