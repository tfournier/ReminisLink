package store

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func read(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// deleteGame moves a game with its save, clock file, state, cheats, art
// and lock to the trash, like library.DeleteGame.
func deleteGame(t *testing.T, tr *Trash, sd string) TrashEntry {
	t.Helper()
	items := []MoveItem{
		{Path: write(t, sd, "Roms/GBA (GBA)/Game.gba", "rom")},
		{Path: write(t, sd, "Saves/GBA/Game.gba.sav", "save")},
		{Path: write(t, sd, "Saves/GBA/Game.gba.rtc", "clock")},
		{Path: write(t, sd, ".userdata/shared/GBA-gpsp/Game.gba.st0", "state")},
		{Path: write(t, sd, "Cheats/GBA/Game.gba.cht", "cheats")},
		{Path: write(t, sd, "Roms/GBA (GBA)/.media/Game.png", "art")},
		{Path: filepath.Dir(write(t, sd, ".userdata/shared/ReminisLink/locks/GBA/Game.gba/slot0.st", "lock")), Internal: true},
	}
	res, err := tr.Move(ReasonDeleteGame, "Game", items)
	if err != nil {
		t.Fatal(err)
	}
	return res.Entry
}

func TestRestoreDeletedGame(t *testing.T) {
	tr, sd, _, logs := newTestTrash(t)
	e := deleteGame(t, tr, sd)

	p, err := tr.PlanRestore(e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Conflict || len(p.Files) != 6 || p.Bytes != int64(len("romsaveclockstatecheatsart")) || p.Label != "Game" || p.Reason != ReasonDeleteGame {
		t.Fatalf("plan %+v", p)
	}
	for _, f := range p.Files {
		if f.Conflict || f.FreeName == nil || *f.FreeName != f.Name {
			t.Fatalf("file %+v", f)
		}
	}

	called := false
	res, err := tr.Restore(e.ID, RestoreFail, func(p RestorePlan) error { called = true; return nil })
	if err != nil || !called {
		t.Fatal(err, called)
	}
	if res.Renamed || len(res.Restored) != 6 || res.RestoredBytes != p.Bytes {
		t.Fatalf("result %+v", res)
	}
	for rel, want := range map[string]string{
		"Roms/GBA (GBA)/Game.gba": "rom", "Saves/GBA/Game.gba.sav": "save", "Saves/GBA/Game.gba.rtc": "clock",
		".userdata/shared/GBA-gpsp/Game.gba.st0": "state", "Cheats/GBA/Game.gba.cht": "cheats",
		"Roms/GBA (GBA)/.media/Game.png":                           "art",
		".userdata/shared/ReminisLink/locks/GBA/Game.gba/slot0.st": "lock",
	} {
		if got := read(t, filepath.Join(sd, filepath.FromSlash(rel))); got != want {
			t.Errorf("%s: %q", rel, got)
		}
	}
	if exists(filepath.Join(tr.Dir(), e.ID)) {
		t.Fatal("entry still in the trash")
	}
	if entries, _, _ := tr.List(); len(entries) != 0 {
		t.Fatalf("listed %+v", entries)
	}
	if !strings.Contains(logs.String(), "restored from the trash") {
		t.Fatalf("log: %s", logs.String())
	}
	if _, err := tr.Restore(e.ID, RestoreFail, nil); !errors.Is(err, ErrTrashNotFound) {
		t.Fatalf("second restore: %v", err)
	}
}

func TestRestoreConflictFailAndRename(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	e := deleteGame(t, tr, sd)
	// A new game of the same name (other case: exFAT ignores it) and a lock.
	write(t, sd, "Roms/GBA (GBA)/game.GBA", "new rom")
	write(t, sd, ".userdata/shared/ReminisLink/locks/GBA/Game.gba/slot1.st", "new lock")

	p, err := tr.PlanRestore(e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Conflict {
		t.Fatal("no conflict")
	}
	got := map[string]RestoreFile{}
	for _, f := range p.Files {
		got[f.Name] = f
	}
	if f := got["Roms/GBA (GBA)/Game.gba"]; !f.Conflict || f.FreeName == nil || *f.FreeName != "Roms/GBA (GBA)/Game (2).gba" {
		t.Fatalf("rom %+v", f)
	}
	if f := got["Saves/GBA/Game.gba.sav"]; f.Conflict || f.FreeName == nil || *f.FreeName != "Saves/GBA/Game (2).gba.sav" {
		t.Fatalf("save %+v", f)
	}
	if f := got["Roms/GBA (GBA)/.media/Game.png"]; f.FreeName == nil || *f.FreeName != "Roms/GBA (GBA)/.media/Game (2).png" {
		t.Fatalf("art %+v", f)
	}

	_, err = tr.Restore(e.ID, RestoreFail, nil)
	var ce *RestoreConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("restore: %v", err)
	}
	sort.Strings(ce.Conflicts)
	if want := []string{"ReminisLink/locks/GBA/Game.gba", "Roms/GBA (GBA)/Game.gba"}; !reflect.DeepEqual(ce.Conflicts, want) {
		t.Fatalf("conflicts %v", ce.Conflicts)
	}
	if entries, _, _ := tr.List(); len(entries) != 1 {
		t.Fatal("entry gone after a conflict")
	}
	if got := read(t, filepath.Join(sd, "Roms/GBA (GBA)/game.GBA")); got != "new rom" {
		t.Fatal("present file changed")
	}

	res, err := tr.Restore(e.ID, RestoreRename, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Renamed {
		t.Fatal("not renamed")
	}
	for rel, want := range map[string]string{
		"Roms/GBA (GBA)/game.GBA": "new rom", "Roms/GBA (GBA)/Game (2).gba": "rom",
		"Saves/GBA/Game (2).gba.sav": "save", "Saves/GBA/Game (2).gba.rtc": "clock",
		".userdata/shared/GBA-gpsp/Game (2).gba.st0": "state", "Cheats/GBA/Game (2).gba.cht": "cheats",
		"Roms/GBA (GBA)/.media/Game (2).png":                           "art",
		".userdata/shared/ReminisLink/locks/GBA/Game.gba/slot1.st":     "new lock",
		".userdata/shared/ReminisLink/locks/GBA/Game (2).gba/slot0.st": "lock",
	} {
		if got := read(t, filepath.Join(sd, filepath.FromSlash(rel))); got != want {
			t.Errorf("%s: %q", rel, got)
		}
	}
	if !contains(res.Restored, "Roms/GBA (GBA)/Game (2).gba") {
		t.Fatalf("restored %v", res.Restored)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestRestoreFreeNameSkipsTakenNumbers(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	sav := write(t, sd, "Saves/GBA/Super Bros. 3.gba.sav", "old")
	res, err := tr.Move(ReasonOverwrite, "Super Bros. 3", []MoveItem{{Path: sav}})
	if err != nil {
		t.Fatal(err)
	}
	write(t, sd, "Saves/GBA/Super Bros. 3.gba.sav", "new")
	write(t, sd, "Saves/GBA/super bros. 3 (2).GBA.sav", "taken")
	r, err := tr.Restore(res.Entry.ID, RestoreRename, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Saves/GBA/Super Bros. 3 (3).gba.sav"}; !reflect.DeepEqual(r.Restored, want) {
		t.Fatalf("restored %v", r.Restored)
	}
	if read(t, filepath.Join(sd, "Saves/GBA/Super Bros. 3.gba.sav")) != "new" {
		t.Fatal("present save changed")
	}
}

func TestRestoreFolderAndParentCase(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	pak := filepath.Join(sd, "Tools", "tg5040", "Clock.pak")
	write(t, sd, "Tools/tg5040/Clock.pak/launch.sh", "#!/bin/sh")
	res, err := tr.Move(ReasonDeletePak, "Clock", []MoveItem{{Path: pak}})
	if err != nil {
		t.Fatal(err)
	}
	// The parent folder was renamed meanwhile (exFAT: same folder).
	if err := os.Rename(filepath.Join(sd, "Tools"), filepath.Join(sd, "tools")); err != nil {
		t.Fatal(err)
	}
	var seen RestorePlan
	r, err := tr.Restore(res.Entry.ID, RestoreFail, func(p RestorePlan) error { seen = p; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(seen.Items) != 1 || !seen.Items[0].Dir || seen.Items[0].Target != "tools/tg5040/Clock.pak" {
		t.Fatalf("plan %+v", seen.Items)
	}
	if !reflect.DeepEqual(r.Restored, []string{"tools/tg5040/Clock.pak/launch.sh"}) {
		t.Fatalf("restored %v", r.Restored)
	}
	if read(t, filepath.Join(sd, "tools/tg5040/Clock.pak/launch.sh")) != "#!/bin/sh" {
		t.Fatal("pak not back")
	}

	// A folder of the same name ignoring case: "Clock (2).pak".
	res, err = tr.Move(ReasonDeletePak, "Clock", []MoveItem{{Path: filepath.Join(sd, "tools/tg5040/Clock.pak")}})
	if err != nil {
		t.Fatal(err)
	}
	write(t, sd, "tools/tg5040/CLOCK.pak/launch.sh", "other")
	r, err = tr.Restore(res.Entry.ID, RestoreRename, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Restored, []string{"tools/tg5040/Clock (2).pak/launch.sh"}) {
		t.Fatalf("restored %v", r.Restored)
	}
}

func TestRestoreCheckRefusesAndChangesNothing(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	e := deleteGame(t, tr, sd)
	refused := errors.New("refused")
	if _, err := tr.Restore(e.ID, RestoreFail, func(RestorePlan) error { return refused }); !errors.Is(err, refused) {
		t.Fatalf("restore: %v", err)
	}
	if exists(filepath.Join(sd, "Roms/GBA (GBA)/Game.gba")) {
		t.Fatal("restored despite the check")
	}
	if entries, _, _ := tr.List(); len(entries) != 1 {
		t.Fatal("entry changed")
	}
}

func TestRestoreFailureRollsBack(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	e := deleteGame(t, tr, sd)
	calls := 0
	tr.SetRename(func(o, n string) error {
		calls++
		if calls == 4 {
			return errors.New("card removed")
		}
		return tr.renameInSD(o, n)
	})
	if _, err := tr.Restore(e.ID, RestoreFail, nil); err == nil || !strings.Contains(err.Error(), "card removed") {
		t.Fatalf("restore: %v", err)
	}
	tr.SetRename(tr.renameInSD)
	for _, rel := range []string{"Roms/GBA (GBA)/Game.gba", "Saves/GBA/Game.gba.sav", "Saves/GBA/Game.gba.rtc"} {
		if exists(filepath.Join(sd, filepath.FromSlash(rel))) {
			t.Errorf("%s left in place after the roll back", rel)
		}
	}
	entries, _, _ := tr.List()
	if len(entries) != 1 || len(entries[0].Files) != 6 {
		t.Fatalf("entry after the roll back %+v", entries)
	}
	if _, err := tr.Restore(e.ID, RestoreFail, nil); err != nil {
		t.Fatalf("second try: %v", err)
	}
}

func TestRecoverFinishesAnInterruptedRestore(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	e := deleteGame(t, tr, sd)
	// The card fails after the journal and the first two renames, and
	// the roll back fails too: the state a power cut leaves.
	calls := 0
	tr.SetRename(func(o, n string) error {
		calls++
		if calls > 2 {
			return errors.New("card removed")
		}
		return tr.renameInSD(o, n)
	})
	if _, err := tr.Restore(e.ID, RestoreFail, nil); err == nil {
		t.Fatal("restore did not fail")
	}
	tr.SetRename(tr.renameInSD)
	if entries, _, _ := tr.List(); len(entries) != 0 {
		t.Fatalf("a returning entry is listed: %+v", entries)
	}
	if _, err := tr.Delete(e.ID); !errors.Is(err, ErrTrashNotFound) {
		t.Fatalf("delete of a returning entry: %v", err)
	}
	// Something took the place of the cheats meanwhile: they stay.
	write(t, sd, "Cheats/GBA/GAME.gba.cht", "other")
	if err := tr.Recover(); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(sd, "Saves/GBA/Game.gba.rtc")) != "clock" || read(t, filepath.Join(sd, "Roms/GBA (GBA)/.media/Game.png")) != "art" {
		t.Fatal("restore not finished")
	}
	entries, _, _ := tr.List()
	if len(entries) != 1 || !reflect.DeepEqual(entries[0].Files, []string{"Cheats/GBA/Game.gba.cht"}) {
		t.Fatalf("left %+v", entries)
	}
	if read(t, filepath.Join(sd, "Cheats/GBA/GAME.gba.cht")) != "other" {
		t.Fatal("present file overwritten")
	}
}

func TestPurgeNeverTouchesAReturningEntry(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	credit, err := NewCreditClock(NewConfigFile(filepath.Join(t.TempDir(), "config.json")), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	tr.UseCreditClock(credit)
	e := deleteGame(t, tr, sd)
	dir := filepath.Join(tr.Dir(), e.ID)
	ef, err := readEntry(dir)
	if err != nil {
		t.Fatal(err)
	}
	ef.State = stateReturning
	if err := writeEntry(dir, ef); err != nil {
		t.Fatal(err)
	}
	if freed, err := tr.Purge(0); err != nil || freed != 0 || !exists(dir) {
		t.Fatalf("purge: %d %v", freed, err)
	}
	if _, err := tr.PlanRestore(e.ID); !errors.Is(err, ErrTrashNotFound) {
		t.Fatalf("plan of a returning entry: %v", err)
	}
}

func TestRestoreRefusesBadIDs(t *testing.T) {
	tr, _, _, _ := newTestTrash(t)
	if _, err := tr.PlanRestore("../x"); !errors.Is(err, ErrInvalidTrashID) {
		t.Fatal(err)
	}
	if _, err := tr.Restore("tr_20261118T170000Z_ffff", RestoreFail, nil); !errors.Is(err, ErrTrashNotFound) {
		t.Fatal(err)
	}
	if _, err := tr.Restore("tr_20261118T170000Z_ffff", "overwrite", nil); err == nil {
		t.Fatal("unknown rule accepted")
	}
}

func TestWithNumber(t *testing.T) {
	stems := []string{"Game", "Game.gba"}
	for base, want := range map[string]string{
		"Game.gba": "Game (2).gba", "Game.gba.sav": "Game (2).gba.sav", "Game.png": "Game (2).png",
		"Other.txt": "Other (2).txt", "noext": "noext (2)", ".hidden": ".hidden (2)",
	} {
		if got := withNumber(base, stems, 2); got != want {
			t.Errorf("%s: %s", base, got)
		}
	}
}
