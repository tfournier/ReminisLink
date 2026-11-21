package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Codex review of M18, round 1: damaged entries, links, journals, free
// names, no-replace renames and the "replace" rule.

func entryJSON(t *testing.T, tr *Trash, id string) entryFile {
	t.Helper()
	e, err := readEntry(filepath.Join(tr.Dir(), id))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func saveJSON(t *testing.T, tr *Trash, e entryFile) {
	t.Helper()
	if err := writeEntry(filepath.Join(tr.Dir(), e.ID), e); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreRefusesAnEntryWithUnlistedFiles(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	e := deleteGame(t, tr, sd)
	ef := entryJSON(t, tr, e.ID)
	var moves []string
	for _, m := range ef.Moves {
		if !strings.HasSuffix(m, ".rtc") {
			moves = append(moves, m)
		}
	}
	ef.Moves = moves
	saveJSON(t, tr, ef)
	if _, err := tr.PlanRestore(e.ID); !errors.Is(err, ErrEntryDamaged) {
		t.Fatalf("plan: %v", err)
	}
	if _, err := tr.Restore(e.ID, RestoreFail, nil); !errors.Is(err, ErrEntryDamaged) {
		t.Fatalf("restore: %v", err)
	}
	rtc := filepath.Join(tr.Dir(), e.ID, "Saves/GBA/Game.gba.rtc")
	if read(t, rtc) != "clock" || exists(filepath.Join(sd, "Roms/GBA (GBA)/Game.gba")) {
		t.Fatal("entry changed")
	}
}

func TestRecoverNeverRemovesAnEntryWithFilesLeft(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	e := deleteGame(t, tr, sd)
	ef := entryJSON(t, tr, e.ID)
	// A tampered journal: the clock file is neither a move nor a return.
	ef.State, ef.ReturnMode = stateReturning, RestoreFail
	ef.Returns = nil
	var moves []string
	for _, m := range ef.Moves {
		if strings.HasSuffix(m, ".rtc") {
			continue
		}
		moves = append(moves, m)
		ef.Returns = append(ef.Returns, returnRecord{Move: m, Target: m})
	}
	ef.Moves = moves
	saveJSON(t, tr, ef)
	// One item already went back before the power cut.
	if err := os.Rename(filepath.Join(tr.Dir(), e.ID, "Roms/GBA (GBA)/Game.gba"), filepath.Join(sd, "Roms/GBA (GBA)/Game.gba")); err != nil {
		t.Fatal(err)
	}
	if err := tr.Recover(); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(sd, "Saves/GBA/Game.gba.sav")) != "save" {
		t.Fatal("restore not finished")
	}
	entries, _, _ := tr.List()
	if len(entries) != 1 || !reflect.DeepEqual(entries[0].Files, []string{"Saves/GBA/Game.gba.rtc"}) {
		t.Fatalf("left %+v", entries)
	}
	if read(t, filepath.Join(tr.Dir(), e.ID, "Saves/GBA/Game.gba.rtc")) != "clock" {
		t.Fatal("unlisted file lost")
	}
}

func TestRecoverKeepsAnEntryWhoseFilesCannotBeRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	tr, sd, _, _ := newTestTrash(t)
	e := deleteGame(t, tr, sd)
	ef := entryJSON(t, tr, e.ID)
	ef.State, ef.ReturnMode = stateReturning, RestoreFail
	for _, m := range ef.Moves {
		ef.Returns = append(ef.Returns, returnRecord{Move: m, Target: m})
	}
	saveJSON(t, tr, ef)
	saves := filepath.Join(tr.Dir(), e.ID, "Saves")
	if err := os.Chmod(saves, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(saves, 0o755)
	if err := tr.Recover(); err != nil {
		t.Fatal(err)
	}
	os.Chmod(saves, 0o755)
	// Unknown fate: nothing moved, nothing removed, still "returning".
	if got := entryJSON(t, tr, e.ID); got.State != stateReturning {
		t.Fatalf("state %s", got.State)
	}
	if read(t, filepath.Join(saves, "GBA/Game.gba.sav")) != "save" || exists(filepath.Join(sd, "Roms/GBA (GBA)/Game.gba")) {
		t.Fatal("entry changed")
	}
}

func TestRestoreRefusesLinks(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	outside := t.TempDir()
	write(t, outside, "secret/name.txt", "secret")
	victim := write(t, outside, "victim.txt", "keep me")

	// A folder of the entry replaced by a link to outside the card.
	e := deleteGame(t, tr, sd)
	cheats := filepath.Join(tr.Dir(), e.ID, "Cheats")
	if err := os.RemoveAll(cheats); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), cheats); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.PlanRestore(e.ID); !errors.Is(err, ErrEntryDamaged) {
		t.Fatalf("plan through a link: %v", err)
	}

	// entry.json itself a link: not an entry.
	e2 := deleteGame(t, tr, sd)
	dir2 := filepath.Join(tr.Dir(), e2.ID)
	data, _ := os.ReadFile(filepath.Join(dir2, trashEntryFile))
	os.WriteFile(filepath.Join(outside, "entry.json"), data, 0o644)
	os.Remove(filepath.Join(dir2, trashEntryFile))
	if err := os.Symlink(filepath.Join(outside, "entry.json"), filepath.Join(dir2, trashEntryFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.PlanRestore(e2.ID); !errors.Is(err, ErrTrashNotFound) {
		t.Fatalf("plan of a linked journal: %v", err)
	}

	// entry.json.tmp a link to a file outside: the journal is never
	// written through it.
	tr3, sd3, _, _ := newTestTrash(t)
	e3 := deleteGame(t, tr3, sd3)
	if err := os.Symlink(victim, filepath.Join(tr3.Dir(), e3.ID, trashEntryFile+".tmp")); err != nil {
		t.Fatal(err)
	}
	if _, err := tr3.Restore(e3.ID, RestoreFail, nil); err == nil {
		t.Fatal("restore wrote its journal through a link")
	}
	if read(t, victim) != "keep me" {
		t.Fatal("file outside the card overwritten")
	}
	if exists(filepath.Join(sd3, "Roms/GBA (GBA)/Game.gba")) {
		t.Fatal("restored without a journal")
	}
}

func TestRecoverChecksTheJournaledPlaces(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	write(t, sd, "Roms/GBA (GBA)/Folder/data.bin", "x")
	res, err := tr.Move(ReasonDeleteGame, "Folder", []MoveItem{{Path: filepath.Join(sd, "Roms/GBA (GBA)/Folder")}, {Path: write(t, sd, "Saves/GBA/Folder.sav", "s")}})
	if err != nil {
		t.Fatal(err)
	}
	ef := entryJSON(t, tr, res.Entry.ID)
	ef.State, ef.ReturnMode = stateReturning, RestoreFail
	ef.Returns = []returnRecord{
		{Move: "Saves/GBA/Folder.sav", Target: "Saves/GBA/Folder.sav"},
		// Tampered: a folder without launch.sh sent to the tools.
		{Move: "Roms/GBA (GBA)/Folder", Target: "Tools/tg5040/Evil.pak"},
	}
	saveJSON(t, tr, ef)
	// The save went back before the cut.
	os.Rename(filepath.Join(tr.Dir(), res.Entry.ID, "Saves/GBA/Folder.sav"), filepath.Join(sd, "Saves/GBA/Folder.sav"))
	if err := tr.Recover(); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(sd, "Tools")) {
		t.Fatal("journaled place outside the rule used")
	}
	if entries, _, _ := tr.List(); len(entries) != 1 || entries[0].Files[0] != "Roms/GBA (GBA)/Folder/data.bin" {
		t.Fatalf("left %+v", entries)
	}

	// The checks of the POST run at the start too.
	tr2, sd2, _, _ := newTestTrash(t)
	write(t, sd2, "Tools/tg5040/Clock.pak/readme.txt", "no launch.sh")
	res2, err := tr2.Move(ReasonDeletePak, "Clock", []MoveItem{{Path: filepath.Join(sd2, "Tools/tg5040/Clock.pak")}})
	if err != nil {
		t.Fatal(err)
	}
	refused := errors.New("refused")
	tr2.SetRestoreHooks(func(it RestoreItem) error {
		if it.Dir {
			return refused
		}
		return nil
	}, nil)
	if _, err := tr2.Restore(res2.Entry.ID, RestoreFail, nil); !errors.Is(err, refused) {
		t.Fatalf("restore: %v", err)
	}
	ef2 := entryJSON(t, tr2, res2.Entry.ID)
	ef2.State, ef2.ReturnMode = stateReturning, RestoreFail
	ef2.Internal = ef2.Moves // a journal cannot skip the checks
	ef2.Returns = []returnRecord{{Move: ef2.Moves[0], Target: ef2.Moves[0]}}
	ef2.Moves = append(ef2.Moves, "Saves/x.sav")
	ef2.Returns = append(ef2.Returns, returnRecord{Move: "Saves/x.sav", Target: "Saves/x.sav"})
	saveJSON(t, tr2, ef2)
	if err := tr2.Recover(); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(sd2, "Tools/tg5040/Clock.pak")) {
		t.Fatal("recovery skipped the checks")
	}
}

func TestRestoreFreeNamesFollowTheGame(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	items := []MoveItem{
		{Path: write(t, sd, "Roms/GBA (GBA)/Game.gba", "rom")},
		{Path: write(t, sd, "Saves/GBA/GAME.gba.sav", "save")},
		{Path: filepath.Dir(write(t, sd, ".userdata/shared/ReminisLink/locks/GBA/Game.gba/slot0.st", "lock")), Internal: true},
		{Path: filepath.Dir(filepath.Dir(write(t, sd, ".userdata/shared/ReminisLink/backups/GBA/Game.gba/resume/1.st", "r"))), Internal: true},
	}
	res, err := tr.Move(ReasonDeleteGame, "Game", items)
	if err != nil {
		t.Fatal(err)
	}
	write(t, sd, "Roms/GBA (GBA)/game.gba", "new")
	r, err := tr.Restore(res.Entry.ID, RestoreRename, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"Roms/GBA (GBA)/Game (2).gba", "Saves/GBA/GAME (2).gba.sav",
		".userdata/shared/ReminisLink/locks/GBA/Game (2).gba/slot0.st", ".userdata/shared/ReminisLink/backups/GBA/Game (2).gba/resume/1.st"} {
		if !exists(filepath.Join(sd, filepath.FromSlash(rel))) {
			t.Errorf("%s missing (restored %v)", rel, r.Restored)
		}
	}

	// A folder game: its playlist follows the folder.
	write(t, sd, "Roms/PS (PS)/Multi/Multi.m3u", "Multi (Disc 1).bin\n")
	write(t, sd, "Roms/PS (PS)/Multi/Multi (Disc 1).bin", "disc")
	res, err = tr.Move(ReasonDeleteGame, "Multi", []MoveItem{
		{Path: filepath.Join(sd, "Roms/PS (PS)/Multi")}, {Path: write(t, sd, "Saves/PS/Multi.m3u.sav", "s")},
	})
	if err != nil {
		t.Fatal(err)
	}
	write(t, sd, "Roms/PS (PS)/MULTI/x", "other")
	p, err := tr.PlanRestore(res.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	var free []string
	for _, f := range p.Files {
		free = append(free, *f.FreeName)
	}
	sort.Strings(free)
	want := []string{"Roms/PS (PS)/Multi (2)/Multi (2).m3u", "Roms/PS (PS)/Multi (2)/Multi (Disc 1).bin", "Saves/PS/Multi (2).m3u.sav"}
	if !reflect.DeepEqual(free, want) {
		t.Fatalf("free names %v", free)
	}
	r, err = tr.Restore(res.Entry.ID, RestoreRename, nil)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(r.Restored)
	if !reflect.DeepEqual(r.Restored, want) {
		t.Fatalf("restored %v", r.Restored)
	}
	if read(t, filepath.Join(sd, "Roms/PS (PS)/Multi (2)/Multi (2).m3u")) != "Multi (Disc 1).bin\n" {
		t.Fatal("playlist not renamed")
	}
}

func TestRestoreSaysWhenNoFreeNameExists(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	res, err := tr.Move(ReasonOverwrite, "Game", []MoveItem{{Path: write(t, sd, "Saves/GBA/Game.gba.sav", "old")}})
	if err != nil {
		t.Fatal(err)
	}
	// The folder of the save is now a file.
	os.RemoveAll(filepath.Join(sd, "Saves/GBA"))
	write(t, sd, "Saves/GBA", "a file")
	p, err := tr.PlanRestore(res.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Conflict || p.NoFreeName == nil || *p.NoFreeName != NoFreeNamePathBlocked || p.Files[0].FreeName != nil {
		t.Fatalf("plan %+v", p)
	}
	var ce *RestoreConflictError
	if _, err := tr.Restore(res.Entry.ID, RestoreRename, nil); !errors.As(err, &ce) || ce.NoFreeName != NoFreeNamePathBlocked {
		t.Fatalf("restore: %v", err)
	}
}

func TestAJournalWriteErrorNeverRestoresLater(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	e := deleteGame(t, tr, sd)
	// The journal reaches the card but its sync fails, and so does the
	// write of "done" that follows.
	tr.saveEntry = func(dir string, ef entryFile) error {
		if ef.State == stateReturning {
			_ = writeEntry(dir, ef)
		}
		return errors.New("sync failed")
	}
	if _, err := tr.Restore(e.ID, RestoreFail, nil); err == nil {
		t.Fatal("restore did not fail")
	}
	if got := entryJSON(t, tr, e.ID); got.State != stateReturning {
		t.Fatalf("state %s", got.State)
	}
	tr.saveEntry = func(dir string, ef entryFile) error { return writeEntry(dir, ef) }
	if err := tr.Recover(); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(sd, "Roms/GBA (GBA)/Game.gba")) {
		t.Fatal("a failed POST restored at the next start")
	}
	if entries, _, _ := tr.List(); len(entries) != 1 || len(entries[0].Files) != 6 {
		t.Fatalf("entry %+v", entries)
	}
}

func TestRenameNoReplace(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	a := write(t, sd, "a/file", "a")
	b := write(t, sd, "b/file", "b")
	if err := tr.renameNoReplaceInSD(a, b); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("file over file: %v", err)
	}
	if read(t, b) != "b" || read(t, a) != "a" {
		t.Fatal("file replaced")
	}
	write(t, sd, "d1/x", "1")
	write(t, sd, "d2/y", "2")
	if err := tr.renameNoReplaceInSD(filepath.Join(sd, "d1"), filepath.Join(sd, "d2")); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("folder over folder: %v", err)
	}
	if err := tr.renameNoReplaceInSD(a, filepath.Join(sd, "b/new")); err != nil || read(t, filepath.Join(sd, "b/new")) != "a" || exists(a) {
		t.Fatalf("free file: %v", err)
	}
	if err := tr.renameNoReplaceInSD(filepath.Join(sd, "d1"), filepath.Join(sd, "d3")); err != nil || read(t, filepath.Join(sd, "d3/x")) != "1" {
		t.Fatalf("free folder: %v", err)
	}
}

// replaceHook is the hook of internal/transfer for a save of Game.gba.
func replaceHook(sd string) func(p RestorePlan) (*ReplaceSpec, error) {
	return func(p RestorePlan) (*ReplaceSpec, error) {
		spec := &ReplaceSpec{Targets: map[string]string{}, Label: "Game"}
		for _, it := range p.Items {
			if it.Dir || !strings.HasPrefix(it.Rel, "Saves/GBA/") {
				return nil, &NotReplaceableError{Reason: "not-a-save"}
			}
			ext := filepath.Ext(it.Rel)
			spec.Targets[it.Rel] = "Saves/GBA/GAME.gba" + ext
		}
		for _, n := range []string{"GAME.gba.sav", "GAME.gba.rtc"} {
			if p := filepath.Join(sd, "Saves/GBA", n); exists(p) {
				spec.Displace = append(spec.Displace, p)
			}
		}
		return spec, nil
	}
}

func TestRestoreReplacesTheCurrentSave(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	res, err := tr.Move(ReasonOverwrite, "Game", []MoveItem{
		{Path: write(t, sd, "Saves/GBA/Game.gba.sav", "old save")},
		{Path: write(t, sd, "Saves/GBA/Game.gba.rtc", "old clock")},
	})
	if err != nil {
		t.Fatal(err)
	}
	write(t, sd, "Saves/GBA/GAME.gba.sav", "current save")
	write(t, sd, "Saves/GBA/GAME.gba.rtc", "current clock")
	if _, err := tr.Restore(res.Entry.ID, RestoreReplace, nil); !errors.Is(err, ErrNotReplaceable) {
		t.Fatalf("without a hook: %v", err)
	}
	tr.SetRestoreHooks(nil, replaceHook(sd))
	p, err := tr.PlanRestore(res.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(p.ReplaceDisplaced)
	if !p.Replaceable || !reflect.DeepEqual(p.ReplaceDisplaced, []string{"Saves/GBA/GAME.gba.rtc", "Saves/GBA/GAME.gba.sav"}) {
		t.Fatalf("plan %+v", p)
	}
	for _, f := range p.Files {
		if f.ReplaceName == nil || !strings.HasPrefix(*f.ReplaceName, "Saves/GBA/GAME.gba.") {
			t.Fatalf("file %+v", f)
		}
	}
	r, err := tr.Restore(res.Entry.ID, RestoreReplace, nil)
	if err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(sd, "Saves/GBA/GAME.gba.sav")) != "old save" || read(t, filepath.Join(sd, "Saves/GBA/GAME.gba.rtc")) != "old clock" {
		t.Fatal("save not replaced")
	}
	if r.ReplacedEntryID == nil || len(r.Replaced) != 2 {
		t.Fatalf("result %+v", r)
	}
	entries, _, _ := tr.List()
	if len(entries) != 1 || entries[0].ID != *r.ReplacedEntryID || entries[0].Reason != ReasonRestore {
		t.Fatalf("trash %+v", entries)
	}
	dir := filepath.Join(tr.Dir(), *r.ReplacedEntryID)
	if read(t, filepath.Join(dir, "Saves/GBA/GAME.gba.sav")) != "current save" || entryJSON(t, tr, *r.ReplacedEntryID).RestoreOf != "" {
		t.Fatal("current save not in the trash")
	}
}

func TestAFailedReplacePutsTheCurrentSaveBack(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	res, err := tr.Move(ReasonOverwrite, "Game", []MoveItem{{Path: write(t, sd, "Saves/GBA/Game.gba.sav", "old save")}})
	if err != nil {
		t.Fatal(err)
	}
	write(t, sd, "Saves/GBA/GAME.gba.sav", "current save")
	tr.SetRestoreHooks(nil, replaceHook(sd))
	tr.renameExcl = func(string, string) error { return errors.New("card removed") }
	if _, err := tr.Restore(res.Entry.ID, RestoreReplace, nil); err == nil {
		t.Fatal("no error")
	}
	if read(t, filepath.Join(sd, "Saves/GBA/GAME.gba.sav")) != "current save" {
		t.Fatal("current save not back")
	}
	if entries, _, _ := tr.List(); len(entries) != 1 || entries[0].ID != res.Entry.ID {
		t.Fatalf("trash %+v", entries)
	}

	// A power cut right after the current save moved away (the restore
	// journal never written): the next start puts it back.
	tr.renameExcl = tr.renameNoReplaceInSD
	_, _, _, err = tr.moveLocked(ReasonRestore, "Game", []MoveItem{{Path: filepath.Join(sd, "Saves/GBA/GAME.gba.sav")}}, nil,
		func(e *entryFile) { e.RestoreOf = res.Entry.ID })
	if err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(sd, "Saves/GBA/GAME.gba.sav")) {
		t.Fatal("not displaced")
	}
	if err := tr.Recover(); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(sd, "Saves/GBA/GAME.gba.sav")) != "current save" {
		t.Fatal("displaced save not put back at the start")
	}
	if entries, _, _ := tr.List(); len(entries) != 1 {
		t.Fatalf("trash %+v", entries)
	}
}

func TestReplaceIsRefusedForOtherEntries(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	e := deleteGame(t, tr, sd)
	tr.SetRestoreHooks(nil, replaceHook(sd))
	if _, err := tr.Restore(e.ID, RestoreReplace, nil); !errors.Is(err, ErrNotReplaceable) {
		t.Fatalf("replace of a game: %v", err)
	}
	p, err := tr.PlanRestore(e.ID)
	if err != nil || p.Replaceable {
		t.Fatalf("plan %+v %v", p.Replaceable, err)
	}
	if exists(filepath.Join(sd, "Roms/GBA (GBA)/Game.gba")) {
		t.Fatal("restored")
	}
}
