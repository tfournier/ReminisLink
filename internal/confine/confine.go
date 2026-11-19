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
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("%w: %w", ErrOutside, err)
	}
	fi, err := f.Stat()
	if err != nil || !os.SameFile(lfi, fi) || !fi.Mode().IsRegular() {
		f.Close()
		return nil, nil, ErrOutside // replaced while opened
	}
	return f, fi, nil
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
