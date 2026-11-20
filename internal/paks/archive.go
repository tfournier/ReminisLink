package paks

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/tfournier/ReminisLink/internal/store"
)

// Limits of an archive or a folder installed as a pak
// (protocol/openapi.yaml POST /paks/install).
const (
	// MaxEntries bounds the entries of an archive (files and folders).
	MaxEntries = 10000
	// MaxUncompressedBytes bounds the size of a pak, announced or real.
	MaxUncompressedBytes = 1 << 30
	// MaxArchiveBytes bounds an archive sent as a pak.
	MaxArchiveBytes = 1 << 30
	// MaxRatio bounds the compression ratio of an entry larger than
	// RatioMinBytes (zip bombs).
	MaxRatio = 200
	// RatioMinBytes: smaller entries are not checked for their ratio.
	RatioMinBytes = 1 << 20
	// MaxComponentBytes and MaxPathBytes bound the names of the entries.
	MaxComponentBytes = 255
	MaxPathBytes      = 1024
)

// FormatError is a refused archive or folder (422 unsupported_format);
// its text, in English, says why (never a path of the console).
type FormatError struct{ Reason string }

func (e *FormatError) Error() string { return e.Reason }

func refuse(format string, args ...any) error {
	return &FormatError{Reason: fmt.Sprintf(format, args...)}
}

// IsFormatError reports whether err is a FormatError, and returns it.
func IsFormatError(err error) (*FormatError, bool) {
	var fe *FormatError
	ok := errors.As(err, &fe)
	return fe, ok
}

// SpaceFunc checks that need bytes can be written to the staging area;
// it returns an error (the caller's) when they cannot.
type SpaceFunc func(need int64) error

// Progress reports the bytes written so far out of total.
type Progress func(done, total int64)

// Staged is a pak extracted or copied into a staging folder, checked.
type Staged struct {
	Dir  string // absolute path of the <Name>.pak folder in the staging area
	Name string // folder name, ".pak" included, as in the source
	// Kind is the kind given by the source layout (Tools/ or Emus/ in
	// the archive), "" when it gives none.
	Kind string
}

// macOSFolder is the resource fork folder some zip tools add.
const macOSFolder = "__MACOSX"

// exFATForbidden are the characters exFAT does not allow in a name.
const exFATForbidden = "\"*:<>?\\|"

// checkComponent checks one component of an entry path.
func checkComponent(c string) error {
	switch {
	case c == "":
		return refuse("empty path component")
	case c == "." || c == "..":
		return refuse("dot path component")
	case len(c) > MaxComponentBytes:
		return refuse("name longer than %d bytes", MaxComponentBytes)
	case strings.ContainsAny(c, exFATForbidden):
		return refuse("name with a character the SD card does not allow")
	}
	return nil
}

// checkPath checks the path of an entry: relative, "/"-separated, valid
// UTF-8, no control character, no empty, "." or ".." component.
func checkPath(name string) error {
	switch {
	case name == "":
		return refuse("empty entry name")
	case len(name) > MaxPathBytes:
		return refuse("entry path longer than %d bytes", MaxPathBytes)
	case !utf8.ValidString(name):
		return refuse("entry name is not valid UTF-8")
	case strings.ContainsRune(name, '\\'):
		return refuse("entry name with a backslash")
	case strings.HasPrefix(name, "/"):
		return refuse("absolute entry path")
	case len(name) >= 2 && name[1] == ':':
		return refuse("absolute entry path")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return refuse("entry name with a control character")
		}
	}
	for _, c := range strings.Split(name, "/") {
		if err := checkComponent(c); err != nil {
			return err
		}
	}
	return nil
}

// entry is a checked entry of the pak folder.
type entry struct {
	file *zip.File
	rel  string // path inside the pak folder, "/" separator
	dir  bool
}

// plan is a checked archive: its pak folder and what to extract.
type plan struct {
	name    string // pak folder name
	kind    string
	entries []entry
	total   int64 // announced uncompressed bytes
}

// item is an entry of the archive whose name and type were checked.
type item struct {
	f     *zip.File
	comps []string
	dir   bool
}

// planArchive checks every entry of an archive and finds its single pak
// folder (see protocol/openapi.yaml POST /paks/install): a <Name>.pak
// folder (at the top or under Tools|Emus/<platform>/), or, for a "loose"
// archive, the files at its top with a launch.sh among them (p.name is
// then empty: the caller names it).
func planArchive(zr *zip.Reader, platform string) (plan, error) {
	var p plan
	if len(zr.File) > MaxEntries {
		return p, refuse("more than %d entries", MaxEntries)
	}
	// First pass: names, types, explicit duplicates (a folder too).
	var items []item
	explicit := map[string]bool{}
	looseLaunch := false
	for _, f := range zr.File {
		name := f.Name
		if strings.ContainsRune(name, 0) {
			return p, refuse("entry name with a NUL byte")
		}
		mode := f.Mode()
		isDir := strings.HasSuffix(name, "/") || mode.IsDir()
		name = strings.TrimSuffix(name, "/")
		if err := checkPath(name); err != nil {
			return p, err
		}
		switch {
		case mode&fs.ModeSymlink != 0:
			return p, refuse("symbolic link in the archive")
		case !isDir && !mode.IsRegular():
			return p, refuse("entry that is neither a file nor a folder")
		case isDir && mode.Type() != 0 && !mode.IsDir():
			return p, refuse("entry that is neither a file nor a folder")
		case f.Flags&0x1 != 0:
			return p, refuse("encrypted archive")
		}
		comps := strings.Split(name, "/")
		if strings.EqualFold(comps[0], macOSFolder) {
			continue
		}
		key := strings.ToLower(name)
		if explicit[key] {
			return p, refuse("two entries with the same name (ignoring case)")
		}
		explicit[key] = true
		if len(comps) == 1 && !isDir && strings.EqualFold(comps[0], LaunchScript) {
			looseLaunch = true
		}
		items = append(items, item{f: f, comps: comps, dir: isDir})
	}

	rootKey := ""
	// kinds of the paths seen (lowercase, inside the pak folder): a path
	// can be a file or a folder, never both, never twice a file.
	seen := map[string]bool{} // true: folder
	var total uint64
	for _, it := range items {
		comps, isDir := it.comps, it.dir
		var root []string
		kind := ""
		switch {
		case looseLaunch:
			root = nil // every entry belongs to the pak
		case HasSuffix(comps[0]):
			root = comps[:1]
		case strings.EqualFold(comps[0], "Tools") || strings.EqualFold(comps[0], "Emus"):
			if len(comps) == 1 {
				if !isDir {
					return p, refuse("entry outside the pak folder")
				}
				continue
			}
			if !strings.EqualFold(comps[1], platform) {
				continue // a pak for another platform
			}
			if len(comps) == 2 {
				if !isDir {
					return p, refuse("entry outside the pak folder")
				}
				continue
			}
			if !HasSuffix(comps[2]) {
				return p, refuse("entry outside the pak folder")
			}
			root = comps[:3]
			kind = KindTool
			if strings.EqualFold(comps[0], "Emus") {
				kind = KindEmu
			}
		case len(comps) == 1 && !isDir:
			continue // a file next to the pak folder (README...)
		default:
			return p, refuse("entry outside the pak folder")
		}
		if !looseLaunch {
			if len(comps) == len(root) && !isDir {
				return p, refuse("the pak folder is a file")
			}
			key := strings.ToLower(strings.Join(root, "/"))
			if rootKey == "" {
				rootKey, p.name, p.kind = key, root[len(root)-1], kind
			} else if key != rootKey {
				return p, refuse("more than one pak in the archive")
			}
		}
		rel := comps[len(root):]
		if len(rel) == 0 {
			continue // the pak folder itself
		}
		// Every parent is a folder; the entry is what it says.
		for i := 1; i <= len(rel); i++ {
			k := strings.ToLower(strings.Join(rel[:i], "/"))
			wantDir := i < len(rel) || isDir
			if was, ok := seen[k]; ok {
				if was != wantDir || (!wantDir && i == len(rel)) {
					return p, refuse("two entries with the same name (ignoring case)")
				}
				continue
			}
			seen[k] = wantDir
		}
		f := it.f
		if !isDir {
			if f.UncompressedSize64 > RatioMinBytes && f.CompressedSize64*MaxRatio < f.UncompressedSize64 {
				return p, refuse("entry compressed more than %d times", MaxRatio)
			}
			if total += f.UncompressedSize64; total > MaxUncompressedBytes || f.UncompressedSize64 > MaxUncompressedBytes {
				return p, refuse("more than %d bytes uncompressed", MaxUncompressedBytes)
			}
			if f.Method != zip.Store && f.Method != zip.Deflate {
				return p, refuse("unsupported compression method")
			}
		}
		p.entries = append(p.entries, entry{file: f, rel: strings.Join(rel, "/"), dir: isDir})
	}
	if !looseLaunch {
		if rootKey == "" {
			return p, refuse("no .pak folder in the archive")
		}
		if strings.HasPrefix(p.name, ".") || DisplayName(p.name) == "" {
			return p, refuse("invalid pak folder name")
		}
	}
	if isDir, ok := seen[LaunchScript]; !ok || isDir {
		return p, refuse("no launch.sh in the pak folder")
	}
	p.total = int64(total)
	return p, nil
}

// ExtractOptions configure ExtractZip.
type ExtractOptions struct {
	Platform string // platform folder of Tools|Emus/<platform>/ (tg5040)
	// Name names a loose archive (launch.sh at its top) when its pak.json
	// has no usable name: the file name of the archive (".zip" dropped,
	// ".pak" added).
	Name string
	// Clean checks and cleans a pak name (transfer.CleanFileName); nil:
	// only the checks of the archive entries.
	Clean    func(string) (string, error)
	Space    SpaceFunc
	Progress Progress
}

// looseName names the pak of a loose archive: the name of its pak.json
// when usable, else o.Name, without ".zip", with ".pak".
func looseName(p plan, o ExtractOptions) (string, error) {
	// archive: the name is the file name of the archive (".zip" dropped);
	// a pak.json name is used as it is.
	clean := func(n string, archive bool) string {
		n = strings.TrimSpace(n)
		if archive && len(n) > 4 && strings.EqualFold(n[len(n)-4:], ".zip") {
			n = n[:len(n)-4]
		}
		if !HasSuffix(n) {
			n += Suffix
		}
		if o.Clean != nil {
			c, err := o.Clean(n)
			if err != nil {
				return ""
			}
			n = c
		}
		if checkComponent(n) != nil || strings.HasPrefix(n, ".") || !HasSuffix(n) || DisplayName(n) == "" {
			return ""
		}
		return n
	}
	for _, e := range p.entries {
		if !e.dir && strings.EqualFold(e.rel, MetadataFile) && e.file.UncompressedSize64 <= maxMetadataBytes {
			if name := metadataName(e.file); name != "" {
				if n := clean(name, false); n != "" {
					return n, nil
				}
			}
		}
	}
	if n := clean(o.Name, true); n != "" {
		return n, nil
	}
	return "", refuse("cannot name the pak (no name in pak.json nor a usable archive name)")
}

// metadataName reads the name of a pak.json entry ("" when unreadable).
func metadataName(f *zip.File) string {
	rc, err := f.Open()
	if err != nil {
		return ""
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxMetadataBytes+1))
	if err != nil || len(data) > maxMetadataBytes {
		return ""
	}
	var md struct {
		Name any `json:"name"`
	}
	if json.Unmarshal(data, &md) != nil {
		return ""
	}
	name, _ := md.Name.(string)
	if strings.ContainsAny(name, "/\\") {
		return ""
	}
	return name
}

// ExtractZip checks the zip archive r and extracts its pak folder into
// dest (an empty staging folder). The central directory is bounded
// before it is read (CheckDirectory); nothing is written before every
// entry was checked and o.Space approved the announced size; the real
// sizes are checked again while extracting, and every file and folder
// written is synced (an error fails the extraction). Refusals are
// FormatErrors.
func ExtractZip(ctx context.Context, r io.ReaderAt, size int64, dest string, o ExtractOptions) (Staged, error) {
	if err := CheckDirectory(r, size); err != nil {
		return Staged{}, err
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return Staged{}, refuse("not a zip archive")
	}
	p, err := planArchive(zr, o.Platform)
	if err != nil {
		return Staged{}, err
	}
	if p.name == "" {
		if p.name, err = looseName(p, o); err != nil {
			return Staged{}, err
		}
	}
	if o.Space != nil {
		if err := o.Space(p.total); err != nil {
			return Staged{}, err
		}
	}
	target := filepath.Join(dest, p.name)
	if err := os.Mkdir(target, 0o755); err != nil {
		return Staged{}, err
	}
	dirs := map[string]bool{target: true}
	var done int64
	for _, e := range p.entries {
		if err := ctx.Err(); err != nil {
			return Staged{}, err
		}
		path := filepath.Join(target, filepath.FromSlash(e.rel))
		if rel, err := filepath.Rel(target, path); err != nil || !filepath.IsLocal(rel) {
			return Staged{}, refuse("entry outside the pak folder") // never reached: checked by planArchive
		}
		dir := path
		if !e.dir {
			dir = filepath.Dir(path)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Staged{}, err
		}
		for d := dir; d != target && !dirs[d]; d = filepath.Dir(d) {
			dirs[d] = true
		}
		if e.dir {
			continue
		}
		n, err := extractFile(ctx, e.file, path, MaxUncompressedBytes-done)
		done += n
		if err != nil {
			return Staged{}, err
		}
		if o.Progress != nil {
			o.Progress(done, p.total)
		}
	}
	if err := syncTree(target, dirs); err != nil {
		return Staged{}, err
	}
	return Staged{Dir: target, Name: p.name, Kind: p.kind}, nil
}

// extractFile writes one entry to path (created, never replaced), synced,
// and returns the bytes written. budget bounds the bytes of the whole pak.
func extractFile(ctx context.Context, f *zip.File, path string, budget int64) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, refuse("unreadable entry")
	}
	defer rc.Close()
	perm := os.FileMode(0o644)
	if f.Mode()&0o111 != 0 {
		perm = 0o755
	}
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return 0, refuse("two entries with the same name (ignoring case)")
		}
		return 0, err
	}
	limit := min(int64(f.UncompressedSize64), budget)
	n, err := io.Copy(errWriter{out}, io.LimitReader(ctxReader{ctx, rc}, limit+1))
	if err == nil && n > limit {
		err = refuse("entry larger than announced, or more than %d bytes uncompressed", MaxUncompressedBytes)
	}
	if err != nil {
		out.Close()
		if _, ok := IsFormatError(err); !ok && ctx.Err() == nil && !isWriteErr(err) {
			err = refuse("corrupt archive")
		}
		return n, err
	}
	if n != int64(f.UncompressedSize64) {
		out.Close()
		return n, refuse("corrupt archive")
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return n, err
	}
	if err := out.Close(); err != nil {
		return n, err
	}
	if !f.Modified.IsZero() {
		_ = os.Chtimes(path, f.Modified, f.Modified)
	}
	return n, nil
}

// writeErr marks an error of the file being written (card full, I/O),
// not of the archive.
type writeErr struct{ error }

func (w writeErr) Unwrap() error { return w.error }

// errWriter marks the errors of w as writeErr.
type errWriter struct{ w io.Writer }

func (e errWriter) Write(p []byte) (int, error) {
	n, err := e.w.Write(p)
	if err != nil {
		err = writeErr{err}
	}
	return n, err
}

func isWriteErr(err error) bool {
	var w writeErr
	return errors.As(err, &w) || errors.Is(err, fs.ErrPermission)
}

// ctxReader stops a read when ctx is done.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// syncTree syncs every folder written, deepest first, then the staging
// folder holding the pak, and returns the first error.
func syncTree(target string, dirs map[string]bool) error {
	list := make([]string, 0, len(dirs))
	for d := range dirs {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool { return len(list[i]) > len(list[j]) })
	for _, d := range list {
		if err := syncDir(d); err != nil {
			return err
		}
	}
	return syncDir(filepath.Dir(target))
}

// syncDir is store.SyncDirChecked; tests replace it.
var syncDir = store.SyncDirChecked
