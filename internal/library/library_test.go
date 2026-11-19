package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

const (
	gba = "Game Boy Advance (GBA)"
	ps  = "Sony PlayStation (PS)"
)

func newLibrary(t testing.TB) (*Library, string) {
	t.Helper()
	sd := testutil.CopySDCard(t)
	return libraryFor(sd), sd
}

func libraryFor(sd string) *Library {
	roots := nextui.RootsForSD(sd, "tg5040")
	return New(nextui.New(roots, nil), store.Layout{Root: roots.Data})
}

func TestLibrarySystems(t *testing.T) {
	l, _ := newLibrary(t)
	systems, err := l.Systems()
	if err != nil {
		t.Fatal(err)
	}
	if len(systems) != 4 {
		t.Fatalf("systems = %+v", systems)
	}
	s := systems[1]
	if s.ID != EncodeID(gba) || s.ID != "R2FtZSBCb3kgQWR2YW5jZSAoR0JBKQ" || s.Label != "Game Boy Advance" || s.Tag != "GBA" || s.GameCount != 6 {
		t.Errorf("GBA = %+v", s)
	}
}

func TestLibraryListGames(t *testing.T) {
	l, _ := newLibrary(t)
	listing, err := l.ListGames(EncodeID(gba), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Folders) != 1 || len(listing.Games) != 4 {
		t.Fatalf("listing = %+v", listing)
	}
	var sc Game
	for _, g := range listing.Games {
		if g.FileName == "Starlight Courier.gba" {
			sc = g
		}
	}
	if sc.ID != EncodeID(gba+"/Starlight Courier.gba") || sc.CheatCount != 2 || sc.StateCount != 3 || !sc.HasSave || !sc.HasArt ||
		sc.Kind != "file" || sc.LockedStateCount != 0 || sc.ModifiedAt.Nanosecond() != 0 || sc.ModifiedAt.Location() != time.UTC {
		t.Errorf("Starlight Courier = %+v", sc)
	}
	// Descend with the folder id of the listing.
	hacks := listing.Folders[0]
	sub, err := l.ListGames(EncodeID(gba), hacks.ID)
	if err != nil || len(sub.Games) != 1 || len(sub.Folders) != 1 {
		t.Fatalf("Hacks = %+v, %v", sub, err)
	}
	deeper, err := l.ListGames(EncodeID(gba), sub.Folders[0].ID)
	if err != nil || len(deeper.Games) != 1 || deeper.Games[0].Name != "Tiny Trial" {
		t.Fatalf("Deeper = %+v, %v", deeper, err)
	}
}

func TestLibraryListGamesErrors(t *testing.T) {
	l, _ := newLibrary(t)
	sysID := EncodeID(gba)
	for _, tc := range []struct {
		name, systemID, folderID string
		want                     error
	}{
		{"bad system id", "not base64!", "", ErrInvalidID},
		{"system id with a slash", EncodeID(gba + "/Hacks"), "", ErrInvalidID},
		{"traversal system id", EncodeID(".."), "", ErrInvalidID},
		{"unknown system", EncodeID("Missing (X)"), "", ErrNotFound},
		{"not a system", EncodeID("No Tag Folder"), "", ErrNotFound},
		{"folder of another system", sysID, EncodeID("Game Boy Advance (MGBA)/Hacks"), ErrInvalidID},
		{"folder id of the system itself", sysID, EncodeID(gba), ErrInvalidID},
		{"folder with a common prefix", sysID, EncodeID(gba + "x/Hacks"), ErrInvalidID},
		{"bad folder id", sysID, "%%%", ErrInvalidID},
		{"traversal folder id", sysID, EncodeID(gba + "/../Sony PlayStation (PS)"), ErrInvalidID},
		{"missing folder", sysID, EncodeID(gba + "/Missing"), ErrNotFound},
		{"hidden folder", sysID, EncodeID(gba + "/.media"), ErrNotFound},
		{"game as folder", sysID, EncodeID(gba + "/Starlight Courier.gba"), ErrNotFound},
		{"multi-disc game as folder", EncodeID(ps), EncodeID(ps + "/Mossy Hollow"), ErrNotFound},
	} {
		if _, err := l.ListGames(tc.systemID, tc.folderID); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestLibraryLockedStateCount(t *testing.T) {
	l, sd := newLibrary(t)
	locks := filepath.Join(nextui.RootsForSD(sd, "").Data, "locks", "GBA")
	// Folder name with another case than the ROM: exFAT ignores case.
	testutil.WriteFile(t, locks, "starlight courier.gba/slot0.st", "x")
	testutil.WriteFile(t, locks, "starlight courier.gba/slot0.json", "{}")
	testutil.WriteFile(t, locks, "starlight courier.gba/slot1.st", "x")
	testutil.WriteFile(t, locks, "stray.txt", "x")
	listing, err := l.ListGames(EncodeID(gba), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range listing.Games {
		want := 0
		if g.FileName == "Starlight Courier.gba" {
			want = 2
		}
		if g.LockedStateCount != want {
			t.Errorf("%s: lockedStateCount = %d, want %d", g.FileName, g.LockedStateCount, want)
		}
	}
}

func TestLibraryGame(t *testing.T) {
	l, _ := newLibrary(t)
	d, err := l.Game(EncodeID(gba + "/Starlight Courier.gba"))
	if err != nil {
		t.Fatal(err)
	}
	if d.SystemID != EncodeID(gba) || d.Name != "Starlight Courier" || d.Kind != "file" {
		t.Errorf("detail = %+v", d)
	}
	var roles []string
	var total int64
	for _, f := range d.Files {
		r := f.Role
		if f.Slot != nil {
			r += fmt.Sprint(*f.Slot)
		}
		roles = append(roles, r)
		total += f.SizeBytes
	}
	if fmt.Sprint(roles) != "[rom save state0 state1 state9 cheats art]" || total != d.TotalBytes {
		t.Errorf("roles = %v, total %d/%d", roles, total, d.TotalBytes)
	}
	if d.Files[0].Path != "Roms/"+gba+"/Starlight Courier.gba" {
		t.Errorf("rom path = %q", d.Files[0].Path)
	}

	for _, tc := range []struct {
		id   string
		want error
	}{
		{"", ErrInvalidID},
		{"R2FtZQ==", ErrInvalidID},
		{EncodeID("/etc/passwd"), ErrInvalidID},
		{EncodeID(gba + "/../../x"), ErrInvalidID},
		{EncodeID(gba + "//Starlight Courier.gba"), ErrInvalidID},
		{EncodeID(gba + "/Missing.gba"), ErrNotFound},
		{EncodeID(gba), ErrNotFound},
		{EncodeID(gba + "/Hacks"), ErrNotFound},
		{EncodeID(gba + "/readme.txt"), ErrNotFound},
	} {
		if _, err := l.Game(tc.id); !errors.Is(err, tc.want) {
			t.Errorf("Game(%q) = %v, want %v", tc.id, err, tc.want)
		}
	}
}

func TestLibraryArt(t *testing.T) {
	l, _ := newLibrary(t)
	f, err := l.Art(EncodeID(gba + "/Starlight Courier.gba"))
	if err != nil || f.Size == 0 {
		t.Fatalf("art = %+v, %v", f, err)
	}
	if _, err := l.Art(EncodeID(gba + "/Lantern Keeper [b1].gba")); !errors.Is(err, ErrNotFound) {
		t.Errorf("no art: %v", err)
	}
	if _, err := l.Art("!"); !errors.Is(err, ErrInvalidID) {
		t.Errorf("bad id: %v", err)
	}
}

func TestLibrarySymlinkEscape(t *testing.T) {
	l, sd := newLibrary(t)
	outside := t.TempDir()
	testutil.WriteFile(t, outside, "Secret (SEC)/Secret.gba", "x")
	if err := os.Symlink(filepath.Join(outside, "Secret (SEC)"), filepath.Join(sd, "Roms", "Secret (SEC)")); err != nil {
		t.Skip("symlinks not supported:", err)
	}
	if _, err := l.ListGames(EncodeID("Secret (SEC)"), ""); !errors.Is(err, ErrInvalidID) {
		t.Errorf("linked system: %v", err)
	}
	if _, err := l.Game(EncodeID("Secret (SEC)/Secret.gba")); !errors.Is(err, ErrInvalidID) {
		t.Errorf("game through a link: %v", err)
	}
}

// NFD names (as the macOS Finder writes them) are listed and read back
// with their own bytes; no Unicode normalization is applied (03 §5).
func TestLibraryNFDNames(t *testing.T) {
	l, sd := newLibrary(t)
	nfd := "Café Amélié.gba" // "Café Amélié" with combining accents
	nfc := "Café Amélié.gba"
	testutil.WriteFile(t, sd, "Roms/"+gba+"/"+nfd, "nfd-rom")
	testutil.WriteFile(t, sd, "Saves/GBA/"+nfd+".sav", "nfd-save")

	listing, err := l.ListGames(EncodeID(gba), "")
	if err != nil {
		t.Fatal(err)
	}
	var found *Game
	for i, g := range listing.Games {
		if g.FileName == nfd {
			found = &listing.Games[i]
		}
		if g.FileName == nfc {
			t.Errorf("the NFD name came back as NFC")
		}
	}
	if found == nil {
		t.Fatalf("NFD game not listed: %+v", listing.Games)
	}
	if found.ID != EncodeID(gba+"/"+nfd) || !found.HasSave {
		t.Errorf("NFD game = %+v", *found)
	}
	d, err := l.Game(found.ID)
	if err != nil {
		t.Fatalf("read back the NFD game: %v", err)
	}
	if d.FileName != nfd || len(d.Files) != 2 || d.Files[1].Role != "save" {
		t.Errorf("NFD detail = %+v", d)
	}
	// The NFC spelling is another identifier, and no file has exactly that
	// name: not found, even on APFS which would find the file.
	if _, err := l.Game(EncodeID(gba + "/" + nfc)); !errors.Is(err, ErrNotFound) {
		t.Errorf("NFC id of an NFD file: %v, want ErrNotFound", err)
	}
}

func TestLibraryCaseMismatch(t *testing.T) {
	l, _ := newLibrary(t)
	if _, err := l.Game(EncodeID(gba + "/STARLIGHT COURIER.gba")); !errors.Is(err, ErrNotFound) {
		t.Errorf("game: %v", err)
	}
	if _, err := l.ListGames(EncodeID("game boy advance (gba)"), ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("system: %v", err)
	}
	if _, err := l.ListGames(EncodeID(gba), EncodeID(gba+"/hacks")); !errors.Is(err, ErrNotFound) {
		t.Errorf("folder: %v", err)
	}
	// The system part of a folderId is compared byte for byte with the
	// route: another case is a folder of another system.
	if _, err := l.ListGames(EncodeID(gba), EncodeID("game boy advance (gba)/Hacks")); !errors.Is(err, ErrInvalidID) {
		t.Errorf("folder of another case of the system: %v", err)
	}
}

// GET /systems/{id}/games must stay fast: 500 games with a save, states
// and cheats each (docs/specs/04-api-v1.md: 300 ms on the console).
func TestLibraryListGamesPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("performance test")
	}
	sd := t.TempDir()
	const n = 500
	// A realistic .cht: 300 cheats, about 38 KB, as RetroArch writes it.
	var cht strings.Builder
	cht.WriteString("cheats = 300\n\n")
	for c := 0; c < 300; c++ {
		fmt.Fprintf(&cht, "cheat%d_desc = \"Cheat number %d with a long description\"\ncheat%d_code = \"0200%04X+00FF\"\ncheat%d_enable = false\n\n", c, c, c, c, c)
	}
	if cht.Len() < 30<<10 {
		t.Fatalf("cht too small: %d", cht.Len())
	}
	for i := 0; i < n; i++ {
		rom := fmt.Sprintf("Generated Game %03d.gba", i)
		testutil.WriteFile(t, sd, "Roms/Perf (PERF)/"+rom, "rom")
		testutil.WriteFile(t, sd, "Saves/PERF/"+rom+".sav", "sav")
		testutil.WriteFile(t, sd, ".userdata/shared/PERF-core/"+rom+".st0", "st")
		testutil.WriteFile(t, sd, ".userdata/shared/PERF-core/"+rom+".st9", "st")
		testutil.WriteFile(t, sd, "Cheats/PERF/"+rom+".cht", cht.String())
		testutil.WriteFile(t, sd, "Roms/Perf (PERF)/.media/"+fmt.Sprintf("Generated Game %03d.png", i), "png")
	}
	l := libraryFor(sd)
	start := time.Now()
	listing, err := l.ListGames(EncodeID("Perf (PERF)"), "")
	elapsed := time.Since(start)
	if err != nil || len(listing.Games) != n {
		t.Fatalf("listing: %d games, %v", len(listing.Games), err)
	}
	for _, g := range listing.Games {
		if !g.HasSave || g.StateCount != 2 || g.CheatCount != 300 || !g.HasArt {
			t.Fatalf("game = %+v", g)
		}
	}
	t.Logf("500 games listed in %v", elapsed)
	if elapsed > 300*time.Millisecond {
		t.Errorf("500 games listed in %v, want < 300 ms", elapsed)
	}
}
