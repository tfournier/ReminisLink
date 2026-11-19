package library

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tfournier/ReminisLink/internal/confine"
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
