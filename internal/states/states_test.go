package states

import (
	"bytes"
	"context"
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

	// A lock of a game deleted with keepSave (no ROM): still checked.
	testutil.WriteFile(t, e.sd, locksGBA+"Gone Game.gba/slot2.st", "locked")
	r = e.svc.Maintain(context.Background())
	if len(r.Restored) != 1 || e.read(statesGB+"Gone Game.gba.st2") != "locked" {
		t.Fatalf("gone game: %+v", r)
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
	if r := e.svc.Maintain(context.Background()); r.LocksChecked != 0 || r.Archived != 0 {
		t.Fatalf("maintenance with another format: %+v", r)
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
