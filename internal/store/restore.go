package store

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Restore from the trash (M18, protocol/openapi.yaml
// POST /trash/{entryId}/restore): every item of an entry goes back to its
// original place, renamed on the same SD card. A present file is never
// overwritten: a taken place (same name ignoring case, exFAT) is a
// conflict, answered as such or avoided by a free name for every item.
//
// Power loss: entry.json is rewritten with state "returning" and the
// place of every item before the first rename; Recover finishes such an
// entry at the next start (each item goes to its place when it is still
// free, the rest stays in a "done" entry). An entry "returning" is never
// listed, deleted, emptied nor purged (pending). Every restore holds t.mu,
// like Purge, Delete and Empty: none of them runs during a restore.

// Conflict rules of a restore.
const (
	RestoreFail   = "fail"
	RestoreRename = "rename"
)

// stateReturning: the items of the entry are going back to the places in
// Returns (Restore); Recover finishes it.
const stateReturning = "returning"

// maxFreeName bounds the " (N)" tried for a free name.
const maxFreeName = 999

// returnRecord is an item of a restore in entry.json, paths relative to
// the SD card: Move (as in Moves) goes back to Target.
type returnRecord struct {
	Move   string `json:"move"`
	Target string `json:"target"`
}

// RestoreConflictError lists the taken places (relative to the SD card,
// "ReminisLink/..." for the copies of ReminisLink) of a restore refused
// with RestoreFail, or whose free names all are taken.
type RestoreConflictError struct{ Conflicts []string }

func (e *RestoreConflictError) Error() string {
	return "trash: restore conflicts with " + strings.Join(e.Conflicts, ", ")
}

// RestoreItem is a moved item of an entry and its place.
type RestoreItem struct {
	Src      string // absolute, in the entry
	Dst      string // absolute, where it goes (free name when Renamed)
	Rel      string // original place, relative to the SD card ("/")
	Target   string // where it goes, relative to the SD card ("/")
	Dir      bool
	Internal bool // a ReminisLink copy (lock, resume history)
	Conflict bool // its original place is taken
}

// RestoreFile is a user file of TrashRestorePreview.
type RestoreFile struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Conflict bool   `json:"conflict"`
	FreeName string `json:"freeName"`
	// target is where it goes without a free name (the folders on the
	// way as named on the card).
	target string
}

// RestorePlan is what a restore puts back (GET /trash/{entryId}/restore
// is its JSON form).
type RestorePlan struct {
	EntryID  string        `json:"entryId"`
	Reason   string        `json:"reason"`
	Label    string        `json:"label"`
	Conflict bool          `json:"conflict"`
	Files    []RestoreFile `json:"items"`
	Bytes    int64         `json:"restoredBytes"`
	// Items go to their original place, or, when Renamed, to a free name.
	Items   []RestoreItem `json:"-"`
	Renamed bool          `json:"-"`
	// Conflicts are the taken places (see RestoreConflictError).
	Conflicts []string `json:"-"`
	// free are the Targets of the items under a free name (nil when the
	// entry has no conflict or no free name was found).
	free []string
}

// RestoreResult is the answer of POST /trash/{entryId}/restore.
type RestoreResult struct {
	Restored      []string `json:"restored"`
	RestoredBytes int64    `json:"restoredBytes"`
	Renamed       bool     `json:"renamed"`
	// Plan is what was restored (Items at their final place).
	Plan RestorePlan `json:"-"`
}

// Entry returns a listed (done) entry.
func (t *Trash) Entry(id string) (TrashEntry, error) {
	if !ValidTrashID(id) {
		return TrashEntry{}, ErrInvalidTrashID
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	e, err := t.doneEntry(id)
	if err != nil {
		return TrashEntry{}, err
	}
	return e.public(t.now(), t.creditNow()), nil
}

// doneEntry reads a listed entry. Called with t.mu held.
func (t *Trash) doneEntry(id string) (entryFile, error) {
	if err := t.checkTrashDir(); err != nil {
		return entryFile{}, err
	}
	dir := filepath.Join(t.dir, id)
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		return entryFile{}, ErrTrashNotFound
	}
	e, err := readEntry(dir)
	if err != nil || e.State != stateDone || e.ID != id || !validReasons[e.Reason] {
		return entryFile{}, ErrTrashNotFound
	}
	return e, nil
}

// PlanRestore returns what Restore would put back (the preview): every
// user file, whether its place is taken and its free name.
func (t *Trash) PlanRestore(id string) (RestorePlan, error) {
	if !ValidTrashID(id) {
		return RestorePlan{}, ErrInvalidTrashID
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.planRestore(id)
}

// planRestore computes the plan of an entry: the items at their original
// place, and their free names when a place is taken. Called with t.mu held.
func (t *Trash) planRestore(id string) (RestorePlan, error) {
	e, err := t.doneEntry(id)
	if err != nil {
		return RestorePlan{}, err
	}
	sd, err := filepath.Abs(t.sd)
	if err != nil {
		return RestorePlan{}, fmt.Errorf("trash: %w", err)
	}
	entryDir := filepath.Join(t.dir, id)
	internal := map[string]bool{}
	for _, rel := range e.Internal {
		internal[rel] = true
	}
	p := RestorePlan{EntryID: e.ID, Reason: e.Reason, Label: e.Label, Files: []RestoreFile{}}
	for _, rel := range e.Moves {
		if !filepath.IsLocal(filepath.FromSlash(rel)) {
			return RestorePlan{}, fmt.Errorf("trash: entry %s has a move outside the SD card", id)
		}
		src := filepath.Join(entryDir, filepath.FromSlash(rel))
		fi, err := os.Lstat(src)
		if err != nil {
			t.log.Warn("trash: an item of the entry is missing", "entry", id, "file", rel)
			continue
		}
		if !fi.IsDir() && !fi.Mode().IsRegular() {
			// Never a link nor a special file (rule 2 of CLAUDE.md).
			return RestorePlan{}, fmt.Errorf("trash: entry %s holds an item that is not a file nor a folder", id)
		}
		target, taken := resolveFold(sd, rel)
		it := RestoreItem{Src: src, Rel: rel, Target: target, Dir: fi.IsDir(), Internal: internal[rel], Conflict: taken}
		if taken {
			p.Conflict = true
			p.Conflicts = append(p.Conflicts, t.displayName(sd, rel, it.Internal))
		}
		p.Items = append(p.Items, it)
	}
	if len(p.Items) == 0 {
		return RestorePlan{}, ErrNothingToMove
	}
	if p.Conflict {
		p.free = t.freeNames(sd, p.Items)
	}
	for i, it := range p.Items {
		freeTarget := it.Target
		if p.free != nil {
			freeTarget = p.free[i]
		}
		if it.Internal {
			continue
		}
		_ = filepath.WalkDir(it.Src, func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			inner, _ := filepath.Rel(it.Src, path)
			name, free, target := it.Rel, freeTarget, it.Target
			if inner != "." {
				name += "/" + filepath.ToSlash(inner)
				free += "/" + filepath.ToSlash(inner)
				target += "/" + filepath.ToSlash(inner)
			}
			if p.free == nil {
				free = name // no free name needed (or none found)
			}
			p.Files = append(p.Files, RestoreFile{Name: name, Size: info.Size(), Conflict: it.Conflict, FreeName: free, target: target})
			p.Bytes += info.Size()
			return nil
		})
	}
	for i := range p.Items {
		p.Items[i].Dst = filepath.Join(sd, filepath.FromSlash(p.Items[i].Target))
	}
	return p, nil
}

// displayName is rel as the app shows it: relative to the SD card, or
// "ReminisLink/..." for a copy of ReminisLink.
func (t *Trash) displayName(sd, rel string, internal bool) string {
	if !internal {
		return rel
	}
	data := filepath.Dir(t.dir)
	if r, err := filepath.Rel(data, filepath.Join(sd, filepath.FromSlash(rel))); err == nil && filepath.IsLocal(r) {
		return "ReminisLink/" + filepath.ToSlash(r)
	}
	return rel
}

// resolveFold resolves rel (relative to sd, "/" separator) the way exFAT
// does, ignoring case: each existing component takes its name on the card
// (an exact match wins). taken reports that something (a file, a folder,
// anything else) already has the name of the last component, or that a
// component on the way is not a folder (a file or a link: the item cannot
// go there).
func resolveFold(sd, rel string) (target string, taken bool) {
	parts := strings.Split(rel, "/")
	dir := sd
	out := make([]string, 0, len(parts))
	for i, c := range parts {
		match, mode, found := findAny(dir, c)
		if !found {
			out = append(out, parts[i:]...)
			return strings.Join(out, "/"), false
		}
		if i == len(parts)-1 {
			// The item keeps its own name (a free name is built on it).
			return strings.Join(append(out, c), "/"), true
		}
		out = append(out, match)
		if !mode.IsDir() {
			out = append(out, parts[i+1:]...)
			return strings.Join(out, "/"), true
		}
		dir = filepath.Join(dir, match)
	}
	return strings.Join(out, "/"), false
}

// findAny returns the entry of dir named name ignoring case (any type, an
// exact match wins) and its type.
func findAny(dir, name string) (string, fs.FileMode, bool) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return "", 0, false
	}
	match, mode, found := "", fs.FileMode(0), false
	for _, d := range des {
		if d.Name() == name {
			return name, d.Type(), true
		}
		if !found && strings.EqualFold(d.Name(), name) {
			match, mode, found = d.Name(), d.Type(), true
		}
	}
	return match, mode, found
}

// freeNames returns the targets of items under the first free name
// " (N)" (N from 2), the same N for every item so the files of a game
// stay together, or nil when none is free up to maxFreeName.
func (t *Trash) freeNames(sd string, items []RestoreItem) []string {
	stems := nameStems(items)
	for n := 2; n <= maxFreeName; n++ {
		out := make([]string, len(items))
		seen := map[string]bool{}
		ok := true
		for i, it := range items {
			parent, base := splitRel(it.Target)
			name := withNumber(base, stems, n)
			target := name
			if parent != "" {
				target = parent + "/" + name
			}
			key := strings.ToLower(target)
			if seen[key] || len(name) > 255 {
				ok = false
				break
			}
			seen[key] = true
			if _, taken := resolveFold(sd, target); taken {
				ok = false
				break
			}
			out[i] = target
		}
		if ok {
			return out
		}
	}
	return nil
}

// splitRel splits a "/" path into its folder ("" at the top) and name.
func splitRel(rel string) (string, string) {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[:i], rel[i+1:]
	}
	return "", rel
}

// nameStems returns the names of the items without their last extension
// (the game name of a ROM, "Game.gba" for its save "Game.gba.sav").
func nameStems(items []RestoreItem) []string {
	var stems []string
	for _, it := range items {
		_, base := splitRel(it.Rel)
		stem := base
		if !it.Dir || strings.Contains(base, ".") {
			stem = strings.TrimSuffix(base, filepath.Ext(base))
		}
		if stem != "" {
			stems = append(stems, stem)
		}
	}
	return stems
}

// withNumber inserts " (n)" after the shortest stem that starts base (the
// game name: "Game (2).gba", "Game (2).gba.sav", "Clock (2).pak"), or,
// without one, before the last extension of base.
func withNumber(base string, stems []string, n int) string {
	suffix := " (" + strconv.Itoa(n) + ")"
	best := ""
	for _, s := range stems {
		if (base == s || strings.HasPrefix(base, s+".")) && (best == "" || len(s) < len(best)) {
			best = s
		}
	}
	if best == "" {
		best = strings.TrimSuffix(base, filepath.Ext(base))
		if best == "" {
			return base + suffix
		}
	}
	return best + suffix + base[len(best):]
}

// Restore puts every item of the entry id back (RestoreFail: refused with
// a *RestoreConflictError when a place is taken; RestoreRename: every item
// under the same free name then). check, when not nil, runs on the final
// plan before anything moves (the pak checks of internal/transfer); its
// error is returned as it is. All or nothing: on an error, the items
// already restored go back to the entry.
func (t *Trash) Restore(id, onConflict string, check func(RestorePlan) error) (RestoreResult, error) {
	if !ValidTrashID(id) {
		return RestoreResult{}, ErrInvalidTrashID
	}
	if onConflict != RestoreFail && onConflict != RestoreRename {
		return RestoreResult{}, fmt.Errorf("trash: unknown conflict rule %q", onConflict)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	p, err := t.planRestore(id)
	if err != nil {
		return RestoreResult{}, err
	}
	if p.Conflict {
		if onConflict == RestoreFail || p.free == nil {
			return RestoreResult{}, &RestoreConflictError{Conflicts: p.Conflicts}
		}
		sd, err := filepath.Abs(t.sd)
		if err != nil {
			return RestoreResult{}, fmt.Errorf("trash: %w", err)
		}
		for i := range p.Items {
			p.Items[i].Target = p.free[i]
			p.Items[i].Dst = filepath.Join(sd, filepath.FromSlash(p.free[i]))
		}
		p.Renamed = true
	}
	if check != nil {
		if err := check(p); err != nil {
			return RestoreResult{}, err
		}
	}
	if err := t.returnItems(p); err != nil {
		return RestoreResult{}, err
	}
	res := RestoreResult{Restored: []string{}, RestoredBytes: p.Bytes, Renamed: p.Renamed, Plan: p}
	for _, f := range p.Files {
		name := f.target
		if p.Renamed {
			name = f.FreeName
		}
		res.Restored = append(res.Restored, name)
	}
	t.log.Info("restored from the trash", "entry", id, "reason", p.Reason, "label", p.Label,
		"renamed", p.Renamed, "files", res.Restored, "bytes", res.RestoredBytes)
	return res, nil
}

// returnItems journals the restore, renames every item to its place and
// removes the entry. Called with t.mu held.
func (t *Trash) returnItems(p RestorePlan) error {
	entryDir := filepath.Join(t.dir, p.EntryID)
	e, err := readEntry(entryDir)
	if err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	sd, err := filepath.Abs(t.sd)
	if err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	e.State = stateReturning
	e.Returns = nil
	for _, it := range p.Items {
		e.Returns = append(e.Returns, returnRecord{Move: it.Rel, Target: it.Target})
	}
	// The intent is on the card before the first rename.
	if err := t.saveEntry(entryDir, e); err != nil {
		return err
	}
	for i, it := range p.Items {
		if err := t.returnOne(sd, it.Src, it.Dst, it.Target); err != nil {
			t.undoReturn(entryDir, &e, p.Items[:i+1])
			return fmt.Errorf("trash: restore %s: %w", it.Rel, withoutPathErr(err))
		}
	}
	// Everything is back: the entry only holds entry.json and folders.
	doomed := filepath.Join(t.dir, trashDeletingPrefix+p.EntryID)
	if err := os.Rename(entryDir, doomed); err != nil {
		// Still "returning" with nothing left: Recover removes it.
		t.log.Warn("trash: restored entry left for the next start", "entry", p.EntryID, "error", err)
		return nil
	}
	SyncDir(t.dir)
	if err := os.RemoveAll(doomed); err != nil {
		t.log.Warn("trash: cannot remove a restored entry", "entry", p.EntryID, "error", err)
	}
	SyncDir(t.dir)
	return nil
}

// returnOne renames src (in the entry) to dst when its place is still
// free, ignoring case, and syncs every folder created on the way and both
// folders of the rename.
func (t *Trash) returnOne(sd, src, dst, target string) error {
	if _, taken := resolveFold(sd, target); taken {
		return fs.ErrExist
	}
	parent := filepath.Dir(dst)
	top := parent
	for {
		if _, err := os.Lstat(top); err == nil || top == sd || !isUnder(sd, top) {
			break
		}
		top = filepath.Dir(top)
	}
	if err := t.mkdirInSD(parent); err != nil {
		return err
	}
	if err := t.syncChain(top, parent); err != nil {
		return err
	}
	if err := t.rename(src, dst); err != nil {
		return err
	}
	if err := t.syncDir(parent); err != nil {
		return err
	}
	return t.syncDir(filepath.Dir(src))
}

// undoReturn moves the items already restored (the last one too when its
// rename happened) back into the entry, which becomes "done" again; when
// one cannot go back, the entry stays "returning" and the next start
// finishes the restore. Called with t.mu held.
func (t *Trash) undoReturn(entryDir string, e *entryFile, done []RestoreItem) {
	ok := true
	for i := len(done) - 1; i >= 0; i-- {
		it := done[i]
		if _, err := os.Lstat(it.Src); err == nil {
			continue // never moved
		}
		if err := t.restoreItem(it.Dst, it.Src); err != nil {
			ok = false
		}
	}
	if !ok {
		t.log.Error("trash: restore interrupted, finished at the next start", "entry", e.ID)
		return
	}
	e.State, e.Returns = stateDone, nil
	if err := t.saveEntry(entryDir, *e); err != nil {
		t.log.Error("trash: restore undone, but its entry stays returning (finished at the next start)", "entry", e.ID, "error", err)
	}
}

// recoverReturn finishes a restore interrupted by a power cut (called by
// Recover with t.mu held): each item still in the entry goes to its place
// when that place is free; the others stay in the entry, "done" again.
func (t *Trash) recoverReturn(dir, sd string, e *entryFile) error {
	// The "returning" journal may not be durable yet (its sync failed).
	if err := t.syncDir(dir); err != nil {
		t.log.Error("trash: restore kept for the next start, its journal is not durable", "entry", e.ID, "error", err)
		return nil
	}
	var left []string
	for _, r := range e.Returns {
		if !filepath.IsLocal(filepath.FromSlash(r.Move)) || !filepath.IsLocal(filepath.FromSlash(r.Target)) {
			left = append(left, r.Move)
			continue
		}
		src := filepath.Join(dir, filepath.FromSlash(r.Move))
		if _, err := os.Lstat(src); err != nil {
			continue // already back
		}
		dst := filepath.Join(sd, filepath.FromSlash(r.Target))
		if err := t.returnOne(sd, src, dst, r.Target); err != nil {
			left = append(left, r.Move)
			continue
		}
		t.log.Info("trash: restored after an interrupted restore", "entry", e.ID, "file", r.Target)
	}
	// Moves not journaled (none in practice) stay in the entry too.
	journaled := map[string]bool{}
	for _, r := range e.Returns {
		journaled[r.Move] = true
	}
	for _, m := range e.Moves {
		if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(m))); err == nil && !journaled[m] {
			left = append(left, m)
		}
	}
	if len(left) == 0 {
		if err := os.RemoveAll(dir); err != nil {
			t.log.Warn("trash: cannot remove a restored entry", "entry", e.ID, "error", err)
		}
		t.log.Info("trash: interrupted restore finished", "entry", e.ID)
		return nil
	}
	recount(dir, e, left)
	e.Returns = nil
	if err := writeEntry(dir, *e); err != nil {
		return err
	}
	t.log.Warn("trash: interrupted restore, files kept in the trash (their place is taken)", "entry", e.ID, "files", e.Files)
	return nil
}
