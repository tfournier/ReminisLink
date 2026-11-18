package auth

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestByteBudgetWaitsAndReleases(t *testing.T) {
	b := newByteBudget(100)
	n, err := b.acquire(context.Background(), 70)
	if err != nil || n != 70 {
		t.Fatalf("acquire = %d, %v", n, err)
	}
	got := make(chan int64)
	go func() {
		n, _ := b.acquire(context.Background(), 50)
		got <- n
	}()
	select {
	case <-got:
		t.Fatal("acquired beyond the budget")
	case <-time.After(20 * time.Millisecond):
	}
	b.release(70)
	if n := <-got; n != 50 {
		t.Fatalf("waiter got %d", n)
	}
	b.release(50)
	if b.used != 0 || len(b.waiters) != 0 {
		t.Fatalf("used %d, waiters %d", b.used, len(b.waiters))
	}
}

func TestByteBudgetCapsAndCancels(t *testing.T) {
	b := newByteBudget(100)
	n, err := b.acquire(context.Background(), 1000)
	if err != nil || n != 100 {
		t.Fatalf("large acquire = %d, %v (must be capped)", n, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error)
	go func() {
		_, err := b.acquire(ctx, 10)
		errc <- err
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	b.release(100)
	if b.used != 0 || len(b.waiters) != 0 {
		t.Fatalf("cancelled waiter kept budget: used %d, waiters %d", b.used, len(b.waiters))
	}
}

// With the budget used up by a request in progress, another request waits;
// if its client goes away it is answered without reading its body.
func TestMiddlewareBodyBudget(t *testing.T) {
	f := newMWFixture(t, 0)
	f.verifier.BodyBudget = 2048
	release := make(chan struct{})
	slow := f.verifier.Require(RouteJSON, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))

	first := f.request(signOpts{method: "POST", target: "/api/v1/x", body: bytes.Repeat([]byte{'a'}, 2048)})
	first.RequestURI = "/api/v1/x"
	done := make(chan int)
	go func() {
		rec := httptest.NewRecorder()
		slow.ServeHTTP(rec, first)
		done <- rec.Code
	}()
	for {
		f.verifier.bodyBudget().mu.Lock()
		used := f.verifier.bodyBudget().used
		f.verifier.bodyBudget().mu.Unlock()
		if used == 2048 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	second := f.request(signOpts{method: "POST", target: "/api/v1/x", body: []byte("{}")}).WithContext(ctx)
	counter := &countingReader{size: 2}
	second.Body = io.NopCloser(counter)
	second.RequestURI = "/api/v1/x"
	rec := httptest.NewRecorder()
	slow.ServeHTTP(rec, second)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), CodeShuttingDown) {
		t.Fatalf("second: %d %s", rec.Code, rec.Body.String())
	}
	if counter.n != 0 {
		t.Fatal("body read without budget")
	}

	close(release)
	if code := <-done; code != http.StatusNoContent {
		t.Fatalf("first: %d", code)
	}
	if used := f.verifier.bodyBudget().used; used != 0 {
		t.Fatalf("budget not released: %d", used)
	}
}
