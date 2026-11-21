// Package states manages the emulator save states of the games: the slot
// list, downloads, deletions, the locks (docs/specs/05-console-pak.md
// §5.2) and the history of the resume state (§5.3), with the checks run
// at every start of the pak.
//
// Lock: locks/<TAG>/<ROM file name>/slot<N>.st is the locked copy and
// slot<N>.json its metadata (SHA-256, size, date, restored flag). The copy
// defines the lock (an interrupted lock leaves a copy without metadata,
// repaired at the next start; an interrupted unlock removes the copy
// first, so a metadata file without copy is a leftover).
//
// Resume history: backups/<TAG>/<ROM file name>/resume/<entryId>.st and
// <entryId>.json, at most MaxResumeEntries per game. Entries are ordered by
// a sequence number, never by date (the console clock is not reliable).
package states

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tfournier/ReminisLink/internal/confine"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/store"
)

// MaxSlot is the largest slot number accepted. It matches the state file
// names the NextUI adapter recognizes (".st" and one or two digits).
// NextUI itself uses the slots 0 to 7 (its menu), 8 (a default state it
// never writes) and 9 (automatic resume).
const MaxSlot = 99

// MaxResumeEntries is the length of the resume history (05 §5.3).
const MaxResumeEntries = 5

// Lock statuses (Slot.lockStatus in protocol/openapi.yaml).
const (
	StatusIntact      = "intact"
	StatusRestored    = "restored"
	StatusMissing     = "missing"
	StatusModified    = "modified"     // the state differs from the copy and was not restored
	StatusCoreChanged = "core-changed" // the copy comes from another core's state folder
)

const (
	metaVersion = 1
	resumeDir   = "resume"
	stateExt    = ".st"
	metaExt     = ".json"
	// tempPrefix names the temporary files written in the uploads folder;
	// leftovers are deleted at start with the other orphan files there.
	tempPrefix = "st_tmp_"
)

// pngSignature starts every PNG file.
const pngSignature = "\x89PNG\r\n\x1a\n"

// Errors of the package.
var (
	ErrLocked         = errors.New("state slot is locked")
	ErrInvalidSlot    = errors.New("invalid state slot")
	ErrNoState        = errors.New("no state in this slot")
	ErrNotLocked      = errors.New("state slot is not locked")
	ErrNoThumbnail    = errors.New("no state thumbnail")
	ErrNoResumeSlot   = errors.New("the OS has no resume slot")
	ErrEntryNotFound  = errors.New("resume history entry not found")
	ErrInvalidEntryID = errors.New("invalid resume history entry id")
	ErrCorruptCopy    = errors.New("copy does not match its checksum")
	ErrCoreChanged    = errors.New("the copy comes from the state folder of another emulator core")
	ErrUnknownCore    = errors.New("the state folder of the copy is unknown: lock the slot again to confirm it")
)

var (
	lockFileRe = regexp.MustCompile(`(?i)^slot(\d{1,2})\.st$`)
	// lockLeftRe matches the files of a lock besides its copy: metadata
	// and the copies of the NextUI side files (thumbnail, disc).
	lockLeftRe = regexp.MustCompile(`(?i)^slot(\d{1,2})\.(json|bmp|txt)$`)
	entryIDRe  = regexp.MustCompile(`^rh_\d{8}T\d{6}Z_[0-9a-f]{4}$`)
)

// ValidEntryID reports whether id has the form of a resume history entry id.
func ValidEntryID(id string) bool { return entryIDRe.MatchString(id) }

// Slot is a state slot (Slot in protocol/openapi.yaml).
type Slot struct {
	Slot         int       `json:"slot"`
	SizeBytes    int64     `json:"sizeBytes"`
	ModifiedAt   time.Time `json:"modifiedAt"`
	HasThumbnail bool      `json:"hasThumbnail"`
	Locked       bool      `json:"locked"`
	LockStatus   *string   `json:"lockStatus"`
}

// SlotList is the answer of GET /games/{gameId}/states.
type SlotList struct {
	Slots      []Slot `json:"slots"`
	ResumeSlot *int   `json:"resumeSlot"`
}

// ResumeEntry is an entry of the resume history (ResumeHistoryEntry).
type ResumeEntry struct {
	ID         string    `json:"id"`
	CapturedAt time.Time `json:"capturedAt"`
	SizeBytes  int64     `json:"sizeBytes"`
}

// ValidSlot reports whether slot is a slot number the pak accepts.
func ValidSlot(slot int) bool { return slot >= 0 && slot <= MaxSlot }

// lockFileName is the copy of a locked state in its lock folder.
func lockFileName(slot int) string { return "slot" + strconv.Itoa(slot) + stateExt }

func lockMetaName(slot int) string { return "slot" + strconv.Itoa(slot) + metaExt }

// unknownStateDir marks a lock whose state folder is unknown (metadata
// rebuilt from the copy): it is never restored automatically.
const unknownStateDir = "?"

// lockSideName is the copy, in the lock folder, of a NextUI side file of
// the slot (ext ".bmp" or ".txt").
func lockSideName(slot int, ext string) string { return "slot" + strconv.Itoa(slot) + ext }

// IsLocked reports whether the slot of g is locked: its copy exists in
// locks/<TAG>/<ROM file name>/slot<N>.st (names compared ignoring case, as
// on exFAT).
func IsLocked(layout store.Layout, g osadapter.Game, slot int) bool {
	dir := store.FindFold(layout.LocksDir(g.System.Tag), g.ROMName, true)
	if dir == "" {
		return false
	}
	return store.FindFold(dir, lockFileName(slot), false) != ""
}

// lockMeta is the content of slot<N>.json.
type lockMeta struct {
	Version   int       `json:"version"`
	Slot      int       `json:"slot"`
	SHA256    string    `json:"sha256"`
	SizeBytes int64     `json:"sizeBytes"`
	LockedAt  time.Time `json:"lockedAt"`
	// StateSize and StateModTime (Unix nanoseconds) describe the state
	// file when it was last seen identical to the copy: a state with the
	// same size and date is not hashed again at start.
	StateSize    int64 `json:"stateSize"`
	StateModTime int64 `json:"stateModTime"`
	// Restored is set when the copy was restored at start; cleared by the
	// next GET /games/{gameId}/states that reports it.
	Restored bool `json:"restored"`
	// StateDir is the state folder (<TAG>-<core>) the copy comes from. A
	// lock written before this field takes the current folder at its first
	// reading.
	StateDir string `json:"stateDir,omitempty"`
}

// resumeMeta is the content of <entryId>.json.
type resumeMeta struct {
	Version    int       `json:"version"`
	ID         string    `json:"id"`
	Seq        int64     `json:"seq"`
	CapturedAt time.Time `json:"capturedAt"`
	SizeBytes  int64     `json:"sizeBytes"`
	SHA256     string    `json:"sha256"`
	// StateSize and StateModTime (Unix nanoseconds): the resume state when
	// it was last seen with this content (05 §5.3, date and size first).
	StateSize    int64 `json:"stateSize"`
	StateModTime int64 `json:"stateModTime"`
	// StateDir is the state folder (<TAG>-<core>) of the version; entries
	// of different cores are never compared nor restored into each other.
	StateDir string `json:"stateDir,omitempty"`
}

func (m resumeMeta) public() ResumeEntry {
	return ResumeEntry{ID: m.ID, CapturedAt: m.CapturedAt, SizeBytes: m.SizeBytes}
}

// Options are the dependencies of a Service.
type Options struct {
	Adapter osadapter.Adapter
	Layout  store.Layout
	Trash   *store.Trash
	// Lock serializes every change of the SD card files (shared with the
	// uploads, the saves and the cheats). nil: a lock of its own.
	Lock   *sync.Mutex
	Now    func() time.Time // nil: time.Now (dates and ids only)
	Logger *slog.Logger     // nil: slog.Default()
	Rand   io.Reader        // nil: crypto/rand
}

// Service manages the states, their locks and the resume history.
type Service struct {
	o Options
	// ctx is the context of a running Maintain (nil otherwise), read with
	// the lock held.
	ctx context.Context
}

// context returns the context the long reads stop with.
func (s *Service) context() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

// New returns a Service.
func New(o Options) (*Service, error) {
	if o.Adapter == nil || o.Trash == nil || o.Layout.Root == "" {
		return nil, errors.New("states: missing dependency")
	}
	if o.Lock == nil {
		o.Lock = &sync.Mutex{}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Rand == nil {
		o.Rand = rand.Reader
	}
	return &Service{o: o}, nil
}

// gameRef is the part of a game the state files depend on.
func gameRef(tag, rom string) osadapter.Game {
	return osadapter.Game{System: osadapter.System{Tag: tag}, ROMName: rom, Name: displayName(rom)}
}

func displayName(rom string) string {
	if n := strings.TrimSuffix(rom, filepath.Ext(rom)); n != "" {
		return n
	}
	return rom
}

// lockDir returns the lock folder of a game, "" when it has none.
func (s *Service) lockDir(g osadapter.Game) string {
	return store.FindFold(s.o.Layout.LocksDir(g.System.Tag), g.ROMName, true)
}

// newLockDir returns the lock folder of a game, existing or to create.
func (s *Service) newLockDir(g osadapter.Game) string {
	if d := s.lockDir(g); d != "" {
		return d
	}
	return filepath.Join(s.o.Layout.LocksDir(g.System.Tag), g.ROMName)
}

// resumeDirOf returns the resume history folder of a game (existing
// folders are found ignoring case).
func (s *Service) resumeDirOf(tag, rom string) string {
	tagDir := s.o.Layout.BackupsDir(tag)
	romDir := store.FindFold(tagDir, rom, true)
	if romDir == "" {
		romDir = filepath.Join(tagDir, rom)
	}
	if d := store.FindFold(romDir, resumeDir, true); d != "" {
		return d
	}
	return filepath.Join(romDir, resumeDir)
}

// statePath returns the state file of a slot: the file on the card when
// it exists (its name may differ by case), else the adapter's path ("" when
// the state folder of the system is unknown).
func (s *Service) statePath(g osadapter.Game, slot int) (string, bool) {
	p := s.o.Adapter.StatePath(g, slot)
	if p == "" {
		return "", false
	}
	if found := store.FindFold(filepath.Dir(p), filepath.Base(p), false); found != "" {
		return found, true
	}
	return p, false
}

// writable reports whether the states of g can be written: the state
// format is the default one and the state folder of the system is known.
// The error wraps osadapter.ErrUnsupportedFormat.
func (s *Service) writable(g osadapter.Game) error {
	if err := s.o.Adapter.CheckStateFormat(g); err != nil {
		return err
	}
	if s.o.Adapter.StatePath(g, 0) == "" {
		return fmt.Errorf("%w: no state folder for the tag %s (emulator pak not found)", osadapter.ErrUnsupportedFormat, g.System.Tag)
	}
	return nil
}

// stateDirOf returns the name of the current state folder of g
// (<TAG>-<core>), "" when unknown.
func (s *Service) stateDirOf(g osadapter.Game) string {
	p := s.o.Adapter.StatePath(g, 0)
	if p == "" {
		return ""
	}
	return filepath.Base(filepath.Dir(p))
}

// completeLock fills the state folder of a lock written before it was
// recorded: the current folder. Called with the lock held.
func (s *Service) completeLock(g osadapter.Game, dir string, l *lock) {
	if l == nil || !l.ok || l.meta.StateDir != "" {
		return
	}
	if cur := s.stateDirOf(g); cur != "" {
		l.meta.StateDir = cur
		if err := s.writeLockMeta(dir, l.meta); err != nil {
			s.o.Logger.Warn("states: cannot complete a lock", "game", g.Path, "slot", l.meta.Slot, "error", err)
		}
	}
}

// coreChanged reports whether the copy of l comes from another state
// folder than the current one of g.
// It is false when the current folder or the copy's folder is unknown
// (see unknownCore). Folder names are compared ignoring case (exFAT).
func (s *Service) coreChanged(g osadapter.Game, l *lock) bool {
	if l == nil || !l.ok || l.meta.StateDir == "" || l.meta.StateDir == unknownStateDir {
		return false
	}
	cur := s.stateDirOf(g)
	return cur != "" && !strings.EqualFold(l.meta.StateDir, cur)
}

// unknownCore reports whether the copy of l cannot be tied to the current
// state folder: its folder is unknown (rebuilt metadata), or the current
// folder is (no emulator pak). Such a lock is never restored.
func (s *Service) unknownCore(g osadapter.Game, l *lock) bool {
	return l != nil && (!l.ok || l.meta.StateDir == unknownStateDir || s.stateDirOf(g) == "")
}

// lock describes the lock of a slot.
type lock struct {
	copy string // locked copy
	meta lockMeta
	ok   bool // metadata readable
}

// readLock returns the lock of a slot (nil when the slot is not locked).
func (s *Service) readLock(dir string, slot int) *lock {
	if dir == "" {
		return nil
	}
	cp := store.FindFold(dir, lockFileName(slot), false)
	if cp == "" {
		return nil
	}
	l := &lock{copy: cp}
	if mp := store.FindFold(dir, lockMetaName(slot), false); mp != "" {
		if data, err := os.ReadFile(mp); err == nil && json.Unmarshal(data, &l.meta) == nil &&
			l.meta.Version == metaVersion && l.meta.Slot == slot {
			l.ok = true
		}
	}
	return l
}

func (s *Service) writeLockMeta(dir string, m lockMeta) error {
	p := store.FindFold(dir, lockMetaName(m.Slot), false)
	if p == "" {
		p = filepath.Join(dir, lockMetaName(m.Slot))
	}
	return store.WriteJSONAtomic(p, m)
}

func modTime(fi os.FileInfo) time.Time { return fi.ModTime().UTC().Truncate(time.Second) }

func (s *Service) hasThumbnail(g osadapter.Game, slot int) bool {
	_, err := s.Thumbnail(g, slot)
	return err == nil
}

// slotOf describes a slot whose state is at path (or only locked). The
// boolean is false when the slot has neither a state nor a lock. With a
// lock dir, the lock status compares the state with the copy (recorded
// size and date, SHA-256 when they differ). Called with the lock held.
func (s *Service) slotOf(g osadapter.Game, slot int, path string, dir string, l *lock) (Slot, bool) {
	out := Slot{Slot: slot, HasThumbnail: s.hasThumbnail(g, slot)}
	var fi os.FileInfo
	exists := false
	if path != "" {
		var err error
		fi, err = os.Stat(path)
		exists = err == nil && fi.Mode().IsRegular()
	}
	if exists {
		out.SizeBytes, out.ModifiedAt = fi.Size(), modTime(fi)
	}
	if l == nil {
		return out, exists
	}
	out.Locked = true
	s.completeLock(g, dir, l)
	var status string
	switch {
	case s.coreChanged(g, l):
		status = StatusCoreChanged
	case s.unknownCore(g, l) && !(exists && s.matchesCopy(dir, l, path, fi)):
		status = StatusModified
	case !exists:
		status = StatusMissing
	case !s.matchesCopy(dir, l, path, fi) || !s.sidesMatch(g, slot, dir):
		status = StatusModified
	case l.meta.Restored:
		status = StatusRestored
	default:
		status = StatusIntact
	}
	if !exists {
		if cfi, err := os.Stat(l.copy); err == nil {
			out.SizeBytes, out.ModifiedAt = cfi.Size(), modTime(cfi)
		}
	}
	out.LockStatus = &status
	return out, true
}

// matchesCopy reports whether the state at path (described by fi) holds
// the bytes of the locked copy: same size and recorded date, else same
// SHA-256 (the date is then recorded). Called with the lock held.
func (s *Service) matchesCopy(dir string, l *lock, path string, fi os.FileInfo) bool {
	if !l.ok {
		same, err := store.SameFile(path, l.copy)
		return err == nil && same
	}
	if fi.Size() != l.meta.SizeBytes {
		return false
	}
	if fi.Size() == l.meta.StateSize && fi.ModTime().UnixNano() == l.meta.StateModTime {
		return true
	}
	sum, _, err := store.HashFileContext(s.context(), path)
	if err != nil || sum != l.meta.SHA256 {
		return false
	}
	l.meta.StateSize, l.meta.StateModTime = fi.Size(), fi.ModTime().UnixNano()
	_ = s.writeLockMeta(dir, l.meta)
	return true
}

// List returns the slots of g: the occupied ones and the locked ones whose
// state is missing. A "restored" status is reported once, then cleared.
func (s *Service) List(g osadapter.Game) (SlotList, error) {
	s.o.Lock.Lock()
	defer s.o.Lock.Unlock()
	files, err := s.o.Adapter.GameFiles(g)
	if err != nil {
		return SlotList{}, fmt.Errorf("states: %w", err)
	}
	dir := s.lockDir(g)
	paths := map[int]string{}
	for _, st := range files.States {
		paths[st.Slot] = st.Path
	}
	if dir != "" {
		des, _ := os.ReadDir(dir)
		for _, d := range des {
			if m := lockFileRe.FindStringSubmatch(d.Name()); m != nil && d.Type().IsRegular() {
				slot, _ := strconv.Atoi(m[1])
				if _, ok := paths[slot]; !ok {
					paths[slot] = s.o.Adapter.StatePath(g, slot)
				}
			}
		}
	}
	out := SlotList{Slots: []Slot{}}
	if rs, ok := s.o.Adapter.ResumeSlot(); ok {
		out.ResumeSlot = &rs
	}
	for slot, p := range paths {
		l := s.readLock(dir, slot)
		sl, ok := s.slotOf(g, slot, p, dir, l)
		if !ok {
			continue
		}
		out.Slots = append(out.Slots, sl)
		if l != nil && l.ok && l.meta.Restored && sl.LockStatus != nil && *sl.LockStatus == StatusRestored {
			l.meta.Restored = false
			if err := s.writeLockMeta(dir, l.meta); err != nil {
				s.o.Logger.Warn("states: cannot clear the restored flag", "game", g.Path, "slot", slot, "error", err)
			}
		}
	}
	sort.Slice(out.Slots, func(i, j int) bool { return out.Slots[i].Slot < out.Slots[j].Slot })
	return out, nil
}

// slotNow describes a slot after a change. Called with the lock held.
func (s *Service) slotNow(g osadapter.Game, slot int) Slot {
	p, _ := s.statePath(g, slot)
	dir := s.lockDir(g)
	sl, _ := s.slotOf(g, slot, p, dir, s.readLock(dir, slot))
	return sl
}

// Open opens the state of a slot for reading and returns its file name.
func (s *Service) Open(g osadapter.Game, slot int) (*os.File, string, error) {
	if !ValidSlot(slot) {
		return nil, "", ErrInvalidSlot
	}
	s.o.Lock.Lock()
	defer s.o.Lock.Unlock()
	p, ok := s.statePath(g, slot)
	if !ok {
		return nil, "", ErrNoState
	}
	f, _, err := confine.OpenUnder(s.cardRoot(p), p)
	if err != nil {
		return nil, "", fmt.Errorf("state: %w", err)
	}
	return f, filepath.Base(p), nil
}

// Thumbnail returns the thumbnail of a slot (ErrNoThumbnail when the OS
// keeps none, or the slot has none).
func (s *Service) Thumbnail(g osadapter.Game, slot int) (string, error) {
	f, p, err := s.OpenThumbnail(g, slot)
	if err != nil {
		return "", err
	}
	f.Close()
	return p, nil
}

// OpenThumbnail opens the thumbnail of a slot for reading (confined, with
// its PNG signature checked) and returns it positioned at its start, with
// its path. The caller serves this descriptor, never the path again
// (Codex review of M20: the file could be replaced in between).
func (s *Service) OpenThumbnail(g osadapter.Game, slot int) (*os.File, string, error) {
	if !ValidSlot(slot) {
		return nil, "", ErrInvalidSlot
	}
	p := s.o.Adapter.StateThumbnailPath(g, slot)
	if p == "" {
		return nil, "", ErrNoThumbnail
	}
	if p = store.FindFold(filepath.Dir(p), filepath.Base(p), false); p == "" {
		return nil, "", ErrNoThumbnail
	}
	// NextUI writes a PNG image in a ".bmp" file: check the signature.
	f, _, err := confine.OpenUnder(s.cardRoot(p), p)
	if err != nil {
		return nil, "", ErrNoThumbnail
	}
	var sig [8]byte
	if _, err := io.ReadFull(f, sig[:]); err != nil || string(sig[:]) != pngSignature {
		f.Close()
		return nil, "", ErrNoThumbnail
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, "", ErrNoThumbnail
	}
	return f, p, nil
}

// Delete moves the state of a slot to the trash (ErrLocked when locked).
func (s *Service) Delete(g osadapter.Game, slot int) (store.MoveResult, error) {
	if !ValidSlot(slot) {
		return store.MoveResult{}, ErrInvalidSlot
	}
	s.o.Lock.Lock()
	defer s.o.Lock.Unlock()
	if IsLocked(s.o.Layout, g, slot) {
		return store.MoveResult{}, ErrLocked
	}
	p, ok := s.statePath(g, slot)
	if !ok {
		return store.MoveResult{}, ErrNoState
	}
	items := append([]store.MoveItem{{Path: p}}, s.sideItems(g, slot)...)
	res, err := s.o.Trash.Move(store.ReasonDeleteState, g.Name, items)
	if err != nil {
		return store.MoveResult{}, fmt.Errorf("delete state: %w", err)
	}
	s.o.Logger.Info("state deleted (to the trash)", "game", g.Path, "slot", slot, "trash", res.Entry.ID)
	return res, nil
}

// Lock copies the state of a slot to the lock area with its SHA-256. A
// slot already locked is left as it is.
func (s *Service) Lock(g osadapter.Game, slot int) (Slot, error) {
	if !ValidSlot(slot) {
		return Slot{}, ErrInvalidSlot
	}
	s.o.Lock.Lock()
	defer s.o.Lock.Unlock()
	if err := s.writable(g); err != nil {
		return Slot{}, err
	}
	p, ok := s.statePath(g, slot)
	if l := s.readLock(s.lockDir(g), slot); l != nil {
		if !ok || !s.unknownCore(g, l) {
			return s.slotNow(g, slot), nil
		}
		// Locking again confirms a lock whose folder is unknown: the old
		// copy goes to the trash and the current state is locked.
		if err := s.dropLock(g, slot, s.lockDir(g), l, true); err != nil {
			return Slot{}, fmt.Errorf("lock: %w", err)
		}
	}
	if !ok {
		return Slot{}, ErrNoState
	}
	fi, err := os.Stat(p)
	if err != nil {
		return Slot{}, fmt.Errorf("lock: %w", err)
	}
	dir := s.newLockDir(g)
	// Never written through a link (rule 2): the lock folder, or a folder
	// above it in the ReminisLink data area, may have been replaced by one.
	if err := s.checkDataDir(dir); err != nil {
		return Slot{}, fmt.Errorf("lock: %w", err)
	}
	// The side files first (thumbnail, disc of a multi-disc state), then
	// the copy, which defines the lock (see the package comment).
	for _, side := range s.o.Adapter.StateSideFiles(g, slot) {
		if f := store.FindFold(filepath.Dir(side), filepath.Base(side), false); f != "" {
			if _, _, err := store.CopyFileAtomic(f, filepath.Join(dir, lockSideName(slot, filepath.Ext(side)))); err != nil {
				return Slot{}, fmt.Errorf("lock: %w", err)
			}
		}
	}
	cp := filepath.Join(dir, lockFileName(slot))
	sum, size, err := store.CopyFileAtomic(p, cp)
	if err != nil {
		return Slot{}, fmt.Errorf("lock: %w", err)
	}
	m := lockMeta{
		Version: metaVersion, Slot: slot, SHA256: sum, SizeBytes: size,
		LockedAt: s.o.Now().UTC().Truncate(time.Second), StateDir: filepath.Base(filepath.Dir(p)),
	}
	if size == fi.Size() {
		m.StateSize, m.StateModTime = fi.Size(), fi.ModTime().UnixNano()
	}
	if err := s.writeLockMeta(dir, m); err != nil {
		// Repaired at the next start from the copy.
		s.o.Logger.Warn("lock: metadata not written", "game", g.Path, "slot", slot, "error", err)
	}
	s.o.Logger.Info("state locked", "game", g.Path, "slot", slot, "copy", cp)
	return s.slotNow(g, slot), nil
}

// Unlock removes the lock of a slot. The state does not change. The locked
// copy is erased only when the state holds the same bytes; otherwise (state
// missing or different) the copy goes to the trash, so no version is lost.
func (s *Service) Unlock(g osadapter.Game, slot int) (Slot, error) {
	if !ValidSlot(slot) {
		return Slot{}, ErrInvalidSlot
	}
	s.o.Lock.Lock()
	defer s.o.Lock.Unlock()
	dir := s.lockDir(g)
	l := s.readLock(dir, slot)
	if l == nil {
		// Already unlocked: nothing to do.
		if _, ok := s.statePath(g, slot); !ok {
			return Slot{}, ErrNoState
		}
		return s.slotNow(g, slot), nil
	}
	p, _ := s.statePath(g, slot)
	same, err := store.SameFile(p, l.copy)
	if err != nil {
		return Slot{}, fmt.Errorf("unlock: %w", err)
	}
	var res Slot
	if !same {
		// Describe the slot while the copy is still there.
		res, _ = s.slotOf(g, slot, p, "", nil)
		if fi, err := os.Stat(l.copy); err == nil && res.ModifiedAt.IsZero() {
			res.ModifiedAt = modTime(fi)
		}
	}
	if err := s.dropLock(g, slot, dir, l, !same); err != nil {
		return Slot{}, fmt.Errorf("unlock: %w", err)
	}
	if same {
		res = s.slotNow(g, slot)
	}
	return res, nil
}

// dropLock removes the lock of a slot: its copy and side copies go to the
// trash when keep is true, else they are erased (the same bytes are in
// place); the metadata is removed last. Called with the lock held.
func (s *Service) dropLock(g osadapter.Game, slot int, dir string, l *lock, keep bool) error {
	files := []string{l.copy}
	for _, ext := range []string{".bmp", ".txt"} {
		if f := store.FindFold(dir, lockSideName(slot, ext), false); f != "" {
			files = append(files, f)
		}
	}
	if keep {
		var items []store.MoveItem
		for _, f := range files {
			items = append(items, store.MoveItem{Path: f})
		}
		mr, err := s.o.Trash.Move(store.ReasonDeleteState, g.Name, items)
		if err != nil {
			return err
		}
		s.o.Logger.Info("lock removed, its copy differs and went to the trash", "game", g.Path, "slot", slot, "trash", mr.Entry.ID)
	} else {
		for _, f := range files {
			if err := os.Remove(f); err != nil {
				return err
			}
		}
		s.o.Logger.Info("lock removed", "game", g.Path, "slot", slot)
	}
	if mp := store.FindFold(dir, lockMetaName(slot), false); mp != "" {
		_ = os.Remove(mp)
	}
	store.SyncDir(dir)
	removeIfEmpty(dir)
	return nil
}

// removeIfEmpty removes an empty folder (errors ignored).
func removeIfEmpty(dir string) {
	if des, err := os.ReadDir(dir); err == nil && len(des) == 0 {
		_ = os.Remove(dir)
	}
}

// RestoreLock replaces the state of a slot with its locked copy (POST
// /games/{gameId}/states/{slot}/restore); the current state goes to the
// trash first.
func (s *Service) RestoreLock(g osadapter.Game, slot int) (Slot, error) {
	if !ValidSlot(slot) {
		return Slot{}, ErrInvalidSlot
	}
	s.o.Lock.Lock()
	defer s.o.Lock.Unlock()
	dir := s.lockDir(g)
	l := s.readLock(dir, slot)
	if l == nil {
		return Slot{}, ErrNotLocked
	}
	if err := s.writable(g); err != nil {
		return Slot{}, err
	}
	if !l.ok {
		if err := s.repairLockMeta(dir, slot, l); err != nil {
			return Slot{}, err
		}
	}
	s.completeLock(g, dir, l)
	if s.coreChanged(g, l) {
		return Slot{}, ErrCoreChanged
	}
	if s.unknownCore(g, l) {
		return Slot{}, ErrUnknownCore
	}
	p, exists := s.statePath(g, slot)
	if exists {
		if same, err := store.SameFile(p, l.copy); err != nil {
			return Slot{}, fmt.Errorf("restore: %w", err)
		} else if same && s.sidesMatch(g, slot, dir) {
			return s.slotNow(g, slot), nil
		}
	}
	if err := s.restoreCopy(g, slot, dir, l, p, exists, store.ReasonRestore, false); err != nil {
		return Slot{}, err
	}
	s.o.Logger.Info("locked state restored", "game", g.Path, "slot", slot)
	return s.slotNow(g, slot), nil
}

// restoreCopy puts the locked copy back at path: the copy (and the copies
// of its side files) are first copied to temporary files and checked
// against its SHA-256, then the current state and side files go to the
// trash and the temporary files are renamed in place, as one replacement
// journaled in the trash entry (store.Trash.Replace): a power cut between
// two renames (the state restored, its disc file not yet) is finished or
// rolled back at the next start, never left half done. markRestored sets
// the restored flag before the first change, so a power cut never loses
// it. Called with the lock held.
func (s *Service) restoreCopy(g osadapter.Game, slot int, dir string, l *lock, path string, exists bool, reason string, markRestored bool) error {
	tmp, err := s.tempFile()
	if err != nil {
		return err
	}
	// The temporary files are removed (already gone after a successful
	// replacement) unless a pending replacement still names them.
	temps := []string{tmp}
	pending := false
	defer func() {
		if !pending {
			for _, p := range temps {
				os.Remove(p)
			}
		}
	}()
	sum, size, err := store.CopyFileAtomicContext(s.context(), l.copy, tmp)
	if err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	if sum != l.meta.SHA256 {
		return ErrCorruptCopy
	}
	// The side files: a copy kept with the lock replaces the current file
	// (which goes to the trash with the state); without a copy, a stale
	// thumbnail goes to the trash but the disc file stays (an old lock
	// without it: the current disc is the best guess).
	installs := []store.Install{{Src: tmp, Dst: path, Size: size, SHA256: sum}}
	var items []store.MoveItem
	if exists {
		items = append(items, store.MoveItem{Path: path})
	}
	for _, side := range s.o.Adapter.StateSideFiles(g, slot) {
		ext := filepath.Ext(side)
		cur := store.FindFold(filepath.Dir(side), filepath.Base(side), false)
		saved := store.FindFold(dir, lockSideName(slot, ext), false)
		if saved != "" {
			dst := side
			if cur != "" {
				dst = cur
			}
			sideTmp := tmp + ext
			temps = append(temps, sideTmp)
			sideSum, sideSize, err := store.CopyFileAtomicContext(s.context(), saved, sideTmp)
			if err != nil {
				return fmt.Errorf("restore: %w", err)
			}
			installs = append(installs, store.Install{Src: sideTmp, Dst: dst, Size: sideSize, SHA256: sideSum})
		}
		if cur != "" && (saved != "" || ext != ".txt") {
			items = append(items, store.MoveItem{Path: cur})
		}
	}
	before := l.meta
	if markRestored {
		l.meta.Restored = true
		if err := s.writeLockMeta(dir, l.meta); err != nil {
			return fmt.Errorf("restore: %w", err)
		}
	}
	if _, err := s.o.Trash.Replace(reason, g.Name, items, installs); err != nil {
		pending = errors.Is(err, store.ErrReplaceFailed)
		if markRestored && !pending {
			_ = s.writeLockMeta(dir, before)
		}
		return fmt.Errorf("restore: %w", err)
	}
	if fi, err := os.Stat(path); err == nil {
		l.meta.StateSize, l.meta.StateModTime = fi.Size(), fi.ModTime().UnixNano()
		_ = s.writeLockMeta(dir, l.meta)
	}
	return nil
}

// sidesMatch reports whether the side files of a locked slot (thumbnail,
// disc of a multi-disc state) hold the bytes of the copies kept with the
// lock: a lock is only intact when they do too. Called with the lock held.
func (s *Service) sidesMatch(g osadapter.Game, slot int, dir string) bool {
	for _, side := range s.o.Adapter.StateSideFiles(g, slot) {
		saved := store.FindFold(dir, lockSideName(slot, filepath.Ext(side)), false)
		if saved == "" {
			continue
		}
		cur := store.FindFold(filepath.Dir(side), filepath.Base(side), false)
		if cur == "" {
			return false
		}
		if same, err := store.SameFile(cur, saved); err != nil || !same {
			return false
		}
	}
	return true
}

// repairLockMeta rebuilds the metadata of a lock from its copy (a lock
// interrupted before its metadata was written).
func (s *Service) repairLockMeta(dir string, slot int, l *lock) error {
	sum, size, err := store.HashFile(l.copy)
	if err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	// The folder of the copy is unknown: never assume the current one.
	l.meta = lockMeta{
		Version: metaVersion, Slot: slot, SHA256: sum, SizeBytes: size,
		LockedAt: s.o.Now().UTC().Truncate(time.Second), StateDir: unknownStateDir,
	}
	l.ok = true
	return s.writeLockMeta(dir, l.meta)
}

// InstallLocked makes the synced file tmp the state of a slot of g (the
// "state" target of the uploads and downloads), with the lock held:
// refused when the slot is locked (ErrLocked); the current state of the
// slot, if any, is first moved to the trash.
func (s *Service) InstallLocked(g osadapter.Game, slot int, tmp string) (Slot, error) {
	if !ValidSlot(slot) {
		return Slot{}, ErrInvalidSlot
	}
	if IsLocked(s.o.Layout, g, slot) {
		return Slot{}, ErrLocked
	}
	if err := s.writable(g); err != nil {
		return Slot{}, err
	}
	p, exists := s.statePath(g, slot)
	if err := confine.CheckDir(s.cardRoot(p), filepath.Dir(p)); err != nil {
		return Slot{}, fmt.Errorf("state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return Slot{}, fmt.Errorf("state: %w", err)
	}
	var items []store.MoveItem
	if exists {
		// The thumbnail of the old state goes with it; the disc file stays:
		// the user knows which disc the uploaded state belongs to.
		items = []store.MoveItem{{Path: p}}
		for _, it := range s.sideItems(g, slot) {
			if !strings.EqualFold(filepath.Ext(it.Path), ".txt") {
				items = append(items, it)
			}
		}
	}
	fi, err := os.Lstat(tmp)
	if err != nil {
		return Slot{}, fmt.Errorf("state: install: %w", err)
	}
	// One journaled replacement: a power cut after the old state went to
	// the trash is finished or rolled back at the next start.
	if _, err := s.o.Trash.Replace(store.ReasonOverwrite, g.Name, items, []store.Install{{Src: tmp, Dst: p, Size: fi.Size()}}); err != nil {
		return Slot{}, fmt.Errorf("state: install: %w", err)
	}
	s.o.Logger.Info("state replaced", "game", g.Path, "slot", slot)
	return s.slotNow(g, slot), nil
}

// sideItems are the existing OS files tied to a slot (thumbnail, disc of a
// multi-disc state): they go to the trash with the state they describe,
// so a stale thumbnail never stays next to another state.
func (s *Service) sideItems(g osadapter.Game, slot int) []store.MoveItem {
	var out []store.MoveItem
	for _, p := range s.o.Adapter.StateSideFiles(g, slot) {
		if f := store.FindFold(filepath.Dir(p), filepath.Base(p), false); f != "" {
			out = append(out, store.MoveItem{Path: f})
		}
	}
	return out
}

// tempFile returns a free temporary path in the uploads folder (same SD
// card as the states, so the final rename is atomic).
func (s *Service) tempFile() (string, error) {
	dir := s.o.Layout.UploadsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	b := make([]byte, 8)
	if _, err := io.ReadFull(s.o.Rand, b); err != nil {
		return "", err
	}
	return filepath.Join(dir, tempPrefix+hex.EncodeToString(b)+stateExt), nil
}

// --- Resume history --------------------------------------------------------

// readResume returns the complete entries of a history folder, newest
// (largest sequence) first.
func (s *Service) readResume(dir string) []resumeMeta {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []resumeMeta
	for _, d := range des {
		id, ok := strings.CutSuffix(d.Name(), metaExt)
		if !ok || !ValidEntryID(id) {
			continue
		}
		var m resumeMeta
		data, err := os.ReadFile(filepath.Join(dir, d.Name()))
		if err != nil || json.Unmarshal(data, &m) != nil || m.Version != metaVersion || m.ID != id {
			s.o.Logger.Warn("resume history: unreadable entry", "entry", id)
			continue
		}
		if fi, err := os.Lstat(filepath.Join(dir, id+stateExt)); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Seq != out[j].Seq {
			return out[i].Seq > out[j].Seq
		}
		return out[i].ID > out[j].ID
	})
	return out
}

// ResumeHistory returns the resume history of g, newest first.
func (s *Service) ResumeHistory(g osadapter.Game) []ResumeEntry {
	s.o.Lock.Lock()
	defer s.o.Lock.Unlock()
	return s.historyLocked(g)
}

func (s *Service) historyLocked(g osadapter.Game) []ResumeEntry {
	metas := s.readResume(s.resumeDirOf(g.System.Tag, g.ROMName))
	out := make([]ResumeEntry, 0, len(metas))
	for _, m := range metas {
		out = append(out, m.public())
	}
	return out
}

// archiveResult says what archiveLocked did.
type archiveResult int

const (
	archiveUnchanged archiveResult = iota // same date and size as an entry
	archiveSame                           // same content as an entry (hashed)
	archiveAdded
)

// archiveLocked archives the resume state at path for (tag, rom) when it
// differs from the entries of the history (05 §5.3: date and size first,
// SHA-256 only when they changed; the history keeps distinct versions),
// then moves the entries past MaxResumeEntries to the trash. With
// hashAlways (before a restore overwrites the state), the content is
// always compared by SHA-256: a state with the date and size of an entry
// but other bytes is archived too. Called with the lock held.
func (s *Service) archiveLocked(tag, rom, path string, hashAlways bool) (archiveResult, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	dir := s.resumeDirOf(tag, rom)
	if err := s.checkDataDir(dir); err != nil {
		return 0, err
	}
	stateDir := filepath.Base(filepath.Dir(path))
	metas := s.readResume(dir)
	// An entry without folder (written before it was recorded) is of an
	// unknown core: never compared with the current state, never restored.
	mod := fi.ModTime().UnixNano()
	for _, m := range metas {
		if !hashAlways && strings.EqualFold(m.StateDir, stateDir) && m.StateSize == fi.Size() && m.StateModTime == mod {
			return archiveUnchanged, nil
		}
	}
	sum, size, err := store.HashFileContext(s.context(), path)
	if err != nil {
		return 0, err
	}
	for _, m := range metas {
		if strings.EqualFold(m.StateDir, stateDir) && m.SHA256 == sum && m.SizeBytes == size {
			// Same version: remember its date so it is not hashed again.
			m.StateSize, m.StateModTime = fi.Size(), mod
			if err := store.WriteJSONAtomic(filepath.Join(dir, m.ID+metaExt), m); err != nil {
				s.o.Logger.Warn("resume history: cannot update an entry", "entry", m.ID, "error", err)
			}
			return archiveSame, nil
		}
	}
	var seq int64
	for _, m := range metas {
		seq = max(seq, m.Seq)
	}
	captured := fi.ModTime().UTC().Truncate(time.Second)
	id, err := store.NewStampID("rh", captured, s.o.Rand, func(id string) bool {
		for _, ext := range []string{stateExt, metaExt} {
			if _, err := os.Lstat(filepath.Join(dir, id+ext)); !errors.Is(err, fs.ErrNotExist) {
				return true
			}
		}
		return false
	})
	if err != nil {
		return 0, err
	}
	sum, size, err = store.CopyFileAtomicContext(s.context(), path, filepath.Join(dir, id+stateExt))
	if err != nil {
		return 0, err
	}
	m := resumeMeta{
		Version: metaVersion, ID: id, Seq: seq + 1, CapturedAt: captured, SizeBytes: size, SHA256: sum,
		StateSize: fi.Size(), StateModTime: mod, StateDir: stateDir,
	}
	if err := store.WriteJSONAtomic(filepath.Join(dir, id+metaExt), m); err != nil {
		os.Remove(filepath.Join(dir, id+stateExt))
		return 0, err
	}
	s.o.Logger.Info("resume state archived", "tag", tag, "rom", rom, "entry", id, "bytes", size)
	all := append([]resumeMeta{m}, metas...)
	for _, old := range all[min(len(all), MaxResumeEntries):] {
		_, err := s.o.Trash.Move(store.ReasonDeleteBackup, displayName(rom), []store.MoveItem{
			{Path: filepath.Join(dir, old.ID+stateExt)},
			{Path: filepath.Join(dir, old.ID+metaExt), Internal: true},
		})
		if err != nil {
			s.o.Logger.Warn("resume history: cannot move an old entry to the trash", "entry", old.ID, "error", err)
		}
	}
	return archiveAdded, nil
}

// RestoreResume replaces the resume state of g with a history entry; the
// current resume state is archived first. It returns the updated history.
func (s *Service) RestoreResume(g osadapter.Game, entryID string) ([]ResumeEntry, error) {
	if !ValidEntryID(entryID) {
		return nil, ErrInvalidEntryID
	}
	slot, ok := s.o.Adapter.ResumeSlot()
	if !ok {
		return nil, ErrNoResumeSlot
	}
	s.o.Lock.Lock()
	defer s.o.Lock.Unlock()
	dir := s.resumeDirOf(g.System.Tag, g.ROMName)
	var entry *resumeMeta
	for _, m := range s.readResume(dir) {
		if m.ID == entryID {
			entry = &m
			break
		}
	}
	if entry == nil {
		return nil, ErrEntryNotFound
	}
	if IsLocked(s.o.Layout, g, slot) {
		return nil, ErrLocked
	}
	if err := s.writable(g); err != nil {
		return nil, err
	}
	if cur := s.stateDirOf(g); entry.StateDir == "" || !strings.EqualFold(entry.StateDir, cur) {
		// A state of another core cannot be loaded by the current one.
		return nil, ErrCoreChanged
	}
	tmp, err := s.tempFile()
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp)
	sum, _, err := store.CopyFileAtomic(filepath.Join(dir, entryID+stateExt), tmp)
	if err != nil {
		return nil, fmt.Errorf("restore resume: %w", err)
	}
	if sum != entry.SHA256 {
		return nil, ErrCorruptCopy
	}
	p, exists := s.statePath(g, slot)
	if exists {
		if _, err := s.archiveLocked(g.System.Tag, g.ROMName, p, true); err != nil {
			return nil, fmt.Errorf("restore resume: archive the current state: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, fmt.Errorf("restore resume: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return nil, fmt.Errorf("restore resume: %w", err)
	}
	store.SyncDir(filepath.Dir(p))
	s.o.Logger.Info("resume state restored", "game", g.Path, "entry", entryID)
	return s.historyLocked(g), nil
}

// --- Maintenance at start ---------------------------------------------------

// RestoredLock is a locked state restored at start.
type RestoredLock struct {
	Tag, ROMName string
	Slot         int
	Missing      bool // the state was gone (else it was overwritten)
}

// Report is the outcome of the maintenance of the states.
type Report struct {
	Archived     int            // resume states added to their history
	LocksChecked int            // locks compared with their state
	LocksIntact  int            // identical to the locked copy
	Restored     []RestoredLock // locked copies put back
	Failed       int            // archives or locks that could not be handled
	Skipped      int            // left for the next start (time limit)
	// Held are the locks left as they are although their state differs:
	// unsupported state format, core changed, game gone. Nothing is lost:
	// the copy stays locked.
	Held int
}

// Maintain runs the checks of docs/specs/05-console-pak.md §2.3 before the
// server opens: the resume states that changed are archived (§5.3), then
// every lock is checked and its copy restored when the state was
// overwritten or removed (§5.2). It stops when ctx is done (time limit):
// what is left is checked at the next start. Errors on one item are logged
// and counted, never fatal.
func (s *Service) Maintain(ctx context.Context) Report {
	s.o.Lock.Lock()
	defer s.o.Lock.Unlock()
	// The long reads (hashes, copies) stop with ctx too; the writes
	// already engaged are journaled (store.Trash.Replace).
	s.ctx = ctx
	defer func() { s.ctx = nil }()
	var r Report
	s.archiveAll(ctx, &r)
	s.checkLocks(ctx, &r)
	return r
}

func (s *Service) archiveAll(ctx context.Context, r *Report) {
	slot, ok := s.o.Adapter.ResumeSlot()
	if !ok {
		return
	}
	refs, err := s.o.Adapter.StateFiles(ctx)
	if ctx.Err() != nil {
		r.Skipped++
		return
	}
	if err != nil {
		s.o.Logger.Warn("maintenance: cannot list the states", "error", err)
		r.Failed++
		return
	}
	for _, ref := range refs {
		if ref.Slot != slot {
			continue
		}
		if ctx.Err() != nil {
			r.Skipped++
			continue
		}
		if err := s.o.Adapter.CheckStateFormat(gameRef(ref.Tag, ref.ROMName)); err != nil {
			// Not files the pak names: archiving them could mislead.
			continue
		}
		res, err := s.archiveLocked(ref.Tag, ref.ROMName, ref.File.Path, false)
		if err != nil && ctx.Err() != nil {
			r.Skipped++ // stopped in the middle: the next start does it
			continue
		}
		if err != nil {
			s.o.Logger.Warn("maintenance: cannot archive a resume state", "tag", ref.Tag, "rom", ref.ROMName, "error", err)
			r.Failed++
			continue
		}
		if res == archiveAdded {
			r.Archived++
		}
	}
}

func (s *Service) checkLocks(ctx context.Context, r *Report) {
	root := filepath.Join(s.o.Layout.Root, "locks")
	tags, err := os.ReadDir(root)
	if err != nil {
		return // no lock
	}
	for _, tag := range tags {
		if !tag.IsDir() {
			continue
		}
		roms, _ := os.ReadDir(filepath.Join(root, tag.Name()))
		for _, rom := range roms {
			if !rom.IsDir() {
				continue
			}
			dir := filepath.Join(root, tag.Name(), rom.Name())
			store.RemoveTemps(dir)
			des, _ := os.ReadDir(dir)
			copies := map[int]bool{}
			for _, d := range des {
				m := lockFileRe.FindStringSubmatch(d.Name())
				if m == nil || !d.Type().IsRegular() {
					continue
				}
				slot, _ := strconv.Atoi(m[1])
				copies[slot] = true
				if ctx.Err() != nil {
					r.Skipped++
					continue
				}
				r.LocksChecked++
				s.checkLock(gameRef(tag.Name(), rom.Name()), dir, slot, r)
			}
			// Metadata and side copies left by an interrupted lock or unlock.
			for _, d := range des {
				if m := lockLeftRe.FindStringSubmatch(d.Name()); m != nil {
					if slot, _ := strconv.Atoi(m[1]); !copies[slot] {
						_ = os.Remove(filepath.Join(dir, d.Name()))
					}
				}
			}
			removeIfEmpty(dir)
		}
	}
}

// checkLock applies 05 §5.2 to one lock.
func (s *Service) checkLock(g osadapter.Game, dir string, slot int, r *Report) {
	log := s.o.Logger.With("tag", g.System.Tag, "rom", g.ROMName, "slot", slot)
	l := s.readLock(dir, slot)
	if l == nil {
		return
	}
	if !l.ok {
		if err := s.repairLockMeta(dir, slot, l); err != nil {
			log.Warn("maintenance: cannot repair a lock", "error", err)
			r.Failed++
			return
		}
		log.Info("maintenance: lock metadata rebuilt from its copy")
	}
	p, exists := s.statePath(g, slot)
	if p == "" {
		log.Warn("maintenance: lock not checked, no state folder for its tag (emulator pak not found)")
		r.Held++
		return
	}
	if err := s.o.Adapter.CheckStateFormat(g); err != nil {
		log.Warn("maintenance: lock not checked, state format not supported", "error", err)
		r.Held++
		return
	}
	s.completeLock(g, dir, l)
	if l.meta.StateDir == unknownStateDir {
		if exists {
			if fi, err := os.Stat(p); err == nil && s.matchesCopy(dir, l, p, fi) {
				r.LocksIntact++
				return
			}
		}
		log.Warn("maintenance: lock not restored, the state folder of its copy is unknown (lock it again to confirm)")
		r.Held++
		return
	}
	if s.coreChanged(g, l) {
		log.Warn("maintenance: lock not restored, its copy comes from another emulator core", "copyFolder", l.meta.StateDir, "currentFolder", s.stateDirOf(g))
		r.Held++
		return
	}
	if exists {
		fi, err := os.Stat(p)
		if err != nil {
			log.Warn("maintenance: cannot read a locked state", "error", err)
			r.Failed++
			return
		}
		if s.matchesCopy(dir, l, p, fi) && s.sidesMatch(g, slot, dir) {
			r.LocksIntact++
			return
		}
	}
	if !s.o.Adapter.GameExists(g.System.Tag, g.ROMName) {
		// The game was deleted (keepSave) or renamed: never recreate its
		// state at every start. The lock stays, nothing is lost.
		log.Warn("maintenance: lock not restored, its game no longer exists")
		r.Held++
		return
	}
	if err := s.restoreCopy(g, slot, dir, l, p, exists, store.ReasonLockRestore, true); err != nil {
		log.Error("maintenance: cannot restore a locked state", "error", err)
		r.Failed++
		return
	}
	r.Restored = append(r.Restored, RestoredLock{Tag: g.System.Tag, ROMName: g.ROMName, Slot: slot, Missing: !exists})
	if exists {
		log.Info("maintenance: locked state was overwritten, copy restored (overwritten state in the trash)", "copy", l.copy)
	} else {
		log.Info("maintenance: locked state was missing, copy restored", "copy", l.copy)
	}
}

// cardRoot is the folder a state file p must be read under, with no link
// below it (rule 2): the card, or the states or thumbnails folder when it
// is elsewhere.
func (s *Service) cardRoot(p string) string {
	r := s.o.Adapter.Roots()
	for _, root := range []string{r.SD, r.States, r.Minui} {
		if _, err := confine.RelToRoot(root, p); root != "" && err == nil {
			return root
		}
	}
	return filepath.Dir(p)
}

// checkDataDir refuses a write in dir, a folder of the ReminisLink data
// area, when a folder between the root of the area and dir is a symbolic
// link (rule 2). The root itself is checked at start (app.Open).
func (s *Service) checkDataDir(dir string) error {
	return confine.CheckDir(s.o.Layout.Root, dir)
}
