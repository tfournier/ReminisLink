package knulli

import (
	"testing"
	"time"
)

func TestParseScreenshotName(t *testing.T) {
	loc := time.FixedZone("console", 3600)
	cases := []struct {
		name, game string
		at         time.Time
	}{
		{"Sky Garden Quest-261121-183000.png", "Sky Garden Quest", time.Date(2026, 11, 21, 18, 30, 0, 0, loc)},
		{"Sky Garden Quest (Europe)-261121-183000.png", "Sky Garden Quest (Europe)", time.Date(2026, 11, 21, 18, 30, 0, 0, loc)},
		{"knulli-screenshot_1024x768_20261121_18h30m05s.png", "", time.Date(2026, 11, 21, 18, 30, 5, 0, loc)},
		{"screenshot-2026-11-21_18h3005.png", "", time.Date(2026, 11, 21, 18, 30, 5, 0, loc)},
		{"screenshot-2026.11.21-18h30.05.png", "", time.Date(2026, 11, 21, 18, 30, 5, 0, loc)},
		{"screenshot-all-2026.11.21-18h30.05.png", "", time.Date(2026, 11, 21, 18, 30, 5, 0, loc)},
		// A NextUI name, a date that does not exist, no date: nothing.
		{"Game.2026-11-21-18-30-00.png", "", time.Time{}},
		{"Game-261321-183000.png", "", time.Time{}},
		{"knulli-screenshot_1024x768_20261121_25h30m05s.png", "", time.Time{}},
		{"photo.png", "", time.Time{}},
	}
	for _, c := range cases {
		got := ParseScreenshotName(c.name, loc)
		if got.Game != c.game || !got.TakenAt.Equal(c.at) {
			t.Errorf("%q: got %q %v, want %q %v", c.name, got.Game, got.TakenAt, c.game, c.at)
		}
	}
}

func TestScreenshotsRoot(t *testing.T) {
	r := RootsForSD("/userdata")
	if r.Screenshots != "/userdata/screenshots" {
		t.Fatalf("Screenshots = %q", r.Screenshots)
	}
	a := New(r, nil)
	if got := a.ScreenshotName("Game-261121-183000.png"); got.Game != "Game" {
		t.Fatalf("adapter reading: %+v", got)
	}
}
