package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tfournier/ReminisLink/internal/store"
)

// Writes of the SD card browser (M23, protocol/openapi.yaml POST
// /sd/folders, POST /sd/trash and the sd-file upload target): every place
// comes from an sdEntryId resolved by resolveSDEntry (under the card, no
// link at any component, never the data of ReminisLink), then is checked
// against the protection rules of the OS (osadapter.SDRules) and the
// places of ReminisLink.

// Protections of SdProtection (protocol/openapi.yaml).
const (
	ProtectSystem = "system"
	ProtectData   = "data"
	ProtectRoot   = "root"
	ProtectLink   = "link"
)

// ProtectedError: a place of the card cannot be changed (409 conflict
// with details.protected). IDs are the refused sdEntryIds (POST
// /sd/trash).
type ProtectedError struct {
	Reason string
	IDs    []string
}

func (e *ProtectedError) Error() string { return "protected place of the SD card: " + e.Reason }

// MissingError: some sdEntryIds of POST /sd/trash name nothing (404 with
// details.missing).
type MissingError struct{ IDs []string }

func (e *MissingError) Error() string { return "SD card entries not found" }

// ChangedError: entries of POST /sd/trash are not the version the client
// showed (409 with details.changed).
type ChangedError struct{ IDs []string }

func (e *ChangedError) Error() string { return "SD card entries changed since they were listed" }

// SDTrashItem is an item of POST /sd/trash: an entry and the version the
// client showed (SdTrashItem).
type SDTrashItem struct {
	ID         string    `json:"sdEntryId"`
	SizeBytes  *int64    `json:"sizeBytes"`
	ModifiedAt time.Time `json:"modifiedAt"`
	Version    string    `json:"version"`
	// SizeGiven: the request carried sizeBytes (null for a folder): an
	// item without it is refused (Codex review of M23, round 3).
	SizeGiven bool `json:"-"`
}

// UnmarshalJSON tells an absent sizeBytes from a null one.
func (it *SDTrashItem) UnmarshalJSON(b []byte) error {
	type plain SDTrashItem
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err != nil {
		return err
	}
	_, given := keys["sizeBytes"]
	*it = SDTrashItem(p)
	it.SizeGiven = given
	return nil
}

// matches reports whether the entry described by info is the version the
// client showed: same modification time (whole seconds), same size for a
// file, same version when both are known (Codex review of M23).
func (it SDTrashItem) matches(info fs.FileInfo) bool {
	if !info.ModTime().UTC().Truncate(time.Second).Equal(it.ModifiedAt.UTC().Truncate(time.Second)) {
		return false
	}
	if info.IsDir() != (it.SizeBytes == nil) {
		return false
	}
	if !info.IsDir() && *it.SizeBytes != info.Size() {
		return false
	}
	if sdVersion(info) != it.Version {
		return false
	}
	return true
}

// MaxSDTrash bounds the entries of one POST /sd/trash.
const MaxSDTrash = 100

// ProtectPaths adds places of ReminisLink (absolute paths) that the
// browser never changes: the folder of the running pak (M23).
func (l *Library) ProtectPaths(paths ...string) {
	for _, p := range paths {
		if p != "" {
			l.protected = append(l.protected, p)
		}
	}
}

// sdRules are the protection rules for one request, as components of
// paths relative to the SD card.
type sdRules struct {
	system     [][]string
	hiddenRoot bool
	roots      [][]string
	data       [][]string
}

// relComps returns the components of p relative to sd, ok false when p is
// not inside sd (or is sd itself: nil, true).
func relComps(sd, p string) ([]string, bool) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return nil, false
	}
	rel, err := filepath.Rel(sd, abs)
	if err != nil || !filepath.IsLocal(rel) && rel != "." {
		return nil, false
	}
	return splitRel(rel), true
}

// rules computes the protection rules (sd absolute).
func (l *Library) rules(sd string) sdRules {
	osr := l.adapter.SDRules()
	r := sdRules{hiddenRoot: osr.HiddenRootSystem}
	add := func(dst *[][]string, paths ...string) {
		for _, p := range paths {
			if p == "" {
				continue
			}
			if c, ok := relComps(sd, p); ok && len(c) > 0 {
				*dst = append(*dst, c)
			}
		}
	}
	add(&r.system, osr.System...)
	add(&r.roots, osr.Roots...)
	roots := l.adapter.Roots()
	add(&r.data, osr.Data...)
	add(&r.data, roots.Data, roots.Pak)
	add(&r.data, l.protected...)
	return r
}

// readOnly returns why the place at comps (and everything in it) cannot
// be changed, or "".
func (r sdRules) readOnly(comps []string) string {
	for _, d := range r.data {
		if prefixFold(comps, d) {
			return ProtectData
		}
	}
	if r.hiddenRoot && len(comps) > 0 && strings.HasPrefix(comps[0], ".") {
		return ProtectSystem
	}
	for _, s := range r.system {
		if prefixFold(comps, s) {
			return ProtectSystem
		}
	}
	return ""
}

// deleteRefusal returns why the entry at comps cannot go to the trash, or
// "": read only, a main folder or the root, or a folder holding a
// protected place.
func (r sdRules) deleteRefusal(comps []string) string {
	if len(comps) == 0 {
		return ProtectRoot
	}
	if why := r.readOnly(comps); why != "" {
		return why
	}
	for _, x := range r.roots {
		if len(x) == len(comps) && prefixFold(comps, x) {
			return ProtectRoot // a main folder itself
		}
	}
	for _, d := range r.data {
		if prefixFold(d, comps) {
			return ProtectData
		}
	}
	for _, s := range r.system {
		if prefixFold(s, comps) {
			return ProtectSystem
		}
	}
	for _, x := range r.roots {
		if prefixFold(x, comps) {
			return ProtectRoot
		}
	}
	return ""
}

// SDFolderDir resolves the folder of an sdEntryId ("" for the root of the
// card) where a file is added or a folder made: its absolute path and its
// path relative to the card ("" for the root). ErrInvalidID, ErrNotFound
// (missing, a file, a link on the way, ReminisLink data) or a
// *ProtectedError when it is not writable.
func (l *Library) SDFolderDir(folderID string) (string, string, error) {
	root, sd, err := l.sdRoot()
	if err != nil {
		return "", "", err
	}
	defer root.Close()
	rel := ""
	if folderID != "" {
		e, err := l.resolveSDEntry(root, sd, folderID)
		if err != nil {
			return "", "", err
		}
		if !e.info.IsDir() {
			return "", "", fmt.Errorf("%w: not a folder", ErrNotFound)
		}
		rel = e.rel
	}
	if why := l.rules(sd).readOnly(splitRel(rel)); why != "" {
		return "", "", &ProtectedError{Reason: why}
	}
	return filepath.Join(sd, filepath.FromSlash(rel)), rel, nil
}

// SDEntryAt describes the entry rel (relative to the card) of a folder
// just written (a new folder, an added file), as GET /sd/browse lists it.
func (l *Library) SDEntryAt(rel string) (SDEntry, error) {
	root, sd, err := l.sdRoot()
	if err != nil {
		return SDEntry{}, err
	}
	defer root.Close()
	e, err := l.resolveSDEntry(root, sd, SDEntryID(rel))
	if err != nil {
		return SDEntry{}, err
	}
	return l.describe(l.rules(sd), splitRel(rel), path.Base(rel), e.info), nil
}

// describe builds the SDEntry of a listed entry.
func (l *Library) describe(r sdRules, comps []string, name string, info fs.FileInfo) SDEntry {
	e := SDEntry{
		ID: SDEntryID(strings.Join(comps, "/")), Name: name, ModifiedAt: info.ModTime().UTC().Truncate(time.Second),
		Version: sdVersion(info),
	}
	if info.IsDir() {
		e.Kind = SDEntryFolder
	} else {
		e.Kind = SDEntryFile
		size := info.Size()
		e.SizeBytes = &size
	}
	if why := r.deleteRefusal(comps); why != "" {
		e.Protection = &why
	} else {
		e.Deletable = true
	}
	return e
}

// SDTrashSelection is what POST /sd/trash moves.
type SDTrashSelection struct {
	Items []store.MoveItem
	Label string
}

// SelectSDTrash resolves the items of POST /sd/trash (duplicates once):
// ErrInvalidID for an unreadable identifier, *MissingError for those that
// name nothing (a link, missing, ReminisLink data), *ProtectedError for
// those that cannot go to the trash (a protected place, or a folder
// holding a link or a special file, which could not come back),
// *ChangedError for those that are not the version the client showed. An
// entry inside another of the list moves with it (store.Trash.Move).
func (l *Library) SelectSDTrash(items []SDTrashItem) (SDTrashSelection, error) {
	root, sd, err := l.sdRoot()
	if err != nil {
		return SDTrashSelection{}, err
	}
	defer root.Close()
	r := l.rules(sd)
	var missing, refused, changed []string
	reason := ""
	seen := map[string]bool{}
	var sel SDTrashSelection
	var names []string
	for _, item := range items {
		id := item.ID
		if seen[id] {
			continue
		}
		seen[id] = true
		e, err := l.resolveSDEntry(root, sd, id)
		if errors.Is(err, ErrNotFound) {
			missing = append(missing, id)
			continue
		}
		if err != nil {
			return SDTrashSelection{}, err
		}
		why := r.deleteRefusal(splitRel(e.rel))
		if why == "" && e.info.IsDir() {
			bad, werr := holdsLink(root, filepath.FromSlash(e.rel))
			if werr != nil {
				return SDTrashSelection{}, werr
			}
			if bad {
				why = ProtectLink
			}
		}
		if why != "" {
			if reason == "" {
				reason = why
			}
			refused = append(refused, id)
			continue
		}
		if !item.matches(e.info) {
			changed = append(changed, id)
			continue
		}
		var identity fs.FileInfo
		if e.info.Mode().IsRegular() {
			identity = e.info // moved only if it is still that file
		}
		sel.Items = append(sel.Items, store.MoveItem{Path: filepath.Join(sd, filepath.FromSlash(e.rel)), Identity: identity})
		names = append(names, path.Base(e.rel))
	}
	if len(missing) > 0 {
		return SDTrashSelection{}, &MissingError{IDs: missing}
	}
	if len(refused) > 0 {
		return SDTrashSelection{}, &ProtectedError{Reason: reason, IDs: refused}
	}
	if len(changed) > 0 {
		return SDTrashSelection{}, &ChangedError{IDs: changed}
	}
	if len(sel.Items) == 1 {
		sel.Label = names[0]
	} else {
		sel.Label = strconv.Itoa(len(sel.Items)) + " items"
	}
	return sel, nil
}

// holdsLink reports whether the folder rel of root holds a symbolic link
// or a special file at any depth (walked without following any).
func holdsLink(root *os.Root, rel string) (bool, error) {
	bad := false
	err := fs.WalkDir(root.FS(), filepath.ToSlash(rel), func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if t := d.Type(); !t.IsDir() && !t.IsRegular() {
			bad = true
			return fs.SkipAll
		}
		return nil
	})
	return bad, err
}
