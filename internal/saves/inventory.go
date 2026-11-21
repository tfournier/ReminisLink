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
	// MaxUncachedSave: saves of at most this size (nearly all of them)
	// are hashed at every listing. A save rewritten in place can keep its
	// size and its date (exFAT keeps dates to 2 s and has no change
	// time), so no metadata proves it unchanged; only larger ones use the
	// cache.
	MaxUncachedSave = 1 << 20
	// RacyMargin: a cached hash is used only for a file modified at least
	// this long before the hash was computed ("racy git" rule).
	RacyMargin = 3 * time.Second
	// hashChunk is how much is hashed between two checks of the request
	// context.
	hashChunk = 1 << 20
)

// statusChange is changeTime, replaced by tests (a file system without
// change time, such as exFAT).
var statusChange = changeTime

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

// hashCache remembers the SHA-256 of the saves larger than
// MaxUncachedSave, keyed by absolute path. A hash is used again only for
// the same file (os.SameFile: device and inode), with the same size,
// modification time and status change time (where the platform has one),
// when the modification time is at least RacyMargin before the hash was
// computed. It only holds the files of the last listing.
type hashCache struct {
	// slot lets one listing run at a time.
	slot chan struct{}
	now  func() time.Time // wall clock of the file system dates (tests change it)
	mu   sync.Mutex
	m    map[string]hashEntry
	// hashed counts, per file, the reads to compute a hash (tests).
	hashed map[string]int
}

type hashEntry struct {
	info     fs.FileInfo
	sum      string
	hashedAt time.Time // when the reading started
}

func newHashCache() *hashCache {
	return &hashCache{slot: make(chan struct{}, 1), now: time.Now, m: map[string]hashEntry{}, hashed: map[string]int{}}
}

func (c *hashCache) get(path string, fi fs.FileInfo) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[path]
	if !ok || fi.Size() <= MaxUncachedSave || !os.SameFile(e.info, fi) || e.info.Size() != fi.Size() ||
		!e.info.ModTime().Equal(fi.ModTime()) || !fi.ModTime().Before(e.hashedAt.Add(-RacyMargin)) {
		return "", false
	}
	oldC, okOld := statusChange(e.info)
	newC, okNew := statusChange(fi)
	if okOld != okNew || !oldC.Equal(newC) {
		return "", false
	}
	return e.sum, true
}

func (c *hashCache) put(path string, fi fs.FileInfo, sum string, hashedAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hashed[path]++
	if fi.Size() > MaxUncachedSave {
		c.m[path] = hashEntry{info: fi, sum: sum, hashedAt: hashedAt}
	}
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
	s    *Service
	ctx  context.Context
	dir  string   // saves folder, as the adapter gives it
	root *os.Root // dir, every file is opened through it
	seen map[string]bool
	// done memoizes the result of each file for this listing only, so two
	// games sharing one save get the same hash (never kept across
	// listings: that is the role of hashCache).
	done    map[string]hashResult
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

	l := &lister{s: s, ctx: ctx, seen: map[string]bool{}, done: map[string]hashResult{},
		listing: Listing{Saves: []Summary{}, GeneratedAt: s.o.Now().UTC().Truncate(time.Second)}}
	l.dir = s.o.Adapter.Roots().Saves
	if s.checkWriteDir(l.dir) != nil {
		return l.listing, nil // the saves folder is a link: no save is read through it (rule 2)
	}
	root, err := os.OpenRoot(l.dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return l.listing, nil // no saves folder: no save
	case err != nil:
		return Listing{}, fmt.Errorf("list saves: %w", err)
	}
	defer root.Close()
	l.root = root
	systems, err := s.o.Adapter.Systems()
	if err != nil {
		return Listing{}, fmt.Errorf("list saves: %w", err)
	}
	for _, sys := range systems {
		saves := s.o.Adapter.SaveDir(sys.Tag)
		err := s.o.Adapter.WalkGames(ctx, sys, func(g osadapter.Game) error { return l.game(g, saves) })
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

// hashResult is the outcome of hash for one file (err nil or errSkip).
type hashResult struct {
	fi  fs.FileInfo
	sum string
	err error
}

// afterHash, when set (tests), is called after a file was hashed.
var afterHash func(path string)

// hash returns the description and the SHA-256 of the regular file p,
// computed once per listing (hashFile).
func (l *lister) hash(p string) (fs.FileInfo, string, error) {
	if r, ok := l.done[p]; ok {
		return r.fi, r.sum, r.err
	}
	fi, sum, err := l.hashFile(p)
	if err == nil || errors.Is(err, errSkip) {
		l.done[p] = hashResult{fi, sum, err}
	}
	if afterHash != nil {
		afterHash(p)
	}
	return fi, sum, err
}

// hashFile returns the description and the SHA-256 of the regular file p,
// confined to the saves folder without any symbolic link (errSkip
// otherwise, or when it is gone).
func (l *lister) hashFile(p string) (fs.FileInfo, string, error) {
	rel, err := relToRoot(l.dir, p)
	var f *os.File
	var fi fs.FileInfo
	if err == nil {
		f, fi, err = openConfined(l.root, rel)
	}
	if errors.Is(err, errOutside) {
		return nil, "", l.skip(p, "outside the saves folder or reached through a link")
	}
	if err != nil {
		return nil, "", l.skip(p, "unreadable")
	}
	defer f.Close()
	l.seen[p] = true
	if sum, ok := l.s.hashes.get(p, fi); ok {
		return fi, sum, nil
	}
	hashedAt := l.s.hashes.now()
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
	l.s.hashes.put(p, fi, sum, hashedAt)
	return fi, sum, nil
}

func (l *lister) skip(p, why string) error {
	l.s.o.Logger.Warn("list saves: file skipped", "file", filepath.Base(p), "reason", why)
	return errSkip
}
