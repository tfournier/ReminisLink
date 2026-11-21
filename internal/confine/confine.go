// Package confine opens files of the SD card that must stay inside a known
// folder (rule 2 of CLAUDE.md): no symbolic link at any component, no
// escape from the folder, races included.
package confine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ErrOutside means a file is a symbolic link, is reached through one, is
// not a regular file or is not under the expected folder: it is never
// read.
var ErrOutside = errors.New("file outside its folder or reached through a link")

// RelToRoot returns p relative to dir, ErrOutside when it is not under it.
func RelToRoot(dir, p string) (string, error) {
	rel, err := filepath.Rel(dir, p)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return "", ErrOutside
	}
	return rel, nil
}

// Open opens the regular file rel of root for reading. The open goes
// through root (os.Root), which refuses any escape from the folder at
// every component, races with links included. Links that stay inside the
// folder are refused as well: no component may be a symbolic link
// (checked through root too, so a race can at worst reach another file of
// the folder). The returned description is the one of the opened file.
func Open(root *os.Root, rel string) (*os.File, fs.FileInfo, error) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	var lfi fs.FileInfo
	for i := range parts {
		fi, err := root.Lstat(filepath.Join(parts[:i+1]...))
		if err != nil {
			return nil, nil, err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return nil, nil, ErrOutside
		}
		lfi = fi
	}
	if !lfi.Mode().IsRegular() {
		return nil, nil, ErrOutside
	}
	f, err := root.Open(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || isIOError(err) {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("%w: %w", ErrOutside, err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !os.SameFile(lfi, fi) || !fi.Mode().IsRegular() {
		f.Close()
		return nil, nil, ErrOutside // replaced while opened
	}
	return f, fi, nil
}

// isIOError reports whether an open failed because of the card or the
// system (permission, I/O error, no file descriptor left...), not because
// the path left the folder: os.Root reports an escape with an error of
// its own, and a link or a file put in place of a folder since the check
// with ELOOP or ENOTDIR (refusals; EXDEV for a resolution kept beneath
// the folder by the kernel). An I/O error is never a refusal: the
// caller reports it instead of answering "not found".
func isIOError(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno != syscall.ELOOP && errno != syscall.ENOTDIR && errno != syscall.EXDEV
}

// OpenUnder opens the file p (an absolute path) with Open under the
// folder dir. Its errors wrap ErrOutside or fs.ErrNotExist, or are I/O
// errors.
func OpenUnder(dir, p string) (*os.File, fs.FileInfo, error) {
	rel, err := RelToRoot(dir, p)
	if err != nil {
		return nil, nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	return Open(root, rel)
}

// CheckDir returns an error wrapping ErrOutside when dir is not root or
// under it, or when a folder between root (excluded: the card itself may
// be reached through a mount link, as /mnt/SDCARD on h700) and dir
// (included) is a symbolic link or not a folder. Folders that do not exist
// yet end the check: the caller creates them as folders. Called before a
// write into dir, so the write cannot leave root through a link (rule 2;
// a link put in place after the check is a race on a card the user
// controls, not a path from the network).
func CheckDir(root, dir string) error {
	rel, err := filepath.Rel(root, dir)
	if err != nil || (rel != "." && !filepath.IsLocal(rel)) {
		return ErrOutside
	}
	if rel == "." {
		return nil
	}
	p := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		p = filepath.Join(p, part)
		fi, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
			return fmt.Errorf("%w: %s", ErrOutside, filepath.ToSlash(rel))
		}
	}
	return nil
}
