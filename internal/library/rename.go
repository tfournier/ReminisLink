package library

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/store"
)

// Renaming a game (M23, POST /games/{gameId}/rename): the ROM and every
// file named after it get the new name, with the pairing rules of the
// adapter. PlanRename computes the renames and checks that every new name
// is free; internal/transfer runs them as one journaled group
// (store.Trash.RenameGroup).

// ErrInvalidName: the new name cannot be used (400 invalid_name).
var ErrInvalidName = errors.New("invalid name")

// MaxNameBytes is the longest file name written (exFAT: 255 characters;
// counted in bytes, as the uploads, docs/specs/03-protocole.md §5).
const MaxNameBytes = 255

// RenameConflictError: the rename cannot run (409 conflict): new names
// are taken (Conflicts, relative to the SD card), or the discs of a folder
// game are shared with another playlist (Shared).
type RenameConflictError struct {
	Conflicts []string
	Shared    bool
}

func (e *RenameConflictError) Error() string {
	if e.Shared {
		return "rename: discs shared with another playlist"
	}
	return "rename: names taken: " + strings.Join(e.Conflicts, ", ")
}

// Renamed is an item of GameRenameResult.renamed.
type Renamed struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// RenamePlan is what renaming a game does.
type RenamePlan struct {
	Old, New osadapter.Game
	Steps    []store.RenameStep
	Renamed  []Renamed
}

// PlanRename computes the renames that give the game gameID the name name
// (already cleaned by the caller: NFC, no forbidden character), the
// extension of its ROM (or of the playlist of a folder game) kept. An
// empty plan means the name does not change. Errors: ErrInvalidID,
// ErrNotFound, ErrInvalidName (a name too long once completed, or not a
// game name for the OS), osadapter.ErrUnsupportedFormat (the saves or
// states use a format the pak does not name), *RenameConflictError.
func (l *Library) PlanRename(gameID, name string) (RenamePlan, error) {
	g, err := l.game(gameID)
	if err != nil {
		return RenamePlan{}, err
	}
	if err := l.adapter.CheckSaveFormat(g); err != nil {
		return RenamePlan{}, err
	}
	if err := l.adapter.CheckStateFormat(g); err != nil {
		return RenamePlan{}, err
	}
	newFile, newROM := name+filepath.Ext(g.FileName), name+filepath.Ext(g.FileName)
	if g.Kind.IsFolder() {
		newFile, newROM = name, name+filepath.Ext(g.ROMName)
	}
	if !g.Kind.IsFolder() && !l.adapter.IsSystemGameFile(g.System, newFile) {
		return RenamePlan{}, fmt.Errorf("%w: not a game file name", ErrInvalidName)
	}
	plan := RenamePlan{Old: g, New: g}
	plan.New.Path = path.Join(path.Dir(g.Path), newFile)
	plan.New.FileName, plan.New.ROMName = newFile, newROM
	if newFile == g.FileName && newROM == g.ROMName {
		return plan, nil
	}
	files, err := l.adapter.GameFiles(g)
	if err != nil {
		return RenamePlan{}, notFound(err, "game")
	}
	shared := l.adapter.SharedFiles(g)
	artShared := files.Art != nil && shared[files.Art.Path]
	if g.Kind.IsFolder() && len(shared) > 0 && !(artShared && len(shared) == 1) {
		return RenamePlan{}, &RenameConflictError{Shared: true}
	}
	main, err := resolveUnder(l.romsRoot(), g.Path)
	if err != nil {
		return RenamePlan{}, err
	}
	// check is where a step's new name is looked for before anything is
	// renamed (the playlist of a folder game is still in the old folder).
	type planned struct {
		step      store.RenameStep
		checkFrom string
		checkTo   string
	}
	var steps []planned
	add := func(from, to, checkFrom, checkTo string) {
		if from == to {
			return
		}
		steps = append(steps, planned{store.RenameStep{From: from, To: to}, checkFrom, checkTo})
	}
	newMain := filepath.Join(filepath.Dir(main), newFile)
	add(main, newMain, main, newMain)
	if g.Kind.IsFolder() && newROM != g.ROMName {
		add(filepath.Join(newMain, g.ROMName), filepath.Join(newMain, newROM),
			filepath.Join(main, g.ROMName), filepath.Join(main, newROM))
	}
	// The emulator data is named after the ROM file name (NextUI) or its
	// state key (Knulli: without its extension): the longest key that
	// starts the name is replaced, the rest of the name kept.
	oldKeys := []string{g.ROMName, l.adapter.StateKey(g.ROMName)}
	newKeys := []string{newROM, l.adapter.StateKey(newROM)}
	data := func(p string) {
		base := filepath.Base(p)
		for i, k := range oldKeys {
			if k != "" && len(base) >= len(k) && strings.EqualFold(base[:len(k)], k) {
				to := filepath.Join(filepath.Dir(p), newKeys[i]+base[len(k):])
				add(p, to, p, to)
				return
			}
		}
	}
	if files.Save != nil {
		data(files.Save.Path)
	}
	if files.RTC != nil {
		data(files.RTC.Path)
	}
	for _, st := range files.States {
		data(st.Path)
	}
	for _, f := range files.StateSide {
		data(f.Path)
	}
	if files.Cheats != nil {
		data(files.Cheats.Path)
		if bak := store.FindFold(filepath.Dir(files.Cheats.Path), filepath.Base(files.Cheats.Path)+".bak", false); bak != "" {
			data(bak)
		}
	}
	if files.Art != nil && !artShared {
		to := filepath.Join(filepath.Dir(files.Art.Path), filepath.Base(l.adapter.ArtPath(plan.New)))
		add(files.Art.Path, to, files.Art.Path, to)
	}
	// The ReminisLink copies: state locks, save backups and resume
	// history, one folder per ROM file name.
	for _, dir := range []string{l.layout.LocksDir(g.System.Tag), l.layout.BackupsDir(g.System.Tag)} {
		if from := store.FindFold(dir, g.ROMName, true); from != "" {
			to := filepath.Join(dir, newROM)
			add(from, to, from, to)
		}
	}

	var conflicts []string
	seen := map[string]bool{}
	for _, s := range steps {
		if len(filepath.Base(s.step.To)) > MaxNameBytes {
			return RenamePlan{}, fmt.Errorf("%w: too long", ErrInvalidName)
		}
		key := strings.ToLower(s.checkTo)
		if seen[key] {
			conflicts = append(conflicts, l.sdDisplay(s.step.To))
			continue
		}
		seen[key] = true
		ti, err := os.Lstat(s.checkTo)
		if err != nil {
			continue
		}
		fi, ferr := os.Lstat(s.checkFrom)
		if ferr != nil || !os.SameFile(fi, ti) {
			conflicts = append(conflicts, l.sdDisplay(s.step.To))
		}
	}
	// Another game of the tag under the new name would share the save and
	// the states (NextUI names them after the ROM file name only).
	if !strings.EqualFold(newROM, g.ROMName) && l.adapter.GameExists(g.System.Tag, newROM) {
		conflicts = append(conflicts, l.sdDisplay(newMain))
	}
	if len(conflicts) > 0 {
		return RenamePlan{}, &RenameConflictError{Conflicts: conflicts}
	}
	for _, s := range steps {
		plan.Steps = append(plan.Steps, s.step)
		plan.Renamed = append(plan.Renamed, Renamed{From: l.sdDisplay(s.checkFrom), To: l.sdDisplay(s.step.To)})
	}
	return plan, nil
}

// sdDisplay is the informative name of a path: "ReminisLink/..." in the
// data folder, else relative to the SD card, never absolute.
func (l *Library) sdDisplay(p string) string {
	if rel, err := filepath.Rel(l.layout.Root, p); err == nil && filepath.IsLocal(rel) {
		return "ReminisLink/" + filepath.ToSlash(rel)
	}
	if rel, err := filepath.Rel(l.adapter.Roots().SD, p); err == nil && filepath.IsLocal(rel) {
		return filepath.ToSlash(rel)
	}
	return filepath.Base(p)
}
