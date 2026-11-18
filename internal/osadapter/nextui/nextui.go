// Package nextui is the adapter of NextUI, which keeps the MinUI layout
// (docs/specs/05-console-pak.md §3.1).
//
// The listing rules (systems are folders of Roms/ with a "(TAG)" suffix and
// at least one visible item, hidden and ignored files, case-insensitive
// sort) are copied from Cheat Manager, ListSystems and ListEntries in
// internal/cheats/roms.go of github.com/tfournier/nextui-cheat-manager.
// Differences: symbolic links are skipped instead of followed (the SD card
// is exFAT and has none, and a link could lead outside the SD card), and a
// folder holding a .m3u of the same name is a game, not a folder.
package nextui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/tfournier/ReminisLink/internal/osadapter"
)

const (
	// ResumeSlot is the slot NextUI writes when the console is turned off
	// in game.
	// TODO(M0): MinUI convention (slot 9), not yet checked on NextUI.
	ResumeSlot = 9
	// MaxFolderDepth bounds the recursion when counting games.
	MaxFolderDepth = 16
	// maxM3UBytes bounds the .m3u files read to find the discs.
	maxM3UBytes = 64 << 10
	// OSName is the "os" value of the API and of the Bonjour TXT record.
	OSName = "nextui"
	// ModelDesktop is the model reported off the console.
	ModelDesktop = "desktop"
)

// knownModels are the values of the NextUI DEVICE variable the API knows
// (Model enum of protocol/openapi.yaml).
// TODO(M0): check the exact DEVICE values exported by NextUI.
var knownModels = map[string]bool{
	"brick": true, "brickpro": true, "smartpro": true, "smartpros": true, "h700": true, "my355": true,
}

var (
	tagRe      = regexp.MustCompile(`\(([^()]*)\)\s*$`)
	leadingNum = regexp.MustCompile(`^\d+[)._ -]+`)
	// stateRe matches "<ROM file name>.st<slot>".
	// TODO(M0): MinUI naming, number of slots not yet checked on NextUI.
	stateRe = regexp.MustCompile(`^(.+)\.st(\d{1,2})$`)
)

// ignoredExt are the files of a ROM folder that are not games (Cheat
// Manager list).
var ignoredExt = map[string]bool{
	".txt": true, ".png": true, ".jpg": true, ".jpeg": true, ".bmp": true, ".xml": true,
	".json": true, ".log": true, ".db": true, ".srm": true, ".sav": true, ".state": true,
	".cht": true, ".bak": true, ".tmp": true, ".pdf": true, ".md": true,
}

// Adapter is the NextUI adapter.
type Adapter struct {
	roots osadapter.Roots
	env   Env
}

var _ osadapter.Adapter = (*Adapter)(nil)

// New returns the adapter for these roots. env gives DEVICE and PLATFORM
// (nil: no variable set).
func New(roots osadapter.Roots, env Env) *Adapter {
	if env == nil {
		env = func(string) string { return "" }
	}
	return &Adapter{roots: roots, env: env}
}

// Name returns "nextui".
func (a *Adapter) Name() string { return OSName }

// Roots returns the SD card folders.
func (a *Adapter) Roots() osadapter.Roots { return a.roots }

func hidden(name string) bool { return strings.HasPrefix(name, ".") }

// fold is the key of the case-insensitive comparisons (exFAT ignores case).
// No Unicode normalization: NFC and NFD spellings stay different.
func fold(s string) string { return strings.ToLower(s) }

func lessFold(a, b string) bool {
	if fa, fb := fold(a), fold(b); fa != fb {
		return fa < fb
	}
	return a < b
}

func isSymlink(de fs.DirEntry) bool { return de.Type()&fs.ModeSymlink != 0 }

// parseSystem returns the system stored in a folder of Roms/.
func parseSystem(folder string) (osadapter.System, bool) {
	if hidden(folder) || strings.ContainsAny(folder, `/\`) {
		return osadapter.System{}, false
	}
	m := tagRe.FindStringSubmatch(folder)
	if m == nil || m[1] == "" {
		return osadapter.System{}, false
	}
	label := leadingNum.ReplaceAllString(folder, "")
	label = strings.TrimSpace(tagRe.ReplaceAllString(label, ""))
	if label == "" {
		label = folder
	}
	return osadapter.System{Folder: folder, Label: label, Tag: m[1]}, true
}

// Systems lists the folders of Roms/ with a tag and a visible item.
func (a *Adapter) Systems() ([]osadapter.System, error) {
	des, err := os.ReadDir(a.roots.Roms)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list systems: %w", err)
	}
	var out []osadapter.System
	for _, de := range des {
		if !de.IsDir() {
			continue
		}
		sys, ok := parseSystem(de.Name())
		if !ok || !hasVisible(filepath.Join(a.roots.Roms, de.Name())) {
			continue
		}
		out = append(out, sys)
	}
	sort.Slice(out, func(i, j int) bool {
		if fold(out[i].Label) != fold(out[j].Label) {
			return lessFold(out[i].Label, out[j].Label)
		}
		return lessFold(out[i].Folder, out[j].Folder)
	})
	return out, nil
}

// System returns the system of a folder of Roms/.
func (a *Adapter) System(folder string) (osadapter.System, error) {
	sys, ok := parseSystem(folder)
	if !ok {
		return osadapter.System{}, osadapter.ErrNotFound
	}
	dir := filepath.Join(a.roots.Roms, folder)
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() || !hasExactEntry(a.roots.Roms, folder) || !hasVisible(dir) {
		return osadapter.System{}, osadapter.ErrNotFound
	}
	return sys, nil
}

// hasExactEntry reports whether dir holds an entry named exactly name.
// exFAT (and APFS) find a file whatever the case (and, on APFS, the Unicode
// normalization) of the name asked; identifiers must match the bytes on
// the card so that a file has a single identifier
// (docs/specs/03-protocole.md §5).
func hasExactEntry(dir, name string) bool {
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	defer f.Close()
	for {
		names, err := f.Readdirnames(256)
		for _, n := range names {
			if n == name {
				return true
			}
		}
		if err != nil {
			return false
		}
	}
}

func hasVisible(dir string) bool {
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	defer f.Close()
	for {
		names, err := f.Readdirnames(32)
		for _, n := range names {
			if !hidden(n) {
				return true
			}
		}
		if err != nil {
			return false
		}
	}
}

// m3uIn returns the name of the .m3u named after folder among its entries.
func m3uIn(folder string, entries []fs.DirEntry) (string, bool) {
	want := fold(folder + ".m3u")
	for _, e := range entries {
		if e.Type().IsRegular() && fold(e.Name()) == want {
			return e.Name(), true
		}
	}
	return "", false
}

func isGameFile(name string) bool {
	return !hidden(name) && !ignoredExt[fold(filepath.Ext(name))]
}

func displayName(fileName string) string {
	if n := strings.TrimSuffix(fileName, filepath.Ext(fileName)); n != "" {
		return n
	}
	return fileName
}

func fileKind(name string) osadapter.GameKind {
	if fold(filepath.Ext(name)) == ".m3u" {
		return osadapter.KindM3U
	}
	return osadapter.KindFile
}

// CountGames counts the games of a system, subfolders included.
func (a *Adapter) CountGames(sys osadapter.System) (int, error) {
	dir := filepath.Join(a.roots.Roms, sys.Folder)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("count games: %w", withoutPath(err))
	}
	return countGames(dir, entries, 0), nil
}

func countGames(dir string, entries []fs.DirEntry, depth int) int {
	n := 0
	for _, e := range entries {
		name := e.Name()
		if hidden(name) || isSymlink(e) {
			continue
		}
		if e.IsDir() {
			if depth >= MaxFolderDepth {
				continue
			}
			sub := filepath.Join(dir, name)
			subEntries, err := os.ReadDir(sub)
			if err != nil {
				continue
			}
			if _, ok := m3uIn(name, subEntries); ok {
				n++
				continue
			}
			n += countGames(sub, subEntries, depth+1)
			continue
		}
		if e.Type().IsRegular() && isGameFile(name) {
			n++
		}
	}
	return n
}

// systemIndex holds one directory read of each data folder of a system, so
// a listing never stats the save, states or cheats of each game.
type systemIndex struct {
	saves  map[string]bool         // folded file names of Saves/<TAG>
	states map[string]map[int]bool // folded ROM file name -> slots
	cheats map[string]string       // folded file name -> file name in Cheats/<TAG>
	media  map[string]bool         // folded file names of <folder>/.media
}

func readNames(dir string) []fs.DirEntry {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	return des
}

func (a *Adapter) index(sys osadapter.System, dir string) systemIndex {
	idx := systemIndex{
		saves:  map[string]bool{},
		states: map[string]map[int]bool{},
		cheats: map[string]string{},
		media:  map[string]bool{},
	}
	for _, e := range readNames(filepath.Join(a.roots.Saves, sys.Tag)) {
		if e.Type().IsRegular() {
			idx.saves[fold(e.Name())] = true
		}
	}
	for _, e := range readNames(filepath.Join(a.roots.States, sys.Tag)) {
		if !e.Type().IsRegular() {
			continue
		}
		if rom, slot, ok := parseState(e.Name()); ok {
			k := fold(rom)
			if idx.states[k] == nil {
				idx.states[k] = map[int]bool{}
			}
			idx.states[k][slot] = true
		}
	}
	for _, e := range readNames(filepath.Join(a.roots.Cheats, sys.Tag)) {
		if e.Type().IsRegular() {
			idx.cheats[fold(e.Name())] = e.Name()
		}
	}
	for _, e := range readNames(filepath.Join(dir, ".media")) {
		if e.Type().IsRegular() {
			idx.media[fold(e.Name())] = true
		}
	}
	return idx
}

func parseState(name string) (rom string, slot int, ok bool) {
	m := stateRe.FindStringSubmatch(name)
	if m == nil {
		return "", 0, false
	}
	slot, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, false
	}
	return m[1], slot, true
}

// checkFolderPath reports whether rel (relative to Roms/) is the system
// folder or under it, without hidden components.
func checkFolderPath(sys osadapter.System, rel string) bool {
	if rel != sys.Folder && !strings.HasPrefix(rel, sys.Folder+"/") {
		return false
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || hidden(part) {
			return false
		}
	}
	return true
}

// ListFolder lists the subfolders and games of a folder of a system.
func (a *Adapter) ListFolder(sys osadapter.System, rel string) (osadapter.Listing, error) {
	if !checkFolderPath(sys, rel) {
		return osadapter.Listing{}, osadapter.ErrNotFound
	}
	dir := filepath.Join(a.roots.Roms, filepath.FromSlash(rel))
	if err := a.checkPlainFolders(sys, rel, true); err != nil {
		return osadapter.Listing{}, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return osadapter.Listing{}, osadapter.ErrNotFound
		}
		return osadapter.Listing{}, fmt.Errorf("list folder: %w", withoutPath(err))
	}
	idx := a.index(sys, dir)
	listing := osadapter.Listing{Folders: []osadapter.Folder{}, Games: []osadapter.GameSummary{}}
	for _, e := range entries {
		name := e.Name()
		if hidden(name) || isSymlink(e) {
			continue
		}
		childRel := rel + "/" + name
		if e.IsDir() {
			sub := filepath.Join(dir, name)
			subEntries, err := os.ReadDir(sub)
			if err != nil {
				continue
			}
			if m3u, ok := m3uIn(name, subEntries); ok {
				g := osadapter.Game{
					System: sys, Path: childRel, FileName: name, Name: name,
					Kind: osadapter.KindFolderM3U, ROMName: m3u,
				}
				s := idx.summary(a, g)
				for _, se := range subEntries {
					if !se.Type().IsRegular() || hidden(se.Name()) {
						continue
					}
					if info, err := se.Info(); err == nil {
						s.Size += info.Size()
						if se.Name() == m3u {
							s.ModTime = info.ModTime().UTC()
						}
					}
				}
				listing.Games = append(listing.Games, s)
				continue
			}
			listing.Folders = append(listing.Folders, osadapter.Folder{
				Path: childRel, Name: name, GameCount: countGames(sub, subEntries, strings.Count(childRel, "/")),
			})
			continue
		}
		if !e.Type().IsRegular() || !isGameFile(name) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		g := osadapter.Game{
			System: sys, Path: childRel, FileName: name, Name: displayName(name),
			Kind: fileKind(name), ROMName: name,
		}
		s := idx.summary(a, g)
		s.Size = info.Size()
		s.ModTime = info.ModTime().UTC()
		listing.Games = append(listing.Games, s)
	}
	sort.Slice(listing.Folders, func(i, j int) bool { return lessFold(listing.Folders[i].Name, listing.Folders[j].Name) })
	sort.Slice(listing.Games, func(i, j int) bool { return lessFold(listing.Games[i].FileName, listing.Games[j].FileName) })
	return listing, nil
}

func (idx systemIndex) summary(a *Adapter, g osadapter.Game) osadapter.GameSummary {
	s := osadapter.GameSummary{Game: g}
	s.HasSave = idx.saves[fold(g.ROMName+".sav")]
	s.StateCount = len(idx.states[fold(g.ROMName)])
	if name, ok := idx.cheats[fold(g.ROMName+".cht")]; ok {
		s.CheatFile = filepath.Join(a.roots.Cheats, g.System.Tag, name)
	}
	s.HasArt = idx.media[fold(g.Name+".png")]
	return s
}

// checkPlainFolders checks that the folders of rel between the system and
// the last component exist and are not game folders (folder-m3u), so a
// file inside a multi-disc folder is never a game of its own. When
// includeLast is true, the last component is checked too.
func (a *Adapter) checkPlainFolders(sys osadapter.System, rel string, includeLast bool) error {
	parts := strings.Split(rel, "/")
	end := len(parts)
	if !includeLast {
		end--
	}
	cur := filepath.Join(a.roots.Roms, sys.Folder)
	for i := 1; i < end; i++ {
		parent := cur
		cur = filepath.Join(cur, parts[i])
		fi, err := os.Lstat(cur)
		if err != nil || !fi.IsDir() || !hasExactEntry(parent, parts[i]) {
			return osadapter.ErrNotFound
		}
		if _, ok := m3uIn(parts[i], readNames(cur)); ok {
			return osadapter.ErrNotFound
		}
	}
	return nil
}

// Game returns the game stored at rel (relative to Roms/).
func (a *Adapter) Game(rel string) (osadapter.Game, error) {
	parts := strings.Split(rel, "/")
	if len(parts) < 2 {
		return osadapter.Game{}, osadapter.ErrNotFound
	}
	for _, p := range parts {
		if p == "" || hidden(p) {
			return osadapter.Game{}, osadapter.ErrNotFound
		}
	}
	sys, err := a.System(parts[0])
	if err != nil {
		return osadapter.Game{}, err
	}
	if err := a.checkPlainFolders(sys, rel, false); err != nil {
		return osadapter.Game{}, err
	}
	full := filepath.Join(a.roots.Roms, filepath.FromSlash(rel))
	base := parts[len(parts)-1]
	fi, err := os.Lstat(full)
	if err != nil || !hasExactEntry(filepath.Dir(full), base) {
		return osadapter.Game{}, osadapter.ErrNotFound
	}
	switch {
	case fi.IsDir():
		m3u, ok := m3uIn(base, readNames(full))
		if !ok {
			return osadapter.Game{}, osadapter.ErrNotFound
		}
		return osadapter.Game{
			System: sys, Path: rel, FileName: base, Name: base, Kind: osadapter.KindFolderM3U, ROMName: m3u,
		}, nil
	case fi.Mode().IsRegular() && isGameFile(base):
		return osadapter.Game{
			System: sys, Path: rel, FileName: base, Name: displayName(base), Kind: fileKind(base), ROMName: base,
		}, nil
	}
	return osadapter.Game{}, osadapter.ErrNotFound
}

func (a *Adapter) gamePath(g osadapter.Game) string {
	return filepath.Join(a.roots.Roms, filepath.FromSlash(g.Path))
}

// folderOf returns the absolute folder that holds the game.
func (a *Adapter) folderOf(g osadapter.Game) string { return filepath.Dir(a.gamePath(g)) }

// SavePath is Saves/<TAG>/<ROM file name>.sav.
// TODO(M0): MinUI convention, not yet checked on NextUI (and for .m3u games).
func (a *Adapter) SavePath(g osadapter.Game) string {
	return filepath.Join(a.roots.Saves, g.System.Tag, g.ROMName+".sav")
}

// StatePath is <States>/<TAG>/<ROM file name>.st<slot>.
// TODO(M0): MinUI convention, not yet checked on NextUI.
func (a *Adapter) StatePath(g osadapter.Game, slot int) string {
	return filepath.Join(a.roots.States, g.System.Tag, g.ROMName+".st"+strconv.Itoa(slot))
}

// StateThumbnailPath returns "": where NextUI keeps the state previews is
// not known yet.
// TODO(M0): find the state thumbnails (if any) on NextUI.
func (a *Adapter) StateThumbnailPath(osadapter.Game, int) string { return "" }

// ResumeSlot returns the slot NextUI writes when turned off in game.
func (a *Adapter) ResumeSlot() (int, bool) { return ResumeSlot, true }

// CheatPath is Cheats/<TAG>/<ROM file name>.cht (as in Cheat Manager).
func (a *Adapter) CheatPath(g osadapter.Game) string {
	return filepath.Join(a.roots.Cheats, g.System.Tag, g.ROMName+".cht")
}

// ArtPath is <folder of the game>/.media/<name>.png.
// TODO(M0): .media next to the ROM for games in subfolders, and the name
// used for folder-m3u games, are not yet checked on NextUI.
func (a *Adapter) ArtPath(g osadapter.Game) string {
	return filepath.Join(a.folderOf(g), ".media", g.Name+".png")
}

// GameFiles finds the files of a game, with case-insensitive matching.
func (a *Adapter) GameFiles(g osadapter.Game) (osadapter.GameFiles, error) {
	var files osadapter.GameFiles
	main := a.gamePath(g)
	switch g.Kind {
	case osadapter.KindFile:
		f, err := a.fileAt(main)
		if err != nil {
			return files, err
		}
		files.ROM = []osadapter.File{f}
	case osadapter.KindM3U:
		f, err := a.fileAt(main)
		if err != nil {
			return files, err
		}
		files.ROM = append([]osadapter.File{f}, a.m3uDiscs(g, main)...)
	case osadapter.KindFolderM3U:
		m3u, err := a.fileAt(filepath.Join(main, g.ROMName))
		if err != nil {
			return files, err
		}
		files.ROM = []osadapter.File{m3u}
		for _, e := range readNames(main) {
			if e.Name() == g.ROMName || hidden(e.Name()) || !e.Type().IsRegular() {
				continue
			}
			if f, err := a.fileAt(filepath.Join(main, e.Name())); err == nil {
				files.ROM = append(files.ROM, f)
			}
		}
	default:
		return files, fmt.Errorf("game files: unknown kind %q", g.Kind)
	}
	files.Save = a.findFold(filepath.Join(a.roots.Saves, g.System.Tag), g.ROMName+".sav")
	files.Cheats = a.findFold(filepath.Join(a.roots.Cheats, g.System.Tag), g.ROMName+".cht")
	files.Art = a.findFold(filepath.Join(a.folderOf(g), ".media"), g.Name+".png")
	statesDir := filepath.Join(a.roots.States, g.System.Tag)
	want := fold(g.ROMName)
	for _, e := range readNames(statesDir) {
		if !e.Type().IsRegular() {
			continue
		}
		rom, slot, ok := parseState(e.Name())
		if !ok || fold(rom) != want {
			continue
		}
		if f, err := a.fileAt(filepath.Join(statesDir, e.Name())); err == nil {
			files.States = append(files.States, osadapter.StateFile{File: f, Slot: slot})
		}
	}
	sort.SliceStable(files.States, func(i, j int) bool { return files.States[i].Slot < files.States[j].Slot })
	return files, nil
}

// m3uDiscs returns the files listed by a .m3u that exist inside the system
// folder. Entries that leave the system folder are ignored.
func (a *Adapter) m3uDiscs(g osadapter.Game, m3uPath string) []osadapter.File {
	f, err := os.Open(m3uPath)
	if err != nil {
		return nil
	}
	defer f.Close()
	sysDir := filepath.Join(a.roots.Roms, g.System.Folder)
	base := filepath.Dir(m3uPath)
	seen := map[string]bool{}
	var out []osadapter.File
	sc := bufio.NewScanner(io.LimitReader(f, maxM3UBytes))
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.ReplaceAll(line, `\`, "/")
		clean := path.Clean(line)
		if path.IsAbs(clean) {
			continue
		}
		full := filepath.Join(base, filepath.FromSlash(clean))
		if !strings.HasPrefix(full, sysDir+string(filepath.Separator)) || seen[full] {
			continue
		}
		if file, err := a.fileAt(full); err == nil {
			seen[full] = true
			out = append(out, file)
		}
	}
	return out
}

// findFold returns the regular file of dir named name, ignoring case (an
// exact match wins), or nil.
func (a *Adapter) findFold(dir, name string) *osadapter.File {
	want := fold(name)
	var match string
	for _, e := range readNames(dir) {
		if !e.Type().IsRegular() || fold(e.Name()) != want {
			continue
		}
		if match == "" || e.Name() == name {
			match = e.Name()
		}
	}
	if match == "" {
		return nil
	}
	f, err := a.fileAt(filepath.Join(dir, match))
	if err != nil {
		return nil
	}
	return &f
}

// fileAt describes a regular file (symbolic links are refused).
func (a *Adapter) fileAt(p string) (osadapter.File, error) {
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return osadapter.File{}, osadapter.ErrNotFound
	}
	return osadapter.File{Path: p, Rel: a.relToSD(p), Size: fi.Size(), ModTime: fi.ModTime().UTC()}, nil
}

// relToSD returns p relative to the SD card root with "/" separators.
func (a *Adapter) relToSD(p string) string {
	rel, err := filepath.Rel(a.roots.SD, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		// A root moved outside the SD card by an environment variable.
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(rel)
}

// DeviceInfo returns the model and platform from the NextUI environment
// (DEVICE, PLATFORM). The OS version and the battery are unknown.
func (a *Adapter) DeviceInfo() (osadapter.DeviceInfo, error) {
	model := a.env("DEVICE")
	if !knownModels[model] {
		model = ModelDesktop
	}
	platform := a.env("PLATFORM")
	if platform == "" {
		platform = DefaultPlatform
	}
	return osadapter.DeviceInfo{
		Model:    model,
		Platform: platform,
		OS:       OSName,
		// TODO(M0): read the NextUI version (file on the SD card) and the
		// battery (/sys/class/power_supply/*/capacity and status) once
		// their location is checked; null until then.
		OSVersion: nil,
		Battery:   nil,
	}, nil
}

// withoutPath drops the path of an *fs.PathError so the error never shows
// the SD card layout.
func withoutPath(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// modelLabels are the display names of the models.
var modelLabels = map[string]string{
	"brick": "Brick", "brickpro": "Brick Pro", "smartpro": "Smart Pro", "smartpros": "Smart Pro S",
	"h700": "H700", "my355": "MY355", ModelDesktop: "Desktop",
}

// DefaultDeviceName is the name of a console that was never renamed:
// "<Model> <last 4 characters of the device id>" (docs/specs/03-protocole.md §2).
func DefaultDeviceName(model, deviceID string) string {
	label, ok := modelLabels[model]
	if !ok {
		label = "ReminisLink"
	}
	if len(deviceID) < 4 {
		return label
	}
	return label + " " + deviceID[len(deviceID)-4:]
}
