// Package screenshots lists, serves, previews and moves to the trash the
// screenshots taken on the console (M22, protocol/openapi.yaml tag
// Screenshots). The folder comes from the OS adapter
// (osadapter.Roots.Screenshots); only the files directly in it are
// screenshots, never through a link (rule 2), and a screenshot is named
// on the network by an opaque identifier resolved to its exact name in
// that folder.
package screenshots

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tfournier/ReminisLink/internal/confine"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/store"
)

// Limits of the API.
const (
	// IDPrefix starts every screenshotId: IDPrefix + b64u(file name).
	IDPrefix = "sc_"
	// MaxList is the most screenshots GET /screenshots lists (the newest).
	MaxList = 5000
	// MaxTrash is the most identifiers of one POST /screenshots/trash.
	MaxTrash = 500
)

// Formats of a screenshot (Screenshot.format), from its extension.
const (
	FormatPNG  = "png"
	FormatJPEG = "jpeg"
	FormatBMP  = "bmp"
)

// Errors of the service.
var (
	// ErrInvalidID: an identifier the pak never produces (400 invalid_id).
	ErrInvalidID = errors.New("screenshots: invalid id")
	// ErrNotFound: no such screenshot in the folder (404 not_found).
	ErrNotFound = errors.New("screenshots: not found")
)

// MissingError lists the identifiers of a POST /screenshots/trash that
// name no screenshot (wraps ErrNotFound): nothing was moved.
type MissingError struct{ IDs []string }

func (e *MissingError) Error() string {
	return ErrNotFound.Error() + ": " + strings.Join(e.IDs, ", ")
}
func (e *MissingError) Unwrap() error { return ErrNotFound }

// Screenshot is a screenshot of GET /screenshots.
type Screenshot struct {
	ID           string    `json:"screenshotId"`
	Name         string    `json:"name"`
	TakenAt      time.Time `json:"takenAt"`
	SizeBytes    int64     `json:"sizeBytes"`
	Format       string    `json:"format"`
	GameName     *string   `json:"gameName"`
	HasThumbnail bool      `json:"hasThumbnail"`
}

// List is the answer of GET /screenshots.
type List struct {
	Screenshots []Screenshot `json:"screenshots"`
	TotalBytes  int64        `json:"totalBytes"`
	Truncated   bool         `json:"truncated"`
}

// Options are the dependencies of the service.
type Options struct {
	Adapter osadapter.Adapter
	Trash   *store.Trash
	// Lock is the SD card lock shared with the other writers of the
	// card (saves, states, transfers): held while screenshots move to the
	// trash.
	Lock   sync.Locker
	Logger *slog.Logger
}

// Service serves the screenshots. Create it with New.
type Service struct {
	o      Options
	thumbs *thumbCache
	// decode bounds the previews made at the same time (memory and CPU
	// of the console).
	decode chan struct{}
}

// New returns the service.
func New(o Options) (*Service, error) {
	if o.Adapter == nil || o.Trash == nil {
		return nil, errors.New("screenshots: missing dependency")
	}
	if o.Lock == nil {
		o.Lock = &sync.Mutex{}
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Service{o: o, thumbs: newThumbCache(thumbCacheEntries, thumbCacheBytes), decode: make(chan struct{}, maxDecodes)}, nil
}

// ID is the identifier of the screenshot file name.
func ID(name string) string { return IDPrefix + library.EncodeID(name) }

// nameOf decodes an identifier into a file name of the folder, checked
// like any identifier (no "/", "..", control character, invalid UTF-8...)
// and a name the list shows.
func nameOf(id string) (string, error) {
	rest, ok := strings.CutPrefix(id, IDPrefix)
	if !ok {
		return "", ErrInvalidID
	}
	name, err := library.DecodeID(rest)
	if err != nil || strings.Contains(name, "/") || !listable(name) {
		return "", ErrInvalidID
	}
	return name, nil
}

// formatOf is the format of a file name of the folder ("" when the list
// does not show it).
func formatOf(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return FormatPNG
	case ".jpg", ".jpeg":
		return FormatJPEG
	case ".bmp":
		return FormatBMP
	}
	return ""
}

// listable reports whether a file name of the folder is a screenshot of
// the list: an image extension, not hidden, with an identifier the pak
// can resolve back (library.DecodeID refuses control characters and
// invalid UTF-8).
func listable(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") || formatOf(name) == "" {
		return false
	}
	back, err := library.DecodeID(library.EncodeID(name))
	return err == nil && back == name && !strings.Contains(name, "/")
}

// folder is the screenshots folder opened for one operation.
type folder struct {
	dir  string   // absolute
	root *os.Root // of base
	rel  string   // dir relative to base ("." when dir is base)
}

func (f *folder) Close() {
	if f.root != nil {
		f.root.Close()
	}
}

// openFolder opens the screenshots folder. ok is false when it does not
// exist, or is reached through a link (it then has no screenshot); err is
// an error of the card.
func (s *Service) openFolder() (*folder, bool, error) {
	roots := s.o.Adapter.Roots()
	dir := roots.Screenshots
	if dir == "" {
		return nil, false, nil
	}
	// The card itself may be reached through a link (h700); nothing below
	// it is followed.
	base := confine.Base(roots.SD, dir)
	if err := confine.CheckDir(base, dir); err != nil {
		if errors.Is(err, confine.ErrOutside) {
			s.o.Logger.Warn("screenshots folder refused", "error", err)
			return nil, false, nil
		}
		return nil, false, err
	}
	fi, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	case !fi.IsDir():
		return nil, false, nil
	}
	rel, err := filepath.Rel(base, dir)
	if err != nil {
		return nil, false, err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, false, err
	}
	return &folder{dir: dir, root: root, rel: rel}, true, nil
}

// readBatch is how many entries of the folder are read at a time.
const readBatch = 256

// scan reads the folder through its root, readBatch entries at a time,
// and calls fn with each regular file the list shows, by exact name (never
// a link: the type comes from the directory, not from the target). fn
// returns false to stop. ErrOutside: the folder was replaced by a link.
func (f *folder) scan(ctx context.Context, fn func(de fs.DirEntry) bool) error {
	d, err := f.root.Open(f.rel)
	if err != nil {
		return err
	}
	defer d.Close()
	// The folder opened is the one checked (no link put in its place).
	fi, err := d.Stat()
	if err != nil {
		return err
	}
	li, err := f.root.Lstat(f.rel)
	if err != nil || !fi.IsDir() || (f.rel != "." && !os.SameFile(fi, li)) {
		return confine.ErrOutside
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		des, err := d.ReadDir(readBatch)
		for _, de := range des {
			if de.Type().IsRegular() && listable(de.Name()) && !fn(de) {
				return nil
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (f *folder) fileRel(name string) string { return filepath.Join(f.rel, name) }

// newer orders the list: takenAt, newest first, then name.
func newer(a, b Screenshot) bool {
	if !a.TakenAt.Equal(b.TakenAt) {
		return a.TakenAt.After(b.TakenAt)
	}
	return a.Name < b.Name
}

// List lists the screenshots, newest first (GET /screenshots). The folder
// is read in batches and only the MaxList newest are kept, so the memory
// does not grow with the folder.
func (s *Service) List(ctx context.Context) (List, error) {
	out := List{Screenshots: []Screenshot{}}
	f, ok, err := s.openFolder()
	if err != nil || !ok {
		return out, err
	}
	defer f.Close()
	keep := func() {
		sort.Slice(out.Screenshots, func(i, j int) bool { return newer(out.Screenshots[i], out.Screenshots[j]) })
		if len(out.Screenshots) > MaxList {
			out.Screenshots, out.Truncated = out.Screenshots[:MaxList], true
		}
	}
	var infoErr error
	err = f.scan(ctx, func(de fs.DirEntry) bool {
		info, err := de.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return true // removed meanwhile
		}
		if err != nil {
			infoErr = err
			return false
		}
		if !info.Mode().IsRegular() {
			return true
		}
		out.Screenshots = append(out.Screenshots, s.describe(de.Name(), info))
		if len(out.Screenshots) >= 2*MaxList {
			keep()
		}
		return true
	})
	switch {
	case errors.Is(err, confine.ErrOutside):
		return List{Screenshots: []Screenshot{}}, nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return List{}, err
	case err != nil:
		return List{}, fmt.Errorf("screenshots: %w", err)
	case infoErr != nil:
		return List{}, fmt.Errorf("screenshots: %w", infoErr)
	}
	keep()
	for _, sc := range out.Screenshots {
		out.TotalBytes += sc.SizeBytes
	}
	return out, nil
}

// describe is the listing of one screenshot.
func (s *Service) describe(name string, info fs.FileInfo) Screenshot {
	n := s.o.Adapter.ScreenshotName(name)
	at := n.TakenAt
	if at.IsZero() {
		at = info.ModTime()
	}
	sc := Screenshot{
		ID: ID(name), Name: name, TakenAt: at.UTC().Truncate(time.Second), SizeBytes: info.Size(),
		Format: formatOf(name),
	}
	if n.Game != "" {
		g := n.Game
		sc.GameName = &g
	}
	sc.HasThumbnail = (sc.Format == FormatPNG || sc.Format == FormatJPEG) && info.Size() <= MaxThumbSourceBytes
	return sc
}

// Open opens a screenshot for reading (GET /screenshots/{screenshotId}),
// confined to the folder, by its exact name. It returns the file
// positioned at its start, its description, its name and the media type
// of its content. The caller serves this descriptor and closes it.
func (s *Service) Open(id string) (*os.File, fs.FileInfo, string, string, error) {
	name, err := nameOf(id)
	if err != nil {
		return nil, nil, "", "", err
	}
	file, fi, err := s.open(name)
	if err != nil {
		return nil, nil, "", "", err
	}
	var head [16]byte
	n, err := io.ReadFull(file, head[:])
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		file.Close()
		return nil, nil, "", "", fmt.Errorf("screenshots: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		return nil, nil, "", "", fmt.Errorf("screenshots: %w", err)
	}
	return file, fi, name, contentType(head[:n]), nil
}

// open opens the screenshot name directly in the folder, through the
// root of the card: no link at any component, a regular file (confine.Open).
// No directory is read: on a card that ignores case (exFAT), a name that
// differs only by case reaches the same file (the identifier is opaque:
// the pak never produces such a name); on ext4 it is another file.
func (s *Service) open(name string) (*os.File, fs.FileInfo, error) {
	f, ok, err := s.openFolder()
	if err != nil {
		return nil, nil, fmt.Errorf("screenshots: %w", err)
	}
	if !ok {
		return nil, nil, ErrNotFound
	}
	defer f.Close()
	file, fi, err := confine.Open(f.root, f.fileRel(name))
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, confine.ErrOutside):
		return nil, nil, ErrNotFound
	case err != nil:
		return nil, nil, fmt.Errorf("screenshots: %w", err)
	}
	return file, fi, nil
}

// contentType is the media type of the first bytes of a file.
func contentType(head []byte) string {
	switch {
	case isPNG(head):
		return "image/png"
	case isJPEG(head):
		return "image/jpeg"
	case len(head) >= 2 && head[0] == 'B' && head[1] == 'M':
		return "image/bmp"
	}
	return "application/octet-stream"
}

func isPNG(head []byte) bool { return len(head) >= 8 && string(head[:8]) == "\x89PNG\r\n\x1a\n" }
func isJPEG(head []byte) bool {
	return len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF
}

// Trash moves the screenshots of ids to the trash in one entry (POST
// /screenshots/trash), all or nothing: an invalid identifier gives
// ErrInvalidID, missing screenshots a *MissingError, and nothing moves.
// Each screenshot must be listed under its exact name; its identity
// (device and inode, through the root of the card) is checked again by
// the trash right before it moves, so a folder or file swapped meanwhile
// (another folder, a folder of the same name) is never moved in its place.
func (s *Service) Trash(ids []string) (store.MoveResult, error) {
	if len(ids) == 0 || len(ids) > MaxTrash {
		return store.MoveResult{}, ErrInvalidID
	}
	var names []string
	seen := map[string]bool{}
	for _, id := range ids {
		name, err := nameOf(id)
		if err != nil {
			return store.MoveResult{}, err
		}
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	s.o.Lock.Lock()
	defer s.o.Lock.Unlock()
	f, ok, err := s.openFolder()
	if err != nil {
		return store.MoveResult{}, fmt.Errorf("screenshots: %w", err)
	}
	listed := map[string]bool{}
	if ok {
		defer f.Close()
		// Exact names: the folder is read only until every name is found.
		left := len(names)
		err := f.scan(context.Background(), func(de fs.DirEntry) bool {
			if seen[de.Name()] && !listed[de.Name()] {
				listed[de.Name()] = true
				left--
			}
			return left > 0
		})
		if err != nil && !errors.Is(err, confine.ErrOutside) {
			return store.MoveResult{}, fmt.Errorf("screenshots: %w", err)
		}
		if errors.Is(err, confine.ErrOutside) {
			listed = map[string]bool{}
		}
	}
	var missing []string
	var items []store.MoveItem
	for _, name := range names {
		if !listed[name] {
			missing = append(missing, ID(name))
			continue
		}
		file, fi, err := confine.Open(f.root, f.fileRel(name))
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, confine.ErrOutside) {
			missing = append(missing, ID(name))
			continue
		}
		if err != nil {
			return store.MoveResult{}, fmt.Errorf("screenshots: %w", err)
		}
		file.Close()
		items = append(items, store.MoveItem{Path: filepath.Join(f.dir, name), Identity: fi})
	}
	if len(missing) > 0 {
		return store.MoveResult{}, &MissingError{IDs: missing}
	}
	label := names[0]
	if len(names) > 1 {
		label = fmt.Sprintf("%d screenshots", len(names))
	}
	res, err := s.o.Trash.Move(store.ReasonDeleteScreenshot, label, items)
	for _, name := range names {
		s.thumbs.forget(name)
	}
	if errors.Is(err, store.ErrChanged) {
		// Swapped since it was checked: as if it had gone.
		return store.MoveResult{}, ErrNotFound
	}
	if err != nil {
		return store.MoveResult{}, &PartialError{Err: fmt.Errorf("screenshots: %w", err)}
	}
	s.o.Logger.Info("screenshots moved to the trash", "count", len(names), "trash", res.Entry.ID)
	return res, nil
}

// PartialError: moving to the trash failed after the checks. The trash
// puts back what it moved; a file that cannot go back stays in a trash
// entry (GET /trash lists it). The folder may have changed: a screenshots
// event follows anyway.
type PartialError struct{ Err error }

func (e *PartialError) Error() string { return e.Err.Error() }
func (e *PartialError) Unwrap() error { return e.Err }

// InFolder reports whether the absolute path p is a file directly in the
// screenshots folder (a restore from the trash put a screenshot back).
func (s *Service) InFolder(p string) bool {
	dir := s.o.Adapter.Roots().Screenshots
	if dir == "" {
		return false
	}
	if filepath.Dir(p) == dir {
		return true
	}
	a, errA := os.Stat(dir)
	b, errB := os.Stat(filepath.Dir(p))
	return errA == nil && errB == nil && os.SameFile(a, b)
}
