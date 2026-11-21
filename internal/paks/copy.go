package paks

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// CopyFolder checks the pak folder src (a folder of the SD card whose
// name ends with ".pak") and copies it into dest (an empty staging
// folder), with the limits of an archive: no link nor special file at any
// level, MaxEntries entries, MaxUncompressedBytes bytes, a launch.sh at
// its top. Reads go through an os.Root of src, so nothing outside it is
// read even if the folder changes meanwhile. Refusals are FormatErrors.
func CopyFolder(ctx context.Context, src, dest string, space SpaceFunc, progress Progress) (Staged, error) {
	name := filepath.Base(src)
	root, err := openPakFolder(src)
	if err != nil {
		return Staged{}, err
	}
	defer root.Close()
	// First pass: check everything before writing anything.
	items, total, err := scanFolder(ctx, root)
	if err != nil {
		return Staged{}, err
	}
	if space != nil {
		if err := space(total); err != nil {
			return Staged{}, err
		}
	}

	target := filepath.Join(dest, name)
	if err := os.Mkdir(target, 0o755); err != nil {
		return Staged{}, err
	}
	dirs := map[string]bool{target: true}
	var done int64
	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return Staged{}, err
		}
		out := filepath.Join(target, filepath.FromSlash(it.rel))
		if it.dir {
			if err := os.MkdirAll(out, 0o755); err != nil {
				return Staged{}, err
			}
			dirs[out] = true
			continue
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return Staged{}, err
		}
		n, err := copyOne(ctx, root, it.rel, out, it.size)
		done += n
		if err != nil {
			return Staged{}, err
		}
		if progress != nil {
			progress(done, total)
		}
	}
	if err := syncTree(target, dirs); err != nil {
		return Staged{}, err
	}
	return Staged{Dir: target, Name: name}, nil
}

// folderItem is a checked entry of a pak folder.
type folderItem struct {
	rel  string
	dir  bool
	size int64
}

// CheckFolder runs the checks of CopyFolder on the pak folder dir without
// copying it (M18: a pak folder restored from the trash goes through the
// checks of an installation). Refusals are FormatErrors.
func CheckFolder(ctx context.Context, dir string) error {
	root, err := openPakFolder(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	_, _, err = scanFolder(ctx, root)
	return err
}

// openPakFolder checks the name of the pak folder src and opens it as an
// os.Root.
func openPakFolder(src string) (*os.Root, error) {
	name := filepath.Base(src)
	if !HasSuffix(name) || strings.HasPrefix(name, ".") {
		return nil, refuse("not a .pak folder")
	}
	if err := checkComponent(name); err != nil {
		return nil, err
	}
	return os.OpenRoot(src)
}

// scanFolder checks every entry of a pak folder: no link nor special file
// at any level, MaxEntries entries, MaxUncompressedBytes bytes, no two
// names equal ignoring case, a launch.sh at its top.
func scanFolder(ctx context.Context, root *os.Root) ([]folderItem, int64, error) {
	var items []folderItem
	var total int64
	seen := map[string]bool{}
	launch := false
	err := fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		if len(items) >= MaxEntries {
			return refuse("more than %d entries", MaxEntries)
		}
		if err := checkPath(p); err != nil {
			return err
		}
		t := d.Type()
		switch {
		case t&fs.ModeSymlink != 0:
			return refuse("symbolic link in the pak folder")
		case !t.IsDir() && !t.IsRegular():
			return refuse("entry that is neither a file nor a folder")
		}
		key := strings.ToLower(p)
		if seen[key] {
			return refuse("two entries with the same name (ignoring case)")
		}
		seen[key] = true
		it := folderItem{rel: p, dir: t.IsDir()}
		if !it.dir {
			info, err := d.Info()
			if err != nil {
				return err
			}
			it.size = info.Size()
			if total += it.size; total > MaxUncompressedBytes {
				return refuse("more than %d bytes", MaxUncompressedBytes)
			}
			if strings.EqualFold(p, LaunchScript) {
				launch = true
			}
		}
		items = append(items, it)
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	if !launch {
		return nil, 0, refuse("no launch.sh in the pak folder")
	}
	return items, total, nil
}

// copyOne copies the regular file rel of root to out (created, synced):
// a file that changed since the check (a link, another size) refuses the
// folder.
func copyOne(ctx context.Context, root *os.Root, rel, out string, size int64) (int64, error) {
	lfi, err := root.Lstat(rel)
	if err != nil {
		return 0, err
	}
	if !lfi.Mode().IsRegular() || lfi.Size() != size {
		return 0, refuse("the pak folder changed while it was copied")
	}
	in, err := root.Open(path.Clean(rel))
	if err != nil {
		return 0, err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil || !os.SameFile(lfi, fi) {
		return 0, refuse("the pak folder changed while it was copied")
	}
	w, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, lfi.Mode().Perm()|0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return 0, refuse("two entries with the same name (ignoring case)")
		}
		return 0, err
	}
	n, err := io.Copy(w, io.LimitReader(ctxReader{ctx, in}, size+1))
	if err == nil && n != size {
		err = refuse("the pak folder changed while it was copied")
	}
	if err == nil {
		err = w.Sync()
	}
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		_ = os.Chtimes(out, lfi.ModTime(), lfi.ModTime())
	}
	return n, err
}
