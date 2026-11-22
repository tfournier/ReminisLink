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

// BackupsDir is the folder of the backups of a tag: one folder per ROM
// file name, holding saves/ and resume/.
func (l Layout) BackupsDir(tag string) string { return filepath.Join(l.Root, "backups", tag) }

// TrashDir is the trash: one folder per trash entry.
func (l Layout) TrashDir() string { return filepath.Join(l.Root, "trash") }

// UploadsDir holds the temporary files of the uploads and patches.
func (l Layout) UploadsDir() string { return filepath.Join(l.Root, "uploads") }

// StagingDir holds the patches and .cht files waiting to be used.
func (l Layout) StagingDir() string { return filepath.Join(l.Root, "staging") }

// PatchesDir holds the patches dropped by the user (listed by /sd/files).
func (l Layout) PatchesDir() string { return filepath.Join(l.Root, "patches") }

// TLSDir holds the certificate of the pak and its private key (M13).
func (l Layout) TLSDir() string { return filepath.Join(l.Root, "tls") }

// RenamesDir holds the journals of the renames in progress (M23): one
// folder per rename, removed once it is done.
func (l Layout) RenamesDir() string { return filepath.Join(l.Root, "renames") }
