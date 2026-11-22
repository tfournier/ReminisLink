// Package knulli is the adapter of the Knulli firmware (milestone M20,
// experimental): a Batocera-based Linux for handhelds whose user data is
// in /userdata (the SHARE partition, exFAT or ext4), with one folder per
// system under roms/ and RetroArch saves and states under saves/<system>/
// (docs/specs/05-console-pak.md §3.3).
//
// It reuses the engine of the NextUI adapter (folders, playlists,
// multi-disc folders) with the Knulli rules (nextui.Layout). Sources:
// github.com/knulli-cfw/knulli-linux 57afa662 —
// package/system/knulli-configgen/configgen/configgen/batoceraPaths.py
// (paths), generators/libretro/libretroConfig.py and
// libretroRetroarchCustom.py (savefile_directory and savestate_directory
// /userdata/saves/<system>, sort_savefiles_enable false, thumbnails),
// package/emulationstation/knulli-es-system/es_systems.yml (systems,
// extensions), board/*/fsoverlay/etc/init.d/rcS (/boot/boot/knulli.board),
// package/system/knulli-scripts/scripts/knulli-version
// (/usr/share/knulli/knulli.version), board/fsoverlay/usr/bin/knulli-battery-check
// (battery); github.com/knulli-cfw/batocera-emulationstation f7c5ae10
// es-app/src/FileData.cpp:170 findLocalArt (art), SystemData.cpp:278 and
// es-core/src/utils/FileSystemUtil.cpp:1096 (hidden files).
package knulli

import (
	_ "embed"
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
)

const (
	// OSName is the "os" value of the API and of the Bonjour TXT record.
	OSName = "knulli"
	// DefaultUserdata is the root of the user data (batoceraPaths.py).
	DefaultUserdata = "/userdata"
	// DataDirName is the ReminisLink folder under /userdata/system.
	DataDirName = "ReminisLink"
	// ScreenshotsDirName is the screenshots folder of /userdata (M22).
	ScreenshotsDirName = "screenshots"
	// DefaultBoardFile names the device (knulli.board: rg35xx-plus,
	// trimui-brick...).
	DefaultBoardFile = "/boot/boot/knulli.board"
	// DefaultVersionFile is the Knulli release (knulli-version).
	DefaultVersionFile = "/usr/share/knulli/knulli.version"
	// DefaultPowerSupplyDir holds the battery.
	DefaultPowerSupplyDir = "/sys/class/power_supply"
	// WirelessInterface is the Wi-Fi interface (knulli-wifi).
	WirelessInterface = "wlan0"
	// MaxVersionBytes bounds osVersion (it goes in the Bonjour TXT record).
	MaxVersionBytes = 64
)

//go:embed systems.json
var systemsJSON []byte

// systemInfo is a system of es_systems.yml.
type systemInfo struct {
	Folder     string   `json:"folder"`
	Name       string   `json:"name"`
	Hardware   string   `json:"hardware"`
	Extensions []string `json:"extensions"`
}

// systems are the systems whose folder holds games, by folder name.
var systems = func() map[string]systemInfo {
	var doc struct {
		Systems []systemInfo `json:"systems"`
	}
	if err := json.Unmarshal(systemsJSON, &doc); err != nil {
		panic("knulli: systems.json: " + err.Error())
	}
	out := map[string]systemInfo{}
	for _, s := range doc.Systems {
		switch s.Hardware {
		case "tool", "emulator", "system", "library":
			// Tools (where the ReminisLink launcher itself lives), the
			// emulator launchers and the image viewers hold no game.
			continue
		}
		out[s.Folder] = s
	}
	return out
}()

// mediaFolders are the folders EmulationStation and its scrapers keep in a
// system folder: they are not game folders.
var mediaFolders = map[string]bool{
	"images": true, "videos": true, "manuals": true, "media": true, "bezels": true,
	"downloaded_images": true, "downloaded_videos": true, "thumbnails": true,
}

// Boards maps the content of knulli.board to the API model (Model enum of
// protocol/openapi.yaml). Only the Allwinner A133 (TrimUI) and H700
// (Anbernic) boards, which NextUI also runs on, have a model; the other
// Knulli boards report "desktop" until the contract gets their models.
var Boards = map[string]string{
	"trimui-brick": "brick", "trimui-brick-pro": "brickpro", "trimui-smart-pro": "smartpro",
	"rg28xx": "h700", "rg34xx": "h700", "rg34xx-sp": "h700", "rg35xx-h": "h700", "rg35xx-plus": "h700",
	"rg35xx-pro": "h700", "rg35xx-sp": "h700", "rg40xx-h": "h700", "rg40xx-v": "h700", "rg-cubexx": "h700",
}

// RootsFromEnv returns the Knulli folders. The variables of the NextUI
// launcher are honoured (SDCARD_PATH for /userdata, ROMS_PATH, SAVES_PATH,
// CHEATS_PATH, USERDATA_PATH for /userdata/system, LOGS_PATH) so the
// tests and the --sd option work the same way.
func RootsFromEnv(env nextui.Env) osadapter.Roots {
	or := func(key, def string) string {
		if v := env(key); v != "" {
			return v
		}
		return def
	}
	sd := or("SDCARD_PATH", DefaultUserdata)
	system := or("USERDATA_PATH", filepath.Join(sd, "system"))
	saves := or("SAVES_PATH", filepath.Join(sd, "saves"))
	return osadapter.Roots{
		SD:       sd,
		Roms:     or("ROMS_PATH", filepath.Join(sd, "roms")),
		Saves:    saves,
		States:   saves,
		Cheats:   or("CHEATS_PATH", filepath.Join(sd, "cheats")),
		Userdata: system,
		Shared:   system,
		Data:     filepath.Join(system, DataDirName),
		Pak:      filepath.Join(or("ROMS_PATH", filepath.Join(sd, "roms")), "tools", "."+DataDirName),
		Logs:     or("LOGS_PATH", filepath.Join(system, "logs")),
		// batoceraPaths.py SCREENSHOTS, RetroArch screenshot_directory
		// (libretroRetroarchCustom.py), knulli-screenshot.
		Screenshots: filepath.Join(sd, ScreenshotsDirName),
		// No paks on Knulli: Minui, System, Emus and Tools stay empty
		// (GET /paks lists nothing, an installation is refused).
	}
}

// RootsForSD returns the folders of a /userdata copy mounted at sd.
func RootsForSD(sd string) osadapter.Roots {
	return RootsFromEnv(func(key string) string {
		if key == "SDCARD_PATH" {
			return sd
		}
		return ""
	})
}

// Adapter is the Knulli adapter: the NextUI engine with the Knulli layout,
// plus Battery for the status of the pak.
type Adapter struct {
	*nextui.Adapter
}

var _ osadapter.Adapter = Adapter{}

// device reads the console files (paths from the environment for the
// tests: KNULLI_BOARD_FILE, KNULLI_VERSION_FILE, POWER_SUPPLY_PATH).
type device struct {
	board, version, power string
}

// New returns the Knulli adapter for these roots. env gives the paths of
// the console files (nil: the defaults).
func New(roots osadapter.Roots, env nextui.Env) Adapter {
	if env == nil {
		env = func(string) string { return "" }
	}
	or := func(key, def string) string {
		if v := env(key); v != "" {
			return v
		}
		return def
	}
	d := device{
		board:   or("KNULLI_BOARD_FILE", DefaultBoardFile),
		version: or("KNULLI_VERSION_FILE", DefaultVersionFile),
		power:   or("POWER_SUPPLY_PATH", DefaultPowerSupplyDir),
	}
	st := states{dir: roots.States}
	return Adapter{nextui.NewWithLayout(roots, env, nextui.Layout{
		OS:     OSName,
		System: system,
		EmulatorInstalled: func(tag string) bool {
			// es_systems.cfg is generated per board from the cores built
			// for it; the card does not tell which: every known system is
			// taken as launchable.
			_, ok := systems[tag]
			return ok
		},
		Skip: func(name string) bool { return mediaFolders[strings.ToLower(name)] },
		// /userdata may be ext4 (case-sensitive) or exFAT.
		CaseFromFS:     true,
		GameFile:       gameFile,
		ScreenshotName: ParseScreenshotName,
		// RetroArch names the save after the content without its last
		// extension (savefile_directory = /userdata/saves/<system>).
		SaveName: func(rom string) string { return stem(rom) + ".srm" },
		RTCName:  func(rom string) string { return stem(rom) + ".rtc" },
		States:   st,
		// RetroArch reads the cheats of a game from
		// /userdata/cheats/saves/<core>/<game>.cht (cheat_settings_path):
		// the core is chosen per system or game in Knulli's settings, so
		// the pak cannot name that file (as NextUI without its emulator
		// pak). Cheats are not offered on Knulli.
		NoCheats: true,
		ArtDir: func(sys osadapter.System, _ string) string {
			// EmulationStation looks for local art in the images folder of
			// the system root only (findLocalArt, mStartPath): two ROMs of
			// the same name in two folders share it (SharedFiles keeps it
			// when one is deleted).
			return filepath.Join(roots.Roms, sys.Folder, "images")
		},
		ArtName:    func(g osadapter.Game) string { return stem(path.Base(g.Path)) + "-image.png" },
		DeviceInfo: d.info,
		Battery:    d.battery,
	})}
}

// system returns the system of a folder of roms/: a known system of
// es_systems.yml, matched exactly (EmulationStation opens
// /userdata/roms/<folder>, which ext4 matches case-sensitively).
func system(folder string) (osadapter.System, bool) {
	s, ok := systems[folder]
	if !ok {
		return osadapter.System{}, false
	}
	return osadapter.System{Folder: folder, Label: s.Name, Tag: folder}, true
}

// gameFile reports whether name has one of the extensions of the system
// (EmulationStation lists only those, ignoring case).
func gameFile(sys osadapter.System, name string) bool {
	s, ok := systems[sys.Folder]
	if !ok || len(s.Extensions) == 0 {
		return true
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	for _, e := range s.Extensions {
		if e == ext {
			return true
		}
	}
	return false
}

// stem is a file name without its last extension.
func stem(name string) string {
	if s := strings.TrimSuffix(name, filepath.Ext(name)); s != "" {
		return s
	}
	return name
}

// states are RetroArch's: <saves>/<system>/<content>.state for slot 0,
// .state<N> for the others, .state.auto for the automatic one, and a
// thumbnail <state file>.png (savestate_thumbnail_enable true in
// libretroRetroarchCustom.py).
type states struct{ dir string }

// stateRe matches "<content>.state" and "<content>.state<N>" (N up to
// 99, the slots the pak accepts; Knulli's incremental states may go
// beyond and are then not listed).
var stateRe = regexp.MustCompile(`^(.+)\.state(\d{0,2})$`)

func (s states) Dir(tag string) string     { return filepath.Join(s.dir, tag) }
func (s states) Key(romName string) string { return stem(romName) }

func (s states) Name(key string, slot int) string {
	if slot == 0 {
		return key + ".state"
	}
	return key + ".state" + strconv.Itoa(slot)
}

func (s states) Parse(name string) (string, int, bool) {
	m := stateRe.FindStringSubmatch(name)
	if m == nil {
		return "", 0, false
	}
	if m[2] == "" {
		return m[1], 0, true
	}
	n, err := strconv.Atoi(m[2])
	if err != nil || (m[2][0] == '0') {
		return "", 0, false // ".state0" or ".state01": not a RetroArch name
	}
	return m[1], n, true
}

func (s states) Thumbnail(key string, slot int) string { return s.Name(key, slot) + ".png" }
func (s states) Side(key string, slot int) []string    { return []string{s.Thumbnail(key, slot)} }

// IsSide: the thumbnails of the states of key and its automatic state
// (with its thumbnail), which go with the game when it is deleted.
func (s states) IsSide(name, key string, exact bool) bool {
	n, k := name, key+".state"
	if !exact {
		n, k = strings.ToLower(n), strings.ToLower(k)
	}
	rest, ok := strings.CutPrefix(n, k)
	if !ok {
		return false
	}
	if rest == ".auto" || rest == ".auto.png" {
		return true
	}
	digits, ok := strings.CutSuffix(rest, ".png")
	if !ok {
		return false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ResumeSlot: RetroArch's automatic state is ".state.auto", not a slot.
func (s states) ResumeSlot() (int, bool) { return 0, false }

// info describes the console from knulli.board and knulli.version; off
// the console (no board file) the model is desktop.
func (d device) info() (osadapter.DeviceInfo, error) {
	board := readLine(d.board, 64)
	model, ok := Boards[board]
	if !ok {
		model = nextui.ModelDesktop
	}
	platform := board
	if platform == "" {
		platform = OSName
	}
	var iface string
	if model != nextui.ModelDesktop {
		iface = WirelessInterface
	}
	var version *string
	if v := readLine(d.version, MaxVersionBytes); v != "" {
		version = &v
	}
	return osadapter.DeviceInfo{
		Model: model, Platform: platform, OS: OSName, OSVersion: version,
		Battery: d.battery(), WirelessInterface: iface,
	}, nil
}

// battery reads the first power supply whose name holds BAT or bat, as
// knulli-battery-check does (capacity, status).
func (d device) battery() *osadapter.Battery {
	entries, err := os.ReadDir(d.power)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !strings.Contains(e.Name(), "BAT") && !strings.Contains(e.Name(), "bat") {
			continue
		}
		dir := filepath.Join(d.power, e.Name())
		n, err := strconv.Atoi(readLine(filepath.Join(dir, "capacity"), 16))
		if err != nil || n < 0 {
			continue
		}
		return &osadapter.Battery{Percent: min(n, 100), Charging: readLine(filepath.Join(dir, "status"), 32) == "Charging"}
	}
	return nil
}

// readLine returns the first line of a small file, trimmed, "" when
// missing, longer than max or not printable ASCII.
func readLine(p string, max int) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	data, _ := io.ReadAll(io.LimitReader(f, 1024))
	line, _, _ := strings.Cut(string(data), "\n")
	line = strings.TrimSpace(line)
	if len(line) > max {
		return ""
	}
	for i := 0; i < len(line); i++ {
		if line[i] < ' ' || line[i] > '~' {
			return ""
		}
	}
	return line
}
