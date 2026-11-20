// Package paks lists, checks and stages the tool and emulator paks of the
// console (M12, protocol/openapi.yaml GET /paks and POST /paks/install).
// It never changes the installed paks itself: internal/transfer moves the
// staged folder into place under the write lock and the old pak to the
// trash.
package paks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter"
)

// Kinds of pak (PakKind and PakInstallKind in protocol/openapi.yaml).
const (
	KindTool = "tool"
	KindEmu  = "emu"
	KindAuto = "auto"
)

// Suffix ends the folder name of every pak.
const Suffix = ".pak"

// LaunchScript is the file every pak has at the top of its folder.
const LaunchScript = "launch.sh"

// MetadataFile is the optional description of a pak (version, type).
const MetadataFile = "pak.json"

// idPrefix starts every pakId.
const idPrefix = "pk_"

// maxMetadataBytes bounds the pak.json read.
const maxMetadataBytes = 64 << 10

// maxSizeWalk bounds the files counted in the size of a listed pak.
const maxSizeWalk = 100000

// Errors of Resolve.
var (
	ErrInvalidID = errors.New("paks: invalid identifier")
	ErrNotFound  = errors.New("paks: not found")
)

// Pak is an item of GET /paks.
type Pak struct {
	ID         string    `json:"pakId"`
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	Path       string    `json:"path"`
	SizeBytes  int64     `json:"sizeBytes"`
	ModifiedAt time.Time `json:"modifiedAt"`
	Version    *string   `json:"version"`
	Self       bool      `json:"self"`
}

// Manager knows where the paks of the platform are, and which one is
// ReminisLink itself.
type Manager struct {
	sd       string
	tools    string
	emus     string
	platform string
	self     string
}

// NewManager returns the manager of the paks under roots.Tools and
// roots.Emus (whose last component is the platform). self is the folder
// of the running pak ("" when unknown: no pak is self).
func NewManager(roots osadapter.Roots, self string) *Manager {
	abs := func(p string) string {
		if p == "" {
			return ""
		}
		if a, err := filepath.Abs(p); err == nil {
			return a
		}
		return filepath.Clean(p)
	}
	return &Manager{sd: abs(roots.SD), tools: abs(roots.Tools), emus: abs(roots.Emus),
		platform: filepath.Base(roots.Emus), self: abs(self)}
}

// Platform is the platform folder of the paks (tg5040).
func (m *Manager) Platform() string { return m.platform }

// Dir returns the folder of the paks of a kind ("" for another kind).
func (m *Manager) Dir(kind string) string {
	switch kind {
	case KindTool:
		return m.tools
	case KindEmu:
		return m.emus
	}
	return ""
}

// HasSuffix reports whether name ends with ".pak", ignoring case.
func HasSuffix(name string) bool {
	return len(name) > len(Suffix) && strings.EqualFold(name[len(name)-len(Suffix):], Suffix)
}

// DisplayName is the folder name of a pak without ".pak".
func DisplayName(folder string) string {
	if HasSuffix(folder) {
		return folder[:len(folder)-len(Suffix)]
	}
	return folder
}

// ID returns the pakId of the pak folder dir (absolute), "" when it is not
// under the SD card.
func (m *Manager) ID(dir string) string {
	rel, err := filepath.Rel(m.sd, dir)
	if err != nil || !filepath.IsLocal(rel) {
		return ""
	}
	return idPrefix + library.EncodeID(filepath.ToSlash(rel))
}

// IsSelf reports whether dir is the folder of the running pak (the same
// folder, whatever the case of its name).
func (m *Manager) IsSelf(dir string) bool {
	if m.self == "" || dir == "" {
		return false
	}
	a, err1 := os.Stat(dir)
	b, err2 := os.Stat(m.self)
	if err1 == nil && err2 == nil {
		return os.SameFile(a, b)
	}
	return strings.EqualFold(filepath.Clean(dir), m.self)
}

// KindOf returns the kind of the folder holding a pak folder (Tools or
// Emus of the platform, names compared ignoring case), "" otherwise.
func (m *Manager) KindOf(parent string) string {
	parent = filepath.Clean(parent)
	switch {
	case m.tools != "" && strings.EqualFold(parent, m.tools):
		return KindTool
	case m.emus != "" && strings.EqualFold(parent, m.emus):
		return KindEmu
	}
	return ""
}

// List returns the installed paks: tools first, then by name ignoring
// case. A missing Tools or Emus folder lists nothing.
func (m *Manager) List() ([]Pak, error) {
	out := []Pak{}
	for _, kind := range []string{KindTool, KindEmu} {
		dir := m.Dir(kind)
		if !m.safeParent(dir) {
			continue
		}
		des, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		var group []Pak
		for _, d := range des {
			if !d.IsDir() || strings.HasPrefix(d.Name(), ".") || !HasSuffix(d.Name()) {
				continue // a link to a folder is not a folder here
			}
			p, err := m.describe(kind, filepath.Join(dir, d.Name()))
			if err != nil {
				continue // vanished meanwhile
			}
			group = append(group, p)
		}
		sort.Slice(group, func(i, j int) bool {
			a, b := strings.ToLower(group[i].Name), strings.ToLower(group[j].Name)
			if a != b {
				return a < b
			}
			return group[i].Name < group[j].Name
		})
		out = append(out, group...)
	}
	return out, nil
}

// safeParent reports whether a pak folder of the platform is a real
// folder inside the SD card, reached without a link.
func (m *Manager) safeParent(dir string) bool {
	if dir == "" {
		return false
	}
	rel, err := filepath.Rel(m.sd, dir)
	if err != nil || !filepath.IsLocal(rel) {
		return false
	}
	p := m.sd
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		p = filepath.Join(p, part)
		fi, err := os.Lstat(p)
		if err != nil || !fi.IsDir() {
			return false
		}
	}
	return true
}

// Describe returns the Pak of an installed pak folder of a kind.
func (m *Manager) Describe(kind, dir string) (Pak, error) { return m.describe(kind, dir) }

func (m *Manager) describe(kind, dir string) (Pak, error) {
	fi, err := os.Lstat(dir)
	if err != nil {
		return Pak{}, err
	}
	if !fi.IsDir() {
		return Pak{}, ErrNotFound
	}
	rel, _ := filepath.Rel(m.sd, dir)
	version, _ := ReadMetadata(dir)
	return Pak{
		ID: m.ID(dir), Name: DisplayName(filepath.Base(dir)), Kind: kind, Path: filepath.ToSlash(rel),
		SizeBytes: folderSize(dir), ModifiedAt: fi.ModTime().UTC().Truncate(time.Second),
		Version: version, Self: m.IsSelf(dir),
	}, nil
}

// ErrUnsafeFolder means the folder of the paks is not a real folder of
// the SD card (a link at some component).
var ErrUnsafeFolder = errors.New("paks: folder outside the SD card or reached through a link")

// EnsureDir creates the folder of the paks of a kind under the SD card,
// through an os.Root of the card (nothing is created outside it), and
// checks that no component of it is a link.
func (m *Manager) EnsureDir(kind string) (string, error) {
	dir := m.Dir(kind)
	rel, err := filepath.Rel(m.sd, dir)
	if dir == "" || err != nil || !filepath.IsLocal(rel) {
		return "", ErrUnsafeFolder
	}
	root, err := os.OpenRoot(m.sd)
	if err != nil {
		return "", err
	}
	defer root.Close()
	parts := strings.Split(rel, string(filepath.Separator))
	if err := root.MkdirAll(rel, 0o755); err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnsafeFolder, err)
	}
	if !m.safeParent(dir) {
		return "", ErrUnsafeFolder
	}
	// Every folder of the chain is durable in its parent, up to the SD
	// root, every time: a first install whose sync failed is synced
	// again on its retry, when the folders already exist.
	for i := len(parts); i >= 0; i-- {
		if err := syncDir(filepath.Join(append([]string{m.sd}, parts[:i]...)...)); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// FolderSize is the size of the regular files of a folder (links not
// followed): the size a journaled replacement checks (store.Install).
func FolderSize(dir string) int64 { return folderSize(dir) }

// folderSize is the size of the regular files of dir (links are not
// followed), bounded by maxSizeWalk files.
func folderSize(dir string) int64 {
	var n int64
	seen := 0
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if seen++; seen > maxSizeWalk {
			return filepath.SkipAll
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// Resolve returns the installed pak named by a pakId and its folder:
// ErrInvalidID when the identifier is malformed or does not name a pak
// folder of the platform, ErrNotFound when the folder is missing, is not
// a folder or is a link.
func (m *Manager) Resolve(id string) (Pak, string, error) {
	enc, ok := strings.CutPrefix(id, idPrefix)
	if !ok {
		return Pak{}, "", ErrInvalidID
	}
	rel, err := library.DecodeID(enc)
	if err != nil {
		return Pak{}, "", ErrInvalidID
	}
	dir := filepath.Join(m.sd, filepath.FromSlash(rel))
	name := filepath.Base(dir)
	kind := m.KindOf(filepath.Dir(dir))
	if kind == "" || !HasSuffix(name) || strings.HasPrefix(name, ".") {
		return Pak{}, "", ErrInvalidID
	}
	if !m.safeParent(filepath.Dir(dir)) {
		return Pak{}, "", ErrNotFound
	}
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() {
		return Pak{}, "", ErrNotFound
	}
	p, err := m.describe(kind, dir)
	if err != nil {
		return Pak{}, "", ErrNotFound
	}
	return p, dir, nil
}

// metadata is the part of pak.json the pak reads.
type metadata struct {
	Version any `json:"version"`
	Type    any `json:"type"`
}

// ReadMetadata reads the version and the type (TOOL, EMU...) of the
// pak.json of a pak folder: nil and "" when absent, unreadable, or not
// strings. The file is read through no link.
func ReadMetadata(dir string) (version *string, typ string) {
	p := filepath.Join(dir, MetadataFile)
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return nil, ""
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, ""
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxMetadataBytes+1))
	if err != nil || len(data) > maxMetadataBytes {
		return nil, ""
	}
	var md metadata
	if json.Unmarshal(data, &md) != nil {
		return nil, ""
	}
	if v, ok := md.Version.(string); ok && v != "" {
		version = &v
	}
	if t, ok := md.Type.(string); ok {
		typ = t
	}
	return version, typ
}

// KindFromMetadata maps the type of a pak.json to a kind ("" unknown).
func KindFromMetadata(typ string) string {
	switch strings.ToUpper(strings.TrimSpace(typ)) {
	case "TOOL":
		return KindTool
	case "EMU":
		return KindEmu
	}
	return ""
}
