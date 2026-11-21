package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Restore from the trash (M18, protocol/openapi.yaml
// POST /trash/{entryId}/restore): every item of an entry goes back to its
// original place, renamed on the same SD card. A present file is never
// overwritten: a taken place (same name ignoring case, exFAT) is a
// conflict, answered as such, avoided by a free name for every item, or,
// for a save of a game that still exists, settled by moving the current
// save to the trash first ("replace").
//
// Everything is read and renamed through an os.Root of the SD card,
// refusing links at every level, and an item goes back with a rename that
// never replaces (renameNoReplaceInSD).
//
// Power loss: entry.json is rewritten with state "returning", the rule and
// the place of every item before the first rename; Recover then finishes
// the restore when at least one item went back, and cancels it (the entry
// is "done" again, the displaced current save goes back) when none did:
// a POST that failed before its first rename never restores anything.
// Recover checks every journaled place against the rule and the original
// place, and runs the same checks as the POST (the pak checks of
// internal/transfer). An entry folder is only removed once it holds no
// file at all; an entry "returning" is never listed, deleted, emptied nor
// purged. Every restore holds t.mu, like Purge, Delete and Empty.

// Conflict rules of a restore.
const (
	RestoreFail    = "fail"
	RestoreRename  = "rename"
	RestoreReplace = "replace"
)

// stateReturning: the items of the entry are going back to the places in
// Returns (Restore); Recover finishes or cancels it.
const stateReturning = "returning"

// maxFreeName bounds the " (N)" tried for a free name.
const maxFreeName = 999

// Why no free name exists (TrashRestorePreview.freeNameUnavailable).
const (
	// NoFreeNameAllTaken: every " (N)" up to maxFreeName is taken.
	NoFreeNameAllTaken = "all-taken"
	// NoFreeNamePathBlocked: a folder on the way is a file (or a link):
	// no name in that folder can be used.
	NoFreeNamePathBlocked = "path-blocked"
)

// Where the current files go with "replace".
const (
	ReplaceKeptTrash  = "trash"
	ReplaceKeptBackup = "backup"
)

// Errors of a restore.
var (
	// ErrEntryDamaged: the entry holds files its journal does not list,
	// or lists places outside the card: nothing is restored (and nothing
	// of it is ever deleted by a restore).
	ErrEntryDamaged = errors.New("trash: entry damaged, its files do not match its journal")
	// ErrNotReplaceable: "replace" asked for an entry that is not the
	// save of a game that still exists.
	ErrNotReplaceable = errors.New("trash: only the save of an existing game can replace the current one")
)

// NotReplaceableError says why an entry cannot replace the current files
// (wraps ErrNotReplaceable).
type NotReplaceableError struct{ Reason string }

func (e *NotReplaceableError) Error() string { return ErrNotReplaceable.Error() + ": " + e.Reason }
func (e *NotReplaceableError) Unwrap() error { return ErrNotReplaceable }

// returnRecord is an item of a restore in entry.json, paths relative to
// the SD card: Move (as in Moves) goes back to Target.
type returnRecord struct {
	Move   string `json:"move"`
	Target string `json:"target"`
}

// RestoreConflictError lists the taken places (relative to the SD card,
// "ReminisLink/..." for the copies of ReminisLink) of a restore refused
// with RestoreFail, or with RestoreRename when no free name exists
// (NoFreeName then says why).
type RestoreConflictError struct {
	Conflicts  []string
	NoFreeName string
}

func (e *RestoreConflictError) Error() string {
	return "trash: restore conflicts with " + strings.Join(e.Conflicts, ", ")
}

// RestoreItem is a moved item of an entry and its place.
type RestoreItem struct {
	Src      string // absolute, in the entry
	Dst      string // absolute, where it goes
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
	// FreeName is nil when the entry has a conflict and no free name.
	FreeName *string `json:"freeName"`
	// ReplaceName is where the file goes with "replace" (nil when the
	// entry cannot replace).
	ReplaceName *string `json:"replaceName"`
	// target is where it goes without a free name (the folders on the
	// way as named on the card); free and item locate it.
	target string
	free   string
}

// ReplaceSpec is what a "replace" restore does (computed by the hook of
// SetRestoreHooks from the OS rules): every item of the entry goes to
// Targets[item.Rel] (relative to the SD card), after the files of
// Displace (absolute) went to a new trash entry labelled Label.
type ReplaceSpec struct {
	Targets  map[string]string
	Displace []string
	Label    string
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
	// NoFreeName says why no free name exists (nil: one exists, or no
	// conflict).
	NoFreeName *string `json:"freeNameUnavailable"`
	// Replaceable: "replace" is possible; ReplaceDisplaced are the
	// current files it would move to the trash first (relative to the SD
	// card).
	Replaceable      bool     `json:"replaceable"`
	ReplaceDisplaced []string `json:"replaceDisplaced"`
	// ReplaceKeptAs is where the current files go with "replace":
	// ReplaceKeptTrash (a new trash entry) or ReplaceKeptBackup (a backup
	// copy of the save, internal/saves); nil when not replaceable.
	ReplaceKeptAs *string `json:"replaceKeptAs"`
	// Items go to their original place, or to their free name or their
	// replace target once the rule is applied.
	Items   []RestoreItem `json:"-"`
	Renamed bool          `json:"-"`
	Mode    string        `json:"-"`
	// Conflicts are the taken places (see RestoreConflictError).
	Conflicts []string `json:"-"`
	// free are the Targets of the items under a free name (nil when the
	// entry has no conflict or no free name was found).
	free    []string
	replace *ReplaceSpec
}

// RestoreResult is the answer of POST /trash/{entryId}/restore.
type RestoreResult struct {
	Restored      []string `json:"restored"`
	RestoredBytes int64    `json:"restoredBytes"`
	Renamed       bool     `json:"renamed"`
	// ReplacedEntryID is the trash entry that now holds the current
	// files a "replace" moved away (nil otherwise), Replaced those files.
	ReplacedEntryID *string  `json:"replacedEntryId"`
	Replaced        []string `json:"replaced"`
	// ReplacedKeptAs: see RestorePlan.ReplaceKeptAs (nil without replace).
	ReplacedKeptAs *string `json:"replacedKeptAs"`
	// Plan is what was restored (Items at their final place).
	Plan RestorePlan `json:"-"`
}

// SetRestoreHooks sets what the store cannot know (internal/transfer):
// check runs before every item goes back, by a POST and by Recover alike
// (the pak checks of an installation); replace computes a "replace"
// restore from the OS rules (nil: never replaceable). Call it before
// Recover.
func (t *Trash) SetRestoreHooks(check func(it RestoreItem) error, replace func(p RestorePlan) (*ReplaceSpec, error)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.returnCheck, t.replaceSpec = check, replace
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
	e, err := t.readEntry(dir)
	if err != nil || e.State != stateDone || e.ID != id || !validReasons[e.Reason] {
		return entryFile{}, ErrTrashNotFound
	}
	return e, nil
}

// PlanRestore returns what Restore would put back (the preview): every
// user file, whether its place is taken, its free name and, when the
// entry can replace the current files, where each goes then.
func (t *Trash) PlanRestore(id string) (RestorePlan, error) {
	if !ValidTrashID(id) {
		return RestorePlan{}, ErrInvalidTrashID
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	p, err := t.planRestore(id)
	if err != nil {
		return RestorePlan{}, err
	}
	t.planReplace(&p)
	return p, nil
}

// entryPayload returns every file of an entry folder (relative to the
// entry, "/" separator), entry.json and its temporary file aside, read
// through root (entryRel relative to the SD card). Links and special
// files count as files: they are never followed. A read error is
// returned: the content of the entry is then unknown.
func entryPayload(root *os.Root, entryRel string) ([]string, error) {
	var out []string
	base := filepath.ToSlash(entryRel)
	err := fs.WalkDir(root.FS(), base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == base || d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(p, base+"/")
		if rel == trashEntryFile || rel == trashEntryFile+".tmp" {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return out, err
}

// under reports whether the "/" path p is root or inside it.
func under(root, p string) bool { return p == root || strings.HasPrefix(p, root+"/") }

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
	root, err := t.openSD()
	if err != nil {
		return RestorePlan{}, fmt.Errorf("trash: %w", err)
	}
	defer root.Close()
	entryDir := filepath.Join(t.dir, id)
	entryRel, err := t.confinedRel(root, entryDir)
	if err != nil {
		return RestorePlan{}, fmt.Errorf("trash: %w", err)
	}
	trashRel, _ := t.sdRel(t.dir)
	internal := map[string]bool{}
	for _, rel := range e.Internal {
		internal[rel] = true
	}
	// Every file of the folder must belong to a move of the journal:
	// otherwise the restore would leave it behind (and the entry would be
	// removed with it).
	payload, err := entryPayload(root, entryRel)
	if err != nil {
		return RestorePlan{}, fmt.Errorf("trash: read entry %s: %w", id, withoutPathErr(err))
	}
	for _, f := range payload {
		covered := false
		for _, m := range e.Moves {
			if under(m, f) {
				covered = true
				break
			}
		}
		if !covered {
			t.log.Error("trash: entry holds a file its journal does not list", "entry", id, "file", f)
			return RestorePlan{}, ErrEntryDamaged
		}
	}
	p := RestorePlan{EntryID: e.ID, Reason: e.Reason, Label: e.Label, Files: []RestoreFile{}, ReplaceDisplaced: []string{}}
	seen := map[string]bool{}
	for _, rel := range e.Moves {
		local := filepath.FromSlash(rel)
		if !filepath.IsLocal(local) || seen[strings.ToLower(rel)] || isUnder(trashRel, local) || isUnder(local, trashRel) {
			t.log.Error("trash: entry lists a place outside the card or in the trash", "entry", id, "file", rel)
			return RestorePlan{}, ErrEntryDamaged
		}
		seen[strings.ToLower(rel)] = true
		srcRel := filepath.Join(entryRel, local)
		if err := checkNoLinks(root, srcRel); err != nil {
			t.log.Error("trash: an item of the entry is reached through a link", "entry", id, "file", rel)
			return RestorePlan{}, ErrEntryDamaged
		}
		fi, err := root.Lstat(srcRel)
		if errors.Is(err, fs.ErrNotExist) {
			t.log.Warn("trash: an item of the entry is missing", "entry", id, "file", rel)
			continue
		}
		if err != nil {
			return RestorePlan{}, fmt.Errorf("trash: %w", withoutPathErr(err))
		}
		if !fi.IsDir() && !fi.Mode().IsRegular() {
			// Never a link nor a special file (rule 2 of CLAUDE.md).
			return RestorePlan{}, ErrEntryDamaged
		}
		target, taken, _, err := resolveFold(root, rel)
		if err != nil {
			return RestorePlan{}, fmt.Errorf("trash: %w", err)
		}
		it := RestoreItem{Src: filepath.Join(sd, srcRel), Rel: rel, Target: target, Dir: fi.IsDir(), Internal: internal[rel], Conflict: taken}
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
		var why string
		p.free, why, err = t.freeNames(root, p.Items)
		if err != nil {
			return RestorePlan{}, fmt.Errorf("trash: %w", err)
		}
		if p.free == nil {
			p.NoFreeName = &why
		}
	}
	for i, it := range p.Items {
		if it.Internal {
			continue
		}
		freeTarget := it.Target
		if p.free != nil {
			freeTarget = p.free[i]
		}
		_, fromBase := splitRel(it.Rel)
		_, toBase := splitRel(freeTarget)
		_ = fs.WalkDir(root.FS(), filepath.ToSlash(srcRelOf(sd, it.Src)), func(walked string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			inner := strings.TrimPrefix(strings.TrimPrefix(walked, filepath.ToSlash(srcRelOf(sd, it.Src))), "/")
			name, free, target := it.Rel, freeTarget, it.Target
			if inner != "" {
				name += "/" + inner
				target += "/" + inner
				free += "/" + playlistName(inner, fromBase, toBase)
			}
			f := RestoreFile{Name: name, Size: info.Size(), Conflict: it.Conflict, target: target, free: free}
			if !p.Conflict {
				f.FreeName = ptr(name)
			} else if p.free != nil {
				f.FreeName = ptr(free)
			}
			p.Files = append(p.Files, f)
			p.Bytes += info.Size()
			return nil
		})
	}
	for i := range p.Items {
		p.Items[i].Dst = filepath.Join(sd, filepath.FromSlash(p.Items[i].Target))
	}
	return p, nil
}

func ptr[T any](v T) *T { return &v }

// srcRelOf returns abs relative to sd (OS separators).
func srcRelOf(sd, abs string) string {
	rel, err := filepath.Rel(sd, abs)
	if err != nil {
		return abs
	}
	return rel
}

// playlistName renames the playlist of a folder game that goes back under
// a free name: NextUI recognises Game/Game.m3u (or Game.cue) as a game
// only when the playlist is named after the folder, ignoring case, so
// Game (2)/Game.m3u becomes Game (2)/Game (2).m3u. The playlist lists the
// files of its folder by relative name: its content does not change.
// inner is a "/" path inside the folder; other files keep their name.
func playlistName(inner, fromBase, toBase string) string {
	if fromBase == toBase || strings.Contains(inner, "/") {
		return inner
	}
	for _, ext := range playlistExts {
		if strings.EqualFold(inner, fromBase+ext) {
			return toBase + ext
		}
	}
	return inner
}

var playlistExts = []string{".m3u", ".cue"}

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

// resolveFold resolves rel ("/" separator, relative to the SD card) the
// way exFAT does, ignoring case, reading the folders through root (a link
// is never followed: it is a component that is not a folder): each
// existing component takes its name on the card (an exact match wins).
// taken reports that something already has the name of the last
// component, or that a component on the way is not a folder; blocked
// reports the latter (no name in that folder can be used). A folder that
// cannot be read is an error, never "free".
func resolveFold(root *os.Root, rel string) (target string, taken, blocked bool, err error) {
	parts := strings.Split(rel, "/")
	dir := "."
	out := make([]string, 0, len(parts))
	for i, c := range parts {
		match, mode, found, err := findAny(root, dir, c)
		if err != nil {
			return "", false, false, err
		}
		if !found {
			out = append(out, parts[i:]...)
			return strings.Join(out, "/"), false, false, nil
		}
		if i == len(parts)-1 {
			// The item keeps its own name (a free name is built on it).
			return strings.Join(append(out, c), "/"), true, false, nil
		}
		out = append(out, match)
		if !mode.IsDir() {
			out = append(out, parts[i+1:]...)
			return strings.Join(out, "/"), true, true, nil
		}
		dir = path.Join(dir, match)
	}
	return strings.Join(out, "/"), false, false, nil
}

// findAny returns the entry of dir (relative to root) named name ignoring
// case (any type, an exact match wins) and its type. A missing dir holds
// nothing; any other read error is returned.
func findAny(root *os.Root, dir, name string) (string, fs.FileMode, bool, error) {
	des, err := fs.ReadDir(root.FS(), dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, fmt.Errorf("read folder: %w", withoutPathErr(err))
	}
	match, mode, found := "", fs.FileMode(0), false
	for _, d := range des {
		if d.Name() == name {
			return name, d.Type(), true, nil
		}
		if !found && strings.EqualFold(d.Name(), name) {
			match, mode, found = d.Name(), d.Type(), true
		}
	}
	return match, mode, found, nil
}

// freeNames returns the targets of items under the first free name
// " (N)" (N from 2), the same N for every item so the files of a game
// stay together, or nil and why when none is free up to maxFreeName.
func (t *Trash) freeNames(root *os.Root, items []RestoreItem) ([]string, string, error) {
	for _, it := range items {
		if _, _, blocked, err := resolveFold(root, it.Target); err != nil {
			return nil, "", err
		} else if blocked {
			return nil, NoFreeNamePathBlocked, nil
		}
	}
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
			_, taken, _, err := resolveFold(root, target)
			if err != nil {
				return nil, "", err
			}
			if taken {
				ok = false
				break
			}
			out[i] = target
		}
		if ok {
			return out, "", nil
		}
	}
	return nil, NoFreeNameAllTaken, nil
}

// splitRel splits a "/" path into its folder ("" at the top) and name.
func splitRel(rel string) (string, string) {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[:i], rel[i+1:]
	}
	return "", rel
}

// nameStems returns the names of the items without their last extension
// (the game name of a ROM, "Game.gba" for its save "Game.gba.sav"; a
// folder without a dot keeps its name).
func nameStems(items []RestoreItem) []string {
	var stems []string
	for _, it := range items {
		_, base := splitRel(it.Rel)
		stem := stemOf(base, it.Dir)
		stems = append(stems, stem)
		// The data of a game is named after its ROM file (Game.gba.sav):
		// alone in an entry, it still takes the number after the game
		// name ("Game (2).gba.sav", the save of "Game (2).gba").
		if !it.Dir && dataExt(filepath.Ext(base)) && strings.Contains(stem, ".") {
			stems = append(stems, strings.TrimSuffix(stem, filepath.Ext(stem)))
		}
	}
	return stems
}

// dataExt reports whether ext names the emulator data of a game (save,
// clock file, cheats, state).
func dataExt(ext string) bool {
	switch e := strings.ToLower(ext); {
	case e == ".sav" || e == ".rtc" || e == ".cht":
		return true
	case len(e) == 4 && strings.HasPrefix(e, ".st") && e[3] >= '0' && e[3] <= '9':
		return true
	}
	return false
}

func stemOf(base string, dir bool) string {
	stem := base
	if !dir || strings.Contains(base, ".") {
		stem = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return stem
}

// withNumber inserts " (n)" after the shortest stem that starts base,
// ignoring case as NextUI pairs a game with its data (Game.gba with
// GAME.gba.sav: "Game (2).gba", "GAME (2).gba.sav", "Clock (2).pak"), or,
// without one, before the last extension of base. base keeps its own case.
func withNumber(base string, stems []string, n int) string {
	suffix := " (" + strconv.Itoa(n) + ")"
	best := -1
	for _, s := range stems {
		if s == "" || len(s) > len(base) || !strings.EqualFold(base[:len(s)], s) {
			continue
		}
		if len(base) != len(s) && base[len(s)] != '.' {
			continue
		}
		if best < 0 || len(s) < best {
			best = len(s)
		}
	}
	if best < 0 {
		best = len(strings.TrimSuffix(base, filepath.Ext(base)))
		if best == 0 {
			return base + suffix
		}
	}
	return base[:best] + suffix + base[best:]
}

// validTarget reports whether a journaled place is one the rule can give
// to the item move (Recover never trusts the journal alone): the original
// place (ignoring case), a free name " (N)" in the same folder, or, with
// "replace", a file of the same kind (extension) in the same folder.
func validTarget(mode, move, target string, stems []string) bool {
	if !filepath.IsLocal(filepath.FromSlash(target)) {
		return false
	}
	if strings.EqualFold(move, target) {
		return true
	}
	mParent, mBase := splitRel(move)
	tParent, tBase := splitRel(target)
	if !strings.EqualFold(mParent, tParent) {
		return false
	}
	switch mode {
	case RestoreRename:
		for n := 2; n <= maxFreeName; n++ {
			if strings.EqualFold(tBase, withNumber(mBase, stems, n)) {
				return true
			}
		}
	case RestoreReplace:
		ext := strings.ToLower(filepath.Ext(mBase))
		return (ext == ".sav" || ext == ".rtc") && strings.EqualFold(filepath.Ext(tBase), ext)
	}
	return false
}

// planReplace fills Replaceable, ReplaceDisplaced and the replace names of
// a plan from the hook (nothing without one). Called with t.mu held.
func (t *Trash) planReplace(p *RestorePlan) {
	if t.replaceSpec == nil {
		return
	}
	spec, err := t.replaceSpec(*p)
	if err != nil || spec == nil || t.checkReplace(*p, spec) != nil {
		return
	}
	p.Replaceable, p.replace, p.ReplaceKeptAs = true, spec, ptr(ReplaceKeptTrash)
	sd, _ := filepath.Abs(t.sd)
	for _, d := range spec.Displace {
		if r, err := filepath.Rel(sd, d); err == nil {
			p.ReplaceDisplaced = append(p.ReplaceDisplaced, filepath.ToSlash(r))
		}
	}
	for i, f := range p.Files {
		for _, it := range p.Items {
			if f.Name == it.Rel {
				p.Files[i].ReplaceName = ptr(spec.Targets[it.Rel])
			}
		}
	}
}

// checkReplace checks a replace spec: only files, a target for each item
// in its own folder (validTarget), displaced files inside the card.
func (t *Trash) checkReplace(p RestorePlan, spec *ReplaceSpec) error {
	stems := nameStems(p.Items)
	for _, it := range p.Items {
		target, ok := spec.Targets[it.Rel]
		if it.Dir || it.Internal || !ok || !validTarget(RestoreReplace, it.Rel, target, stems) {
			return &NotReplaceableError{Reason: "not-a-save"}
		}
	}
	for _, d := range spec.Displace {
		if _, err := t.sdRel(d); err != nil {
			return &NotReplaceableError{Reason: "not-a-save"}
		}
	}
	return nil
}

// Restore puts every item of the entry id back (RestoreFail: refused with
// a *RestoreConflictError when a place is taken; RestoreRename: every item
// under the same free name then; RestoreReplace: the current save moves to
// a new entry first, *NotReplaceableError for anything else than the save
// of an existing game). check, when not nil, runs on the final plan before
// anything moves; the hook of SetRestoreHooks then runs on every item.
// Their errors are returned as they are. All or nothing: on an error, the
// items already restored go back to the entry (and a displaced save to
// its place).
func (t *Trash) Restore(id, onConflict string, check func(RestorePlan) error) (RestoreResult, error) {
	if !ValidTrashID(id) {
		return RestoreResult{}, ErrInvalidTrashID
	}
	if onConflict != RestoreFail && onConflict != RestoreRename && onConflict != RestoreReplace {
		return RestoreResult{}, fmt.Errorf("trash: unknown conflict rule %q", onConflict)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	p, err := t.planRestore(id)
	if err != nil {
		return RestoreResult{}, err
	}
	sd, err := filepath.Abs(t.sd)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("trash: %w", err)
	}
	p.Mode = RestoreFail
	switch {
	case onConflict == RestoreReplace:
		if t.replaceSpec == nil {
			return RestoreResult{}, &NotReplaceableError{Reason: "not-a-save"}
		}
		spec, err := t.replaceSpec(p)
		if err != nil {
			return RestoreResult{}, err
		}
		if err := t.checkReplace(p, spec); err != nil {
			return RestoreResult{}, err
		}
		p.Mode, p.replace = RestoreReplace, spec
		for i := range p.Items {
			p.Items[i].Target = spec.Targets[p.Items[i].Rel]
			p.Items[i].Dst = filepath.Join(sd, filepath.FromSlash(p.Items[i].Target))
		}
	case p.Conflict && onConflict == RestoreFail:
		return RestoreResult{}, &RestoreConflictError{Conflicts: p.Conflicts}
	case p.Conflict && p.free == nil:
		return RestoreResult{}, &RestoreConflictError{Conflicts: p.Conflicts, NoFreeName: *p.NoFreeName}
	case p.Conflict:
		for i := range p.Items {
			p.Items[i].Target = p.free[i]
			p.Items[i].Dst = filepath.Join(sd, filepath.FromSlash(p.free[i]))
		}
		p.Renamed, p.Mode = true, RestoreRename
	}
	if check != nil {
		if err := check(p); err != nil {
			return RestoreResult{}, err
		}
	}
	if t.returnCheck != nil {
		for _, it := range p.Items {
			if err := t.returnCheck(it); err != nil {
				return RestoreResult{}, err
			}
		}
	}
	res := RestoreResult{Restored: []string{}, Replaced: []string{}, RestoredBytes: p.Bytes, Renamed: p.Renamed, Plan: p}
	displaced := ""
	if p.Mode == RestoreReplace && len(p.replace.Displace) > 0 {
		items := make([]MoveItem, len(p.replace.Displace))
		for i, d := range p.replace.Displace {
			items[i] = MoveItem{Path: d}
		}
		moved, _, de, err := t.moveLocked(ReasonRestore, p.replace.Label, items, nil, func(e *entryFile) { e.RestoreOf = id })
		if err != nil {
			return RestoreResult{}, err
		}
		displaced = de.ID
		res.ReplacedEntryID, res.Replaced = ptr(de.ID), moved.Moved
	}
	if p.Mode == RestoreReplace {
		res.ReplacedKeptAs = ptr(ReplaceKeptTrash)
	}
	if err := t.returnItems(&p, displaced); err != nil {
		if displaced != "" {
			if uerr := t.undoLocked(displaced); uerr != nil {
				t.log.Error("trash: the current save stays in the trash after a failed restore", "entry", displaced, "error", uerr)
			}
		}
		return RestoreResult{}, err
	}
	if displaced != "" {
		t.clearRestoreOf(displaced)
	}
	for _, f := range p.Files {
		switch {
		case p.Mode == RestoreReplace && f.ReplaceNameOr(p) != "":
			res.Restored = append(res.Restored, f.ReplaceNameOr(p))
		case p.Renamed:
			res.Restored = append(res.Restored, f.free)
		default:
			res.Restored = append(res.Restored, f.target)
		}
	}
	res.Plan = p
	t.log.Info("restored from the trash", "entry", id, "reason", p.Reason, "label", p.Label, "mode", p.Mode,
		"files", res.Restored, "bytes", res.RestoredBytes, "displaced", displaced)
	return res, nil
}

// ReplaceNameOr is the replace target of a file of p ("" if none).
func (f RestoreFile) ReplaceNameOr(p RestorePlan) string {
	if p.replace == nil {
		return ""
	}
	return p.replace.Targets[f.Name]
}

// clearRestoreOf drops the link of a displaced entry to its restore once
// that restore succeeded (best effort: a stale link is harmless, the
// places of its files are taken). Called with t.mu held.
func (t *Trash) clearRestoreOf(id string) {
	dir := filepath.Join(t.dir, id)
	e, err := t.readEntry(dir)
	if err != nil || e.RestoreOf == "" {
		return
	}
	e.RestoreOf = ""
	if err := t.saveEntry(dir, e); err != nil {
		t.log.Warn("trash: cannot update a displaced entry", "entry", id, "error", err)
	}
}

// returnItems journals the restore, renames every item to its place and
// removes the entry once it holds no file. Called with t.mu held.
func (t *Trash) returnItems(p *RestorePlan, displaced string) error {
	entryDir := filepath.Join(t.dir, p.EntryID)
	e, err := t.readEntry(entryDir)
	if err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	sd, err := filepath.Abs(t.sd)
	if err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	done := e
	e.State, e.ReturnMode, e.Displaced = stateReturning, p.Mode, displaced
	e.Returns = nil
	for _, it := range p.Items {
		e.Returns = append(e.Returns, returnRecord{Move: it.Rel, Target: it.Target})
	}
	// The intent is on the card before the first rename. If writing it
	// fails, it may still have reached the card: put "done" back (best
	// effort); otherwise Recover sees nothing moved and cancels it.
	if err := t.saveEntry(entryDir, e); err != nil {
		if rerr := t.saveEntry(entryDir, done); rerr != nil {
			t.log.Error("trash: restore journal may be left, cancelled at the next start", "entry", e.ID, "error", rerr)
		}
		return err
	}
	for i, it := range p.Items {
		if err := t.returnOne(sd, it.Src, it.Dst, it.Target, it.Rel, it.Dir); err != nil {
			t.undoReturn(entryDir, &e, p.Items[:i+1])
			return fmt.Errorf("trash: restore %s: %w", it.Rel, withoutPathErr(err))
		}
	}
	t.finishEntry(entryDir, &e, e.Moves)
	return nil
}

// finishEntry ends an entry whose items went back: removed when its
// folder holds no file any more; otherwise it becomes "done" again with
// what is really left (the moves in left that are still there, and any
// file the journal did not list, each as its own item), never deleted.
// Called with t.mu held.
func (t *Trash) finishEntry(dir string, e *entryFile, left []string) {
	root, err := t.openSD()
	if err != nil {
		t.log.Error("trash: entry kept, the card cannot be read", "entry", e.ID, "error", err)
		return
	}
	defer root.Close()
	rel, err := t.confinedRel(root, dir)
	if err != nil {
		t.log.Error("trash: entry kept, reached through a link", "entry", e.ID, "error", err)
		return
	}
	payload, err := entryPayload(root, rel)
	if err != nil {
		t.log.Error("trash: entry kept, its folder cannot be read", "entry", e.ID, "error", err)
		return
	}
	if len(payload) == 0 {
		doomed := filepath.Join(t.dir, trashDeletingPrefix+e.ID)
		if err := os.Rename(dir, doomed); err != nil {
			t.log.Warn("trash: restored entry left for the next start", "entry", e.ID, "error", err)
			return
		}
		SyncDir(t.dir)
		if err := os.RemoveAll(doomed); err != nil {
			t.log.Warn("trash: cannot remove a restored entry", "entry", e.ID, "error", err)
		}
		SyncDir(t.dir)
		return
	}
	keep := map[string]bool{}
	for _, f := range payload {
		owner := f
		for _, m := range left {
			if under(m, f) {
				owner = m
				break
			}
		}
		keep[owner] = true
	}
	var moves []string
	for m := range keep {
		moves = append(moves, m)
	}
	sort.Strings(moves)
	e.Returns, e.ReturnMode, e.Displaced = nil, "", ""
	recount(dir, e, moves)
	if err := t.saveEntry(dir, *e); err != nil {
		t.log.Error("trash: entry with files left, journal not updated", "entry", e.ID, "error", err)
		return
	}
	t.log.Warn("trash: files kept in the trash after a restore", "entry", e.ID, "files", e.Files, "internal", e.Internal)
}

// returnOne renames src (in the entry) to dst when its place is still
// free, ignoring case, without ever replacing it; a folder game that goes
// back under a free name gets its playlist renamed after the folder. It
// syncs every folder created on the way and both folders of the rename.
func (t *Trash) returnOne(sd, src, dst, target, move string, dir bool) error {
	root, err := t.openSD()
	if err != nil {
		return err
	}
	_, taken, _, err := resolveFold(root, target)
	root.Close()
	if err != nil {
		return err
	}
	if taken {
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
	if err := t.renameExcl(src, dst); err != nil {
		return err
	}
	if dir {
		if err := t.renamePlaylist(dst, move, target, false); err != nil {
			return err
		}
	}
	if err := t.syncDir(parent); err != nil {
		return err
	}
	return t.syncDir(filepath.Dir(src))
}

// renamePlaylist renames the playlist of a folder game restored under a
// free name (see playlistName) inside folder, or back when undo.
func (t *Trash) renamePlaylist(folder, move, target string, undo bool) error {
	_, fromBase := splitRel(move)
	_, toBase := splitRel(target)
	if fromBase == toBase {
		return nil
	}
	if undo {
		fromBase, toBase = toBase, fromBase
	}
	des, err := os.ReadDir(folder)
	if err != nil {
		return err
	}
	for _, ext := range playlistExts {
		for _, d := range des {
			if d.Type().IsRegular() && strings.EqualFold(d.Name(), fromBase+ext) {
				if err := t.renameExcl(filepath.Join(folder, d.Name()), filepath.Join(folder, toBase+ext)); err != nil && !errors.Is(err, fs.ErrExist) {
					return err
				}
				if err := t.syncDir(folder); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

// moved reports whether an item left the entry: only a missing source
// says so; any other error leaves its fate unknown.
func moved(src string) (bool, error) {
	_, err := os.Lstat(src)
	switch {
	case err == nil:
		return false, nil
	case errors.Is(err, fs.ErrNotExist):
		return true, nil
	default:
		return false, err
	}
}

// undoReturn moves the items already restored (the last one too when its
// rename happened) back into the entry, which becomes "done" again; when
// one cannot go back, the entry stays "returning" and the next start
// finishes the restore. Called with t.mu held.
func (t *Trash) undoReturn(entryDir string, e *entryFile, done []RestoreItem) {
	ok := true
	for i := len(done) - 1; i >= 0; i-- {
		it := done[i]
		gone, err := moved(it.Src)
		if err != nil {
			ok = false
			continue
		}
		if !gone {
			continue // never moved
		}
		if it.Dir {
			if err := t.renamePlaylist(it.Dst, it.Rel, it.Target, true); err != nil {
				ok = false
				continue
			}
		}
		if err := t.restoreItem(it.Dst, it.Src); err != nil {
			ok = false
		}
	}
	if !ok {
		t.log.Error("trash: restore interrupted, finished at the next start", "entry", e.ID)
		return
	}
	e.State, e.Returns, e.ReturnMode, e.Displaced = stateDone, nil, "", ""
	if err := t.saveEntry(entryDir, *e); err != nil {
		t.log.Error("trash: restore undone, but its entry stays returning (cancelled at the next start)", "entry", e.ID, "error", err)
	}
}

// recoverReturn finishes or cancels a restore interrupted by a power cut
// (called by Recover with t.mu held). Nothing went back yet: cancelled
// (the entry is "done" again, a displaced current save goes back). Else
// each item still in the entry goes to its journaled place when that
// place is valid for the rule (validTarget), passes the checks and is
// free; the others stay in the entry, "done" again.
func (t *Trash) recoverReturn(dir, sd string, e *entryFile) error {
	// The "returning" journal may not be durable yet (its sync failed).
	if err := t.syncDir(dir); err != nil {
		t.log.Error("trash: restore kept for the next start, its journal is not durable", "entry", e.ID, "error", err)
		return nil
	}
	inMoves := map[string]bool{}
	for _, m := range e.Moves {
		inMoves[m] = true
	}
	internal := map[string]bool{}
	for _, m := range e.Internal {
		internal[m] = true
	}
	var items []RestoreItem
	for _, r := range e.Returns {
		items = append(items, RestoreItem{Rel: r.Move})
	}
	stems := nameStems(items)
	anyMoved, unknown := false, false
	for _, r := range e.Returns {
		if !inMoves[r.Move] || !filepath.IsLocal(filepath.FromSlash(r.Move)) {
			continue
		}
		gone, err := moved(filepath.Join(dir, filepath.FromSlash(r.Move)))
		if err != nil {
			unknown = true
		}
		anyMoved = anyMoved || gone
	}
	if unknown {
		t.log.Error("trash: restore kept for the next start, the entry cannot be read", "entry", e.ID)
		return nil
	}
	if !anyMoved {
		// The POST failed (or the power went) before the first rename:
		// the restore never happened.
		displaced := e.Displaced
		e.State, e.Returns, e.ReturnMode, e.Displaced = stateDone, nil, "", ""
		if err := t.saveEntry(dir, *e); err != nil {
			return err
		}
		t.log.Info("trash: interrupted restore cancelled, nothing had gone back", "entry", e.ID)
		if displaced != "" && ValidTrashID(displaced) {
			if err := t.undoLocked(displaced); err != nil {
				t.log.Error("trash: the displaced save stays in the trash", "entry", displaced, "error", err)
			}
		}
		return nil
	}
	var left []string
	for _, r := range e.Returns {
		src := filepath.Join(dir, filepath.FromSlash(r.Move))
		if gone, err := moved(src); err != nil || gone {
			continue // already back (or unknown: kept below by finishEntry)
		}
		if !inMoves[r.Move] || !validTarget(e.ReturnMode, r.Move, r.Target, stems) {
			t.log.Error("trash: journaled place refused, the item stays in the trash", "entry", e.ID, "file", r.Move, "target", r.Target)
			left = append(left, r.Move)
			continue
		}
		fi, err := os.Lstat(src)
		if err != nil || (!fi.IsDir() && !fi.Mode().IsRegular()) {
			left = append(left, r.Move)
			continue
		}
		dst := filepath.Join(sd, filepath.FromSlash(r.Target))
		it := RestoreItem{Src: src, Dst: dst, Rel: r.Move, Target: r.Target, Dir: fi.IsDir(), Internal: internal[r.Move]}
		if t.returnCheck != nil {
			if err := t.returnCheck(it); err != nil {
				t.log.Error("trash: item refused by the checks, it stays in the trash", "entry", e.ID, "file", r.Move, "error", err)
				left = append(left, r.Move)
				continue
			}
		}
		if err := t.returnOne(sd, src, dst, r.Target, r.Move, it.Dir); err != nil {
			left = append(left, r.Move)
			continue
		}
		t.log.Info("trash: restored after an interrupted restore", "entry", e.ID, "file", r.Target)
	}
	// Moves not journaled stay in the entry too.
	journaled := map[string]bool{}
	for _, r := range e.Returns {
		journaled[r.Move] = true
	}
	for _, m := range e.Moves {
		if !journaled[m] {
			left = append(left, m)
		}
	}
	if e.Displaced != "" {
		t.clearRestoreOf(e.Displaced)
	}
	t.log.Info("trash: interrupted restore finished", "entry", e.ID, "left", left)
	t.finishEntry(dir, e, e.Moves)
	return nil
}

// recoverDisplaced handles a "done" entry that holds the current save a
// "replace" restore moved away (RestoreOf): while that restore's entry is
// still "done", the restore never started, so the save goes back (called
// by Recover with t.mu held). A finished or vanished restore leaves it.
func (t *Trash) recoverDisplaced(name string, e entryFile) {
	if !ValidTrashID(e.RestoreOf) {
		return
	}
	ref, err := t.readEntry(filepath.Join(t.dir, e.RestoreOf))
	if err != nil || ref.State != stateDone {
		return // finished, or "returning" (its own recovery decides)
	}
	if err := t.undoLocked(name); err != nil {
		t.log.Error("trash: the displaced save of an interrupted restore stays in the trash", "entry", name, "error", err)
		return
	}
	t.log.Info("trash: put back the save of a restore that never started", "entry", name)
}
