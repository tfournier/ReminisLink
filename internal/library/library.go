package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tfournier/ReminisLink/internal/cheats"
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
		out = append(out, System{ID: EncodeID(s.Folder), Label: s.Label, Folder: s.Folder, Tag: s.Tag, GameCount: n})
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
		cheatCount := 0
		if g.CheatFile != "" {
			cheatCount = cheats.CountFile(g.CheatFile)
		}
		out.Games = append(out.Games, Game{
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
		})
	}
	return out, nil
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

// Game returns a game and all its files.
func (l *Library) Game(gameID string) (GameDetail, error) {
	g, err := l.game(gameID)
	if err != nil {
		return GameDetail{}, err
	}
	files, err := l.adapter.GameFiles(g)
	if err != nil {
		return GameDetail{}, notFound(err, "game")
	}
	d := GameDetail{
		ID:       EncodeID(g.Path),
		SystemID: EncodeID(g.System.Folder),
		Name:     g.Name,
		FileName: g.FileName,
		Kind:     string(g.Kind),
		Files:    []GameFile{},
	}
	add := func(role string, f osadapter.File, slot *int) {
		d.Files = append(d.Files, GameFile{Role: role, Slot: slot, Path: f.Rel, SizeBytes: f.Size, ModifiedAt: timestamp(f.ModTime)})
		d.TotalBytes += f.Size
	}
	for _, f := range files.ROM {
		add("rom", f, nil)
	}
	if files.Save != nil {
		add("save", *files.Save, nil)
	}
	if files.RTC != nil {
		add("rtc", *files.RTC, nil)
	}
	for _, s := range files.States {
		slot := s.Slot
		add("state", s.File, &slot)
	}
	if files.Cheats != nil {
		add("cheats", *files.Cheats, nil)
	}
	if files.Art != nil {
		add("art", *files.Art, nil)
	}
	return d, nil
}

// Art returns the art file of a game (ErrNotFound if it has none).
func (l *Library) Art(gameID string) (osadapter.File, error) {
	g, err := l.game(gameID)
	if err != nil {
		return osadapter.File{}, err
	}
	files, err := l.adapter.GameFiles(g)
	if err != nil {
		return osadapter.File{}, notFound(err, "game")
	}
	if files.Art == nil {
		return osadapter.File{}, fmt.Errorf("%w: art", ErrNotFound)
	}
	return *files.Art, nil
}
