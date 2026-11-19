package states

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

const (
	gba      = "Game Boy Advance (GBA)"
	statesGB = ".userdata/shared/GBA-gpsp/"
	locksGBA = ".userdata/shared/ReminisLink/locks/GBA/"
)

type env struct {
	t      *testing.T
	sd     string
	a      *nextui.Adapter
	layout store.Layout
	trash  *store.Trash
	svc    *Service
	logs   *bytes.Buffer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	sd := testutil.CopySDCard(t)
	roots := nextui.RootsForSD(sd, "tg5040")
	a := nextui.New(roots, nil)
	layout := store.Layout{Root: roots.Data}
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	trash := store.NewTrash(layout, sd, nil, logger)
	svc, err := New(Options{Adapter: a, Layout: layout, Trash: trash, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, sd: sd, a: a, layout: layout, trash: trash, svc: svc, logs: logs}
}

func (e *env) game(name string) osadapter.Game {
	e.t.Helper()
	g, err := e.a.Game(gba + "/" + name)
	if err != nil {
		e.t.Fatal(err)
	}
	return g
}

func (e *env) path(rel string) string { return filepath.Join(e.sd, filepath.FromSlash(rel)) }

func (e *env) read(rel string) string {
	e.t.Helper()
	data, err := os.ReadFile(e.path(rel))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(data)
}

func (e *env) exists(rel string) bool {
	_, err := os.Lstat(e.path(rel))
	return err == nil
}

// write changes a file as the emulator would (new content, new date).
func (e *env) write(rel, content string, age time.Duration) {
	e.t.Helper()
	testutil.WriteFile(e.t, e.sd, rel, content)
	at := time.Now().Add(-age)
	if err := os.Chtimes(e.path(rel), at, at); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) trashReasons() []string {
	entries, _, _ := e.trash.List()
	var out []string
	for _, en := range entries {
		out = append(out, en.Reason)
	}
	return out
}

func status(s Slot) string {
	if s.LockStatus == nil {
		return ""
	}
	return *s.LockStatus
}

func (e *env) slots(g osadapter.Game) map[int]Slot {
	e.t.Helper()
	list, err := e.svc.List(g)
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[int]Slot{}
	for _, s := range list.Slots {
		out[s.Slot] = s
	}
	return out
}

func TestListAndThumbnails(t *testing.T) {
	e := newEnv(t)
	g := e.game("Starlight Courier.gba")
	list, err := e.svc.List(g)
	if err != nil || list.ResumeSlot == nil || *list.ResumeSlot != 9 || len(list.Slots) != 3 {
		t.Fatalf("list: %+v %v", list, err)
	}
	for i, want := range []int{0, 1, 9} {
		s := list.Slots[i]
		if s.Slot != want || s.Locked || s.LockStatus != nil || s.SizeBytes == 0 || s.ModifiedAt.IsZero() {
			t.Errorf("slot %d: %+v", want, s)
		}
		if s.HasThumbnail != (want == 0) {
			t.Errorf("slot %d thumbnail: %v", want, s.HasThumbnail)
		}
	}
	p, err := e.svc.Thumbnail(g, 0)
	if err != nil || !strings.HasSuffix(p, "Starlight Courier.gba.0.bmp") {
		t.Fatalf("thumbnail: %q %v", p, err)
	}
	// Not a PNG (NextUI writes PNG in a .bmp): no thumbnail.
	e.write(".userdata/shared/.minui/GBA/Starlight Courier.gba.1.bmp", "BMnot a png", 0)
	if _, err := e.svc.Thumbnail(g, 1); !errors.Is(err, ErrNoThumbnail) {
		t.Errorf("non-PNG thumbnail: %v", err)
	}
	if _, err := e.svc.Thumbnail(g, 9); !errors.Is(err, ErrNoThumbnail) {
		t.Errorf("slot 9 has no thumbnail: %v", err)
	}
	if _, err := e.svc.Thumbnail(g, MaxSlot+1); !errors.Is(err, ErrInvalidSlot) {
		t.Errorf("bad slot: %v", err)
	}
	f, name, err := e.svc.Open(g, 1)
	if err != nil || name != "Starlight Courier.gba.st1" {
		t.Fatalf("open: %q %v", name, err)
	}
	f.Close()
	if _, _, err := e.svc.Open(g, 5); !errors.Is(err, ErrNoState) {
		t.Errorf("empty slot: %v", err)
	}
	// The multi-disc game: states named after its .m3u, in the PS core folder.
	ps, err := e.a.Game("Sony PlayStation (PS)/Mossy Hollow")
	if err != nil {
		t.Fatal(err)
	}
	if s := e.slots(ps); len(s) != 1 || s[0].SizeBytes == 0 {
		t.Errorf("multi-disc states: %+v", s)
	}
}

func TestDeleteState(t *testing.T) {
	e := newEnv(t)
	g := e.game("Starlight Courier.gba")
	res, err := e.svc.Delete(g, 0)
	if err != nil || len(res.Moved) != 2 || res.Entry.Reason != store.ReasonDeleteState {
		t.Fatalf("delete: %+v %v", res, err)
	}
	// The thumbnail goes with the state.
	if e.exists(statesGB+"Starlight Courier.gba.st0") || e.exists(".userdata/shared/.minui/GBA/Starlight Courier.gba.0.bmp") {
		t.Fatal("not moved")
	}
	if _, err := e.svc.Delete(g, 0); !errors.Is(err, ErrNoState) {
		t.Fatalf("deleted twice: %v", err)
	}
}

func TestLockUnlockRestore(t *testing.T) {
	e := newEnv(t)
	g := e.game("Starlight Courier.gba")
	original := e.read(statesGB + "Starlight Courier.gba.st1")
	s, err := e.svc.Lock(g, 1)
	if err != nil || !s.Locked || status(s) != StatusIntact {
		t.Fatalf("lock: %+v %v", s, err)
	}
	if !IsLocked(e.layout, g, 1) || e.read(locksGBA+"Starlight Courier.gba/slot1.st") != original || !e.exists(locksGBA+"Starlight Courier.gba/slot1.json") {
		t.Fatal("lock files")
	}
	if s, err := e.svc.Lock(g, 1); err != nil || status(s) != StatusIntact {
		t.Fatalf("lock twice: %+v %v", s, err)
	}
	if _, err := e.svc.Lock(g, 5); !errors.Is(err, ErrNoState) {
		t.Fatalf("lock empty slot: %v", err)
	}
	// A locked slot cannot be deleted nor replaced.
	if _, err := e.svc.Delete(g, 1); !errors.Is(err, ErrLocked) {
		t.Fatalf("delete locked: %v", err)
	}
	tmp := testutil.WriteFile(t, e.sd, ".userdata/shared/ReminisLink/uploads/x.part", "new")
	if _, err := e.svc.InstallLocked(g, 1, tmp); !errors.Is(err, ErrLocked) {
		t.Fatalf("install locked: %v", err)
	}

	// Restore by the app: the current state goes to the trash.
	e.write(statesGB+"Starlight Courier.gba.st1", "played on", 0)
	s, err = e.svc.RestoreLock(g, 1)
	if err != nil || status(s) != StatusIntact {
		t.Fatalf("restore: %+v %v", s, err)
	}
	if e.read(statesGB+"Starlight Courier.gba.st1") != original {
		t.Fatal("not restored")
	}
	if r := e.trashReasons(); len(r) != 1 || r[0] != store.ReasonRestore {
		t.Fatalf("trash: %v", r)
	}
	// Restoring an identical state changes nothing.
	if _, err := e.svc.RestoreLock(g, 1); err != nil || len(e.trashReasons()) != 1 {
		t.Fatalf("restore identical: %v", err)
	}
	if _, err := e.svc.RestoreLock(g, 0); !errors.Is(err, ErrNotLocked) {
		t.Fatalf("restore unlocked: %v", err)
	}

	// Unlock with an identical state: the copy is erased (same bytes in place).
	s, err = e.svc.Unlock(g, 1)
	if err != nil || s.Locked || s.LockStatus != nil || IsLocked(e.layout, g, 1) || len(e.trashReasons()) != 1 {
		t.Fatalf("unlock: %+v %v %v", s, err, e.trashReasons())
	}
	if e.exists(locksGBA + "Starlight Courier.gba") {
		t.Error("empty lock folder left")
	}
	// Unlocking again answers the slot.
	if s, err := e.svc.Unlock(g, 1); err != nil || s.Locked {
		t.Fatalf("unlock twice: %+v %v", s, err)
	}
	if _, err := e.svc.Unlock(g, 5); !errors.Is(err, ErrNoState) {
		t.Fatalf("unlock empty: %v", err)
	}

	// Unlock with a different state: the copy goes to the trash.
	if _, err := e.svc.Lock(g, 0); err != nil {
		t.Fatal(err)
	}
	e.write(statesGB+"Starlight Courier.gba.st0", "different", 0)
	if s, err := e.svc.Unlock(g, 0); err != nil || s.Locked || s.SizeBytes != int64(len("different")) {
		t.Fatalf("unlock different: %+v %v", s, err)
	}
	if r := e.trashReasons(); len(r) != 2 {
		t.Fatalf("copy not kept in the trash: %v", r)
	}
}

// TestLockCheckAtStart covers docs/specs/05-console-pak.md §5.2.
func TestLockCheckAtStart(t *testing.T) {
	e := newEnv(t)
	sc := e.game("Starlight Courier.gba")
	pg := e.game("Pixel Garden 🌱.gba")
	orig0 := e.read(statesGB + "Starlight Courier.gba.st0")
	orig1 := e.read(statesGB + "Starlight Courier.gba.st1")
	for _, slot := range []int{0, 1} {
		if _, err := e.svc.Lock(sc, slot); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.Lock(pg, 9); err != nil {
		t.Fatal(err)
	}

	// Nothing changed: all intact, nothing hashed or moved.
	r := e.svc.Maintain(context.Background())
	if r.LocksChecked != 3 || r.LocksIntact != 3 || len(r.Restored) != 0 || r.Failed != 0 {
		t.Fatalf("unchanged: %+v", r)
	}

	// Slot 0 overwritten in game, slot 1 deleted, slot 9 of Pixel Garden
	// rewritten with the same bytes (new date).
	e.write(statesGB+"Starlight Courier.gba.st0", "overwritten in game", 0)
	os.Remove(e.path(statesGB + "Starlight Courier.gba.st1"))
	pgState := e.read(statesGB + "Pixel Garden 🌱.gba.st9")
	e.write(statesGB+"Pixel Garden 🌱.gba.st9", pgState, -time.Hour)
	r = e.svc.Maintain(context.Background())
	if r.LocksChecked != 3 || r.LocksIntact != 1 || len(r.Restored) != 2 || r.Failed != 0 {
		t.Fatalf("after play: %+v", r)
	}
	if e.read(statesGB+"Starlight Courier.gba.st0") != orig0 || e.read(statesGB+"Starlight Courier.gba.st1") != orig1 {
		t.Fatal("states not restored")
	}
	// The overwritten state is in the trash (lock-restore); the missing
	// one had nothing to keep.
	entries, _, _ := e.trash.List()
	if len(entries) != 1 || entries[0].Reason != store.ReasonLockRestore {
		t.Fatalf("trash: %+v", entries)
	}
	if data, _ := os.ReadFile(filepath.Join(e.trash.Dir(), entries[0].ID, filepath.FromSlash(statesGB+"Starlight Courier.gba.st0"))); string(data) != "overwritten in game" {
		t.Fatal("overwritten state lost")
	}
	if !strings.Contains(e.logs.String(), "locked state was overwritten") || !strings.Contains(e.logs.String(), "locked state was missing") {
		t.Error("restorations not logged")
	}
	// "restored" is reported once, then "intact".
	s := e.slots(sc)
	if status(s[0]) != StatusRestored || status(s[1]) != StatusRestored {
		t.Fatalf("first list: %+v", s)
	}
	s = e.slots(sc)
	if status(s[0]) != StatusIntact || status(s[1]) != StatusIntact {
		t.Fatalf("second list: %+v", s)
	}
	if status(e.slots(pg)[9]) != StatusIntact {
		t.Fatal("same bytes must stay intact")
	}
	// Next start: nothing to do.
	if r := e.svc.Maintain(context.Background()); r.LocksIntact != 3 || len(r.Restored) != 0 {
		t.Fatalf("next start: %+v", r)
	}
}

func TestLockCheckEdgeCases(t *testing.T) {
	e := newEnv(t)
	sc := e.game("Starlight Courier.gba")
	if _, err := e.svc.Lock(sc, 0); err != nil {
		t.Fatal(err)
	}
	// A state missing while the pak runs: status missing, with the copy's size.
	os.Remove(e.path(statesGB + "Starlight Courier.gba.st0"))
	if s := e.slots(sc)[0]; status(s) != StatusMissing || !s.Locked || s.SizeBytes == 0 {
		t.Fatalf("missing: %+v", s)
	}
	// A corrupted locked copy is never restored.
	e.write(locksGBA+"Starlight Courier.gba/slot0.st", "corrupted copy", 0)
	r := e.svc.Maintain(context.Background())
	if r.Failed != 1 || len(r.Restored) != 0 || e.exists(statesGB+"Starlight Courier.gba.st0") {
		t.Fatalf("corrupted copy: %+v", r)
	}

	// A lock interrupted before its metadata: rebuilt from the copy.
	os.Remove(e.path(locksGBA + "Starlight Courier.gba/slot0.json"))
	e.write(statesGB+"Starlight Courier.gba.st0", "corrupted copy", 0)
	r = e.svc.Maintain(context.Background())
	if r.Failed != 0 || r.LocksIntact != 1 || !e.exists(locksGBA+"Starlight Courier.gba/slot0.json") {
		t.Fatalf("repair: %+v", r)
	}
	// Metadata left by an interrupted unlock, and temporary files: removed.
	testutil.WriteFile(t, e.sd, locksGBA+"Starlight Courier.gba/slot4.json", "{}")
	testutil.WriteFile(t, e.sd, locksGBA+"Starlight Courier.gba/slot0.st.tmp", "x")
	e.svc.Maintain(context.Background())
	if e.exists(locksGBA+"Starlight Courier.gba/slot4.json") || e.exists(locksGBA+"Starlight Courier.gba/slot0.st.tmp") {
		t.Fatal("leftovers kept")
	}

	// A lock of a game that no longer exists (deleted with keepSave, then
	// renamed): never restored, the lock stays (nothing lost).
	testutil.WriteFile(t, e.sd, locksGBA+"Gone Game.gba/slot2.st", "locked")
	testutil.WriteFile(t, e.sd, locksGBA+"Gone Game.gba/slot2.json",
		`{"version":1,"slot":2,"sha256":"`+sha256Hex("locked")+`","sizeBytes":6,"stateDir":"GBA-gpsp"}`)
	r = e.svc.Maintain(context.Background())
	if len(r.Restored) != 0 || r.Held != 1 || e.exists(statesGB+"Gone Game.gba.st2") || !e.exists(locksGBA+"Gone Game.gba/slot2.st") {
		t.Fatalf("gone game: %+v", r)
	}
	if !strings.Contains(e.logs.String(), "its game no longer exists") {
		t.Error("not logged")
	}

	// The time limit: nothing checked, everything left for the next start.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := e.svc.Maintain(ctx); r.LocksChecked != 0 || r.Skipped == 0 {
		t.Fatalf("time limit: %+v", r)
	}
}

// TestResumeHistory covers docs/specs/05-console-pak.md §5.3.
func TestResumeHistory(t *testing.T) {
	e := newEnv(t)
	sc := e.game("Starlight Courier.gba")
	r := e.svc.Maintain(context.Background())
	if r.Archived != 2 { // Starlight Courier and Pixel Garden have a .st9
		t.Fatalf("first start: %+v", r)
	}
	h := e.svc.ResumeHistory(sc)
	first := e.read(statesGB + "Starlight Courier.gba.st9")
	if len(h) != 1 || h[0].SizeBytes != int64(len(first)) || !ValidEntryID(h[0].ID) {
		t.Fatalf("history: %+v", h)
	}
	// Same state: nothing archived. Same bytes with a new date: nothing.
	if r := e.svc.Maintain(context.Background()); r.Archived != 0 {
		t.Fatalf("unchanged: %+v", r)
	}
	e.write(statesGB+"Starlight Courier.gba.st9", first, -time.Minute)
	if r := e.svc.Maintain(context.Background()); r.Archived != 0 {
		t.Fatalf("touched: %+v", r)
	}
	// New versions: at most 5, the oldest go to the trash.
	for i := 0; i < MaxResumeEntries+1; i++ {
		e.write(statesGB+"Starlight Courier.gba.st9", "resume "+string(rune('a'+i)), time.Duration(-i-2)*time.Minute)
		if r := e.svc.Maintain(context.Background()); r.Archived != 1 {
			t.Fatalf("version %d: %+v", i, r)
		}
	}
	h = e.svc.ResumeHistory(sc)
	if len(h) != MaxResumeEntries || h[0].SizeBytes != int64(len("resume f")) {
		t.Fatalf("history: %+v", h)
	}
	if reasons := e.trashReasons(); len(reasons) != 2 || reasons[0] != store.ReasonDeleteBackup {
		t.Fatalf("trash: %v", reasons)
	}

	// Restore an older version: the current one is already archived.
	target := h[3] // "resume c"
	h2, err := e.svc.RestoreResume(sc, target.ID)
	if err != nil || len(h2) != MaxResumeEntries || h2[0].ID != h[0].ID {
		t.Fatalf("restore: %+v %v", h2, err)
	}
	if e.read(statesGB+"Starlight Courier.gba.st9") != "resume c" {
		t.Fatal("not restored")
	}
	// The restored version is already in the history: not archived again.
	if r := e.svc.Maintain(context.Background()); r.Archived != 0 {
		t.Fatalf("after restore: %+v", r)
	}
	// A current version not yet archived is archived before the restore.
	e.write(statesGB+"Starlight Courier.gba.st9", "played after restore", -time.Hour)
	h3, err := e.svc.RestoreResume(sc, target.ID)
	if err != nil || len(h3) != MaxResumeEntries || h3[0].SizeBytes != int64(len("played after restore")) {
		t.Fatalf("restore with a new current version: %+v %v", h3, err)
	}
	if _, err := e.svc.RestoreResume(sc, "rh_20261118T165500Z_dead"); !errors.Is(err, ErrEntryNotFound) {
		t.Errorf("unknown entry: %v", err)
	}
	if _, err := e.svc.RestoreResume(sc, "../x"); !errors.Is(err, ErrInvalidEntryID) {
		t.Errorf("bad entry: %v", err)
	}
	// Locked resume slot: refused.
	if _, err := e.svc.Lock(sc, 9); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RestoreResume(sc, target.ID); !errors.Is(err, ErrLocked) {
		t.Errorf("locked resume slot: %v", err)
	}
}

func TestUnsupportedStates(t *testing.T) {
	e := newEnv(t)
	sc := e.game("Starlight Courier.gba")
	if _, err := e.svc.Lock(sc, 0); err != nil {
		t.Fatal(err)
	}
	e.write(statesGB+"Starlight Courier.gba.st0", "changed", 0)
	testutil.WriteFile(t, e.sd, ".userdata/shared/minuisettings.txt", "stateFormat=2\n")
	if _, err := e.svc.Lock(sc, 1); !errors.Is(err, osadapter.ErrUnsupportedFormat) {
		t.Fatalf("lock: %v", err)
	}
	if _, err := e.svc.RestoreLock(sc, 0); !errors.Is(err, osadapter.ErrUnsupportedFormat) {
		t.Fatalf("restore: %v", err)
	}
	if r := e.svc.Maintain(context.Background()); r.Held != 1 || len(r.Restored) != 0 || r.Archived != 0 {
		t.Fatalf("maintenance with another format: %+v", r)
	}
	// The state differs from the copy and was not restored: modified.
	if st := status(e.slots(sc)[0]); st != StatusModified {
		t.Fatalf("status with another format: %s", st)
	}
	if e.read(statesGB+"Starlight Courier.gba.st0") != "changed" {
		t.Fatal("state touched")
	}
	if list, _ := e.svc.List(sc); list.ResumeSlot != nil {
		t.Fatal("resume slot with another format")
	}
	os.Remove(e.path(".userdata/shared/minuisettings.txt"))

	// A system without emulator pak nor state folder.
	testutil.WriteFile(t, e.sd, "Roms/Nothing (NOPE)/game.bin", "rom")
	g, err := e.a.Game("Nothing (NOPE)/game.bin")
	if err != nil {
		t.Fatal(err)
	}
	tmp := testutil.WriteFile(t, e.sd, ".userdata/shared/ReminisLink/uploads/x.part", "new")
	if _, err := e.svc.InstallLocked(g, 0, tmp); !errors.Is(err, osadapter.ErrUnsupportedFormat) {
		t.Fatalf("install without state folder: %v", err)
	}
	if list, err := e.svc.List(g); err != nil || len(list.Slots) != 0 {
		t.Fatalf("list without state folder: %+v %v", list, err)
	}
}

// TestCoreChanged: a lock and the resume history remember the state
// folder (<TAG>-<core>) of their copies; after a change of emulator core
// nothing is restored into the other folder.
func TestCoreChanged(t *testing.T) {
	e := newEnv(t)
	sc := e.game("Starlight Courier.gba")
	if _, err := e.svc.Lock(sc, 0); err != nil {
		t.Fatal(err)
	}
	e.svc.Maintain(context.Background()) // archives the gpsp resume state
	gpspEntry := e.svc.ResumeHistory(sc)[0]

	// The user installs an extra GBA pak with the mgba core.
	testutil.WriteFile(t, e.sd, "Emus/tg5040/GBA.pak/launch.sh", "#!/bin/sh\nEMU_EXE=mgba\n")
	if st := status(e.slots(sc)[0]); st != StatusCoreChanged {
		t.Fatalf("status: %s", st)
	}
	r := e.svc.Maintain(context.Background())
	if len(r.Restored) != 0 || r.Held != 1 || e.exists(".userdata/shared/GBA-mgba/Starlight Courier.gba.st0") {
		t.Fatalf("maintenance: %+v", r)
	}
	if _, err := e.svc.RestoreLock(sc, 0); !errors.Is(err, ErrCoreChanged) {
		t.Fatalf("restore: %v", err)
	}
	// Resume history: the mgba version is archived on its own, never
	// compared with the gpsp entries, and a gpsp entry is not restored.
	e.write(".userdata/shared/GBA-mgba/Starlight Courier.gba.st9", e.read(statesGB+"Starlight Courier.gba.st9"), 0)
	if r := e.svc.Maintain(context.Background()); r.Archived != 1 {
		t.Fatalf("mgba resume state (same bytes, other core) not archived: %+v", r)
	}
	if _, err := e.svc.RestoreResume(sc, gpspEntry.ID); !errors.Is(err, ErrCoreChanged) {
		t.Fatalf("restore resume: %v", err)
	}
	// Back to gpsp: the lock is intact again.
	os.Remove(e.path("Emus/tg5040/GBA.pak/launch.sh"))
	if st := status(e.slots(sc)[0]); st != StatusIntact {
		t.Fatalf("status back: %s", st)
	}
}

// TestLegacyLock: a lock written without its state folder takes the
// current one at its first reading.
func TestLegacyLock(t *testing.T) {
	e := newEnv(t)
	sc := e.game("Starlight Courier.gba")
	if _, err := e.svc.Lock(sc, 1); err != nil {
		t.Fatal(err)
	}
	meta := e.path(locksGBA + "Starlight Courier.gba/slot1.json")
	data, _ := os.ReadFile(meta)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	delete(m, "stateDir")
	data, _ = json.Marshal(m)
	os.WriteFile(meta, data, 0o644)
	if st := status(e.slots(sc)[1]); st != StatusIntact {
		t.Fatalf("status: %s", st)
	}
	if !strings.Contains(e.read(locksGBA+"Starlight Courier.gba/slot1.json"), `"stateDir": "GBA-gpsp"`) {
		t.Fatal("lock not completed")
	}
}

// TestModifiedStatus: "intact" only when the state matches its copy.
func TestModifiedStatus(t *testing.T) {
	e := newEnv(t)
	sc := e.game("Starlight Courier.gba")
	for _, slot := range []int{0, 1} {
		if _, err := e.svc.Lock(sc, slot); err != nil {
			t.Fatal(err)
		}
	}
	// Overwritten in game, maintenance skipped (time limit): modified.
	e.write(statesGB+"Starlight Courier.gba.st0", "overwritten", 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.svc.Maintain(ctx)
	if st := status(e.slots(sc)[0]); st != StatusModified {
		t.Fatalf("skipped maintenance: %s", st)
	}
	// Damaged copy: not restored, modified.
	e.write(statesGB+"Starlight Courier.gba.st1", "overwritten", 0)
	e.write(locksGBA+"Starlight Courier.gba/slot1.st", "damaged", 0)
	r := e.svc.Maintain(context.Background())
	if len(r.Restored) != 1 || r.Failed != 1 {
		t.Fatalf("maintenance: %+v", r)
	}
	s := e.slots(sc)
	if status(s[0]) != StatusRestored || status(s[1]) != StatusModified {
		t.Fatalf("statuses: %s %s", status(s[0]), status(s[1]))
	}
	if _, err := e.svc.RestoreLock(sc, 1); !errors.Is(err, ErrCorruptCopy) {
		t.Fatalf("restore damaged: %v", err)
	}
}

// TestSideFiles: the NextUI thumbnail and disc file of a slot go to the
// trash with the state they describe.
func TestSideFiles(t *testing.T) {
	e := newEnv(t)
	sc := e.game("Starlight Courier.gba")
	thumb := ".userdata/shared/.minui/GBA/Starlight Courier.gba.0.bmp"
	disc := ".userdata/shared/.minui/GBA/Starlight Courier.gba.0.txt"
	testutil.WriteFile(t, e.sd, disc, "disc 1")
	tmp := testutil.WriteFile(t, e.sd, ".userdata/shared/ReminisLink/uploads/x.part", "uploaded")
	if _, err := e.svc.InstallLocked(sc, 0, tmp); err != nil {
		t.Fatal(err)
	}
	// The thumbnail of the old state goes; the disc file stays (the user
	// knows which disc the uploaded state belongs to).
	if e.exists(thumb) || e.read(disc) != "disc 1" {
		t.Fatal("thumbnail kept or disc file moved after an upload")
	}
	entries, _, _ := e.trash.List()
	if len(entries) != 1 || len(entries[0].Files) != 2 {
		t.Fatalf("trash: %+v", entries)
	}
	// Restore of a locked copy: the thumbnail of the overwritten state goes too.
	if _, err := e.svc.Lock(sc, 0); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, e.sd, statesGB+"Starlight Courier.gba.st0", "played")
	testutil.WriteFile(t, e.sd, thumb, "\x89PNG\r\n\x1a\nnew")
	if _, err := e.svc.RestoreLock(sc, 0); err != nil {
		t.Fatal(err)
	}
	if e.exists(thumb) {
		t.Fatal("thumbnail of the overwritten state kept")
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestLockKeepsDisc: the disc file and the thumbnail of a multi-disc state
// are kept with its lock and put back with the copy; an old lock without
// them leaves the current disc file in place.
func TestLockKeepsDisc(t *testing.T) {
	e := newEnv(t)
	ps, err := e.a.Game("Sony PlayStation (PS)/Mossy Hollow")
	if err != nil {
		t.Fatal(err)
	}
	const (
		st    = ".userdata/shared/PS-pcsx_rearmed/Mossy Hollow.m3u.st0"
		disc  = ".userdata/shared/.minui/PS/Mossy Hollow.m3u.0.txt"
		thumb = ".userdata/shared/.minui/PS/Mossy Hollow.m3u.0.bmp"
		locks = ".userdata/shared/ReminisLink/locks/PS/Mossy Hollow.m3u/"
	)
	png := "\x89PNG\r\n\x1a\ndisc 2 picture"
	testutil.WriteFile(t, e.sd, disc, "Mossy Hollow (Disc 2).cue")
	testutil.WriteFile(t, e.sd, thumb, png)
	locked := e.read(st)
	if _, err := e.svc.Lock(ps, 0); err != nil {
		t.Fatal(err)
	}
	if e.read(locks+"slot0.txt") != "Mossy Hollow (Disc 2).cue" || e.read(locks+"slot0.bmp") != png {
		t.Fatal("side files not kept with the lock")
	}
	// Played on disc 1 and saved in the same slot.
	e.write(st, "disc 1 state", 0)
	testutil.WriteFile(t, e.sd, disc, "Mossy Hollow (Disc 1).cue")
	testutil.WriteFile(t, e.sd, thumb, "\x89PNG\r\n\x1a\ndisc 1 picture")
	if r := e.svc.Maintain(context.Background()); len(r.Restored) != 1 {
		t.Fatalf("maintenance: %+v", r)
	}
	if e.read(st) != locked || e.read(disc) != "Mossy Hollow (Disc 2).cue" || e.read(thumb) != png {
		t.Fatal("state, disc or thumbnail not restored together")
	}
	// An old lock without side copies: the current disc file stays.
	os.Remove(e.path(locks + "slot0.txt"))
	os.Remove(e.path(locks + "slot0.bmp"))
	e.write(st, "disc 1 again", 0)
	testutil.WriteFile(t, e.sd, disc, "Mossy Hollow (Disc 1).cue")
	if _, err := e.svc.RestoreLock(ps, 0); err != nil {
		t.Fatal(err)
	}
	if e.read(disc) != "Mossy Hollow (Disc 1).cue" || e.exists(thumb) {
		t.Fatal("old lock: disc file must stay, stale thumbnail must go")
	}
	// Unlock removes the side copies with the copy.
	if _, err := e.svc.Lock(ps, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Unlock(ps, 0); err != nil {
		t.Fatal(err)
	}
	if e.exists(locks) {
		t.Fatal("lock folder left")
	}
}

// TestUnknownLockFolder: a lock rebuilt from its copy has no known state
// folder: never restored automatically, until it is locked again.
func TestUnknownLockFolder(t *testing.T) {
	e := newEnv(t)
	sc := e.game("Starlight Courier.gba")
	if _, err := e.svc.Lock(sc, 0); err != nil {
		t.Fatal(err)
	}
	os.Remove(e.path(locksGBA + "Starlight Courier.gba/slot0.json"))
	e.write(statesGB+"Starlight Courier.gba.st0", "played", 0)
	r := e.svc.Maintain(context.Background())
	if len(r.Restored) != 0 || r.Held != 1 || e.read(statesGB+"Starlight Courier.gba.st0") != "played" {
		t.Fatalf("maintenance: %+v", r)
	}
	if st := status(e.slots(sc)[0]); st != StatusModified {
		t.Fatalf("status: %s", st)
	}
	if _, err := e.svc.RestoreLock(sc, 0); !errors.Is(err, ErrUnknownCore) {
		t.Fatalf("restore: %v", err)
	}
	// Locking again confirms: the current state is locked, the old copy kept in the trash.
	if s, err := e.svc.Lock(sc, 0); err != nil || status(s) != StatusIntact {
		t.Fatalf("lock again: %+v %v", s, err)
	}
	if !strings.Contains(e.read(locksGBA+"Starlight Courier.gba/slot0.json"), `"stateDir": "GBA-gpsp"`) || len(e.trashReasons()) != 1 {
		t.Fatal("lock not confirmed")
	}
	// No state folder (emulator pak gone): modified, not core-changed.
	os.Remove(e.path(".system/tg5040/paks/Emus/GBA.pak/launch.sh"))
	os.Rename(e.path(".userdata/shared/GBA-gpsp"), e.path(".userdata/shared/GBA-gpsp-old-x"))
	l := e.svc.readLock(e.svc.lockDir(sc), 0)
	if e.svc.coreChanged(sc, l) || !e.svc.unknownCore(sc, l) {
		t.Fatal("unknown current folder must not be core-changed")
	}
}

// Codex review of M5: a resume state with the date and size of an archived
// entry but other bytes was not archived before a restore overwrote it.
func TestRestoreResumeArchivesSameDateAndSizeByContent(t *testing.T) {
	e := newEnv(t)
	sc := e.game("Starlight Courier.gba")
	st9 := statesGB + "Starlight Courier.gba.st9"
	e.write(st9, "version A", time.Hour)
	e.svc.Maintain(context.Background())
	e.write(st9, "version B", 2*time.Hour)
	e.svc.Maintain(context.Background())
	h := e.svc.ResumeHistory(sc)
	if len(h) != 2 {
		t.Fatalf("history %+v", h)
	}
	// The emulator writes other bytes with the date and size of "version B".
	fi, _ := os.Stat(e.path(st9))
	testutil.WriteFile(t, e.sd, st9, "version C")
	os.Chtimes(e.path(st9), fi.ModTime(), fi.ModTime())
	older := h[1] // h[0] is B (newest), h[1] is A
	if _, err := e.svc.RestoreResume(sc, older.ID); err != nil {
		t.Fatal(err)
	}
	if e.read(st9) != "version A" {
		t.Fatal("not restored")
	}
	// "version C" must survive somewhere: in the history.
	found := false
	dir := e.svc.resumeDirOf("GBA", "Starlight Courier.gba")
	for _, en := range e.svc.ResumeHistory(sc) {
		if data, _ := os.ReadFile(filepath.Join(dir, en.ID+stateExt)); string(data) == "version C" {
			found = true
		}
	}
	if !found {
		t.Fatal("the only copy of the overwritten resume state was lost")
	}
}

// Codex review of M5: a multi-disc locked state whose disc file differs
// from its locked copy is not intact (a power cut between the state and
// its .txt left it half restored): the next start puts the set back.
func TestLockIntactOnlyWithItsSideFiles(t *testing.T) {
	e := newEnv(t)
	ps, err := e.a.Game("Sony PlayStation (PS)/Mossy Hollow")
	if err != nil {
		t.Fatal(err)
	}
	const (
		st   = ".userdata/shared/PS-pcsx_rearmed/Mossy Hollow.m3u.st0"
		disc = ".userdata/shared/.minui/PS/Mossy Hollow.m3u.0.txt"
	)
	testutil.WriteFile(t, e.sd, disc, "Mossy Hollow (Disc 2).cue")
	if _, err := e.svc.Lock(ps, 0); err != nil {
		t.Fatal(err)
	}
	locked := e.read(st)
	// The state is the locked one, but its disc file is another disc.
	testutil.WriteFile(t, e.sd, disc, "Mossy Hollow (Disc 1).cue")
	if got := status(e.slots(ps)[0]); got != StatusModified {
		t.Fatalf("status %q, want modified", got)
	}
	r := e.svc.Maintain(context.Background())
	if r.LocksIntact != 0 || len(r.Restored) != 1 {
		t.Fatalf("maintenance %+v", r)
	}
	if e.read(st) != locked || e.read(disc) != "Mossy Hollow (Disc 2).cue" {
		t.Fatal("the locked set was not put back")
	}
	if r := e.svc.Maintain(context.Background()); r.LocksIntact != 1 {
		t.Fatalf("second start %+v", r)
	}
	// A manual restore does not stop on the state alone either.
	testutil.WriteFile(t, e.sd, disc, "Mossy Hollow (Disc 1).cue")
	if _, err := e.svc.RestoreLock(ps, 0); err != nil {
		t.Fatal(err)
	}
	if e.read(disc) != "Mossy Hollow (Disc 2).cue" {
		t.Fatal("manual restore kept the wrong disc file")
	}
}

// Codex review of M5: an emulator pak whose core cannot be read (a custom
// pak choosing it in a shell variable) suspends the states of its tag:
// nothing is written nor restored into the folder of the former core.
func TestUnknownCoreSuspendsTheTag(t *testing.T) {
	e := newEnv(t)
	sc := e.game("Starlight Courier.gba")
	if _, err := e.svc.Lock(sc, 0); err != nil {
		t.Fatal(err)
	}
	e.write(statesGB+"Starlight Courier.gba.st0", "overwritten in game", 0)
	testutil.WriteFile(t, e.sd, "Emus/tg5040/GBA.pak/launch.sh", "#!/bin/sh\nEMU_EXE=$CORE\n")
	r := e.svc.Maintain(context.Background())
	if len(r.Restored) != 0 || r.Held != 1 {
		t.Fatalf("maintenance %+v", r)
	}
	if e.read(statesGB+"Starlight Courier.gba.st0") != "overwritten in game" {
		t.Fatal("restored into the folder of the former core")
	}
	if _, err := e.svc.RestoreLock(sc, 0); !errors.Is(err, osadapter.ErrUnsupportedFormat) {
		t.Fatalf("manual restore: %v", err)
	}
	if _, err := e.svc.Lock(sc, 1); !errors.Is(err, osadapter.ErrUnsupportedFormat) {
		t.Fatalf("lock: %v", err)
	}
}

// Codex review of M5: the time limit of the maintenance also stops a
// long read already started (a big state being hashed).
func TestMaintainStopsInsideALongRead(t *testing.T) {
	e := newEnv(t)
	sc := e.game("Starlight Courier.gba")
	if _, err := e.svc.Lock(sc, 0); err != nil {
		t.Fatal(err)
	}
	// A big resume state to archive (hashed, then copied).
	big := strings.Repeat("s", 8<<20)
	e.write(statesGB+"Starlight Courier.gba.st9", big, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := e.svc.Maintain(ctx)
	if r.Archived != 0 || r.Failed != 0 || r.Skipped == 0 {
		t.Fatalf("cancelled maintenance %+v", r)
	}
	// The hash itself stops with the context.
	if _, _, err := store.HashFileContext(ctx, e.path(statesGB+"Starlight Courier.gba.st9")); !errors.Is(err, context.Canceled) {
		t.Fatalf("hash: %v", err)
	}
	if r := e.svc.Maintain(context.Background()); r.Archived == 0 {
		t.Fatalf("next start %+v", r)
	}
}

// Codex review of M8: when a lock restore cannot be confirmed (the folder
// of the disc files cannot be synced after the rename), the temporary
// files the journal names are kept and the next start finishes it.
func TestLockRestoreKeepsTheTempsOfAPendingReplacement(t *testing.T) {
	e := newEnv(t)
	ps, err := e.a.Game("Sony PlayStation (PS)/Mossy Hollow")
	if err != nil {
		t.Fatal(err)
	}
	const (
		st   = ".userdata/shared/PS-pcsx_rearmed/Mossy Hollow.m3u.st0"
		disc = ".userdata/shared/.minui/PS/Mossy Hollow.m3u.0.txt"
	)
	testutil.WriteFile(t, e.sd, disc, "Mossy Hollow (Disc 2).cue")
	if _, err := e.svc.Lock(ps, 0); err != nil {
		t.Fatal(err)
	}
	locked := e.read(st)
	e.write(st, "disc 1 state", 0)
	testutil.WriteFile(t, e.sd, disc, "Mossy Hollow (Disc 1).cue")
	minui := filepath.Dir(e.path(disc))
	armed := false
	e.trash.SetSyncDir(func(dir string) error {
		if data, err := os.ReadFile(e.path(disc)); err == nil && string(data) == "Mossy Hollow (Disc 2).cue" {
			armed = true // the locked disc file was renamed in place
		}
		if dir == minui && armed {
			return errors.New("simulated sync failure")
		}
		return nil
	})
	if _, err := e.svc.RestoreLock(ps, 0); !errors.Is(err, store.ErrReplaceFailed) {
		t.Fatalf("restore: %v", err)
	}
	pending := e.trash.PendingSources()
	if len(pending) != 2 {
		t.Fatalf("pending sources %v", pending)
	}
	for p := range pending {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("temporary file of the pending replacement removed: %v", err)
		}
	}
	e.trash.SetSyncDir(store.SyncDirChecked)
	if err := e.trash.Recover(); err != nil {
		t.Fatal(err)
	}
	if e.read(st) != locked || e.read(disc) != "Mossy Hollow (Disc 2).cue" {
		t.Fatal("the locked set was not finished at the next start")
	}
}
