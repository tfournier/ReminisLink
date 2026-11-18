// Package saves manages the in-game saves of the games. Milestone M4 only
// installs a received save (the previous one goes to the trash); the named
// backup copies come with milestone M5.
package saves

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/store"
)

// Backup is a save backup copy (Backup in protocol/openapi.yaml).
type Backup struct {
	ID        string    `json:"id"`
	Label     *string   `json:"label"`
	CreatedAt time.Time `json:"createdAt"`
	SizeBytes int64     `json:"sizeBytes"`
	Reason    string    `json:"reason"`
}

// Install makes the file at tmp the save of g: the current save, if any,
// is first moved to the trash, then tmp is renamed to the save path given
// by the adapter. tmp must be on the same file system (the ReminisLink
// folder of the SD card) and already synced.
//
// It returns the backup copy created before the replacement: always nil
// in M4 (the previous save is kept in the trash instead).
// TODO(M5): create the "before-replace" backup copy (docs/specs/04-api-v1.md).
func Install(a osadapter.Adapter, trash *store.Trash, g osadapter.Game, tmp string) (*Backup, error) {
	files, err := a.GameFiles(g)
	if err != nil {
		return nil, fmt.Errorf("save: %w", err)
	}
	dst := a.SavePath(g)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, fmt.Errorf("save: %w", err)
	}
	var moved *store.MoveResult
	if files.Save != nil {
		res, err := trash.Move(store.ReasonOverwrite, g.Name, []store.MoveItem{{Path: files.Save.Path}})
		if err != nil {
			return nil, fmt.Errorf("save: %w", err)
		}
		moved = &res
	}
	if err := os.Rename(tmp, dst); err != nil {
		if moved != nil {
			return nil, errors.Join(fmt.Errorf("save: install: %w", err),
				fmt.Errorf("save: the previous save is in trash entry %s", moved.Entry.ID))
		}
		return nil, fmt.Errorf("save: install: %w", err)
	}
	store.SyncDir(filepath.Dir(dst))
	store.SyncDir(filepath.Dir(tmp))
	return nil, nil
}
