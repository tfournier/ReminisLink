// Package osadapter hides how an OS lays out its files on the SD card. The
// rest of the pak never builds an OS path itself: it asks the Adapter
// (docs/specs/05-console-pak.md §3).
package osadapter

import (
	"errors"
	"time"
)

// ErrNotFound means the system, folder or game does not exist (or is not
// one: hidden, no tag, ignored file...).
var ErrNotFound = errors.New("not found")

// Roots are the main folders of the SD card, as absolute paths.
type Roots struct {
	SD       string // SD card root
	Roms     string // ROM folders, one per system
	Saves    string // in-game saves
	States   string // emulator save states
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
)

// Game is a ROM (file, .m3u, or folder with a .m3u).
type Game struct {
	System   System
	Path     string   // relative to Roms/, "/" separator (source of the gameId)
	FileName string   // last component of Path (file or folder name)
	Name     string   // display name
	Kind     GameKind // file, m3u or folder-m3u
	// ROMName is the file name the emulator data (save, states, cheats) is
	// named after: the ROM file, or the .m3u of a folder-m3u game.
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

// GameFiles are all the files of a game. Missing ones are nil or empty.
type GameFiles struct {
	ROM    []File // the ROM, or the .m3u and its discs
	Save   *File
	States []StateFile // sorted by slot
	Cheats *File
	Art    *File
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
}

// Adapter is the file layout of one OS. Paths given to it (game and folder
// paths relative to Roms/) must already be validated by the caller
// (library.ResolveID); the adapter does not check them again.
type Adapter interface {
	Name() string
	Roots() Roots

	// Systems lists the systems, sorted by label (case-insensitive).
	Systems() ([]System, error)
	// System returns the system stored in this folder of Roms/, or
	// ErrNotFound.
	System(folder string) (System, error)
	// CountGames counts the games of a system, subfolders included.
	CountGames(sys System) (int, error)
	// ListFolder lists a folder of a system: rel is relative to Roms/ and
	// is sys.Folder for the root of the system. ErrNotFound if it is not a
	// folder of the system (a game folder is not a folder).
	ListFolder(sys System, rel string) (Listing, error)
	// Game returns the game at rel (relative to Roms/), or ErrNotFound.
	Game(rel string) (Game, error)
	// GameFiles finds every file of a game.
	GameFiles(g Game) (GameFiles, error)

	// Where each file of a game is (or would be) stored.
	SavePath(g Game) string
	StatePath(g Game, slot int) string
	StateThumbnailPath(g Game, slot int) string // "" when the OS has none
	ResumeSlot() (slot int, ok bool)
	CheatPath(g Game) string
	ArtPath(g Game) string

	DeviceInfo() (DeviceInfo, error)
}
