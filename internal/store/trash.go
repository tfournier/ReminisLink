package store

import (
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
	"strings"
	"sync"
	"time"
)

// The trash (docs/specs/05-console-pak.md §4): every user file that
// ReminisLink deletes or replaces is moved (renamed, same SD card) into
// trash/<entryId>/<path relative to the SD card>, next to an entry.json
// that describes the operation. Only Delete, Empty and Purge erase files.
//
// Power loss: entry.json is written with state "moving" before the first
// rename and rewritten with state "done" after the last one. An entry
// still "moving" when the pak starts is rolled back by Recover (each file
// goes back to its original place when that place is free), so an
// interrupted operation looks like it never happened and nothing is lost.
// Permanent deletion first renames the entry folder to .deleting-<id>, so
// a half-deleted entry is never listed and is finished at the next start.

// TrashRetention is how long an entry is kept before Purge erases it.
const TrashRetention = 30 * 24 * time.Hour

// Trash reasons (TrashEntry.reason in protocol/openapi.yaml).
const (
	ReasonDeleteGame  = "delete-game"
	ReasonDeleteState = "delete-state"
	ReasonOverwrite   = "overwrite"
	ReasonReplaceROM  = "replace-rom"
	ReasonLockRestore = "lock-restore"
	ReasonRestore     = "restore"
)

var validReasons = map[string]bool{
	ReasonDeleteGame: true, ReasonDeleteState: true, ReasonOverwrite: true,
	ReasonReplaceROM: true, ReasonLockRestore: true, ReasonRestore: true,
}

// Errors of the trash.
var (
	ErrTrashNotFound  = errors.New("trash entry not found")
	ErrInvalidTrashID = errors.New("invalid trash entry id")
	ErrNothingToMove  = errors.New("nothing to move to the trash")
)

const (
	trashEntryFile      = "entry.json"
	trashDeletingPrefix = ".deleting-"
	trashEntryVersion   = 1
	stateMoving         = "moving"
	stateDone           = "done"
	trashIDTimeLayout   = "20060102T150405Z"
)

// trashIDRe matches tr_<YYYYMMDDTHHMMSSZ>_<4 hex>.
var trashIDRe = regexp.MustCompile(`^tr_(\d{8}T\d{6}Z)_[0-9a-f]{4}$`)

// ValidTrashID reports whether id has the form of a trash entry id.
func ValidTrashID(id string) bool { return trashIDRe.MatchString(id) }

// TrashEntry is an entry of GET /trash.
type TrashEntry struct {
	ID        string    `json:"id"`
	TrashedAt time.Time `json:"trashedAt"`
	Reason    string    `json:"reason"`
	Label     string    `json:"label"`
	Files     []string  `json:"files"`
	SizeBytes int64     `json:"sizeBytes"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// entryFile is the content of entry.json.
type entryFile struct {
	Version   int       `json:"version"`
	State     string    `json:"state"`
	ID        string    `json:"id"`
	TrashedAt time.Time `json:"trashedAt"`
	Reason    string    `json:"reason"`
	Label     string    `json:"label"`
	// Files are the user files, relative to the SD card (shown by the app).
	Files []string `json:"files"`
	// Moves are the renamed items (files or folders), relative to the SD
	// card: what Recover puts back.
	Moves []string `json:"moves"`
	// Internal are the moved ReminisLink copies (locks, resume history),
	// relative to the SD card; not listed in Files.
	Internal []string `json:"internal,omitempty"`
	// ClockUntrusted: TrashedAt came from an untrusted clock (see
	// TimeGuard); never purged, dated again once the clock is trusted.
	ClockUntrusted bool  `json:"clockUntrusted,omitempty"`
	SizeBytes      int64 `json:"sizeBytes"`
}

func (e entryFile) public() TrashEntry {
	files := e.Files
	if files == nil {
		files = []string{}
	}
	return TrashEntry{
		ID: e.ID, TrashedAt: e.TrashedAt, Reason: e.Reason, Label: e.Label,
		Files: files, SizeBytes: e.SizeBytes, ExpiresAt: e.TrashedAt.Add(TrashRetention),
	}
}

// MoveItem is a file or folder to move to the trash.
type MoveItem struct {
	Path     string // absolute path, inside the SD card
	Internal bool   // a ReminisLink copy (lock, resume history), not a user file
}

// MoveResult is the outcome of Trash.Move.
type MoveResult struct {
	Entry TrashEntry
	// Moved are the user files moved, relative to the SD card.
	Moved []string
	// MovedBytes is the size of the user files moved.
	MovedBytes int64
}

// Trash moves files to the trash and erases trash entries.
type Trash struct {
	dir  string
	sd   string
	now  func() time.Time
	log  *slog.Logger
	rand io.Reader
	// guard protects the dates from a clock set back (nil: plain clock).
	guard *TimeGuard
	// rename is os.Rename; tests replace it to simulate failures.
	rename func(oldpath, newpath string) error

	mu sync.Mutex // serializes the changes of the trash
}

// NewTrash returns the trash of layout for the SD card mounted at sdRoot
// (now nil: time.Now, log nil: slog.Default()).
func NewTrash(layout Layout, sdRoot string, now func() time.Time, log *slog.Logger) *Trash {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &Trash{dir: layout.TrashDir(), sd: sdRoot, now: now, log: log, rand: rand.Reader, rename: os.Rename}
}

// UseTimeGuard dates the entries with g.Now() (never before the latest
// time observed) and makes Purge skip when g does not trust the clock.
// Call it before the trash is used.
func (t *Trash) UseTimeGuard(g *TimeGuard) { t.guard = g }

// clock returns the time used to date entries and whether it is trusted.
func (t *Trash) clock() (time.Time, bool) {
	if t.guard != nil {
		return t.guard.Now()
	}
	return t.now(), true
}

// Dir returns the trash folder.
func (t *Trash) Dir() string { return t.dir }

type plannedMove struct {
	abs, rel string
	internal bool
	files    []string // regular files (relative to the SD card)
	size     int64
}

func (t *Trash) plan(items []MoveItem) ([]plannedMove, error) {
	sd, err := filepath.Abs(t.sd)
	if err != nil {
		return nil, fmt.Errorf("trash: %w", err)
	}
	trashDir, err := filepath.Abs(t.dir)
	if err != nil {
		return nil, fmt.Errorf("trash: %w", err)
	}
	var out []plannedMove
	seen := map[string]bool{}
	for _, it := range items {
		abs, err := filepath.Abs(it.Path)
		if err != nil {
			return nil, fmt.Errorf("trash: %w", err)
		}
		rel, err := filepath.Rel(sd, abs)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("trash: %s is not inside the SD card", it.Path)
		}
		if isUnder(trashDir, abs) || isUnder(abs, trashDir) {
			return nil, fmt.Errorf("trash: %s is the trash", it.Path)
		}
		if seen[abs] {
			continue
		}
		fi, err := os.Lstat(abs)
		if err != nil {
			return nil, fmt.Errorf("trash: %w", err)
		}
		p := plannedMove{abs: abs, rel: filepath.ToSlash(rel), internal: it.Internal}
		switch {
		case fi.Mode().IsRegular():
			p.files, p.size = []string{p.rel}, fi.Size()
		case fi.IsDir():
			err := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !d.Type().IsRegular() {
					return nil
				}
				info, err := d.Info()
				if err != nil {
					return err
				}
				r, _ := filepath.Rel(sd, path)
				p.files = append(p.files, filepath.ToSlash(r))
				p.size += info.Size()
				return nil
			})
			if err != nil {
				return nil, fmt.Errorf("trash: %w", err)
			}
		default:
			return nil, fmt.Errorf("trash: %s is not a regular file nor a folder", it.Path)
		}
		seen[abs] = true
		out = append(out, p)
	}
	// Drop the items inside another moved folder: they move with it.
	kept := out[:0]
	for _, p := range out {
		nested := false
		for _, q := range out {
			if q.abs != p.abs && isUnder(q.abs, p.abs) {
				nested = true
				break
			}
		}
		if !nested {
			kept = append(kept, p)
		}
	}
	if len(kept) == 0 {
		return nil, ErrNothingToMove
	}
	return kept, nil
}

// isUnder reports whether p is root or inside it.
func isUnder(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

func (t *Trash) newID(at time.Time) (string, error) {
	var b [2]byte
	if _, err := io.ReadFull(t.rand, b[:]); err != nil {
		return "", err
	}
	return "tr_" + at.UTC().Format(trashIDTimeLayout) + "_" + hex.EncodeToString(b[:]), nil
}

// Move renames items into a new trash entry. It either moves all of them
// or none (the moved ones are put back on failure). reason is one of the
// Reason* constants; label is shown by the app (a game name).
func (t *Trash) Move(reason, label string, items []MoveItem) (MoveResult, error) {
	if !validReasons[reason] {
		return MoveResult{}, fmt.Errorf("trash: unknown reason %q", reason)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	planned, err := t.plan(items)
	if err != nil {
		return MoveResult{}, err
	}
	if err := os.MkdirAll(t.dir, 0o755); err != nil {
		return MoveResult{}, fmt.Errorf("trash: %w", err)
	}
	at, trusted := t.clock()
	var id string
	for attempt := 0; ; attempt++ {
		if id, err = t.newID(at); err != nil {
			return MoveResult{}, fmt.Errorf("trash: %w", err)
		}
		err = os.Mkdir(filepath.Join(t.dir, id), 0o755)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) || attempt >= 16 {
			return MoveResult{}, fmt.Errorf("trash: %w", err)
		}
	}
	entryDir := filepath.Join(t.dir, id)
	e := entryFile{
		Version: trashEntryVersion, State: stateMoving, ID: id,
		TrashedAt: at.UTC().Truncate(time.Second), Reason: reason, Label: label, ClockUntrusted: !trusted,
	}
	res := MoveResult{Moved: []string{}}
	for _, p := range planned {
		e.Moves = append(e.Moves, p.rel)
		e.SizeBytes += p.size
		if p.internal {
			e.Internal = append(e.Internal, p.rel)
			continue
		}
		e.Files = append(e.Files, p.files...)
		res.Moved = append(res.Moved, p.files...)
		res.MovedBytes += p.size
	}
	// The intent is on the card before the first rename.
	if err := writeEntry(entryDir, e); err != nil {
		os.RemoveAll(entryDir) // only holds the entry file
		return MoveResult{}, err
	}
	for i, p := range planned {
		dst := filepath.Join(entryDir, filepath.FromSlash(p.rel))
		err := os.MkdirAll(filepath.Dir(dst), 0o755)
		if err == nil {
			err = t.rename(p.abs, dst)
		}
		if err != nil {
			// Put back what was moved; the entry then only holds entry.json
			// unless a file cannot go back.
			left := t.putBack(entryDir, planned[:i])
			if len(left) == 0 {
				os.RemoveAll(entryDir)
			} else {
				recount(entryDir, &e, left)
				_ = writeEntry(entryDir, e)
				t.log.Error("trash: files left in the trash after a failed move", "entry", id, "files", left)
			}
			return MoveResult{}, fmt.Errorf("trash: move %s: %w", p.rel, withoutPathErr(err))
		}
		SyncDir(filepath.Dir(p.abs))
		SyncDir(filepath.Dir(dst))
	}
	e.State = stateDone
	if err := writeEntry(entryDir, e); err != nil {
		// The files are safe in the trash; Recover would put them back.
		return MoveResult{}, err
	}
	res.Entry = e.public()
	t.log.Info("moved to the trash", "entry", id, "reason", reason, "label", label,
		"backup", entryDir, "files", e.Files, "internal", e.Internal, "bytes", e.SizeBytes)
	return res, nil
}

// putBack moves the planned items of an entry back to their place and
// returns those that could not go back (relative paths).
func (t *Trash) putBack(entryDir string, moved []plannedMove) []string {
	var left []string
	for _, p := range moved {
		src := filepath.Join(entryDir, filepath.FromSlash(p.rel))
		if err := restoreItem(src, p.abs); err != nil {
			left = append(left, p.rel)
		}
	}
	return left
}

// restoreItem renames src back to dst when dst is free.
func restoreItem(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fs.ErrExist
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	SyncDir(filepath.Dir(dst))
	return nil
}

func writeEntry(dir string, e entryFile) error {
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return fmt.Errorf("trash: encode entry: %w", err)
	}
	if err := WriteFileAtomic(filepath.Join(dir, trashEntryFile), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	return nil
}

func readEntry(dir string) (entryFile, error) {
	var e entryFile
	data, err := os.ReadFile(filepath.Join(dir, trashEntryFile))
	if err != nil {
		return e, err
	}
	if err := json.Unmarshal(data, &e); err != nil {
		return e, err
	}
	if e.Version != trashEntryVersion {
		return e, fmt.Errorf("unsupported version %d", e.Version)
	}
	return e, nil
}

// entryDirs returns the names of the entry folders (valid ids only).
func (t *Trash) entryDirs() ([]string, error) {
	des, err := os.ReadDir(t.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("trash: %w", err)
	}
	var out []string
	for _, d := range des {
		if d.IsDir() && ValidTrashID(d.Name()) {
			out = append(out, d.Name())
		}
	}
	return out, nil
}

// List returns the entries, newest first, and their total size. Entries
// whose entry.json is unreadable or still "moving" are not listed (Recover
// handles them at the next start).
func (t *Trash) List() ([]TrashEntry, int64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	names, err := t.entryDirs()
	if err != nil {
		return nil, 0, err
	}
	out := []TrashEntry{}
	var total int64
	for _, name := range names {
		e, err := readEntry(filepath.Join(t.dir, name))
		if err != nil || e.State != stateDone || e.ID != name || !validReasons[e.Reason] {
			if err != nil {
				t.log.Warn("trash: unreadable entry", "entry", name, "error", err)
			}
			continue
		}
		out = append(out, e.public())
		total += e.SizeBytes
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].TrashedAt.Equal(out[j].TrashedAt) {
			return out[i].TrashedAt.After(out[j].TrashedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, total, nil
}

// Delete erases a trash entry for good and returns the bytes freed.
func (t *Trash) Delete(id string) (int64, error) {
	if !ValidTrashID(id) {
		return 0, ErrInvalidTrashID
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	fi, err := os.Lstat(filepath.Join(t.dir, id))
	if err != nil || !fi.IsDir() {
		return 0, ErrTrashNotFound
	}
	return t.erase(id, "deleted")
}

// Empty erases every trash entry for good and returns the bytes freed.
func (t *Trash) Empty() (int64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	names, err := t.entryDirs()
	if err != nil {
		return 0, err
	}
	var freed int64
	for _, name := range names {
		n, err := t.erase(name, "emptied")
		freed += n
		if err != nil {
			return freed, err
		}
	}
	t.finishDeletions()
	return freed, nil
}

// erase deletes an entry folder: renamed first, so it is never listed half
// deleted. Called with t.mu held.
func (t *Trash) erase(id, why string) (int64, error) {
	dir := filepath.Join(t.dir, id)
	// The size reported is the one of the entry (what GET /trash showed);
	// an entry without a readable entry.json counts its whole folder.
	var files []string
	var size int64
	if e, err := readEntry(dir); err == nil && e.State == stateDone {
		files, size = e.Files, e.SizeBytes
	} else {
		size = dirSize(dir)
	}
	doomed := filepath.Join(t.dir, trashDeletingPrefix+id)
	if err := os.Rename(dir, doomed); err != nil {
		return 0, fmt.Errorf("trash: %w", withoutPathErr(err))
	}
	SyncDir(t.dir)
	if err := os.RemoveAll(doomed); err != nil {
		return 0, fmt.Errorf("trash: %w", withoutPathErr(err))
	}
	SyncDir(t.dir)
	t.log.Info("trash entry erased permanently", "entry", id, "why", why, "files", files, "freedBytes", size)
	return size, nil
}

// finishDeletions removes the .deleting-* folders left by an interrupted
// deletion. Called with t.mu held.
func (t *Trash) finishDeletions() {
	des, err := os.ReadDir(t.dir)
	if err != nil {
		return
	}
	for _, d := range des {
		if strings.HasPrefix(d.Name(), trashDeletingPrefix) {
			if err := os.RemoveAll(filepath.Join(t.dir, d.Name())); err != nil {
				t.log.Warn("trash: cannot finish a deletion", "entry", d.Name(), "error", err)
			} else {
				t.log.Info("trash: finished an interrupted deletion", "entry", strings.TrimPrefix(d.Name(), trashDeletingPrefix))
			}
		}
	}
}

func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// Recover finishes what a power loss interrupted: entries still "moving"
// are rolled back (their files go back to their original place when it is
// free; the others stay in a "done" entry), empty entry folders are
// removed, interrupted deletions are finished.
func (t *Trash) Recover() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	names, err := t.entryDirs()
	if err != nil {
		return err
	}
	sd, err := filepath.Abs(t.sd)
	if err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	for _, name := range names {
		dir := filepath.Join(t.dir, name)
		e, err := readEntry(dir)
		if err != nil {
			if !hasRegularFile(dir) {
				// Created but nothing moved yet (entry.json never written).
				if err := os.RemoveAll(dir); err == nil {
					t.log.Info("trash: removed an empty entry", "entry", name)
				}
			}
			continue
		}
		if e.State == stateDone {
			continue
		}
		var left []string
		for _, rel := range e.Moves {
			src := filepath.Join(dir, filepath.FromSlash(rel))
			if _, err := os.Lstat(src); err != nil {
				continue // never moved
			}
			dst := filepath.Join(sd, filepath.FromSlash(rel))
			if !isUnder(sd, dst) {
				left = append(left, rel)
				continue
			}
			if err := restoreItem(src, dst); err != nil {
				left = append(left, rel)
				continue
			}
			t.log.Info("trash: put back after an interrupted move", "entry", name, "file", rel)
		}
		if len(left) == 0 {
			if err := os.RemoveAll(dir); err != nil {
				t.log.Warn("trash: cannot remove a rolled back entry", "entry", name, "error", err)
			}
			continue
		}
		recount(dir, &e, left)
		if err := writeEntry(dir, e); err != nil {
			return err
		}
		t.log.Warn("trash: kept files of an interrupted move (their place is taken)", "entry", name, "files", e.Files)
	}
	t.finishDeletions()
	return nil
}

// recount makes e describe what is really left in its folder: the moves
// in left (relative paths), their files and size. Internal copies stay
// marked internal. The entry becomes "done".
func recount(dir string, e *entryFile, left []string) {
	internal := map[string]bool{}
	for _, rel := range e.Internal {
		internal[rel] = true
	}
	e.Moves, e.State = left, stateDone
	e.Files, e.Internal, e.SizeBytes = nil, nil, 0
	for _, rel := range left {
		if internal[rel] {
			e.Internal = append(e.Internal, rel)
		}
		_ = filepath.WalkDir(filepath.Join(dir, filepath.FromSlash(rel)), func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				if info, err := d.Info(); err == nil {
					e.SizeBytes += info.Size()
				}
				if !internal[rel] {
					r, _ := filepath.Rel(dir, p)
					e.Files = append(e.Files, filepath.ToSlash(r))
				}
			}
			return nil
		})
	}
}

func hasRegularFile(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() && d.Name() != trashEntryFile && d.Name() != trashEntryFile+".tmp" {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// Purge erases the entries older than maxAge (TrashRetention at launch)
// and returns the bytes freed. The date is the one of entry.json; an entry
// whose entry.json is unreadable is kept.
func (t *Trash) Purge(maxAge time.Duration) (int64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	names, err := t.entryDirs()
	if err != nil {
		return 0, err
	}
	now, trusted := t.clock()
	if !trusted {
		t.log.Warn("trash: purge skipped, the clock is not trusted")
		return 0, nil
	}
	t.redateUntrusted(names, now)
	limit := now.Add(-maxAge)
	var freed int64
	for _, name := range names {
		at, ok := t.entryTime(name)
		if !ok || !at.Before(limit) {
			continue
		}
		n, err := t.erase(name, "expired")
		freed += n
		if err != nil {
			return freed, err
		}
	}
	t.finishDeletions()
	return freed, nil
}

// redateUntrusted dates the entries made with an untrusted clock at now,
// the first trusted time. Called with t.mu held.
func (t *Trash) redateUntrusted(names []string, now time.Time) {
	for _, name := range names {
		dir := filepath.Join(t.dir, name)
		e, err := readEntry(dir)
		if err != nil || !e.ClockUntrusted || e.State != stateDone {
			continue
		}
		e.TrashedAt, e.ClockUntrusted = now.UTC().Truncate(time.Second), false
		if err := writeEntry(dir, e); err != nil {
			t.log.Warn("trash: cannot date an entry again", "entry", name, "error", err)
			continue
		}
		t.log.Info("trash: entry dated again with a trusted clock", "entry", name, "trashedAt", e.TrashedAt)
	}
}

// entryTime returns the date used by the purge; false when the entry must
// not be purged (dated by an untrusted clock).
func (t *Trash) entryTime(name string) (time.Time, bool) {
	e, err := readEntry(filepath.Join(t.dir, name))
	if err != nil || e.TrashedAt.IsZero() {
		// Unreadable, or written by a future version: never purged on the
		// sole date of its id. "Empty the trash" still erases it.
		t.log.Warn("trash: entry kept by the purge, entry.json unreadable", "entry", name, "error", err)
		return time.Time{}, false
	}
	return e.TrashedAt, !e.ClockUntrusted
}

// withoutPathErr drops the path of a *fs.PathError or *os.LinkError.
func withoutPathErr(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Err
	}
	return err
}
