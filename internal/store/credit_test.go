package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const day = 24 * time.Hour

// clocks are a wall clock and a monotonic clock, independent.
type clocks struct {
	wall time.Time
	mono time.Duration
}

func (c *clocks) Wall() time.Time               { return c.wall }
func (c *clocks) Mono() time.Duration           { return c.mono }
func (c *clocks) run(d time.Duration)           { c.wall, c.mono = c.wall.Add(d), c.mono+d }
func (c *clocks) off(wallElapsed time.Duration) { c.wall = c.wall.Add(wallElapsed) }

type creditEnv struct {
	t     *testing.T
	cf    *ConfigFile
	clk   *clocks
	trash *Trash
	sd    string
	clock *CreditClock
}

func newCreditEnv(t *testing.T) *creditEnv {
	t.Helper()
	tr, sd, wall, _ := newTestTrash(t)
	clk := &clocks{wall: time.Date(2026, 11, 18, 17, 0, 0, 0, time.UTC), mono: 123 * time.Hour}
	tr.now = clk.Wall
	_ = wall
	e := &creditEnv{t: t, cf: NewConfigFile(filepath.Join(t.TempDir(), "config.json")), clk: clk, trash: tr, sd: sd}
	e.launch()
	return e
}

// launch starts the pak: a new credit clock (the monotonic clock of a new
// boot restarts anywhere).
func (e *creditEnv) launch() {
	e.t.Helper()
	e.clk.mono = time.Duration(len(e.t.Name())) * time.Hour // arbitrary origin
	c, err := NewCreditClock(e.cf, e.clk.Wall, e.clk.Mono)
	if err != nil {
		e.t.Fatal(err)
	}
	e.clock = c
	e.trash.UseCreditClock(c)
}

func (e *creditEnv) closePak() {
	e.t.Helper()
	if err := e.clock.Close(); err != nil {
		e.t.Fatal(err)
	}
}

func (e *creditEnv) move(name string) TrashEntry {
	e.t.Helper()
	res, err := e.trash.Move(ReasonOverwrite, name, []MoveItem{{Path: write(e.t, e.sd, name, "x")}})
	if err != nil {
		e.t.Fatal(err)
	}
	return res.Entry
}

func (e *creditEnv) count() int {
	entries, _, err := e.trash.List()
	if err != nil {
		e.t.Fatal(err)
	}
	return len(entries)
}

func (e *creditEnv) purge() {
	e.t.Helper()
	if _, err := e.trash.Purge(TrashRetention); err != nil {
		e.t.Fatal(err)
	}
}

func TestCreditFirstLaunchAndSession(t *testing.T) {
	e := newCreditEnv(t)
	if e.clock.Now() != 0 {
		t.Fatalf("first launch credit %v", e.clock.Now())
	}
	e.move("a")
	// A 31-day session (monotonic), the wall clock frozen: purged.
	e.clk.mono += 31 * day
	e.purge()
	if e.count() != 0 {
		t.Fatal("not purged after 31 days of session")
	}
}

func TestCreditOfflineIsClamped(t *testing.T) {
	e := newCreditEnv(t)
	e.move("a")
	e.closePak()
	e.clk.off(365 * day) // the wall clock jumps a year
	e.launch()
	if got := e.clock.Now(); got != 7*day {
		t.Fatalf("credited %v, want 7 days", got)
	}
	e.purge()
	if e.count() != 1 {
		t.Fatal("purged by a wall clock jump")
	}
}

func TestCreditFiveWeeklyOutages(t *testing.T) {
	e := newCreditEnv(t)
	e.move("a")
	for i := 1; i <= 5; i++ {
		e.closePak()
		e.clk.off(7 * day)
		e.launch()
		e.purge()
		if want := map[bool]int{true: 0, false: 1}[i == 5]; e.count() != want {
			t.Fatalf("outage %d: %d entries", i, e.count())
		}
		if i < 5 {
			// Credited age 28 days after 4 outages: expiresAt in 2 days.
			entries, _, _ := e.trash.List()
			if want := e.clk.wall.Add(TrashRetention - time.Duration(i)*7*day); !entries[0].ExpiresAt.Equal(want) {
				t.Fatalf("expiresAt %v, want %v", entries[0].ExpiresAt, want)
			}
		}
	}
}

func TestCreditNTPDuringSessionAndClockBack(t *testing.T) {
	e := newCreditEnv(t)
	e.move("a")
	e.closePak()
	// Off for a while with a frozen clock (no credit), then NTP moves the
	// wall clock a year ahead during the session: no credit from the jump.
	e.launch()
	if e.clock.Now() != 0 {
		t.Fatalf("frozen clock credited %v", e.clock.Now())
	}
	e.clk.run(time.Hour)
	e.clk.wall = e.clk.wall.Add(365 * day)
	if got := e.clock.Now(); got != time.Hour {
		t.Fatalf("session credit %v, want 1h", got)
	}
	e.closePak()
	// The wall clock goes back: 0 credited.
	e.clk.wall = e.clk.wall.Add(-400 * day)
	e.launch()
	if got := e.clock.Now(); got != time.Hour {
		t.Fatalf("clock back credited %v", got-time.Hour)
	}
}

func TestCreditCrashLosesTheTimeSinceTheLastSave(t *testing.T) {
	e := newCreditEnv(t)
	e.clk.run(10 * time.Minute)
	if err := e.clock.Save(); err != nil { // periodic save
		t.Fatal(err)
	}
	e.clk.run(4 * time.Minute) // then a crash: no final save
	e.clk.off(3 * day)
	e.launch()
	if got := e.clock.Now(); got != 10*time.Minute {
		t.Fatalf("after a crash %v, want 10m (never more)", got)
	}
	// A clean close afterwards credits the next outage again.
	e.closePak()
	e.clk.off(day)
	e.launch()
	if got := e.clock.Now(); got != 10*time.Minute+day {
		t.Fatalf("after a clean close %v", got)
	}
}

func TestCreditEntryWithoutCreditIsKept(t *testing.T) {
	e := newCreditEnv(t)
	broken := write(t, e.trash.Dir(), "tr_20261001T000000Z_ffff/x", "456")
	os.WriteFile(filepath.Join(filepath.Dir(broken), trashEntryFile), []byte("{"), 0o644)
	old := write(t, e.trash.Dir(), "tr_20261001T000000Z_eeee/y", "7")
	os.WriteFile(filepath.Join(filepath.Dir(old), trashEntryFile), []byte(`{"version":1,"state":"done","id":"tr_20261001T000000Z_eeee","reason":"overwrite","trashedAt":"2026-10-01T00:00:00Z"}`), 0o644)
	e.move("a")
	e.clk.mono += 60 * day
	e.purge()
	if !exists(filepath.Dir(broken)) || !exists(filepath.Dir(old)) {
		t.Fatal("entry without credit purged")
	}
	entries, _, _ := e.trash.List()
	for _, en := range entries {
		if en.ExpiresAt.Before(e.clk.wall) {
			t.Fatalf("expiresAt in the past: %+v", en)
		}
	}
	if len(entries) != 1 { // the old-version entry; "a" was purged
		t.Fatalf("entries %+v", entries)
	}
	// Without a credit clock nothing is purged.
	e.trash.UseCreditClock(nil)
	e.move("b")
	if freed, _ := e.trash.Purge(0); freed != 0 {
		t.Fatal("purged without a credit clock")
	}
}

func TestCreditConfigKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"version":1,"deviceName":"Brick","lastSeenTime":"2026-11-18T17:00:00Z","candidateTime":"2030-01-01T00:00:00Z"}`), 0o644)
	cf := NewConfigFile(path)
	c, err := NewCreditClock(cf, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := cf.Save(Config{Version: 1, DeviceName: "Renamed", Port: 47821}); err != nil {
		t.Fatal(err)
	}
	c.Close()
	var m map[string]any
	data, _ := os.ReadFile(path)
	json.Unmarshal(data, &m)
	if m["deviceName"] != "Renamed" || m["creditSeconds"] == nil || m["creditOpen"] != false || m["lastSeenTime"] != nil || m["candidateTime"] != nil {
		t.Fatalf("config.json %s", data)
	}
	os.WriteFile(path, []byte(`{"creditSeconds":-1}`), 0o644)
	if _, err := NewCreditClock(cf, nil, nil); err == nil {
		t.Fatal("negative credit accepted")
	}
}

// Codex review of M4: the clock is behind when the pak closes (RTC reset
// at boot, before NTP) and right when it opens (after NTP). Without a
// bound, each pair of sessions credited 7 days: five pairs in a few
// minutes purged a fresh entry.
func TestCreditClockBackAndForthIsNotCreditedTwice(t *testing.T) {
	e := newCreditEnv(t)
	real := e.clk.wall
	behind := real.Add(-200 * day)
	e.move("a")
	for i := 0; i < 5; i++ {
		// Boot with the RTC behind, pak opened and closed before NTP.
		e.closePak()
		real = real.Add(time.Minute)
		e.clk.wall = behind
		e.launch()
		e.clk.run(time.Minute)
		e.closePak()
		// NTP has set the clock, pak opened again.
		real = real.Add(time.Minute)
		e.clk.wall = real
		e.launch()
		e.clk.run(time.Minute)
	}
	if got := e.clock.Now(); got > time.Hour {
		t.Fatalf("credited %v for a few minutes of real time", got)
	}
	e.purge()
	if e.count() != 1 {
		t.Fatal("entry purged by a clock going back and forth")
	}
	// Real offline time past the latest wall time seen still counts.
	e.closePak()
	e.clk.off(3 * day)
	e.launch()
	if got := e.clock.Now(); got < 3*day || got > 3*day+time.Hour {
		t.Fatalf("real outage credited %v, want about 3 days", got)
	}
}
