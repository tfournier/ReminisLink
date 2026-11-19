package library

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

func TestFolderDirAndRelToRoms(t *testing.T) {
	l, sd := newLibrary(t)
	gba := EncodeID("Game Boy Advance (GBA)")
	dir, sys, err := l.FolderDir(gba, "")
	if err != nil || sys.Tag != "GBA" || dir != filepath.Join(sd, "Roms", "Game Boy Advance (GBA)") {
		t.Fatalf("root: %q %+v %v", dir, sys, err)
	}
	dir, _, err = l.FolderDir(gba, EncodeID("Game Boy Advance (GBA)/Hacks"))
	if err != nil || filepath.Base(dir) != "Hacks" {
		t.Fatalf("subfolder: %q %v", dir, err)
	}
	if rel, err := l.RelToRoms(filepath.Join(dir, "x.gba")); err != nil || rel != "Game Boy Advance (GBA)/Hacks/x.gba" {
		t.Fatalf("RelToRoms: %q %v", rel, err)
	}
	if _, err := l.RelToRoms(sd); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("outside: %v", err)
	}
	cases := map[string][2]string{
		"bad system":    {"Li4", ""},
		"other system":  {gba, EncodeID("Sony PlayStation (PS)/Mossy Hollow")},
		"traversal":     {gba, EncodeID("Game Boy Advance (GBA)/../x")},
		"missing":       {EncodeID("Nope (NO)"), ""},
		"game folder":   {EncodeID("Sony PlayStation (PS)"), EncodeID("Sony PlayStation (PS)/Mossy Hollow")},
		"missing child": {gba, EncodeID("Game Boy Advance (GBA)/Nope")},
	}
	for name, c := range cases {
		if _, _, err := l.FolderDir(c[0], c[1]); !errors.Is(err, ErrInvalidID) && !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDeleteGameErrors(t *testing.T) {
	l, sd := newLibrary(t)
	tr := store.NewTrash(store.Layout{Root: filepath.Join(sd, ".userdata", "shared", "ReminisLink")}, sd, nil, nil)
	if _, err := l.DeleteGame("Li4vZXRj", false, tr); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("bad id: %v", err)
	}
	if _, err := l.DeleteGame(EncodeID("Game Boy Advance (GBA)/Nope.gba"), false, tr); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	res, err := l.DeleteGame(EncodeID("Game Boy Advance (GBA)/Hacks/Deeper/Tiny Trial.gba"), false, tr)
	if err != nil || len(res.Moved) != 1 || res.Moved[0] != "Roms/Game Boy Advance (GBA)/Hacks/Deeper/Tiny Trial.gba" {
		t.Fatalf("delete: %+v %v", res, err)
	}
}

// Codex review of M4: two playlists share a disc; deleting one keeps the
// shared disc in place (the other stays playable, the purge never erases
// it), the other discs go to the trash.
func TestDeleteGameKeepsSharedDiscs(t *testing.T) {
	l, sd := newLibrary(t)
	tr := store.NewTrash(store.Layout{Root: filepath.Join(sd, ".userdata", "shared", "ReminisLink")}, sd, nil, nil)
	ps := filepath.Join(sd, "Roms", "Sony PlayStation (PS)")
	testutil.WriteFile(t, ps, "Quiet Harbor Remix.m3u", "Quiet Harbor (Disc 2).cue\n")
	res, err := l.DeleteGame(EncodeID("Sony PlayStation (PS)/Quiet Harbor.m3u"), false, tr)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.Moved {
		if strings.Contains(m, "Disc 2") {
			t.Fatalf("shared disc moved: %v", res.Moved)
		}
	}
	if _, err := os.Stat(filepath.Join(ps, "Quiet Harbor (Disc 2).cue")); err != nil {
		t.Fatal("shared disc gone")
	}
	if _, err := os.Stat(filepath.Join(ps, "Quiet Harbor (Disc 1).cue")); !os.IsNotExist(err) {
		t.Fatal("own disc kept")
	}
	if _, err := l.Game(EncodeID("Sony PlayStation (PS)/Quiet Harbor Remix.m3u")); err != nil {
		t.Fatalf("the other playlist: %v", err)
	}
	// A multi-disc folder sharing a disc keeps it: its other files move.
	testutil.WriteFile(t, ps, "Other.m3u", "Mossy Hollow/Mossy Hollow (Disc 1).cue\n")
	res, err = l.DeleteGame(EncodeID("Sony PlayStation (PS)/Mossy Hollow"), false, tr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ps, "Mossy Hollow", "Mossy Hollow (Disc 1).cue")); err != nil {
		t.Fatalf("shared disc of a folder moved: %v", res.Moved)
	}
	if _, err := os.Stat(filepath.Join(ps, "Mossy Hollow", "Mossy Hollow (Disc 1).bin")); err != nil {
		t.Fatalf("track of the shared disc moved: %v", res.Moved)
	}
	if _, err := os.Stat(filepath.Join(ps, "Mossy Hollow", "Mossy Hollow (Disc 2).bin")); !os.IsNotExist(err) {
		t.Fatalf("track of an own disc kept: %v", res.Moved)
	}
	if _, err := os.Stat(filepath.Join(ps, "Mossy Hollow", "Mossy Hollow.m3u")); !os.IsNotExist(err) {
		t.Fatalf("playlist of the folder kept: %v", res.Moved)
	}
}

// Codex review of M5: a folder holding <Game>.cue (and no <Game>.m3u) is a
// game, as NextUI launches it, named after the .cue for its data; with a
// .m3u too, the data follows the .m3u.
func TestFolderCueGame(t *testing.T) {
	l, sd := newLibrary(t)
	ps := filepath.Join(sd, "Roms", "Sony PlayStation (PS)")
	testutil.WriteFile(t, ps, "Tide Runner/Tide Runner.cue", `FILE "Tide Runner.bin" BINARY`)
	testutil.WriteFile(t, ps, "Tide Runner/Tide Runner.bin", "bin")
	testutil.WriteFile(t, sd, "Saves/PS/Tide Runner.cue.sav", "save")
	testutil.WriteFile(t, sd, ".userdata/shared/PS-pcsx_rearmed/Tide Runner.cue.st1", "state")
	listing, err := l.ListGames(EncodeID("Sony PlayStation (PS)"), "")
	if err != nil {
		t.Fatal(err)
	}
	var tide *Game
	for i, g := range listing.Games {
		if g.Name == "Tide Runner" {
			tide = &listing.Games[i]
		}
	}
	for _, f := range listing.Folders {
		if f.Name == "Tide Runner" {
			t.Fatal("listed as a folder")
		}
	}
	if tide == nil || tide.Kind != "folder-cue" || !tide.HasSave || tide.StateCount != 1 {
		t.Fatalf("folder-cue game: %+v", tide)
	}
	d, err := l.Game(tide.ID)
	if err != nil || d.Kind != "folder-cue" || len(d.Files) != 4 {
		t.Fatalf("detail: %+v %v", d, err)
	}
	// With a .m3u of the same name, the data is named after the .m3u.
	testutil.WriteFile(t, ps, "Tide Runner/Tide Runner.m3u", "Tide Runner.cue\n")
	g, err := l.GameByID(tide.ID)
	if err != nil || g.Kind != "folder-m3u" || g.ROMName != "Tide Runner.m3u" {
		t.Fatalf("with a .m3u: %+v %v", g, err)
	}
	// A file inside the folder is not a game of its own.
	if _, err := l.Game(EncodeID("Sony PlayStation (PS)/Tide Runner/Tide Runner.bin")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("file inside a cue folder: %v", err)
	}
}
