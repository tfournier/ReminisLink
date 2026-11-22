package nextui

import (
	"path/filepath"

	"github.com/tfournier/ReminisLink/internal/osadapter"
)

// NextUI defaults (docs/specs/05-console-pak.md §3.1), as in Cheat Manager.
const (
	DefaultSDCard   = "/mnt/SDCARD"
	DefaultPlatform = "tg5040"
	// DataDirName is the ReminisLink folder under the shared userdata.
	DataDirName = "ReminisLink"
	// ScreenshotsDirName is the screenshots folder at the root of the
	// card (M22).
	ScreenshotsDirName = "Screenshots"
	// MinuiDirName is the folder of the OS data under the shared userdata
	// (state thumbnails, recent games). The states themselves are in
	// <shared userdata>/<TAG>-<core>/ (NextUI source, minarch ma_core.c).
	MinuiDirName = ".minui"
	// SettingsFileName is the NextUI settings file under the shared
	// userdata (saveFormat, stateFormat).
	SettingsFileName = "minuisettings.txt"
	// VersionFile is the NextUI release file, relative to the SD card root
	// (written by the NextUI makefile, replaced by each update).
	VersionFile = ".system/version.txt"
)

// Env reads an environment variable ("" when unset). os.Getenv fits.
type Env func(key string) string

// RootsFromEnv returns the SD card folders from the NextUI environment
// variables (SDCARD_PATH, ROMS_PATH, SAVES_PATH, CHEATS_PATH, USERDATA_PATH,
// SHARED_USERDATA_PATH, SYSTEM_PATH, LOGS_PATH, PLATFORM), with the NextUI
// defaults (MinUI.pak/launch.sh of NextUI).
func RootsFromEnv(env Env) osadapter.Roots {
	or := func(key, def string) string {
		if v := env(key); v != "" {
			return v
		}
		return def
	}
	sd := or("SDCARD_PATH", DefaultSDCard)
	platform := or("PLATFORM", DefaultPlatform)
	userdata := or("USERDATA_PATH", filepath.Join(sd, ".userdata", platform))
	shared := or("SHARED_USERDATA_PATH", filepath.Join(sd, ".userdata", "shared"))
	return osadapter.Roots{
		SD:       sd,
		Roms:     or("ROMS_PATH", filepath.Join(sd, "Roms")),
		Saves:    or("SAVES_PATH", filepath.Join(sd, "Saves")),
		States:   shared,
		Minui:    filepath.Join(shared, MinuiDirName),
		System:   or("SYSTEM_PATH", filepath.Join(sd, ".system", platform)),
		Emus:     filepath.Join(sd, "Emus", platform),
		Tools:    filepath.Join(sd, "Tools", platform),
		Cheats:   or("CHEATS_PATH", filepath.Join(sd, "Cheats")),
		Userdata: userdata,
		Shared:   shared,
		Data:     filepath.Join(shared, DataDirName),
		Logs:     or("LOGS_PATH", filepath.Join(userdata, "logs")),
		// minarch writes SDCARD_PATH "/Screenshots" (a constant of every
		// platform: /mnt/SDCARD), ma_menu.c Menu_screenshot.
		Screenshots: filepath.Join(sd, ScreenshotsDirName),
	}
}

// RootsForSD returns the folders of an SD card mounted at sd, ignoring the
// path variables of the environment (--sd option). platform names the
// userdata folder.
func RootsForSD(sd, platform string) osadapter.Roots {
	if platform == "" {
		platform = DefaultPlatform
	}
	return RootsFromEnv(func(key string) string {
		switch key {
		case "SDCARD_PATH":
			return sd
		case "PLATFORM":
			return platform
		}
		return ""
	})
}
