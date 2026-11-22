package library

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func entryNamed(t *testing.T, f SDFolder, name string) SDEntry {
	t.Helper()
	for _, e := range f.Entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no entry %q", name)
	return SDEntry{}
}

func protectionOf(e SDEntry) string {
	if e.Protection == nil {
		return ""
	}
	return *e.Protection
}

// TestSDProtectionNextUI checks the protected places of NextUI (M23).
func TestSDProtectionNextUI(t *testing.T) {
	l, sd := newLibrary(t)
	writeSD(t, sd, "Tools/tg5040/ReminisLink.pak/launch.sh", "#!/bin/sh")
	writeSD(t, sd, "Tools/tg5040/Other.pak/launch.sh", "#!/bin/sh")
	writeSD(t, sd, "Bios/gba_bios.bin", "bios")
	l.ProtectPaths(filepath.Join(sd, "Tools/tg5040/ReminisLink.pak"))
	root, err := l.Browse("")
	if err != nil {
		t.Fatal(err)
	}
	if !root.Writable {
		t.Fatal("root not writable")
	}
	for name, want := range map[string]string{
		".system": ProtectSystem, ".userdata": ProtectSystem, "Roms": ProtectRoot, "Saves": ProtectRoot,
		"Bios": ProtectRoot, "Tools": ProtectRoot, "Emus": ProtectRoot, "Screenshots": ProtectRoot, "Cheats": ProtectRoot,
	} {
		e := entryNamed(t, root, name)
		if e.Deletable || protectionOf(e) != want {
			t.Errorf("%s: deletable %v protection %q, want %q", name, e.Deletable, protectionOf(e), want)
		}
	}
	tools, err := l.Browse(SDEntryID("Tools/tg5040"))
	if err != nil || !tools.Writable {
		t.Fatalf("tools %+v %v", tools, err)
	}
	if e := entryNamed(t, tools, "ReminisLink.pak"); e.Deletable || protectionOf(e) != ProtectData {
		t.Errorf("own pak: %+v", e)
	}
	if e := entryNamed(t, tools, "Other.pak"); !e.Deletable || e.Protection != nil {
		t.Errorf("other pak: %+v", e)
	}
	gba, err := l.Browse(SDEntryID("Roms/Game Boy Advance (GBA)"))
	if err != nil || !gba.Writable {
		t.Fatal(err)
	}
	if e := entryNamed(t, gba, "readme.txt"); !e.Deletable {
		t.Errorf("readme: %+v", e)
	}
	// Read only: .userdata (listed), .system, the own pak.
	for _, id := range []string{SDEntryID(".userdata/shared"), SDEntryID(".system/tg5040"), SDEntryID("Tools/tg5040/ReminisLink.pak")} {
		f, err := l.Browse(id)
		if err != nil || f.Writable {
			t.Errorf("%s writable: %+v %v", id, f.Writable, err)
		}
		var p *ProtectedError
		if _, _, err := l.SDFolderDir(id); !errors.As(err, &p) {
			t.Errorf("%s folder dir: %v", id, err)
		}
	}
	// Whatever the case (exFAT).
	var p *ProtectedError
	if _, _, err := l.SDFolderDir(SDEntryID(".USERDATA/shared")); err == nil {
		t.Error(".USERDATA writable")
	} else if !errors.As(err, &p) && !errors.Is(err, ErrNotFound) {
		t.Errorf(".USERDATA: %v", err)
	}
	if dir, rel, err := l.SDFolderDir(SDEntryID("Roms/Game Boy Advance (GBA)/Hacks")); err != nil || rel != "Roms/Game Boy Advance (GBA)/Hacks" || dir != filepath.Join(sd, "Roms", "Game Boy Advance (GBA)", "Hacks") {
		t.Errorf("hacks: %q %q %v", dir, rel, err)
	}
	if _, _, err := l.SDFolderDir(SDEntryID("Roms/Game Boy Advance (GBA)/readme.txt")); !errors.Is(err, ErrNotFound) {
		t.Errorf("a file as a folder: %v", err)
	}
	for _, bad := range []string{"se_Li4", "se_" + EncodeID("../x"), "x", "se_!!"} {
		if _, _, err := l.SDFolderDir(bad); !errors.Is(err, ErrInvalidID) {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

func TestSelectSDTrash(t *testing.T) {
	l, sd := newLibrary(t)
	writeSD(t, sd, "Roms/Game Boy Advance (GBA)/Hacks/notes.txt", "n")
	readme := SDEntryID("Roms/Game Boy Advance (GBA)/readme.txt")
	hacks := SDEntryID("Roms/Game Boy Advance (GBA)/Hacks")
	sel, err := l.SelectSDTrash([]string{readme, readme})
	if err != nil || len(sel.Items) != 1 || sel.Label != "readme.txt" || sel.Items[0].Identity == nil {
		t.Fatalf("one file: %+v %v", sel, err)
	}
	sel, err = l.SelectSDTrash([]string{readme, hacks})
	if err != nil || len(sel.Items) != 2 || sel.Label != "2 items" || sel.Items[1].Identity != nil {
		t.Fatalf("two: %+v %v", sel, err)
	}
	// Protected entries: nothing is selected, the refused ones are named.
	var p *ProtectedError
	_, err = l.SelectSDTrash([]string{readme, SDEntryID("Roms"), SDEntryID(".userdata/shared/minuisettings.txt")})
	if !errors.As(err, &p) || p.Reason != ProtectRoot || len(p.IDs) != 2 {
		t.Fatalf("protected: %v", err)
	}
	var m *MissingError
	if _, err := l.SelectSDTrash([]string{readme, SDEntryID("Roms/nope.txt"), SDEntryID(".userdata/shared/ReminisLink/clients.json")}); !errors.As(err, &m) || len(m.IDs) != 2 {
		t.Fatalf("missing: %v", err)
	}
	if _, err := l.SelectSDTrash([]string{"se_" + EncodeID("/etc/passwd")}); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("absolute: %v", err)
	}
}

func TestSelectSDTrashRefusesLinks(t *testing.T) {
	l, sd := newLibrary(t)
	outside := t.TempDir()
	writeSD(t, outside, "secret.txt", "s")
	writeSD(t, sd, "Stuff/keep.txt", "k")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(sd, "Stuff", "link")); err != nil {
		t.Skip("no links")
	}
	if err := os.Symlink(outside, filepath.Join(sd, "Out")); err != nil {
		t.Fatal(err)
	}
	var p *ProtectedError
	if _, err := l.SelectSDTrash([]string{SDEntryID("Stuff")}); !errors.As(err, &p) || p.Reason != ProtectLink {
		t.Fatalf("folder with a link: %v", err)
	}
	var m *MissingError
	for _, rel := range []string{"Out", "Out/secret.txt", "Stuff/link"} {
		if _, err := l.SelectSDTrash([]string{SDEntryID(rel)}); !errors.As(err, &m) {
			t.Errorf("%s: %v", rel, err)
		}
		if _, _, err := l.SDFolderDir(SDEntryID(rel)); !errors.Is(err, ErrNotFound) {
			t.Errorf("folder %s: %v", rel, err)
		}
	}
}

// TestSDProtectionKnulli checks the protected places of Knulli (M23).
func TestSDProtectionKnulli(t *testing.T) {
	l, _, sd := knulliLibrary(t, t.TempDir())
	writeSD(t, sd, "bios/x.bin", "b")
	root, err := l.Browse("")
	if err != nil || !root.Writable {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"system": ProtectSystem, "roms": ProtectRoot, "saves": ProtectRoot, "bios": ProtectRoot, "screenshots": ProtectRoot} {
		if e := entryNamed(t, root, name); e.Deletable || protectionOf(e) != want {
			t.Errorf("%s: %+v", name, e)
		}
	}
	tools, err := l.Browse(SDEntryID("roms/tools"))
	if err != nil || !tools.Writable {
		t.Fatal(err)
	}
	if e := entryNamed(t, tools, "ReminisLink.sh"); e.Deletable || protectionOf(e) != ProtectData {
		t.Errorf("launcher: %+v", e)
	}
	roms, err := l.Browse(SDEntryID("roms"))
	if err != nil {
		t.Fatal(err)
	}
	if e := entryNamed(t, roms, "tools"); e.Deletable {
		t.Errorf("tools deletable: %+v", e)
	}
	if e := entryNamed(t, roms, "notasystem"); !e.Deletable {
		t.Errorf("notasystem: %+v", e)
	}
	var p *ProtectedError
	if _, _, err := l.SDFolderDir(SDEntryID("system")); !errors.As(err, &p) || p.Reason != ProtectSystem {
		t.Errorf("system: %v", err)
	}
}
