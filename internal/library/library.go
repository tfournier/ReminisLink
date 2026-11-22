package library

import (
	"container/heap"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tfournier/ReminisLink/internal/cheats"
	"github.com/tfournier/ReminisLink/internal/confine"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/store"
)

// ErrNotFound means the system, folder or game does not exist (404 not_found).
var ErrNotFound = errors.New("not found")

// Library answers the library routes (docs/specs/04-api-v1.md, Bibliothèque)
// from the OS adapter. Its errors wrap ErrInvalidID, ErrNotFound or are
// internal errors.
type Library struct {
	adapter osadapter.Adapter
	layout  store.Layout
	// protected are places of ReminisLink the SD browser never changes
	// (ProtectPaths, M23).
	protected []string
}

// New returns a library reading the SD card through a, with the
// ReminisLink data (locks) in layout.
func New(a osadapter.Adapter, layout store.Layout) *Library {
	return &Library{adapter: a, layout: layout}
}

// System is an item of GET /systems.
type System struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Folder    string `json:"folder"`
	Tag       string `json:"tag"`
	GameCount int    `json:"gameCount"`
	// EmulatorInstalled: the OS has an emulator for the tag (decision D6,
	// docs/specs/m0/questions.md).
	EmulatorInstalled bool `json:"emulatorInstalled"`
}

// Folder is a subfolder in GET /systems/{systemId}/games.
type Folder struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	GameCount int    `json:"gameCount"`
}

// Game is a game in GET /systems/{systemId}/games.
type Game struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	FileName         string    `json:"fileName"`
	Kind             string    `json:"kind"`
	SizeBytes        int64     `json:"sizeBytes"`
	ModifiedAt       time.Time `json:"modifiedAt"`
	HasSave          bool      `json:"hasSave"`
	StateCount       int       `json:"stateCount"`
	LockedStateCount int       `json:"lockedStateCount"`
	CheatCount       int       `json:"cheatCount"`
	HasArt           bool      `json:"hasArt"`
}

// Listing is the answer of GET /systems/{systemId}/games.
type Listing struct {
	Folders []Folder `json:"folders"`
	Games   []Game   `json:"games"`
}

// GameFile is a file of GET /games/{gameId}.
type GameFile struct {
	// FileID names the file for GET /games/{gameId}/files/{fileId}:
	// EncodeID of its path relative to the SD card. It is only ever
	// compared with the identifiers of the game's current files, never
	// decoded into a path.
	FileID     string    `json:"fileId"`
	Role       string    `json:"role"`
	Slot       *int      `json:"slot,omitempty"`
	Path       string    `json:"path"`
	SizeBytes  int64     `json:"sizeBytes"`
	ModifiedAt time.Time `json:"modifiedAt"`
}

// GameDetail is the answer of GET /games/{gameId}.
type GameDetail struct {
	ID         string     `json:"id"`
	SystemID   string     `json:"systemId"`
	Name       string     `json:"name"`
	FileName   string     `json:"fileName"`
	Kind       string     `json:"kind"`
	Files      []GameFile `json:"files"`
	TotalBytes int64      `json:"totalBytes"`
	// CheatCount (M23): the cheats of its .cht file, as Game.CheatCount.
	CheatCount int `json:"cheatCount"`
}

// timestamp is the API form of a date: UTC, whole seconds (RFC 3339).
func timestamp(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

func (l *Library) romsRoot() string { return l.adapter.Roots().Roms }

// resolve checks that id names a path inside Roms/ (symbolic links
// included) and returns that path relative to Roms/.
func (l *Library) resolve(id string) (string, error) {
	rel, err := DecodeID(id)
	if err != nil {
		return "", err
	}
	if _, err := ResolveID(l.romsRoot(), id); err != nil {
		return "", err
	}
	return rel, nil
}

func notFound(err error, what string) error {
	if errors.Is(err, osadapter.ErrNotFound) {
		return fmt.Errorf("%w: %s", ErrNotFound, what)
	}
	return err
}

// Systems lists the systems with their number of games.
func (l *Library) Systems() ([]System, error) {
	systems, err := l.adapter.Systems()
	if err != nil {
		return nil, err
	}
	out := make([]System, 0, len(systems))
	for _, s := range systems {
		n, err := l.adapter.CountGames(s)
		if err != nil {
			return nil, err
		}
		out = append(out, System{ID: EncodeID(s.Folder), Label: s.Label, Folder: s.Folder, Tag: s.Tag, GameCount: n,
			EmulatorInstalled: l.adapter.EmulatorInstalled(s.Tag)})
	}
	return out, nil
}

func (l *Library) system(systemID string) (osadapter.System, error) {
	folder, err := DecodeSystemID(systemID)
	if err != nil {
		return osadapter.System{}, err
	}
	if _, err := ResolveID(l.romsRoot(), systemID); err != nil {
		return osadapter.System{}, err
	}
	sys, err := l.adapter.System(folder)
	if err != nil {
		return osadapter.System{}, notFound(err, "system")
	}
	return sys, nil
}

// ListGames lists a folder of a system: its root when folderID is "", or
// the subfolder folderID, which must belong to this system (ErrInvalidID
// otherwise).
func (l *Library) ListGames(systemID, folderID string) (Listing, error) {
	sys, err := l.system(systemID)
	if err != nil {
		return Listing{}, err
	}
	rel := sys.Folder
	if folderID != "" {
		if rel, err = l.resolve(folderID); err != nil {
			return Listing{}, err
		}
		if !strings.HasPrefix(rel, sys.Folder+"/") {
			return Listing{}, fmt.Errorf("%w: folder of another system", ErrInvalidID)
		}
	}
	listing, err := l.adapter.ListFolder(sys, rel)
	if err != nil {
		return Listing{}, notFound(err, "folder")
	}
	locks := l.lockedSlots(sys)
	out := Listing{Folders: make([]Folder, 0, len(listing.Folders)), Games: make([]Game, 0, len(listing.Games))}
	for _, f := range listing.Folders {
		out.Folders = append(out.Folders, Folder{ID: EncodeID(f.Path), Name: f.Name, GameCount: f.GameCount})
	}
	for _, g := range listing.Games {
		out.Games = append(out.Games, apiGame(g, locks))
	}
	return out, nil
}

// apiGame is the API form of a listed game; locks come from lockedSlots.
func apiGame(g osadapter.GameSummary, locks map[string]int) Game {
	cheatCount := 0
	if g.CheatFile != "" {
		cheatCount = countCheats(g.CheatFile)
	}
	return Game{
		ID:               EncodeID(g.Path),
		Name:             g.Name,
		FileName:         g.FileName,
		Kind:             string(g.Kind),
		SizeBytes:        g.Size,
		ModifiedAt:       timestamp(g.ModTime),
		HasSave:          g.HasSave,
		StateCount:       g.StateCount,
		LockedStateCount: locks[strings.ToLower(g.ROMName)],
		CheatCount:       cheatCount,
		HasArt:           g.HasArt,
	}
}

// lockSlotRe matches the copy of a locked state: slot<N>.st.
var lockSlotRe = regexp.MustCompile(`^slot\d{1,2}\.st$`)

// lockedSlots counts the locked slots of each ROM of a system (locks/<TAG>/
// <ROM file name>/slot<N>.st), keyed by lowercase ROM file name. One
// directory read per system, plus one per locked game.
func (l *Library) lockedSlots(sys osadapter.System) map[string]int {
	dir := l.layout.LocksDir(sys.Tag)
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := map[string]int{}
	for _, d := range des {
		if !d.IsDir() {
			continue
		}
		slots, err := os.ReadDir(filepath.Join(dir, d.Name()))
		if err != nil {
			continue
		}
		for _, s := range slots {
			if s.Type().IsRegular() && lockSlotRe.MatchString(s.Name()) {
				out[strings.ToLower(d.Name())]++
			}
		}
	}
	return out
}

func (l *Library) game(gameID string) (osadapter.Game, error) {
	rel, err := l.resolve(gameID)
	if err != nil {
		return osadapter.Game{}, err
	}
	g, err := l.adapter.Game(rel)
	if err != nil {
		return osadapter.Game{}, notFound(err, "game")
	}
	return g, nil
}

// gameFile is a file of a game with where it is on disk.
type gameFile struct {
	GameFile
	path string // absolute path
	// root is the top folder the file is opened through (no link at any
	// component below it); the file must also be lexically under within,
	// the narrowest folder of its role (the system folder for a ROM).
	root, within string
}

// gameFiles lists the files of g in the order of GET /games/{gameId}.
func (l *Library) gameFiles(g osadapter.Game) ([]gameFile, error) {
	files, err := l.adapter.GameFiles(g)
	if err != nil {
		return nil, notFound(err, "game")
	}
	roots := l.adapter.Roots()
	system := filepath.Join(roots.Roms, filepath.FromSlash(g.System.Folder))
	saves := filepath.Join(roots.Saves, g.System.Tag)
	var out []gameFile
	add := func(role string, f osadapter.File, slot *int, root, within string) {
		// Opened under the card: a link at the functional root itself is
		// refused too (rule 2).
		root = confine.Base(roots.SD, root)
		out = append(out, gameFile{
			GameFile: GameFile{FileID: EncodeID(f.Rel), Role: role, Slot: slot, Path: f.Rel, SizeBytes: f.Size, ModifiedAt: timestamp(f.ModTime)},
			path:     f.Path,
			root:     root,
			within:   within,
		})
	}
	for _, f := range files.ROM {
		add("rom", f, nil, roots.Roms, system)
	}
	if files.Save != nil {
		add("save", *files.Save, nil, roots.Saves, saves)
	}
	if files.RTC != nil {
		add("rtc", *files.RTC, nil, roots.Saves, saves)
	}
	for _, s := range files.States {
		slot := s.Slot
		add("state", s.File, &slot, roots.States, roots.States)
	}
	if files.Cheats != nil {
		add("cheats", *files.Cheats, nil, roots.Cheats, filepath.Join(roots.Cheats, g.System.Tag))
	}
	if files.Art != nil {
		add("art", *files.Art, nil, roots.Roms, system)
	}
	return out, nil
}

// Game returns a game and all its files.
func (l *Library) Game(gameID string) (GameDetail, error) {
	g, err := l.game(gameID)
	if err != nil {
		return GameDetail{}, err
	}
	files, err := l.gameFiles(g)
	if err != nil {
		return GameDetail{}, err
	}
	d := GameDetail{
		ID:       EncodeID(g.Path),
		SystemID: EncodeID(g.System.Folder),
		Name:     g.Name,
		FileName: g.FileName,
		Kind:     string(g.Kind),
		Files:    make([]GameFile, 0, len(files)),
	}
	for _, f := range files {
		d.Files = append(d.Files, f.GameFile)
		d.TotalBytes += f.SizeBytes
		if f.Role == "cheats" {
			d.CheatCount = countCheatsConfined(f)
		}
	}
	return d, nil
}

// OpenFile opens the file fileID of a game for GET
// /games/{gameId}/files/{fileId}. The file list of the game is computed
// again, as GET /games/{gameId} does, and only the entry whose fileId is
// exactly fileID is opened: fileID is never decoded into a path. The open
// refuses a link at any component below the top folder of the file's role
// (Roms/, Saves/, Cheats/ or the states folder; confine.Open) and a file
// outside the narrowest folder of that role (system, Saves/<TAG>,
// Cheats/<TAG>). Errors: ErrInvalidID for a malformed
// fileID, ErrNotFound when it is not in the list or the file is gone or
// refused (the refusal also wraps confine.ErrOutside), or an I/O error.
func (l *Library) OpenFile(gameID, fileID string) (*os.File, fs.FileInfo, string, error) {
	g, err := l.game(gameID)
	if err != nil {
		return nil, nil, "", err
	}
	if fileID == "" || len(fileID) > maxFileIDLen || !isB64URLAlphabet(fileID) {
		return nil, nil, "", fmt.Errorf("%w: file id", ErrInvalidID)
	}
	files, err := l.gameFiles(g)
	if err != nil {
		return nil, nil, "", err
	}
	for _, f := range files {
		if f.FileID != fileID {
			continue
		}
		var file *os.File
		var fi fs.FileInfo
		_, err := confine.RelToRoot(f.within, f.path)
		if err == nil {
			file, fi, err = confine.OpenUnder(f.root, f.path)
		}
		switch {
		case errors.Is(err, confine.ErrOutside):
			return nil, nil, "", fmt.Errorf("%w: %w: file", confine.ErrOutside, ErrNotFound)
		case errors.Is(err, fs.ErrNotExist):
			return nil, nil, "", fmt.Errorf("%w: file", ErrNotFound)
		case err != nil:
			return nil, nil, "", fmt.Errorf("open game file: %w", withoutPath(err))
		}
		return file, fi, filepath.Base(f.path), nil
	}
	return nil, nil, "", fmt.Errorf("%w: file", ErrNotFound)
}

// maxFileIDLen bounds a fileId: the identifier of the longest path.
var maxFileIDLen = base64.RawURLEncoding.EncodedLen(MaxIDPathBytes)

// OpenArt opens the art of a game for reading, like the art of GET
// /games/{gameId}/files: under Roms/, lexically inside the system folder,
// with no link at any component (confine). It returns the opened file,
// its description (fstat) and its absolute path. Errors: ErrNotFound when
// the game has no art or its art is refused (the refusal also wraps
// confine.ErrOutside), ErrInvalidID, or an I/O error.
func (l *Library) OpenArt(gameID string) (*os.File, fs.FileInfo, string, error) {
	g, err := l.game(gameID)
	if err != nil {
		return nil, nil, "", err
	}
	art, err := l.adapter.ArtFile(g)
	if err != nil {
		// The card could not be read: not an absent art.
		return nil, nil, "", fmt.Errorf("find art: %w", withoutPath(err))
	}
	if art == nil {
		return nil, nil, "", fmt.Errorf("%w: art", ErrNotFound)
	}
	roots := l.adapter.Roots()
	system := filepath.Join(roots.Roms, filepath.FromSlash(g.System.Folder))
	var file *os.File
	var fi fs.FileInfo
	_, err = confine.RelToRoot(system, art.Path)
	if err == nil {
		file, fi, err = confine.OpenUnder(confine.Base(roots.SD, roots.Roms), art.Path)
	}
	switch {
	case errors.Is(err, confine.ErrOutside):
		return nil, nil, "", fmt.Errorf("%w: %w: art", confine.ErrOutside, ErrNotFound)
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil, "", fmt.Errorf("%w: art", ErrNotFound)
	case err != nil:
		return nil, nil, "", fmt.Errorf("open art: %w", withoutPath(err))
	}
	return file, fi, art.Path, nil
}

// MaxAllGames is the most games GET /games returns (truncated beyond).
const MaxAllGames = 20000

// LibraryGame is a game of GET /games: a listed game with its system and
// folder.
type LibraryGame struct {
	Game
	SystemID string `json:"systemId"`
	// FolderID is the folderId GET /systems/{systemId}/games gives the
	// folder that holds the game, nil at the root of the system.
	FolderID *string `json:"folderId"`
	// FolderPath is the folder names below the system joined with "/",
	// nil at the root (informative).
	FolderPath *string `json:"folderPath"`
}

// AllGames is the answer of GET /games (GeneratedAt is set by the caller).
type AllGames struct {
	Games       []LibraryGame `json:"games"`
	Truncated   bool          `json:"truncated"`
	GeneratedAt time.Time     `json:"generatedAt"`
}

// AllGames lists the games of every system of GET /systems (with or
// without emulator), subfolders included, with the rules of ListGames,
// sorted by system folder then by path (case-insensitive). At most limit
// games are returned; Truncated tells there were more. Each system is
// walked once (WalkSummaries), keeping only the games that can still be
// returned; it stops with ctx.Err() when ctx is done.
func (l *Library) AllGames(ctx context.Context, limit int) (AllGames, error) {
	out := AllGames{Games: []LibraryGame{}}
	// The systems without a visible item have no game: SystemFolders does
	// not open each folder to leave them out, the walk reads it once.
	systems, err := l.adapter.SystemFolders()
	if err != nil {
		return AllGames{}, err
	}
	sort.Slice(systems, func(i, j int) bool { return lessFold(systems[i].Folder, systems[j].Folder) })
	for _, sys := range systems {
		if err := ctx.Err(); err != nil {
			return AllGames{}, err
		}
		room := limit - len(out.Games)
		// The room+1 first games of the system in the final order: one
		// more than room tells the list is truncated.
		kept := &gameHeap{}
		err := l.adapter.WalkSummaries(ctx, sys, func(folder string, g osadapter.GameSummary) error {
			kept.offer(foundGame{folder, g}, room+1)
			if room <= 0 {
				return errEnough // only whether one more game exists
			}
			return nil
		})
		if err != nil && !errors.Is(err, errEnough) {
			return AllGames{}, err
		}
		if err := ctx.Err(); err != nil {
			return AllGames{}, err
		}
		games := kept.sorted()
		if len(games) > room {
			games, out.Truncated = games[:room], true
		}
		if len(games) == 0 {
			if out.Truncated {
				return out, nil
			}
			continue
		}
		locks := l.lockedSlots(sys)
		systemID := EncodeID(sys.Folder)
		for _, f := range games {
			// apiGame reads the cheat file of the game.
			if err := ctx.Err(); err != nil {
				return AllGames{}, err
			}
			lg := LibraryGame{Game: apiGame(f.g, locks), SystemID: systemID}
			if sub, ok := strings.CutPrefix(f.folder, sys.Folder+"/"); ok {
				id := EncodeID(f.folder)
				lg.FolderID, lg.FolderPath = &id, &sub
			}
			out.Games = append(out.Games, lg)
		}
		if out.Truncated {
			return out, nil
		}
	}
	return out, nil
}

// countCheatsConfined counts the cheats of the .cht of a game opened like
// GET /games/{gameId}/files/{fileId}: under Cheats/ through os.Root, no
// link at any component, inside Cheats/<TAG> (Codex review of M23). A
// refused or unreadable file counts 0.
func countCheatsConfined(f gameFile) int {
	if _, err := confine.RelToRoot(f.within, f.path); err != nil {
		return 0
	}
	file, _, err := confine.OpenUnder(f.root, f.path)
	if err != nil {
		return 0
	}
	defer file.Close()
	return cheats.CountReader(file)
}

// countCheats counts the cheats of a .cht file (cheats.CountFile; counted
// by tests).
var countCheats = cheats.CountFile

// errEnough stops a walk that found all it needs.
var errEnough = errors.New("enough games")

// foundGame is a game met by AllGames, with the folder that holds it.
type foundGame struct {
	folder string
	g      osadapter.GameSummary
}

// gameHeap keeps the first games by path (lessFold): a max-heap whose
// root is the last game kept, so memory stays bounded by what can be
// returned however large the system is.
type gameHeap []foundGame

func (h gameHeap) Len() int           { return len(h) }
func (h gameHeap) Less(i, j int) bool { return lessFold(h[j].g.Path, h[i].g.Path) }
func (h gameHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *gameHeap) Push(x any)        { *h = append(*h, x.(foundGame)) }
func (h *gameHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// offer keeps g if it is among the n first games met so far.
func (h *gameHeap) offer(g foundGame, n int) {
	switch {
	case n <= 0:
	case h.Len() < n:
		heap.Push(h, g)
	case lessFold(g.g.Path, (*h)[0].g.Path):
		(*h)[0] = g
		heap.Fix(h, 0)
	}
}

// sorted returns the games kept in order (and empties the heap).
func (h *gameHeap) sorted() []foundGame {
	out := make([]foundGame, h.Len())
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = heap.Pop(h).(foundGame)
	}
	return out
}

// lessFold orders case-insensitively, then by the exact bytes.
func lessFold(a, b string) bool {
	fa, fb := strings.ToLower(a), strings.ToLower(b)
	if fa != fb {
		return fa < fb
	}
	return a < b
}
