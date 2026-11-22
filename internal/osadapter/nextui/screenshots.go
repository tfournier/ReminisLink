package nextui

import (
	"regexp"
	"strconv"
	"time"

	"github.com/tfournier/ReminisLink/internal/osadapter"
)

// nextuiShot is the name minarch gives a screenshot (NextUI b6ec1343,
// workspace/all/minarch/ma_menu.c Menu_screenshot):
// "<display name>.<%Y-%m-%d-%H-%M-%S>.png", the display name being the
// alias of map.txt or the ROM name without its extensions nor its
// trailing parentheses (utils.c getDisplayName). The extension is written
// in lowercase; any case is read.
var nextuiShot = regexp.MustCompile(`^(.+)\.(\d{4})-(\d{2})-(\d{2})-(\d{2})-(\d{2})-(\d{2})\.(?i:png)$`)

// ParseScreenshotName reads a NextUI screenshot name in the time zone
// loc. A name of another form gives nothing (zero ScreenshotName).
func ParseScreenshotName(name string, loc *time.Location) osadapter.ScreenshotName {
	m := nextuiShot.FindStringSubmatch(name)
	if m == nil {
		return osadapter.ScreenshotName{}
	}
	at, ok := DateOf(loc, m[2], m[3], m[4], m[5], m[6], m[7])
	if !ok {
		return osadapter.ScreenshotName{}
	}
	return osadapter.ScreenshotName{Game: m[1], TakenAt: at}
}

// DateOf builds a date from its decimal fields (year, month, day, hour,
// minute, second) in loc; ok is false for a date that does not exist
// (2026-02-30, 25:00): time.Date would normalize it.
func DateOf(loc *time.Location, fields ...string) (time.Time, bool) {
	if len(fields) != 6 {
		return time.Time{}, false
	}
	var n [6]int
	for i, f := range fields {
		v, err := strconv.Atoi(f)
		if err != nil {
			return time.Time{}, false
		}
		n[i] = v
	}
	t := time.Date(n[0], time.Month(n[1]), n[2], n[3], n[4], n[5], 0, loc)
	if t.Year() != n[0] || int(t.Month()) != n[1] || t.Day() != n[2] || t.Hour() != n[3] || t.Minute() != n[4] || t.Second() != n[5] {
		// A wall time skipped by a change of daylight saving time is
		// normalized too: the name is then read as nothing.
		return time.Time{}, false
	}
	return t, true
}
