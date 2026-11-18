package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"time"
)

// The console clock may be wrong: no network time before the Wi-Fi is up,
// a reset or a drained real-time clock. A clock set back must neither date
// new trash entries in the past (they would be purged early) nor let the
// purge run against wrong ages. TimeGuard remembers in config.json
// ("lastSeenTime") the latest time the pak ever observed.
// TODO(M0): check whether the Brick has a real-time clock kept on battery
// and whether NextUI sets the time with NTP.

// MaxClockJump: a clock that moved forward by more than this since the
// last observed time is not trusted for the purge (it may be aberrant).
// A real absence that long only skips one purge.
const MaxClockJump = 366 * 24 * time.Hour

// timeGuardPersistEvery bounds the writes of config.json.
const timeGuardPersistEvery = time.Minute

// TimeGuard is the latest time observed by the pak, kept in config.json.
type TimeGuard struct {
	path string
	now  func() time.Time

	mu        sync.Mutex
	last      time.Time // latest time observed
	persisted time.Time // value in config.json
	previous  time.Time // value at load (for the purge decision)
}

// NewTimeGuard reads lastSeenTime from the config file at path (now nil:
// time.Now). A missing file or field starts with no known time.
func NewTimeGuard(path string, now func() time.Time) (*TimeGuard, error) {
	if now == nil {
		now = time.Now
	}
	g := &TimeGuard{path: path, now: now}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("time guard: %w", err)
	}
	if err == nil {
		var c struct {
			LastSeenTime *time.Time `json:"lastSeenTime"`
		}
		if err := json.Unmarshal(data, &c); err != nil {
			return nil, fmt.Errorf("time guard: %w", err)
		}
		if c.LastSeenTime != nil {
			g.last, g.persisted, g.previous = *c.LastSeenTime, *c.LastSeenTime, *c.LastSeenTime
		}
	}
	return g, nil
}

// Now returns max(clock, latest observed time) and records the clock when
// it is ahead.
func (g *TimeGuard) Now() time.Time {
	n := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if !n.After(g.last) {
		return g.last
	}
	g.last = n
	if n.Sub(g.persisted) >= timeGuardPersistEvery {
		if err := g.persistLocked(); err == nil {
			g.persisted = n
		}
	}
	return n
}

// Trusted reports whether the clock can be used to judge ages: not behind
// the latest observed time, not too far ahead of the time known at load.
func (g *TimeGuard) Trusted() bool {
	n := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if n.Before(g.last) {
		return false
	}
	return g.previous.IsZero() || n.Sub(g.previous) <= MaxClockJump
}

// Save writes the latest observed time now (at the end of the maintenance
// or at shutdown).
func (g *TimeGuard) Save() error {
	g.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.last.Equal(g.persisted) {
		return nil
	}
	if err := g.persistLocked(); err != nil {
		return err
	}
	g.persisted = g.last
	return nil
}

// persistLocked sets lastSeenTime in config.json, keeping its other
// fields (atomic write).
func (g *TimeGuard) persistLocked() error {
	fields := map[string]json.RawMessage{}
	data, err := os.ReadFile(g.path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("time guard: %w", err)
	}
	if err == nil {
		if err := json.Unmarshal(data, &fields); err != nil {
			return fmt.Errorf("time guard: %w", err)
		}
	}
	v, _ := json.Marshal(g.last.UTC().Truncate(time.Second))
	fields["lastSeenTime"] = v
	out, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(g.path, append(out, '\n'), 0o644)
}
