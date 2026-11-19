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
	"context"
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
	// ResumeSlot is the slot NextUI writes when the console sleeps or is
	// turned off in game (AUTO_RESUME_SLOT of NextUI defines.h).
	ResumeSlot = 9
	// ThumbnailSlots: NextUI saves a thumbnail for the slots 0 to 7 only
	// (the user slots of its menu).
	ThumbnailSlots = 8
	// maxPakScriptBytes bounds the launch.sh read to find the core.
	maxPakScriptBytes = 64 << 10
	// MaxFolderDepth bounds the recursion when counting games.
	MaxFolderDepth = 16
	// maxM3UBytes bounds the .m3u files read to find the discs.
	maxM3UBytes = 64 << 10
	// OSName is the "os" value of the API and of the Bonjour TXT record.
	OSName = "nextui"
	// ModelDesktop is the model reported off the console.
	ModelDesktop = "desktop"
	// WirelessInterface is the Wi-Fi interface of the console
	// (wifi_init.sh of NextUI tg5040).
	WirelessInterface = "wlan0"
)

// knownModels are the values of the NextUI DEVICE variable the API knows
// (Model enum of protocol/openapi.yaml).
// NextUI exports brick, brickpro or smartpro on tg5040 (MinUI.pak/launch.sh);
// the other values are kept for the platforms to come.
var knownModels = map[string]bool{
	"brick": true, "brickpro": true, "smartpro": true, "smartpros": true, "h700": true, "my355": true,
}

var (
	tagRe      = regexp.MustCompile(`\(([^()]*)\)\s*$`)
	leadingNum = regexp.MustCompile(`^\d+[)._ -]+`)
	// stateRe matches "<ROM file name>.st<slot>" (default stateFormat).
	stateRe = regexp.MustCompile(`^(.+)\.st(\d{1,2})$`)
	// emuExeRe is the core line of an emulator pak launch.sh.
	emuExeRe = regexp.MustCompile(`^\s*EMU_EXE=["']?([A-Za-z0-9_.+-]+)["']?\s*$`)
	// formatRe reads saveFormat=N and stateFormat=N of minuisettings.txt.
	formatRe = regexp.MustCompile(`^\s*(saveFormat|stateFormat|useExtractedFileName)\s*=\s*(-?\d+)\s*$`)
)

// ignoredExt are the files of a ROM folder that are not games (Cheat
// Manager list).
var ignoredExt = map[string]bool{
	".txt": true, ".png": true, ".jpg": true, ".jpeg": true, ".bmp": true, ".xml": true,
	".json": true, ".log": true, ".db": true, ".srm": true, ".sav": true, ".state": true,
	".cht": true, ".bak": true, ".tmp": true, ".pdf": true, ".md": true,
	// Patches (IPS, BPS, UPS) dropped next to their ROM: 04 lists them
	// under Roms/ for GET /sd/files?type=patch, they are not games.
	".ips": true, ".bps": true, ".ups": true,
}

// Adapter is the NextUI adapter.
type Adapter struct {
	roots osadapter.Roots
	env   Env
	// powerSupply is the sysfs folder of the power supplies
	// (DefaultPowerSupplyDir; tests use a fake one).
	powerSupply string
}

// DefaultPowerSupplyDir holds the battery and USB power supplies.
const DefaultPowerSupplyDir = "/sys/class/power_supply"

// SetPowerSupplyDir changes the sysfs folder of the power supplies (tests).
func (a *Adapter) SetPowerSupplyDir(dir string) { a.powerSupply = dir }

var _ osadapter.Adapter = (*Adapter)(nil)

// New returns the adapter for these roots. env gives DEVICE and PLATFORM
// (nil: no variable set).
func New(roots osadapter.Roots, env Env) *Adapter {
	if env == nil {
		env = func(string) string { return "" }
	}
	a := &Adapter{roots: roots, env: env, powerSupply: DefaultPowerSupplyDir}
	if !knownModels[env("DEVICE")] {
		// Off the console (desktop and headless modes on a fake SD card),
		// the battery is read from <SD>/.sys/class/power_supply when the
		// card has one: the screenshots show a battery that way.
		a.powerSupply = filepath.Join(roots.SD, ".sys", "class", "power_supply")
	}
	return a
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

// validTag reports whether a tag can name the save, state and cheat
// folders: not empty, not "." nor "..", no leading or trailing space, no
// path separator.
func validTag(tag string) bool {
	return tag != "" && tag != "." && tag != ".." && strings.TrimSpace(tag) == tag && !strings.ContainsAny(tag, `/\`)
}

// parseSystem returns the system stored in a folder of Roms/.
func parseSystem(folder string) (osadapter.System, bool) {
	if hidden(folder) || strings.ContainsAny(folder, `/\`) {
		return osadapter.System{}, false
	}
	m := tagRe.FindStringSubmatch(folder)
	if m == nil || !validTag(m[1]) {
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
	return namedIn(folder+".m3u", entries)
}

func namedIn(name string, entries []fs.DirEntry) (string, bool) {
	want := fold(name)
	for _, e := range entries {
		if e.Type().IsRegular() && fold(e.Name()) == want {
			return e.Name(), true
		}
	}
	return "", false
}

// folderGame reports whether a folder is a game, as NextUI resolves it
// (nextui.c openDirectory, readyResumePath and openRom, minarch
// ma_game.c): a .cue named after the folder is launched first, but when a
// .m3u named after the folder exists, the save and states are named after
// the .m3u (folder-m3u); with the .cue only, after the .cue (folder-cue).
// romName is the file the data is named after.
func folderGame(folder string, entries []fs.DirEntry) (romName string, kind osadapter.GameKind, ok bool) {
	if m3u, ok := m3uIn(folder, entries); ok {
		return m3u, osadapter.KindFolderM3U, true
	}
	if cue, ok := namedIn(folder+".cue", entries); ok {
		return cue, osadapter.KindFolderCue, true
	}
	return "", "", false
}

// IsGameFile reports whether a file named name is listed as a game.
func (a *Adapter) IsGameFile(name string) bool { return isGameFile(name) }

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
	discs := m3uSiblings(dir, entries)
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
			if _, _, ok := folderGame(name, subEntries); ok {
				n++
				continue
			}
			n += countGames(sub, subEntries, depth+1)
			continue
		}
		if e.Type().IsRegular() && isGameFile(name) && !discs[fold(name)] {
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
	statesDir := a.statesDir(sys.Tag)
	var stateEntries []fs.DirEntry
	if statesDir != "" {
		stateEntries = readNames(statesDir)
	}
	for _, e := range stateEntries {
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
	discs := m3uSiblings(dir, entries)
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
			if romName, kind, ok := folderGame(name, subEntries); ok {
				g := osadapter.Game{
					System: sys, Path: childRel, FileName: name, Name: name,
					Kind: kind, ROMName: romName,
				}
				s := idx.summary(a, g)
				for _, se := range subEntries {
					if !se.Type().IsRegular() || hidden(se.Name()) {
						continue
					}
					if info, err := se.Info(); err == nil {
						s.Size += info.Size()
						if se.Name() == romName {
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
		if !e.Type().IsRegular() || !isGameFile(name) || discs[fold(name)] {
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
		if g.Kind == osadapter.KindM3U {
			// Same total as the ROM files of GET /games/{gameId}.
			for _, d := range a.m3uDiscs(g, filepath.Join(dir, name), map[string][]fs.DirEntry{dir: entries}) {
				s.Size += d.Size
			}
		}
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
	s.HasArt = idx.media[fold(artName(g)+".png")]
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
		if _, _, ok := folderGame(parts[i], readNames(cur)); ok {
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
		romName, kind, ok := folderGame(base, readNames(full))
		if !ok {
			return osadapter.Game{}, osadapter.ErrNotFound
		}
		return osadapter.Game{
			System: sys, Path: rel, FileName: base, Name: base, Kind: kind, ROMName: romName,
		}, nil
	case fi.Mode().IsRegular() && isGameFile(base):
		if m3uSiblings(filepath.Dir(full), readNames(filepath.Dir(full)))[fold(base)] {
			return osadapter.Game{}, osadapter.ErrNotFound // a disc of a .m3u game
		}
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

// SavePath is Saves/<TAG>/<ROM file name>.sav (minarch ma_saves.c, default
// saveFormat; a multi-disc game is named after its .m3u).
func (a *Adapter) SavePath(g osadapter.Game) string {
	return filepath.Join(a.roots.Saves, g.System.Tag, g.ROMName+".sav")
}

// StatePath is <shared userdata>/<TAG>-<core>/<ROM file name>.st<slot>
// (minarch ma_core.c and ma_saves.c, default stateFormat), "" when the
// core of the system is unknown.
func (a *Adapter) StatePath(g osadapter.Game, slot int) string {
	dir := a.statesDir(g.System.Tag)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, g.ROMName+".st"+strconv.Itoa(slot))
}

// emuPakDirs are the folders of the emulator paks, in the order NextUI
// looks them up (getEmuPath in utils.c): the extra paks of
// <SD>/Emus/<platform>, then the system paks of .system/<platform>/paks/Emus.
func (a *Adapter) emuPakDirs() []string {
	return []string{a.roots.Emus, filepath.Join(a.roots.System, "paks", "Emus")}
}

// EmulatorInstalled reports whether an emulator pak <TAG>.pak/launch.sh
// exists in one of emuPakDirs (the condition NextUI puts on showing a
// system, nextui.c hasEmu).
func (a *Adapter) EmulatorInstalled(tag string) bool {
	if !validTag(tag) {
		return false
	}
	for _, dir := range a.emuPakDirs() {
		if fi, err := os.Stat(filepath.Join(dir, tag+".pak", "launch.sh")); err == nil && fi.Mode().IsRegular() {
			return true
		}
	}
	return false
}

// pakCore returns the libretro core of the emulator pak of a tag: the
// EMU_EXE value of <TAG>.pak/launch.sh, looked up like NextUI launches a
// game (getEmuPath in utils.c): the extra paks of <SD>/Emus/<platform>
// first, then the system paks of .system/<platform>/paks/Emus. minarch
// names the core after "<EMU_EXE>_libretro.so" cut at its last "_", that
// is EMU_EXE itself.
//
// found reports that a pak exists; core is "" when its launch.sh cannot be
// read or names no core.
func (a *Adapter) pakCore(tag string) (core string, found bool) {
	for _, dir := range a.emuPakDirs() {
		script := filepath.Join(dir, tag+".pak", "launch.sh")
		f, err := os.Open(script)
		if err != nil {
			if _, serr := os.Stat(script); serr == nil {
				return "", true // there, but unreadable
			}
			continue
		}
		sc := bufio.NewScanner(io.LimitReader(f, maxPakScriptBytes))
		for sc.Scan() {
			if m := emuExeRe.FindStringSubmatch(strings.TrimRight(sc.Text(), "\r")); m != nil {
				core = m[1]
				break
			}
		}
		f.Close()
		if core != "" && !strings.HasPrefix(core, ".") {
			return core, true
		}
		// A pak whose core cannot be read: NextUI would still use it, so
		// do not look at the next folder.
		return "", true
	}
	return "", false
}

// statesDir returns the state folder of a tag: <shared>/<TAG>-<core>,
// with the core of its emulator pak. Without any pak (a card copied
// without its .system folder), a single existing <TAG>-* folder is used.
// "" when it cannot be found, and when a pak exists but its core cannot
// be read from its launch.sh (a custom pak choosing its core in a shell
// variable): NextUI then uses that unknown core, so the folder of another
// core is never guessed (Codex review of M5); the states of the tag are
// neither written nor restored.
func (a *Adapter) statesDir(tag string) string {
	core, pak := a.pakCore(tag)
	if core != "" {
		return filepath.Join(a.roots.States, tag+"-"+core)
	}
	if pak {
		return ""
	}
	found := ""
	for _, e := range readNames(a.roots.States) {
		rest, ok := strings.CutPrefix(e.Name(), tag+"-")
		if !ok || rest == "" || !e.IsDir() || strings.Contains(rest, "-") {
			continue
		}
		if found != "" {
			return "" // several cores: cannot choose
		}
		found = filepath.Join(a.roots.States, e.Name())
	}
	return found
}

// settings are the format settings of minuisettings.txt.
type settings struct {
	save, state int
	extracted   bool // useExtractedFileName=1
}

// formats reads saveFormat, stateFormat and useExtractedFileName in
// minuisettings.txt (0, the NextUI defaults, when absent).
func (a *Adapter) formats() settings {
	var out settings
	f, err := os.Open(filepath.Join(a.roots.States, SettingsFileName))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(io.LimitReader(f, maxPakScriptBytes))
	for sc.Scan() {
		if m := formatRe.FindStringSubmatch(strings.TrimRight(sc.Text(), "\r")); m != nil {
			n, _ := strconv.Atoi(m[2])
			switch m[1] {
			case "saveFormat":
				out.save = n
			case "stateFormat":
				out.state = n
			default:
				out.extracted = n == 1
			}
		}
	}
	return out
}

// checkExtracted refuses a zipped game when useExtractedFileName=1: minarch
// then names its save and states after the file inside the zip (when the
// core does not read zips), which the adapter cannot know.
func checkExtracted(st settings, g osadapter.Game) error {
	if st.extracted && fold(filepath.Ext(g.ROMName)) == ".zip" {
		return fmt.Errorf("%w: useExtractedFileName=1 in %s names the data of a .zip game after its extracted file",
			osadapter.ErrUnsupportedFormat, SettingsFileName)
	}
	return nil
}

// CheckSaveFormat accepts the default saveFormat (0: <ROM file>.sav).
// The other formats (.srm, compressed or not, and <name>.sav) are refused,
// and so is a zipped game with useExtractedFileName=1.
func (a *Adapter) CheckSaveFormat(g osadapter.Game) error {
	st := a.formats()
	if st.save != 0 {
		return fmt.Errorf("%w: saveFormat=%d in %s", osadapter.ErrUnsupportedFormat, st.save, SettingsFileName)
	}
	return checkExtracted(st, g)
}

// CheckStateFormat accepts the default stateFormat (0: <ROM file>.st<N>).
// The other formats (.state.<N>, .state<N>, .state.auto) are refused, and
// so is a zipped game with useExtractedFileName=1.
func (a *Adapter) CheckStateFormat(g osadapter.Game) error {
	st := a.formats()
	if st.state != 0 {
		return fmt.Errorf("%w: stateFormat=%d in %s", osadapter.ErrUnsupportedFormat, st.state, SettingsFileName)
	}
	return checkExtracted(st, g)
}

// StateSideFiles are <shared>/.minui/<TAG>/<ROM file name>.<slot>.bmp
// (thumbnail) and .<slot>.txt (disc of a multi-disc state).
func (a *Adapter) StateSideFiles(g osadapter.Game, slot int) []string {
	base := filepath.Join(a.roots.Minui, g.System.Tag, g.ROMName+"."+strconv.Itoa(slot))
	return []string{base + ".bmp", base + ".txt"}
}

// stateSideRe matches the side files of the states of a ROM in .minui/<TAG>:
// "<rom>.<N>.bmp", "<rom>.<N>.txt" and "<rom>.txt" (last slot used).
var stateSideRe = regexp.MustCompile(`^(.+?)(\.\d{1,2}\.(bmp|txt)|\.txt)$`)

// GameExists looks for a regular file named romName (ignoring case) in the
// system folders of the tag, subfolders included (a .m3u inside a
// multi-disc folder too). Hidden entries and links are skipped.
func (a *Adapter) GameExists(tag, romName string) bool {
	want := fold(romName)
	for _, e := range readNames(a.roots.Roms) {
		sys, ok := parseSystem(e.Name())
		if !ok || !e.IsDir() || sys.Tag != tag {
			continue
		}
		root := filepath.Join(a.roots.Roms, e.Name())
		found := false
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || found {
				return filepath.SkipDir
			}
			if p != root && hidden(d.Name()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if rel, _ := filepath.Rel(root, p); strings.Count(filepath.ToSlash(rel), "/") >= MaxFolderDepth {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type().IsRegular() && fold(d.Name()) == want {
				found = true
				return filepath.SkipAll
			}
			return nil
		})
		if found {
			return true
		}
	}
	return false
}

// stateTags returns the tags whose states may exist: the tags of the
// system folders and of the emulator paks.
func (a *Adapter) stateTags() []string {
	seen := map[string]bool{}
	var out []string
	add := func(tag string) {
		if validTag(tag) && !seen[tag] {
			seen[tag] = true
			out = append(out, tag)
		}
	}
	for _, e := range readNames(a.roots.Roms) {
		if sys, ok := parseSystem(e.Name()); ok && e.IsDir() {
			add(sys.Tag)
		}
	}
	for _, dir := range []string{a.roots.Emus, filepath.Join(a.roots.System, "paks", "Emus")} {
		for _, e := range readNames(dir) {
			if tag, ok := strings.CutSuffix(e.Name(), ".pak"); ok && e.IsDir() {
				add(tag)
			}
		}
	}
	sort.Strings(out)
	return out
}

// StateFiles lists the states of the state folder of every known tag (the
// tags of the system folders and of the emulator paks), found with the
// core of the tag's emulator pak: the folders of other cores are ignored,
// and a core name is never guessed from a folder name.
func (a *Adapter) StateFiles(ctx context.Context) ([]osadapter.StateRef, error) {
	var out []osadapter.StateRef
	seen := map[string]bool{}
	for _, tag := range a.stateTags() {
		dir := a.statesDir(tag)
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		for _, e := range readNames(dir) {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			if !e.Type().IsRegular() {
				continue
			}
			rom, slot, ok := parseState(e.Name())
			if !ok {
				continue
			}
			f, err := a.fileAt(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			out = append(out, osadapter.StateRef{Tag: tag, ROMName: rom, Slot: slot, File: f})
		}
	}
	return out, nil
}

// StateThumbnailPath is <shared>/.minui/<TAG>/<ROM file name>.<slot>.bmp
// (minarch ma_menu.c), slots 0 to 7 only. The file holds a PNG image
// despite its extension.
func (a *Adapter) StateThumbnailPath(g osadapter.Game, slot int) string {
	if slot < 0 || slot >= ThumbnailSlots {
		return ""
	}
	return filepath.Join(a.roots.Minui, g.System.Tag, g.ROMName+"."+strconv.Itoa(slot)+".bmp")
}

// ResumeSlot returns the slot NextUI writes when it sleeps or is turned
// off in game. With a non-default stateFormat the resume state is a
// ".state.auto" file the adapter does not handle: no resume slot.
func (a *Adapter) ResumeSlot() (int, bool) {
	if a.formats().state != 0 {
		return 0, false
	}
	return ResumeSlot, true
}

// CheatPath is Cheats/<TAG>/<ROM file name>.cht (as in Cheat Manager).
func (a *Adapter) CheatPath(g osadapter.Game) string {
	return filepath.Join(a.roots.Cheats, g.System.Tag, g.ROMName+".cht")
}

// ArtPath is <folder that holds the entry>/.media/<entry name without its
// last extension>.png (nextui.c:2979-2993, 3034): only the last dot is
// removed, even for the folder of a multi-disc game
// ("Sky Garden Quest Vol. 3" gives "Sky Garden Quest Vol.png").
func (a *Adapter) ArtPath(g osadapter.Game) string {
	return filepath.Join(a.folderOf(g), ".media", artName(g)+".png")
}

// artName is the name of the art of g, without ".png".
func artName(g osadapter.Game) string { return displayName(path.Base(g.Path)) }

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
		files.ROM = append([]osadapter.File{f}, a.m3uDiscs(g, main, nil)...)
	case osadapter.KindFolderM3U, osadapter.KindFolderCue:
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
	files.RTC = a.findFold(filepath.Join(a.roots.Saves, g.System.Tag), g.ROMName+".rtc")
	for _, e := range readNames(filepath.Join(a.roots.Minui, g.System.Tag)) {
		m := stateSideRe.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() || fold(m[1]) != fold(g.ROMName) {
			continue
		}
		if f, err := a.fileAt(filepath.Join(a.roots.Minui, g.System.Tag, e.Name())); err == nil {
			files.StateSide = append(files.StateSide, f)
		}
	}
	files.Cheats = a.findFold(filepath.Join(a.roots.Cheats, g.System.Tag), g.ROMName+".cht")
	files.Art = a.findFold(filepath.Join(a.folderOf(g), ".media"), artName(g)+".png")
	statesDir := a.statesDir(g.System.Tag)
	want := fold(g.ROMName)
	var stateEntries []fs.DirEntry
	if statesDir != "" {
		stateEntries = readNames(statesDir)
	}
	for _, e := range stateEntries {
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
// folder. Entries that leave the system folder are ignored. Each entry is
// matched against the listing of its folder with the case-insensitive
// comparison m3uSiblings uses to hide the discs from the folder listing,
// so a disc never vanishes from both the listing and the game. listed
// holds the folder listings already read (may be nil); size and date come
// from the listing.
// SharedFiles returns the files of g that another .m3u of its system
// lists (two playlists can share a disc). Only an m3u or folder game has
// any; the whole system folder is read, which is fine for a deletion.
func (a *Adapter) SharedFiles(g osadapter.Game) map[string]bool {
	if g.Kind == osadapter.KindFile {
		return nil
	}
	own := filepath.Join(a.gamePath(g))
	if g.Kind.IsFolder() {
		own = filepath.Join(own, g.ROMName)
	}
	files, err := a.GameFiles(g)
	if err != nil {
		return nil
	}
	mine := map[string]bool{}
	for _, f := range files.ROM {
		mine[f.Path] = true
	}
	root := filepath.Join(a.roots.Roms, g.System.Folder)
	out := map[string]bool{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p != root && hidden(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if rel, _ := filepath.Rel(root, p); strings.Count(filepath.ToSlash(rel), "/") >= MaxFolderDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || fileKind(d.Name()) != osadapter.KindM3U || p == own {
			return nil
		}
		for _, f := range a.m3uDiscs(g, p, nil) {
			if !mine[f.Path] {
				continue
			}
			out[f.Path] = true
			// The tracks of a shared .cue stay with it.
			for _, track := range cueTracks(f.Path) {
				if mine[track] {
					out[track] = true
				}
			}
		}
		return nil
	})
	return out
}

// cueFileRe matches a FILE line of a .cue sheet: FILE "name" TYPE, or an
// unquoted name.
var cueFileRe = regexp.MustCompile(`(?i)^\s*FILE\s+(?:"([^"]+)"|(\S+))`)

// cueTracks returns the files a .cue sheet names that exist next to it
// (absolute paths, names matched ignoring case); nil for another file.
func cueTracks(cue string) []string {
	if fold(filepath.Ext(cue)) != ".cue" {
		return nil
	}
	f, err := os.Open(cue)
	if err != nil {
		return nil
	}
	defer f.Close()
	dir := filepath.Dir(cue)
	entries := readNames(dir)
	var out []string
	sc := bufio.NewScanner(io.LimitReader(f, 1<<20))
	for sc.Scan() {
		m := cueFileRe.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		name := m[1] + m[2]
		if name == "" || strings.ContainsAny(name, `/\`) {
			continue
		}
		if e := matchFold(entries, name); e != nil {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

func (a *Adapter) m3uDiscs(g osadapter.Game, m3uPath string, listed map[string][]fs.DirEntry) []osadapter.File {
	sysDir := filepath.Join(a.roots.Roms, g.System.Folder)
	base := filepath.Dir(m3uPath)
	if listed == nil {
		listed = map[string][]fs.DirEntry{}
	}
	seen := map[string]bool{}
	var out []osadapter.File
	for _, clean := range readM3U(m3uPath) {
		full := filepath.Join(base, filepath.FromSlash(clean))
		if !strings.HasPrefix(full, sysDir+string(filepath.Separator)) {
			continue
		}
		dir, name := filepath.Dir(full), filepath.Base(full)
		entries, ok := listed[dir]
		if !ok {
			entries = readNames(dir)
			listed[dir] = entries
		}
		e := matchFold(entries, name)
		if e == nil {
			continue
		}
		actual := filepath.Join(dir, e.Name())
		if seen[actual] || actual == m3uPath {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		seen[actual] = true
		out = append(out, osadapter.File{Path: actual, Rel: a.relToSD(actual), Size: info.Size(), ModTime: info.ModTime().UTC()})
	}
	return out
}

// matchFold returns the regular, visible entry named name ignoring case
// (an exact match wins), or nil.
func matchFold(entries []fs.DirEntry, name string) fs.DirEntry {
	want := fold(name)
	var match fs.DirEntry
	for _, e := range entries {
		if !e.Type().IsRegular() || hidden(e.Name()) || fold(e.Name()) != want {
			continue
		}
		if match == nil || e.Name() == name {
			match = e
		}
	}
	return match
}

// readM3U returns the entries of a .m3u file as cleaned relative paths
// ("/" separator); absolute entries are dropped.
func readM3U(m3uPath string) []string {
	f, err := os.Open(m3uPath)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(io.LimitReader(f, maxM3UBytes))
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		clean := path.Clean(strings.ReplaceAll(line, `\`, "/"))
		if !path.IsAbs(clean) {
			out = append(out, clean)
		}
	}
	return out
}

// m3uSiblings returns the folded names of the files of dir that a .m3u of
// dir lists (the discs of a loose multi-disc game). They belong to that
// game and are not games of their own. A .m3u is never hidden this way.
func m3uSiblings(dir string, entries []fs.DirEntry) map[string]bool {
	var out map[string]bool
	for _, e := range entries {
		if !e.Type().IsRegular() || hidden(e.Name()) || fileKind(e.Name()) != osadapter.KindM3U {
			continue
		}
		for _, clean := range readM3U(filepath.Join(dir, e.Name())) {
			if strings.Contains(clean, "/") || clean == "." || clean == ".." || fileKind(clean) == osadapter.KindM3U {
				continue
			}
			if out == nil {
				out = map[string]bool{}
			}
			out[fold(clean)] = true
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
// (DEVICE, PLATFORM), the NextUI release (.system/version.txt) and the
// battery (sysfs).
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
		Model:     model,
		Platform:  platform,
		OS:        OSName,
		OSVersion: ReadOSVersion(filepath.Join(a.roots.SD, VersionFile)),
		Battery:   a.Battery(),
		// TODO(M0): the SSID (wpa_supplicant STATUS, 05 §6) is not read yet.
		SSID:              nil,
		WirelessInterface: WirelessInterface,
	}, nil
}

// Battery reads the battery as NextUI does (platform.c:183-188, 05 §6):
// the level in axp2202-battery/capacity; charging when axp2202-usb/online
// is 1 and axp2202-battery/time_to_full_now is above 0 (NextUI does not use
// "status"). nil when the level cannot be read (desktop mode).
func (a *Adapter) Battery() *osadapter.Battery {
	read := func(supply, name string) (int, bool) {
		data, err := os.ReadFile(filepath.Join(a.powerSupply, supply, name))
		if err != nil || len(data) > 32 {
			return 0, false
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(data)))
		return n, err == nil
	}
	percent, ok := read("axp2202-battery", "capacity")
	if !ok || percent < 0 {
		return nil
	}
	online, _ := read("axp2202-usb", "online")
	toFull, _ := read("axp2202-battery", "time_to_full_now")
	return &osadapter.Battery{Percent: min(percent, 100), Charging: online == 1 && toFull > 0}
}

// MaxOSVersionBytes bounds the osVersion string (it also goes in the
// Bonjour TXT record, which must stay short).
const MaxOSVersionBytes = 64

// ReadOSVersion returns the NextUI release as NextUI shows it in Settings >
// About (decision D4, docs/specs/m0/questions.md): the file has the
// release name on its first line (NextUI-YYYYMMDD-N) and the short git
// hash on the second; NextUI prints the whole file on one line, so the
// lines are joined without separator, e.g. "NextUI-20261013-0" and
// "39a06de" give "NextUI-20261013-039a06de". The separator may be a real
// newline or a literal "\n" (NextUI makefile:249, depending on the shell
// of the build). nil when the file is missing, empty, too long or not
// printable ASCII.
func ReadOSVersion(path string) *string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1024))
	if err != nil {
		return nil
	}
	text := strings.ReplaceAll(string(data), `\n`, "\n")
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		b.WriteString(strings.TrimSpace(line))
	}
	v := b.String()
	if v == "" || len(v) > MaxOSVersionBytes {
		return nil
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '!' || v[i] > '~' {
			return nil
		}
	}
	return &v
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
