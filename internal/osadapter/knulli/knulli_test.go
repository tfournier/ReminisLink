package knulli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

// newAdapter copies testdata/knulli (a /userdata rebuilt from the Knulli
// sources, see its README) and returns the adapter on it with the device
// files of testdata/knulli/device.
func newAdapter(t *testing.T) (Adapter, string) {
	t.Helper()
	root := t.TempDir()
	if err := testutil.CopyTree(filepath.Join(testutil.RepoRoot(), "testdata", "knulli"), root); err != nil {
		t.Fatal(err)
	}
	sd := filepath.Join(root, "userdata")
	dev := filepath.Join(root, "device")
	env := map[string]string{
		"KNULLI_BOARD_FILE":   filepath.Join(dev, "knulli.board"),
		"KNULLI_VERSION_FILE": filepath.Join(dev, "knulli.version"),
		"POWER_SUPPLY_PATH":   filepath.Join(dev, "power_supply"),
	}
	return New(RootsForSD(sd), func(k string) string { return env[k] }), sd
}

func TestRoots(t *testing.T) {
	r := RootsFromEnv(func(string) string { return "" })
	want := osadapter.Roots{
		SD: "/userdata", Roms: "/userdata/roms", Saves: "/userdata/saves", States: "/userdata/saves",
		Cheats: "/userdata/cheats", Userdata: "/userdata/system", Shared: "/userdata/system",
		Data: "/userdata/system/ReminisLink", Logs: "/userdata/system/logs",
		Pak: "/userdata/roms/tools/.ReminisLink", Screenshots: "/userdata/screenshots",
	}
	if r != want {
		t.Fatalf("roots = %+v\nwant %+v", r, want)
	}
	if r.Tools != "" || r.Emus != "" || r.System != "" {
		t.Fatal("Knulli has no paks")
	}
}

func TestSystems(t *testing.T) {
	a, _ := newAdapter(t)
	if a.Name() != "knulli" {
		t.Fatalf("Name = %q", a.Name())
	}
	sys, err := a.Systems()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range sys {
		got = append(got, s.Folder+"="+s.Label+"/"+s.Tag)
	}
	// tools (the ReminisLink launcher) and an unknown folder are not systems.
	want := []string{"gba=Game Boy Advance/gba", "psx=PlayStation/psx"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("systems = %v, want %v", got, want)
	}
	if _, err := a.System("tools"); !errors.Is(err, osadapter.ErrNotFound) {
		t.Fatalf("tools: %v", err)
	}
	if !a.EmulatorInstalled("gba") || a.EmulatorInstalled("notasystem") {
		t.Fatal("EmulatorInstalled")
	}
}

func TestListFolder(t *testing.T) {
	a, sd := newAdapter(t)
	sys, err := a.System("gba")
	if err != nil {
		t.Fatal(err)
	}
	l, err := a.ListFolder(sys, "gba")
	if err != nil {
		t.Fatal(err)
	}
	var folders, games []string
	for _, f := range l.Folders {
		folders = append(folders, f.Name)
		if f.GameCount != 1 {
			t.Errorf("%s: %d games", f.Name, f.GameCount)
		}
	}
	for _, g := range l.Games {
		games = append(games, g.FileName)
	}
	// images/ is EmulationStation's, readme.txt and notes.sh are not GBA
	// files, gamelist.xml is metadata.
	if !reflect.DeepEqual(folders, []string{"Hacks"}) || !reflect.DeepEqual(games, []string{"Pixel Garden.gba", "Starlight Courier.gba"}) {
		t.Fatalf("folders %v games %v", folders, games)
	}
	s := l.Games[1]
	if !s.HasSave || s.StateCount != 2 || !s.HasArt || s.CheatFile != "" {
		t.Fatalf("summary %+v", s)
	}
	if l.Games[0].HasSave || l.Games[0].HasArt || l.Games[0].StateCount != 0 {
		t.Fatalf("Pixel Garden %+v", l.Games[0])
	}
	if n, _ := a.CountGames(sys); n != 3 {
		t.Fatalf("CountGames = %d", n)
	}
	if _, err := a.ListFolder(sys, "gba/images"); !errors.Is(err, osadapter.ErrNotFound) {
		t.Fatalf("images listed: %v", err)
	}
	if _, err := a.Game("gba/images/Starlight Courier-image.png"); !errors.Is(err, osadapter.ErrNotFound) {
		t.Fatalf("art is a game: %v", err)
	}
	if _, err := a.Game("gba/notes.sh"); !errors.Is(err, osadapter.ErrNotFound) {
		t.Fatalf("notes.sh is a game: %v", err)
	}
	_ = sd
}

func TestGameFiles(t *testing.T) {
	a, sd := newAdapter(t)
	g, err := a.Game("gba/Starlight Courier.gba")
	if err != nil {
		t.Fatal(err)
	}
	files, err := a.GameFiles(g)
	if err != nil {
		t.Fatal(err)
	}
	saves := filepath.Join(sd, "saves", "gba")
	if files.Save == nil || files.Save.Path != filepath.Join(saves, "Starlight Courier.srm") {
		t.Fatalf("save %+v", files.Save)
	}
	if files.RTC == nil || files.RTC.Path != filepath.Join(saves, "Starlight Courier.rtc") {
		t.Fatalf("rtc %+v", files.RTC)
	}
	var slots []int
	for _, s := range files.States {
		slots = append(slots, s.Slot)
	}
	if !reflect.DeepEqual(slots, []int{0, 1}) {
		t.Fatalf("slots %v", slots)
	}
	var side []string
	for _, f := range files.StateSide {
		side = append(side, filepath.Base(f.Path))
	}
	sort.Strings(side)
	if !reflect.DeepEqual(side, []string{"Starlight Courier.state.auto", "Starlight Courier.state1.png"}) {
		t.Fatalf("side %v", side)
	}
	if files.Cheats != nil || a.CheatPath(g) != "" {
		t.Fatal("no cheats on Knulli")
	}
	if files.Art == nil || files.Art.Path != filepath.Join(sd, "roms", "gba", "images", "Starlight Courier-image.png") {
		t.Fatalf("art %+v", files.Art)
	}

	if got := a.SavePath(g); got != filepath.Join(saves, "Starlight Courier.srm") {
		t.Errorf("SavePath %q", got)
	}
	if got := a.StatePath(g, 0); got != filepath.Join(saves, "Starlight Courier.state") {
		t.Errorf("StatePath 0 %q", got)
	}
	if got := a.StatePath(g, 7); got != filepath.Join(saves, "Starlight Courier.state7") {
		t.Errorf("StatePath 7 %q", got)
	}
	if got := a.StateThumbnailPath(g, 7); got != filepath.Join(saves, "Starlight Courier.state7.png") {
		t.Errorf("thumbnail %q", got)
	}
	if got := a.StateSideFiles(g, 0); !reflect.DeepEqual(got, []string{filepath.Join(saves, "Starlight Courier.state.png")}) {
		t.Errorf("side files %v", got)
	}
	if _, ok := a.ResumeSlot(); ok {
		t.Error("RetroArch's automatic state is not a slot")
	}
	if a.CheckSaveFormat(g) != nil || a.CheckStateFormat(g) != nil {
		t.Error("formats")
	}

	// A game in a subfolder: EmulationStation reads the art of the system root.
	h, err := a.Game("gba/Hacks/Lantern Keeper.gba")
	if err != nil {
		t.Fatal(err)
	}
	if got := a.ArtPath(h); got != filepath.Join(sd, "roms", "gba", "images", "Lantern Keeper-image.png") {
		t.Errorf("ArtPath %q", got)
	}

	// A playlist and its discs.
	m, err := a.Game("psx/Quiet Harbor.m3u")
	if err != nil {
		t.Fatal(err)
	}
	mf, err := a.GameFiles(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(mf.ROM) != 3 || mf.Save == nil || filepath.Base(mf.Save.Path) != "Quiet Harbor.srm" {
		t.Fatalf("m3u files %+v save %+v", mf.ROM, mf.Save)
	}
	if _, err := a.Game("psx/Quiet Harbor (Disc 1).chd"); !errors.Is(err, osadapter.ErrNotFound) {
		t.Fatalf("a disc of a playlist is a game: %v", err)
	}
}

// TestCaseSensitivity: /userdata may be ext4 (case-sensitive) or exFAT.
// A save whose case differs from the ROM is found either way, and a system
// folder must be spelled exactly (EmulationStation opens roms/<folder>).
func TestCaseSensitivity(t *testing.T) {
	a, sd := newAdapter(t)
	saves := filepath.Join(sd, "saves", "gba")
	if err := os.Rename(filepath.Join(saves, "Starlight Courier.srm"), filepath.Join(saves, "tmp.srm")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(saves, "tmp.srm"), filepath.Join(saves, "starlight courier.srm")); err != nil {
		t.Fatal(err)
	}
	g, err := a.Game("gba/Starlight Courier.gba")
	if err != nil {
		t.Fatal(err)
	}
	files, err := a.GameFiles(g)
	if err != nil {
		t.Fatal(err)
	}
	// On a file system that ignores case (exFAT, APFS), the OS finds the
	// save under the other case: so does the pak. On ext4, RetroArch
	// would not read it: it is not the save of the game.
	if caseInsensitive(t, saves) {
		if files.Save == nil || filepath.Base(files.Save.Path) != "starlight courier.srm" {
			t.Fatalf("save %+v", files.Save)
		}
	} else if files.Save != nil {
		t.Fatalf("case-sensitive: save %+v", files.Save)
	}
	if _, err := a.System("GBA"); !errors.Is(err, osadapter.ErrNotFound) {
		t.Fatalf("GBA: %v", err)
	}
}

func TestStateFiles(t *testing.T) {
	a, _ := newAdapter(t)
	refs, err := a.StateFiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range refs {
		got = append(got, r.Tag+"/"+r.ROMName+"#"+string(rune('0'+r.Slot)))
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"gba/Starlight Courier#0", "gba/Starlight Courier#1"}) {
		t.Fatalf("states %v", got)
	}
}

func TestParseState(t *testing.T) {
	var s states
	cases := map[string]struct {
		key  string
		slot int
		ok   bool
	}{
		"Game.state":      {"Game", 0, true},
		"Game.state3":     {"Game", 3, true},
		"Game.state42":    {"Game", 42, true},
		"Game.state100":   {"", 0, false},
		"Game.state0":     {"", 0, false},
		"Game.state.auto": {"", 0, false},
		"Game.state1.png": {"", 0, false},
		"Game.srm":        {"", 0, false},
	}
	for name, want := range cases {
		key, slot, ok := s.Parse(name)
		if key != want.key || slot != want.slot || ok != want.ok {
			t.Errorf("%s: %q %d %v", name, key, slot, ok)
		}
	}
}

func TestDeviceInfo(t *testing.T) {
	a, _ := newAdapter(t)
	info, err := a.DeviceInfo()
	if err != nil {
		t.Fatal(err)
	}
	if info.Model != "h700" || info.Platform != "rg35xx-plus" || info.OS != "knulli" || info.WirelessInterface != "wlan0" {
		t.Fatalf("info %+v", info)
	}
	if info.OSVersion == nil || *info.OSVersion != "41.1 2025/06/01 12:00" {
		t.Fatalf("version %v", info.OSVersion)
	}
	if info.Battery == nil || info.Battery.Percent != 76 || info.Battery.Charging {
		t.Fatalf("battery %+v", info.Battery)
	}

	// Off the console: no board file.
	d := New(RootsForSD(t.TempDir()), func(k string) string {
		return map[string]string{"KNULLI_BOARD_FILE": "/nonexistent", "KNULLI_VERSION_FILE": "/nonexistent", "POWER_SUPPLY_PATH": "/nonexistent"}[k]
	})
	info, _ = d.DeviceInfo()
	if info.Model != "desktop" || info.Platform != "knulli" || info.Battery != nil || info.OSVersion != nil || info.WirelessInterface != "" {
		t.Fatalf("desktop %+v", info)
	}
}

func TestBoards(t *testing.T) {
	for board, model := range map[string]string{"trimui-brick": "brick", "trimui-smart-pro": "smartpro", "rg40xx-v": "h700"} {
		if Boards[board] != model {
			t.Errorf("%s: %q", board, Boards[board])
		}
	}
}

func caseInsensitive(t *testing.T, dir string) bool {
	t.Helper()
	_, err := os.Lstat(filepath.Join(dir, "STARLIGHT COURIER.RTC"))
	return err == nil
}

func write(t *testing.T, p, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newCaseSensitive is newAdapter on a case-sensitive file system (ext4).
func newCaseSensitive(t *testing.T) (Adapter, string) {
	t.Helper()
	sd, env := testutil.CopyKnulli(t, testutil.CaseSensitiveDir(t))
	return New(RootsForSD(sd), func(k string) string { return env[k] }), sd
}

// Codex review of M20: on ext4, Game.gba and game.gba are two games; each
// gets only the files spelled like it (states, thumbnails, automatic
// state, save, clock), so deleting one never takes the other's.
func TestTwoGamesDifferingByCase(t *testing.T) {
	a, sd := newCaseSensitive(t)
	saves := filepath.Join(sd, "saves", "gba")
	write(t, filepath.Join(sd, "roms", "gba", "starlight courier.gba"), "other rom")
	for _, n := range []string{"starlight courier.state1", "starlight courier.state1.png", "starlight courier.state.auto", "starlight courier.rtc"} {
		write(t, filepath.Join(saves, n), "other")
	}
	files := func(rel string) osadapter.GameFiles {
		t.Helper()
		g, err := a.Game(rel)
		if err != nil {
			t.Fatal(err)
		}
		f, err := a.GameFiles(g)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	names := func(f osadapter.GameFiles) []string {
		var out []string
		for _, s := range f.States {
			out = append(out, filepath.Base(s.Path))
		}
		for _, s := range f.StateSide {
			out = append(out, filepath.Base(s.Path))
		}
		for _, s := range []*osadapter.File{f.Save, f.RTC} {
			if s != nil {
				out = append(out, filepath.Base(s.Path))
			}
		}
		sort.Strings(out)
		return out
	}
	upper := names(files("gba/Starlight Courier.gba"))
	for _, n := range upper {
		if n[0] == 's' {
			t.Errorf("Starlight Courier.gba takes %s", n)
		}
	}
	lower := names(files("gba/starlight courier.gba"))
	want := []string{"starlight courier.rtc", "starlight courier.state.auto", "starlight courier.state1", "starlight courier.state1.png"}
	if !reflect.DeepEqual(lower, want) {
		t.Errorf("starlight courier.gba: %v, want %v", lower, want)
	}
	// Without its own save, a game never takes the other's.
	if err := os.Remove(filepath.Join(saves, "Starlight Courier.rtc")); err != nil {
		t.Fatal(err)
	}
	if f := files("gba/Starlight Courier.gba"); f.RTC != nil {
		t.Errorf("RTC of the other game: %+v", f.RTC)
	}
	// The listing counts each game's states only.
	sys, _ := a.System("gba")
	l, err := a.ListFolder(sys, "gba")
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range l.Games {
		switch g.FileName {
		case "starlight courier.gba":
			if g.StateCount != 1 || g.HasSave {
				t.Errorf("%s: %+v", g.FileName, g)
			}
		case "Starlight Courier.gba":
			if !g.HasSave {
				t.Errorf("%s: %+v", g.FileName, g)
			}
		}
	}
}

// A folder of the states reached through a symbolic link: the states are
// neither listed, read nor written (rule 2, Codex review of M20).
func TestStatesThroughALink(t *testing.T) {
	a, sd := newAdapter(t)
	saves := filepath.Join(sd, "saves", "gba")
	outside := filepath.Join(filepath.Dir(sd), "elsewhere")
	if err := os.Rename(saves, outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, saves); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}
	g, _ := a.Game("gba/Starlight Courier.gba")
	if p := a.StatePath(g, 1); p != "" {
		t.Errorf("StatePath through a link: %s", p)
	}
	if p := a.StateThumbnailPath(g, 1); p != "" {
		t.Errorf("StateThumbnailPath through a link: %s", p)
	}
	files, err := a.GameFiles(g)
	if err != nil || len(files.States) != 0 || len(files.StateSide) != 0 {
		t.Errorf("states through a link: %+v %v", files.States, err)
	}
	refs, _ := a.StateFiles(context.Background())
	for _, r := range refs {
		if r.Tag == "gba" {
			t.Errorf("state through a link listed: %+v", r)
		}
	}
}

// Two ROMs of the same name in two folders of a system get the same art
// (roms/<system>/images, EmulationStation findLocalArt): deleting one
// keeps it for the other.
func TestSharedArt(t *testing.T) {
	a, sd := newAdapter(t)
	write(t, filepath.Join(sd, "roms", "gba", "Hacks", "Starlight Courier.gba"), "hack")
	for _, rel := range []string{"gba/Starlight Courier.gba", "gba/Hacks/Starlight Courier.gba"} {
		g, err := a.Game(rel)
		if err != nil {
			t.Fatal(err)
		}
		art := a.ArtPath(g)
		if art != filepath.Join(sd, "roms", "gba", "images", "Starlight Courier-image.png") {
			t.Errorf("%s: art %s", rel, art)
		}
		if !a.SharedFiles(g)[art] {
			t.Errorf("%s: art not shared", rel)
		}
	}
	g, _ := a.Game("gba/Pixel Garden.gba")
	write(t, filepath.Join(sd, "roms", "gba", "images", "Pixel Garden-image.png"), "png")
	if len(a.SharedFiles(g)) != 0 {
		t.Errorf("Pixel Garden: %v", a.SharedFiles(g))
	}
}
