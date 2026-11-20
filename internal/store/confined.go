package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrLinkInPath means a path of a move or an install goes through a
// symbolic link, or leaves the SD card: it is refused (rule 2 of
// CLAUDE.md; the desktop mode runs on file systems with links).
var ErrLinkInPath = errors.New("trash: path outside the SD card or through a link")

// sdRel returns p relative to the SD card (OS separators), or
// ErrLinkInPath when it is not inside it.
func (t *Trash) sdRel(p string) (string, error) {
	sd, err := filepath.Abs(t.sd)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(sd, abs)
	if err != nil || !filepath.IsLocal(rel) {
		return "", ErrLinkInPath
	}
	return rel, nil
}

// checkNoLinks checks every existing component of rel under root: none is
// a symbolic link, all but the last are folders. A missing component
// ends the check (nothing below it exists).
func checkNoLinks(root *os.Root, rel string) error {
	parts := strings.Split(rel, string(filepath.Separator))
	for i := range parts {
		fi, err := root.Lstat(filepath.Join(parts[:i+1]...))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if fi.Mode()&fs.ModeSymlink != 0 || (i < len(parts)-1 && !fi.IsDir()) {
			return ErrLinkInPath
		}
	}
	return nil
}

// renameInSD is the rename of the trash (moves, installs, put backs and
// the recovery at start): src and dst must be inside the SD card, and no
// component of either may be a link; the rename itself goes through an
// os.Root of the card, so even a race cannot reach outside it.
func (t *Trash) renameInSD(src, dst string) error {
	rs, err := t.sdRel(src)
	if err != nil {
		return err
	}
	rd, err := t.sdRel(dst)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(t.sd)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := checkNoLinks(root, rs); err != nil {
		return err
	}
	if err := checkNoLinks(root, rd); err != nil {
		return err
	}
	if err := root.Rename(rs, rd); err != nil {
		return fmt.Errorf("rename: %w", withoutPathErr(err))
	}
	return nil
}

// mkdirInSD creates dir (and its parents) inside the SD card through an
// os.Root of the card, refusing links on the way.
func (t *Trash) mkdirInSD(dir string) error {
	rel, err := t.sdRel(dir)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(t.sd)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := checkNoLinks(root, rel); err != nil {
		return err
	}
	if err := root.MkdirAll(rel, 0o755); err != nil {
		return err
	}
	return checkNoLinks(root, rel)
}
