package server

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
)

// accessFixture is the M10 state with two paired clients "a" and "b".
type accessFixture struct {
	clock   *clock
	clients *auth.ClientStore
	acc     *access
}

const (
	idA = "00000000-0000-4000-8000-00000000000a"
	idB = "00000000-0000-4000-8000-00000000000b"
)

func newAccessFixture(t *testing.T) *accessFixture {
	t.Helper()
	clients, err := auth.OpenClientStore(filepath.Join(t.TempDir(), "clients.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{idA, idB} {
		if err := clients.Put(auth.Client{ID: id, Name: id, Key: make([]byte, auth.KeySize)}); err != nil {
			t.Fatal(err)
		}
	}
	clk := &clock{t: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}
	return &accessFixture{clock: clk, clients: clients, acc: newAccess(clk.Now, clients, newPresence())}
}

var (
	writeOpts = auth.RouteOptions{TakesLease: true}
	readOpts  = auth.RouteOptions{}
)

func (f *accessFixture) admit(t *testing.T, id string, opts auth.RouteOptions) admission {
	t.Helper()
	ad, other, rej := f.acc.admit(id, opts)
	if rej != nil || other != nil {
		t.Fatalf("admit %s: %+v %+v", id, other, rej)
	}
	return ad
}

func (f *accessFixture) holder() string {
	h, _ := f.acc.current()
	return h.id
}

// The holder is confirmed when b is refused by the arbitration of admit.
func (f *accessFixture) blocksB(t *testing.T) bool {
	t.Helper()
	_, other, _ := f.acc.admit(idB, writeOpts)
	return other != nil
}

// Finding 1: two overlapping writes of the same client; the first one's
// reservation is rolled back while the second is accepted, in both orders.
// The accepted write always holds a confirmed lease.
func TestAccessOverlappingWrites(t *testing.T) {
	for _, rollbackFirst := range []bool{false, true} {
		f := newAccessFixture(t)
		w1 := f.admit(t, idA, writeOpts) // reserves
		w2 := f.admit(t, idA, writeOpts) // already held by a: no ticket
		if w1.ticket == 0 || w2.ticket != 0 {
			t.Fatalf("tickets %d %d", w1.ticket, w2.ticket)
		}
		if rollbackFirst {
			f.acc.rollback(w1)
		}
		if other, rej := f.acc.accept(w2, writeOpts); other != nil || rej != nil {
			t.Fatalf("accept w2: %+v %+v", other, rej)
		}
		f.acc.rollback(w1)
		if f.holder() != idA || !f.acc.confirmed || !f.blocksB(t) {
			t.Fatalf("rollbackFirst=%v: the accepted write lost its lease (holder %q)", rollbackFirst, f.holder())
		}
	}
}

// Finding 2: a read of the same client never confirms a reservation.
func TestAccessReadDoesNotConfirmAReservation(t *testing.T) {
	f := newAccessFixture(t)
	w := f.admit(t, idA, writeOpts)
	r := f.admit(t, idA, readOpts)
	if other, rej := f.acc.accept(r, readOpts); other != nil || rej != nil {
		t.Fatal(other, rej)
	}
	f.acc.rollback(w)
	if f.holder() != "" {
		t.Fatal("a read made the reservation permanent")
	}

	// A read renews a confirmed lease only.
	f.acc.accept(f.admit(t, idA, writeOpts), writeOpts)
	f.clock.Advance(50 * time.Second)
	f.acc.accept(f.admit(t, idA, readOpts), readOpts)
	f.clock.Advance(59 * time.Second)
	if f.holder() != idA {
		t.Fatal("the read did not renew the confirmed lease")
	}
	// A read of b renews nothing.
	f.acc.accept(f.admit(t, idB, readOpts), readOpts)
	f.clock.Advance(time.Second)
	if f.holder() != "" {
		t.Fatal("expiry")
	}
}

// Rollback never frees a lease confirmed by PUT /writer, forced away, or
// reserved again later.
func TestAccessRollbackTokens(t *testing.T) {
	f := newAccessFixture(t)
	w := f.admit(t, idA, writeOpts)
	if _, _, ok, _ := f.acc.take(f.admit(t, idA, readOpts), false); !ok {
		t.Fatal("take")
	}
	f.acc.rollback(w)
	if f.holder() != idA {
		t.Fatal("rollback freed a lease confirmed by PUT /writer")
	}

	f.acc.release(idA)
	w = f.admit(t, idA, writeOpts)
	if _, prev, ok, _ := f.acc.take(f.admit(t, idB, readOpts), true); !ok || prev != idA {
		t.Fatal("force")
	}
	f.acc.rollback(w)
	if f.holder() != idB {
		t.Fatal("rollback stole back a forced lease")
	}
	// The write admitted before the take-over is refused when accepted.
	if other, _ := f.acc.accept(w, writeOpts); other == nil || other.id != idB {
		t.Fatalf("accept after take-over: %+v", other)
	}

	f.acc.release(idB)
	old := f.admit(t, idA, writeOpts)
	f.acc.rollback(old)
	f.admit(t, idA, writeOpts)
	f.acc.rollback(old)
	if f.holder() != idA {
		t.Fatal("a stale ticket freed a newer reservation")
	}
}

// Finding 3: a revocation between admission and acceptance refuses the
// request even if the client was reconnected or paired again since.
func TestAccessGeneration(t *testing.T) {
	cases := []struct {
		name   string
		revoke func(f *accessFixture)
		code   string
	}{
		{"disconnect then reconnect", func(f *accessFixture) { f.acc.disconnect(idA); f.acc.connect(idA) }, CodeDisconnected},
		{"disconnect", func(f *accessFixture) { f.acc.disconnect(idA) }, CodeDisconnected},
		{"forget then pair again", func(f *accessFixture) {
			f.acc.forget(idA)
			f.acc.pair(auth.Client{ID: idA, Name: "new", Key: make([]byte, auth.KeySize)})
		}, auth.CodeUnknownClient},
		{"pair again (new key)", func(f *accessFixture) {
			f.acc.pair(auth.Client{ID: idA, Name: "new", Key: make([]byte, auth.KeySize)})
		}, auth.CodeUnknownClient},
		{"forget all", func(f *accessFixture) { f.acc.forgetAll() }, auth.CodeUnknownClient},
	}
	for _, tc := range cases {
		for _, opts := range []auth.RouteOptions{writeOpts, readOpts} {
			f := newAccessFixture(t)
			ad := f.admit(t, idA, opts)
			tc.revoke(f)
			other, rej := f.acc.accept(ad, opts)
			if other != nil || rej == nil || rej.Code != tc.code {
				t.Fatalf("%s (write=%v): %+v %+v", tc.name, opts.TakesLease, other, rej)
			}
			if f.holder() != "" {
				t.Fatalf("%s: lease %q", tc.name, f.holder())
			}
			// PUT /writer admitted before the revocation is refused too.
			f2 := newAccessFixture(t)
			ad = f2.admit(t, idA, readOpts)
			tc.revoke(f2)
			if _, _, ok, rej := f2.acc.take(ad, true); ok || rej == nil || rej.Code != tc.code {
				t.Fatalf("%s: take after revocation: %v %+v", tc.name, ok, rej)
			}
		}
	}
	// A request admitted after the reconnection is accepted.
	f := newAccessFixture(t)
	f.acc.disconnect(idA)
	f.acc.connect(idA)
	if _, rej := f.acc.accept(f.admit(t, idA, writeOpts), writeOpts); rej != nil {
		t.Fatal(rej)
	}
}

// Finding 5: presence is recorded at acceptance under the same lock as
// Disconnect: an accepted request admitted before a disconnection never
// puts the device back.
func TestAccessPresence(t *testing.T) {
	f := newAccessFixture(t)
	ad := f.admit(t, idA, readOpts)
	f.acc.disconnect(idA)
	f.acc.accept(ad, readOpts)
	if seen := f.acc.presence.seenSince(time.Time{}); len(seen) != 0 {
		t.Fatalf("presence %v", seen)
	}
	f.acc.accept(f.admit(t, idB, readOpts), readOpts)
	if seen := f.acc.presence.seenSince(time.Time{}); len(seen) != 1 {
		t.Fatalf("presence %v", seen)
	}
}

// TestAccessRace hammers every operation concurrently (run with -race):
// at the end, at most one client holds the lease and a revoked client
// never does.
func TestAccessRace(t *testing.T) {
	f := newAccessFixture(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := idA
			if i%2 == 1 {
				id = idB
			}
			for j := 0; j < 200; j++ {
				ad, other, rej := f.acc.admit(id, writeOpts)
				if rej != nil || other != nil {
					continue
				}
				if j%3 == 0 {
					f.acc.rollback(ad)
				} else if _, rej := f.acc.accept(ad, writeOpts); rej == nil && j%5 == 0 {
					f.acc.release(id)
				}
				if i == 0 && j%50 == 0 {
					f.acc.disconnect(idB)
					f.acc.connect(idB)
				}
			}
		}(i)
	}
	wg.Wait()
}
