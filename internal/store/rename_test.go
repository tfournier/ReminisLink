package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// listNames returns the exact names of a folder, sorted.
func listNames(t *testing.T, dir string) string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range des {
		out = append(out, d.Name())
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func renamesLeft(t *testing.T, tr *Trash) int {
	des, err := os.ReadDir(tr.renames)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(des)
}

func TestRenameGroupFolderGame(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	write(t, sd, "Roms/PS (PS)/Old/Old.m3u", "Old (Disc 1).bin\n")
	write(t, sd, "Roms/PS (PS)/Old/Old (Disc 1).bin", "disc")
	write(t, sd, "Saves/PS/Old.m3u.sav", "save")
	ps := filepath.Join(sd, "Roms", "PS (PS)")
	err := tr.RenameGroup("Old", []RenameStep{
		{From: filepath.Join(ps, "Old"), To: filepath.Join(ps, "New")},
		{From: filepath.Join(ps, "New", "Old.m3u"), To: filepath.Join(ps, "New", "New.m3u"), Identity: lstat(t, filepath.Join(ps, "Old", "Old.m3u"))},
		{From: filepath.Join(sd, "Saves/PS/Old.m3u.sav"), To: filepath.Join(sd, "Saves/PS/New.m3u.sav")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := listNames(t, filepath.Join(ps, "New")); got != "New.m3u,Old (Disc 1).bin" {
		t.Fatalf("folder: %s", got)
	}
	if got := listNames(t, filepath.Join(sd, "Saves/PS")); got != "New.m3u.sav" {
		t.Fatalf("saves: %s", got)
	}
	if renamesLeft(t, tr) != 0 {
		t.Fatal("journal left")
	}
}

func TestRenameGroupConflictRollsBack(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	rom := write(t, sd, "Roms/GBA (GBA)/Old.gba", "rom")
	sav := write(t, sd, "Saves/GBA/Old.gba.sav", "save")
	taken := write(t, sd, "Saves/GBA/New.gba.sav", "other game")
	err := tr.RenameGroup("Old", []RenameStep{
		{From: rom, To: filepath.Join(filepath.Dir(rom), "New.gba")},
		{From: sav, To: taken},
	}, nil)
	var conflict *RenameConflictError
	if !errors.As(err, &conflict) || len(conflict.Conflicts) != 1 || conflict.Conflicts[0] != "Saves/GBA/New.gba.sav" {
		t.Fatalf("err %v", err)
	}
	if !exists(rom) || exists(filepath.Join(filepath.Dir(rom), "New.gba")) || !exists(sav) {
		t.Fatal("not rolled back")
	}
	if b, _ := os.ReadFile(taken); string(b) != "other game" {
		t.Fatal("taken file replaced")
	}
	if renamesLeft(t, tr) != 0 {
		t.Fatal("journal left")
	}
}

// TestRenameGroupCaseOnly renames to another case: on a case-insensitive
// folder (exFAT, APFS) through a temporary name, never refused as taken.
func TestRenameGroupCaseOnly(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	rom := write(t, sd, "Roms/GBA (GBA)/zelda.gba", "rom")
	err := tr.RenameGroup("zelda", []RenameStep{{From: rom, To: filepath.Join(filepath.Dir(rom), "Zelda.gba")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := listNames(t, filepath.Dir(rom)); got != "Zelda.gba" {
		t.Fatalf("names: %s", got)
	}
}

// TestRenameGroupRecoverAfterCut cuts the power between two renames (a
// panic in the third rename call, the journal stays): the next start puts
// every file back under its old name.
func TestRenameGroupRecoverAfterCut(t *testing.T) {
	for _, cut := range []int{1, 2, 3, 4} {
		tr, sd, clk, logs := newTestTrash(t)
		write(t, sd, "Roms/PS (PS)/old/old.m3u", "d.bin\n")
		write(t, sd, "Roms/PS (PS)/old/d.bin", "disc")
		write(t, sd, "Saves/PS/old.m3u.sav", "save")
		ps := filepath.Join(sd, "Roms", "PS (PS)")
		steps := []RenameStep{
			{From: filepath.Join(ps, "old"), To: filepath.Join(ps, "Old")}, // case only: two renames
			{From: filepath.Join(ps, "Old", "old.m3u"), To: filepath.Join(ps, "Old", "Old.m3u"), Identity: lstat(t, filepath.Join(ps, "old", "old.m3u"))},
			{From: filepath.Join(sd, "Saves/PS/old.m3u.sav"), To: filepath.Join(sd, "Saves/PS/Old.m3u.sav")},
		}
		calls := 0
		tr.renameExcl = func(a, b string) error {
			calls++
			if calls == cut {
				panic("power cut")
			}
			return tr.renameNoReplaceInSD(a, b)
		}
		cutDone := false
		func() {
			defer func() { cutDone = recover() != nil }()
			_ = tr.RenameGroup("old", steps, nil)
		}()
		if !cutDone {
			// A case-sensitive folder (ext4): the case-only steps take one
			// rename each, fewer calls than this cut.
			continue
		}
		if renamesLeft(t, tr) != 1 {
			t.Fatalf("cut %d: journal missing", cut)
		}
		// Next start.
		tr2 := NewTrash(Layout{Root: filepath.Join(sd, ".userdata", "shared", "ReminisLink")}, sd, clk.Now, nil)
		if err := tr2.Recover(); err != nil {
			t.Fatal(err)
		}
		if got := listNames(t, ps); got != "old" {
			t.Fatalf("cut %d: system folder %s (%s)", cut, got, logs)
		}
		if got := listNames(t, filepath.Join(ps, "old")); got != "d.bin,old.m3u" {
			t.Fatalf("cut %d: game folder %s", cut, got)
		}
		if got := listNames(t, filepath.Join(sd, "Saves/PS")); got != "old.m3u.sav" {
			t.Fatalf("cut %d: saves %s", cut, got)
		}
		if renamesLeft(t, tr2) != 0 {
			t.Fatalf("cut %d: journal left", cut)
		}
	}
}

// TestRenameGroupSyncFailure: the renames succeed but a folder cannot be
// synced (Codex review of M23): the group goes back and fails; when the
// roll back cannot be synced either, the journal stays and the next start
// puts the files back, keeping the journal until its folders are synced.
func TestRenameGroupSyncFailure(t *testing.T) {
	tr, sd, clk, _ := newTestTrash(t)
	rom := write(t, sd, "Roms/GBA (GBA)/Old.gba", "rom")
	sav := write(t, sd, "Saves/GBA/Old.gba.sav", "save")
	steps := []RenameStep{
		{From: rom, To: filepath.Join(filepath.Dir(rom), "New.gba")},
		{From: sav, To: filepath.Join(filepath.Dir(sav), "New.gba.sav")},
	}
	saves := filepath.Dir(sav)
	failSaves := func(dir string) error {
		if dir == saves {
			return errors.New("sync failed")
		}
		return nil
	}
	tr.SetSyncDir(failSaves)
	if err := tr.RenameGroup("Old", steps, nil); err == nil {
		t.Fatal("a rename that was not synced succeeded")
	}
	// The roll back of the save folder could not be synced: journal kept.
	if renamesLeft(t, tr) != 1 {
		t.Fatal("journal removed although the roll back is not durable")
	}
	if !exists(rom) || !exists(sav) {
		t.Fatal("not rolled back")
	}
	// Next start, the sync still failing: the journal stays again.
	tr2 := NewTrash(Layout{Root: filepath.Join(sd, ".userdata", "shared", "ReminisLink")}, sd, clk.Now, nil)
	tr2.SetSyncDir(failSaves)
	if err := tr2.Recover(); err != nil {
		t.Fatal(err)
	}
	if renamesLeft(t, tr2) != 1 {
		t.Fatal("journal removed by a recovery that was not synced")
	}
	// Then it works.
	tr3 := NewTrash(Layout{Root: filepath.Join(sd, ".userdata", "shared", "ReminisLink")}, sd, clk.Now, nil)
	if err := tr3.Recover(); err != nil {
		t.Fatal(err)
	}
	if renamesLeft(t, tr3) != 0 || !exists(rom) || !exists(sav) {
		t.Fatal("journal left after a synced recovery")
	}
	// A sync failure of a folder whose roll back syncs: no journal left.
	tr4 := NewTrash(Layout{Root: filepath.Join(sd, ".userdata", "shared", "ReminisLink")}, sd, clk.Now, nil)
	calls := 0
	tr4.SetSyncDir(func(dir string) error {
		if dir == saves {
			calls++
			if calls == 1 {
				return errors.New("sync failed once")
			}
		}
		return nil
	})
	if err := tr4.RenameGroup("Old", steps, nil); err == nil {
		t.Fatal("success after a failed sync")
	}
	if renamesLeft(t, tr4) != 0 || !exists(rom) || !exists(sav) {
		t.Fatal("not rolled back cleanly")
	}
}

func lstat(t *testing.T, p string) os.FileInfo {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

// Codex review of M23 (round 2): a power cut between the rename of the
// folder and of its playlist; the first recovery puts the folder back but
// cannot make it durable (journal kept), then the user makes a new game
// New/New.m3u: the second recovery never touches it (another inode), and
// never syncs a folder that no longer exists.
func TestRenameGroupRecoveryNeverTouchesAnotherEntry(t *testing.T) {
	tr, sd, clk, _ := newTestTrash(t)
	write(t, sd, "Roms/PS (PS)/Old/Old.m3u", "d.bin\n")
	write(t, sd, "Roms/PS (PS)/Old/d.bin", "disc")
	ps := filepath.Join(sd, "Roms", "PS (PS)")
	steps := []RenameStep{
		{From: filepath.Join(ps, "Old"), To: filepath.Join(ps, "New")},
		{From: filepath.Join(ps, "New", "Old.m3u"), To: filepath.Join(ps, "New", "New.m3u"), Identity: lstat(t, filepath.Join(ps, "Old", "Old.m3u"))},
	}
	calls := 0
	tr.renameExcl = func(a, b string) error {
		calls++
		if calls == 2 {
			panic("power cut")
		}
		return tr.renameNoReplaceInSD(a, b)
	}
	func() {
		defer func() { _ = recover() }()
		_ = tr.RenameGroup("Old", steps, nil)
	}()
	layout := Layout{Root: filepath.Join(sd, ".userdata", "shared", "ReminisLink")}
	// First start: the folder goes back, the sync of the system folder fails.
	var synced []string
	tr2 := NewTrash(layout, sd, clk.Now, nil)
	tr2.SetSyncDir(func(dir string) error {
		synced = append(synced, dir)
		if _, err := os.Lstat(dir); err != nil {
			t.Errorf("synced a folder that does not exist: %s", dir)
		}
		if dir == ps {
			return errors.New("sync failed")
		}
		return nil
	})
	if err := tr2.Recover(); err != nil {
		t.Fatal(err)
	}
	if renamesLeft(t, tr2) != 1 || listNames(t, ps) != "Old" {
		t.Fatalf("first recovery: %s, %d journals", listNames(t, ps), renamesLeft(t, tr2))
	}
	// The user makes a new game New/New.m3u.
	write(t, sd, "Roms/PS (PS)/New/New.m3u", "other.bin\n")
	tr3 := NewTrash(layout, sd, clk.Now, nil)
	tr3.SetSyncDir(func(dir string) error {
		if _, err := os.Lstat(dir); err != nil {
			t.Errorf("synced a folder that does not exist: %s", dir)
		}
		return nil
	})
	if err := tr3.Recover(); err != nil {
		t.Fatal(err)
	}
	if got := listNames(t, filepath.Join(ps, "New")); got != "New.m3u" {
		t.Fatalf("the new game was touched: %s", got)
	}
	if got := listNames(t, filepath.Join(ps, "Old")); got != "Old.m3u,d.bin" {
		t.Fatalf("old game: %s", got)
	}
	if renamesLeft(t, tr3) != 0 {
		t.Fatal("journal left")
	}
}

// Codex review of M23 (round 2): when the journal cannot be removed once
// every step is done, the rename is never a success: it is rolled back.
func TestRenameGroupJournalNotRemoved(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	rom := write(t, sd, "Roms/GBA (GBA)/Old.gba", "rom")
	sav := write(t, sd, "Saves/GBA/Old.gba.sav", "save")
	steps := []RenameStep{
		{From: rom, To: filepath.Join(filepath.Dir(rom), "New.gba")},
		{From: sav, To: filepath.Join(filepath.Dir(sav), "New.gba.sav")},
	}
	failures := 1
	tr.removeJournalFile = func(rel string) error {
		if failures > 0 {
			failures--
			return errors.New("remove failed")
		}
		root, err := os.OpenRoot(sd)
		if err != nil {
			return err
		}
		defer root.Close()
		return root.Remove(rel)
	}
	err := tr.RenameGroup("Old", steps, nil)
	if !errors.Is(err, ErrRenameCommit) {
		t.Fatalf("err %v", err)
	}
	if !exists(rom) || !exists(sav) || renamesLeft(t, tr) != 0 {
		t.Fatal("not rolled back")
	}
	// The removal made durable fails (the sync of the journal folder), and
	// keeps failing for the journal written again: injected only once the
	// files moved (Codex review of M23, round 3). The files go back first
	// whatever happens to the journal.
	tr.removeJournalFile = nil
	newROM := filepath.Join(filepath.Dir(rom), "New.gba")
	tr.SetSyncDir(func(dir string) error {
		if filepath.Dir(dir) == tr.renames && exists(newROM) {
			return errors.New("sync failed")
		}
		return nil
	})
	if err := tr.RenameGroup("Old", steps, nil); err == nil {
		t.Fatal("success although the journal removal is not durable")
	}
	if !exists(rom) || !exists(sav) || exists(newROM) {
		t.Fatal("not rolled back")
	}
	// The journal kept by that failure goes at the next start.
	tr.SetSyncDir(func(string) error { return nil })
	if err := tr.Recover(); err != nil || renamesLeft(t, tr) != 0 || !exists(rom) {
		t.Fatalf("recovery: %v", err)
	}
	// A verify that fails rolls back too.
	refused := errors.New("not listed")
	if err := tr.RenameGroup("Old", steps, func() error { return refused }); !errors.Is(err, refused) || !exists(rom) || renamesLeft(t, tr) != 0 {
		t.Fatalf("verify: %v", err)
	}
}

// Codex review of M23 (round 3): the exFAT driver of Linux numbers inodes
// in memory: after a restart the renamed file has another inode. The
// recovery still recognizes it (kind, size, date) and puts it back.
func TestRenameGroupRecoveryWithAnotherInode(t *testing.T) {
	tr, sd, clk, _ := newTestTrash(t)
	rom := write(t, sd, "Roms/GBA (GBA)/Old.gba", "rom")
	sav := write(t, sd, "Saves/GBA/Old.gba.sav", "save")
	steps := []RenameStep{
		{From: rom, To: filepath.Join(filepath.Dir(rom), "New.gba")},
		{From: sav, To: filepath.Join(filepath.Dir(sav), "New.gba.sav")},
	}
	calls := 0
	tr.renameExcl = func(a, b string) error {
		calls++
		if calls == 2 {
			panic("power cut")
		}
		return tr.renameNoReplaceInSD(a, b)
	}
	func() {
		defer func() { _ = recover() }()
		_ = tr.RenameGroup("Old", steps, nil)
	}()
	// The restart: every inode of the journal is another one now.
	des, err := os.ReadDir(tr.renames)
	if err != nil || len(des) != 1 {
		t.Fatalf("journal: %v", err)
	}
	jp := filepath.Join(tr.renames, des[0].Name(), renameJournalFile)
	data, err := os.ReadFile(jp)
	if err != nil {
		t.Fatal(err)
	}
	var j renameJournal
	if err := json.Unmarshal(data, &j); err != nil {
		t.Fatal(err)
	}
	for i := range j.Steps {
		if j.Steps[i].ID == "" {
			t.Skip("no inode here")
		}
		j.Steps[i].ID = "1:999999999"
	}
	data, _ = json.Marshal(j)
	if err := os.WriteFile(jp, data, 0o644); err != nil {
		t.Fatal(err)
	}
	tr2 := NewTrash(Layout{Root: filepath.Join(sd, ".userdata", "shared", "ReminisLink")}, sd, clk.Now, nil)
	if err := tr2.Recover(); err != nil {
		t.Fatal(err)
	}
	if !exists(rom) || exists(filepath.Join(filepath.Dir(rom), "New.gba")) || !exists(sav) || renamesLeft(t, tr2) != 0 {
		t.Fatal("not put back")
	}
	// Another file at the new name (other size) is still never taken.
	if (renameRecord{ID: "1:2", Kind: "file", Size: 3, MtimeNS: 1}).isEntry(lstat(t, sav)) {
		t.Fatal("another file accepted")
	}
}

// Codex review of M23 (round 3): a folder of the card replaced by a link
// (ext4) is never synced through it by the recovery.
func TestRenameGroupRecoveryNeverSyncsThroughALink(t *testing.T) {
	tr, sd, clk, _ := newTestTrash(t)
	write(t, sd, "Roms/GBA (GBA)/Arcade/Old.gba", "rom")
	write(t, sd, "Saves/GBA/Old.gba.sav", "save")
	arcade := filepath.Join(sd, "Roms", "GBA (GBA)", "Arcade")
	steps := []RenameStep{
		{From: filepath.Join(arcade, "Old.gba"), To: filepath.Join(arcade, "New.gba")},
		{From: filepath.Join(sd, "Saves/GBA/Old.gba.sav"), To: filepath.Join(sd, "Saves/GBA/New.gba.sav")},
	}
	calls := 0
	tr.renameExcl = func(a, b string) error {
		calls++
		if calls == 2 {
			panic("power cut")
		}
		return tr.renameNoReplaceInSD(a, b)
	}
	func() {
		defer func() { _ = recover() }()
		_ = tr.RenameGroup("Old", steps, nil)
	}()
	// The system folder becomes a link to a folder outside the card that
	// holds an Arcade folder too.
	outside := t.TempDir()
	write(t, outside, "Arcade/New.gba", "outside")
	gba := filepath.Join(sd, "Roms", "GBA (GBA)")
	if err := os.Rename(gba, gba+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, gba); err != nil {
		t.Skip("no links")
	}
	var synced []string
	tr2 := NewTrash(Layout{Root: filepath.Join(sd, ".userdata", "shared", "ReminisLink")}, sd, clk.Now, nil)
	tr2.SetSyncDir(func(dir string) error {
		synced = append(synced, dir)
		return nil
	})
	if err := tr2.Recover(); err != nil {
		t.Fatal(err)
	}
	for _, d := range synced {
		if strings.HasPrefix(d, gba+string(filepath.Separator)) || d == gba {
			t.Fatalf("synced through the link: %s", d)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "Arcade", "New.gba")); string(b) != "outside" {
		t.Fatal("the outside file was touched")
	}
	if _, err := os.Lstat(filepath.Join(outside, "Arcade", "Old.gba")); err == nil {
		t.Fatal("renamed outside the card")
	}
}

func TestRenameGroupRefusesLinksAndOtherFolders(t *testing.T) {
	tr, sd, _, _ := newTestTrash(t)
	rom := write(t, sd, "Roms/GBA (GBA)/Old.gba", "rom")
	if err := tr.RenameGroup("x", []RenameStep{{From: rom, To: filepath.Join(sd, "Saves", "Old.gba")}}, nil); err == nil {
		t.Fatal("moved to another folder")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(sd, "Link")); err != nil {
		t.Skip("no links")
	}
	if err := tr.RenameGroup("x", []RenameStep{{From: filepath.Join(sd, "Link", "a"), To: filepath.Join(sd, "Link", "b")}}, nil); err == nil {
		t.Fatal("renamed through a link")
	}
	if err := tr.RenameGroup("x", []RenameStep{{From: rom, To: filepath.Join(outside, "x")}}, nil); err == nil {
		t.Fatal("renamed outside the card")
	}
	if !exists(rom) {
		t.Fatal("rom moved")
	}
}
