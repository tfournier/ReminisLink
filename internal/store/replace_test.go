package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sha(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func TestTrashReplace(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	rom := write(t, sd, "Roms/GBA (GBA)/Game.gba", "old")
	tmp := write(t, sd, ".userdata/shared/ReminisLink/uploads/up_1.part", "new rom")
	res, err := tr.Replace(ReasonOverwrite, "Game.gba", []MoveItem{{Path: rom}},
		[]Install{{Src: tmp, Dst: rom, Size: 7, SHA256: sha("new rom")}})
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(rom); string(data) != "new rom" || exists(tmp) {
		t.Fatalf("not installed: %q", data)
	}
	entries, _, _ := tr.List()
	if len(entries) != 1 || entries[0].ID != res.Entry.ID || entries[0].Files[0] != "Roms/GBA (GBA)/Game.gba" {
		t.Fatalf("entries %+v", entries)
	}
	if data, _ := os.ReadFile(filepath.Join(tr.Dir(), res.Entry.ID, "Roms", "GBA (GBA)", "Game.gba")); string(data) != "old" {
		t.Fatalf("old file in the trash: %q", data)
	}
	// Nothing to replace: a plain rename, no entry.
	tmp2 := write(t, sd, ".userdata/shared/ReminisLink/uploads/up_2.part", "x")
	dst := filepath.Join(sd, "Roms/GBA (GBA)/.media/Game.png")
	if _, err := tr.Replace(ReasonOverwrite, "Game", nil, []Install{{Src: tmp2, Dst: dst, Size: 1}}); err != nil || !exists(dst) {
		t.Fatalf("install without items: %v", err)
	}
	if entries, _, _ := tr.List(); len(entries) != 1 {
		t.Fatalf("entry made for nothing: %+v", entries)
	}
	if _, err := tr.Replace(ReasonOverwrite, "x", nil, []Install{{Src: "/elsewhere/a", Dst: dst}}); err == nil {
		t.Fatal("source outside the SD card accepted")
	}
}

func TestTrashReplaceFailurePutsEverythingBack(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	state := write(t, sd, ".userdata/shared/GBA-gpsp/Game.gba.st0", "old state")
	disc := write(t, sd, ".userdata/shared/.minui/GBA/Game.gba.0.txt", "old disc")
	tmpState := write(t, sd, ".userdata/shared/ReminisLink/uploads/a.st", "new state")
	tmpDisc := write(t, sd, ".userdata/shared/ReminisLink/uploads/a.st.txt", "new disc")
	calls := 0
	tr.rename = func(oldpath, newpath string) error {
		calls++
		if calls == 4 { // 2 moves, the state installed, the disc fails
			return errors.New("simulated failure")
		}
		return os.Rename(oldpath, newpath)
	}
	_, err := tr.Replace(ReasonRestore, "Game", []MoveItem{{Path: state}, {Path: disc}}, []Install{
		{Src: tmpState, Dst: state, Size: 9}, {Src: tmpDisc, Dst: disc, Size: 8},
	})
	if err == nil || errors.Is(err, ErrReplaceFailed) {
		t.Fatalf("error = %v", err)
	}
	for p, want := range map[string]string{state: "old state", disc: "old disc", tmpState: "new state", tmpDisc: "new disc"} {
		if data, _ := os.ReadFile(p); string(data) != want {
			t.Errorf("%s = %q, want %q", p, data, want)
		}
	}
	if names, _ := tr.entryDirs(); len(names) != 0 {
		t.Fatalf("entry left: %v", names)
	}
}

// interrupted makes an entry as a power cut leaves it during Replace:
// the old files moved, state "installing".
func interrupted(t *testing.T, tr *Trash, sd string, old map[string]string, installs []installRecord) string {
	t.Helper()
	id := "tr_20261118T170000Z_00aa"
	dir := filepath.Join(tr.Dir(), id)
	credit := int64(0)
	e := entryFile{Version: 1, State: stateInstalling, ID: id, Reason: ReasonOverwrite, Label: "Game",
		TrashedAt: time.Date(2026, 11, 18, 17, 0, 0, 0, time.UTC), Credit: &credit, Installs: installs}
	for rel, content := range old {
		e.Moves = append(e.Moves, rel)
		e.Files = append(e.Files, rel)
		write(t, dir, rel, content)
		os.Remove(filepath.Join(sd, filepath.FromSlash(rel)))
	}
	if err := writeEntry(dir, e); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestTrashRecoverFinishesAnInterruptedReplacement(t *testing.T) {
	tr, sd, clk, _ := newTestTrash(t)
	write(t, sd, "Roms/GBA (GBA)/Game.gba", "old")
	write(t, sd, ".userdata/shared/ReminisLink/uploads/up_1.part", "new rom")
	id := interrupted(t, tr, sd, map[string]string{"Roms/GBA (GBA)/Game.gba": "old"}, []installRecord{
		{Src: ".userdata/shared/ReminisLink/uploads/up_1.part", Dst: "Roms/GBA (GBA)/Game.gba", Size: 7, SHA256: sha("new rom")},
	})
	// Until the next start, the entry is protected: not listed, not
	// deleted, emptied nor purged, and its temporary file is kept.
	if entries, _, _ := tr.List(); len(entries) != 0 {
		t.Fatalf("listed: %+v", entries)
	}
	if _, err := tr.Delete(id); !errors.Is(err, ErrTrashNotFound) {
		t.Fatalf("delete: %v", err)
	}
	if _, err := tr.Empty(); err != nil {
		t.Fatal(err)
	}
	cc := newCreditForTest(t, 60*24*time.Hour)
	tr.UseCreditClock(cc)
	clk.t = clk.t.Add(60 * 24 * time.Hour)
	if _, err := tr.Purge(TrashRetention); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(tr.Dir(), id, "Roms", "GBA (GBA)", "Game.gba")) {
		t.Fatal("the only copy of the replaced ROM was erased")
	}
	if src := tr.PendingSources(); !src[filepath.Join(sd, ".userdata/shared/ReminisLink/uploads/up_1.part")] {
		t.Fatalf("pending sources %v", src)
	}

	if err := tr.Recover(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(sd, "Roms/GBA (GBA)/Game.gba")); string(data) != "new rom" {
		t.Fatalf("installation not finished: %q", data)
	}
	entries, _, _ := tr.List()
	if len(entries) != 1 || entries[0].ID != id {
		t.Fatalf("entry not done: %+v", entries)
	}
	if len(tr.PendingSources()) != 0 {
		t.Fatal("still pending")
	}
}

func TestTrashRecoverRollsBackAnIncompleteReplacement(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	// The state was installed, the disc file is incomplete (wrong size).
	write(t, sd, ".userdata/shared/ReminisLink/uploads/a.st.txt", "new")
	id := interrupted(t, tr, sd, map[string]string{
		".userdata/shared/GBA-gpsp/Game.gba.st0":     "old state",
		".userdata/shared/.minui/GBA/Game.gba.0.txt": "old disc",
	}, []installRecord{
		{Src: ".userdata/shared/ReminisLink/uploads/a.st", Dst: ".userdata/shared/GBA-gpsp/Game.gba.st0", Size: 9},
		{Src: ".userdata/shared/ReminisLink/uploads/a.st.txt", Dst: ".userdata/shared/.minui/GBA/Game.gba.0.txt", Size: 8},
	})
	write(t, sd, ".userdata/shared/GBA-gpsp/Game.gba.st0", "new state")
	if err := tr.Recover(); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]string{
		".userdata/shared/GBA-gpsp/Game.gba.st0":     "old state",
		".userdata/shared/.minui/GBA/Game.gba.0.txt": "old disc",
		".userdata/shared/ReminisLink/uploads/a.st":  "new state",
	} {
		if data, _ := os.ReadFile(filepath.Join(sd, filepath.FromSlash(rel))); string(data) != want {
			t.Errorf("%s = %q, want %q", rel, data, want)
		}
	}
	if exists(filepath.Join(tr.Dir(), id)) {
		t.Fatal("rolled back entry kept")
	}
}

func TestTrashMoveFinalWriteFailurePutsBack(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	rom := write(t, sd, "Roms/GBA (GBA)/Game.gba", "rom")
	writes := 0
	tr.saveEntry = func(dir string, e entryFile) error {
		writes++
		if writes == 2 { // the final "done"
			return errors.New("simulated write failure")
		}
		return writeEntry(dir, e)
	}
	if _, err := tr.Move(ReasonReplaceROM, "Game", []MoveItem{{Path: rom}}); err == nil {
		t.Fatal("expected an error")
	}
	if data, _ := os.ReadFile(rom); string(data) != "rom" {
		t.Fatal("the ROM was not put back at once")
	}
	if names, _ := tr.entryDirs(); len(names) != 0 {
		t.Fatalf("entry left: %v", names)
	}
}

func TestTrashMoveChecksTheSyncOfItsFolders(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	rom := write(t, sd, "Roms/GBA (GBA)/Game.gba", "rom")
	var synced []string
	tr.syncDir = func(dir string) error {
		synced = append(synced, dir)
		if filepath.Base(dir) == "GBA (GBA)" && filepath.Base(filepath.Dir(dir)) == "Roms" &&
			filepath.Base(filepath.Dir(filepath.Dir(dir))) != filepath.Base(sd) {
			return errors.New("simulated I/O error") // the new folder in the entry
		}
		return nil
	}
	if _, err := tr.Move(ReasonReplaceROM, "Game", []MoveItem{{Path: rom}}); err == nil {
		t.Fatal("a failed sync of the entry folders was ignored")
	}
	if data, _ := os.ReadFile(rom); string(data) != "rom" {
		t.Fatal("the ROM left its place although its copy was not durable")
	}
	// Success: every folder from the trash down to the copy is synced
	// before the rename.
	tr.syncDir = func(dir string) error { synced = append(synced, dir); return nil }
	synced = nil
	res, err := tr.Move(ReasonReplaceROM, "Game", []MoveItem{{Path: rom}})
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(tr.Dir(), res.Entry.ID)
	for _, want := range []string{tr.Dir(), entry, filepath.Join(entry, "Roms"), filepath.Join(entry, "Roms", "GBA (GBA)")} {
		found := false
		for _, d := range synced {
			if d == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s not synced (%v)", want, synced)
		}
	}
}

// newCreditForTest returns a credit clock frozen at credit.
func newCreditForTest(t *testing.T, credit time.Duration) *CreditClock {
	t.Helper()
	return &CreditClock{base: credit, mono: func() time.Duration { return 0 }}
}

// replaceFixture prepares a locked-state restore: the current state and
// its disc file, and their new versions in temporary files.
func replaceFixture(t *testing.T, tr *Trash, sd string) (state, disc, tmpState, tmpDisc string, items []MoveItem, installs []Install) {
	t.Helper()
	state = write(t, sd, ".userdata/shared/GBA-gpsp/Game.gba.st0", "old state")
	disc = write(t, sd, ".userdata/shared/.minui/GBA/Game.gba.0.txt", "old disc")
	tmpState = write(t, sd, ".userdata/shared/ReminisLink/uploads/a.st", "new state")
	tmpDisc = write(t, sd, ".userdata/shared/ReminisLink/uploads/a.st.txt", "new disc")
	items = []MoveItem{{Path: state}, {Path: disc}}
	installs = []Install{
		{Src: tmpState, Dst: state, Size: 9, SHA256: sha("new state")},
		{Src: tmpDisc, Dst: disc, Size: 8, SHA256: sha("new disc")},
	}
	return
}

func entryStates(t *testing.T, tr *Trash) []string {
	t.Helper()
	names, _ := tr.entryDirs()
	var out []string
	for _, n := range names {
		e, err := readEntry(filepath.Join(tr.Dir(), n))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, e.State)
	}
	return out
}

// Codex review of M8: the sync of the folder fails after the rename of the
// last new file (the disc file). The rename is recorded: the new disc file
// goes back too, and when even that cannot be confirmed the journal stays
// and the next starts finish the replacement, the same way twice.
func TestReplaceSyncFailureAfterTheLastRename(t *testing.T) {
	t.Run("rolled back at once", func(t *testing.T) {
		tr, sd, _, _ := newTestTrash(t)
		state, disc, tmpState, tmpDisc, items, installs := replaceFixture(t, tr, sd)
		failed := false
		tr.syncDir = func(dir string) error {
			if filepath.Base(dir) == "GBA" && filepath.Base(filepath.Dir(dir)) == ".minui" && exists(disc) && !failed {
				if data, _ := os.ReadFile(disc); string(data) == "new disc" {
					failed = true
					return errors.New("simulated sync failure")
				}
			}
			return nil
		}
		_, err := tr.Replace(ReasonRestore, "Game", items, installs)
		if err == nil || errors.Is(err, ErrReplaceFailed) || !failed {
			t.Fatalf("error %v (failure injected: %v)", err, failed)
		}
		for p, want := range map[string]string{state: "old state", disc: "old disc", tmpState: "new state", tmpDisc: "new disc"} {
			if data, _ := os.ReadFile(p); string(data) != want {
				t.Errorf("%s = %q, want %q", filepath.Base(p), data, want)
			}
		}
		if names, _ := tr.entryDirs(); len(names) != 0 {
			t.Fatalf("entry left %v", names)
		}
	})
	t.Run("journal kept, finished at the next starts", func(t *testing.T) {
		tr, sd, _, _ := newTestTrash(t)
		state, disc, _, tmpDisc, items, installs := replaceFixture(t, tr, sd)
		// From the rename of the new disc file on, the folder of the disc
		// files cannot be synced any more.
		minui := filepath.Dir(disc)
		armed := false
		tr.syncDir = func(dir string) error {
			if !exists(tmpDisc) {
				armed = true
			}
			if dir == minui && armed {
				return errors.New("simulated sync failure")
			}
			return nil
		}
		if _, err := tr.Replace(ReasonRestore, "Game", items, installs); !errors.Is(err, ErrReplaceFailed) {
			t.Fatalf("error %v", err)
		}
		if st := entryStates(t, tr); len(st) != 1 || st[0] != stateInstalling {
			t.Fatalf("journal %v", st)
		}
		// The temporary files the journal names are still there.
		if src := tr.PendingSources(); !src[tmpDisc] || !exists(tmpDisc) {
			t.Fatalf("pending sources %v", src)
		}
		tr.syncDir = func(string) error { return nil }
		for i := 0; i < 2; i++ {
			if err := tr.Recover(); err != nil {
				t.Fatal(err)
			}
			if data, _ := os.ReadFile(state); string(data) != "new state" {
				t.Fatalf("start %d: state %q", i, data)
			}
			if data, _ := os.ReadFile(disc); string(data) != "new disc" {
				t.Fatalf("start %d: disc %q", i, data)
			}
			entries, _, _ := tr.List()
			if len(entries) != 1 {
				t.Fatalf("start %d: entries %+v", i, entries)
			}
			old, _ := os.ReadFile(filepath.Join(tr.Dir(), entries[0].ID, ".userdata/shared/GBA-gpsp/Game.gba.st0"))
			oldDisc, _ := os.ReadFile(filepath.Join(tr.Dir(), entries[0].ID, ".userdata/shared/.minui/GBA/Game.gba.0.txt"))
			if string(old) != "old state" || string(oldDisc) != "old disc" {
				t.Fatalf("start %d: originals %q %q", i, old, oldDisc)
			}
			if st := entryStates(t, tr); st[0] != stateDone {
				t.Fatalf("start %d: journal %v", i, st)
			}
		}
	})
}

// Codex review of M8: a roll back whose move back cannot be confirmed
// (sync failure) keeps the journal; the next start ends it.
func TestReplaceRollbackKeepsTheJournalUntilDurable(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	rom := write(t, sd, "Roms/GBA (GBA)/Game.gba", "old rom")
	tmp := write(t, sd, ".userdata/shared/ReminisLink/uploads/pt_1.part", "patched")
	tr.rename = func(oldpath, newpath string) error {
		if oldpath == tmp {
			return errors.New("simulated rename failure")
		}
		return os.Rename(oldpath, newpath)
	}
	romDir := filepath.Dir(rom)
	moved := false
	tr.syncDir = func(dir string) error {
		if dir == romDir && moved {
			return errors.New("simulated sync failure")
		}
		if dir == romDir && !exists(rom) {
			moved = true // the ROM went to the trash: syncing its folder now fails
		}
		return nil
	}
	if _, err := tr.Replace(ReasonReplaceROM, "Game", []MoveItem{{Path: rom}}, []Install{{Src: tmp, Dst: rom, Size: 7}}); !errors.Is(err, ErrReplaceFailed) {
		t.Fatalf("error %v", err)
	}
	if st := entryStates(t, tr); len(st) != 1 || st[0] != stateRestoring {
		t.Fatalf("journal %v", st)
	}
	if _, err := tr.Empty(); err != nil {
		t.Fatal(err)
	}
	if st := entryStates(t, tr); len(st) != 1 {
		t.Fatal("the journal of an unconfirmed roll back was erased")
	}
	tr.rename, tr.syncDir = os.Rename, func(string) error { return nil }
	for i := 0; i < 2; i++ {
		if err := tr.Recover(); err != nil {
			t.Fatal(err)
		}
		if data, _ := os.ReadFile(rom); string(data) != "old rom" {
			t.Fatalf("start %d: rom %q", i, data)
		}
		if names, _ := tr.entryDirs(); len(names) != 0 {
			t.Fatalf("start %d: entry left %v", i, names)
		}
		if data, _ := os.ReadFile(tmp); string(data) != "patched" {
			t.Fatalf("start %d: temporary file %q", i, data)
		}
	}
}

// Codex review of M8 (counter-review): a ROM moved back from the trash
// whose folder sync failed is synced again by the next start before the
// journal goes; and the move to "restoring" must be durable before any
// original goes back.
func TestRollbackRetriesTheSyncOfAFileAlreadyBack(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	rom := write(t, sd, "Roms/GBA (GBA)/Game.gba", "old rom")
	tmp := write(t, sd, ".userdata/shared/ReminisLink/uploads/pt_1.part", "patched")
	romDir := filepath.Dir(rom)
	tr.rename = func(oldpath, newpath string) error {
		if oldpath == tmp {
			return errors.New("simulated rename failure")
		}
		return os.Rename(oldpath, newpath)
	}
	failRomDir := func(dir string) error {
		if dir == romDir && exists(rom) {
			if data, _ := os.ReadFile(rom); string(data) == "old rom" {
				return errors.New("simulated sync failure") // the ROM came back
			}
		}
		return nil
	}
	tr.syncDir = failRomDir
	if _, err := tr.Replace(ReasonReplaceROM, "Game", []MoveItem{{Path: rom}}, []Install{{Src: tmp, Dst: rom, Size: 7}}); !errors.Is(err, ErrReplaceFailed) {
		t.Fatalf("error %v", err)
	}
	if !exists(rom) {
		t.Fatal("the ROM did not come back")
	}
	// Next start: the sync still fails, the journal stays.
	tr.rename = os.Rename
	synced := 0
	tr.syncDir = func(dir string) error {
		if dir == romDir {
			synced++
		}
		return failRomDir(dir)
	}
	if err := tr.Recover(); err != nil {
		t.Fatal(err)
	}
	if st := entryStates(t, tr); len(st) != 1 || st[0] != stateRestoring || synced == 0 {
		t.Fatalf("journal %v, ROM folder synced %d times", st, synced)
	}
	// Then it works: the journal goes.
	tr.syncDir = func(string) error { return nil }
	if err := tr.Recover(); err != nil {
		t.Fatal(err)
	}
	if names, _ := tr.entryDirs(); len(names) != 0 {
		t.Fatalf("entry left %v", names)
	}
	if data, _ := os.ReadFile(rom); string(data) != "old rom" {
		t.Fatalf("rom %q", data)
	}
}

func TestRestoringTransitionIsDurableBeforeTheRollback(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	rom := write(t, sd, "Roms/GBA (GBA)/Game.gba", "old rom")
	tmp := write(t, sd, ".userdata/shared/ReminisLink/uploads/pt_1.part", "patched")
	tr.rename = func(oldpath, newpath string) error {
		if oldpath == tmp {
			return errors.New("simulated rename failure")
		}
		return os.Rename(oldpath, newpath)
	}
	tr.syncDir = func(dir string) error {
		if data, err := os.ReadFile(filepath.Join(dir, trashEntryFile)); err == nil && strings.Contains(string(data), `"restoring"`) {
			return errors.New("simulated sync failure of the journal")
		}
		return nil
	}
	if _, err := tr.Replace(ReasonReplaceROM, "Game", []MoveItem{{Path: rom}}, []Install{{Src: tmp, Dst: rom, Size: 7}}); !errors.Is(err, ErrReplaceFailed) {
		t.Fatalf("error %v", err)
	}
	if exists(rom) {
		t.Fatal("the ROM went back before the roll back was recorded durably")
	}
	tr.rename, tr.syncDir = os.Rename, func(string) error { return nil }
	for i := 0; i < 2; i++ {
		if err := tr.Recover(); err != nil {
			t.Fatal(err)
		}
		if data, _ := os.ReadFile(rom); string(data) != "old rom" {
			t.Fatalf("start %d: rom %q", i, data)
		}
	}
	if names, _ := tr.entryDirs(); len(names) != 0 {
		t.Fatalf("entry left %v", names)
	}
}
