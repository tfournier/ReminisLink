package server

import (
	"crypto/ed25519"
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
		if err := clients.Put(auth.Client{ID: id, Name: id, Platform: auth.PlatformApple, PublicKey: testKey(t)}); err != nil {
			t.Fatal(err)
		}
	}
	clk := &clock{t: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}
	return &accessFixture{clock: clk, clients: clients, acc: newAccess(clk.Now, clients, newPresence())}
}

var readOpts = auth.RouteOptions{}

// testKey is a random Ed25519 public key.
func testKey(t *testing.T) ed25519.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func (f *accessFixture) admit(t *testing.T, id string, opts auth.RouteOptions) admission {
	t.Helper()
	c, _ := f.acc.Lookup(id)
	ad, rej := f.acc.admit(c, opts)
	if rej != nil {
		t.Fatalf("admit %s: %+v", id, rej)
	}
	return ad
}

// reconnect is an accepted POST /clients/me/connect.
func (f *accessFixture) reconnect(t *testing.T, id string) {
	t.Helper()
	opts := auth.RouteOptions{WhileDisconnected: true}
	if rej, _ := f.acc.accept(f.admit(t, id, opts), opts); rej != nil {
		t.Fatal(rej)
	}
}

// Finding 3: a revocation between admission and acceptance refuses the
// request even if the client was reconnected or paired again since.
func TestAccessGeneration(t *testing.T) {
	cases := []struct {
		name   string
		revoke func(t *testing.T, f *accessFixture)
		code   string
	}{
		{"disconnect then reconnect", func(t *testing.T, f *accessFixture) { f.acc.disconnect(idA); f.reconnect(t, idA) }, CodeDisconnected},
		{"disconnect", func(t *testing.T, f *accessFixture) { f.acc.disconnect(idA) }, CodeDisconnected},
		{"forget then pair again", func(t *testing.T, f *accessFixture) {
			f.acc.forget(idA)
			f.acc.pair(auth.Client{ID: idA, Name: "new", Platform: auth.PlatformApple, PublicKey: testKey(t)})
		}, auth.CodeUnknownClient},
		{"pair again (new key)", func(t *testing.T, f *accessFixture) {
			f.acc.pair(auth.Client{ID: idA, Name: "new", Platform: auth.PlatformApple, PublicKey: testKey(t)})
		}, auth.CodeUnknownClient},
		{"forget all", func(t *testing.T, f *accessFixture) { f.acc.forgetAll() }, auth.CodeUnknownClient},
	}
	for _, tc := range cases {
		f := newAccessFixture(t)
		ad := f.admit(t, idA, readOpts)
		tc.revoke(t, f)
		rej, _ := f.acc.accept(ad, readOpts)
		if rej == nil || rej.Code != tc.code {
			t.Fatalf("%s: %+v", tc.name, rej)
		}
	}
	// A request admitted after the reconnection is accepted.
	f := newAccessFixture(t)
	f.acc.disconnect(idA)
	f.reconnect(t, idA)
	if rej, _ := f.acc.accept(f.admit(t, idA, readOpts), readOpts); rej != nil {
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
	if seen := f.acc.presence.seenSince(time.Time{}, time.Time{}); len(seen) != 0 {
		t.Fatalf("presence %v", seen)
	}
	f.acc.accept(f.admit(t, idB, readOpts), readOpts)
	if seen := f.acc.presence.seenSince(time.Time{}, time.Time{}); len(seen) != 1 {
		t.Fatalf("presence %v", seen)
	}
}

// TestAccessRace hammers every operation concurrently (run with -race).
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
				c, _ := f.acc.Lookup(id)
				ad, rej := f.acc.admit(c, readOpts)
				if rej != nil {
					continue
				}
				f.acc.accept(ad, readOpts)
				if i == 0 && j%50 == 0 {
					f.acc.disconnect(idB)
					opts := auth.RouteOptions{WhileDisconnected: true}
					if c, ok := f.acc.Lookup(idB); ok {
						if ad, rej := f.acc.admit(c, opts); rej == nil {
							f.acc.accept(ad, opts)
						}
					}
				}
			}
		}(i)
	}
	wg.Wait()
}

// Codex round 3, finding 1: a client read (key and generation) before a
// forget + re-pairing is refused by admit, even though the id is paired.
func TestAccessAdmitChecksTheEpochOfTheLookup(t *testing.T) {
	f := newAccessFixture(t)
	old, _ := f.acc.Lookup(idA)
	f.acc.forget(idA)
	f.acc.pair(auth.Client{ID: idA, Name: "new", Platform: auth.PlatformApple, PublicKey: testKey(t)})
	_, rej := f.acc.admit(old, readOpts)
	if rej == nil || rej.Code != auth.CodeUnknownClient {
		t.Fatalf("admit with a stale lookup: %+v", rej)
	}
	cur, _ := f.acc.Lookup(idA)
	if _, rej := f.acc.admit(cur, readOpts); rej != nil {
		t.Fatal(rej)
	}
}

// Codex round 3, finding 3: a connect request admitted before a newer
// disconnection does not lift it.
func TestAccessConnectDoesNotLiftANewerDisconnection(t *testing.T) {
	f := newAccessFixture(t)
	f.acc.disconnect(idA)
	opts := auth.RouteOptions{WhileDisconnected: true}
	ad := f.admit(t, idA, opts)
	f.reconnect(t, idA)   // lifted by another connect...
	f.acc.disconnect(idA) // ...then disconnected again
	rej, lifted := f.acc.accept(ad, opts)
	if rej == nil || rej.Code != CodeDisconnected || lifted || !f.acc.disconnected[idA] {
		t.Fatalf("stale connect: %+v lifted=%v disconnected=%v", rej, lifted, f.acc.disconnected[idA])
	}
	if seen := f.acc.presence.seenSince(time.Time{}, time.Time{}); len(seen) != 0 {
		t.Fatalf("presence %v", seen)
	}
	// A connect admitted now lifts it and records presence.
	ad = f.admit(t, idA, opts)
	if rej, lifted := f.acc.accept(ad, opts); rej != nil || !lifted || f.acc.disconnected[idA] {
		t.Fatalf("connect: %+v %v", rej, lifted)
	}
	if seen := f.acc.presence.seenSince(time.Time{}, time.Time{}); len(seen) != 1 {
		t.Fatalf("presence %v", seen)
	}
}

// Codex round 3, finding 4: the Devices rows read presence under the same
// lock as the disconnections.
func TestAccessDevicesConsistent(t *testing.T) {
	f := newAccessFixture(t)
	f.acc.accept(f.admit(t, idA, readOpts), readOpts)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			f.acc.disconnect(idA)
			opts := auth.RouteOptions{WhileDisconnected: true}
			if c, ok := f.acc.Lookup(idA); ok {
				if ad, rej := f.acc.admit(c, opts); rej == nil {
					f.acc.accept(ad, opts)
				}
			}
		}
	}()
	for i := 0; i < 2000; i++ {
		for _, d := range f.acc.devices(time.Time{}) {
			if d.Connected && d.Disconnected {
				close(stop)
				wg.Wait()
				t.Fatalf("row both connected and disconnected: %+v", d)
			}
		}
	}
	close(stop)
	wg.Wait()
}
