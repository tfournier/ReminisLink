package saves

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// errOutside means a file is a symbolic link, is reached through one, or is
// not under the saves folder: it is never read (rule 2 of CLAUDE.md).
var errOutside = errors.New("file outside the saves folder or reached through a link")

// openConfined opens the regular file p for reading. p must be under root
// (the saves folder, as the adapter gives it; realRoot is root with its
// links resolved) and no component below root may be a symbolic link,
// even one that stays inside root. The returned description is the one of
// the opened file.
func openConfined(root, realRoot, p string) (*os.File, fs.FileInfo, error) {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, nil, errOutside
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", errOutside, err)
	}
	if dir != filepath.Join(realRoot, filepath.Dir(rel)) {
		return nil, nil, errOutside
	}
	lfi, err := os.Lstat(p)
	if err != nil {
		return nil, nil, err
	}
	if !lfi.Mode().IsRegular() {
		return nil, nil, errOutside
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil || !os.SameFile(lfi, fi) || !fi.Mode().IsRegular() {
		f.Close()
		return nil, nil, errOutside // replaced while opened
	}
	return f, fi, nil
}

// openSavesFile is openConfined under the saves folder of the adapter.
func (s *Service) openSavesFile(p string) (*os.File, error) {
	root := s.o.Adapter.Roots().Saves
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	f, _, err := openConfined(root, realRoot, p)
	return f, err
}
