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
//
// Replacements (Replace) add a third state, "installing": the old files
// are in the entry and the new ones are being renamed into their place.
// Recover finishes such an entry when the new files are complete, or puts
// everything back. Until it is "done", an entry is never listed, deleted,
// emptied nor purged: the only copy of a replaced file stays safe.

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
	// ReasonDeleteBackup: a save backup or a resume history entry deleted
	// by the app or pushed out by the automatic limits.
	ReasonDeleteBackup = "delete-backup"
	// ReasonDeletePak and ReasonReplacePak (M12): a pak folder deleted
	// or replaced by an installation.
	ReasonDeletePak  = "delete-pak"
	ReasonReplacePak = "replace-pak"
	// ReasonImportArchive (M19): the files of a game replaced by the
	// import of a game archive.
	ReasonImportArchive = "import-archive"
)

var validReasons = map[string]bool{
	ReasonDeleteGame: true, ReasonDeleteState: true, ReasonOverwrite: true,
	ReasonReplaceROM: true, ReasonLockRestore: true, ReasonRestore: true, ReasonDeleteBackup: true,
	ReasonDeletePak: true, ReasonReplacePak: true, ReasonImportArchive: true,
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
	stateInstalling     = "installing"
	// stateRestoring: a failed replacement whose new files are back at
	// their temporary place; the old files are going back (Recover ends it).
	stateRestoring    = "restoring"
	stateDone         = "done"
	trashIDTimeLayout = "20060102T150405Z"
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
	// Credit is the credited-age clock (seconds, see CreditClock) when the
	// entry was made; an entry without it is never purged automatically.
	Credit    *int64 `json:"credit,omitempty"`
	SizeBytes int64  `json:"sizeBytes"`
	// Installs are the new files of a replacement (Replace), relative to
	// the SD card: renamed into place while the state is "installing".
	Installs []installRecord `json:"installs,omitempty"`
}

// installRecord is an Install in entry.json, paths relative to the SD card.
type installRecord struct {
	Src    string `json:"src"`
	Dst    string `json:"dst"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
	Dir    bool   `json:"dir,omitempty"`
}

// public returns the API form of an entry. expiresAt is the earliest time
// it can be purged: now + (TrashRetention - credited age), never in the
// past. Without a credit (or a credit clock) the entry is never purged
// automatically and expiresAt is now + TrashRetention.
func (e entryFile) public(now time.Time, credit *time.Duration) TrashEntry {
	files := e.Files
	if files == nil {
		files = []string{}
	}
	left := TrashRetention
	if e.Credit != nil && credit != nil {
		age := max(*credit-time.Duration(*e.Credit)*time.Second, 0)
		left = max(TrashRetention-age, 0)
	}
	return TrashEntry{
		ID: e.ID, TrashedAt: e.TrashedAt, Reason: e.Reason, Label: e.Label,
		Files: files, SizeBytes: e.SizeBytes, ExpiresAt: now.Add(left).UTC().Truncate(time.Second),
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
	// credit ages the entries (nil: no automatic purge).
	credit *CreditClock
	// rename is os.Rename, syncDir is SyncDirChecked and saveEntry is
	// writeEntry; tests replace them to simulate failures.
	rename    func(oldpath, newpath string) error
	syncDir   func(dir string) error
	saveEntry func(dir string, e entryFile) error

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
	t := &Trash{dir: layout.TrashDir(), sd: sdRoot, now: now, log: log, rand: rand.Reader,
		syncDir: SyncDirChecked}
	// Every rename of the trash stays inside the SD card, through no link
	// (M12 review).
	t.rename = t.renameInSD
	// The journal is durable only once its folder is synced (and checked).
	t.saveEntry = func(dir string, e entryFile) error {
		return writeEntryWith(dir, e, func(d string) error { return t.syncDir(d) })
	}
	return t
}

// SetRename replaces os.Rename for the moves and installs of the trash:
// the tests of other packages simulate failures with it.
func (t *Trash) SetRename(rename func(oldpath, newpath string) error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rename = rename
}

// SetSyncDir replaces SyncDirChecked for the trash (tests of other
// packages simulate sync failures with it).
func (t *Trash) SetSyncDir(sync func(dir string) error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.syncDir = sync
}

// UseCreditClock ages the entries with c (docs/specs/05-console-pak.md
// §4). Call it before the trash is used; without it nothing is purged.
func (t *Trash) UseCreditClock(c *CreditClock) { t.credit = c }

// creditNow returns the current credit, or nil without a credit clock.
func (t *Trash) creditNow() *time.Duration {
	if t.credit == nil {
		return nil
	}
	c := t.credit.Now()
	return &c
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
	res, _, _, err := t.moveLocked(reason, label, items, nil)
	return res, err
}

// moveLocked moves items into a new entry (Move, Replace). With installs,
// the entry is left "installing" (the caller renames the new files, then
// marks it done); otherwise it is "done". On error nothing is moved: the
// moved items are put back, or, if that fails, left in an entry that
// Recover rolls back or keeps (never purged before). Called with t.mu held.
func (t *Trash) moveLocked(reason, label string, items []MoveItem, installs []installRecord) (MoveResult, string, entryFile, error) {
	planned, err := t.plan(items)
	if errors.Is(err, ErrNothingToMove) && len(installs) > 0 {
		// A journal of installs only (M19): nothing is moved.
		planned, err = nil, nil
	}
	if err != nil {
		return MoveResult{}, "", entryFile{}, err
	}
	if err := t.mkdirInSD(t.dir); err != nil {
		return MoveResult{}, "", entryFile{}, fmt.Errorf("trash: %w", err)
	}
	at := t.now()
	var id string
	for attempt := 0; ; attempt++ {
		if id, err = t.newID(at); err != nil {
			return MoveResult{}, "", entryFile{}, fmt.Errorf("trash: %w", err)
		}
		err = os.Mkdir(filepath.Join(t.dir, id), 0o755)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) || attempt >= 16 {
			return MoveResult{}, "", entryFile{}, fmt.Errorf("trash: %w", err)
		}
	}
	entryDir := filepath.Join(t.dir, id)
	credit := t.creditNow()
	e := entryFile{
		Version: trashEntryVersion, State: stateMoving, ID: id,
		TrashedAt: at.UTC().Truncate(time.Second), Reason: reason, Label: label,
		Installs: installs,
	}
	if credit != nil {
		seconds := int64(*credit / time.Second)
		e.Credit = &seconds
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
	// The intent is on the card before the first rename, and so is the
	// path to it (the entry folder in the trash folder).
	err = t.saveEntry(entryDir, e)
	if err == nil {
		err = t.syncChain(t.dir, entryDir)
	}
	if err != nil {
		os.RemoveAll(entryDir) // only holds the entry file
		return MoveResult{}, "", entryFile{}, err
	}
	fail := func(moved []plannedMove, cause error) (MoveResult, string, entryFile, error) {
		// Put back what was moved; the entry then only holds entry.json
		// unless a file cannot go back (its place is taken): the entry
		// then keeps it, "done". If even that cannot be written, the entry
		// stays "moving" and Recover handles it at the next start.
		left := t.putBack(entryDir, moved)
		if len(left) == 0 {
			os.RemoveAll(entryDir)
		} else {
			recount(entryDir, &e, left)
			e.Installs = nil
			_ = t.saveEntry(entryDir, e)
			t.log.Error("trash: files left in the trash after a failed move", "entry", id, "files", left)
		}
		return MoveResult{}, "", entryFile{}, cause
	}
	for i, p := range planned {
		dst := filepath.Join(entryDir, filepath.FromSlash(p.rel))
		err := t.mkdirInSD(filepath.Dir(dst))
		if err == nil {
			// Every folder from the entry down to the destination is
			// durable before the original leaves its place.
			err = t.syncChain(entryDir, filepath.Dir(dst))
		}
		if err == nil {
			err = t.rename(p.abs, dst)
			if err == nil {
				if err = t.syncDir(filepath.Dir(dst)); err == nil {
					err = t.syncDir(filepath.Dir(p.abs))
				}
				if err != nil {
					i++ // this one moved too
				}
			}
		}
		if err != nil {
			return fail(planned[:i], fmt.Errorf("trash: move %s: %w", p.rel, withoutPathErr(err)))
		}
	}
	e.State = stateDone
	if len(installs) > 0 {
		e.State = stateInstalling
	}
	if err := t.saveEntry(entryDir, e); err != nil {
		// Without its final state the entry would stay "moving": put the
		// files back now rather than leave the game missing until the
		// next start.
		return fail(planned, err)
	}
	res.Entry = e.public(t.now(), credit)
	t.log.Info("moved to the trash", "entry", id, "reason", reason, "label", label,
		"backup", entryDir, "files", e.Files, "internal", e.Internal, "bytes", e.SizeBytes)
	return res, entryDir, e, nil
}

// syncChain syncs dir and every folder between it and top (included,
// top being an ancestor of dir or dir itself), from the deepest up, so
// that a path created in top is durable. Errors are returned (see
// SyncDirChecked).
func (t *Trash) syncChain(top, dir string) error {
	for {
		if err := t.syncDir(dir); err != nil {
			return err
		}
		if dir == top || !isUnder(top, dir) {
			return t.syncDir(filepath.Dir(top))
		}
		dir = filepath.Dir(dir)
	}
}

// FindMove returns the most recent done entry of reason that moved the
// item rel (relative to the SD card, "/" separator).
func (t *Trash) FindMove(reason, rel string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	names, err := t.entryDirs()
	if err != nil {
		return "", false
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names))) // ids start with their time
	for _, name := range names {
		e, err := readEntry(filepath.Join(t.dir, name))
		if err != nil || e.State != stateDone || e.Reason != reason || e.ID != name {
			continue
		}
		for _, m := range e.Moves {
			if m == rel {
				return name, true
			}
		}
	}
	return "", false
}

// ErrNotPutBack means some files of an entry could not go back (their
// place is taken): they stay in the entry.
var ErrNotPutBack = errors.New("trash: files left in the entry, their place is taken")

// Undo puts every item of an entry back to its original place, as if Move
// had not happened (an operation that failed after moving its files):
// the entry is removed when it is empty, otherwise it keeps what could not
// go back and ErrNotPutBack is returned.
func (t *Trash) Undo(id string) error {
	if !ValidTrashID(id) {
		return ErrInvalidTrashID
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	dir := filepath.Join(t.dir, id)
	e, err := readEntry(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrTrashNotFound
	}
	if err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	sd, err := filepath.Abs(t.sd)
	if err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	var left []string
	for _, rel := range e.Moves {
		src := filepath.Join(dir, filepath.FromSlash(rel))
		dst := filepath.Join(sd, filepath.FromSlash(rel))
		if _, err := os.Lstat(src); err != nil {
			continue // already back
		}
		if !isUnder(sd, dst) || t.restoreItem(src, dst) != nil {
			left = append(left, rel)
			continue
		}
		t.log.Info("trash: put back", "entry", id, "file", rel)
	}
	if len(left) > 0 {
		recount(dir, &e, left)
		if err := writeEntry(dir, e); err != nil {
			return err
		}
		return ErrNotPutBack
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("trash: remove the undone entry: %w", err)
	}
	return nil
}

// putBack moves the planned items of an entry back to their place and
// returns those that could not go back (relative paths).
func (t *Trash) putBack(entryDir string, moved []plannedMove) []string {
	var left []string
	for _, p := range moved {
		src := filepath.Join(entryDir, filepath.FromSlash(p.rel))
		if err := t.restoreItem(src, p.abs); err != nil {
			left = append(left, p.rel)
		}
	}
	return left
}

// restoreItem renames src back to dst when dst is free, and syncs (and
// checks) both folders: until it returns nil, the move back may not be on
// the card, so the caller keeps the journal (the trash entry).
func (t *Trash) restoreItem(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fs.ErrExist
	}
	if err := t.mkdirInSD(filepath.Dir(dst)); err != nil {
		return err
	}
	if err := t.rename(src, dst); err != nil {
		return err
	}
	if err := t.syncDir(filepath.Dir(dst)); err != nil {
		return err
	}
	return t.syncDir(filepath.Dir(src))
}

func writeEntry(dir string, e entryFile) error { return writeEntryWith(dir, e, SyncDirChecked) }

// writeEntryWith writes entry.json atomically and syncs its folder with
// syncDir, returning its error.
func writeEntryWith(dir string, e entryFile, syncDir func(string) error) error {
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return fmt.Errorf("trash: encode entry: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(dir, trashEntryFile), append(data, '\n'), 0o644, syncDir); err != nil {
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
	if err := t.checkTrashDir(); err != nil {
		return nil, err
	}
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
	now, credit := t.now(), t.creditNow()
	for _, name := range names {
		e, err := readEntry(filepath.Join(t.dir, name))
		if err != nil || e.State != stateDone || e.ID != name || !validReasons[e.Reason] {
			if err != nil {
				t.log.Warn("trash: unreadable entry", "entry", name, "error", err)
			}
			continue
		}
		out = append(out, e.public(now, credit))
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
	if err := t.checkTrashDir(); err != nil {
		return 0, err
	}
	fi, err := os.Lstat(filepath.Join(t.dir, id))
	if err != nil || !fi.IsDir() || t.pending(id) {
		// A replacement in progress is not an entry yet (never listed).
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
		if t.pending(name) {
			continue // a replacement in progress keeps its only copy
		}
		n, err := t.erase(name, "emptied")
		freed += n
		if err != nil {
			return freed, err
		}
	}
	t.finishDeletions()
	return freed, nil
}

// pending reports whether an entry is a move or a replacement in progress
// (state "moving" or "installing"). Called with t.mu held.
func (t *Trash) pending(id string) bool {
	e, err := readEntry(filepath.Join(t.dir, id))
	return err == nil && e.State != stateDone
}

// erase deletes an entry folder: renamed first, so it is never listed half
// deleted. Called with t.mu held.
func (t *Trash) erase(id, why string) (int64, error) {
	if err := t.checkTrashDir(); err != nil {
		return 0, err
	}
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
	if err := t.checkTrashDir(); err != nil {
		// Never follow a link at the trash folder (its journals would
		// be read and rewritten elsewhere): left as it is, logged.
		t.log.Error("trash: recovery skipped, the trash folder is reached through a link", "error", err)
		return nil
	}
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
			if len(e.Moves) == 0 && len(e.Installs) > 0 {
				// The journal of an installation that replaced nothing
				// (M19), left by a stop before its removal.
				t.removeInstallJournal(dir, e)
			}
			continue
		}
		if e.State == stateInstalling {
			if err := t.recoverInstall(dir, sd, &e); err != nil {
				return err
			}
			continue
		}
		if e.State == stateRestoring {
			// The "restoring" journal may have been renamed in place
			// without its folder being synced (the sync failed): make it
			// durable before the first file goes back (Codex review of M8).
			if err := t.syncDir(dir); err != nil {
				t.log.Error("trash: roll back kept for the next start, its journal is not durable", "entry", name, "error", err)
				continue
			}
			if t.rollback(dir, sd, &e) {
				t.log.Info("trash: interrupted roll back finished", "entry", name)
			} else {
				t.log.Error("trash: roll back kept for the next start, its files could not go back", "entry", name)
			}
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
			if err := t.restoreItem(src, dst); err != nil {
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

// Purge erases the entries whose credited age reached maxAge
// (TrashRetention at launch) and returns the bytes freed. An entry without
// a credit (unreadable entry.json, older version) is kept, and nothing is
// purged without a credit clock.
func (t *Trash) Purge(maxAge time.Duration) (int64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.credit == nil {
		return 0, nil
	}
	names, err := t.entryDirs()
	if err != nil {
		return 0, err
	}
	now := t.credit.Now()
	var freed int64
	for _, name := range names {
		e, err := readEntry(filepath.Join(t.dir, name))
		if err != nil || e.Credit == nil {
			t.log.Warn("trash: entry kept by the purge, no credited age", "entry", name, "error", err)
			continue
		}
		if e.State != stateDone {
			continue // a replacement in progress keeps its only copy
		}
		if now-time.Duration(*e.Credit)*time.Second < maxAge {
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
