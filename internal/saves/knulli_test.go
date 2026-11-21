package saves

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/confine"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/osadapter/knulli"
	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

// newKnulliEnv is newEnv on a copy of testdata/knulli in dir (M20).
func newKnulliEnv(t *testing.T, dir string) (*env, knulli.Adapter) {
	t.Helper()
	sd, devEnv := testutil.CopyKnulli(t, dir)
	roots := knulli.RootsForSD(sd)
	a := knulli.New(roots, func(k string) string { return devEnv[k] })
	layout := store.Layout{Root: roots.Data}
	clk := &clock{t: time.Date(2026, 11, 18, 17, 0, 0, 0, time.UTC)}
	trash := store.NewTrash(layout, sd, clk.Now, nil)
	svc, err := New(Options{Adapter: a, Layout: layout, Trash: trash, Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, sd: sd, a: a.Adapter, trash: trash, clk: clk, svc: svc}, a
}

func (e *env) knulliGame(rel string) osadapter.Game {
	e.t.Helper()
	g, err := e.a.Game(rel)
	if err != nil {
		e.t.Fatal(err)
	}
	return g
}

// Codex review of M20: the save folder of a system is a link to another
// folder of the volume. Nothing is written there and the save is not read
// through it (rule 2).
func TestKnulliSaveFolderThroughALink(t *testing.T) {
	e, _ := newKnulliEnv(t, t.TempDir())
	saves := filepath.Join(e.sd, "saves", "gba")
	outside := filepath.Join(filepath.Dir(e.sd), "elsewhere")
	if err := os.Rename(saves, outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, saves); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}
	before, _ := os.ReadDir(outside)
	g := e.knulliGame("gba/Starlight Courier.gba")
	if _, err := e.svc.Replace(g, e.temp("new save")); !errors.Is(err, confine.ErrOutside) {
		t.Fatalf("replace through a link: %v", err)
	}
	if _, _, err := e.svc.OpenSave(g); err == nil {
		t.Fatal("save read through a link")
	}
	if _, err := e.svc.Create(g, nil); !errors.Is(err, confine.ErrOutside) {
		t.Fatalf("backup through a link: %v", err)
	}
	// A game without a save there: no save is created through the link.
	pg := e.knulliGame("gba/Pixel Garden.gba")
	if _, err := e.svc.Replace(pg, e.temp("new save")); !errors.Is(err, confine.ErrOutside) {
		t.Fatalf("new save through a link: %v", err)
	}
	after, _ := os.ReadDir(outside)
	if len(after) != len(before) {
		t.Fatalf("files written through the link: %d -> %d", len(before), len(after))
	}
	if data, _ := os.ReadFile(filepath.Join(outside, "Starlight Courier.srm")); string(data) == "new save" {
		t.Fatal("save replaced through the link")
	}
}

// Codex review round 2: the saves root itself is a link; only the card
// root may be one. Nothing is read, listed or written through it.
func TestKnulliSavesRootThroughALink(t *testing.T) {
	e, _ := newKnulliEnv(t, t.TempDir())
	saves := filepath.Join(e.sd, "saves")
	outside := filepath.Join(filepath.Dir(e.sd), "elsewhere")
	if err := os.Rename(saves, outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, saves); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}
	g := e.knulliGame("gba/Starlight Courier.gba")
	if f, _, err := e.svc.OpenSave(g); err == nil {
		f.Close()
		t.Fatal("save read through the saves root link")
	}
	if _, err := e.svc.Create(g, nil); !errors.Is(err, confine.ErrOutside) {
		t.Fatalf("backup through the saves root link: %v", err)
	}
	if _, err := e.svc.Replace(g, e.temp("new")); !errors.Is(err, confine.ErrOutside) {
		t.Fatalf("replace through the saves root link: %v", err)
	}
	all, err := e.svc.ListAll(context.Background())
	if err != nil || len(all.Saves) != 0 {
		t.Fatalf("saves listed through the link: %+v %v", all.Saves, err)
	}
}

// Codex review of M20: on ext4, a restore writes the save and its clock
// under the names RetroArch reads (Game.srm, Game.rtc for Game.gba), with
// or without files in place; files of another case (another game's, or
// none RetroArch reads) are left as they are.
func TestKnulliRestoreExactCase(t *testing.T) {
	e, _ := newKnulliEnv(t, testutil.CaseSensitiveDir(t))
	g := e.knulliGame("gba/Starlight Courier.gba")
	dir := "saves/gba/"
	// A backup of the full pair.
	b, err := e.svc.Create(g, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := e.read(dir + "Starlight Courier.srm")
	wantRTC := e.read(dir + "Starlight Courier.rtc")
	// The pair is replaced by files of another case only.
	for _, ext := range []string{".srm", ".rtc"} {
		if err := os.Rename(filepath.Join(e.sd, dir+"Starlight Courier"+ext), filepath.Join(e.sd, dir+"starlight courier"+ext)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.Restore(g, b.ID); err != nil {
		t.Fatal(err)
	}
	if e.read(dir+"Starlight Courier.srm") != want || e.read(dir+"Starlight Courier.rtc") != wantRTC {
		t.Fatal("restore did not write the exact pair")
	}
	if !e.exists(dir+"starlight courier.srm") || !e.exists(dir+"starlight courier.rtc") {
		t.Fatal("the files of another case were touched")
	}
	// With the exact pair in place, the restore replaces it (backed up).
	if err := os.WriteFile(filepath.Join(e.sd, dir+"Starlight Courier.srm"), []byte("played"), 0o644); err != nil {
		t.Fatal(err)
	}
	safety, err := e.svc.Restore(g, b.ID)
	if err != nil || safety == nil {
		t.Fatalf("restore over the pair: %+v %v", safety, err)
	}
	if e.read(dir+"Starlight Courier.srm") != want {
		t.Fatal("restore over the pair")
	}
	// Without any file in place.
	for _, n := range []string{"Starlight Courier.srm", "Starlight Courier.rtc"} {
		if err := os.Remove(filepath.Join(e.sd, dir+n)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.Restore(g, b.ID); err != nil {
		t.Fatal(err)
	}
	if e.read(dir+"Starlight Courier.srm") != want || e.read(dir+"Starlight Courier.rtc") != wantRTC {
		t.Fatal("restore without files in place")
	}
}
