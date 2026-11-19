package library

import (
	"fmt"
	"os"
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

// DeleteGame moves a game and its data to the trash in one entry
// (docs/specs/04-api-v1.md, DELETE /games/{gameId}): the ROM (the .m3u and
// its discs, or the whole folder of a folder-m3u game), the cheats, the
// art and, unless keepSave, the save, the states, their locks and the
// resume history (ReminisLink copies, moved into the same entry rather
// than erased, so nothing is lost before the trash is purged). With
// keepSave, the save, the states, the locks and the resume history stay.
// Nothing is erased.
func (l *Library) DeleteGame(gameID string, keepSave bool, trash *store.Trash) (DeleteResult, error) {
	g, err := l.game(gameID)
	if err != nil {
		return DeleteResult{}, err
	}
	files, err := l.adapter.GameFiles(g)
	if err != nil {
		return DeleteResult{}, notFound(err, "game")
	}
	var items []store.MoveItem
	if g.Kind == osadapter.KindFolderM3U {
		dir, err := resolveUnder(l.romsRoot(), g.Path)
		if err != nil {
			return DeleteResult{}, err
		}
		items = append(items, store.MoveItem{Path: dir})
	} else {
		for _, f := range files.ROM {
			items = append(items, store.MoveItem{Path: f.Path})
		}
	}
	if files.Cheats != nil {
		items = append(items, store.MoveItem{Path: files.Cheats.Path})
		// The previous version kept by the cheat routes (<file>.cht.bak).
		if bak := store.FindFold(filepath.Dir(files.Cheats.Path), filepath.Base(files.Cheats.Path)+".bak", false); bak != "" {
			items = append(items, store.MoveItem{Path: bak})
		}
	}
	if files.Art != nil {
		items = append(items, store.MoveItem{Path: files.Art.Path})
	}
	if !keepSave {
		if files.Save != nil {
			items = append(items, store.MoveItem{Path: files.Save.Path})
		}
		if files.RTC != nil {
			items = append(items, store.MoveItem{Path: files.RTC.Path})
		}
		// Thumbnails and discs of the states, last slot used (NextUI).
		for _, f := range files.StateSide {
			items = append(items, store.MoveItem{Path: f.Path})
		}
		for _, s := range files.States {
			items = append(items, store.MoveItem{Path: s.Path})
		}
		// The locks and the resume history go with the states; with
		// keepSave they stay with them (docs/specs/04-api-v1.md).
		if dir := findDirFold(l.layout.LocksDir(g.System.Tag), g.ROMName); dir != "" {
			items = append(items, store.MoveItem{Path: dir, Internal: true})
		}
		if romDir := findDirFold(l.layout.BackupsDir(g.System.Tag), g.ROMName); romDir != "" {
			if dir := findDirFold(romDir, "resume"); dir != "" {
				items = append(items, store.MoveItem{Path: dir, Internal: true})
			}
		}
	}
	res, err := trash.Move(store.ReasonDeleteGame, g.Name, items)
	if err != nil {
		return DeleteResult{}, err
	}
	return DeleteResult{TrashEntryID: res.Entry.ID, Moved: res.Moved, MovedBytes: res.MovedBytes}, nil
}

// findDirFold returns the path of the folder of dir named name ignoring
// case (exFAT), or "".
func findDirFold(dir, name string) string {
	des, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	match := ""
	for _, d := range des {
		if !d.IsDir() {
			continue
		}
		if d.Name() == name {
			return filepath.Join(dir, name)
		}
		if match == "" && strings.EqualFold(d.Name(), name) {
			match = filepath.Join(dir, d.Name())
		}
	}
	return match
}
