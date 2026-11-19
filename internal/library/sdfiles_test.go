package library

import (
	"errors"
	"testing"

	"github.com/tfournier/ReminisLink/internal/testutil"
)

func TestSDFiles(t *testing.T) {
	l, sd := newLibrary(t)
	testutil.WriteFile(t, sd, "Cheats/GBA/.hidden.cht", "x")
	testutil.WriteFile(t, sd, "Cheats/.trash/GBA/x.cht", "x")
	testutil.WriteFile(t, sd, "Cheats/GBA/notes.txt", "x")
	testutil.WriteFile(t, sd, "Roms/Game Boy Advance (GBA)/Hacks/fix.IPS", "p")
	testutil.WriteFile(t, sd, ".userdata/shared/ReminisLink/patches/fr.bps", "p")
	chts, err := l.SDFiles(SDKindCheat)
	if err != nil || len(chts) != 2 || chts[1].Path != "Cheats/GBA/Starlight Courier.gba.cht" {
		t.Fatalf("cht: %+v %v", chts, err)
	}
	p, err := l.ResolveSDFile(chts[1].ID, SDKindCheat)
	if err != nil || p == "" {
		t.Fatalf("resolve: %v", err)
	}
	patches, err := l.SDFiles(SDKindPatch)
	if err != nil || len(patches) != 2 || patches[0].Path != ".userdata/shared/ReminisLink/patches/fr.bps" {
		t.Fatalf("patch: %+v %v", patches, err)
	}
	for _, f := range patches {
		if _, err := l.ResolveSDFile(f.ID, SDKindPatch); err != nil {
			t.Errorf("resolve %s: %v", f.Path, err)
		}
	}
	if _, err := l.SDFiles("rom"); !errors.Is(err, ErrBadKind) {
		t.Errorf("kind: %v", err)
	}
	// Confinement: another kind, another root, hidden files, escapes.
	for id, want := range map[string]error{
		chts[1].ID: ErrInvalidID, // a .cht is not a patch
		"sd_" + EncodeID("Saves/GBA/Starlight Courier.gba.sav"): ErrInvalidID,
		"sd_" + EncodeID("Cheats/GBA/.hidden.cht"):               ErrInvalidID,
		"sd_" + EncodeID("Cheats/../Saves/x.cht"):                ErrInvalidID,
		"sd_" + EncodeID("Cheats/GBA/missing.cht"):               ErrNotFound,
		EncodeID("Cheats/GBA/Starlight Courier.gba.cht"):         ErrInvalidID, // no prefix
		"sd_!!":                                                  ErrInvalidID,
	} {
		kind := SDKindCheat
		if id == chts[1].ID {
			kind = SDKindPatch
		}
		if _, err := l.ResolveSDFile(id, kind); !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", id, err, want)
		}
	}
}
