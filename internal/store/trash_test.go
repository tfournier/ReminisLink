package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

func write(t *testing.T, root, rel, content string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func newTestTrash(t *testing.T) (*Trash, string, *fakeClock, *bytes.Buffer) {
	t.Helper()
	sd := t.TempDir()
	clk := &fakeClock{t: time.Date(2026, 11, 18, 17, 0, 0, 0, time.UTC)}
	var logs bytes.Buffer
	tr := NewTrash(Layout{Root: filepath.Join(sd, ".userdata", "shared", "ReminisLink")}, sd, clk.Now, slog.New(slog.NewTextHandler(&logs, nil)))
	return tr, sd, clk, &logs
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestTrashMoveListDelete(t *testing.T) {
	tr, sd, clk, logs := newTestTrash(t)
	rom := write(t, sd, "Roms/GBA (GBA)/Game.gba", "rom-bytes")
	sav := write(t, sd, "Saves/GBA/Game.gba.sav", "save")
	disc := write(t, sd, "Roms/PS (PS)/Multi/Multi.m3u", "Multi (Disc 1).bin\n")
	write(t, sd, "Roms/PS (PS)/Multi/Multi (Disc 1).bin", "disc1")
	lock := write(t, sd, ".userdata/shared/ReminisLink/locks/GBA/Game.gba/slot0.st", "lock")

	res, err := tr.Move(ReasonDeleteGame, "Game", []MoveItem{
		{Path: rom}, {Path: sav}, {Path: filepath.Dir(disc)}, {Path: disc}, // nested: moves with its folder
		{Path: filepath.Dir(lock), Internal: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if exists(rom) || exists(sav) || exists(disc) || exists(lock) {
		t.Fatal("files still in place")
	}
	if !ValidTrashID(res.Entry.ID) || !strings.HasPrefix(res.Entry.ID, "tr_20261118T170000Z_") {
		t.Fatalf("id %q", res.Entry.ID)
	}
	if len(res.Moved) != 4 || res.MovedBytes != int64(len("rom-bytes")+len("save")+len("Multi (Disc 1).bin\n")+len("disc1")) {
		t.Fatalf("moved %v %d", res.Moved, res.MovedBytes)
	}
	if res.Entry.SizeBytes != res.MovedBytes+int64(len("lock")) {
		t.Fatalf("entry size %d", res.Entry.SizeBytes)
	}
	// The original path is kept under the entry.
	kept := filepath.Join(tr.Dir(), res.Entry.ID, "Roms", "GBA (GBA)", "Game.gba")
	if data, _ := os.ReadFile(kept); string(data) != "rom-bytes" {
		t.Fatalf("trash copy %q", data)
	}
	if !strings.Contains(logs.String(), "moved to the trash") || !strings.Contains(logs.String(), tr.Dir()) {
		t.Fatalf("log: %s", logs.String())
	}

	clk.t = clk.t.Add(time.Hour)
	other := write(t, sd, "Roms/GBA (GBA)/Other.gba", "x")
	res2, err := tr.Move(ReasonOverwrite, "Other.gba", []MoveItem{{Path: other}})
	if err != nil {
		t.Fatal(err)
	}
	entries, total, err := tr.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].ID != res2.Entry.ID || total != res.Entry.SizeBytes+1 {
		t.Fatalf("list %+v total %d", entries, total)
	}
	if !entries[1].ExpiresAt.Equal(entries[1].TrashedAt.Add(30 * 24 * time.Hour)) {
		t.Fatal("expiresAt")
	}
	for _, f := range entries[1].Files {
		if strings.Contains(f, "locks") {
			t.Fatalf("internal copy listed: %v", entries[1].Files)
		}
	}

	if _, err := tr.Delete("../x"); !errors.Is(err, ErrInvalidTrashID) {
		t.Fatalf("bad id: %v", err)
	}
	if _, err := tr.Delete("tr_20000101T000000Z_0000"); !errors.Is(err, ErrTrashNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	freed, err := tr.Delete(res.Entry.ID)
	if err != nil || freed != res.Entry.SizeBytes {
		t.Fatalf("delete: %d %v", freed, err)
	}
	if exists(filepath.Join(tr.Dir(), res.Entry.ID)) {
		t.Fatal("entry still there")
	}
	freed, err = tr.Empty()
	if err != nil || freed != 1 {
		t.Fatalf("empty: %d %v", freed, err)
	}
	if entries, total, _ := tr.List(); len(entries) != 0 || total != 0 {
		t.Fatal("not empty")
	}
}

func TestTrashMoveRefuses(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	outside := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(outside, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Move(ReasonOverwrite, "x", []MoveItem{{Path: outside}}); err == nil {
		t.Error("file outside the SD card accepted")
	}
	if _, err := tr.Move(ReasonOverwrite, "x", []MoveItem{{Path: filepath.Join(sd, "missing")}}); err == nil {
		t.Error("missing file accepted")
	}
	if _, err := tr.Move("nope", "x", nil); err == nil {
		t.Error("unknown reason accepted")
	}
	if _, err := tr.Move(ReasonOverwrite, "x", nil); !errors.Is(err, ErrNothingToMove) {
		t.Errorf("empty: %v", err)
	}
	if err := os.MkdirAll(tr.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Move(ReasonOverwrite, "x", []MoveItem{{Path: tr.Dir()}}); err == nil {
		t.Error("trash moved into itself")
	}
}

func TestTrashMoveIsAllOrNothing(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	a := write(t, sd, "Roms/A (A)/a.gba", "a")
	b := write(t, sd, "Roms/A (A)/b.gba", "b")
	calls := 0
	tr.rename = func(oldpath, newpath string) error {
		calls++
		if calls == 2 {
			return errors.New("simulated failure")
		}
		return os.Rename(oldpath, newpath)
	}
	if _, err := tr.Move(ReasonDeleteGame, "a", []MoveItem{{Path: a}, {Path: b}}); err == nil {
		t.Fatal("expected an error")
	}
	if !exists(a) || !exists(b) {
		t.Fatal("a file was not put back")
	}
	if names, _ := tr.entryDirs(); len(names) != 0 {
		t.Fatalf("entry left: %v", names)
	}
}

func TestTrashRecoverRollsBackAnInterruptedMove(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	a := write(t, sd, "Roms/A (A)/a.gba", "a")
	b := write(t, sd, "Saves/A/a.gba.sav", "b")
	taken := write(t, sd, "Cheats/A/a.gba.cht", "new")
	// Power loss after two renames: entry.json says "moving".
	id := "tr_20261118T170000Z_0001"
	dir := filepath.Join(tr.Dir(), id)
	e := entryFile{Version: 1, State: stateMoving, ID: id, Reason: ReasonDeleteGame, Label: "a",
		TrashedAt: time.Date(2026, 11, 18, 17, 0, 0, 0, time.UTC),
		Moves:     []string{"Roms/A (A)/a.gba", "Saves/A/a.gba.sav", "Cheats/A/a.gba.cht", "Roms/A (A)/never.gba"}}
	if err := writeEntry(dir, e); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{a, b} {
		rel, _ := filepath.Rel(sd, p)
		dst := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(dst), 0o755)
		if err := os.Rename(p, dst); err != nil {
			t.Fatal(err)
		}
	}
	// The third file was moved, and its place taken since.
	write(t, dir, "Cheats/A/a.gba.cht", "old")
	// Listed? No: still moving.
	if entries, _, _ := tr.List(); len(entries) != 0 {
		t.Fatalf("moving entry listed: %+v", entries)
	}
	// An empty entry folder (crash before entry.json) and a half deletion.
	empty := filepath.Join(tr.Dir(), "tr_20261118T170001Z_0002")
	_ = os.MkdirAll(empty, 0o755)
	half := write(t, tr.Dir(), ".deleting-tr_20261101T000000Z_0003/Roms/x", "x")

	if err := tr.Recover(); err != nil {
		t.Fatal(err)
	}
	if !exists(a) || !exists(b) {
		t.Fatal("files not put back")
	}
	if data, _ := os.ReadFile(taken); string(data) != "new" {
		t.Fatal("a file in place was replaced")
	}
	if exists(empty) || exists(filepath.Dir(filepath.Dir(half))) {
		t.Fatal("leftovers not cleaned")
	}
	entries, _, err := tr.List()
	if err != nil || len(entries) != 1 || len(entries[0].Files) != 1 || entries[0].Files[0] != "Cheats/A/a.gba.cht" {
		t.Fatalf("kept entry: %+v %v", entries, err)
	}
}

func TestTrashPurge(t *testing.T) {
	tr, sd, clk, _ := newTestTrash(t)
	old, err := tr.Move(ReasonOverwrite, "old", []MoveItem{{Path: write(t, sd, "a", "12")}})
	if err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(10 * 24 * time.Hour)
	recent, err := tr.Move(ReasonOverwrite, "recent", []MoveItem{{Path: write(t, sd, "b", "3")}})
	if err != nil {
		t.Fatal(err)
	}
	// An entry with a broken entry.json is never purged on its id date,
	// nor one written by a future version.
	broken := write(t, tr.Dir(), "tr_20261001T000000Z_ffff/x", "456")
	if err := os.WriteFile(filepath.Join(filepath.Dir(broken), trashEntryFile), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := write(t, tr.Dir(), "tr_20261001T000000Z_eeee/y", "7")
	os.WriteFile(filepath.Join(filepath.Dir(future), trashEntryFile), []byte(`{"version":2,"trashedAt":"2026-10-01T00:00:00Z"}`), 0o644)
	clk.t = clk.t.Add(25 * 24 * time.Hour) // old is 35 days old, recent 25
	freed, err := tr.Purge(TrashRetention)
	if err != nil {
		t.Fatal(err)
	}
	if freed != 2 { // old only
		t.Fatalf("freed %d", freed)
	}
	if !exists(filepath.Dir(broken)) || !exists(filepath.Dir(future)) {
		t.Fatal("unreadable entry purged")
	}
	if exists(filepath.Join(tr.Dir(), old.Entry.ID)) || !exists(filepath.Join(tr.Dir(), recent.Entry.ID)) {
		t.Fatal("wrong entries purged")
	}
}

func TestTrashEntryJSON(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	res, err := tr.Move(ReasonOverwrite, "x", []MoveItem{{Path: write(t, sd, "a b/c.gba", "1")}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(tr.Dir(), res.Entry.ID, trashEntryFile))
	if err != nil {
		t.Fatal(err)
	}
	var e map[string]any
	if err := json.Unmarshal(data, &e); err != nil || e["state"] != "done" || e["reason"] != "overwrite" {
		t.Fatalf("entry.json %s", data)
	}
}

func TestTrashFailedMoveRecountsWhatIsLeft(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	a := write(t, sd, "Roms/A (A)/a.gba", "aaaa")
	b := write(t, sd, "Saves/A/a.gba.sav", "bb")
	c := write(t, sd, "Cheats/A/a.gba.cht", "c")
	calls := 0
	tr.rename = func(oldpath, newpath string) error {
		calls++
		switch calls {
		case 3:
			// Third move fails; meanwhile a new file took a's place, so a
			// cannot go back.
			write(t, sd, "Roms/A (A)/a.gba", "new")
			return errors.New("simulated failure")
		}
		return os.Rename(oldpath, newpath)
	}
	if _, err := tr.Move(ReasonDeleteGame, "a", []MoveItem{{Path: a}, {Path: b}, {Path: c}}); err == nil {
		t.Fatal("expected an error")
	}
	tr.rename = os.Rename
	if data, _ := os.ReadFile(b); string(data) != "bb" {
		t.Fatal("b not put back")
	}
	entries, total, err := tr.List()
	if err != nil || len(entries) != 1 || len(entries[0].Files) != 1 || entries[0].Files[0] != "Roms/A (A)/a.gba" ||
		entries[0].SizeBytes != 4 || total != 4 {
		t.Fatalf("entry %+v total %d %v", entries, total, err)
	}
}
