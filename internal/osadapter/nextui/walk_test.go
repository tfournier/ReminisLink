package nextui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

// gameCounts returns the games of sys as CountGames, WalkGames,
// WalkSummaries and a recursive ListFolder count them.
func gameCounts(t *testing.T, a *Adapter, sys osadapter.System) (count, walked, summarized, listed int) {
	t.Helper()
	count, err := a.CountGames(sys)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.WalkGames(context.Background(), sys, func(osadapter.Game) error { walked++; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := a.WalkSummaries(context.Background(), sys, func(string, osadapter.GameSummary) error { summarized++; return nil }); err != nil {
		t.Fatal(err)
	}
	var list func(rel string)
	list = func(rel string) {
		l, err := a.ListFolder(sys, rel)
		if err != nil {
			t.Fatal(err)
		}
		listed += len(l.Games)
		for _, f := range l.Folders {
			list(f.Path)
		}
	}
	list(sys.Folder)
	return
}

// The walks, the count and the listings share one depth rule: a folder
// (game or not) deeper than MaxFolderDepth is never looked into.
func TestFolderDepthRule(t *testing.T) {
	for _, depth := range []int{MaxFolderDepth - 1, MaxFolderDepth, MaxFolderDepth + 1} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			a, sd := newAdapter(t)
			sys, err := a.System(gba)
			if err != nil {
				t.Fatal(err)
			}
			before, _, _, _ := gameCounts(t, a, sys)
			var parts []string
			for i := 0; i < depth; i++ {
				parts = append(parts, fmt.Sprintf("d%02d", i))
			}
			nested := "Roms/" + gba + "/" + strings.Join(parts, "/")
			testutil.WriteFile(t, sd, nested+"/Final/Final.cue", "c")         // folder-cue game
			testutil.WriteFile(t, sd, nested+"/Multi/Multi.m3u", "Multi.cue") // folder-m3u game
			testutil.WriteFile(t, sd, nested+"/Loose.gba", "r")               // file game
			count, walked, summarized, listed := gameCounts(t, a, sys)
			if count != walked || count != summarized || count != listed {
				t.Errorf("depth %d: CountGames %d, WalkGames %d, WalkSummaries %d, ListFolder %d", depth, count, walked, summarized, listed)
			}
			want := before + 3 // all three games
			switch {
			case depth == MaxFolderDepth:
				want = before + 1 // the loose file only: its folders are not looked into
			case depth > MaxFolderDepth:
				want = before
			}
			if count != want {
				t.Errorf("depth %d: %d games, want %d", depth, count, want)
			}
		})
	}
}

// A walk stops soon after its context is cancelled, even in one folder
// of many games.
func TestWalkCancelledMidFolder(t *testing.T) {
	a, sd := newAdapter(t)
	for i := 0; i < 300; i++ {
		testutil.WriteFile(t, sd, fmt.Sprintf("Roms/%s/Bulk %03d.gba", gba, i), "r")
	}
	sys, err := a.System(gba)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	err = a.WalkSummaries(ctx, sys, func(string, osadapter.GameSummary) error {
		n++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || n > 1 {
		t.Errorf("WalkSummaries: %v after %d games", err, n)
	}
	ctx, cancel = context.WithCancel(context.Background())
	n = 0
	err = a.WalkGames(ctx, sys, func(osadapter.Game) error {
		n++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || n > 1 {
		t.Errorf("WalkGames: %v after %d games", err, n)
	}
}

// countReads counts the directory reads until the test ends.
func countReads(t *testing.T) map[string]int {
	reads := map[string]int{}
	orig := readDir
	readDir = func(dir string) ([]fs.DirEntry, error) {
		reads[dir]++
		return orig(dir)
	}
	t.Cleanup(func() { readDir = orig })
	return reads
}

// A walk reads each folder of the system once, even when several
// playlists list discs of the same subfolder.
func TestWalkSummariesReadsEachFolderOnce(t *testing.T) {
	a, sd := newAdapter(t)
	for _, name := range []string{"Saga One", "Saga Two", "Saga Three"} {
		testutil.WriteFile(t, sd, "Roms/"+ps+"/"+name+".m3u", "Discs/"+name+" (Disc 1).cue\ndiscs/"+name+" (Disc 2).cue")
		testutil.WriteFile(t, sd, "Roms/"+ps+"/Discs/"+name+" (Disc 1).cue", "c")
		testutil.WriteFile(t, sd, "Roms/"+ps+"/Discs/"+name+" (Disc 2).cue", "c")
	}
	sys, err := a.System(ps)
	if err != nil {
		t.Fatal(err)
	}
	reads := countReads(t)
	sizes := map[string]int64{}
	if err := a.WalkSummaries(context.Background(), sys, func(_ string, g osadapter.GameSummary) error {
		sizes[g.FileName] = g.Size
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for dir, n := range reads {
		if n > 1 && strings.HasPrefix(dir, filepath.Join(sd, "Roms")) {
			t.Errorf("%s read %d times", dir, n)
		}
	}
	if reads[filepath.Join(sd, "Roms", ps, "Discs")] != 1 {
		t.Errorf("Discs read %d times", reads[filepath.Join(sd, "Roms", ps, "Discs")])
	}
	// The discs still count in the size of each playlist.
	for _, name := range []string{"Saga One", "Saga Two", "Saga Three"} {
		if want := int64(len("Discs/"+name+" (Disc 1).cue\ndiscs/"+name+" (Disc 2).cue") + 2); sizes[name+".m3u"] != want {
			t.Errorf("%s: size %d, want %d", name, sizes[name+".m3u"], want)
		}
	}
}
