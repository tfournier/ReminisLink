package store

import (
	"encoding/json"
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

// checkTrashDir checks that the trash folder is inside the SD card and
// that no component of its path is a link (a missing one is fine).
func (t *Trash) checkTrashDir() error {
	rel, err := t.sdRel(t.dir)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(t.sd)
	if err != nil {
		return err
	}
	defer root.Close()
	return checkNoLinks(root, rel)
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
	// The folders about to be made, deepest first: their entries in their
	// parents are synced once made, so that a file renamed into them is
	// durable before a journal that names it goes (M19 Codex review: ext4
	// may lose a new folder's entry after a power cut).
	var made []string
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Lstat(d); err == nil || filepath.Dir(d) == d {
			break
		}
		if r, err := t.sdRel(d); err != nil || r == "." {
			break
		}
		made = append(made, d)
	}
	if err := root.MkdirAll(rel, 0o755); err != nil {
		return err
	}
	if err := checkNoLinks(root, rel); err != nil {
		return err
	}
	for _, d := range made {
		if err := t.syncDir(filepath.Dir(d)); err != nil {
			return err
		}
	}
	return nil
}

// errNoReplaceUnsupported: the system cannot rename without replacing in
// one call (renameNoReplaceAt).
var errNoReplaceUnsupported = errors.New("rename without replacing not supported")

// openSD opens the SD card as an os.Root: nothing reached through it can
// leave the card, whatever links a race creates.
func (t *Trash) openSD() (*os.Root, error) { return os.OpenRoot(t.sd) }

// confinedRel returns p relative to the SD card and checks that no
// existing component of it is a link (ErrLinkInPath).
func (t *Trash) confinedRel(root *os.Root, p string) (string, error) {
	rel, err := t.sdRel(p)
	if err != nil {
		return "", err
	}
	if err := checkNoLinks(root, rel); err != nil {
		return "", err
	}
	return rel, nil
}

// readEntry reads the entry.json of an entry folder through the SD card
// root, refusing a link at any level (M18 review: never follow a linked
// journal).
func (t *Trash) readEntry(dir string) (entryFile, error) {
	var e entryFile
	root, err := t.openSD()
	if err != nil {
		return e, err
	}
	defer root.Close()
	rel, err := t.confinedRel(root, filepath.Join(dir, trashEntryFile))
	if err != nil {
		return e, err
	}
	data, err := root.ReadFile(rel)
	if err != nil {
		return e, err
	}
	return parseEntry(data)
}

// writeEntryConfined writes entry.json atomically through the SD card
// root: the temporary file is created exclusively (O_EXCL never follows a
// link), a stale one is removed first, no component is a link; then the
// folder is synced with syncDir (its error is returned).
func (t *Trash) writeEntryConfined(dir string, e entryFile, syncDir func(string) error) error {
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return fmt.Errorf("trash: encode entry: %w", err)
	}
	data = append(data, '\n')
	root, err := t.openSD()
	if err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	defer root.Close()
	rel, err := t.confinedRel(root, filepath.Join(dir, trashEntryFile))
	if err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	tmp := rel + ".tmp"
	if err := checkNoLinks(root, tmp); err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	if err := root.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("trash: %w", withoutPathErr(err))
	}
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("trash: create journal: %w", withoutPathErr(err))
	}
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = root.Remove(tmp)
		return fmt.Errorf("trash: write journal: %w", withoutPathErr(werr))
	}
	if err := root.Rename(tmp, rel); err != nil {
		_ = root.Remove(tmp)
		return fmt.Errorf("trash: rename journal: %w", withoutPathErr(err))
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	return nil
}

// renameNoReplaceInSD renames src to dst inside the SD card (no link on
// either path, through an os.Root) and never replaces dst (M18 review):
//   - renameat2(RENAME_NOREPLACE) on Linux, one call;
//   - otherwise a file is linked to dst (link(2) fails when dst exists)
//     and then unlinked from src, and a folder is renamed onto a folder
//     created exclusively for it (rename(2) only replaces an empty folder,
//     and fails if anything appeared in it);
//   - on a file system with none of these (exFAT drivers without hard
//     links nor RENAME_NOREPLACE), a check then a rename: on the console
//     every write of the SD card is serialized by the pak (write lock,
//     trash lock) and nothing else writes while ReminisLink runs.
func (t *Trash) renameNoReplaceInSD(src, dst string) error {
	root, err := t.openSD()
	if err != nil {
		return err
	}
	defer root.Close()
	rs, err := t.confinedRel(root, src)
	if err != nil {
		return err
	}
	rd, err := t.confinedRel(root, dst)
	if err != nil {
		return err
	}
	fi, err := root.Lstat(rs)
	if err != nil {
		return withoutPathErr(err)
	}
	if _, err := root.Lstat(rd); err == nil {
		return fs.ErrExist
	} else if !errors.Is(err, fs.ErrNotExist) {
		return withoutPathErr(err)
	}
	srcDir, err := root.Open(filepath.Dir(rs))
	if err != nil {
		return withoutPathErr(err)
	}
	defer srcDir.Close()
	dstDir, err := root.Open(filepath.Dir(rd))
	if err != nil {
		return withoutPathErr(err)
	}
	defer dstDir.Close()
	err = renameNoReplaceAt(srcDir, filepath.Base(rs), dstDir, filepath.Base(rd))
	if !errors.Is(err, errNoReplaceUnsupported) {
		return err
	}
	if fi.Mode().IsRegular() {
		switch err := root.Link(rs, rd); {
		case err == nil:
			if err := root.Remove(rs); err != nil {
				// Both names hold the file: drop the new one.
				_ = root.Remove(rd)
				return withoutPathErr(err)
			}
			return nil
		case errors.Is(err, fs.ErrExist):
			return fs.ErrExist
		}
		// No hard links here (exFAT): the serialized fallback below.
	} else if fi.IsDir() {
		switch err := root.Mkdir(rd, 0o755); {
		case err == nil:
			if err := root.Rename(rs, rd); err == nil {
				return nil
			}
			// Not empty any more, or no folder over folder here: drop our
			// placeholder (only removed while empty) and refuse.
			if rmErr := root.Remove(rd); rmErr != nil {
				return fs.ErrExist
			}
		case errors.Is(err, fs.ErrExist):
			return fs.ErrExist
		default:
			return withoutPathErr(err)
		}
	}
	if _, err := root.Lstat(rd); err == nil {
		return fs.ErrExist
	}
	if err := root.Rename(rs, rd); err != nil {
		return withoutPathErr(err)
	}
	return nil
}
