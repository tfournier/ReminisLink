package states

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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tfournier/ReminisLink/internal/confine"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/saves"
	"github.com/tfournier/ReminisLink/internal/store"
)

// Limits of the state listing (GET /states, M19).
const (
	// MaxListedStates is the most states GET /states lists; beyond, the
	// list is marked truncated.
	MaxListedStates = 20000
	// RacyMargin: a cached hash is used only for a file modified at least
	// this long before the hash was computed ("racy git" rule).
	RacyMargin = 3 * time.Second
	// inventoryChunk is how much is hashed between two checks of the
	// request context.
	inventoryChunk = 1 << 20
)

// maxListedStates is MaxListedStates, lowered by tests.
var maxListedStates = MaxListedStates

// ThumbnailFile is the thumbnail of a listed state (StateThumbnailFile).
type ThumbnailFile struct {
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
}

// Summary is a state of GET /states (StateSummary in protocol/openapi.yaml).
type Summary struct {
	GameID     string         `json:"gameId"`
	SystemID   string         `json:"systemId"`
	GameName   string         `json:"gameName"`
	FileName   string         `json:"fileName"`
	Slot       int            `json:"slot"`
	Path       string         `json:"path"`
	SizeBytes  int64          `json:"sizeBytes"`
	ModifiedAt time.Time      `json:"modifiedAt"`
	SHA256     string         `json:"sha256"`
	Thumbnail  *ThumbnailFile `json:"thumbnail"`
}

// Listing is the body of GET /states (StateList).
type Listing struct {
	States      []Summary `json:"states"`
	Truncated   bool      `json:"truncated"`
	GeneratedAt time.Time `json:"generatedAt"`
}

// stateHashes remembers the SHA-256 of the states and thumbnails, keyed by
// absolute path. Unlike the saves, every size is cached: NextUI does not
// run while the pak runs, and the pak replaces a state by renaming a new
// file over it (a new inode). A hash is used again only for the same file
// (os.SameFile), with the same size, modification time and status change
// time (where the platform has one), when the modification time is at
// least RacyMargin before the hash was computed.
type stateHashes struct {
	slot   chan struct{} // one listing at a time
	now    func() time.Time
	mu     sync.Mutex
	m      map[string]stateHash
	hashed map[string]int // reads to compute a hash, per file (tests)
}

type stateHash struct {
	info     fs.FileInfo
	sum      string
	hashedAt time.Time
}

func newStateHashes() *stateHashes {
	return &stateHashes{slot: make(chan struct{}, 1), now: time.Now, m: map[string]stateHash{}, hashed: map[string]int{}}
}

func (c *stateHashes) get(path string, fi fs.FileInfo) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[path]
	if !ok || !os.SameFile(e.info, fi) || e.info.Size() != fi.Size() || !e.info.ModTime().Equal(fi.ModTime()) ||
		!fi.ModTime().Before(e.hashedAt.Add(-RacyMargin)) {
		return "", false
	}
	oldC, okOld := saves.ChangeTime(e.info)
	newC, okNew := saves.ChangeTime(fi)
	if okOld != okNew || !oldC.Equal(newC) {
		return "", false
	}
	return e.sum, true
}

func (c *stateHashes) put(path string, fi fs.FileInfo, sum string, hashedAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hashed[path]++
	c.m[path] = stateHash{info: fi, sum: sum, hashedAt: hashedAt}
}

func (c *stateHashes) keep(seen map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for p := range c.m {
		if !seen[p] {
			delete(c.m, p)
		}
	}
}

// errListFull stops the listing at MaxListedStates.
var errListFull = errors.New("listing full")

// errFileSkipped marks a file left out (a link, outside its folder, gone,
// not a PNG for a thumbnail).
var errFileSkipped = errors.New("file skipped")

// hashesOf returns the hash cache of the service, made at the first use.
func (s *Service) hashesOf() *stateHashes {
	s.inventoryOnce.Do(func() {
		if s.inventory == nil {
			s.inventory = newStateHashes()
		}
	})
	return s.inventory
}

// inventoryRun is one run of ListAll.
type inventoryRun struct {
	s       *Service
	ctx     context.Context
	cache   *stateHashes
	seen    map[string]bool
	done    map[string]hashOutcome
	listing Listing
}

type hashOutcome struct {
	fi  fs.FileInfo
	sum string
	err error
}

// ListAll lists every state of every game of every system (GET /states):
// same games as GET /saves, same state resolution as List. Hashes come
// from the cache when the file did not change. It stops with ctx.Err()
// when ctx is done. It takes no lock of the SD card: the files are hashed
// through an open descriptor, and the pak replaces a state by renaming a
// new file over it.
func (s *Service) ListAll(ctx context.Context) (Listing, error) {
	cache := s.hashesOf()
	select {
	case cache.slot <- struct{}{}:
	case <-ctx.Done():
		return Listing{}, ctx.Err()
	}
	defer func() { <-cache.slot }()
	run := &inventoryRun{s: s, ctx: ctx, cache: cache, seen: map[string]bool{}, done: map[string]hashOutcome{},
		listing: Listing{States: []Summary{}, GeneratedAt: s.o.Now().UTC().Truncate(time.Second)}}

	// Every state of the card once, by tag and ROM file name (ignoring
	// case), then the games of each system.
	refs, err := s.o.Adapter.StateFiles(ctx)
	if err != nil {
		return Listing{}, err
	}
	byGame := map[string][]osadapter.StateRef{}
	for _, r := range refs {
		k := strings.ToLower(r.Tag) + "\x00" + strings.ToLower(r.ROMName)
		byGame[k] = append(byGame[k], r)
	}
	systems, err := s.o.Adapter.Systems()
	if err != nil {
		return Listing{}, fmt.Errorf("list states: %w", err)
	}
	for _, sys := range systems {
		err := s.o.Adapter.WalkGames(ctx, sys, func(g osadapter.Game) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return run.game(g, byGame[strings.ToLower(g.System.Tag)+"\x00"+strings.ToLower(g.ROMName)])
		})
		if errors.Is(err, errListFull) {
			run.listing.Truncated = true
			break
		}
		if err != nil {
			return Listing{}, err
		}
	}
	if !run.listing.Truncated {
		cache.keep(run.seen)
	}
	return run.listing, nil
}

// game adds the states of g, by slot.
func (r *inventoryRun) game(g osadapter.Game, refs []osadapter.StateRef) error {
	if len(refs) == 0 {
		return nil
	}
	roots := r.s.o.Adapter.Roots()
	bySlot := map[int]osadapter.StateRef{}
	slots := []int{}
	for _, ref := range refs {
		if !ValidSlot(ref.Slot) {
			continue
		}
		if _, dup := bySlot[ref.Slot]; !dup {
			slots = append(slots, ref.Slot)
		}
		bySlot[ref.Slot] = ref
	}
	sort.Ints(slots)
	for _, slot := range slots {
		ref := bySlot[slot]
		if len(r.listing.States) >= maxListedStates {
			return errListFull
		}
		fi, sum, err := r.hash(roots.States, ref.File.Path, false)
		if errors.Is(err, errFileSkipped) {
			continue
		}
		if err != nil {
			return err
		}
		st := Summary{
			GameID:     library.EncodeID(g.Path),
			SystemID:   library.EncodeID(g.System.Folder),
			GameName:   g.Name,
			FileName:   g.FileName,
			Slot:       slot,
			Path:       ref.File.Rel,
			SizeBytes:  fi.Size(),
			ModifiedAt: fi.ModTime().UTC().Truncate(time.Second),
			SHA256:     sum,
		}
		if p := r.s.o.Adapter.StateThumbnailPath(g, slot); p != "" {
			if found := store.FindFold(filepath.Dir(p), filepath.Base(p), false); found != "" {
				fi, sum, err := r.hash(roots.Minui, found, true)
				switch {
				case err == nil:
					st.Thumbnail = &ThumbnailFile{SizeBytes: fi.Size(), SHA256: sum}
				case !errors.Is(err, errFileSkipped):
					return err
				}
			}
		}
		r.listing.States = append(r.listing.States, st)
	}
	return nil
}

// hash returns the description and SHA-256 of the regular file p, opened
// confined under root without any link (errFileSkipped otherwise, or when
// it is gone; png: also when it does not start with the PNG signature),
// computed once per listing.
func (r *inventoryRun) hash(root, p string, png bool) (fs.FileInfo, string, error) {
	if o, ok := r.done[p]; ok {
		return o.fi, o.sum, o.err
	}
	fi, sum, err := r.hashFile(root, p, png)
	if err == nil || errors.Is(err, errFileSkipped) {
		r.done[p] = hashOutcome{fi, sum, err}
	}
	return fi, sum, err
}

func (r *inventoryRun) hashFile(root, p string, png bool) (fs.FileInfo, string, error) {
	f, fi, err := confine.OpenUnder(root, p)
	if err != nil {
		if errors.Is(err, confine.ErrOutside) {
			r.s.o.Logger.Warn("list states: file skipped", "file", filepath.Base(p), "reason", "outside its folder or reached through a link")
		}
		return nil, "", errFileSkipped
	}
	defer f.Close()
	if png {
		var sig [8]byte
		if _, err := io.ReadFull(f, sig[:]); err != nil || string(sig[:]) != pngSignature {
			return nil, "", errFileSkipped
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, "", errFileSkipped
		}
	}
	r.seen[p] = true
	if sum, ok := r.cache.get(p, fi); ok {
		return fi, sum, nil
	}
	hashedAt := r.cache.now()
	h := sha256.New()
	for n := int64(0); n < fi.Size(); {
		if err := r.ctx.Err(); err != nil {
			return nil, "", err
		}
		c, err := io.CopyN(h, f, min(inventoryChunk, fi.Size()-n))
		n += c
		if err != nil {
			return nil, "", errFileSkipped
		}
	}
	sum := hex.EncodeToString(h.Sum(nil))
	r.cache.put(p, fi, sum, hashedAt)
	return fi, sum, nil
}
