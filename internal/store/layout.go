package store

import "path/filepath"

// Layout gives the paths of the ReminisLink data folder
// (/.userdata/shared/ReminisLink/, docs/specs/05-console-pak.md §4).
type Layout struct {
	Root string
}

// ConfigFile is config.json.
func (l Layout) ConfigFile() string { return filepath.Join(l.Root, "config.json") }

// ClientsFile is clients.json.
func (l Layout) ClientsFile() string { return filepath.Join(l.Root, "clients.json") }

// LocksDir is the folder of the state locks of a tag: one folder per ROM
// file name, holding slot<N>.st and slot<N>.json.
func (l Layout) LocksDir(tag string) string { return filepath.Join(l.Root, "locks", tag) }
