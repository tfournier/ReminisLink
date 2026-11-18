package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/device"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/testutil"
	"github.com/tfournier/ReminisLink/internal/testutil/openapi"
)

const testDeviceID = "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10"

var (
	specOnce sync.Once
	spec     *openapi.Spec
	specErr  error
)

func contract(t testing.TB) *openapi.Spec {
	t.Helper()
	specOnce.Do(func() {
		spec, specErr = openapi.Load(filepath.Join(testutil.RepoRoot(), "protocol", "openapi.yaml"))
	})
	if specErr != nil {
		t.Fatal(specErr)
	}
	return spec
}

// clock is a settable clock shared by the server, the verifier and the
// pairing session.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// syncBuffer collects log output from several goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type fixture struct {
	t       *testing.T
	sd      string
	clock   *clock
	logs    *syncBuffer
	server  *Server
	http    *httptest.Server
	clients *auth.ClientStore
	pairing *auth.PairingSession
	client  *testutil.Client // a paired client
}

// newFixture serves the API on a copy of the fake SD card. The clock
// starts at the real time, so testutil.Client can sign with time.Now.
func newFixture(t *testing.T, tweak ...func(*Options)) *fixture {
	t.Helper()
	sd := testutil.CopySDCard(t)
	roots := nextui.RootsForSD(sd, "tg5040")
	env := map[string]string{"DEVICE": "brick", "PLATFORM": "tg5040"}
	adapter := nextui.New(roots, func(k string) string { return env[k] })
	layout := store.Layout{Root: roots.Data}
	clients, err := auth.OpenClientStore(layout.ClientsFile())
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Now().Truncate(time.Second)}
	pairing, err := auth.NewPairingSession(testDeviceID, clk.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	opts := Options{
		Identity: Identity{DeviceID: testDeviceID, DeviceName: "Brick de test", PakVersion: "1.0.0-test"},
		Adapter:  adapter,
		Library:  library.New(adapter, layout),
		Clients:  clients,
		Pairing:  pairing,
		Verifier: auth.NewVerifier(clients, clk.Now, 0, logger),
		Now:      clk.Now,
		Logger:   logger,
		IPv4:     func() (string, bool) { return "192.168.1.42", true },
		Storage:  func(string) (device.Storage, error) { return device.Storage{TotalBytes: 1000, FreeBytes: 400}, nil },
	}
	for _, f := range tweak {
		f(&opts)
	}
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(s.Handler())
	t.Cleanup(hs.Close)
	f := &fixture{t: t, sd: sd, clock: clk, logs: logs, server: s, http: hs, clients: clients, pairing: pairing}
	f.client = testutil.PairClient(t, clients, hs.URL)
	f.client.Now = clk.Now
	return f
}

// check validates a response against the contract (path relative to
// /api/v1, as written in openapi.yaml).
func (f *fixture) check(method, path string, r testutil.Response) {
	f.t.Helper()
	if err := contract(f.t).ValidateResponse(method, path, r.Status, r.Header, r.Body); err != nil {
		f.t.Errorf("contract: %v\nbody: %s", err, r.Body)
	}
}

// get sends a signed GET, validates the response and checks its status.
func (f *fixture) get(target, path string, want int) testutil.Response {
	f.t.Helper()
	r := f.client.Do(f.t, http.MethodGet, target, nil)
	f.check(http.MethodGet, path, r)
	if r.Status != want {
		f.t.Errorf("GET %s: status %d, want %d: %s", target, r.Status, want, r.Body)
	}
	return r
}

// unsigned sends a request without signature headers.
func (f *fixture) unsigned(method, target string, body []byte) testutil.Response {
	f.t.Helper()
	req, err := http.NewRequest(method, f.http.URL+target, bytes.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return f.client.Send(f.t, req)
}
