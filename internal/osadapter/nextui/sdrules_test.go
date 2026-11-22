package nextui

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

// TestShortcutFoldersAreNotSystems checks that the tool shortcuts of the
// Shortcuts pak (Roms/<name> (SHORTCUT)/<name> (SHORTCUT).m3u) are not
// listed as systems (M23).
func TestShortcutFoldersAreNotSystems(t *testing.T) {
	a, sd := newAdapter(t)
	folder := "\ufeffMy Tool (SHORTCUT)"
	testutil.WriteFile(t, filepath.Join(sd, "Roms"), folder+"/"+folder+".m3u", "../../Tools/tg5040/Foo.pak")
	testutil.WriteFile(t, filepath.Join(sd, "Roms"), folder+"/target", "/mnt/SDCARD/Tools/tg5040/Foo.pak")
	systems, err := a.Systems()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range systems {
		if s.Tag == ShortcutTag {
			t.Fatalf("shortcut listed as a system: %+v", s)
		}
	}
	if _, err := a.System(folder); err == nil {
		t.Fatal("System() accepted a shortcut folder")
	}
}

// writeROMShortcut writes what create_rom_shortcut of the Shortcuts pak
// (Helaas/nextui-shortcuts-pak, src/device.c) writes for the original ROM
// "01) Game Boy (GB)/Puddle Jumper.gb": Roms/<folder>/<folder>.m3u with
// the relative path of the ROM, the .shortcut marker holding the display
// name and the art in <folder>/.media.
func writeROMShortcut(t *testing.T, sd, folder string) {
	t.Helper()
	roms := filepath.Join(sd, "Roms")
	testutil.WriteFile(t, roms, folder+"/"+folder+".m3u", "../01) Game Boy (GB)/Puddle Jumper.gb")
	testutil.WriteFile(t, roms, folder+"/"+ShortcutMarker, "Puddle Jumper")
	testutil.WriteFile(t, roms, folder+"/.media/bg.png", "png")
}

// libraryGames returns the paths of the games of sys as each listing of
// the adapter gives them (CountGames, WalkGames, WalkSummaries and the
// ListFolder of its root folder), failing when they disagree.
func libraryGames(t *testing.T, a *Adapter, sys osadapter.System) []string {
	t.Helper()
	ctx := context.Background()
	count, err := a.CountGames(sys)
	if err != nil {
		t.Fatal(err)
	}
	var walked, summarized, listed []string
	if err := a.WalkGames(ctx, sys, func(g osadapter.Game) error { walked = append(walked, g.Path); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := a.WalkSummaries(ctx, sys, func(_ string, s osadapter.GameSummary) error { summarized = append(summarized, s.Path); return nil }); err != nil {
		t.Fatal(err)
	}
	l, err := a.ListFolder(sys, sys.Folder)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range l.Games {
		listed = append(listed, g.Path)
	}
	if count != len(walked) || len(walked) != len(summarized) || len(walked) != len(listed) {
		t.Fatalf("%s: CountGames %d, WalkGames %v, WalkSummaries %v, ListFolder %v", sys.Folder, count, walked, summarized, listed)
	}
	return walked
}

// TestROMShortcutsAreNotGames checks that the ROM shortcuts of the
// Shortcuts pak are neither games nor systems of the library, whatever
// the sort prefix of their folder, while the original game stays listed.
func TestROMShortcutsAreNotGames(t *testing.T) {
	for _, folder := range []string{
		"\ufeffPuddle Jumper (GB)",  // bottom of the list (SHORTCUT_PREFIX)
		"0) Puddle Jumper (GB)",     // top of the list (TOP_PREFIX)
		"Puddle Jumper (GB)",        // alphabetical
		"\u2605 Puddle Jumper (GB)", // legacy prefix
	} {
		t.Run(folder, func(t *testing.T) {
			a, sd := newAdapter(t)
			writeROMShortcut(t, sd, folder)
			systems, err := a.Systems()
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range systems {
				if s.Folder == folder {
					t.Fatalf("ROM shortcut listed as a system: %+v", s)
				}
			}
			folders, err := a.SystemFolders()
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range folders {
				if s.Folder == folder {
					if games := libraryGames(t, a, s); len(games) != 0 {
						t.Fatalf("ROM shortcut listed as games: %v", games)
					}
				}
			}
			if _, err := a.System(folder); !errors.Is(err, osadapter.ErrNotFound) {
				t.Fatalf("System() of a shortcut folder: %v", err)
			}
			if _, err := a.Game(folder + "/" + folder + ".m3u"); !errors.Is(err, osadapter.ErrNotFound) {
				t.Fatalf("Game() of a ROM shortcut: %v", err)
			}
			orig, err := a.Game("01) Game Boy (GB)/Puddle Jumper.gb")
			if err != nil {
				t.Fatalf("original game: %v", err)
			}
			if orig.Kind != osadapter.KindFile {
				t.Fatalf("original game %+v", orig)
			}
		})
	}
}

// TestROMShortcutOnlyThatFile checks that only the shortcut .m3u next to
// the marker is left out: a game stored in the same folder stays listed
// and keeps the system, and without the marker (or below the system
// folder) a .m3u named after its folder is a game as before.
func TestROMShortcutOnlyThatFile(t *testing.T) {
	a, sd := newAdapter(t)
	roms := filepath.Join(sd, "Roms")
	shared := "Puddle Jumper (GB)"
	writeROMShortcut(t, sd, shared)
	testutil.WriteFile(t, roms, shared+"/Other Game.gb", "rom")

	plain := "Plain List (GB)" // a .m3u named after its folder, no marker
	testutil.WriteFile(t, roms, plain+"/"+plain+".m3u", "Disc 1.gb")
	testutil.WriteFile(t, roms, plain+"/Disc 1.gb", "rom")

	deep := "Deep (GB)" // the marker one level down: not a shortcut folder
	testutil.WriteFile(t, roms, deep+"/Sub/Sub.m3u", "../../01) Game Boy (GB)/Puddle Jumper.gb")
	testutil.WriteFile(t, roms, deep+"/Sub/"+ShortcutMarker, "Sub")

	cases := map[string][]string{
		shared: {shared + "/Other Game.gb"},
		plain:  {plain + "/" + plain + ".m3u"},
		deep:   {deep + "/Sub"},
	}
	for folder, want := range cases {
		sys, err := a.System(folder)
		if err != nil {
			t.Fatalf("%s: %v", folder, err)
		}
		got := libraryGames(t, a, sys)
		if len(got) != len(want) || got[0] != want[0] {
			t.Fatalf("%s: games %v, want %v", folder, got, want)
		}
		for _, rel := range want {
			if _, err := a.Game(rel); err != nil {
				t.Fatalf("Game(%q): %v", rel, err)
			}
		}
	}
	if _, err := a.Game(shared + "/" + shared + ".m3u"); !errors.Is(err, osadapter.ErrNotFound) {
		t.Fatalf("Game() of a ROM shortcut beside a game: %v", err)
	}
}

// TestDefaultSDRules checks the NextUI protected places.
func TestDefaultSDRules(t *testing.T) {
	a, sd := newAdapter(t)
	r := a.SDRules()
	if !r.HiddenRootSystem || len(r.System) != 0 {
		t.Fatalf("rules %+v", r)
	}
	want := map[string]bool{}
	for _, p := range []string{"Roms", "Saves", "Cheats", "Bios", "Tools", "Emus", "Tools/tg5040", "Emus/tg5040", "Screenshots"} {
		want[filepath.Join(sd, p)] = true
	}
	for _, p := range r.Roots {
		delete(want, p)
	}
	if len(want) != 0 {
		t.Fatalf("missing roots %v in %v", want, r.Roots)
	}
}
