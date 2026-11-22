package nextui

import (
	"path/filepath"
	"time"

	"github.com/tfournier/ReminisLink/internal/osadapter"
)

// Layout is what another OS changes in the layout this adapter reads
// (milestone M20). The adapter walks a folder per system holding ROM
// files, .m3u playlists and multi-disc folders, with a save folder per
// system; NextUI and Knulli (internal/osadapter/knulli) share that and
// differ by the names of the systems, of the saves and states, of the
// art and by the device. Each nil field keeps the NextUI rule
// (docs/specs/05-console-pak.md §3).
type Layout struct {
	// OS is the "os" value of the API ("nextui" when empty).
	OS string
	// System parses a folder of Roms/ (NextUI: "Name (TAG)").
	System func(folder string) (osadapter.System, bool)
	// EmulatorInstalled reports whether the OS can launch the games of a
	// tag (NextUI: an emulator pak <TAG>.pak).
	EmulatorInstalled func(tag string) bool
	// Skip reports whether an entry of a system folder (not hidden) is
	// not part of the library: the media folders of the OS. NextUI hides
	// its media in hidden folders and skips nothing more.
	Skip func(name string) bool
	// GameFile reports whether a file of a system folder is a game of
	// that system, on top of the rule of IsGameFile (Knulli: the
	// extensions of the system). nil: any file IsGameFile accepts.
	GameFile func(sys osadapter.System, name string) bool

	// SaveName and RTCName name the save and the clock file of a ROM in
	// Saves/<TAG> (NextUI: "<ROM file name>.sav" and ".rtc").
	SaveName func(romName string) string
	RTCName  func(romName string) string

	// States, when set, replaces the NextUI states (folder per core,
	// "<ROM file name>.st<N>", thumbnails in .minui).
	States StateRules

	// CaseFromFS: names are compared as the file system of each folder
	// does (Knulli: /userdata may be ext4, case-sensitive, or exFAT). When
	// false, names are always compared ignoring case (NextUI: the card is
	// exFAT, and the tests run on any file system).
	CaseFromFS bool

	// NoCheats: the OS has no per-game cheat file the pak can write;
	// CheatPath is then "" and GameFiles finds none.
	NoCheats bool

	// ArtDir and ArtName give where the art of a game is read and
	// written (NextUI: "<folder of the entry>/.media/<entry name without
	// its last extension>.png"). ArtDir receives the folder that holds
	// the entry.
	ArtDir  func(sys osadapter.System, folder string) string
	ArtName func(g osadapter.Game) string

	// CheckFormat refuses a save or a state layout the adapter does not
	// write (NextUI: the format settings of minuisettings.txt).
	CheckSaveFormat  func(g osadapter.Game) error
	CheckStateFormat func(g osadapter.Game) error

	// ScreenshotName reads the file name of a screenshot (M22; NextUI:
	// "<display name>.<YYYY-MM-DD-HH-MM-SS>.png").
	ScreenshotName func(name string, loc *time.Location) osadapter.ScreenshotName

	// DeviceInfo and Battery describe the console.
	DeviceInfo func() (osadapter.DeviceInfo, error)
	Battery    func() *osadapter.Battery
}

// StateRules name the save states of an OS that keeps them in one folder
// per tag, named after a key derived from the ROM file name.
type StateRules interface {
	// Dir is the state folder of a tag ("" when unknown).
	Dir(tag string) string
	// Key is the part of a state file name derived from the ROM file name.
	Key(romName string) string
	// Name is the file name of the state of a slot.
	Name(key string, slot int) string
	// Parse reads a file name of the state folder: ok is false when it is
	// not a state of a slot (an automatic state, a thumbnail...).
	Parse(name string) (key string, slot int, ok bool)
	// Thumbnail is the file name of the thumbnail of a slot ("" when the
	// OS has none).
	Thumbnail(key string, slot int) string
	// Side lists the names of the files tied to a slot (its thumbnail),
	// existing or not.
	Side(key string, slot int) []string
	// IsSide reports whether a file name of the state folder is a side
	// file of key (GameFiles lists them), comparing exactly when exact
	// (a case-sensitive folder) and ignoring case otherwise.
	IsSide(name, key string, exact bool) bool
	// ResumeSlot is the slot the OS writes on its own (ok false: none).
	ResumeSlot() (slot int, ok bool)
}

// NewWithLayout returns the adapter of an OS that shares the NextUI
// engine with the rules of l (nil fields keep NextUI's).
func NewWithLayout(roots osadapter.Roots, env Env, l Layout) *Adapter {
	a := New(roots, env)
	a.layout = l
	return a
}

func (a *Adapter) systemOf(folder string) (osadapter.System, bool) {
	if hidden(folder) {
		return osadapter.System{}, false
	}
	if a.layout.System != nil {
		return a.layout.System(folder)
	}
	return parseSystem(folder)
}

// skip reports whether an entry of a system folder is left out of the
// library: hidden, or a media folder of the OS.
func (a *Adapter) skip(name string) bool {
	return hidden(name) || (a.layout.Skip != nil && a.layout.Skip(name))
}

// IsSystemGameFile is gameFile (osadapter.Adapter).
func (a *Adapter) IsSystemGameFile(sys osadapter.System, name string) bool {
	return a.gameFile(sys, name)
}

// gameFile reports whether a file named name, in a folder of sys, is
// listed as a game.
func (a *Adapter) gameFile(sys osadapter.System, name string) bool {
	return a.IsGameFile(name) && (a.layout.GameFile == nil || a.layout.GameFile(sys, name))
}

func (a *Adapter) saveName(romName string) string {
	if a.layout.SaveName != nil {
		return a.layout.SaveName(romName)
	}
	return romName + ".sav"
}

func (a *Adapter) rtcName(romName string) string {
	if a.layout.RTCName != nil {
		return a.layout.RTCName(romName)
	}
	return romName + ".rtc"
}

func (a *Adapter) cheatName(romName string) string { return romName + ".cht" }

// mediaDir is the folder of the art of the entries of folder.
func (a *Adapter) mediaDir(sys osadapter.System, folder string) string {
	if a.layout.ArtDir != nil {
		return a.layout.ArtDir(sys, folder)
	}
	return filepath.Join(folder, ".media")
}

// StateKey is stateKey (osadapter.Adapter).
func (a *Adapter) StateKey(romName string) string { return a.stateKey(romName) }

// stateKey is the part of the state file names derived from romName.
func (a *Adapter) stateKey(romName string) string {
	if a.layout.States != nil {
		return a.layout.States.Key(romName)
	}
	return romName
}

// parseState reads a file name of a state folder.
func (a *Adapter) parseState(name string) (key string, slot int, ok bool) {
	if a.layout.States != nil {
		return a.layout.States.Parse(name)
	}
	return parseState(name)
}

// artFileName is the file name of the art of g.
func (a *Adapter) artFileName(g osadapter.Game) string {
	if a.layout.ArtName != nil {
		return a.layout.ArtName(g)
	}
	return artName(g) + ".png"
}

// ScreenshotName is the reading of a screenshot name of the layout
// (osadapter.Adapter), in the time zone of the console.
func (a *Adapter) ScreenshotName(name string) osadapter.ScreenshotName {
	if a.layout.ScreenshotName != nil {
		return a.layout.ScreenshotName(name, time.Local)
	}
	return ParseScreenshotName(name, time.Local)
}

// CaseFromFS reports whether names are compared as the file system of each
// folder does (Knulli on ext4, M20) rather than always ignoring case.
func (a *Adapter) CaseFromFS() bool { return a.layout.CaseFromFS }
