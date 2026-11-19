package library

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tfournier/ReminisLink/internal/confine"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

func TestAllGamesLibrary(t *testing.T) {
	l, _ := newLibrary(t)
	all, err := l.AllGames(context.Background(), MaxAllGames)
	if err != nil {
		t.Fatal(err)
	}
	systems, err := l.Systems()
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, s := range systems {
		total += s.GameCount
	}
	if len(all.Games) != total || all.Truncated {
		t.Errorf("%d games (truncated %v), GET /systems counts %d", len(all.Games), all.Truncated, total)
	}
	// Truncated just below the total, not at it.
	if cut, err := l.AllGames(context.Background(), total-1); err != nil || !cut.Truncated || len(cut.Games) != total-1 {
		t.Errorf("limit %d: %d games, truncated %v, %v", total-1, len(cut.Games), cut.Truncated, err)
	}
	if cut, err := l.AllGames(context.Background(), total); err != nil || cut.Truncated || len(cut.Games) != total {
		t.Errorf("limit %d: %d games, truncated %v, %v", total, len(cut.Games), cut.Truncated, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.AllGames(ctx, MaxAllGames); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
}

func TestOpenFile(t *testing.T) {
	l, sd := newLibrary(t)
	gameID := EncodeID(gba + "/Starlight Courier.gba")
	d, err := l.Game(gameID)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range d.Files {
		f, fi, name, err := l.OpenFile(gameID, file.FileID)
		if err != nil {
			t.Fatalf("%s: %v", file.Path, err)
		}
		data, _ := io.ReadAll(f)
		f.Close()
		want, _ := os.ReadFile(filepath.Join(sd, filepath.FromSlash(file.Path)))
		if string(data) != string(want) || fi.Size() != file.SizeBytes || name != filepath.Base(file.Path) {
			t.Errorf("%s: %q %d %q", file.Path, data, fi.Size(), name)
		}
	}
	for id, want := range map[string]error{
		"":                                      ErrInvalidID,
		"a.b":                                   ErrInvalidID,
		EncodeID("Saves/GBA/x.sav"):             ErrNotFound,
		EncodeID("Roms/" + gba + "/readme.txt"): ErrNotFound,
	} {
		if _, _, _, err := l.OpenFile(gameID, id); !errors.Is(err, want) {
			t.Errorf("%q: %v, want %v", id, err, want)
		}
	}
	if _, _, _, err := l.OpenFile(EncodeID(gba+"/Missing.gba"), d.Files[0].FileID); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing game: %v", err)
	}
	// The save folder of the tag is a link: refused, as missing.
	saves := filepath.Join(sd, "Saves", "GBA")
	if err := os.Rename(saves, saves+"-real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("GBA-real", saves); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	d, err = l.Game(gameID)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range d.Files {
		if file.Role != "save" {
			continue
		}
		_, _, _, err := l.OpenFile(gameID, file.FileID)
		if !errors.Is(err, ErrNotFound) || !errors.Is(err, confine.ErrOutside) {
			t.Errorf("save through a link: %v", err)
		}
		return
	}
	t.Error("save not listed through the link")
}

// cancelAfterWalk cancels the request context once the walk of the
// system folder last returned (the client went away while its games were
// read).
type cancelAfterWalk struct {
	osadapter.Adapter
	last   string
	cancel context.CancelFunc
}

func (c cancelAfterWalk) WalkSummaries(ctx context.Context, sys osadapter.System, fn func(string, osadapter.GameSummary) error) error {
	err := c.Adapter.WalkSummaries(ctx, sys, fn)
	if sys.Folder == c.last {
		c.cancel()
	}
	return err
}

// The listing is not built for a client gone during the walk, even of the
// last system.
func TestAllGamesCancelledDuringWalk(t *testing.T) {
	sd := testutil.CopySDCard(t)
	roots := nextui.RootsForSD(sd, "tg5040")
	ctx, cancel := context.WithCancel(context.Background())
	l := New(cancelAfterWalk{nextui.New(roots, nil), ps, cancel}, store.Layout{Root: roots.Data})
	if _, err := l.AllGames(ctx, MaxAllGames); !errors.Is(err, context.Canceled) {
		t.Errorf("AllGames: %v", err)
	}
}

// gameHeap keeps the n first games by path, in the order of lessFold,
// never holding more than n.
func TestGameHeapKeepsFirst(t *testing.T) {
	var paths []string
	for i := 0; i < 200; i++ {
		paths = append(paths, fmt.Sprintf("S/%c%03d.gba", 'a'+rune(i%3)*('A'-'a'), (i*37)%200))
	}
	rand.New(rand.NewSource(1)).Shuffle(len(paths), func(i, j int) { paths[i], paths[j] = paths[j], paths[i] })
	want := slices.Clone(paths)
	slices.SortFunc(want, func(a, b string) int {
		if lessFold(a, b) {
			return -1
		}
		if lessFold(b, a) {
			return 1
		}
		return 0
	})
	for _, n := range []int{0, 1, 7, 199, 200, 500} {
		h := &gameHeap{}
		for _, p := range paths {
			h.offer(foundGame{g: osadapter.GameSummary{Game: osadapter.Game{Path: p}}}, n)
			if h.Len() > n {
				t.Fatalf("n=%d: %d kept", n, h.Len())
			}
		}
		var got []string
		for _, f := range h.sorted() {
			got = append(got, f.g.Path)
		}
		if exp := want[:min(n, len(want))]; !slices.Equal(got, exp) && !(len(got) == 0 && len(exp) == 0) {
			t.Errorf("n=%d: %v, want %v", n, got, exp)
		}
	}
}

// The cheat files are not read any more once the client went away.
func TestAllGamesCancelledDuringCheatCounts(t *testing.T) {
	l, sd := newLibrary(t)
	for _, name := range []string{"Café Rêverie (France).gba", "Lantern Keeper [b1].gba"} {
		testutil.WriteFile(t, sd, "Cheats/GBA/"+name+".cht", "cheats = 0\n")
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	orig := countCheats
	countCheats = func(p string) int {
		calls++
		cancel()
		return orig(p)
	}
	t.Cleanup(func() { countCheats = orig })
	if _, err := l.AllGames(ctx, MaxAllGames); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Errorf("AllGames: %v after %d cheat files read", err, calls)
	}
}

// denyFolder makes a folder unreadable until the test ends (skipped where
// permissions do not apply, as for root).
func denyFolder(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("permissions not enforced here")
	}
}

// An unreadable system is left out of GET /games, as GET /systems leaves
// it out; the games of the other systems are listed.
func TestAllGamesUnreadableSystem(t *testing.T) {
	l, sd := newLibrary(t)
	denyFolder(t, filepath.Join(sd, "Roms", ps))
	all, err := l.AllGames(context.Background(), MaxAllGames)
	if err != nil {
		t.Fatal(err)
	}
	systems, err := l.Systems()
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, s := range systems {
		if s.Folder == ps {
			t.Errorf("GET /systems lists the unreadable %s", ps)
		}
		total += s.GameCount
	}
	if len(all.Games) != total || total == 0 {
		t.Errorf("%d games, GET /systems counts %d", len(all.Games), total)
	}
	for _, g := range all.Games {
		if g.SystemID == EncodeID(ps) {
			t.Errorf("game of the unreadable system: %s", g.FileName)
		}
	}
}
