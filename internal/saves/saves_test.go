package saves

import (
	"os"
	"testing"

	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

func TestInstall(t *testing.T) {
	sd := testutil.CopySDCard(t)
	roots := nextui.RootsForSD(sd, "tg5040")
	a := nextui.New(roots, nil)
	layout := store.Layout{Root: roots.Data}
	trash := store.NewTrash(layout, sd, nil, nil)
	for _, rel := range []string{"Game Boy Advance (GBA)/Café Rêverie (France).gba", "Game Boy Advance (GBA)/Lantern Keeper [b1].gba"} {
		g, err := a.Game(rel)
		if err != nil {
			t.Fatal(err)
		}
		tmp := testutil.WriteFile(t, roots.Data, "uploads/x.part", "new save")
		if b, err := Install(a, trash, g, tmp); err != nil || b != nil {
			t.Fatalf("%s: %v %v", rel, b, err)
		}
		if data, _ := os.ReadFile(a.SavePath(g)); string(data) != "new save" {
			t.Fatalf("%s: not installed", rel)
		}
	}
	// Only the first game had a save (spelled in lowercase): it is in the trash.
	entries, _, _ := trash.List()
	if len(entries) != 1 || entries[0].Files[0] != "Saves/GBA/café rêverie (france).gba.sav" {
		t.Fatalf("trash %+v", entries)
	}
}
