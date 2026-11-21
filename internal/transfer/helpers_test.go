package transfer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/saves"
	"github.com/tfournier/ReminisLink/internal/states"
	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

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

type env struct {
	t      *testing.T
	sd     string
	clock  *testClock
	free   *int64
	logs   *syncBuffer
	layout store.Layout
	trash  *store.Trash
	lib    *library.Library
	opts   Options
	svc    *Service
}

const (
	gbaSystem = "Game Boy Advance (GBA)"
	gbaTag    = "GBA"
)

func newEnv(t *testing.T, tweak ...func(*Options)) *env {
	t.Helper()
	sd := testutil.CopySDCard(t)
	roots := nextui.RootsForSD(sd, "tg5040")
	adapter := nextui.New(roots, func(string) string { return "" })
	layout := store.Layout{Root: roots.Data}
	clk := &testClock{t: time.Now().Truncate(time.Second)}
	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	free := int64(1 << 40)
	e := &env{t: t, sd: sd, clock: clk, free: &free, logs: logs, layout: layout}
	e.trash = store.NewTrash(layout, sd, clk.Now, logger)
	e.lib = library.New(adapter, layout)
	lock := &sync.Mutex{}
	sv, err := saves.New(saves.Options{Adapter: adapter, Layout: layout, Trash: e.trash, Lock: lock, Now: clk.Now, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	st, err := states.New(states.Options{Adapter: adapter, Layout: layout, Trash: e.trash, Lock: lock, Now: clk.Now, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	e.opts = Options{
		Library: e.lib, Adapter: adapter, Layout: layout, Trash: e.trash,
		Saves: sv, States: st, Lock: lock,
		FreeSpace: func(string) (int64, error) { return *e.free, nil },
		Now:       clk.Now, Logger: logger, ChunkSize: 1 << 10,
	}
	for _, f := range tweak {
		f(&e.opts)
	}
	e.svc = e.newService()
	t.Cleanup(func() { e.svc.Close() })
	return e
}

// newService builds a new Service on the same card (a restart).
func (e *env) newService() *Service {
	e.t.Helper()
	s, err := New(e.opts)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := s.Maintain(); err != nil {
		e.t.Fatal(err)
	}
	if err := s.LoadUploads(); err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *env) path(rel string) string { return filepath.Join(e.sd, filepath.FromSlash(rel)) }

func (e *env) read(rel string) string {
	e.t.Helper()
	data, err := os.ReadFile(e.path(rel))
	if err != nil {
		e.t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func (e *env) exists(rel string) bool {
	_, err := os.Lstat(e.path(rel))
	return err == nil
}

func sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func romTarget(name string) *Target {
	return &Target{Kind: KindROM, SystemID: library.EncodeID(gbaSystem), FileName: ptr(name)}
}

func gameID(rel string) string { return library.EncodeID(rel) }
