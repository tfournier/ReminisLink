package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func lastSeen(t *testing.T, path string) string {
	t.Helper()
	var c map[string]any
	data, _ := os.ReadFile(path)
	json.Unmarshal(data, &c)
	s, _ := c["lastSeenTime"].(string)
	return s
}

func TestConfigFileKeepsOtherFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"version":1,"deviceId":"x","lastSeenTime":"2026-11-18T17:00:00Z","future":true}`), 0o644)
	cf := NewConfigFile(path)
	if err := cf.Save(Config{Version: 1, DeviceID: "x", DeviceName: "Brick", Port: 47821}); err != nil {
		t.Fatal(err)
	}
	var c map[string]any
	data, _ := os.ReadFile(path)
	json.Unmarshal(data, &c)
	if c["deviceName"] != "Brick" || c["future"] != true || c["lastSeenTime"] != "2026-11-18T17:00:00Z" {
		t.Fatalf("config.json %s", data)
	}
	os.WriteFile(path, []byte("{"), 0o644)
	if _, err := NewTimeGuard(cf, nil); err == nil {
		t.Fatal("broken config accepted")
	}
}

func TestTimeGuard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cf := NewConfigFile(path)
	right := time.Date(2026, 11, 18, 17, 0, 0, 0, time.UTC)
	clk := &fakeClock{t: right}
	g, err := NewTimeGuard(cf, clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	if now, ok := g.Now(); !ok || !now.Equal(right) {
		t.Fatal("first launch with a plausible clock")
	}
	g.Save()
	if lastSeen(t, path) != "2026-11-18T17:00:00Z" {
		t.Fatal("not saved")
	}
	// Set back by a day: dated with lastSeenTime, not trusted.
	clk.t = right.Add(-24 * time.Hour)
	g, _ = NewTimeGuard(cf, clk.Now)
	if now, ok := g.Now(); ok || !now.Equal(right) {
		t.Fatal("clock behind")
	}
	// A single jump of +10 years, then the right time: never recorded.
	clk.t = right.Add(10 * 365 * 24 * time.Hour)
	g, _ = NewTimeGuard(cf, clk.Now)
	if g.Trusted() {
		t.Fatal("jump trusted")
	}
	g.Save()
	if lastSeen(t, path) != "2026-11-18T17:00:00Z" {
		t.Fatalf("aberrant time recorded: %s", lastSeen(t, path))
	}
	clk.t = right.Add(2 * time.Hour)
	g, _ = NewTimeGuard(cf, clk.Now)
	if !g.Trusted() {
		t.Fatal("right time not trusted")
	}
	g.Save()
	if lastSeen(t, path) != "2026-11-18T19:00:00Z" {
		t.Fatalf("lastSeenTime %s", lastSeen(t, path))
	}
	// A real absence of two years: confirmed by a launch an hour later.
	clk.t = right.Add(2 * 365 * 24 * time.Hour)
	g, _ = NewTimeGuard(cf, clk.Now)
	if g.Trusted() {
		t.Fatal("jump trusted at once")
	}
	clk.t = clk.t.Add(2 * time.Hour)
	g, _ = NewTimeGuard(cf, clk.Now)
	if !g.Trusted() {
		t.Fatal("confirmed jump not trusted")
	}
	// A drained clock is never trusted, even on the first launch.
	g, _ = NewTimeGuard(NewConfigFile(filepath.Join(t.TempDir(), "c.json")), func() time.Time { return time.Unix(0, 0) })
	if g.Trusted() {
		t.Fatal("1970 trusted")
	}
}

func TestTrashWithTimeGuard(t *testing.T) {
	tr, sd, clk, _ := newTestTrash(t)
	cf := NewConfigFile(filepath.Join(t.TempDir(), "config.json"))
	launch := func() *TimeGuard {
		g, err := NewTimeGuard(cf, clk.Now)
		if err != nil {
			t.Fatal(err)
		}
		tr.UseTimeGuard(g)
		return g
	}
	right := clk.t

	// Drained clock (1970) on the first launch: the entry is marked and
	// survives any purge.
	clk.t = time.Unix(0, 0).UTC()
	g := launch()
	early, err := tr.Move(ReasonOverwrite, "early", []MoveItem{{Path: write(t, sd, "a", "1")}})
	if err != nil {
		t.Fatal(err)
	}
	if freed, _ := tr.Purge(TrashRetention); freed != 0 {
		t.Fatal("purged with a drained clock")
	}
	g.Save()
	// The clock is set right: the entry lives 30 days from now.
	clk.t = right
	launch()
	if freed, _ := tr.Purge(TrashRetention); freed != 0 {
		t.Fatal("purged right after the clock was set")
	}
	entries, _, _ := tr.List()
	if len(entries) != 1 || !entries[0].TrashedAt.Equal(right) || entries[0].ID != early.Entry.ID {
		t.Fatalf("not dated again: %+v", entries)
	}
	clk.t = right.Add(29 * 24 * time.Hour)
	launch()
	if freed, _ := tr.Purge(TrashRetention); freed != 0 {
		t.Fatal("purged after 29 days")
	}
	clk.t = right.Add(31 * 24 * time.Hour)
	g = launch()
	if freed, _ := tr.Purge(TrashRetention); freed != 1 {
		t.Fatal("not purged after 31 days")
	}
	g.Save()

	// Clock set back: entries dated with lastSeenTime, marked, no purge.
	clk.t = right
	launch()
	back, _ := tr.Move(ReasonOverwrite, "back", []MoveItem{{Path: write(t, sd, "b", "2")}})
	if !back.Entry.TrashedAt.Equal(right.Add(31 * 24 * time.Hour)) {
		t.Fatalf("dated %v", back.Entry.TrashedAt)
	}
	if freed, _ := tr.Purge(TrashRetention); freed != 0 {
		t.Fatal("purged with a clock behind")
	}

	// A +10 years jump: nothing purged, and the next right launch purges
	// normally (lastSeenTime unchanged).
	clk.t = right.Add(10 * 365 * 24 * time.Hour)
	launch()
	if freed, _ := tr.Purge(TrashRetention); freed != 0 {
		t.Fatal("purged with an aberrant clock")
	}
	clk.t = right.Add(32 * 24 * time.Hour)
	launch()
	if freed, _ := tr.Purge(TrashRetention); freed != 0 {
		t.Fatal("the entry made with a clock behind was dated again now: kept")
	}
	clk.t = right.Add(63 * 24 * time.Hour)
	launch()
	if freed, _ := tr.Purge(TrashRetention); freed != 1 { // one byte
		t.Fatalf("normal purge")
	}
}

func TestConfigSavesNeverTouchGuardKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"version":1,"deviceId":"7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10","lastSeenTime":"2026-11-18T17:00:00Z","candidateTime":"2030-01-01T00:00:00Z","future":{"a":1}}`), 0o644)
	// The initial writer fills the missing fields (merge, not rewrite).
	c, err := LoadConfig(path, defaultName, nil)
	if err != nil {
		t.Fatal(err)
	}
	cf := NewConfigFile(path)
	c.DeviceName = "Renamed"
	if err := cf.Save(c); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	data, _ := os.ReadFile(path)
	json.Unmarshal(data, &m)
	if m["deviceName"] != "Renamed" || m["port"] != float64(DefaultPort) || m["lastSeenTime"] != "2026-11-18T17:00:00Z" ||
		m["candidateTime"] != "2030-01-01T00:00:00Z" || m["future"] == nil {
		t.Fatalf("config.json %s", data)
	}
}
