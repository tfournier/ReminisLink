package server

import (
	"os"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

func connected(t *testing.T, f *fixture, c *testutil.Client) []ConnectedClient {
	t.Helper()
	r := c.Do(t, "GET", "/api/v1/device", nil)
	f.check("GET", "/device", r)
	if r.Status != 200 {
		t.Fatalf("GET /device: %d %s", r.Status, r.Body)
	}
	var d deviceResponse
	r.JSON(t, &d)
	return d.ConnectedClients
}

func TestConnectedClients(t *testing.T) {
	f := newFixture(t)
	if got := f.server.ConnectedClients(); len(got) != 0 {
		t.Fatalf("not empty at launch: %+v", got)
	}
	// The caller is in its own list.
	got := connected(t, f, f.client)
	if len(got) != 1 || got[0].ClientName != "Test iPhone" || !got[0].LastSeenAt.Equal(f.clock.Now()) {
		t.Fatalf("caller only: %+v", got)
	}

	other := testutil.PairClient(t, f.clients, f.http.URL)
	other.Now = f.clock.Now
	f.clock.Advance(10 * time.Second)
	if r := other.Do(t, "GET", "/api/v1/systems", nil); r.Status != 200 {
		t.Fatal(r.Status)
	}
	// A rejected request does not count.
	stranger := &testutil.Client{BaseURL: f.http.URL, ClientID: f.client.ClientID, Key: testutil.NewKey(t), Now: f.clock.Now}
	f.clock.Advance(5 * time.Second)
	stranger.Do(t, "GET", "/api/v1/systems", nil)
	if got := f.server.ConnectedClients(); len(got) != 2 || !got[0].LastSeenAt.Equal(f.clock.Now().Add(-5*time.Second)) || !got[1].LastSeenAt.Equal(f.clock.Now().Add(-15*time.Second)) {
		t.Fatalf("after a rejected request: %+v", got)
	}

	// Most recent first.
	got = connected(t, f, f.client)
	if len(got) != 2 || !got[0].LastSeenAt.Equal(f.clock.Now()) || !got[1].LastSeenAt.Equal(f.clock.Now().Add(-5*time.Second)) {
		t.Fatalf("two clients: %+v", got)
	}

	// 30 s after its last request a client is still listed, not after.
	f.clock.Advance(25 * time.Second)
	if got := f.server.ConnectedClients(); len(got) != 2 {
		t.Fatalf("at 30 s: %+v", got)
	}
	f.clock.Advance(time.Second)
	if got := connected(t, f, f.client); len(got) != 1 || got[0].ClientName != "Test iPhone" {
		t.Fatalf("after 31 s: %+v", got)
	}

	// An unpaired client disappears at once.
	if r := other.Do(t, "GET", "/api/v1/systems", nil); r.Status != 200 {
		t.Fatal(r.Status)
	}
	if r := other.Do(t, "DELETE", "/api/v1/clients/me", nil); r.Status != 204 {
		t.Fatal(r.Status)
	}
	if got := connected(t, f, f.client); len(got) != 1 {
		t.Fatalf("after unpairing: %+v", got)
	}
	if _, err := f.clients.Remove(f.client.ClientID); err != nil {
		t.Fatal(err)
	}
	if got := f.server.ConnectedClients(); len(got) != 0 {
		t.Fatalf("client removed from the store: %+v", got)
	}

	// Nothing about presence is written to the SD card.
	entries, err := os.ReadDir(nextui.RootsForSD(f.sd, "").Data)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "clients.json" {
			t.Errorf("unexpected file in the data folder: %s", e.Name())
		}
	}
}
