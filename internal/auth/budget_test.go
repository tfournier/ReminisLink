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

// budgetFixture builds a verifier with a small body budget and a handler
// that blocks until release is closed.
func budgetFixture(t *testing.T, budget int64) (*mwFixture, http.Handler, chan struct{}) {
	t.Helper()
	f := newMWFixture(t, 0)
	v := &Verifier{Clients: f.verifier.Clients, Nonces: f.verifier.Nonces, Now: f.verifier.Now, BodyBudget: budget}
	f.verifier = v
	release := make(chan struct{})
	h := v.Require(RouteOptions{BodyLimit: budget}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			<-release
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	return f, h, release
}

func budgetUsed(v *Verifier) (int64, int) {
	b := v.bodyBudget()
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used, len(b.waiters)
}

func waitBudget(t *testing.T, v *Verifier, used int64, waiters int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		u, w := budgetUsed(v)
		if u == used && w == waiters {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("budget: used %d waiters %d, want %d and %d", u, w, used, waiters)
		}
		time.Sleep(time.Millisecond)
	}
}

func serveAsync(h http.Handler, r *http.Request) chan int {
	done := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		done <- rec.Code
	}()
	return done
}

// With the budget used up by a request in progress, another request waits;
// when its wait is cancelled it is answered without reading its body.
func TestMiddlewareBodyBudget(t *testing.T) {
	f, h, release := budgetFixture(t, 2048)
	first := f.request(signOpts{method: "POST", target: "/api/v1/x", body: bytes.Repeat([]byte{'a'}, 2048)})
	first.RequestURI = "/api/v1/x"
	done := serveAsync(h, first)
	waitBudget(t, f.verifier, 2048, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	second := f.request(signOpts{method: "POST", target: "/api/v1/x", body: []byte("{}")}).WithContext(ctx)
	counter := &countingReader{size: 2}
	second.Body = io.NopCloser(counter)
	second.RequestURI = "/api/v1/x"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, second)
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
	waitBudget(t, f.verifier, 0, 0)
}

// A request without a body never waits behind uploads holding the budget.
func TestMiddlewareBodylessRequestDoesNotQueue(t *testing.T) {
	f, h, release := budgetFixture(t, 2048)
	defer close(release)
	holder := f.request(signOpts{method: "POST", target: "/api/v1/x", body: bytes.Repeat([]byte{'a'}, 2048)})
	holder.RequestURI = "/api/v1/x"
	serveAsync(h, holder)
	waitBudget(t, f.verifier, 2048, 0)
	waiter := f.request(signOpts{method: "POST", target: "/api/v1/x", body: bytes.Repeat([]byte{'b'}, 1024)})
	waiter.RequestURI = "/api/v1/x"
	serveAsync(h, waiter)
	waitBudget(t, f.verifier, 2048, 1)

	req := f.request(signOpts{method: "GET", target: "/api/v1/device"})
	req.RequestURI = "/api/v1/device"
	select {
	case code := <-serveAsync(h, req):
		if code != http.StatusNoContent {
			t.Fatalf("GET: %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("GET without a body queued behind the uploads")
	}
}

func TestRequireRefusesLimitAboveBudget(t *testing.T) {
	v := &Verifier{BodyBudget: 1024}
	defer func() {
		if recover() == nil {
			t.Fatal("a route limit above the body budget was accepted")
		}
	}()
	v.Require(RouteOptions{BodyLimit: 1025}, http.NotFoundHandler())
}
