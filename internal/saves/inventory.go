package saves

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter"
)

// Limits of the save listing (GET /saves).
const (
	// MaxListedSaves is the most saves GET /saves lists; beyond, the list
	// is marked truncated.
	MaxListedSaves = 10000
	// maxListDepth bounds the recursion in the subfolders of a system (the
	// same depth as the adapter's game count).
	maxListDepth = 16
	// hashChunk is how much is hashed between two checks of the request
	// context.
	hashChunk = 1 << 20
)

// maxListedSaves is MaxListedSaves, lowered by tests.
var maxListedSaves = MaxListedSaves

// ClockFile is the clock file of a listed save (SaveClockFile in
// protocol/openapi.yaml).
type ClockFile struct {
	Path       string    `json:"path"`
	SizeBytes  int64     `json:"sizeBytes"`
	ModifiedAt time.Time `json:"modifiedAt"`
	SHA256     string    `json:"sha256"`
}

// Summary is a save of GET /saves (SaveSummary in protocol/openapi.yaml).
type Summary struct {
	GameID     string     `json:"gameId"`
	SystemID   string     `json:"systemId"`
	GameName   string     `json:"gameName"`
	FileName   string     `json:"fileName"`
	Path       string     `json:"path"`
	SizeBytes  int64      `json:"sizeBytes"`
	ModifiedAt time.Time  `json:"modifiedAt"`
	SHA256     string     `json:"sha256"`
	RTC        *ClockFile `json:"rtc"`
}

// Listing is the body of GET /saves (SaveList in protocol/openapi.yaml).
type Listing struct {
	Saves       []Summary `json:"saves"`
	Truncated   bool      `json:"truncated"`
	GeneratedAt time.Time `json:"generatedAt"`
}

// hashCache remembers the SHA-256 of the saves, keyed by absolute path and
// valid while the file is the same (os.SameFile), with the same size and
// modification time. It only holds the files of the last listing.
type hashCache struct {
	// slot lets one listing run at a time.
	slot chan struct{}
	mu   sync.Mutex
	m    map[string]hashEntry
	// hashed counts the files read to compute a hash (tests).
	hashed int
}

type hashEntry struct {
	info fs.FileInfo
	sum  string
}

func newHashCache() *hashCache {
	return &hashCache{slot: make(chan struct{}, 1), m: map[string]hashEntry{}}
}

func (c *hashCache) get(path string, fi fs.FileInfo) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[path]
	if !ok || !os.SameFile(e.info, fi) || e.info.Size() != fi.Size() || !e.info.ModTime().Equal(fi.ModTime()) {
		return "", false
	}
	return e.sum, true
}

func (c *hashCache) put(path string, fi fs.FileInfo, sum string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[path] = hashEntry{info: fi, sum: sum}
	c.hashed++
}

// keep forgets the files that are not in seen.
func (c *hashCache) keep(seen map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for p := range c.m {
		if !seen[p] {
			delete(c.m, p)
		}
	}
}

// errSkip marks a file left out of the listing (a link, outside the saves
// folder, gone).
var errSkip = errors.New("file skipped")

// errFull stops the listing at MaxListedSaves.
var errFull = errors.New("listing full")

// lister is one run of ListAll.
type lister struct {
	s       *Service
	ctx     context.Context
	root    string // saves folder, symbolic links resolved ("" if missing)
	seen    map[string]bool
	listing Listing
}

// ListAll lists the save of every game that has one, on every system
// (GET /saves): same games and same save resolution as the library routes,
// a clock file given with its save, never alone. Hashes come from the
// cache when the file did not change. It stops with ctx.Err() when ctx is
// done. It takes no lock of the SD card: the files are hashed through an
// open descriptor, and the pak replaces a save by renaming a new file over
// it.
func (s *Service) ListAll(ctx context.Context) (Listing, error) {
	select {
	case s.hashes.slot <- struct{}{}:
	case <-ctx.Done():
		return Listing{}, ctx.Err()
	}
	defer func() { <-s.hashes.slot }()

	l := &lister{s: s, ctx: ctx, seen: map[string]bool{},
		listing: Listing{Saves: []Summary{}, GeneratedAt: s.o.Now().UTC().Truncate(time.Second)}}
	root, err := filepath.EvalSymlinks(s.o.Adapter.Roots().Saves)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return l.listing, nil // no saves folder: no save
	case err != nil:
		return Listing{}, fmt.Errorf("list saves: %w", err)
	}
	l.root = root
	systems, err := s.o.Adapter.Systems()
	if err != nil {
		return Listing{}, fmt.Errorf("list saves: %w", err)
	}
	for _, sys := range systems {
		err := l.folder(sys, s.o.Adapter.SaveDir(sys.Tag), sys.Folder, 0)
		if errors.Is(err, errFull) {
			l.listing.Truncated = true
			break
		}
		if err != nil {
			return Listing{}, err
		}
	}
	if !l.listing.Truncated {
		s.hashes.keep(l.seen)
	}
	return l.listing, nil
}

// folder lists the saves of the games of a folder of sys, then of its
// subfolders. saves is one read of the save folder of sys.
func (l *lister) folder(sys osadapter.System, saves osadapter.SaveDir, rel string, depth int) error {
	if err := l.ctx.Err(); err != nil {
		return err
	}
	listing, err := l.s.o.Adapter.ListFolder(sys, rel)
	if errors.Is(err, osadapter.ErrNotFound) {
		return nil // removed meanwhile
	}
	if err != nil {
		return fmt.Errorf("list saves: %w", err)
	}
	for _, g := range listing.Games {
		if !g.HasSave {
			continue
		}
		if err := l.game(g.Game, saves); err != nil {
			return err
		}
	}
	if depth >= maxListDepth {
		return nil
	}
	for _, f := range listing.Folders {
		if err := l.folder(sys, saves, f.Path, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// game adds the save of g, if it still has one.
func (l *lister) game(g osadapter.Game, saves osadapter.SaveDir) error {
	if err := l.ctx.Err(); err != nil {
		return err
	}
	save, rtc := saves.Files(g)
	if save == nil {
		return nil
	}
	if len(l.listing.Saves) >= maxListedSaves {
		return errFull
	}
	fi, sum, err := l.hash(save.Path)
	if errors.Is(err, errSkip) {
		return nil
	}
	if err != nil {
		return err
	}
	sv := Summary{
		GameID:     library.EncodeID(g.Path),
		SystemID:   library.EncodeID(g.System.Folder),
		GameName:   g.Name,
		FileName:   g.FileName,
		Path:       save.Rel,
		SizeBytes:  fi.Size(),
		ModifiedAt: fi.ModTime().UTC().Truncate(time.Second),
		SHA256:     sum,
	}
	if rtc != nil {
		fi, sum, err := l.hash(rtc.Path)
		switch {
		case err == nil:
			sv.RTC = &ClockFile{Path: rtc.Rel, SizeBytes: fi.Size(),
				ModifiedAt: fi.ModTime().UTC().Truncate(time.Second), SHA256: sum}
		case !errors.Is(err, errSkip):
			return err
		}
	}
	l.listing.Saves = append(l.listing.Saves, sv)
	return nil
}

// hash returns the description and the SHA-256 of the regular file p,
// which must be inside the saves folder without going through a symbolic
// link (errSkip otherwise, or when it is gone).
func (l *lister) hash(p string) (fs.FileInfo, string, error) {
	lfi, err := os.Lstat(p)
	if err != nil || !lfi.Mode().IsRegular() {
		return nil, "", l.skip(p, "not a regular file")
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil || !inside(l.root, dir) {
		return nil, "", l.skip(p, "outside the saves folder")
	}
	l.seen[p] = true
	f, err := os.Open(p)
	if err != nil {
		return nil, "", l.skip(p, "unreadable")
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !os.SameFile(lfi, fi) || !fi.Mode().IsRegular() {
		return nil, "", l.skip(p, "changed while opened")
	}
	if sum, ok := l.s.hashes.get(p, fi); ok {
		return fi, sum, nil
	}
	h := sha256.New()
	for n := int64(0); n < fi.Size(); {
		if err := l.ctx.Err(); err != nil {
			return nil, "", err
		}
		c, err := io.CopyN(h, f, min(hashChunk, fi.Size()-n))
		n += c
		if err != nil {
			return nil, "", l.skip(p, "read error")
		}
	}
	sum := hex.EncodeToString(h.Sum(nil))
	l.s.hashes.put(p, fi, sum)
	return fi, sum, nil
}

func (l *lister) skip(p, why string) error {
	l.s.o.Logger.Warn("list saves: file skipped", "file", filepath.Base(p), "reason", why)
	return errSkip
}

// inside reports whether p is root or under it (both cleaned, links
// resolved).
func inside(root, p string) bool {
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
