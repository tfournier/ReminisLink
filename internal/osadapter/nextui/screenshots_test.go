package nextui

import (
	"testing"
	"time"
)

func TestParseScreenshotName(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("no time zone data:", err)
	}
	cases := []struct {
		name, game string
		at         time.Time
	}{
		{"Sky Garden Quest.2026-11-21-18-30-00.png", "Sky Garden Quest", time.Date(2026, 11, 21, 18, 30, 0, 0, paris)},
		{"Pokémon Rouge.2026-01-02-03-04-05.PNG", "Pokémon Rouge", time.Date(2026, 1, 2, 3, 4, 5, 0, paris)},
		// The display name keeps its inner dots.
		{"Sky Garden Quest Vol. 3.2026-11-21-18-30-00.png", "Sky Garden Quest Vol. 3", time.Date(2026, 11, 21, 18, 30, 0, 0, paris)},
		// Restored from the trash under a free name (M18).
		{"Sky Garden Quest.2026-11-21-18-30-00 (2).png", "Sky Garden Quest", time.Date(2026, 11, 21, 18, 30, 0, 0, paris)},
		{"Sky Garden Quest (Europe).2026-11-21-18-30-00 (12).png", "Sky Garden Quest (Europe)", time.Date(2026, 11, 21, 18, 30, 0, 0, paris)},
		// A date that does not exist, or no date: nothing.
		{"Game.2026-02-30-10-00-00.png", "", time.Time{}},
		{"Game.2026-11-21-25-00-00.png", "", time.Time{}},
		{"Game.png", "", time.Time{}},
		{".2026-11-21-18-30-00.png", "", time.Time{}},
		{"Game.2026-11-21-18-30-00.jpg", "", time.Time{}},
		{"Game.2026-11-21-18-30-00.png.tmp", "", time.Time{}},
		// Skipped by the change to summer time in Paris.
		{"Game.2026-03-29-02-30-00.png", "", time.Time{}},
	}
	for _, c := range cases {
		got := ParseScreenshotName(c.name, paris)
		if got.Game != c.game || !got.TakenAt.Equal(c.at) {
			t.Errorf("%q: got %q %v, want %q %v", c.name, got.Game, got.TakenAt, c.game, c.at)
		}
	}
}

func TestScreenshotsRoot(t *testing.T) {
	r := RootsForSD("/sd", "tg5050")
	if r.Screenshots != "/sd/Screenshots" {
		t.Fatalf("Screenshots = %q", r.Screenshots)
	}
	a := New(r, nil)
	if got := a.ScreenshotName("Game.2026-11-21-18-30-00.png"); got.Game != "Game" {
		t.Fatalf("adapter reading: %+v", got)
	}
}
