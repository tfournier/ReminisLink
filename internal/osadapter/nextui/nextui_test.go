package nextui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

const (
	gba  = "Game Boy Advance (GBA)"
	mgba = "Game Boy Advance (MGBA)"
	gb   = "01) Game Boy (GB)"
	ps   = "Sony PlayStation (PS)"
)

func newAdapter(t *testing.T) (*Adapter, string) {
	t.Helper()
	sd := testutil.CopySDCard(t)
	env := map[string]string{"DEVICE": "brick", "PLATFORM": "tg5040"}
	return New(RootsForSD(sd, "tg5040"), func(k string) string { return env[k] }), sd
}

// TestStateFolders checks how the state folder of a tag is found: the core
// of the extra pak, then of the system pak, then a single <TAG>-* folder.
func TestStateFolders(t *testing.T) {
	a, sd := newAdapter(t)
	shared := filepath.Join(sd, ".userdata", "shared")
	game := func(tag string) osadapter.Game {
		return osadapter.Game{System: osadapter.System{Tag: tag}, ROMName: "x.bin"}
	}
	cases := map[string]string{
		"GBA":  filepath.Join(shared, "GBA-gpsp", "x.bin.st0"),        // system pak
		"MGBA": filepath.Join(shared, "MGBA-mgba", "x.bin.st0"),       // extra pak
		"GB":   filepath.Join(shared, "GB-gambatte", "x.bin.st0"),     // pak, no folder yet
		"PS":   filepath.Join(shared, "PS-pcsx_rearmed", "x.bin.st0"), // system pak
		"NOPE": "",                                                    // no pak, no folder
	}
	for tag, want := range cases {
		if got := a.StatePath(game(tag), 0); got != want {
			t.Errorf("%s: StatePath = %q, want %q", tag, got, want)
		}
	}
	// No pak: a single <TAG>-<core> folder is used; two are ambiguous.
	testutil.WriteFile(t, shared, "SFC-snes9x/a.sfc.st0", "s")
	if got := a.StatePath(game("SFC"), 1); got != filepath.Join(shared, "SFC-snes9x", "x.bin.st1") {
		t.Errorf("fallback: %q", got)
	}
	testutil.WriteFile(t, shared, "SFC-other/a.sfc.st0", "s")
	if got := a.StatePath(game("SFC"), 1); got != "" {
		t.Errorf("ambiguous: %q", got)
	}
	// Quoted EMU_EXE; a pak without a readable core hides the extra pak.
	testutil.WriteFile(t, sd, ".system/tg5040/paks/Emus/MD.pak/launch.sh", "#!/bin/sh\nEMU_EXE=\"picodrive\"\n")
	if got := a.StatePath(game("MD"), 0); got != filepath.Join(shared, "MD-picodrive", "x.bin.st0") {
		t.Errorf("quoted: %q", got)
	}
	// The unreadable core of an extra pak: no other pak is looked at, and
	// the existing folder (of the former core) is never guessed (Codex
	// review of M5): the states of the tag are suspended.
	testutil.WriteFile(t, sd, "Emus/tg5040/MGBA.pak/launch.sh", "#!/bin/sh\nEMU_EXE=$CORE\n")
	if got := a.StatePath(game("MGBA"), 0); got != "" {
		t.Errorf("unreadable core guessed the folder of another core: %q", got)
	}
	if err := a.CheckStateFormat(game("MGBA")); err != nil {
		t.Errorf("format: %v", err)
	}
	// An extra pak wins over the system pak of the same tag (getEmuPath).
	testutil.WriteFile(t, sd, "Emus/tg5040/GBA.pak/launch.sh", "#!/bin/sh\nEMU_EXE=mgba\n")
	if got := a.StatePath(game("GBA"), 0); got != filepath.Join(shared, "GBA-mgba", "x.bin.st0") {
		t.Errorf("extra pak first: %q", got)
	}
	os.Remove(filepath.Join(sd, "Emus", "tg5040", "GBA.pak", "launch.sh"))
	// StateFiles only lists the folder of the current core of each tag.
	refs, err := a.StateFiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, r := range refs {
		seen[r.Tag]++
	}
	if seen["GBA"] != 4 || seen["MGBA"] != 0 || seen["PS"] != 1 || seen["SFC"] != 0 {
		t.Errorf("StateFiles: %v", seen)
	}
}

// TestFormats checks minuisettings.txt: only the default formats are
// handled.
func TestFormats(t *testing.T) {
	a, sd := newAdapter(t)
	g := osadapter.Game{System: osadapter.System{Tag: "GBA"}, ROMName: "Game.gba"}
	zip := osadapter.Game{System: osadapter.System{Tag: "GBA"}, ROMName: "Game.ZIP"}
	if a.CheckSaveFormat(g) != nil || a.CheckStateFormat(g) != nil || a.CheckSaveFormat(zip) != nil {
		t.Fatal("default formats refused")
	}
	testutil.WriteFile(t, sd, ".userdata/shared/minuisettings.txt", "saveFormat=1\r\nstateFormat=3\r\n")
	if err := a.CheckSaveFormat(g); !errors.Is(err, osadapter.ErrUnsupportedFormat) || !strings.Contains(err.Error(), "saveFormat=1") {
		t.Errorf("save format: %v", err)
	}
	if err := a.CheckStateFormat(g); !errors.Is(err, osadapter.ErrUnsupportedFormat) {
		t.Errorf("state format: %v", err)
	}
	if _, ok := a.ResumeSlot(); ok {
		t.Error("resume slot with a .state.auto format")
	}
	// useExtractedFileName=1: only the zipped games are refused.
	testutil.WriteFile(t, sd, ".userdata/shared/minuisettings.txt", "useExtractedFileName=1\n")
	if a.CheckSaveFormat(g) != nil || a.CheckStateFormat(g) != nil {
		t.Error("useExtractedFileName refuses a plain ROM")
	}
	for _, check := range []func(osadapter.Game) error{a.CheckSaveFormat, a.CheckStateFormat} {
		if err := check(zip); !errors.Is(err, osadapter.ErrUnsupportedFormat) || !strings.Contains(err.Error(), "useExtractedFileName=1") {
			t.Errorf("zip with useExtractedFileName: %v", err)
		}
	}
	os.Remove(filepath.Join(sd, ".userdata", "shared", "minuisettings.txt"))
	if a.CheckSaveFormat(g) != nil || a.CheckStateFormat(zip) != nil {
		t.Error("no settings file: defaults")
	}
}

func TestSideFilesAndGameExists(t *testing.T) {
	a, sd := newAdapter(t)
	testutil.WriteFile(t, sd, "Saves/GBA/Starlight Courier.gba.rtc", "clock")
	testutil.WriteFile(t, sd, ".userdata/shared/.minui/GBA/Starlight Courier.gba.0.txt", "disc")
	testutil.WriteFile(t, sd, ".userdata/shared/.minui/GBA/Starlight Courier.gba.txt", "0")
	testutil.WriteFile(t, sd, ".userdata/shared/.minui/GBA/Starlight Courier Remix.gba.1.bmp", "other game")
	g, _ := a.Game(gba + "/Starlight Courier.gba")
	files, err := a.GameFiles(g)
	if err != nil || files.RTC == nil || files.RTC.Rel != "Saves/GBA/Starlight Courier.gba.rtc" {
		t.Fatalf("rtc: %+v %v", files.RTC, err)
	}
	var side []string
	for _, f := range files.StateSide {
		side = append(side, filepath.Base(f.Path))
	}
	if want := []string{"Starlight Courier.gba.0.bmp", "Starlight Courier.gba.0.txt", "Starlight Courier.gba.txt"}; !reflect.DeepEqual(side, want) {
		t.Errorf("side files %q", side)
	}
	if got := a.StateSideFiles(g, 3); len(got) != 2 || filepath.Base(got[0]) != "Starlight Courier.gba.3.bmp" || filepath.Base(got[1]) != "Starlight Courier.gba.3.txt" {
		t.Errorf("StateSideFiles %q", got)
	}
	for _, c := range []struct {
		tag, rom string
		want     bool
	}{
		{"GBA", "starlight courier.gba", true},
		{"GBA", "Tiny Trial.gba", true},   // two levels down
		{"PS", "Mossy Hollow.m3u", true},  // inside its multi-disc folder
		{"MGBA", "Tiny Trial.gba", false}, // another tag
		{"GBA", "Gone Game.gba", false},
		{"GBA", ".hidden.gba", false},
	} {
		if got := a.GameExists(c.tag, c.rom); got != c.want {
			t.Errorf("GameExists(%s, %s) = %v", c.tag, c.rom, got)
		}
	}
}

func TestRootsFromEnv(t *testing.T) {
	r := RootsFromEnv(func(string) string { return "" })
	want := osadapter.Roots{
		SD:       "/mnt/SDCARD",
		Roms:     "/mnt/SDCARD/Roms",
		Saves:    "/mnt/SDCARD/Saves",
		States:   "/mnt/SDCARD/.userdata/shared",
		Minui:    "/mnt/SDCARD/.userdata/shared/.minui",
		System:   "/mnt/SDCARD/.system/tg5040",
		Emus:     "/mnt/SDCARD/Emus/tg5040",
		Cheats:   "/mnt/SDCARD/Cheats",
		Userdata: "/mnt/SDCARD/.userdata/tg5040",
		Shared:   "/mnt/SDCARD/.userdata/shared",
		Data:     "/mnt/SDCARD/.userdata/shared/ReminisLink",
		Logs:     "/mnt/SDCARD/.userdata/tg5040/logs",
	}
	if runtime.GOOS != "windows" && r != want {
		t.Errorf("defaults:\n got %+v\nwant %+v", r, want)
	}
	env := map[string]string{
		"SDCARD_PATH": "/sd", "ROMS_PATH": "/r", "SAVES_PATH": "/s", "CHEATS_PATH": "/c",
		"USERDATA_PATH": "/u", "SHARED_USERDATA_PATH": "/sh", "LOGS_PATH": "/l", "PLATFORM": "tg5050",
	}
	r = RootsFromEnv(func(k string) string { return env[k] })
	if r.Roms != "/r" || r.Saves != "/s" || r.Cheats != "/c" || r.Userdata != "/u" ||
		r.States != "/sh" || r.Minui != "/sh/.minui" || r.Emus != "/sd/Emus/tg5050" || r.System != "/sd/.system/tg5050" || r.Data != "/sh/ReminisLink" || r.Logs != "/l" {
		t.Errorf("from env: %+v", r)
	}
	r = RootsForSD("/card", "")
	if r.Roms != "/card/Roms" || r.Userdata != "/card/.userdata/tg5040" {
		t.Errorf("for SD: %+v", r)
	}
}

func TestSystems(t *testing.T) {
	a, _ := newAdapter(t)
	systems, err := a.Systems()
	if err != nil {
		t.Fatal(err)
	}
	want := []osadapter.System{
		{Folder: gb, Label: "Game Boy", Tag: "GB"},
		{Folder: gba, Label: "Game Boy Advance", Tag: "GBA"},
		{Folder: mgba, Label: "Game Boy Advance", Tag: "MGBA"},
		{Folder: ps, Label: "Sony PlayStation", Tag: "PS"},
	}
	if !reflect.DeepEqual(systems, want) {
		t.Fatalf("systems:\n got %+v\nwant %+v", systems, want)
	}
	for _, folder := range []string{"No Tag Folder", "Hidden Only (HID)", "Missing (X)", ".hidden (H)", "a/b (B)", ""} {
		if _, err := a.System(folder); !errors.Is(err, osadapter.ErrNotFound) {
			t.Errorf("System(%q) = %v, want ErrNotFound", folder, err)
		}
	}
	if sys, err := a.System(gba); err != nil || sys.Tag != "GBA" {
		t.Errorf("System(gba) = %+v, %v", sys, err)
	}
}

func TestSystemsMissingRoms(t *testing.T) {
	a := New(RootsForSD(t.TempDir(), ""), nil)
	systems, err := a.Systems()
	if err != nil || len(systems) != 0 {
		t.Fatalf("got %v, %v", systems, err)
	}
}

func TestParseSystemLabels(t *testing.T) {
	for folder, label := range map[string]string{
		"01) Game Boy (GB)":   "Game Boy",
		"02_Nintendo (NES)":   "Nintendo",
		"10 - Arcade (FBN)":   "Arcade",
		"(GBA)":               "(GBA)",
		"Mega Drive (MD) ":    "Mega Drive",
		"Pico (PICO-8) (P8)":  "Pico (PICO-8)",
		"Café Système (CAFÉ)": "Café Système",
	} {
		sys, ok := parseSystem(folder)
		if !ok || sys.Label != label {
			t.Errorf("parseSystem(%q) = %+v, %v; want label %q", folder, sys, ok, label)
		}
	}
	for _, folder := range []string{"Tools", "Empty ()", ".Hidden (H)", "Dot (.)", "Dots (..)", "Space ( GBA)", "Space (GBA )", "Blank ( )"} {
		if _, ok := parseSystem(folder); ok {
			t.Errorf("parseSystem(%q) accepted", folder)
		}
	}
}

func TestCountGames(t *testing.T) {
	a, _ := newAdapter(t)
	for folder, want := range map[string]int{gba: 6, mgba: 1, gb: 1, ps: 2} {
		sys, err := a.System(folder)
		if err != nil {
			t.Fatal(err)
		}
		n, err := a.CountGames(sys)
		if err != nil || n != want {
			t.Errorf("CountGames(%s) = %d, %v; want %d", folder, n, err, want)
		}
	}
}

func names(l osadapter.Listing) (folders, games []string) {
	for _, f := range l.Folders {
		folders = append(folders, f.Name)
	}
	for _, g := range l.Games {
		games = append(games, g.FileName)
	}
	return folders, games
}

func TestListFolderRoot(t *testing.T) {
	a, _ := newAdapter(t)
	sys, _ := a.System(gba)
	l, err := a.ListFolder(sys, gba)
	if err != nil {
		t.Fatal(err)
	}
	folders, games := names(l)
	if !reflect.DeepEqual(folders, []string{"Hacks"}) {
		t.Errorf("folders = %q", folders)
	}
	wantGames := []string{"Café Rêverie (France).gba", "Lantern Keeper [b1].gba", "Pixel Garden 🌱.gba", "Starlight Courier.gba"}
	if !reflect.DeepEqual(games, wantGames) {
		t.Errorf("games = %q", games)
	}
	if l.Folders[0].GameCount != 2 || l.Folders[0].Path != gba+"/Hacks" {
		t.Errorf("Hacks = %+v", l.Folders[0])
	}
	byName := map[string]osadapter.GameSummary{}
	for _, g := range l.Games {
		byName[g.FileName] = g
	}
	sc := byName["Starlight Courier.gba"]
	if sc.Name != "Starlight Courier" || sc.Kind != osadapter.KindFile || sc.Size != int64(len("fake-rom-1")) ||
		!sc.HasSave || sc.StateCount != 3 || !sc.HasArt || sc.CheatFile == "" || sc.ModTime.IsZero() ||
		sc.Path != gba+"/Starlight Courier.gba" || sc.ROMName != "Starlight Courier.gba" {
		t.Errorf("Starlight Courier = %+v", sc)
	}
	// The save of this one has a different case on the card.
	cafe := byName["Café Rêverie (France).gba"]
	if !cafe.HasSave || !cafe.HasArt || cafe.StateCount != 0 || cafe.CheatFile != "" {
		t.Errorf("Café = %+v", cafe)
	}
	pixel := byName["Pixel Garden 🌱.gba"]
	if pixel.HasSave || pixel.StateCount != 1 || pixel.CheatFile == "" || pixel.HasArt {
		t.Errorf("Pixel Garden = %+v", pixel)
	}
}

func TestListFolderSameGameOtherTag(t *testing.T) {
	a, _ := newAdapter(t)
	sys, _ := a.System(mgba)
	l, err := a.ListFolder(sys, mgba)
	if err != nil || len(l.Games) != 1 {
		t.Fatalf("%+v, %v", l, err)
	}
	g := l.Games[0]
	// MGBA has its own save and one state, no cheats nor art.
	if !g.HasSave || g.StateCount != 1 || g.CheatFile != "" || g.HasArt {
		t.Errorf("MGBA game = %+v", g)
	}
}

func TestListFolderSubfolders(t *testing.T) {
	a, _ := newAdapter(t)
	sys, _ := a.System(gba)
	l, err := a.ListFolder(sys, gba+"/Hacks")
	if err != nil {
		t.Fatal(err)
	}
	folders, games := names(l)
	if !reflect.DeepEqual(folders, []string{"Deeper"}) || !reflect.DeepEqual(games, []string{"Starlight Courier Remix.gba"}) {
		t.Fatalf("folders %q games %q", folders, games)
	}
	if g := l.Games[0]; !g.HasSave || !g.HasArt || g.Path != gba+"/Hacks/Starlight Courier Remix.gba" {
		t.Errorf("remix = %+v", g)
	}
	l, err = a.ListFolder(sys, gba+"/Hacks/Deeper")
	if err != nil || len(l.Games) != 1 || l.Games[0].Name != "Tiny Trial" {
		t.Fatalf("deeper = %+v, %v", l, err)
	}
}

func TestListFolderMultiDisc(t *testing.T) {
	a, _ := newAdapter(t)
	sys, _ := a.System(ps)
	l, err := a.ListFolder(sys, ps)
	if err != nil {
		t.Fatal(err)
	}
	folders, games := names(l)
	if len(folders) != 0 {
		t.Errorf("folders = %q (the multi-disc folder is a game)", folders)
	}
	// The discs listed by Quiet Harbor.m3u are part of that game.
	want := []string{"Mossy Hollow", "Quiet Harbor.m3u"}
	if !reflect.DeepEqual(games, want) {
		t.Fatalf("games = %q", games)
	}
	mossy := l.Games[0]
	wantSize := int64(len("Mossy Hollow (Disc 1).cue\nMossy Hollow (Disc 2).cue\n") +
		2*len(`FILE "Mossy Hollow (Disc 1).bin" BINARY`) + 2*len("fake-disc-1"))
	if mossy.Kind != osadapter.KindFolderM3U || mossy.ROMName != "Mossy Hollow.m3u" || mossy.Size != wantSize ||
		!mossy.HasSave || mossy.StateCount != 1 || !mossy.HasArt {
		t.Errorf("Mossy Hollow = %+v (want size %d)", mossy, wantSize)
	}
	quiet := l.Games[1]
	quietSize := int64(len("Quiet Harbor (Disc 1).cue\nQuiet Harbor (Disc 2).cue\n") + 2*len(`FILE "Quiet Harbor (Disc 1).bin" BINARY`))
	if quiet.Kind != osadapter.KindM3U || quiet.Name != "Quiet Harbor" || quiet.Size != quietSize {
		t.Errorf("Quiet Harbor = %+v (want size %d)", quiet, quietSize)
	}
	if _, err := a.Game(ps + "/Quiet Harbor (Disc 1).cue"); !errors.Is(err, osadapter.ErrNotFound) {
		t.Errorf("a disc of a .m3u game is a game: %v", err)
	}
	// The game folder cannot be listed as a folder.
	if _, err := a.ListFolder(sys, ps+"/Mossy Hollow"); !errors.Is(err, osadapter.ErrNotFound) {
		t.Errorf("list game folder: %v", err)
	}
}

func TestListFolderRejects(t *testing.T) {
	a, _ := newAdapter(t)
	sys, _ := a.System(gba)
	for _, rel := range []string{mgba, gba + "/.media", gba + "/Missing", gba + "/Starlight Courier.gba", gba + "x", ""} {
		if _, err := a.ListFolder(sys, rel); !errors.Is(err, osadapter.ErrNotFound) {
			t.Errorf("ListFolder(%q) = %v, want ErrNotFound", rel, err)
		}
	}
}

func TestListFolderSkipsSymlinks(t *testing.T) {
	a, sd := newAdapter(t)
	outside := t.TempDir()
	testutil.WriteFile(t, outside, "Secret.gba", "x")
	root := filepath.Join(sd, "Roms", gba)
	if err := os.Symlink(outside, filepath.Join(root, "Linked")); err != nil {
		t.Skip("symlinks not supported:", err)
	}
	if err := os.Symlink(filepath.Join(outside, "Secret.gba"), filepath.Join(root, "Linked.gba")); err != nil {
		t.Fatal(err)
	}
	sys, _ := a.System(gba)
	l, err := a.ListFolder(sys, gba)
	if err != nil {
		t.Fatal(err)
	}
	folders, games := names(l)
	for _, n := range append(folders, games...) {
		if n == "Linked" || n == "Linked.gba" {
			t.Errorf("symlink %q listed", n)
		}
	}
	if _, err := a.Game(gba + "/Linked.gba"); !errors.Is(err, osadapter.ErrNotFound) {
		t.Errorf("Game(symlink) = %v", err)
	}
}

func TestGame(t *testing.T) {
	a, _ := newAdapter(t)
	for rel, want := range map[string]osadapter.Game{
		gba + "/Starlight Courier.gba": {Path: gba + "/Starlight Courier.gba", FileName: "Starlight Courier.gba",
			Name: "Starlight Courier", Kind: osadapter.KindFile, ROMName: "Starlight Courier.gba"},
		ps + "/Mossy Hollow": {Path: ps + "/Mossy Hollow", FileName: "Mossy Hollow", Name: "Mossy Hollow",
			Kind: osadapter.KindFolderM3U, ROMName: "Mossy Hollow.m3u"},
		ps + "/Quiet Harbor.m3u": {Path: ps + "/Quiet Harbor.m3u", FileName: "Quiet Harbor.m3u", Name: "Quiet Harbor",
			Kind: osadapter.KindM3U, ROMName: "Quiet Harbor.m3u"},
		gba + "/Hacks/Deeper/Tiny Trial.gba": {Path: gba + "/Hacks/Deeper/Tiny Trial.gba", FileName: "Tiny Trial.gba",
			Name: "Tiny Trial", Kind: osadapter.KindFile, ROMName: "Tiny Trial.gba"},
	} {
		g, err := a.Game(rel)
		if err != nil {
			t.Errorf("Game(%q): %v", rel, err)
			continue
		}
		g.System = osadapter.System{}
		if g != want {
			t.Errorf("Game(%q) =\n %+v\nwant %+v", rel, g, want)
		}
	}
	for _, rel := range []string{
		gba,                  // a system, not a game
		gba + "/Hacks",       // a folder
		gba + "/readme.txt",  // ignored file
		gba + "/.hidden.gba", // hidden file
		gba + "/.media/Starlight Courier.png",
		gba + "/Missing.gba",
		"No Tag Folder/Orphan.gba", // not a system
		ps + "/Mossy Hollow/Mossy Hollow (Disc 1).cue", // inside a game folder
		ps + "/Mossy Hollow/Mossy Hollow.m3u",
	} {
		if _, err := a.Game(rel); !errors.Is(err, osadapter.ErrNotFound) {
			t.Errorf("Game(%q) = %v, want ErrNotFound", rel, err)
		}
	}
}

func TestGameFiles(t *testing.T) {
	a, _ := newAdapter(t)
	g, err := a.Game(gba + "/Starlight Courier.gba")
	if err != nil {
		t.Fatal(err)
	}
	files, err := a.GameFiles(g)
	if err != nil {
		t.Fatal(err)
	}
	if len(files.ROM) != 1 || files.ROM[0].Rel != "Roms/"+gba+"/Starlight Courier.gba" {
		t.Errorf("rom = %+v", files.ROM)
	}
	if files.Save == nil || files.Save.Rel != "Saves/GBA/Starlight Courier.gba.sav" {
		t.Errorf("save = %+v", files.Save)
	}
	if files.Cheats == nil || files.Cheats.Rel != "Cheats/GBA/Starlight Courier.gba.cht" {
		t.Errorf("cheats = %+v", files.Cheats)
	}
	if files.Art == nil || files.Art.Rel != "Roms/"+gba+"/.media/Starlight Courier.png" {
		t.Errorf("art = %+v", files.Art)
	}
	var slots []int
	for _, s := range files.States {
		slots = append(slots, s.Slot)
	}
	if !reflect.DeepEqual(slots, []int{0, 1, 9}) || files.States[0].Rel != ".userdata/shared/GBA-gpsp/Starlight Courier.gba.st0" {
		t.Errorf("states = %+v", files.States)
	}

	// Different case between the ROM and its save.
	g, _ = a.Game(gba + "/Café Rêverie (France).gba")
	files, _ = a.GameFiles(g)
	if files.Save == nil || files.Save.Rel != "Saves/GBA/café rêverie (france).gba.sav" {
		t.Errorf("case-insensitive save = %+v", files.Save)
	}

	// Same game under another tag: its own data only.
	g, _ = a.Game(mgba + "/Starlight Courier.gba")
	files, _ = a.GameFiles(g)
	if files.Save == nil || files.Save.Rel != "Saves/MGBA/Starlight Courier.gba.sav" || len(files.States) != 1 || files.Cheats != nil || files.Art != nil {
		t.Errorf("MGBA files = %+v", files)
	}
}

func TestGameFilesMultiDisc(t *testing.T) {
	a, _ := newAdapter(t)
	g, _ := a.Game(ps + "/Mossy Hollow")
	files, err := a.GameFiles(g)
	if err != nil {
		t.Fatal(err)
	}
	var roms []string
	for _, f := range files.ROM {
		roms = append(roms, filepath.Base(f.Path))
	}
	want := []string{"Mossy Hollow.m3u", "Mossy Hollow (Disc 1).bin", "Mossy Hollow (Disc 1).cue", "Mossy Hollow (Disc 2).bin", "Mossy Hollow (Disc 2).cue"}
	if !reflect.DeepEqual(roms, want) {
		t.Errorf("roms = %q", roms)
	}
	if files.Save == nil || files.Art == nil || len(files.States) != 1 {
		t.Errorf("files = %+v", files)
	}

	g, _ = a.Game(ps + "/Quiet Harbor.m3u")
	files, _ = a.GameFiles(g)
	roms = nil
	for _, f := range files.ROM {
		roms = append(roms, filepath.Base(f.Path))
	}
	want = []string{"Quiet Harbor.m3u", "Quiet Harbor (Disc 1).cue", "Quiet Harbor (Disc 2).cue"}
	if !reflect.DeepEqual(roms, want) {
		t.Errorf("loose m3u roms = %q", roms)
	}
}

func TestM3UDiscsStayInsideTheSystem(t *testing.T) {
	a, sd := newAdapter(t)
	testutil.WriteFile(t, sd, "Roms/"+ps+"/Sub/Escape.m3u",
		"# comment\n../../"+gba+"/Starlight Courier.gba\n/etc/passwd\n..\\Quiet Harbor (Disc 1).cue\nmissing.cue\n\n")
	g, err := a.Game(ps + "/Sub/Escape.m3u")
	if err != nil {
		t.Fatal(err)
	}
	files, _ := a.GameFiles(g)
	var roms []string
	for _, f := range files.ROM {
		roms = append(roms, f.Rel)
	}
	want := []string{"Roms/" + ps + "/Sub/Escape.m3u", "Roms/" + ps + "/Quiet Harbor (Disc 1).cue"}
	if !reflect.DeepEqual(roms, want) {
		t.Errorf("roms = %q", roms)
	}
}

func TestPaths(t *testing.T) {
	a, sd := newAdapter(t)
	g, _ := a.Game(gba + "/Hacks/Starlight Courier Remix.gba")
	if got, want := a.SavePath(g), filepath.Join(sd, "Saves", "GBA", "Starlight Courier Remix.gba.sav"); got != want {
		t.Errorf("SavePath = %s", got)
	}
	if got, want := a.StatePath(g, 3), filepath.Join(sd, ".userdata", "shared", "GBA-gpsp", "Starlight Courier Remix.gba.st3"); got != want {
		t.Errorf("StatePath = %s", got)
	}
	if got, want := a.CheatPath(g), filepath.Join(sd, "Cheats", "GBA", "Starlight Courier Remix.gba.cht"); got != want {
		t.Errorf("CheatPath = %s", got)
	}
	if got, want := a.ArtPath(g), filepath.Join(sd, "Roms", gba, "Hacks", ".media", "Starlight Courier Remix.png"); got != want {
		t.Errorf("ArtPath = %s", got)
	}
	if got, want := a.StateThumbnailPath(g, 7), filepath.Join(sd, ".userdata", "shared", ".minui", "GBA", "Starlight Courier Remix.gba.7.bmp"); got != want {
		t.Errorf("StateThumbnailPath = %s", got)
	}
	if a.StateThumbnailPath(g, 8) != "" || a.StateThumbnailPath(g, 9) != "" {
		t.Error("NextUI keeps thumbnails for the slots 0 to 7 only")
	}
	if slot, ok := a.ResumeSlot(); slot != ResumeSlot || !ok {
		t.Errorf("ResumeSlot = %d, %v", slot, ok)
	}
	if a.Name() != "nextui" || a.Roots().SD != sd {
		t.Errorf("Name/Roots: %s %+v", a.Name(), a.Roots())
	}
}

func TestDeviceInfo(t *testing.T) {
	a, _ := newAdapter(t)
	info, err := a.DeviceInfo()
	if err != nil || info.Model != "brick" || info.Platform != "tg5040" || info.OS != "nextui" || info.OSVersion != nil || info.Battery != nil || info.SSID != nil || info.WirelessInterface != "wlan0" {
		t.Errorf("info = %+v, %v", info, err)
	}
	info, _ = New(RootsForSD(t.TempDir(), ""), func(k string) string {
		if k == "DEVICE" {
			return "toaster"
		}
		return ""
	}).DeviceInfo()
	if info.Model != ModelDesktop || info.Platform != DefaultPlatform {
		t.Errorf("unknown device = %+v", info)
	}
}

func TestParseState(t *testing.T) {
	for name, want := range map[string]struct {
		rom  string
		slot int
		ok   bool
	}{
		"Game.gba.st0":  {"Game.gba", 0, true},
		"Game.gba.st9":  {"Game.gba", 9, true},
		"Game.gba.st12": {"Game.gba", 12, true},
		"Game.gba.st":   {"", 0, false},
		"Game.gba.sta":  {"", 0, false},
		".st1":          {"", 0, false},
		"Game.gba.sav":  {"", 0, false},
	} {
		rom, slot, ok := parseState(name)
		if rom != want.rom || slot != want.slot || ok != want.ok {
			t.Errorf("parseState(%q) = %q %d %v", name, rom, slot, ok)
		}
	}
}

func TestDefaultDeviceName(t *testing.T) {
	for _, tc := range []struct{ model, id, want string }{
		{"brick", "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10", "Brick 3a10"},
		{"smartpro", "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10", "Smart Pro 3a10"},
		{"desktop", "abcd", "Desktop abcd"},
		{"toaster", "12345", "ReminisLink 2345"},
		{"brick", "", "Brick"},
	} {
		if got := DefaultDeviceName(tc.model, tc.id); got != tc.want {
			t.Errorf("DefaultDeviceName(%q, %q) = %q, want %q", tc.model, tc.id, got, tc.want)
		}
	}
}

// An identifier must name the exact bytes on the card: another case (or,
// on APFS, another Unicode normalization) is not the same game, even if
// the file system finds the file.
func TestExactNamesOnly(t *testing.T) {
	a, _ := newAdapter(t)
	for _, rel := range []string{
		"game boy advance (GBA)/Starlight Courier.gba",
		gba + "/starlight courier.gba",
		gba + "/HACKS/Starlight Courier Remix.gba",
		ps + "/mossy hollow",
		"Game Boy Advance (GBA)/Café Rêverie (France).gba", // NFD é
	} {
		if _, err := a.Game(rel); !errors.Is(err, osadapter.ErrNotFound) {
			t.Errorf("Game(%q) = %v, want ErrNotFound", rel, err)
		}
	}
	if _, err := a.System("game boy advance (GBA)"); !errors.Is(err, osadapter.ErrNotFound) {
		t.Errorf("System with another case: %v", err)
	}
	sys, _ := a.System(gba)
	if _, err := a.ListFolder(sys, gba+"/hacks"); !errors.Is(err, osadapter.ErrNotFound) {
		t.Errorf("ListFolder with another case: %v", err)
	}
	if _, err := a.Game(gba + "/Café Rêverie (France).gba"); err != nil {
		t.Errorf("exact name refused: %v", err)
	}
}

// A disc listed with another case is hidden from the folder listing and
// resolved, under its real name, in the game (the same case-insensitive
// comparison on both sides, whatever the file system).
func TestM3UDiscsMatchedIgnoringCase(t *testing.T) {
	a, sd := newAdapter(t)
	testutil.WriteFile(t, sd, "Roms/"+ps+"/Odd Case.m3u", "odd case (DISC 1).CUE\n")
	testutil.WriteFile(t, sd, "Roms/"+ps+"/Odd Case (Disc 1).cue", "disc")
	sys, _ := a.System(ps)
	l, err := a.ListFolder(sys, ps)
	if err != nil {
		t.Fatal(err)
	}
	var oddSize int64 = -1
	for _, g := range l.Games {
		if g.FileName == "Odd Case (Disc 1).cue" {
			t.Error("the disc is listed as a game")
		}
		if g.FileName == "Odd Case.m3u" {
			oddSize = g.Size
		}
	}
	want := int64(len("odd case (DISC 1).CUE\n") + len("disc"))
	if oddSize != want {
		t.Errorf("m3u size %d, want %d", oddSize, want)
	}
	g, err := a.Game(ps + "/Odd Case.m3u")
	if err != nil {
		t.Fatal(err)
	}
	files, _ := a.GameFiles(g)
	if len(files.ROM) != 2 || files.ROM[1].Rel != "Roms/"+ps+"/Odd Case (Disc 1).cue" || files.ROM[1].Size != 4 {
		t.Errorf("roms = %+v", files.ROM)
	}
}

func TestPatchFilesAreNotGames(t *testing.T) {
	a := New(RootsForSD(t.TempDir(), "tg5040"), nil)
	for _, name := range []string{"Hack.ips", "Hack.BPS", "hack.ups"} {
		if a.IsGameFile(name) {
			t.Errorf("%s listed as a game", name)
		}
	}
	if !a.IsGameFile("Hack.gba") {
		t.Error("ROM not a game")
	}
}

func TestReadOSVersion(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]*string{
		"NextUI-20261013-0\n39a06de\n":      ptr("NextUI-20261013-039a06de"), // real newline
		`NextUI-20261013-0\n39a06de` + "\n": ptr("NextUI-20261013-039a06de"), // literal \n
		"NextUI-20261013-0\r\n39a06de\r\n":  ptr("NextUI-20261013-039a06de"),
		"NextUI-20261013-0\n":               ptr("NextUI-20261013-0"),
		"":                                  nil,
		"\n\n":                              nil,
		"NextUI 6\n":                        nil, // space inside
		"Nèxt\n":                            nil, // not ASCII
		strings.Repeat("a", 65):             nil, // too long
	}
	for content, want := range cases {
		p := filepath.Join(dir, "version.txt")
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		got := ReadOSVersion(p)
		if (got == nil) != (want == nil) || got != nil && *got != *want {
			t.Errorf("ReadOSVersion(%q) = %v, want %v", content, deref(got), deref(want))
		}
	}
	if ReadOSVersion(filepath.Join(dir, "missing.txt")) != nil {
		t.Error("missing file must give nil")
	}
}

func TestDeviceInfoOSVersion(t *testing.T) {
	a, sd := newAdapter(t)
	if err := os.WriteFile(filepath.Join(sd, ".system", "version.txt"), []byte("NextUI-20261013-0\n39a06de\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := a.DeviceInfo()
	if err != nil || info.OSVersion == nil || *info.OSVersion != "NextUI-20261013-039a06de" {
		t.Fatalf("OSVersion = %v, %v", deref(info.OSVersion), err)
	}
}

func TestEmulatorInstalled(t *testing.T) {
	a, sd := newAdapter(t)
	for tag, want := range map[string]bool{
		"GBA":  true,  // system pak
		"MGBA": true,  // extra pak
		"GB":   true,  // system pak
		"PS":   true,  // system pak
		"HID":  false, // no pak
		"NOPE": false,
		"":     false,
		"..":   false,
	} {
		if got := a.EmulatorInstalled(tag); got != want {
			t.Errorf("EmulatorInstalled(%q) = %v", tag, got)
		}
	}
	// A pak folder without launch.sh is not an emulator.
	if err := os.MkdirAll(filepath.Join(sd, "Emus", "tg5040", "FC.pak"), 0o755); err != nil {
		t.Fatal(err)
	}
	if a.EmulatorInstalled("FC") {
		t.Error("pak without launch.sh")
	}
	if err := os.WriteFile(filepath.Join(sd, "Emus", "tg5040", "FC.pak", "launch.sh"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !a.EmulatorInstalled("FC") {
		t.Error("extra pak with launch.sh")
	}
}

func ptr(s string) *string { return &s }

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// The battery as NextUI reads it (05 §6), from a fake sysfs.
func TestBattery(t *testing.T) {
	a, _ := newAdapter(t)
	sys := t.TempDir()
	a.SetPowerSupplyDir(sys)
	if b := a.Battery(); b != nil {
		t.Fatalf("no sysfs: %+v", b)
	}
	testutil.WriteFile(t, sys, "axp2202-battery/capacity", "78\n")
	if b := a.Battery(); b == nil || b.Percent != 78 || b.Charging {
		t.Fatalf("level only: %+v", b)
	}
	testutil.WriteFile(t, sys, "axp2202-usb/online", "1\n")
	testutil.WriteFile(t, sys, "axp2202-battery/time_to_full_now", "0\n")
	if b := a.Battery(); b.Charging {
		t.Fatal("plugged in but full: not charging (time_to_full_now = 0)")
	}
	testutil.WriteFile(t, sys, "axp2202-battery/time_to_full_now", "3600\n")
	if b := a.Battery(); !b.Charging {
		t.Fatal("charging not seen")
	}
	testutil.WriteFile(t, sys, "axp2202-usb/online", "0\n")
	if b := a.Battery(); b.Charging {
		t.Fatal("unplugged seen as charging")
	}
	testutil.WriteFile(t, sys, "axp2202-battery/capacity", "garbage")
	if b := a.Battery(); b != nil {
		t.Fatalf("unreadable level: %+v", b)
	}
	testutil.WriteFile(t, sys, "axp2202-battery/capacity", "100")
	info, err := a.DeviceInfo()
	if err != nil || info.Battery == nil || info.Battery.Percent != 100 {
		t.Fatalf("DeviceInfo battery %+v %v", info.Battery, err)
	}
}

// SaveDir finds the same save and clock file as GameFiles, including a
// case-only difference and an exact match among names that differ by case.
func TestSaveDirMatchesGameFiles(t *testing.T) {
	a, sd := newAdapter(t)
	testutil.WriteFile(t, sd, "Saves/GBA/Starlight Courier.gba.rtc", "clock")
	testutil.WriteFile(t, sd, "Saves/GBA/STARLIGHT COURIER.GBA.SAV", "other case")
	testutil.WriteFile(t, sd, "Saves/GBA/lantern keeper [b1].gba.rtc", "lone clock")
	games := []string{
		gba + "/Starlight Courier.gba", gba + "/Café Rêverie (France).gba", gba + "/Lantern Keeper [b1].gba",
		gba + "/Hacks/Starlight Courier Remix.gba", gba + "/Pixel Garden 🌱.gba", mgba + "/Starlight Courier.gba",
		ps + "/Mossy Hollow", ps + "/Quiet Harbor.m3u",
	}
	dirs := map[string]osadapter.SaveDir{}
	for _, rel := range games {
		g, err := a.Game(rel)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		files, err := a.GameFiles(g)
		if err != nil {
			t.Fatal(err)
		}
		if dirs[g.System.Tag] == nil {
			dirs[g.System.Tag] = a.SaveDir(g.System.Tag)
		}
		save, rtc := dirs[g.System.Tag].Files(g)
		if !reflect.DeepEqual(save, files.Save) || !reflect.DeepEqual(rtc, files.RTC) {
			t.Errorf("%s: SaveDir %+v %+v, GameFiles %+v %+v", rel, save, rtc, files.Save, files.RTC)
		}
	}
	if save, _ := a.SaveDir("NOPE").Files(osadapter.Game{ROMName: "x.gba"}); save != nil {
		t.Errorf("missing folder: %+v", save)
	}
}

// WalkGames gives the games a recursive ListFolder lists, and stops with
// its context or the error of fn.
func TestWalkGamesMatchesListFolder(t *testing.T) {
	a, sd := newAdapter(t)
	testutil.WriteFile(t, sd, "Roms/"+ps+"/Tide Pool/Tide Pool.cue", "c")
	testutil.WriteFile(t, sd, "Roms/"+gba+"/Hacks/Deeper/Deepest/x.gba", "r")
	systems, err := a.Systems()
	if err != nil {
		t.Fatal(err)
	}
	for _, sys := range systems {
		var listed []osadapter.Game
		var list func(rel string)
		list = func(rel string) {
			l, err := a.ListFolder(sys, rel)
			if err != nil {
				t.Fatal(err)
			}
			for _, g := range l.Games {
				listed = append(listed, g.Game)
			}
			for _, f := range l.Folders {
				list(f.Path)
			}
		}
		list(sys.Folder)
		var walked []osadapter.Game
		if err := a.WalkGames(context.Background(), sys, func(g osadapter.Game) error {
			walked = append(walked, g)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		key := func(gs []osadapter.Game) map[string]osadapter.Game {
			m := map[string]osadapter.Game{}
			for _, g := range gs {
				m[g.Path] = g
			}
			return m
		}
		if !reflect.DeepEqual(key(listed), key(walked)) || len(listed) != len(walked) {
			t.Errorf("%s: ListFolder %+v, WalkGames %+v", sys.Folder, listed, walked)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.WalkGames(ctx, systems[0], func(osadapter.Game) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
	stop := errors.New("stop")
	n := 0
	if err := a.WalkGames(context.Background(), systems[0], func(osadapter.Game) error { n++; return stop }); !errors.Is(err, stop) || n != 1 {
		t.Errorf("stop: %v after %d", err, n)
	}
}
