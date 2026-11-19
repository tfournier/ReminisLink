package library

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Kinds of GET /sd/files.
const (
	SDKindCheat = "cht"
	SDKindPatch = "patch"
)

// Bounds of the SD card walk of GET /sd/files.
const (
	// MaxSDFileDepth is the deepest folder walked under a root.
	MaxSDFileDepth = 8
	// MaxSDFiles is the largest number of files listed.
	MaxSDFiles = 5000
	// sdFilePrefix starts every sdFileId.
	sdFilePrefix = "sd_"
)

// SDFile is an item of GET /sd/files.
type SDFile struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"sizeBytes"`
}

// ErrBadKind means an unknown kind of SD file.
var ErrBadKind = errors.New("unknown kind of SD file")

var sdExtensions = map[string]map[string]bool{
	SDKindCheat: {".cht": true},
	SDKindPatch: {".ips": true, ".bps": true, ".ups": true},
}

// sdRoots are the folders listed for a kind: Cheats/ for cheats, Roms/
// and the ReminisLink patches/ folder for patches.
func (l *Library) sdRoots(kind string) []string {
	r := l.adapter.Roots()
	switch kind {
	case SDKindCheat:
		return []string{r.Cheats}
	case SDKindPatch:
		return []string{r.Roms, l.layout.PatchesDir()}
	}
	return nil
}

// SDFiles lists the files of a kind (cht or patch) that the app can use,
// with opaque identifiers: "sd_" and the base64url of the path relative
// to the SD card. Hidden files and folders and symbolic links are skipped;
// the walk is bounded (MaxSDFileDepth, MaxSDFiles).
func (l *Library) SDFiles(kind string) ([]SDFile, error) {
	exts, ok := sdExtensions[kind]
	if !ok {
		return nil, ErrBadKind
	}
	sd, err := filepath.Abs(l.adapter.Roots().SD)
	if err != nil {
		return nil, err
	}
	out := []SDFile{}
	for _, root := range l.sdRoots(kind) {
		root, err := filepath.Abs(root)
		if err != nil || !within(sd, root) {
			continue // a root moved off the SD card by the environment
		}
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if p == root {
					return filepath.SkipDir
				}
				return nil
			}
			if len(out) >= MaxSDFiles {
				return filepath.SkipAll
			}
			if p != root && strings.HasPrefix(d.Name(), ".") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				rel, _ := filepath.Rel(root, p)
				if rel != "." && strings.Count(filepath.ToSlash(rel), "/")+1 > MaxSDFileDepth {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || !exts[strings.ToLower(filepath.Ext(d.Name()))] {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			rel, err := filepath.Rel(sd, p)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			out = append(out, SDFile{ID: sdFilePrefix + EncodeID(rel), Path: rel, SizeBytes: info.Size()})
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// ResolveSDFile returns the absolute path of an sdFileId of a kind. The
// identifier must name a regular file with an extension of the kind,
// inside one of its roots, without hidden components (ErrInvalidID when
// it is malformed or outside, ErrNotFound when the file is missing).
func (l *Library) ResolveSDFile(id, kind string) (string, error) {
	exts, ok := sdExtensions[kind]
	if !ok {
		return "", ErrBadKind
	}
	enc, ok := strings.CutPrefix(id, sdFilePrefix)
	if !ok {
		return "", ErrInvalidID
	}
	p, err := ResolveID(l.adapter.Roots().SD, enc)
	if err != nil {
		return "", err
	}
	if !exts[strings.ToLower(filepath.Ext(p))] {
		return "", ErrInvalidID
	}
	inRoot := false
	for _, root := range l.sdRoots(kind) {
		abs, err := filepath.Abs(root)
		if err != nil || abs == p || !within(abs, p) {
			continue
		}
		// Hidden files and folders under the root are never listed.
		rel, _ := filepath.Rel(abs, p)
		hidden := false
		for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
			hidden = hidden || strings.HasPrefix(part, ".")
		}
		if !hidden {
			inRoot = true
			break
		}
	}
	if !inRoot {
		return "", ErrInvalidID
	}
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return "", ErrNotFound
	}
	return p, nil
}
