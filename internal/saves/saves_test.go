package saves

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

const gba = "Game Boy Advance (GBA)"

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

type env struct {
	t     *testing.T
	sd    string
	a     *nextui.Adapter
	trash *store.Trash
	clk   *clock
	svc   *Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	sd := testutil.CopySDCard(t)
	roots := nextui.RootsForSD(sd, "tg5040")
	a := nextui.New(roots, nil)
	layout := store.Layout{Root: roots.Data}
	clk := &clock{t: time.Date(2026, 11, 18, 17, 0, 0, 0, time.UTC)}
	trash := store.NewTrash(layout, sd, clk.Now, nil)
	svc, err := New(Options{Adapter: a, Layout: layout, Trash: trash, Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, sd: sd, a: a, trash: trash, clk: clk, svc: svc}
}

func (e *env) game(name string) osadapter.Game {
	e.t.Helper()
	g, err := e.a.Game(gba + "/" + name)
	if err != nil {
		e.t.Fatal(err)
	}
	return g
}

func (e *env) read(rel string) string {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.sd, filepath.FromSlash(rel)))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(data)
}

func (e *env) temp(content string) string {
	e.t.Helper()
	tmp, err := e.svc.WriteTemp([]byte(content))
	if err != nil {
		e.t.Fatal(err)
	}
	return tmp
}

const backupsDir = ".userdata/shared/ReminisLink/backups/GBA/Starlight Courier.gba/saves/"

func TestCreateListDownload(t *testing.T) {
	e := newEnv(t)
	g := e.game("Starlight Courier.gba")
	old := e.read("Saves/GBA/Starlight Courier.gba.sav")
	if list, err := e.svc.List(g); err != nil || len(list) != 0 {
		t.Fatalf("empty list: %v %v", list, err)
	}
	label := "Avant le boss final"
	b, err := e.svc.Create(g, &label)
	if err != nil || b.Reason != ReasonManual || b.Label == nil || *b.Label != label || b.SizeBytes != int64(len(old)) {
		t.Fatalf("create: %+v %v", b, err)
	}
	if !ValidBackupID(b.ID) || !b.CreatedAt.Equal(e.clk.Now()) {
		t.Fatalf("id/date: %+v", b)
	}
	if e.read(backupsDir+b.ID+".sav") != old {
		t.Fatal("copy content")
	}
	// No label, empty label: null.
	empty := ""
	b2, err := e.svc.Create(g, &empty)
	if err != nil || b2.Label != nil {
		t.Fatalf("empty label: %+v %v", b2, err)
	}
	list, _ := e.svc.List(g)
	if len(list) != 2 || list[0].ID != b2.ID || list[1].ID != b.ID {
		t.Fatalf("list (newest first): %+v", list)
	}
	f, name, err := e.svc.OpenBackup(g, b.ID)
	if err != nil || name != "Starlight Courier ("+b.ID+").sav" {
		t.Fatalf("open: %q %v", name, err)
	}
	f.Close()
	f, name, err = e.svc.OpenSave(g)
	if err != nil || name != "Starlight Courier.gba.sav" {
		t.Fatalf("open save: %q %v", name, err)
	}
	f.Close()

	// Errors.
	if _, err := e.svc.Create(e.game("Lantern Keeper [b1].gba"), nil); !errors.Is(err, ErrNoSave) {
		t.Errorf("no save: %v", err)
	}
	long := strings.Repeat("é", MaxLabelRunes+1)
	ctrl := "a\tb"
	for _, l := range []*string{&long, &ctrl} {
		if _, err := e.svc.Create(g, l); !errors.Is(err, ErrInvalidLabel) {
			t.Errorf("label %q: %v", *l, err)
		}
	}
	ok80 := strings.Repeat("é", MaxLabelRunes)
	if _, err := e.svc.Create(g, &ok80); err != nil {
		t.Errorf("80 characters: %v", err)
	}
	if _, _, err := e.svc.OpenBackup(g, "../x"); !errors.Is(err, ErrInvalidBackupID) {
		t.Errorf("bad id: %v", err)
	}
	if _, _, err := e.svc.OpenBackup(g, "bk_20261118T170000Z_ffff"); !errors.Is(err, ErrBackupNotFound) {
		t.Errorf("unknown id: %v", err)
	}
	// A copy without metadata (interrupted backup) is never listed.
	testutil.WriteFile(t, e.sd, backupsDir+"bk_20261118T170000Z_0000.sav", "orphan")
	if list, _ := e.svc.List(g); len(list) != 3 {
		t.Errorf("orphan copy listed: %+v", list)
	}
}

func TestReplaceAndRestore(t *testing.T) {
	e := newEnv(t)
	g := e.game("Starlight Courier.gba")
	old := e.read("Saves/GBA/Starlight Courier.gba.sav")
	b, err := e.svc.Replace(g, e.temp("new save"))
	if err != nil || b == nil || b.Reason != ReasonBeforeReplace || b.Label == nil || *b.Label != LabelBeforeReplace {
		t.Fatalf("replace: %+v %v", b, err)
	}
	if e.read("Saves/GBA/Starlight Courier.gba.sav") != "new save" || e.read(backupsDir+b.ID+".sav") != old {
		t.Fatal("replace content")
	}
	// The previous save is kept as a backup, not also moved to the trash.
	if entries, _, _ := e.trash.List(); len(entries) != 0 {
		t.Fatalf("trash: %+v", entries)
	}
	// Restore the backup: the current save is backed up first.
	safety, err := e.svc.Restore(g, b.ID)
	if err != nil || safety == nil || safety.Reason != ReasonBeforeRestore || safety.Label != nil {
		t.Fatalf("restore: %+v %v", safety, err)
	}
	if e.read("Saves/GBA/Starlight Courier.gba.sav") != old || e.read(backupsDir+safety.ID+".sav") != "new save" {
		t.Fatal("restore content")
	}
	// A corrupted backup is never restored.
	if err := os.WriteFile(filepath.Join(e.sd, backupsDir+b.ID+".sav"), []byte("bit rot"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Restore(g, b.ID); !errors.Is(err, ErrCorruptBackup) {
		t.Fatalf("corrupt: %v", err)
	}
	if e.read("Saves/GBA/Starlight Courier.gba.sav") != old {
		t.Fatal("save changed by a refused restore")
	}
	// No temporary file is left in the uploads folder.
	if des, _ := os.ReadDir(filepath.Join(e.sd, ".userdata/shared/ReminisLink/uploads")); len(des) != 0 {
		t.Errorf("temporary files left: %v", des)
	}

	// A game without save: no backup, the save is created.
	lk := e.game("Lantern Keeper [b1].gba")
	if b, err := e.svc.Replace(lk, e.temp("first")); err != nil || b != nil {
		t.Fatalf("first save: %+v %v", b, err)
	}
	if e.read("Saves/GBA/Lantern Keeper [b1].gba.sav") != "first" {
		t.Fatal("first save content")
	}

	// A save spelled with another case is replaced in place.
	cafe := e.game("Café Rêverie (France).gba")
	if _, err := e.svc.Replace(cafe, e.temp("café")); err != nil {
		t.Fatal(err)
	}
	if e.read("Saves/GBA/café rêverie (france).gba.sav") != "café" {
		t.Fatal("case-insensitive replace")
	}
	des, _ := os.ReadDir(filepath.Join(e.sd, "Saves", "GBA"))
	for _, d := range des {
		if d.Name() == "Café Rêverie (France).gba.sav" {
			t.Fatal("second save file created")
		}
	}
}

func TestAutomaticLimit(t *testing.T) {
	e := newEnv(t)
	g := e.game("Starlight Courier.gba")
	manual, err := e.svc.Create(g, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The clock goes back in the middle: the order (and the limit) follow
	// the creation order, never the dates; ids stay unique in one second.
	var ids []string
	for i := 0; i < MaxAutoBackups+3; i++ {
		if i == 5 {
			e.clk.Set(e.clk.Now().Add(-48 * time.Hour))
		}
		b, err := e.svc.Replace(g, e.temp("v"+string(rune('a'+i))))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, b.ID)
	}
	list, _ := e.svc.List(g)
	if len(list) != MaxAutoBackups+1 {
		t.Fatalf("%d backups, want %d", len(list), MaxAutoBackups+1)
	}
	for i, b := range list[:MaxAutoBackups] {
		if want := ids[len(ids)-1-i]; b.ID != want {
			t.Fatalf("backup %d = %s, want %s (newest first)", i, b.ID, want)
		}
	}
	if list[MaxAutoBackups].ID != manual.ID {
		t.Fatal("the manual backup must stay")
	}
	// The 3 oldest automatic backups are in the trash, not erased.
	entries, _, _ := e.trash.List()
	if len(entries) != 3 {
		t.Fatalf("trash: %+v", entries)
	}
	for _, en := range entries {
		if en.Reason != store.ReasonDeleteBackup || len(en.Files) != 1 || !strings.HasSuffix(en.Files[0], ".sav") {
			t.Errorf("trash entry %+v", en)
		}
	}
}

func TestDeleteBackup(t *testing.T) {
	e := newEnv(t)
	g := e.game("Starlight Courier.gba")
	b, _ := e.svc.Create(g, nil)
	if err := e.svc.Delete(g, b.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := e.svc.List(g); len(list) != 0 {
		t.Fatalf("still listed: %+v", list)
	}
	entries, _, _ := e.trash.List()
	if len(entries) != 1 || entries[0].Reason != store.ReasonDeleteBackup || entries[0].Label != "Starlight Courier" {
		t.Fatalf("trash: %+v", entries)
	}
	if err := e.svc.Delete(g, b.ID); !errors.Is(err, ErrBackupNotFound) {
		t.Fatalf("deleted twice: %v", err)
	}
}

func TestUnsupportedSaveFormat(t *testing.T) {
	e := newEnv(t)
	g := e.game("Starlight Courier.gba")
	testutil.WriteFile(t, e.sd, ".userdata/shared/minuisettings.txt", "saveFormat=1\nstateFormat=0\n")
	tmp := e.temp("new")
	if _, err := e.svc.Replace(g, tmp); !errors.Is(err, osadapter.ErrUnsupportedFormat) {
		t.Fatalf("replace: %v", err)
	}
	if _, err := os.Stat(tmp); err == nil {
		t.Error("temporary file left after a refused replace")
	}
	if e.read("Saves/GBA/Starlight Courier.gba.sav") == "new" {
		t.Error("save replaced")
	}
	b, _ := e.svc.Create(g, nil) // reading stays possible
	if _, err := e.svc.Restore(g, b.ID); !errors.Is(err, osadapter.ErrUnsupportedFormat) {
		t.Fatalf("restore: %v", err)
	}
}

// TestClockFile: the .rtc written by NextUI with the save is backed up and
// restored with it.
func TestClockFile(t *testing.T) {
	e := newEnv(t)
	g := e.game("Starlight Courier.gba")
	testutil.WriteFile(t, e.sd, "Saves/GBA/Starlight Courier.gba.rtc", "clock 1")
	b, err := e.svc.Create(g, nil)
	if err != nil {
		t.Fatal(err)
	}
	if e.read(backupsDir+b.ID+".rtc") != "clock 1" {
		t.Fatal("clock file not backed up")
	}
	// A new save keeps the clock file in place (backed up with the old save).
	testutil.WriteFile(t, e.sd, "Saves/GBA/Starlight Courier.gba.rtc", "clock 2")
	if _, err := e.svc.Replace(g, e.temp("new save")); err != nil {
		t.Fatal(err)
	}
	if e.read("Saves/GBA/Starlight Courier.gba.rtc") != "clock 2" {
		t.Fatal("clock file changed by a replace")
	}
	safety, err := e.svc.Restore(g, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if e.read("Saves/GBA/Starlight Courier.gba.rtc") != "clock 1" || e.read(backupsDir+safety.ID+".rtc") != "clock 2" {
		t.Fatal("clock file not restored")
	}
	// A damaged clock copy: nothing restored.
	os.WriteFile(filepath.Join(e.sd, backupsDir+b.ID+".rtc"), []byte("bad"), 0o644)
	if _, err := e.svc.Restore(g, b.ID); !errors.Is(err, ErrCorruptBackup) {
		t.Fatalf("damaged clock copy: %v", err)
	}
	// Deleting the backup moves its clock copy too.
	if err := e.svc.Delete(g, b.ID); err != nil {
		t.Fatal(err)
	}
	entries, _, _ := e.trash.List()
	if len(entries) != 1 || len(entries[0].Files) != 2 {
		t.Fatalf("trash: %+v", entries)
	}
}

func TestBackupLocked(t *testing.T) {
	e := newEnv(t)
	g := e.game("Starlight Courier.gba")
	old := e.read("Saves/GBA/Starlight Courier.gba.sav")
	b, err := e.svc.BackupLocked(g, ReasonBeforePatch)
	if err != nil || b == nil || b.Reason != ReasonBeforePatch || b.Label != nil {
		t.Fatalf("backup: %+v %v", b, err)
	}
	if e.read(backupsDir+b.ID+".sav") != old {
		t.Fatal("copy content")
	}
	// No save: nothing to back up.
	if b, err := e.svc.BackupLocked(e.game("Lantern Keeper [b1].gba"), ReasonBeforePatch); err != nil || b != nil {
		t.Fatalf("no save: %+v %v", b, err)
	}
	for _, reason := range []string{ReasonManual, "other"} {
		if _, err := e.svc.BackupLocked(g, reason); err == nil {
			t.Errorf("reason %q accepted", reason)
		}
	}
}
