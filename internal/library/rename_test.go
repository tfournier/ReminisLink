package library

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/store"
)

// renameGame plans and runs a rename as internal/transfer does.
func renameGame(t *testing.T, l *Library, sd, gameID, name string) (RenamePlan, error) {
	t.Helper()
	plan, err := l.PlanRename(gameID, name)
	if err != nil {
		return plan, err
	}
	trash := store.NewTrash(l.layout, sd, time.Now, nil)
	return plan, trash.RenameGroup(plan.Old.Name, plan.Steps)
}

func mustExist(t *testing.T, sd string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		dir, base := filepath.Split(filepath.Join(sd, filepath.FromSlash(rel)))
		des, err := os.ReadDir(dir)
		found := false
		for _, d := range des {
			found = found || d.Name() == base
		}
		if err != nil || !found {
			t.Errorf("missing %s", rel)
		}
	}
}

func mustNotExist(t *testing.T, sd string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		dir, base := filepath.Split(filepath.Join(sd, filepath.FromSlash(rel)))
		des, _ := os.ReadDir(dir)
		for _, d := range des {
			if d.Name() == base {
				t.Errorf("still there: %s", rel)
			}
		}
	}
}

func TestRenameGameNextUIFile(t *testing.T) {
	l, sd := newLibrary(t)
	roots := nextui.RootsForSD(sd, "tg5040")
	writeSD(t, sd, "Cheats/GBA/Starlight Courier.gba.cht.bak", "old cheats")
	writeSD(t, sd, ".userdata/shared/.minui/GBA/Starlight Courier.gba.txt", "1")
	lock := filepath.Join(roots.Data, "locks/GBA/Starlight Courier.gba/slot1.st")
	writeSD(t, sd, mustRel(t, sd, lock), "lock")
	backup := filepath.Join(roots.Data, "backups/GBA/Starlight Courier.gba/saves/b1.sav")
	writeSD(t, sd, mustRel(t, sd, backup), "backup")
	plan, err := renameGame(t, l, sd, EncodeID("Game Boy Advance (GBA)/Starlight Courier.gba"), "Star Courier")
	if err != nil {
		t.Fatal(err)
	}
	mustExist(t, sd,
		"Roms/Game Boy Advance (GBA)/Star Courier.gba",
		"Saves/GBA/Star Courier.gba.sav",
		".userdata/shared/GBA-gpsp/Star Courier.gba.st0",
		".userdata/shared/GBA-gpsp/Star Courier.gba.st1",
		".userdata/shared/GBA-gpsp/Star Courier.gba.st9",
		".userdata/shared/.minui/GBA/Star Courier.gba.0.bmp",
		".userdata/shared/.minui/GBA/Star Courier.gba.txt",
		"Cheats/GBA/Star Courier.gba.cht",
		"Cheats/GBA/Star Courier.gba.cht.bak",
		"Roms/Game Boy Advance (GBA)/.media/Star Courier.png",
		".userdata/shared/ReminisLink/locks/GBA/Star Courier.gba/slot1.st",
		".userdata/shared/ReminisLink/backups/GBA/Star Courier.gba/saves/b1.sav",
	)
	mustNotExist(t, sd, "Roms/Game Boy Advance (GBA)/Starlight Courier.gba", "Saves/GBA/Starlight Courier.gba.sav",
		"Cheats/GBA/Starlight Courier.gba.cht", "Roms/Game Boy Advance (GBA)/.media/Starlight Courier.png")
	// The other games keep theirs: MGBA (another tag), the Remix (another
	// name that starts the same way).
	mustExist(t, sd, "Saves/MGBA/Starlight Courier.gba.sav", "Saves/GBA/Starlight Courier Remix.gba.sav",
		"Roms/Game Boy Advance (GBA)/Hacks/.media/Starlight Courier Remix.png")
	if plan.New.Path != "Game Boy Advance (GBA)/Star Courier.gba" {
		t.Fatalf("new path %q", plan.New.Path)
	}
	for _, r := range plan.Renamed {
		if filepath.IsAbs(r.From) || filepath.IsAbs(r.To) {
			t.Fatalf("absolute path in %+v", r)
		}
	}
	g, err := l.GameSummary(EncodeID(plan.New.Path))
	if err != nil || !g.HasSave || g.StateCount == 0 || g.CheatCount == 0 || !g.HasArt {
		t.Fatalf("summary %+v %v", g, err)
	}
}

func mustRel(t *testing.T, sd, p string) string {
	t.Helper()
	rel, err := filepath.Rel(sd, p)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(rel)
}

func TestRenameGameFolder(t *testing.T) {
	l, sd := newLibrary(t)
	plan, err := renameGame(t, l, sd, EncodeID("Sony PlayStation (PS)/Mossy Hollow"), "Mossy Valley")
	if err != nil {
		t.Fatal(err)
	}
	mustExist(t, sd,
		"Roms/Sony PlayStation (PS)/Mossy Valley/Mossy Valley.m3u",
		"Roms/Sony PlayStation (PS)/Mossy Valley/Mossy Hollow (Disc 1).bin", // the discs keep their names
		"Saves/PS/Mossy Valley.m3u.sav",
		".userdata/shared/PS-pcsx_rearmed/Mossy Valley.m3u.st0",
		"Roms/Sony PlayStation (PS)/.media/Mossy Valley.png",
	)
	mustNotExist(t, sd, "Roms/Sony PlayStation (PS)/Mossy Hollow")
	if plan.Renamed[1].From != "Roms/Sony PlayStation (PS)/Mossy Hollow/Mossy Hollow.m3u" {
		t.Fatalf("renamed %+v", plan.Renamed)
	}
	if _, err := l.GameSummary(EncodeID(plan.New.Path)); err != nil {
		t.Fatal(err)
	}
}

func TestRenameGameConflicts(t *testing.T) {
	l, sd := newLibrary(t)
	id := EncodeID("Game Boy Advance (GBA)/Starlight Courier.gba")
	before := tree(t, sd)
	for name, want := range map[string]string{
		"Café Rêverie (France)":   "Roms/Game Boy Advance (GBA)/Café Rêverie (France).gba", // another ROM (and its save of another case)
		"Starlight Courier Remix": "Saves/GBA/Starlight Courier Remix.gba.sav",             // the save of a game in a subfolder
	} {
		_, err := l.PlanRename(id, name)
		var conflict *RenameConflictError
		if !errors.As(err, &conflict) || !contains(conflict.Conflicts, want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if tree(t, sd) != before {
		t.Fatal("the card changed")
	}
	// The same name changes nothing.
	plan, err := l.PlanRename(id, "Starlight Courier")
	if err != nil || len(plan.Steps) != 0 {
		t.Fatalf("same name: %+v %v", plan, err)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// tree lists every path of the card.
func tree(t *testing.T, root string) string {
	t.Helper()
	var out []string
	_ = filepath.Walk(root, func(p string, _ os.FileInfo, err error) error {
		if err == nil {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return strings.Join(out, "\n")
}

// A change of case only: on exFAT (and APFS) every file is renamed
// through a temporary name, never refused as taken by itself.
func TestRenameGameCaseOnly(t *testing.T) {
	l, sd := newLibrary(t)
	_, err := renameGame(t, l, sd, EncodeID("Game Boy Advance (GBA)/Starlight Courier.gba"), "STARLIGHT courier")
	if err != nil {
		t.Fatal(err)
	}
	mustExist(t, sd, "Roms/Game Boy Advance (GBA)/STARLIGHT courier.gba", "Saves/GBA/STARLIGHT courier.gba.sav",
		".userdata/shared/GBA-gpsp/STARLIGHT courier.gba.st0", "Roms/Game Boy Advance (GBA)/.media/STARLIGHT courier.png")
	mustNotExist(t, sd, "Roms/Game Boy Advance (GBA)/Starlight Courier.gba", "Saves/GBA/Starlight Courier.gba.sav")
}

func TestRenameGameRefusals(t *testing.T) {
	l, sd := newLibrary(t)
	id := EncodeID("Game Boy Advance (GBA)/Starlight Courier.gba")
	if _, err := l.PlanRename(id, strings.Repeat("é", 127)); !errors.Is(err, ErrInvalidName) {
		t.Errorf("too long: %v", err)
	}
	// A folder game whose discs another playlist lists.
	writeSD(t, sd, "Roms/Sony PlayStation (PS)/Other.m3u", "Mossy Hollow/Mossy Hollow (Disc 1).cue\n")
	_, err := l.PlanRename(EncodeID("Sony PlayStation (PS)/Mossy Hollow"), "Mossy Valley")
	var conflict *RenameConflictError
	if !errors.As(err, &conflict) || !conflict.Shared {
		t.Errorf("shared discs: %v", err)
	}
	// A non-default save format.
	writeSD(t, sd, ".userdata/shared/minuisettings.txt", "saveFormat=1\n")
	if _, err := l.PlanRename(id, "Star"); !errors.Is(err, osadapter.ErrUnsupportedFormat) {
		t.Errorf("format: %v", err)
	}
	if _, err := l.PlanRename(EncodeID("Game Boy Advance (GBA)/Nope.gba"), "Star"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing game: %v", err)
	}
	if _, err := l.PlanRename("../x", "Star"); !errors.Is(err, ErrInvalidID) {
		t.Errorf("bad id: %v", err)
	}
}

func TestRenameGameKnulli(t *testing.T) {
	l, trash, sd := knulliLibrary(t, t.TempDir())
	plan, err := l.PlanRename(EncodeID("gba/Starlight Courier.gba"), "Star Courier")
	if err != nil {
		t.Fatal(err)
	}
	if err := trash.RenameGroup(plan.Old.Name, plan.Steps); err != nil {
		t.Fatal(err)
	}
	mustExist(t, sd, "roms/gba/Star Courier.gba", "saves/gba/Star Courier.srm", "saves/gba/Star Courier.rtc",
		"saves/gba/Star Courier.state", "saves/gba/Star Courier.state1", "saves/gba/Star Courier.state1.png",
		"roms/gba/images/Star Courier-image.png")
	mustNotExist(t, sd, "saves/gba/Starlight Courier.srm", "saves/gba/Starlight Courier.state1.png")
}
