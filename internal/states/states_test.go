package states

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

func TestInstallAndLocks(t *testing.T) {
	sd := testutil.CopySDCard(t)
	roots := nextui.RootsForSD(sd, "tg5040")
	a := nextui.New(roots, nil)
	layout := store.Layout{Root: roots.Data}
	trash := store.NewTrash(layout, sd, nil, nil)
	g, err := a.Game("Game Boy Advance (GBA)/Starlight Courier.gba")
	if err != nil {
		t.Fatal(err)
	}
	if !ValidSlot(0) || !ValidSlot(MaxSlot) || ValidSlot(-1) || ValidSlot(MaxSlot+1) {
		t.Fatal("ValidSlot")
	}
	tmp := testutil.WriteFile(t, roots.Data, "uploads/x.part", "new state")
	slot, err := Install(a, trash, layout, g, 0, tmp)
	if err != nil || slot.Slot != 0 || slot.SizeBytes != 9 || slot.Locked || slot.LockStatus != nil {
		t.Fatalf("install: %+v %v", slot, err)
	}
	if data, _ := os.ReadFile(a.StatePath(g, 0)); string(data) != "new state" {
		t.Fatal("not installed")
	}
	if entries, _, _ := trash.List(); len(entries) != 1 {
		t.Fatalf("old state not in the trash: %+v", entries)
	}
	if IsLocked(layout, g, 1) {
		t.Fatal("locked without lock")
	}
	testutil.WriteFile(t, layout.LocksDir("GBA"), "STARLIGHT COURIER.GBA/Slot1.st", "")
	if !IsLocked(layout, g, 1) || IsLocked(layout, g, 2) {
		t.Fatal("lock not found ignoring case")
	}
	tmp = testutil.WriteFile(t, roots.Data, "uploads/y.part", "x")
	if _, err := Install(a, trash, layout, g, 1, tmp); !errors.Is(err, ErrLocked) {
		t.Fatalf("locked slot: %v", err)
	}
	if _, err := Install(a, trash, layout, g, 100, tmp); !errors.Is(err, ErrInvalidSlot) {
		t.Fatalf("bad slot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(roots.Data, "uploads", "y.part")); err != nil {
		t.Fatal("refused file removed")
	}
}
