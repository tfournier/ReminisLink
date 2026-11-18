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

// ConfigFile is the single writer of config.json during a run: every
// change reads the file, changes some fields and writes it back
// atomically under one lock, keeping the fields it does not know.
type ConfigFile struct {
	path string
	mu   sync.Mutex
}

// NewConfigFile returns the writer of the config file at path.
func NewConfigFile(path string) *ConfigFile { return &ConfigFile{path: path} }

// Read returns the fields of the file (empty when it does not exist).
func (c *ConfigFile) Read() (map[string]json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readLocked()
}

func (c *ConfigFile) readLocked() (map[string]json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	data, err := os.ReadFile(c.path)
	if errors.Is(err, fs.ErrNotExist) {
		return fields, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return fields, nil
}

// Update changes the fields with fn and writes the file atomically.
func (c *ConfigFile) Update(fn func(fields map[string]json.RawMessage) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	fields, err := c.readLocked()
	if err != nil {
		return err
	}
	if err := fn(fields); err != nil {
		return err
	}
	out, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return WriteFileAtomic(c.path, append(out, '\n'), 0o644)
}

// Save writes the fields of cfg, keeping the other fields of the file
// (lastSeenTime included when cfg.LastSeenTime is nil). Settings changed
// during a run (milestone M7) must go through it.
func (c *ConfigFile) Save(cfg Config) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	var set map[string]json.RawMessage
	if err := json.Unmarshal(data, &set); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return c.Update(func(fields map[string]json.RawMessage) error {
		for k, v := range set {
			fields[k] = v
		}
		return nil
	})
}

// The console clock may be wrong: a drained real-time clock (1970 or the
// firmware date), no network time before the Wi-Fi is up, a reset. A wrong
// clock must neither date trash entries in the past (they would be purged
// early) nor let the purge judge ages. TimeGuard keeps in config.json the
// latest trusted time the pak observed ("lastSeenTime").
//
// The clock is trusted when it is not before MinPlausibleTime (a drained
// clock), not before lastSeenTime (a clock set back), and at most
// MaxClockJump after lastSeenTime as read at launch (on the first launch:
// not after MaxFirstLaunchTime). An untrusted time is never recorded.
//
// A jump of more than MaxClockJump (an aberrant clock, or a real absence
// of more than a year) is kept as a candidate ("candidateTime") and
// accepted at a later launch at least CandidateConfirmDelay after it, if
// the clock is still consistent with it (not before it, at most
// MaxClockJump after it). So after a real absence of more than a year the
// automatic purge resumes from the next launch an hour or more later; an
// aberrant jump followed by the right time is dropped and never recorded.
// Meanwhile "Empty the trash" (DELETE /trash) still works.
//
// Trash entries created while the clock is not trusted are marked
// clockUntrusted: they are never purged, and are dated again at the first
// launch with a trusted clock (they then live TrashRetention from there).
// TODO(M0): check whether the Brick has a real-time clock kept on battery
// and whether NextUI sets the time with NTP.

// MinPlausibleTime returns the earliest plausible clock: an earlier one is
// a drained real-time clock.
func MinPlausibleTime() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

// MaxFirstLaunchTime returns the latest clock trusted on the first launch.
func MaxFirstLaunchTime() time.Time { return time.Date(2056, 1, 1, 0, 0, 0, 0, time.UTC) }

const (
	// MaxClockJump: a clock that moved forward by more than this since the
	// time known at launch is not trusted.
	MaxClockJump = 366 * 24 * time.Hour
	// CandidateConfirmDelay: a jump is accepted at a launch at least this
	// long after it was first seen.
	CandidateConfirmDelay = time.Hour
	// timeGuardPersistEvery bounds the writes of config.json.
	timeGuardPersistEvery = time.Minute
)

// TimeGuard is the latest trusted time observed by the pak.
type TimeGuard struct {
	config *ConfigFile
	now    func() time.Time

	mu        sync.Mutex
	last      time.Time // latest trusted time observed
	persisted time.Time // lastSeenTime in config.json
	previous  time.Time // lastSeenTime at launch (zero: first launch)
	candidate time.Time // a jump seen at a previous launch (zero: none)
}

// NewTimeGuard reads lastSeenTime and candidateTime from config (now nil:
// time.Now) and accepts a confirmed candidate.
func NewTimeGuard(config *ConfigFile, now func() time.Time) (*TimeGuard, error) {
	if now == nil {
		now = time.Now
	}
	g := &TimeGuard{config: config, now: now}
	fields, err := config.Read()
	if err != nil {
		return nil, fmt.Errorf("time guard: %w", err)
	}
	read := func(key string) (time.Time, error) {
		var t time.Time
		if raw, ok := fields[key]; ok && string(raw) != "null" {
			if err := json.Unmarshal(raw, &t); err != nil {
				return t, fmt.Errorf("time guard: %s: %w", key, err)
			}
		}
		return t, nil
	}
	if g.previous, err = read("lastSeenTime"); err != nil {
		return nil, err
	}
	if g.candidate, err = read("candidateTime"); err != nil {
		return nil, err
	}
	g.last, g.persisted = g.previous, g.previous
	n := now()
	if !g.candidate.IsZero() && !n.Before(g.candidate.Add(CandidateConfirmDelay)) && n.Sub(g.candidate) <= MaxClockJump {
		// The jump is confirmed by a later launch.
		g.previous, g.last = g.candidate, g.candidate
		g.candidate = time.Time{}
		if err := g.persistLocked(); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// trustedLocked tells whether n can be trusted.
func (g *TimeGuard) trustedLocked(n time.Time) bool {
	switch {
	case n.Before(MinPlausibleTime()), n.Before(g.last):
		return false
	case g.previous.IsZero():
		return !n.After(MaxFirstLaunchTime())
	}
	return n.Sub(g.previous) <= MaxClockJump
}

// Now returns the time to date an event, max(clock, latest trusted time),
// and whether the clock is trusted. A trusted clock is recorded; an
// untrusted jump ahead becomes the candidate.
func (g *TimeGuard) Now() (time.Time, bool) {
	n := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.trustedLocked(n) {
		if !g.previous.IsZero() && n.Sub(g.previous) > MaxClockJump && g.candidate.IsZero() {
			g.candidate = n
			_ = g.persistLocked()
		}
		if n.After(g.last) {
			return n, false
		}
		return g.last, false
	}
	if n.After(g.last) {
		g.last = n
		if !g.candidate.IsZero() {
			g.candidate = time.Time{} // the clock came back: drop the jump
			_ = g.persistLocked()
		}
		if n.Sub(g.persisted) >= timeGuardPersistEvery {
			_ = g.persistLocked()
		}
	}
	return g.last, true
}

// Trusted reports whether the clock can be used to judge ages.
func (g *TimeGuard) Trusted() bool {
	_, ok := g.Now()
	return ok
}

// Save writes the latest trusted time (end of maintenance, shutdown).
func (g *TimeGuard) Save() error {
	g.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.last.Equal(g.persisted) {
		return nil
	}
	return g.persistLocked()
}

// persistLocked writes lastSeenTime and candidateTime.
func (g *TimeGuard) persistLocked() error {
	err := g.config.Update(func(fields map[string]json.RawMessage) error {
		if !g.last.IsZero() {
			v, _ := json.Marshal(g.last.UTC().Truncate(time.Second))
			fields["lastSeenTime"] = v
		}
		if g.candidate.IsZero() {
			delete(fields, "candidateTime")
		} else {
			v, _ := json.Marshal(g.candidate.UTC().Truncate(time.Second))
			fields["candidateTime"] = v
		}
		return nil
	})
	if err == nil {
		g.persisted = g.last
	}
	return err
}
