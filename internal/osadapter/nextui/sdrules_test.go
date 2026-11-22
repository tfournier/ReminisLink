package nextui

import (
	"path/filepath"
	"testing"

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
