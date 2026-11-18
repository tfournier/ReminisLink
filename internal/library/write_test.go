package library

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/tfournier/ReminisLink/internal/store"
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
