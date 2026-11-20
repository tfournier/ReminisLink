// Package osadapter hides how an OS lays out its files on the SD card. The
// rest of the pak never builds an OS path itself: it asks the Adapter
// (docs/specs/05-console-pak.md §3).
package osadapter

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound means the system, folder or game does not exist (or is not
// one: hidden, no tag, ignored file...).
var ErrNotFound = errors.New("not found")

// ErrUnsupportedFormat means the saves or the states of the card use a
// layout the adapter does not write (a non-default format chosen in the OS
// settings): the pak refuses to change them rather than guess.
var ErrUnsupportedFormat = errors.New("unsupported save or state format")

// Roots are the main folders of the SD card, as absolute paths.
type Roots struct {
	SD       string // SD card root
	Roms     string // ROM folders, one per system
	Saves    string // in-game saves
	States   string // parent of the per-core state folders (<TAG>-<core>/)
	Minui    string // OS data shared between platforms (state thumbnails...)
	System   string // OS system folder of the platform (emulator paks)
	Emus     string // extra emulator paks of the platform
	Tools    string // tool paks of the platform (M12)
	Cheats   string // RetroArch .cht files
	Userdata string // userdata of the platform (logs...)
	Shared   string // userdata shared between platforms
	Data     string // ReminisLink data (config, clients, backups...)
	Logs     string // log folder
}

// System is a folder of Roms/ with a "(TAG)" suffix.
type System struct {
	Folder string // folder name under Roms/ (source of the systemId)
	Label  string // display name: no sort prefix, no tag
	Tag    string // tag between parentheses: names the save, state and cheat folders
}

// GameKind is how a game is stored.
type GameKind string

// Game kinds (docs/specs/04-api-v1.md, GET /systems/{systemId}/games).
const (
	KindFile      GameKind = "file"       // a single ROM file
	KindM3U       GameKind = "m3u"        // a .m3u file
	KindFolderM3U GameKind = "folder-m3u" // a folder holding a .m3u of the same name
	// KindFolderCue is a folder holding a .cue of the same name and no such
	// .m3u: NextUI launches the .cue (nextui.c hasCue, openDirectory).
	KindFolderCue GameKind = "folder-cue"
)

// IsFolder reports whether a game is a folder (folder-m3u, folder-cue).
func (k GameKind) IsFolder() bool { return k == KindFolderM3U || k == KindFolderCue }

// Game is a ROM (file, .m3u, or folder with a .m3u).
type Game struct {
	System   System
	Path     string   // relative to Roms/, "/" separator (source of the gameId)
	FileName string   // last component of Path (file or folder name)
	Name     string   // display name
	Kind     GameKind // file, m3u, folder-m3u or folder-cue
	// ROMName is the file name the emulator data (save, states, cheats) is
	// named after: the ROM file, the .m3u of a folder-m3u game or the .cue
	// of a folder-cue game.
	ROMName string
}

// Folder is a subfolder of a system that is not a game.
type Folder struct {
	Path      string // relative to Roms/, "/" separator (source of the folderId)
	Name      string
	GameCount int // games in the folder and its subfolders
}

// GameSummary is a game as listed in a folder, with what can be found
// without opening its files.
type GameSummary struct {
	Game
	Size       int64     // ROM size (sum of the files for a folder-m3u)
	ModTime    time.Time // ROM (or .m3u) modification time
	HasSave    bool
	StateCount int
	CheatFile  string // absolute path of the .cht file, "" if none
	HasArt     bool
}

// Listing is the content of a folder.
type Listing struct {
	Folders []Folder
	Games   []GameSummary
}

// File is a file that belongs to a game.
type File struct {
	Path    string // absolute path
	Rel     string // relative to the SD card root, "/" separator (informative only)
	Size    int64
	ModTime time.Time
}

// StateFile is a save state of a slot.
type StateFile struct {
	File
	Slot int
}

// StateRef is a save state found on the card, with the tag and the ROM
// file name it belongs to (the game itself may be gone: DELETE /games with
// keepSave keeps the states).
type StateRef struct {
	Tag     string
	ROMName string
	Slot    int
	File    File
}

// GameFiles are all the files of a game. Missing ones are nil or empty.
type GameFiles struct {
	ROM  []File // the ROM, or the .m3u and its discs
	Save *File
	// RTC is the clock file written with the save (NextUI: .rtc).
	RTC    *File
	States []StateFile // sorted by slot
	Cheats *File
	Art    *File
	// StateSide are the OS files tied to the states (thumbnails, discs of
	// multi-disc states, last slot used). Not listed by the API.
	StateSide []File
}

// SaveDir is one read of the save folder of a tag (Adapter.SaveDir).
type SaveDir interface {
	// Files returns the save and the clock file of g (a game of that
	// tag), as GameFiles would find them; nil when missing.
	Files(g Game) (save, rtc *File)
}

// Battery is the battery state.
type Battery struct {
	Percent  int
	Charging bool
}

// DeviceInfo describes the console. Unknown values are nil.
type DeviceInfo struct {
	Model     string // brick, smartpro... or desktop
	Platform  string // tg5040...
	OS        string // nextui
	OSVersion *string
	Battery   *Battery
	// WirelessInterface is the network interface of the Wi-Fi ("" when
	// unknown: any interface).
	WirelessInterface string
	// SSID is the name of the Wi-Fi network.
	SSID *string
}

// Adapter is the file layout of one OS. Paths given to it (game and folder
// paths relative to Roms/) must already be validated by the caller
// (library.ResolveID); the adapter does not check them again.
type Adapter interface {
	Name() string
	Roots() Roots

	// Systems lists the systems, sorted by label (case-insensitive).
	Systems() ([]System, error)
	// SystemFolders lists the folders Systems may list, without checking
	// they hold a visible item: for a walk of every system, which finds no
	// game in the others and so reads each folder once.
	SystemFolders() ([]System, error)
	// System returns the system stored in this folder of Roms/, or
	// ErrNotFound.
	System(folder string) (System, error)
	// EmulatorInstalled reports whether an emulator for the tag is
	// installed (NextUI only shows the systems that have one).
	EmulatorInstalled(tag string) bool
	// CountGames counts the games of a system, subfolders included.
	CountGames(sys System) (int, error)
	// ListFolder lists a folder of a system: rel is relative to Roms/ and
	// is sys.Folder for the root of the system. ErrNotFound if it is not a
	// folder of the system (a game folder is not a folder).
	ListFolder(sys System, rel string) (Listing, error)
	// WalkGames calls fn for every game of sys, subfolders included, with
	// the games ListFolder lists, reading each folder once. It stops with
	// the error of fn, or ctx.Err() when ctx is done.
	WalkGames(ctx context.Context, sys System, fn func(Game) error) error
	// WalkSummaries calls fn for every game WalkGames finds, with what
	// ListFolder says about it and the folder that holds it (relative to
	// Roms/, sys.Folder at the root of the system), reading each folder
	// and each data folder of the system once. A system folder that
	// cannot be read has no game (Systems leaves it out). It stops with
	// the error of fn, or ctx.Err() when ctx is done.
	WalkSummaries(ctx context.Context, sys System, fn func(folder string, g GameSummary) error) error
	// Game returns the game at rel (relative to Roms/), or ErrNotFound.
	Game(rel string) (Game, error)
	// GameFiles finds every file of a game.
	GameFiles(g Game) (GameFiles, error)
	// ROMFiles returns the files the game itself uses (absolute paths): the
	// ROM, or the playlist (or .cue) of a multi-disc game, the discs it
	// lists and the tracks of their .cue sheets.
	ROMFiles(g Game) map[string]bool
	// SharedFiles returns the files of g (absolute paths) that another
	// playlist (.m3u) of its system also lists: deleting g keeps them, so
	// the other game stays playable.
	SharedFiles(g Game) map[string]bool
	// IsGameFile reports whether a file with this name, in a system
	// folder, is listed as a game (not hidden, not an ignored extension).
	IsGameFile(name string) bool

	// SaveDir reads the save folder of a tag once, to find the save and
	// the clock file of many games with the same rule as GameFiles (GET
	// /saves).
	SaveDir(tag string) SaveDir

	// StateFiles lists every save state of the card (all systems).
	// StateFiles stops with ctx.Err() when ctx is done.
	StateFiles(ctx context.Context) ([]StateRef, error)

	// Where each file of a game is (or would be) stored. The emulator data
	// (save, states, cheats) depends only on g.System.Tag and g.ROMName,
	// so these may be called with a Game holding only those two fields
	// (the lock check at start, for a game deleted with keepSave).
	SavePath(g Game) string
	// StatePath is "" when the state folder of the system cannot be found
	// (no emulator pak): states can then be neither read nor written.
	StatePath(g Game, slot int) string
	StateThumbnailPath(g Game, slot int) string // "" when the OS has none
	// ResumeSlot is the slot written automatically by the OS (ok false
	// when there is none, or the state format is not supported).
	ResumeSlot() (slot int, ok bool)
	// CheckSaveFormat and CheckStateFormat return an error wrapping
	// ErrUnsupportedFormat when the OS settings make the save or the states
	// of g files the adapter does not name (a non-default format, or a
	// zipped game named after its extracted file). g may hold only its tag
	// and ROM file name.
	CheckSaveFormat(g Game) error
	CheckStateFormat(g Game) error
	// StateSideFiles are the OS files tied to a state slot (thumbnail,
	// disc of a multi-disc state), existing or not.
	StateSideFiles(g Game, slot int) []string
	// GameExists reports whether a game with this ROM file name exists in
	// a system folder of this tag (names compared ignoring case).
	GameExists(tag, romName string) bool
	CheatPath(g Game) string
	ArtPath(g Game) string

	DeviceInfo() (DeviceInfo, error)
}
