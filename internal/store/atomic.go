// Package store holds the ReminisLink data kept on the SD card (backups,
// locks, clients, configuration) and the helpers to write it safely.
package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileAtomic replaces path with data: it writes <path>.tmp, fsyncs it,
// then renames it over path, so a reader sees either the old or the new
// content, never a partial file. The parent directory is created if needed.
//
// perm is only used when the file is created; the SD card is exFAT and has
// no permissions, so callers must not rely on it.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("sync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename %s: %w", tmp, err)
	}
	syncDir(dir)
	return nil
}

// syncDir makes the rename durable where the file system supports it.
// Errors are ignored: some file systems (and macOS for directories) refuse it.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
