package library

import (
	"container/heap"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tfournier/ReminisLink/internal/confine"
)

// Read-only browser of the SD card (M12, protocol/openapi.yaml GET
// /sd/browse): every path comes from an opaque sdEntryId resolved here,
// under the SD card, without following any link, and the data of
// ReminisLink (its folder and its log files) is never listed nor reached.

const (
	// sdEntryPrefix starts every sdEntryId.
	sdEntryPrefix = "se_"
	// MaxBrowseEntries bounds the entries of one folder listing.
	MaxBrowseEntries = 5000
	// logFilePrefix starts the names of the log files of the pak in the
	// log folder (ReminisLink.txt, ReminisLink.old.txt,
	// ReminisLink.stderr.txt).
	logFilePrefix = "reminislink"
)

// Kinds of SDEntry.
const (
	SDEntryFolder = "folder"
	SDEntryFile   = "file"
)

// SDEntry is an entry of GET /sd/browse.
type SDEntry struct {
	ID         string    `json:"sdEntryId"`
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	SizeBytes  *int64    `json:"sizeBytes"`
	ModifiedAt time.Time `json:"modifiedAt"`
}

// SDFolder is the answer of GET /sd/browse.
type SDFolder struct {
	FolderID  *string   `json:"folderId"`
	Path      string    `json:"path"`
	ParentID  *string   `json:"parentId"`
	Entries   []SDEntry `json:"entries"`
	Truncated bool      `json:"truncated"`
}

// SDEntryID returns the sdEntryId of a path relative to the SD card.
func SDEntryID(rel string) string { return sdEntryPrefix + EncodeID(rel) }

// sdHidden is the data of ReminisLink, as paths relative to the SD card
// (split in components) and the file descriptions of what exists.
type sdHidden struct {
	data  []string // the ReminisLink folder
	pak   []string // the folder of the pak (Knulli), nil when none
	logs  []string // the log folder (its ReminisLink* files are hidden)
	inSD  bool     // the log folder is on the SD card
	infos []fs.FileInfo
}

func splitRel(rel string) []string {
	if rel == "" || rel == "." {
		return nil
	}
	return strings.Split(filepath.ToSlash(rel), "/")
}

// hiddenSet computes the hidden data for this request (sd absolute).
func (l *Library) hiddenSet(sd string) sdHidden {
	var h sdHidden
	if rel, err := filepath.Rel(sd, l.adapter.Roots().Data); err == nil && filepath.IsLocal(rel) {
		h.data = splitRel(rel)
	}
	if fi, err := os.Stat(l.adapter.Roots().Data); err == nil {
		h.infos = append(h.infos, fi)
	}
	if pak := l.adapter.Roots().Pak; pak != "" {
		if rel, err := filepath.Rel(sd, pak); err == nil && filepath.IsLocal(rel) {
			h.pak = splitRel(rel)
		}
		if fi, err := os.Stat(pak); err == nil {
			h.infos = append(h.infos, fi)
		}
	}
	logs := l.adapter.Roots().Logs
	if rel, err := filepath.Rel(sd, logs); err == nil && filepath.IsLocal(rel) {
		h.logs, h.inSD = splitRel(rel), true
	}
	if des, err := os.ReadDir(logs); err == nil {
		for _, d := range des {
			if strings.HasPrefix(strings.ToLower(d.Name()), logFilePrefix) {
				if fi, err := os.Lstat(filepath.Join(logs, d.Name())); err == nil {
					h.infos = append(h.infos, fi)
				}
			}
		}
	}
	return h
}

// prefixFold reports whether p starts with the components of prefix
// (compared ignoring case, as on exFAT).
func prefixFold(p, prefix []string) bool {
	if len(prefix) == 0 || len(p) < len(prefix) {
		return false
	}
	for i := range prefix {
		if !strings.EqualFold(p[i], prefix[i]) {
			return false
		}
	}
	return true
}

// hides reports whether the entry at comps (relative to the SD card),
// described by fi (nil: unknown), is ReminisLink data.
func (h sdHidden) hides(comps []string, fi fs.FileInfo) bool {
	if prefixFold(comps, h.data) || prefixFold(comps, h.pak) {
		return true
	}
	if h.inSD && len(comps) == len(h.logs)+1 && (len(h.logs) == 0 || prefixFold(comps, h.logs)) &&
		strings.HasPrefix(strings.ToLower(comps[len(comps)-1]), logFilePrefix) {
		return true
	}
	if fi != nil {
		for _, x := range h.infos {
			if os.SameFile(fi, x) {
				return true
			}
		}
	}
	return false
}

// sdEntry is a resolved sdEntryId.
type sdEntry struct {
	sd   string      // SD card root, absolute
	rel  string      // relative to the SD card, "/" separator
	info fs.FileInfo // Lstat of the entry
}

// resolveSDEntry decodes an sdEntryId and checks every component of its
// path through an os.Root of the SD card: ErrInvalidID when it is
// malformed or not under the card, ErrNotFound when a component is
// missing, is a link, is not a folder (but the last) or is ReminisLink
// data.
func (l *Library) resolveSDEntry(root *os.Root, sd, id string) (sdEntry, error) {
	enc, ok := strings.CutPrefix(id, sdEntryPrefix)
	if !ok {
		return sdEntry{}, fmt.Errorf("%w: prefix", ErrInvalidID)
	}
	rel, err := DecodeID(enc)
	if err != nil {
		return sdEntry{}, err
	}
	hidden := l.hiddenSet(sd)
	comps := strings.Split(rel, "/")
	var fi fs.FileInfo
	for i := range comps {
		fi, err = root.Lstat(filepath.Join(comps[:i+1]...))
		if err != nil {
			return sdEntry{}, ErrNotFound
		}
		if fi.Mode()&fs.ModeSymlink != 0 || (i < len(comps)-1 && !fi.IsDir()) {
			return sdEntry{}, ErrNotFound
		}
		if hidden.hides(comps[:i+1], fi) {
			return sdEntry{}, ErrNotFound
		}
	}
	if !fi.IsDir() && !fi.Mode().IsRegular() {
		return sdEntry{}, ErrNotFound
	}
	return sdEntry{sd: sd, rel: rel, info: fi}, nil
}

// sdRoot opens the SD card root.
func (l *Library) sdRoot() (*os.Root, string, error) {
	sd, err := filepath.Abs(l.adapter.Roots().SD)
	if err != nil {
		return nil, "", err
	}
	root, err := os.OpenRoot(sd)
	if err != nil {
		return nil, "", err
	}
	return root, sd, nil
}

// ResolveSDEntry returns the absolute path of an sdEntryId and its
// description (a folder or a regular file), with the rules of
// resolveSDEntry.
func (l *Library) ResolveSDEntry(id string) (string, fs.FileInfo, error) {
	root, sd, err := l.sdRoot()
	if err != nil {
		return "", nil, err
	}
	defer root.Close()
	e, err := l.resolveSDEntry(root, sd, id)
	if err != nil {
		return "", nil, err
	}
	return filepath.Join(sd, filepath.FromSlash(e.rel)), e.info, nil
}

// Browse lists a folder of the SD card: the root for "", else the folder
// of an sdEntryId. Links, special files and ReminisLink data are left
// out; hidden files are listed. Folders first, then files, by name
// ignoring case; at most MaxBrowseEntries.
func (l *Library) Browse(folderID string) (SDFolder, error) {
	root, sd, err := l.sdRoot()
	if err != nil {
		return SDFolder{}, err
	}
	defer root.Close()
	out := SDFolder{Entries: []SDEntry{}}
	rel := ""
	if folderID != "" {
		e, err := l.resolveSDEntry(root, sd, folderID)
		if err != nil {
			return SDFolder{}, err
		}
		if !e.info.IsDir() {
			return SDFolder{}, ErrNotFound
		}
		rel = e.rel
		id := SDEntryID(rel)
		out.FolderID = &id
		out.Path = rel
		if parent := path.Dir(rel); parent != "." {
			pid := SDEntryID(parent)
			out.ParentID = &pid
		}
	}
	open := "."
	if rel != "" {
		open = filepath.FromSlash(rel)
	}
	dir, err := root.Open(open)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return SDFolder{}, ErrNotFound
		}
		return SDFolder{}, err
	}
	// Read in batches and keep only the first MaxBrowseEntries in the
	// sort order (a max-heap of that size): a folder of any size is
	// listed in bounded memory.
	defer dir.Close()
	hidden := l.hiddenSet(sd)
	base := splitRel(rel)
	h := &entryHeap{}
	for {
		des, rerr := dir.ReadDir(browseBatch)
		for _, d := range des {
			t := d.Type()
			if t&fs.ModeSymlink != 0 || (!t.IsDir() && !t.IsRegular()) {
				continue
			}
			info, err := d.Info()
			if err != nil {
				continue // vanished meanwhile
			}
			comps := append(append([]string(nil), base...), d.Name())
			if hidden.hides(comps, info) {
				continue
			}
			childRel := strings.Join(comps, "/")
			if checkRelPath(childRel) != nil {
				continue // a name the API cannot carry (control character...)
			}
			e := SDEntry{ID: SDEntryID(childRel), Name: d.Name(), ModifiedAt: info.ModTime().UTC().Truncate(time.Second)}
			if t.IsDir() {
				e.Kind = SDEntryFolder
			} else {
				e.Kind = SDEntryFile
				size := info.Size()
				e.SizeBytes = &size
			}
			heap.Push(h, e)
			if h.Len() > MaxBrowseEntries {
				heap.Pop(h) // the last in the sort order
				out.Truncated = true
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return SDFolder{}, rerr
		}
	}
	out.Entries = append(out.Entries, *h...)
	sort.Slice(out.Entries, func(i, j int) bool { return entryBefore(out.Entries[i], out.Entries[j]) })
	return out, nil
}

// browseBatch is the number of folder entries read at a time.
const browseBatch = 512

// entryBefore is the order of a listing: folders first, then by name
// ignoring case, then by name.
func entryBefore(a, b SDEntry) bool {
	if a.Kind != b.Kind {
		return a.Kind == SDEntryFolder
	}
	if la, lb := strings.ToLower(a.Name), strings.ToLower(b.Name); la != lb {
		return la < lb
	}
	return a.Name < b.Name
}

// entryHeap is a max-heap of entries in the listing order: its top is
// the entry that comes last.
type entryHeap []SDEntry

func (h entryHeap) Len() int           { return len(h) }
func (h entryHeap) Less(i, j int) bool { return entryBefore(h[j], h[i]) }
func (h entryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *entryHeap) Push(x any)        { *h = append(*h, x.(SDEntry)) }
func (h *entryHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// OpenSDEntry opens the regular file of an sdEntryId for reading, through
// the SD card root (no link at any component, races included):
// ErrNotFound for a folder, a link, a missing file or ReminisLink data.
func (l *Library) OpenSDEntry(id string) (*os.File, fs.FileInfo, string, error) {
	root, sd, err := l.sdRoot()
	if err != nil {
		return nil, nil, "", err
	}
	defer root.Close()
	e, err := l.resolveSDEntry(root, sd, id)
	if err != nil {
		return nil, nil, "", err
	}
	if !e.info.Mode().IsRegular() {
		return nil, nil, "", ErrNotFound
	}
	f, fi, err := confine.Open(root, filepath.FromSlash(e.rel))
	if err != nil {
		if errors.Is(err, confine.ErrOutside) || errors.Is(err, fs.ErrNotExist) {
			return nil, nil, "", ErrNotFound
		}
		return nil, nil, "", err
	}
	if !os.SameFile(fi, e.info) {
		f.Close()
		return nil, nil, "", ErrNotFound // replaced since it was checked
	}
	return f, fi, path.Base(e.rel), nil
}
