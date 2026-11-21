package library

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/store"
)

// GameByID resolves a gameId into the game it names.
func (l *Library) GameByID(gameID string) (osadapter.Game, error) { return l.game(gameID) }

// FolderDir resolves the destination folder of a new ROM: the root of the
// system systemID, or its subfolder folderID (a plain folder, not a game
// folder). It returns the absolute folder and the system.
func (l *Library) FolderDir(systemID, folderID string) (string, osadapter.System, error) {
	sys, err := l.system(systemID)
	if err != nil {
		return "", osadapter.System{}, err
	}
	rel := sys.Folder
	if folderID != "" {
		if rel, err = l.resolve(folderID); err != nil {
			return "", osadapter.System{}, err
		}
		if !strings.HasPrefix(rel, sys.Folder+"/") {
			return "", osadapter.System{}, fmt.Errorf("%w: folder of another system", ErrInvalidID)
		}
		if _, err := l.adapter.ListFolder(sys, rel); err != nil {
			return "", osadapter.System{}, notFound(err, "folder")
		}
	}
	dir, err := resolveUnder(l.romsRoot(), rel)
	if err != nil {
		return "", osadapter.System{}, err
	}
	return dir, sys, nil
}

// RelToRoms returns the path of p relative to Roms/ ("/" separator), the
// source of a gameId.
func (l *Library) RelToRoms(p string) (string, error) {
	root, err := filepath.Abs(l.romsRoot())
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("%w: outside Roms", ErrInvalidID)
	}
	return filepath.ToSlash(rel), nil
}

// DeleteResult is the answer of DELETE /games/{gameId}.
type DeleteResult struct {
	TrashEntryID string   `json:"trashEntryId"`
	Moved        []string `json:"moved"`
	MovedBytes   int64    `json:"movedBytes"`
}

// selItem is a file or folder of the selection of a deletion.
type selItem struct {
	path     string
	role     string // GameFile role, or "other"
	internal bool   // a ReminisLink copy (lock, resume history)
}

// deletion is what deleting a game moves and keeps.
type deletion struct {
	game  osadapter.Game
	moved []selItem
	kept  []selItem
	roms  map[string]bool // the files of the game itself (adapter's ROMFiles)
}

// selectDeletion is the single selection of DELETE /games/{gameId} and of
// its preview (GET /games/{gameId}/deletion): the ROM (the .m3u and its
// discs, or the whole folder of a folder game), the cheats and their .bak,
// the art and, unless keepSave, the save, its clock file, the states and
// their side files, their locks and the resume history. The discs another
// playlist of the system still lists are kept (Codex review of M4): that
// game stays playable, and the purge never erases a disc it needs.
func (l *Library) selectDeletion(gameID string, keepSave bool) (deletion, error) {
	g, err := l.game(gameID)
	if err != nil {
		return deletion{}, err
	}
	files, err := l.adapter.GameFiles(g)
	if err != nil {
		return deletion{}, notFound(err, "game")
	}
	d := deletion{game: g, roms: l.adapter.ROMFiles(g)}
	move := func(p, role string) { d.moved = append(d.moved, selItem{path: p, role: role}) }
	keep := func(p, role string) { d.kept = append(d.kept, selItem{path: p, role: role}) }
	shared := l.adapter.SharedFiles(g)
	// "rom" for the files of the game itself (its playlist or .cue, the
	// discs listed at any depth and their tracks), "other" for the rest
	// of a folder (a readme), whatever their depth.
	romRole := func(p string) string {
		if d.roms[p] {
			return "rom"
		}
		return "other"
	}
	switch {
	case g.Kind.IsFolder() && len(shared) == 0:
		dir, err := resolveUnder(l.romsRoot(), g.Path)
		if err != nil {
			return deletion{}, err
		}
		move(dir, "rom")
	case g.Kind.IsFolder():
		// Some files of the folder are shared: every other file of the
		// folder, at any depth, moves; the folders stay.
		dir, err := resolveUnder(l.romsRoot(), g.Path)
		if err != nil {
			return deletion{}, err
		}
		_ = filepath.WalkDir(dir, func(p string, de fs.DirEntry, err error) error {
			if err == nil && de.Type().IsRegular() {
				if shared[p] {
					keep(p, romRole(p))
				} else {
					move(p, romRole(p))
				}
			}
			return nil
		})
	default:
		for _, f := range files.ROM {
			if shared[f.Path] {
				keep(f.Path, "rom")
			} else {
				move(f.Path, "rom")
			}
		}
	}
	if files.Cheats != nil {
		move(files.Cheats.Path, "cheats")
		// The previous version kept by the cheat routes (<file>.cht.bak).
		if bak := store.FindFold(filepath.Dir(files.Cheats.Path), filepath.Base(files.Cheats.Path)+".bak", false); bak != "" {
			move(bak, "other")
		}
	}
	if files.Art != nil {
		if shared[files.Art.Path] {
			keep(files.Art.Path, "art")
		} else {
			move(files.Art.Path, "art")
		}
	}
	data := move
	if keepSave {
		data = keep
	}
	if files.Save != nil {
		data(files.Save.Path, "save")
	}
	if files.RTC != nil {
		data(files.RTC.Path, "rtc")
	}
	// Thumbnails and discs of the states, last slot used (NextUI).
	for _, f := range files.StateSide {
		data(f.Path, "other")
	}
	for _, st := range files.States {
		data(st.Path, "state")
	}
	// The locks and the resume history go with the states; with keepSave
	// they stay with them (docs/specs/04-api-v1.md).
	var copies []string
	// Same rule as the states and saves services (store.FindFold): on a
	// case-sensitive file system, the locks of game.gba are not those of
	// Game.gba.
	if dir := store.FindFold(l.layout.LocksDir(g.System.Tag), g.ROMName, true); dir != "" {
		copies = append(copies, dir)
	}
	if romDir := store.FindFold(l.layout.BackupsDir(g.System.Tag), g.ROMName, true); romDir != "" {
		if dir := store.FindFold(romDir, "resume", true); dir != "" {
			copies = append(copies, dir)
		}
	}
	for _, c := range copies {
		it := selItem{path: c, role: "other", internal: true}
		if keepSave {
			d.kept = append(d.kept, it)
		} else {
			d.moved = append(d.moved, it)
		}
	}
	return d, nil
}

// DeleteGame moves a game and its data to the trash in one entry
// (docs/specs/04-api-v1.md, DELETE /games/{gameId}), as selectDeletion
// selects them; the ReminisLink copies go into the same entry rather than
// being erased, so nothing is lost before the trash is purged. Nothing is
// erased.
func (l *Library) DeleteGame(gameID string, keepSave bool, trash *store.Trash) (DeleteResult, error) {
	d, err := l.selectDeletion(gameID, keepSave)
	if err != nil {
		return DeleteResult{}, err
	}
	items := make([]store.MoveItem, 0, len(d.moved))
	for _, it := range d.moved {
		items = append(items, store.MoveItem{Path: it.path, Internal: it.internal})
	}
	res, err := trash.Move(store.ReasonDeleteGame, d.game.Name, items)
	if err != nil {
		return DeleteResult{}, err
	}
	return DeleteResult{TrashEntryID: res.Entry.ID, Moved: res.Moved, MovedBytes: res.MovedBytes}, nil
}

// DeletionItem is a file of GET /games/{gameId}/deletion.
type DeletionItem struct {
	Name string `json:"name"`
	Role string `json:"role"`
	Size int64  `json:"size"`
}

// DeletionPreview is the answer of GET /games/{gameId}/deletion.
type DeletionPreview struct {
	Moved      []DeletionItem `json:"moved"`
	Kept       []DeletionItem `json:"kept"`
	MovedBytes int64          `json:"movedBytes"`
}

// PreviewDeletion returns what DeleteGame would move and keep now (the
// same selection), folders expanded to their files.
func (l *Library) PreviewDeletion(gameID string, keepSave bool) (DeletionPreview, error) {
	d, err := l.selectDeletion(gameID, keepSave)
	if err != nil {
		return DeletionPreview{}, err
	}
	p := DeletionPreview{Moved: []DeletionItem{}, Kept: []DeletionItem{}}
	p.Moved = l.expand(d.game, d.roms, d.moved)
	p.Kept = l.expand(d.game, d.roms, d.kept)
	for _, it := range p.Moved {
		p.MovedBytes += it.Size
	}
	return p, nil
}

// expand lists the regular files of items (folders walked), with their
// display name.
func (l *Library) expand(g osadapter.Game, roms map[string]bool, items []selItem) []DeletionItem {
	out := []DeletionItem{}
	for _, it := range items {
		_ = filepath.WalkDir(it.path, func(p string, de fs.DirEntry, err error) error {
			if err != nil || !de.Type().IsRegular() {
				return nil
			}
			info, err := de.Info()
			if err != nil {
				return nil
			}
			role := it.role
			if role == "rom" && p != it.path && !roms[p] {
				// A file of a moved folder that the game does not list.
				role = "other"
			}
			out = append(out, DeletionItem{Name: l.displayPath(g, p), Role: role, Size: info.Size()})
			return nil
		})
	}
	return out
}

// displayPath is the informative name of a file of a game: relative to
// the system folder under Roms/, "ReminisLink/..." for the copies of the
// pak, relative to the SD card otherwise; never absolute.
func (l *Library) displayPath(g osadapter.Game, p string) string {
	sys := filepath.Join(l.romsRoot(), g.System.Folder)
	if rel, err := filepath.Rel(sys, p); err == nil && filepath.IsLocal(rel) {
		return filepath.ToSlash(rel)
	}
	if rel, err := filepath.Rel(l.layout.Root, p); err == nil && filepath.IsLocal(rel) {
		return "ReminisLink/" + filepath.ToSlash(rel)
	}
	if rel, err := filepath.Rel(l.adapter.Roots().SD, p); err == nil && filepath.IsLocal(rel) {
		return filepath.ToSlash(rel)
	}
	return filepath.Base(p)
}

// GameSummary returns a game as GET /systems/{systemId}/games lists it.
func (l *Library) GameSummary(gameID string) (Game, error) {
	g, err := l.game(gameID)
	if err != nil {
		return Game{}, err
	}
	folderID := ""
	if parent := path.Dir(g.Path); parent != g.System.Folder {
		folderID = EncodeID(parent)
	}
	listing, err := l.ListGames(EncodeID(g.System.Folder), folderID)
	if err != nil {
		return Game{}, err
	}
	id := EncodeID(g.Path)
	for _, x := range listing.Games {
		if x.ID == id {
			return x, nil
		}
	}
	return Game{}, fmt.Errorf("%w: game", ErrNotFound)
}

// SetArt makes the synced PNG file tmp the art of a game (PUT
// /games/{gameId}/art), where NextUI reads it (the adapter's ArtPath). An
// existing art first goes to the trash (reason overwrite), as one
// journaled replacement. The caller holds the install lock.
func (l *Library) SetArt(gameID, tmp string, trash *store.Trash) (Game, error) {
	g, err := l.game(gameID)
	if err != nil {
		return Game{}, err
	}
	files, err := l.adapter.GameFiles(g)
	if err != nil {
		return Game{}, notFound(err, "game")
	}
	fi, err := os.Lstat(tmp)
	if err != nil {
		return Game{}, err
	}
	dst := l.adapter.ArtPath(g)
	var items []store.MoveItem
	if files.Art != nil {
		items = append(items, store.MoveItem{Path: files.Art.Path})
		dst = files.Art.Path // the existing spelling (exFAT ignores case)
	}
	if err := l.confined(dst); err != nil {
		return Game{}, err
	}
	if _, err := trash.Replace(store.ReasonOverwrite, g.Name, items, []store.Install{{Src: tmp, Dst: dst, Size: fi.Size()}}); err != nil {
		return Game{}, fmt.Errorf("set art: %w", err)
	}
	return l.GameSummary(gameID)
}

// DeleteArt moves the art of a game to the trash (DELETE
// /games/{gameId}/art); ErrNotFound when it has none. The caller holds
// the install lock.
func (l *Library) DeleteArt(gameID string, trash *store.Trash) (Game, error) {
	g, err := l.game(gameID)
	if err != nil {
		return Game{}, err
	}
	files, err := l.adapter.GameFiles(g)
	if err != nil {
		return Game{}, notFound(err, "game")
	}
	if files.Art == nil {
		return Game{}, fmt.Errorf("%w: art", ErrNotFound)
	}
	if err := l.confined(files.Art.Path); err != nil {
		return Game{}, err
	}
	if _, err := trash.Move(store.ReasonOverwrite, g.Name, []store.MoveItem{{Path: files.Art.Path}}); err != nil {
		return Game{}, fmt.Errorf("delete art: %w", err)
	}
	return l.GameSummary(gameID)
}

// confined checks that an absolute path the pak writes in Roms/ (the art
// of a game) stays inside it once symbolic links are resolved, as the
// ROMs do: a .media folder linked elsewhere (desktop mode, a card that is
// not exFAT) is refused with ErrInvalidID (Codex review of M8).
func (l *Library) confined(p string) error {
	rel, err := l.RelToRoms(p)
	if err != nil {
		return err
	}
	_, err = resolveUnder(l.romsRoot(), rel)
	return err
}
