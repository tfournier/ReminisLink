package library

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/osadapter/knulli"
	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

// knulliLibrary is a library on a copy of testdata/knulli in dir (M20).
func knulliLibrary(t *testing.T, dir string) (*Library, *store.Trash, string) {
	t.Helper()
	sd, env := testutil.CopyKnulli(t, dir)
	roots := knulli.RootsForSD(sd)
	layout := store.Layout{Root: roots.Data}
	trash := store.NewTrash(layout, sd, time.Now, nil)
	return New(knulli.New(roots, func(k string) string { return env[k] }), layout), trash, sd
}

// The folder of the pak on Knulli (roms/tools/.ReminisLink) is hidden
// from the SD browser like the data folder (Codex review of M20).
func TestBrowseHidesTheKnulliPak(t *testing.T) {
	l, _, _ := knulliLibrary(t, t.TempDir())
	tools, err := l.Browse(SDEntryID("roms/tools"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(names(tools), ".ReminisLink") || !strings.Contains(names(tools), "fi:ReminisLink.sh") {
		t.Fatalf("tools: %s", names(tools))
	}
	for _, rel := range []string{"roms/tools/.ReminisLink", "roms/tools/.ReminisLink/reminislink"} {
		if _, err := l.Browse(SDEntryID(rel)); !errors.Is(err, ErrNotFound) {
			t.Errorf("browse %s: %v", rel, err)
		}
		if _, _, _, err := l.OpenSDEntry(SDEntryID(rel)); !errors.Is(err, ErrNotFound) {
			t.Errorf("open %s: %v", rel, err)
		}
	}
}

// Codex review of M20: on ext4, deleting Game.gba never takes the states,
// thumbnails, automatic state or save of game.gba.
func TestKnulliDeleteKeepsTheGameOfAnotherCase(t *testing.T) {
	l, trash, sd := knulliLibrary(t, testutil.CaseSensitiveDir(t))
	other := []string{
		"roms/gba/starlight courier.gba", "saves/gba/starlight courier.srm", "saves/gba/starlight courier.state1",
		"saves/gba/starlight courier.state1.png", "saves/gba/starlight courier.state.auto",
	}
	for _, rel := range other {
		writeSD(t, sd, rel, "other game")
	}
	preview, err := l.PreviewDeletion(EncodeID("gba/Starlight Courier.gba"), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range preview.Moved {
		if strings.HasPrefix(it.Name, "starlight") {
			t.Errorf("preview moves %s", it.Name)
		}
	}
	if _, err := l.DeleteGame(EncodeID("gba/Starlight Courier.gba"), false, trash); err != nil {
		t.Fatal(err)
	}
	for _, rel := range other {
		if _, err := os.Lstat(filepath.Join(sd, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(sd, "saves/gba/Starlight Courier.srm")); err == nil {
		t.Error("the save of the deleted game is still there")
	}
	// The other game is intact and still lists its files.
	d, err := l.Game(EncodeID("gba/starlight courier.gba"))
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]int{}
	for _, f := range d.Files {
		roles[f.Role]++
	}
	if roles["save"] != 1 || roles["state"] != 1 {
		t.Errorf("starlight courier.gba files: %+v", d.Files)
	}
}

// Two ROMs of the same name in two folders share one art file on Knulli:
// deleting one keeps it.
func TestKnulliDeleteKeepsASharedArt(t *testing.T) {
	l, trash, sd := knulliLibrary(t, t.TempDir())
	writeSD(t, sd, "roms/gba/Hacks/Starlight Courier.gba", "hack")
	if _, err := l.DeleteGame(EncodeID("gba/Hacks/Starlight Courier.gba"), true, trash); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(sd, "roms/gba/images/Starlight Courier-image.png")); err != nil {
		t.Fatalf("shared art moved: %v", err)
	}
}
