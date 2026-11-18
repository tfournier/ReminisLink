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
	// StatesDirName is the folder of the save states under the shared
	// userdata.
	// TODO(M0): MinUI convention (.userdata/shared/.minui/<TAG>/), not yet
	// checked on NextUI.
	StatesDirName = ".minui"
)

// Env reads an environment variable ("" when unset). os.Getenv fits.
type Env func(key string) string

// RootsFromEnv returns the SD card folders from the NextUI environment
// variables (SDCARD_PATH, ROMS_PATH, SAVES_PATH, CHEATS_PATH, USERDATA_PATH,
// SHARED_USERDATA_PATH, LOGS_PATH, PLATFORM), with the NextUI defaults.
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
	// TODO(M0): check that NextUI exports SHARED_USERDATA_PATH and that it
	// is .userdata/shared.
	shared := or("SHARED_USERDATA_PATH", filepath.Join(sd, ".userdata", "shared"))
	return osadapter.Roots{
		SD:       sd,
		Roms:     or("ROMS_PATH", filepath.Join(sd, "Roms")),
		Saves:    or("SAVES_PATH", filepath.Join(sd, "Saves")),
		States:   filepath.Join(shared, StatesDirName),
		Cheats:   or("CHEATS_PATH", filepath.Join(sd, "Cheats")),
		Userdata: userdata,
		Shared:   shared,
		Data:     filepath.Join(shared, DataDirName),
		Logs:     or("LOGS_PATH", filepath.Join(userdata, "logs")),
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
