// Package store holds the ReminisLink data kept on the SD card (backups,
// locks, clients, configuration) and the helpers to write it safely.
package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// WriteFileAtomic replaces path with data: it writes <path>.tmp, fsyncs it,
// then renames it over path, so a reader sees either the old or the new
// content, never a partial file. The parent directory is created if needed.
//
// perm is only used when the file is created; the SD card is exFAT and has
// no permissions, so callers must not rely on it.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	return writeFileAtomic(path, data, perm, func(dir string) error { SyncDir(dir); return nil })
}

// writeFileAtomic is WriteFileAtomic with the sync of the folder given:
// its error is returned (the rename may not be durable yet).
func writeFileAtomic(path string, data []byte, perm os.FileMode, syncDir func(string) error) error {
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
	return syncDir(dir)
}

// SyncDir makes the rename durable where the file system supports it.
// Errors are ignored: some file systems (and macOS for directories) refuse it.
// Where durability matters (the trash), use SyncDirChecked.
func SyncDir(dir string) {
	_ = SyncDirChecked(dir)
}

// SyncDirChecked syncs a directory and reports a failure, except a refusal
// of the operation itself by the file system (EINVAL, ENOTSUP, ENOTTY:
// some file systems, and macOS for directories, cannot sync a directory;
// nothing more can be done there).
func SyncDirChecked(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("sync %s: %w", dir, err)
	}
	err = d.Sync()
	cerr := d.Close()
	if err != nil && !syncUnsupported(err) {
		return fmt.Errorf("sync %s: %w", dir, err)
	}
	if cerr != nil {
		return fmt.Errorf("sync %s: %w", dir, cerr)
	}
	return nil
}

func syncUnsupported(err error) bool {
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) ||
		errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.ENOTTY)
}
