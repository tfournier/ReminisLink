package store

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Credited age (docs/specs/05-console-pak.md §4): the console clock cannot
// be trusted (drained battery, NTP setting it while the pak runs), so the
// trash ages its entries with a clock of its own, a credit C of seconds
// kept in config.json:
//   - while the pak runs, C follows the monotonic clock of the system
//     (never the wall clock); it is saved every CreditSaveEvery and when
//     the pak closes;
//   - at launch, C is credited with the wall time elapsed since the last
//     close, clamped to [0, MaxOfflineCredit];
//   - on the first launch (no saved value), C = 0.
//
// A wrong wall clock can therefore never age an entry by more than 7 days
// at once; at worst the purge is delayed.
//
// The offline time is counted from the latest wall time ever recorded
// (creditWallMax), not only from the last close (M8, Codex review of M4):
// a console whose clock is behind when the pak closes (RTC reset at boot,
// before NTP) and right when it opens (after NTP) would otherwise get 7
// days at each such pair of sessions, a month in a few minutes. With the
// high-water mark, only the wall time that goes past everything already
// seen is credited, which real time can do but a clock going back and
// forth cannot. A wall clock once set far in the future only delays the
// purge (the session time still counts). After a crash (the previous run
// did not close cleanly), nothing is credited for the time since its last
// save: that time is lost, never added (the prudent way).
// TODO(M0): check whether the Brick keeps its clock on battery and when
// NextUI sets it with NTP.

const (
	// MaxOfflineCredit bounds the wall time credited at launch.
	MaxOfflineCredit = 7 * 24 * time.Hour
	// CreditSaveEvery is the period of the saves while the pak runs.
	CreditSaveEvery = 5 * time.Minute
)

// Keys of config.json written only by CreditClock.
const (
	keyCredit     = "creditSeconds"
	keyCreditWall = "creditWall"
	keyCreditOpen = "creditOpen"
	// keyCreditWallMax is the latest wall time ever recorded.
	keyCreditWallMax = "creditWallMax"
)

// CreditClock is the credited-age clock.
type CreditClock struct {
	config *ConfigFile
	wall   func() time.Time
	mono   func() time.Duration

	mu        sync.Mutex
	base      time.Duration // C when the session started (after the launch credit)
	monoStart time.Duration
	wallMax   time.Time // latest wall time recorded (creditWallMax)
}

// NewCreditClock loads the credit from config, credits the offline time
// and saves it, marking the session open. wall is the wall clock (nil:
// time.Now); mono is a monotonic clock, any origin (nil: the monotonic
// reading of time.Now).
func NewCreditClock(config *ConfigFile, wall func() time.Time, mono func() time.Duration) (*CreditClock, error) {
	if wall == nil {
		wall = time.Now
	}
	if mono == nil {
		start := time.Now()
		mono = func() time.Duration { return time.Since(start) }
	}
	c := &CreditClock{config: config, wall: wall, mono: mono}
	fields, err := config.Read()
	if err != nil {
		return nil, fmt.Errorf("credit clock: %w", err)
	}
	var seconds int64
	var last time.Time
	var open bool
	_, hasCredit := fields[keyCredit]
	if hasCredit {
		if err := json.Unmarshal(fields[keyCredit], &seconds); err != nil || seconds < 0 {
			return nil, fmt.Errorf("credit clock: invalid %s", keyCredit)
		}
		if raw, ok := fields[keyCreditWall]; ok {
			if err := json.Unmarshal(raw, &last); err != nil {
				return nil, fmt.Errorf("credit clock: invalid %s", keyCreditWall)
			}
		}
		if raw, ok := fields[keyCreditOpen]; ok {
			_ = json.Unmarshal(raw, &open)
		}
		if raw, ok := fields[keyCreditWallMax]; ok {
			if err := json.Unmarshal(raw, &c.wallMax); err != nil {
				return nil, fmt.Errorf("credit clock: invalid %s", keyCreditWallMax)
			}
		}
	}
	if last.After(c.wallMax) {
		c.wallMax = last // a config.json written before creditWallMax
	}
	c.base = time.Duration(seconds) * time.Second
	if hasCredit && !open && !last.IsZero() {
		c.base += min(max(wall().Sub(c.wallMax), 0), MaxOfflineCredit)
	}
	c.monoStart = mono()
	if err := c.save(true); err != nil {
		return nil, err
	}
	return c, nil
}

// Now returns the current credit C.
func (c *CreditClock) Now() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nowLocked()
}

func (c *CreditClock) nowLocked() time.Duration {
	return c.base + max(c.mono()-c.monoStart, 0)
}

// Save records the credit (every CreditSaveEvery while running).
func (c *CreditClock) Save() error { return c.save(true) }

// Close records the credit and marks the session closed cleanly, so the
// next launch credits the time until then.
func (c *CreditClock) Close() error { return c.save(false) }

func (c *CreditClock) save(open bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	seconds := int64(c.nowLocked() / time.Second)
	wall := c.wall().UTC().Truncate(time.Second)
	if wall.After(c.wallMax) {
		c.wallMax = wall
	}
	wallMax := c.wallMax
	return c.config.Update(func(fields map[string]json.RawMessage) error {
		// Keys of the first M4 drafts (clock guard): ignored, removed.
		delete(fields, "lastSeenTime")
		delete(fields, "candidateTime")
		v, _ := json.Marshal(seconds)
		fields[keyCredit] = v
		v, _ = json.Marshal(wall)
		fields[keyCreditWall] = v
		v, _ = json.Marshal(open)
		fields[keyCreditOpen] = v
		v, _ = json.Marshal(wallMax)
		fields[keyCreditWallMax] = v
		return nil
	})
}

// Run saves the credit every CreditSaveEvery until stop is closed.
func (c *CreditClock) Run(stop <-chan struct{}) {
	t := time.NewTicker(CreditSaveEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			_ = c.Save()
		}
	}
}
