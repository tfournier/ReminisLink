// Package states manages the emulator save states of the games. Milestone
// M4 only installs a received state (the previous one goes to the trash)
// and tells whether a slot is locked; locks, restores and the resume
// history come with milestone M5.
package states

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/store"
)

// MaxSlot is the largest slot number accepted. It matches the state file
// names the NextUI adapter recognizes (".st" and one or two digits).
// TODO(M0): number of slots of NextUI not yet checked.
const MaxSlot = 99

// Errors of the package.
var (
	ErrLocked      = errors.New("state slot is locked")
	ErrInvalidSlot = errors.New("invalid state slot")
)

// Slot is a state slot (Slot in protocol/openapi.yaml).
type Slot struct {
	Slot         int       `json:"slot"`
	SizeBytes    int64     `json:"sizeBytes"`
	ModifiedAt   time.Time `json:"modifiedAt"`
	HasThumbnail bool      `json:"hasThumbnail"`
	Locked       bool      `json:"locked"`
	LockStatus   *string   `json:"lockStatus"`
}

// ValidSlot reports whether slot is a slot number the pak accepts.
func ValidSlot(slot int) bool { return slot >= 0 && slot <= MaxSlot }

// lockFileName is the copy of a locked state in its lock folder.
func lockFileName(slot int) string { return "slot" + strconv.Itoa(slot) + ".st" }

// IsLocked reports whether the slot of g is locked: its copy exists in
// locks/<TAG>/<ROM file name>/slot<N>.st (names compared ignoring case, as
// on exFAT).
func IsLocked(layout store.Layout, g osadapter.Game, slot int) bool {
	dir := findFold(layout.LocksDir(g.System.Tag), g.ROMName)
	if dir == "" {
		return false
	}
	return findFold(dir, lockFileName(slot)) != ""
}

// findFold returns the path of the entry of dir named name ignoring case,
// or "".
func findFold(dir, name string) string {
	des, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	match := ""
	for _, d := range des {
		if d.Name() == name {
			return filepath.Join(dir, name)
		}
		if match == "" && strings.EqualFold(d.Name(), name) {
			match = filepath.Join(dir, d.Name())
		}
	}
	return match
}

// Install makes the file at tmp the state of a slot of g: refused when the
// slot is locked (ErrLocked); the current state of the slot, if any, is
// first moved to the trash, then tmp is renamed to the state path given by
// the adapter. tmp must be on the same file system and already synced.
func Install(a osadapter.Adapter, trash *store.Trash, layout store.Layout, g osadapter.Game, slot int, tmp string) (Slot, error) {
	if !ValidSlot(slot) {
		return Slot{}, ErrInvalidSlot
	}
	if IsLocked(layout, g, slot) {
		return Slot{}, ErrLocked
	}
	files, err := a.GameFiles(g)
	if err != nil {
		return Slot{}, fmt.Errorf("state: %w", err)
	}
	dst := a.StatePath(g, slot)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return Slot{}, fmt.Errorf("state: %w", err)
	}
	var trashID string
	for _, s := range files.States {
		if s.Slot != slot {
			continue
		}
		res, err := trash.Move(store.ReasonOverwrite, g.Name, []store.MoveItem{{Path: s.Path}})
		if err != nil {
			return Slot{}, fmt.Errorf("state: %w", err)
		}
		trashID = res.Entry.ID
	}
	if err := os.Rename(tmp, dst); err != nil {
		if trashID != "" {
			return Slot{}, fmt.Errorf("state: install (previous state in trash entry %s): %w", trashID, err)
		}
		return Slot{}, fmt.Errorf("state: install: %w", err)
	}
	store.SyncDir(filepath.Dir(dst))
	store.SyncDir(filepath.Dir(tmp))
	fi, err := os.Stat(dst)
	if err != nil {
		return Slot{}, fmt.Errorf("state: %w", err)
	}
	out := Slot{Slot: slot, SizeBytes: fi.Size(), ModifiedAt: fi.ModTime().UTC().Truncate(time.Second)}
	if p := a.StateThumbnailPath(g, slot); p != "" {
		if _, err := os.Stat(p); err == nil {
			out.HasThumbnail = true
		}
	}
	return out, nil
}
