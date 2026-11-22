package nextui

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
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
// the relative path of the ROM (no line break, as write_text_file writes
// it), the .shortcut marker holding the display name and the art in
// <folder>/.media.
func writeROMShortcut(t *testing.T, sd, folder string) {
	t.Helper()
	writeShortcutFiles(t, sd, folder, folder+".m3u", "../01) Game Boy (GB)/Puddle Jumper.gb")
}

// writeShortcutFiles writes the marker and a playlist m3u holding content
// in Roms/<folder>.
func writeShortcutFiles(t *testing.T, sd, folder, m3u, content string) {
	t.Helper()
	roms := filepath.Join(sd, "Roms")
	testutil.WriteFile(t, roms, folder+"/"+m3u, content)
	testutil.WriteFile(t, roms, folder+"/"+ShortcutMarker, "Puddle Jumper")
	testutil.WriteFile(t, roms, folder+"/.media/bg.png", "png")
}

// libraryGames returns the sorted paths of the games of sys, failing
// unless each listing of the adapter (WalkGames, WalkSummaries, the
// ListFolder of its root folder and of its subfolders) gives the same
// paths and CountGames their number.
func libraryGames(t *testing.T, a *Adapter, sys osadapter.System) []string {
	t.Helper()
	ctx := context.Background()
	var walked, summarized, listed []string
	if err := a.WalkGames(ctx, sys, func(g osadapter.Game) error { walked = append(walked, g.Path); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := a.WalkSummaries(ctx, sys, func(_ string, s osadapter.GameSummary) error { summarized = append(summarized, s.Path); return nil }); err != nil {
		t.Fatal(err)
	}
	var list func(rel string)
	list = func(rel string) {
		l, err := a.ListFolder(sys, rel)
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range l.Games {
			listed = append(listed, g.Path)
		}
		for _, f := range l.Folders {
			list(f.Path)
		}
	}
	list(sys.Folder)
	count, err := a.CountGames(sys)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(walked)
	sort.Strings(summarized)
	sort.Strings(listed)
	if !slices.Equal(walked, summarized) || !slices.Equal(walked, listed) || count != len(walked) {
		t.Fatalf("%s: CountGames %d, WalkGames %q, WalkSummaries %q, ListFolder %q", sys.Folder, count, walked, summarized, listed)
	}
	return walked
}

// wantGames checks the games of the system folder (libraryGames) and that
// Game resolves each of them.
func wantGames(t *testing.T, a *Adapter, folder string, want ...string) {
	t.Helper()
	sys, err := a.System(folder)
	if err != nil {
		t.Fatalf("System(%q): %v", folder, err)
	}
	got := libraryGames(t, a, sys)
	want = slices.Clone(want)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Fatalf("%s: games %q, want %q", folder, got, want)
	}
	for _, rel := range want {
		if _, err := a.Game(rel); err != nil {
			t.Fatalf("Game(%q): %v", rel, err)
		}
	}
}

// wantNoSystem checks that a folder is neither a system nor listed.
func wantNoSystem(t *testing.T, a *Adapter, folder string) {
	t.Helper()
	systems, err := a.Systems()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range systems {
		if s.Folder == folder {
			t.Fatalf("listed as a system: %+v", s)
		}
	}
	if _, err := a.System(folder); !errors.Is(err, osadapter.ErrNotFound) {
		t.Fatalf("System(%q): %v", folder, err)
	}
	folders, err := a.SystemFolders()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range folders {
		if s.Folder == folder {
			if games := libraryGames(t, a, s); len(games) != 0 {
				t.Fatalf("%s: games %q", folder, games)
			}
		}
	}
}

// caseSensitive reports whether the file system of dir tells apart two
// names that differ by case only.
func caseSensitive(t *testing.T, dir string) bool {
	t.Helper()
	testutil.WriteFile(t, dir, "case-probe", "x")
	defer os.Remove(filepath.Join(dir, "case-probe"))
	_, err := os.Stat(filepath.Join(dir, "CASE-PROBE"))
	return errors.Is(err, fs.ErrNotExist)
}

// TestROMShortcutsAreNotGames checks that the ROM shortcuts of the
// Shortcuts pak are neither games nor systems of the library, whatever
// the sort prefix of their folder, while the original game stays listed;
// GameExists does not find them either.
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
			wantNoSystem(t, a, folder)
			if _, err := a.Game(folder + "/" + folder + ".m3u"); !errors.Is(err, osadapter.ErrNotFound) {
				t.Fatalf("Game() of a ROM shortcut: %v", err)
			}
			if a.GameExists("GB", folder+".m3u") {
				t.Fatal("GameExists found a ROM shortcut")
			}
			wantGames(t, a, "01) Game Boy (GB)", "01) Game Boy (GB)/Puddle Jumper.gb")
		})
	}
}

// TestROMShortcutFormats checks the content a shortcut playlist must have:
// the path of a multi-disc game and a final line break are shortcuts; a
// playlist of discs, a path into the folder itself, a second line, a
// comment, an absolute path or a path that does not leave the folder are
// not, and stay listed with the marker beside them.
func TestROMShortcutFormats(t *testing.T) {
	const folder = "Jeu (PS)"
	m3u := folder + "/" + folder + ".m3u"
	for _, c := range []struct {
		content string
		hidden  bool
	}{
		{"../Sony PlayStation (PS)/Quiet Harbor.m3u", true},
		{"../Sony PlayStation (PS)/Mossy Hollow/Mossy Hollow.m3u", true},
		{"../01) Game Boy (GB)/Puddle Jumper.gb\n", true},
		{"../01) Game Boy (GB)/Puddle Jumper.gb\r\n", true},
		{"Disc 1.chd", false},
		{"../Jeu (PS)/Disc 1.chd", false},
		{"../jeu (ps)/Disc 1.chd", false},
		{"../01) Game Boy (GB)/Puddle Jumper.gb\n../01) Game Boy (GB)/Other.gb", false},
		{"#EXTM3U\n../01) Game Boy (GB)/Puddle Jumper.gb", false},
		{"/mnt/SDCARD/Roms/01) Game Boy (GB)/Puddle Jumper.gb", false},
		{"../Puddle Jumper.gb", false},
		{"../01) Game Boy (GB)/../Puddle Jumper.gb", false},
		{"../01) Game Boy (GB)//Puddle Jumper.gb", false},
		{"..\\01) Game Boy (GB)\\Puddle Jumper.gb", false},
		{"", false},
	} {
		t.Run(c.content, func(t *testing.T) {
			a, sd := newAdapter(t)
			writeShortcutFiles(t, sd, folder, folder+".m3u", c.content)
			if c.hidden {
				wantNoSystem(t, a, folder)
				return
			}
			wantGames(t, a, folder, m3u)
		})
	}
}

// TestROMShortcutResidualMarker checks that a .shortcut left beside a real
// multi-disc playlist hides nothing: the playlist is a game, its discs are
// hidden as discs only, and GameExists finds it.
func TestROMShortcutResidualMarker(t *testing.T) {
	a, sd := newAdapter(t)
	roms := filepath.Join(sd, "Roms")
	const folder = "Jeu (PS)"
	writeShortcutFiles(t, sd, folder, folder+".m3u", "Disque 1.chd\nDisque 2.chd\n")
	testutil.WriteFile(t, roms, folder+"/Disque 1.chd", "cd1")
	testutil.WriteFile(t, roms, folder+"/Disque 2.chd", "cd2")
	wantGames(t, a, folder, folder+"/"+folder+".m3u")
	if !a.GameExists("PS", folder+".m3u") {
		t.Fatal("GameExists missed a real playlist beside a residual marker")
	}
}

// TestROMShortcutOnlyThatFile checks that only the shortcut .m3u is left
// out: a game stored in the same folder (even the very file the shortcut
// names) stays listed and keeps the system, and below the system folder,
// or without the marker, a .m3u named after its folder is a game as
// before.
func TestROMShortcutOnlyThatFile(t *testing.T) {
	a, sd := newAdapter(t)
	roms := filepath.Join(sd, "Roms")

	shared := "Puddle Jumper (GB)"
	writeShortcutFiles(t, sd, shared, shared+".m3u", "../01) Game Boy (GB)/Puddle Jumper.gb")
	testutil.WriteFile(t, roms, shared+"/Puddle Jumper.gb", "rom")
	testutil.WriteFile(t, roms, shared+"/Other Game.gb", "rom")

	plain := "Plain List (GB)" // a .m3u named after its folder, no marker
	testutil.WriteFile(t, roms, plain+"/"+plain+".m3u", "../01) Game Boy (GB)/Puddle Jumper.gb")

	deep := "Deep (GB)" // the marker one level down: not a shortcut folder
	testutil.WriteFile(t, roms, deep+"/Sub/Sub.m3u", "../../01) Game Boy (GB)/Puddle Jumper.gb")
	testutil.WriteFile(t, roms, deep+"/Sub/"+ShortcutMarker, "Sub")

	wantGames(t, a, shared, shared+"/Puddle Jumper.gb", shared+"/Other Game.gb")
	wantGames(t, a, plain, plain+"/"+plain+".m3u")
	wantGames(t, a, deep, deep+"/Sub")
	if _, err := a.Game(shared + "/" + shared + ".m3u"); !errors.Is(err, osadapter.ErrNotFound) {
		t.Fatalf("Game() of a ROM shortcut beside a game: %v", err)
	}
	if !a.GameExists("GB", "Puddle Jumper.gb") || !a.GameExists("GB", plain+".m3u") {
		t.Fatal("GameExists missed a game")
	}
}

// TestROMShortcutCase checks that the shortcut is the entry named exactly
// "<folder>.m3u": a playlist whose name differs by case is not one, and on
// a case-sensitive file system, where both exist, only the exact one is
// left out.
func TestROMShortcutCase(t *testing.T) {
	a, sd := newAdapter(t)
	roms := filepath.Join(sd, "Roms")
	const folder = "Puddle Jumper (GB)"
	writeShortcutFiles(t, sd, folder, "puddle jumper (gb).M3U", "../01) Game Boy (GB)/Puddle Jumper.gb")
	wantGames(t, a, folder, folder+"/puddle jumper (gb).M3U")

	if !caseSensitive(t, roms) {
		t.Log("case-insensitive file system: two names that differ by case only cannot both exist")
		return
	}
	testutil.WriteFile(t, roms, folder+"/"+folder+".m3u", "../01) Game Boy (GB)/Puddle Jumper.gb")
	wantGames(t, a, folder, folder+"/puddle jumper (gb).M3U")
	if _, err := a.Game(folder + "/" + folder + ".m3u"); !errors.Is(err, osadapter.ErrNotFound) {
		t.Fatalf("Game() of the exact shortcut: %v", err)
	}
	if !a.GameExists("GB", "puddle jumper (gb).M3U") {
		t.Fatal("GameExists missed the playlist of another case")
	}
}

// TestROMShortcutBesideALink checks that a folder holding a ROM shortcut
// and a symbolic link only is not a system: the listing skips links, and
// so does the check for an empty system.
func TestROMShortcutBesideALink(t *testing.T) {
	a, sd := newAdapter(t)
	roms := filepath.Join(sd, "Roms")
	const folder = "Puddle Jumper (GB)"
	writeROMShortcut(t, sd, folder)
	if err := os.Symlink(filepath.Join(roms, "01) Game Boy (GB)", "Puddle Jumper.gb"), filepath.Join(roms, folder, "Alias.gb")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	wantNoSystem(t, a, folder)
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
