package transfer

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter/knulli"
	"github.com/tfournier/ReminisLink/internal/saves"
	"github.com/tfournier/ReminisLink/internal/states"
	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

// newKnulliEnv is newEnv on a copy of testdata/knulli in dir (M20): saves
// are saves/<system>/<ROM name without extension>.srm and .rtc.
func newKnulliEnv(t *testing.T, dir string) *env {
	t.Helper()
	sd, devEnv := testutil.CopyKnulli(t, dir)
	roots := knulli.RootsForSD(sd)
	adapter := knulli.New(roots, func(k string) string { return devEnv[k] })
	layout := store.Layout{Root: roots.Data}
	clk := &testClock{t: time.Now().Truncate(time.Second)}
	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	free := int64(1 << 40)
	e := &env{t: t, sd: sd, clock: clk, free: &free, logs: logs, layout: layout}
	e.trash = store.NewTrash(layout, sd, clk.Now, logger)
	e.lib = library.New(adapter, layout)
	lock := &sync.Mutex{}
	sv, err := saves.New(saves.Options{Adapter: adapter, Layout: layout, Trash: e.trash, Lock: lock, Now: clk.Now, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	st, err := states.New(states.Options{Adapter: adapter, Layout: layout, Trash: e.trash, Lock: lock, Now: clk.Now, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	e.opts = Options{
		Library: e.lib, Adapter: adapter, Layout: layout, Trash: e.trash, Saves: sv, States: st, Lock: lock,
		FreeSpace: func(string) (int64, error) { return *e.free, nil }, Now: clk.Now, Logger: logger, ChunkSize: 1 << 10,
	}
	e.svc = e.newService()
	t.Cleanup(func() { e.svc.Close() })
	return e
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A Knulli save in the trash (Game.srm, Game.rtc: no ROM extension) is
// recognised as the save of its game and can replace the current one.
func TestKnulliSaveReplacesTheCurrentSave(t *testing.T) {
	e := newKnulliEnv(t, t.TempDir())
	srm, rtc := e.path("saves/gba/Starlight Courier.srm"), e.path("saves/gba/Starlight Courier.rtc")
	writeFile(t, srm, "old save")
	writeFile(t, rtc, "old clock")
	moved, err := e.trash.Move(store.ReasonOverwrite, "Starlight Courier", []store.MoveItem{{Path: srm}, {Path: rtc}})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, srm, "current save")
	writeFile(t, rtc, "current clock")
	p, err := e.svc.PreviewTrashRestore(moved.Entry.ID)
	if err != nil || !p.Replaceable || len(p.ReplaceDisplaced) != 2 {
		t.Fatalf("preview %+v %v", p, err)
	}
	res, apiErr, err := e.svc.RestoreTrash(moved.Entry.ID, store.RestoreReplace)
	if err != nil || apiErr != nil {
		t.Fatalf("restore %v %v", apiErr, err)
	}
	if e.read("saves/gba/Starlight Courier.srm") != "old save" || e.read("saves/gba/Starlight Courier.rtc") != "old clock" || res.ReplacedEntryID == nil {
		t.Fatalf("not replaced: %+v", res)
	}
}

// On ext4, game.gba and Game.gba are two games: the backup copy of one
// never replaces the save of the other, and a save of another case is
// no conflict for a direct restore.
func TestKnulliReplaceKeepsToItsGameOnExt4(t *testing.T) {
	e := newKnulliEnv(t, testutil.CaseSensitiveDir(t))
	writeFile(t, e.path("roms/gba/starlight courier.gba"), "other rom")
	writeFile(t, e.path("saves/gba/starlight courier.srm"), "other save")
	main := e.read("saves/gba/Starlight Courier.srm")
	g, err := e.opts.Adapter.Game("gba/starlight courier.gba")
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.opts.Saves.Create(g, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, e.path("saves/gba/starlight courier.srm"), "other save, newer")
	if err := e.opts.Saves.Delete(g, b.ID); err != nil {
		t.Fatal(err)
	}
	entries, _, _ := e.trash.List()
	if len(entries) != 1 {
		t.Fatalf("trash %+v", entries)
	}
	if _, apiErr, err := e.svc.RestoreTrash(entries[0].ID, store.RestoreReplace); err != nil || apiErr != nil {
		t.Fatalf("restore %v %v", apiErr, err)
	}
	if e.read("saves/gba/starlight courier.srm") != "other save" {
		t.Fatal("the save of its own game not replaced")
	}
	if e.read("saves/gba/Starlight Courier.srm") != main {
		t.Fatal("the save of the other game replaced")
	}

	// A direct restore of Game.srm while game.srm is there: no conflict.
	srm := e.path("saves/gba/Starlight Courier.srm")
	moved, err := e.trash.Move(store.ReasonOverwrite, "Starlight Courier", []store.MoveItem{{Path: srm}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.PreviewTrashRestore(moved.Entry.ID)
	if err != nil || p.Conflict {
		t.Fatalf("false conflict on ext4: %+v %v", p, err)
	}
	if _, apiErr, err := e.svc.RestoreTrash(moved.Entry.ID, store.RestoreFail); err != nil || apiErr != nil {
		t.Fatalf("restore %v %v", apiErr, err)
	}
	if e.read("saves/gba/Starlight Courier.srm") != main || e.read("saves/gba/starlight courier.srm") != "other save" {
		t.Fatal("files after the restore")
	}
}
