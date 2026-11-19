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

// relToRoot returns p relative to dir, errOutside when it is not under it.
func relToRoot(dir, p string) (string, error) {
	rel, err := filepath.Rel(dir, p)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return "", errOutside
	}
	return rel, nil
}

// openConfined opens the regular file rel of root (the saves folder) for
// reading. The open goes through root (os.Root), which refuses any escape
// from the saves folder at every component, races with links included.
// Links that stay inside the folder are refused as well: no component may
// be a symbolic link (checked through root too, so a race can at worst
// reach another file of the saves folder). The returned description is
// the one of the opened file.
func openConfined(root *os.Root, rel string) (*os.File, fs.FileInfo, error) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	var lfi fs.FileInfo
	for i := range parts {
		fi, err := root.Lstat(filepath.Join(parts[:i+1]...))
		if err != nil {
			return nil, nil, err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return nil, nil, errOutside
		}
		lfi = fi
	}
	if !lfi.Mode().IsRegular() {
		return nil, nil, errOutside
	}
	f, err := root.Open(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("%w: %w", errOutside, err)
	}
	fi, err := f.Stat()
	if err != nil || !os.SameFile(lfi, fi) || !fi.Mode().IsRegular() {
		f.Close()
		return nil, nil, errOutside // replaced while opened
	}
	return f, fi, nil
}

// openSavesFile opens the file p (an absolute path given by the adapter)
// with openConfined under the saves folder of the adapter.
func (s *Service) openSavesFile(p string) (*os.File, error) {
	dir := s.o.Adapter.Roots().Saves
	rel, err := relToRoot(dir, p)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, _, err := openConfined(root, rel)
	return f, err
}
