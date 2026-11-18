package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTimeGuard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"version":1,"deviceId":"x","future":true}`), 0o644)
	clk := &fakeClock{t: time.Date(2026, 11, 18, 17, 0, 0, 0, time.UTC)}
	g, err := NewTimeGuard(path, clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	if !g.Trusted() || !g.Now().Equal(clk.t) {
		t.Fatal("first run")
	}
	if err := g.Save(); err != nil {
		t.Fatal(err)
	}
	var c map[string]any
	data, _ := os.ReadFile(path)
	json.Unmarshal(data, &c)
	if c["lastSeenTime"] != "2026-11-18T17:00:00Z" || c["future"] != true || c["deviceId"] != "x" {
		t.Fatalf("config.json %s", data)
	}
	// Restart with the clock set back by a day.
	clk.t = clk.t.Add(-24 * time.Hour)
	g, _ = NewTimeGuard(path, clk.Now)
	if g.Trusted() || !g.Now().Equal(time.Date(2026, 11, 18, 17, 0, 0, 0, time.UTC)) {
		t.Fatal("clock behind is trusted")
	}
	// Then right again.
	clk.t = clk.t.Add(48 * time.Hour)
	if !g.Trusted() || !g.Now().Equal(clk.t) {
		t.Fatal("clock right again")
	}
	// An aberrant jump ahead is not trusted for the purge.
	clk.t = clk.t.Add(10 * 365 * 24 * time.Hour)
	if g.Trusted() {
		t.Fatal("jump ahead trusted")
	}
	if _, err := NewTimeGuard(filepath.Join(t.TempDir(), "missing.json"), nil); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte("{"), 0o644)
	if _, err := NewTimeGuard(path, nil); err == nil {
		t.Fatal("broken config accepted")
	}
}

func TestTrashWithTimeGuard(t *testing.T) {
	tr, sd, clk, _ := newTestTrash(t)
	cfg := filepath.Join(t.TempDir(), "config.json")
	g, _ := NewTimeGuard(cfg, clk.Now)
	tr.UseTimeGuard(g)
	old, err := tr.Move(ReasonOverwrite, "old", []MoveItem{{Path: write(t, sd, "a", "1")}})
	if err != nil {
		t.Fatal(err)
	}
	g.Save()
	// The clock goes back 40 days at the next start: entries are dated
	// with the latest observed time, and the purge is skipped.
	clk.t = clk.t.Add(-40 * 24 * time.Hour)
	g, _ = NewTimeGuard(cfg, clk.Now)
	tr.UseTimeGuard(g)
	res, err := tr.Move(ReasonOverwrite, "b", []MoveItem{{Path: write(t, sd, "b", "2")}})
	if err != nil || !res.Entry.TrashedAt.Equal(old.Entry.TrashedAt) {
		t.Fatalf("dated %v, want %v", res.Entry.TrashedAt, old.Entry.TrashedAt)
	}
	if freed, _ := tr.Purge(TrashRetention); freed != 0 {
		t.Fatal("purged with a clock behind")
	}
	// Right clock, 31 days later: both entries go.
	clk.t = old.Entry.TrashedAt.Add(31 * 24 * time.Hour)
	if freed, _ := tr.Purge(TrashRetention); freed != 2 {
		t.Fatalf("freed %d", freed)
	}
	// An aberrant clock 10 years ahead: nothing is purged.
	tr.Move(ReasonOverwrite, "c", []MoveItem{{Path: write(t, sd, "c", "3")}})
	g.Save()
	g, _ = NewTimeGuard(cfg, clk.Now)
	tr.UseTimeGuard(g)
	clk.t = clk.t.Add(10 * 365 * 24 * time.Hour)
	if freed, _ := tr.Purge(TrashRetention); freed != 0 {
		t.Fatal("purged with an aberrant clock")
	}
}
