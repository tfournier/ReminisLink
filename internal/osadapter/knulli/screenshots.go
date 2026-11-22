package knulli

import (
	"regexp"
	"time"

	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
)

// Screenshot names written into /userdata/screenshots (knulli-linux
// 57afa662):
//   - RetroArch (screenshot_directory, libretroRetroarchCustom.py:91),
//     auto_screenshot_filename left at its default: "<content name without
//     its extension>-<%y%m%d>-<%H%M%S>.png" (RetroArch v1.22.2
//     tasks/task_screenshot.c, libretro-common file_path.c
//     fill_str_dated_filename);
//   - the hotkey of Knulli (board/fsoverlay/usr/bin/knulli-screenshot):
//     "knulli-screenshot_<W>x<H>_<%Y%m%d>_<%H>h<%M>m<%S>s.png";
//   - the scripts of knulli-resolution (other boards):
//     "screenshot-<%Y-%m-%d>_<%H>h<%M%S>.png" or
//     "screenshot-<%Y.%m.%d>-<%H>h<%M>.<%S>.png".
//
// A " (N)" before the extension is the free name of a restore from the
// trash (M18): the name still gives the game and the date.
var (
	retroarchShot = regexp.MustCompile(`^(.+)-(\d{2})(\d{2})(\d{2})-(\d{2})(\d{2})(\d{2})(?: \(\d+\))?\.(?i:png)$`)
	hotkeyShot    = regexp.MustCompile(`^knulli-screenshot_\d+x\d+_(\d{4})(\d{2})(\d{2})_(\d{2})h(\d{2})m(\d{2})s(?: \(\d+\))?\.(?i:png)$`)
	scriptShot    = regexp.MustCompile(`^screenshot-(?:all-)?(\d{4})[-.](\d{2})[-.](\d{2})[_-](\d{2})h(\d{2})\.?(\d{2})(?: \(\d+\))?\.(?i:png|pbm)$`)
)

// ParseScreenshotName reads a Knulli screenshot name in the time zone loc
// (the hotkey and script names give no game).
func ParseScreenshotName(name string, loc *time.Location) osadapter.ScreenshotName {
	if m := hotkeyShot.FindStringSubmatch(name); m != nil {
		if at, ok := nextui.DateOf(loc, m[1], m[2], m[3], m[4], m[5], m[6]); ok {
			return osadapter.ScreenshotName{TakenAt: at}
		}
		return osadapter.ScreenshotName{}
	}
	if m := scriptShot.FindStringSubmatch(name); m != nil {
		if at, ok := nextui.DateOf(loc, m[1], m[2], m[3], m[4], m[5], m[6]); ok {
			return osadapter.ScreenshotName{TakenAt: at}
		}
		return osadapter.ScreenshotName{}
	}
	if m := retroarchShot.FindStringSubmatch(name); m != nil {
		// %y: RetroArch writes the year without its century.
		if at, ok := nextui.DateOf(loc, "20"+m[2], m[3], m[4], m[5], m[6], m[7]); ok {
			return osadapter.ScreenshotName{Game: m[1], TakenAt: at}
		}
	}
	return osadapter.ScreenshotName{}
}
