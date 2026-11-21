package cheats

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/store"
)

// Limits of the cheat routes.
const (
	// MaxCheats is the longest list accepted by PUT and produced by an
	// import.
	MaxCheats = 10000
	// MaxImportBytes is the largest .cht file imported from a URL, the SD
	// card or a staging file (docs/specs/04-api-v1.md: 5 MiB for a URL).
	MaxImportBytes = 5 << 20
	// MaxInlineBytes is the largest inline import text.
	MaxInlineBytes = 1 << 20
	// BakSuffix is appended to the .cht file name for the previous version
	// (05 §5.4, as Cheat Manager).
	BakSuffix = ".bak"
)

// Import modes.
const (
	ModeMerge   = "merge"
	ModeReplace = "replace"
)

// Errors of the service.
var (
	ErrConflict     = errors.New("cheat file changed since the given ETag")
	ErrInvalidCheat = errors.New("invalid cheat")
	ErrTooLarge     = errors.New("cheat file too large")
	ErrNoCheat      = errors.New("no cheat found")
	ErrInvalidMode  = errors.New("invalid import mode")
)

// APICheat is a cheat of the API (Cheat in protocol/openapi.yaml). Extra
// holds the unknown keys of the cheat without the "cheat<index>_" prefix
// of this cheat ("handler" for "cheat0_handler", "cheat1_x" for
// "cheat0_cheat1_x"): the pak adds the prefix of the new position on
// write, so a key always follows its cheat. A key received is used as it
// is (no prefix is ever removed from it).
type APICheat struct {
	Index       int               `json:"index"`
	Description string            `json:"description"`
	Code        string            `json:"code"`
	Enabled     bool              `json:"enabled"`
	Extra       map[string]string `json:"extra"`
}

// List is the answer of GET and PUT /games/{gameId}/cheats.
type List struct {
	Cheats []APICheat `json:"cheats"`
	ETag   string     `json:"etag"`
}

// ImportResult is the answer of POST /games/{gameId}/cheats/import.
type ImportResult struct {
	Imported int `json:"imported"`
	Total    int `json:"total"`
}

// Options are the dependencies of a Service.
type Options struct {
	Adapter osadapter.Adapter
	// Trash receives the previous .cht.bak before it is replaced.
	Trash *store.Trash
	// Lock serializes every change of the SD card files (shared with the
	// uploads, the saves and the states). nil: a lock of its own.
	Lock   *sync.Mutex
	Logger *slog.Logger // nil: slog.Default()
}

// Service reads and writes the cheat files of the games.
type Service struct {
	o Options
}

// New returns a Service.
func New(o Options) (*Service, error) {
	if o.Adapter == nil || o.Trash == nil {
		return nil, errors.New("cheats: missing dependency")
	}
	if o.Lock == nil {
		o.Lock = &sync.Mutex{}
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Service{o: o}, nil
}

// ETagOf returns the ETag of the content of a cheat file. A missing file
// has the ETag of an empty one (both hold no cheat).
func ETagOf(data []byte) string {
	sum := sha256.Sum256(data)
	return `"sha256-` + hex.EncodeToString(sum[:]) + `"`
}

// path returns the cheat file of g: the file on the card when it exists
// (its name may differ by case), else the adapter's path.
func (s *Service) path(g osadapter.Game) (string, bool, error) {
	files, err := s.o.Adapter.GameFiles(g)
	if err != nil {
		return "", false, fmt.Errorf("cheats: %w", err)
	}
	if files.Cheats != nil {
		return files.Cheats.Path, true, nil
	}
	p := s.o.Adapter.CheatPath(g)
	if p == "" {
		// An OS without a cheat file the pak can name (Knulli, M20).
		return "", false, fmt.Errorf("%w: no cheat file on this OS", osadapter.ErrUnsupportedFormat)
	}
	return p, false, nil
}

// readCurrent returns the content of the cheat file of g (nil when it
// does not exist), at most MaxFileBytes.
func (s *Service) readCurrent(g osadapter.Game) (string, bool, []byte, error) {
	p, exists, err := s.path(g)
	if err != nil || !exists {
		return p, false, nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return p, false, nil, nil
	}
	if err != nil {
		return p, false, nil, fmt.Errorf("cheats: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return p, false, nil, fmt.Errorf("cheats: %w", err)
	}
	if len(data) > MaxFileBytes {
		return p, true, nil, ErrTooLarge
	}
	return p, true, data, nil
}

// toAPI converts a parsed file.
func toAPI(cf *File) []APICheat {
	out := make([]APICheat, 0, len(cf.Cheats))
	for i, c := range cf.Cheats {
		extra := map[string]string{}
		for _, e := range c.Extras {
			extra[e.Key] = e.Value
		}
		out = append(out, APICheat{Index: i, Description: c.Desc, Code: c.Code, Enabled: c.Enable, Extra: extra})
	}
	return out
}

var plainKey = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// fromAPI converts and validates the cheats of a PUT. Unknown keys are
// written in alphabetical order.
func fromAPI(in []APICheat) ([]Cheat, error) {
	if len(in) > MaxCheats {
		return nil, fmt.Errorf("%w: more than %d cheats", ErrInvalidCheat, MaxCheats)
	}
	out := make([]Cheat, 0, len(in))
	for i, c := range in {
		cheat := Cheat{Desc: c.Description, Code: c.Code, Enable: c.Enabled}
		for key, v := range c.Extra {
			k := key
			switch {
			case !plainKey.MatchString(key):
				return nil, fmt.Errorf("%w: cheat %d: invalid key %q", ErrInvalidCheat, i, k)
			case key == "desc" || key == "code" || key == "enable":
				return nil, fmt.Errorf("%w: cheat %d: %q is not an extra key", ErrInvalidCheat, i, k)
			}
			cheat.Extras = append(cheat.Extras, Extra{Key: key, Value: v})
		}
		sort.Slice(cheat.Extras, func(a, b int) bool { return cheat.Extras[a].Key < cheat.Extras[b].Key })
		out = append(out, cheat)
	}
	return out, nil
}

// Get returns the cheats of g and the ETag of their file.
func (s *Service) Get(g osadapter.Game) (List, error) {
	s.o.Lock.Lock()
	defer s.o.Lock.Unlock()
	_, _, data, err := s.readCurrent(g)
	if err != nil {
		return List{}, err
	}
	return List{Cheats: toAPI(Parse(data)), ETag: ETagOf(data)}, nil
}

// Put replaces the cheats of g when ifMatch is the ETag of the current
// file (ErrConflict otherwise; "*" matches an existing file). An empty
// list deletes the .cht file, whose content stays in the .bak.
func (s *Service) Put(g osadapter.Game, ifMatch string, cheats []APICheat) (List, error) {
	list, err := fromAPI(cheats)
	if err != nil {
		return List{}, err
	}
	s.o.Lock.Lock()
	defer s.o.Lock.Unlock()
	p, exists, data, err := s.readCurrent(g)
	if err != nil {
		return List{}, err
	}
	if !etagMatches(strings.TrimSpace(ifMatch), ETagOf(data), exists) {
		return List{}, ErrConflict
	}
	cf, err := ParseFile(data)
	if err != nil {
		return List{}, err // never rewrite a file read in part
	}
	cf.Cheats = list
	return s.writeLocked(g, p, exists, cf)
}

func etagMatches(ifMatch, etag string, exists bool) bool {
	if ifMatch == "*" {
		return exists
	}
	return ifMatch == etag
}

// Import adds the cheats of a .cht content to g (merge: after the current
// ones; replace: instead of them, keeping the comment lines of the current
// file, or those of the imported one when there is no file).
func (s *Service) Import(g osadapter.Game, data []byte, mode string) (ImportResult, error) {
	if mode != ModeMerge && mode != ModeReplace {
		return ImportResult{}, ErrInvalidMode
	}
	imported, err := ParseFile(data)
	if err != nil {
		return ImportResult{}, err
	}
	if len(imported.Cheats) == 0 {
		return ImportResult{}, ErrNoCheat
	}
	s.o.Lock.Lock()
	defer s.o.Lock.Unlock()
	p, exists, current, err := s.readCurrent(g)
	if err != nil {
		return ImportResult{}, err
	}
	cf, err := ParseFile(current)
	if err != nil {
		return ImportResult{}, err
	}
	if mode == ModeReplace {
		cf.Cheats = nil
		if !exists {
			cf.Header = imported.Header
		}
	}
	cf.Cheats = append(cf.Cheats, imported.Cheats...)
	if len(cf.Cheats) > MaxCheats {
		return ImportResult{}, fmt.Errorf("%w: more than %d cheats", ErrInvalidCheat, MaxCheats)
	}
	if _, err := s.writeLocked(g, p, exists, cf); err != nil {
		return ImportResult{}, err
	}
	s.o.Logger.Info("cheats imported", "game", g.Path, "mode", mode, "imported", len(imported.Cheats), "total", len(cf.Cheats))
	return ImportResult{Imported: len(imported.Cheats), Total: len(cf.Cheats)}, nil
}

// writeLocked writes cf to the cheat file p of g. The current file, when
// it exists, is first copied to <file>.bak (atomically: the previous .bak
// is replaced only by a complete copy). The new content is written
// atomically; an empty list removes the file (its content is in the .bak).
// Called with the lock held.
func (s *Service) writeLocked(g osadapter.Game, p string, exists bool, cf *File) (List, error) {
	// The size is checked before anything changes: a file larger than the
	// reads accept could not be read back (Codex review of M5).
	out := cf.Bytes()
	if len(out) > MaxFileBytes {
		return List{}, ErrTooLarge
	}
	if exists {
		// The previous .bak holds an older version: it goes to the trash
		// before being replaced, so no version is ever lost (CLAUDE.md
		// rule 1). A power cut between the two steps leaves the .cht
		// unchanged and the old .bak in a complete trash entry.
		bak := store.FindFold(filepath.Dir(p), filepath.Base(p)+BakSuffix, false)
		if bak != "" {
			if _, err := s.o.Trash.Move(store.ReasonOverwrite, g.Name, []store.MoveItem{{Path: bak}}); err != nil {
				return List{}, fmt.Errorf("cheats: backup: %w", err)
			}
		}
		if _, _, err := store.CopyFileAtomic(p, p+BakSuffix); err != nil {
			return List{}, fmt.Errorf("cheats: backup: %w", err)
		}
	}
	if len(cf.Cheats) == 0 {
		if exists {
			if err := os.Remove(p); err != nil {
				return List{}, fmt.Errorf("cheats: %w", err)
			}
			store.SyncDir(filepath.Dir(p))
			s.o.Logger.Info("cheat file removed (empty list), previous version kept", "game", g.Path, "backup", p+BakSuffix)
		}
		return List{Cheats: []APICheat{}, ETag: ETagOf(nil)}, nil
	}
	if err := store.WriteFileAtomic(p, out, 0o644); err != nil {
		return List{}, fmt.Errorf("cheats: %w", err)
	}
	if exists {
		s.o.Logger.Info("cheat file written, previous version kept", "game", g.Path, "cheats", len(cf.Cheats), "backup", p+BakSuffix)
	} else {
		s.o.Logger.Info("cheat file created", "game", g.Path, "cheats", len(cf.Cheats))
	}
	return List{Cheats: toAPI(Parse(out)), ETag: ETagOf(out)}, nil
}
