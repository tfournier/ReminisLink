package transfer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tfournier/ReminisLink/internal/gamearchive"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/saves"
	"github.com/tfournier/ReminisLink/internal/states"
	"github.com/tfournier/ReminisLink/internal/store"
)

// Game archives (M19): GET /archives/{stagingId} previews the import of
// an uploaded archive, POST /archives/{stagingId}/import runs it as a job.
//
// Order of the import (rules 1 and 2 of CLAUDE.md):
//
//  1. The archive is checked (gamearchive.Open: entries, manifest).
//  2. The files to import are extracted into the uploads folder (the
//     ReminisLink area, same card as the game) as ar_<hex>.part, each
//     checked against the manifest's size and SHA-256 and synced. A cut
//     here leaves temporary files, deleted at the next start.
//  3. Under the write lock, the destinations are computed again for the
//     target game, by role (never from a path of the archive); the save is
//     backed up (before-import); every file replaced goes to one trash
//     entry and the new files are renamed into place, as one journaled
//     Trash.Replace (finished or rolled back after a power cut).

// JobArchiveImport is the kind of the import jobs.
const JobArchiveImport = "archive-import"

// Actions and skip reasons of an archive plan (ArchivePlanEntry).
const (
	ArchiveCreate  = "create"
	ArchiveReplace = "replace"
	ArchiveSkip    = "skip"

	SkipGameExists    = "game-exists"
	SkipLocked        = "locked"
	SkipUnsupported   = "unsupported-format"
	SkipNoStateFolder = "no-state-folder"
	SkipNoState       = "no-state"
	SkipNoTarget      = "no-target"
)

// archiveTempPrefix names the extracted files in the uploads folder.
const archiveTempPrefix = "ar_"

// busyArchives are the staging entries an import job is using (a
// DELETE /archives/{stagingId} then answers 409).
var (
	busyArchivesMu sync.Mutex
	busyArchives   = map[string]int{}
)

func markArchive(stagingID string, delta int) {
	busyArchivesMu.Lock()
	defer busyArchivesMu.Unlock()
	if busyArchives[stagingID] += delta; busyArchives[stagingID] <= 0 {
		delete(busyArchives, stagingID)
	}
}

// DeleteStagedArchive deletes a staging entry the app abandoned
// (DELETE /archives/{stagingId}): 404 when unknown, 409 while an import
// job uses it.
func (s *Service) DeleteStagedArchive(stagingID string) (*Error, error) {
	p, err := s.StagingFile(stagingID)
	if err != nil {
		return notFound("Staging file not found"), nil
	}
	busyArchivesMu.Lock()
	defer busyArchivesMu.Unlock()
	if busyArchives[stagingID] > 0 {
		return newError(http.StatusConflict, CodeConflict, "An import is using this archive"), nil
	}
	if err := os.RemoveAll(filepath.Dir(p)); err != nil {
		return nil, fmt.Errorf("staging: %w", err)
	}
	s.log.Info("staged archive deleted", "staging", stagingID)
	return nil, nil
}

// ArchiveTarget is where an archive is imported (ArchiveTarget).
type ArchiveTarget struct {
	GameID   *string `json:"gameId,omitempty"`
	SystemID *string `json:"systemId,omitempty"`
	FolderID *string `json:"folderId,omitempty"`
}

// ArchiveImportRequest is the body of POST /archives/{stagingId}/import.
type ArchiveImportRequest struct {
	Target *ArchiveTarget `json:"target"`
}

// ArchivePlanEntry is what the import does with one file.
type ArchivePlanEntry struct {
	Role      string  `json:"role"`
	Slot      *int    `json:"slot"`
	SizeBytes int64   `json:"sizeBytes"`
	Action    string  `json:"action"`
	Reason    *string `json:"reason"`
}

// ArchivePlan is the answer of GET /archives/{stagingId}.
type ArchivePlan struct {
	Manifest    gamearchive.Manifest `json:"manifest"`
	Target      *ArchiveTarget       `json:"target"`
	TargetName  *string              `json:"targetName"`
	Suggested   bool                 `json:"suggested"`
	Entries     []ArchivePlanEntry   `json:"entries"`
	ImportBytes int64                `json:"importBytes"`
}

// SkippedFile is a file left out by an import (job result).
type SkippedFile struct {
	Role   string `json:"role"`
	Slot   *int   `json:"slot,omitempty"`
	Reason string `json:"reason"`
}

// archiveTarget is a resolved target.
type archiveTarget struct {
	game    osadapter.Game
	newGame bool   // a new game made from the ROM of the archive
	dir     string // newGame: the folder of the ROM
	api     ArchiveTarget
	label   string // game name, or system label for a new game
}

// importItem is a file of the archive with what its import does.
type importItem struct {
	entry    gamearchive.Entry
	plan     ArchivePlanEntry
	dst      string   // destination (create, replace)
	replaces []string // existing files moved to the trash (replace)
}

func skipEntry(e gamearchive.Entry, reason string) importItem {
	r := reason
	return importItem{entry: e, plan: ArchivePlanEntry{Role: e.Role, Slot: e.Slot, SizeBytes: e.SizeBytes, Action: ArchiveSkip, Reason: &r}}
}

// openArchive opens and checks the archive of a staging entry. The caller
// closes the file.
func (s *Service) openArchive(stagingID string) (*gamearchive.Archive, *os.File, *Error, error) {
	p, err := s.StagingFile(stagingID)
	if err != nil {
		return nil, nil, notFound("Staging file not found"), nil
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, nil, notFound("Staging file not found"), nil
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, nil, notFound("Staging file not found"), nil
	}
	a, err := gamearchive.Open(f, fi.Size())
	if err != nil {
		f.Close()
		if fe, ok := gamearchive.IsFormatError(err); ok {
			return nil, nil, unsupportedFormat(fe.Reason), nil
		}
		return nil, nil, nil, fmt.Errorf("archive: %w", err)
	}
	return a, f, nil, nil
}

// resolveArchiveTarget resolves a given target: a game, or a folder of a
// system for a new game made from the ROM of the archive.
func (s *Service) resolveArchiveTarget(a *gamearchive.Archive, t ArchiveTarget) (*archiveTarget, *Error, error) {
	switch {
	case t.GameID != nil && t.SystemID != nil:
		return nil, badRequest("Give either target.gameId or target.systemId"), nil
	case t.GameID != nil:
		g, err := s.opts.Library.GameByID(*t.GameID)
		if err != nil {
			if apiErr := libraryError(err, "Game"); apiErr != nil {
				return nil, apiErr, nil
			}
			return nil, nil, err
		}
		id := library.EncodeID(g.Path)
		return &archiveTarget{game: g, api: ArchiveTarget{GameID: &id}, label: g.Name}, nil, nil
	case t.SystemID != nil:
		if a.Find(gamearchive.RoleROM, nil) == nil {
			return nil, unsupportedFormat("The archive holds no ROM: choose a game as the target"), nil
		}
		folder := ""
		if t.FolderID != nil {
			folder = *t.FolderID
		}
		dir, sys, err := s.opts.Library.FolderDir(*t.SystemID, folder)
		if err != nil {
			if apiErr := libraryError(err, "System"); apiErr != nil {
				return nil, apiErr, nil
			}
			return nil, nil, err
		}
		name, err := CleanFileName(a.Manifest.Source.FileName)
		if err != nil {
			return nil, invalidName("Invalid ROM file name in the archive"), nil
		}
		// A game the system lists (Knulli: one of its extensions; M19
		// final review), else the new game would never be shown.
		if !s.opts.Adapter.IsSystemGameFile(sys, name) {
			return nil, invalidName("Not a ROM file name"), nil
		}
		rel, err := s.opts.Library.RelToRoms(filepath.Join(dir, name))
		if err != nil {
			return nil, nil, err
		}
		g := osadapter.Game{
			System: sys, Path: rel, FileName: name, Name: strings.TrimSuffix(name, filepath.Ext(name)),
			Kind: osadapter.KindFile, ROMName: name,
		}
		api := ArchiveTarget{SystemID: t.SystemID, FolderID: t.FolderID}
		label := sys.Label
		return &archiveTarget{game: g, newGame: true, dir: dir, api: api, label: label}, nil, nil
	}
	return nil, badRequest("Missing target"), nil
}

// errFound stops a walk once a game was found.
var errFound = errors.New("found")

// suggestTarget chooses a target for an archive without one: a game of a
// system with the archive's tag whose data are named after the same ROM
// file name (ignoring case), else, when the archive holds a ROM, the top
// folder of the first system with that tag; nil when nothing fits.
func (s *Service) suggestTarget(ctx context.Context, a *gamearchive.Archive) *archiveTarget {
	src := a.Manifest.Source
	systems, err := s.opts.Adapter.Systems()
	if err != nil || src.SystemTag == "" {
		return nil
	}
	var tagged []osadapter.System
	for _, sys := range systems {
		if strings.EqualFold(sys.Tag, src.SystemTag) {
			tagged = append(tagged, sys)
		}
	}
	// The exact name in every system first, then another case only where
	// the file system ignores case (exFAT): on ext4 Zelda.gba and
	// zelda.gba are two games with their own data (Codex review).
	var found, folded *osadapter.Game
	for _, sys := range tagged {
		_ = s.opts.Adapter.WalkGames(ctx, sys, func(g osadapter.Game) error {
			if g.ROMName == src.RomName {
				gg := g
				found = &gg
				return errFound
			}
			if folded == nil && strings.EqualFold(g.ROMName, src.RomName) && s.sameEntryAs(g, src.RomName) {
				gg := g
				folded = &gg
			}
			return nil
		})
		if found != nil {
			break
		}
	}
	if found == nil {
		found = folded
	}
	if found != nil {
		id := library.EncodeID(found.Path)
		return &archiveTarget{game: *found, api: ArchiveTarget{GameID: &id}, label: found.Name}
	}
	if len(tagged) > 0 && a.Find(gamearchive.RoleROM, nil) != nil {
		id := library.EncodeID(tagged[0].Folder)
		if t, apiErr, err := s.resolveArchiveTarget(a, ArchiveTarget{SystemID: &id}); apiErr == nil && err == nil {
			return t
		}
	}
	return nil
}

// sameEntryAs reports whether the ROM file of g is also found under name
// (another case) in its folder: its file system ignores case.
func (s *Service) sameEntryAs(g osadapter.Game, name string) bool {
	dir := filepath.Dir(filepath.Join(s.opts.Adapter.Roots().Roms, filepath.FromSlash(g.Path)))
	if g.Kind != osadapter.KindFile {
		dir = filepath.Join(s.opts.Adapter.Roots().Roms, filepath.FromSlash(g.Path))
	}
	fa, errA := os.Lstat(filepath.Join(dir, g.ROMName))
	fb, errB := os.Lstat(filepath.Join(dir, name))
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

// existingFile returns the file at p on the card, matched ignoring case
// (exFAT), "" when there is none (or p is "").
func existingFile(p string) string {
	if p == "" {
		return ""
	}
	if f := store.FindFold(filepath.Dir(p), filepath.Base(p), false); f != "" {
		return f
	}
	// The file system may find an entry under this name that the names
	// do not show (APFS: NFC and NFD; M19 final review): the real entry.
	if fb, err := os.Lstat(p); err == nil && fb.Mode().IsRegular() {
		if des, err := os.ReadDir(filepath.Dir(p)); err == nil {
			for _, d := range des {
				if fa, err := os.Lstat(filepath.Join(filepath.Dir(p), d.Name())); err == nil && os.SameFile(fa, fb) {
					return filepath.Join(filepath.Dir(p), d.Name())
				}
			}
		}
		return p
	}
	return ""
}

// planImport computes what importing a into t does, for each file of the
// manifest, from the card as it is now: the destination by role, with the
// names this console uses, and the files it replaces.
func (s *Service) planImport(a *gamearchive.Archive, t *archiveTarget) []importItem {
	ad := s.opts.Adapter
	g := t.game
	// The save and clock file as the saves routes find them.
	var files osadapter.GameFiles
	if !t.newGame {
		files, _ = ad.GameFiles(g)
	}
	saveDst := ad.SavePath(g)
	if files.Save != nil {
		saveDst = files.Save.Path
	} else if f := existingFile(saveDst); f != "" {
		saveDst = f
	}
	rtcDst := strings.TrimSuffix(saveDst, filepath.Ext(saveDst)) + ".rtc"
	if files.RTC != nil {
		rtcDst = files.RTC.Path
	}
	stateOK := map[int]bool{}
	var out []importItem
	place := func(e gamearchive.Entry, dst string, replaces ...string) importItem {
		if dst == "" {
			// This OS keeps no such file (cheats on Knulli, Codex review):
			// skipped, the rest is imported.
			return skipEntry(e, SkipUnsupported)
		}
		it := importItem{entry: e, dst: dst, plan: ArchivePlanEntry{Role: e.Role, Slot: e.Slot, SizeBytes: e.SizeBytes, Action: ArchiveCreate}}
		if cur := existingFile(dst); cur != "" {
			it.dst = cur
			it.plan.Action = ArchiveReplace
			it.replaces = append([]string{cur}, replaces...)
		}
		return it
	}
	// States first: a thumbnail follows its state.
	ordered := make([]gamearchive.Entry, 0, len(a.Entries))
	for _, e := range a.Entries {
		if e.Role == gamearchive.RoleState {
			ordered = append(ordered, e)
		}
	}
	for _, e := range a.Entries {
		if e.Role != gamearchive.RoleState {
			ordered = append(ordered, e)
		}
	}
	for _, e := range ordered {
		switch e.Role {
		case gamearchive.RoleROM:
			if !t.newGame {
				out = append(out, skipEntry(e, SkipGameExists))
				continue
			}
			out = append(out, place(e, filepath.Join(t.dir, g.FileName)))
		case gamearchive.RoleSave, gamearchive.RoleRTC:
			if ad.CheckSaveFormat(g) != nil {
				out = append(out, skipEntry(e, SkipUnsupported))
				continue
			}
			dst := saveDst
			if e.Role == gamearchive.RoleRTC {
				dst = rtcDst
			}
			out = append(out, place(e, dst))
		case gamearchive.RoleState:
			slot := *e.Slot
			p := ad.StatePath(g, slot)
			switch {
			case ad.CheckStateFormat(g) != nil:
				out = append(out, skipEntry(e, SkipUnsupported))
				continue
			case p == "":
				out = append(out, skipEntry(e, SkipNoStateFolder))
				continue
			case states.IsLocked(s.opts.Layout, g, slot):
				out = append(out, skipEntry(e, SkipLocked))
				continue
			}
			// The thumbnail of a replaced state goes with it (as the state
			// uploads do); the disc file of a multi-disc state stays.
			var sides []string
			for _, side := range ad.StateSideFiles(g, slot) {
				if f := existingFile(side); f != "" && !strings.EqualFold(filepath.Ext(f), ".txt") {
					sides = append(sides, f)
				}
			}
			it := place(e, p)
			if it.plan.Action == ArchiveReplace {
				it.replaces = append(it.replaces, sides...)
			}
			stateOK[slot] = true
			out = append(out, it)
		case gamearchive.RoleStateThumbnail:
			slot := *e.Slot
			p := ad.StateThumbnailPath(g, slot)
			switch {
			case !stateOK[slot]:
				out = append(out, skipEntry(e, SkipNoState))
			case p == "":
				out = append(out, skipEntry(e, SkipUnsupported))
			default:
				out = append(out, place(e, p))
			}
		case gamearchive.RoleCheats:
			out = append(out, place(e, ad.CheatPath(g)))
		case gamearchive.RoleArt:
			dst := ad.ArtPath(g)
			if !t.newGame {
				if art, err := ad.ArtFile(g); err == nil && art != nil {
					dst = art.Path
				}
			}
			out = append(out, place(e, dst))
		}
	}
	// Back to the order of the manifest.
	byKey := map[string]importItem{}
	for _, it := range out {
		byKey[it.entry.Key()] = it
	}
	sorted := make([]importItem, 0, len(out))
	for _, e := range a.Entries {
		sorted = append(sorted, byKey[e.Key()])
	}
	return sorted
}

// romConflict is 409 conflict when a new game cannot be made: its ROM is
// taken, or a file it would use (save, clock file, a state or its
// thumbnail, the art: computed by the adapter's own rules, as the import
// would write them) already belongs to another game of the system, whose
// data it would then share or replace (NextUI: A/Zelda.gba and
// B/Zelda.gba; Knulli: A/Zelda.gba and B/Zelda.zip both use Zelda.srm;
// Codex review). The 409 names that game (details.gameId), which the app
// offers as the target. A file no game uses (a save left by a deleted ROM)
// is not a conflict: it is replaced, backed up first.
func (s *Service) romConflict(a *gamearchive.Archive, t *archiveTarget) *Error {
	if !t.newGame {
		return nil
	}
	if existing := findFold(t.dir, t.game.FileName); existing != "" {
		e := s.conflictError(filepath.Join(t.dir, existing))
		if rel, err := s.opts.Library.RelToRoms(filepath.Join(t.dir, existing)); err == nil {
			if _, err := s.opts.Adapter.Game(rel); err == nil {
				e.Details["gameId"] = library.EncodeID(rel)
			}
		}
		return e
	}
	// Every file the new game would use, whether the archive holds it or
	// not (M19 final review: a ROM-only archive would still share the
	// save of another game): its save, clock file, art, and any state or
	// thumbnail of its slots, as the adapter names them.
	ad := s.opts.Adapter
	g := t.game
	used := map[string]bool{}
	add := func(p string) {
		if f := existingFile(p); f != "" {
			used[f] = true
		}
	}
	save := ad.SavePath(g)
	add(save)
	add(strings.TrimSuffix(save, filepath.Ext(save)) + ".rtc")
	add(ad.ArtPath(g))
	usedSlots := map[int]bool{}
	if p := ad.StatePath(g, 0); p != "" {
		// One listing of the state folder: only the slots with a file of
		// that name are looked up.
		names := map[string]int{}
		for slot := 0; slot <= gamearchive.MaxSlot; slot++ {
			for _, q := range []string{ad.StatePath(g, slot), ad.StateThumbnailPath(g, slot)} {
				if q != "" {
					names[strings.ToLower(filepath.Base(q))] = slot
				}
			}
		}
		dirs := map[string]bool{filepath.Dir(p): true}
		if th := ad.StateThumbnailPath(g, 0); th != "" {
			dirs[filepath.Dir(th)] = true
		}
		for dir := range dirs {
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if slot, ok := names[strings.ToLower(e.Name())]; ok {
					usedSlots[slot] = true
				}
			}
		}
		for slot := range usedSlots {
			add(ad.StatePath(g, slot))
			add(ad.StateThumbnailPath(g, slot))
		}
	}
	if len(used) == 0 {
		return nil
	}
	// Against every game sharing those folders: every system with the
	// same tag (NextUI: two GBA folders share Saves/GBA).
	systems, _ := ad.Systems()
	var owner *osadapter.Game
	var file string
	for _, sys := range systems {
		if owner != nil || !strings.EqualFold(sys.Tag, g.System.Tag) {
			continue
		}
		_ = ad.WalkGames(context.Background(), sys, func(other osadapter.Game) error {
			osave := ad.SavePath(other)
			paths := []string{osave, strings.TrimSuffix(osave, filepath.Ext(osave)) + ".rtc", ad.ArtPath(other)}
			for slot := range usedSlots {
				paths = append(paths, ad.StatePath(other, slot), ad.StateThumbnailPath(other, slot))
			}
			for _, q := range paths {
				f := existingFile(q)
				if f == "" {
					continue
				}
				for u := range used {
					if sameFile(f, u) {
						owner, file = &other, u
						return errFound
					}
				}
			}
			return nil
		})
	}
	if owner == nil {
		return nil
	}
	e := s.conflictError(file)
	e.Details["gameId"] = library.EncodeID(owner.Path)
	return e
}

// sameFile reports whether a and b are the same file on the card.
func sameFile(a, b string) bool {
	fa, errA := os.Lstat(a)
	fb, errB := os.Lstat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

// InspectArchive checks the archive of a staging entry and previews its
// import into target (nil: a suggested target).
func (s *Service) InspectArchive(ctx context.Context, stagingID string, target *ArchiveTarget) (ArchivePlan, *Error, error) {
	a, f, apiErr, err := s.openArchive(stagingID)
	if apiErr != nil || err != nil {
		return ArchivePlan{}, apiErr, err
	}
	defer f.Close()
	plan := ArchivePlan{Manifest: a.Manifest, Entries: []ArchivePlanEntry{}}
	var t *archiveTarget
	if target == nil {
		t = s.suggestTarget(ctx, a)
		plan.Suggested = true
	} else if t, apiErr, err = s.resolveArchiveTarget(a, *target); apiErr != nil || err != nil {
		return ArchivePlan{}, apiErr, err
	}
	if t == nil {
		for _, e := range a.Entries {
			plan.Entries = append(plan.Entries, skipEntry(e, SkipNoTarget).plan)
		}
		return plan, nil, nil
	}
	if apiErr := s.romConflict(a, t); apiErr != nil && !plan.Suggested {
		return ArchivePlan{}, apiErr, nil
	}
	api, label := t.api, t.label
	plan.Target, plan.TargetName = &api, &label
	for _, it := range s.planImport(a, t) {
		plan.Entries = append(plan.Entries, it.plan)
		if it.plan.Action != ArchiveSkip {
			plan.ImportBytes += it.plan.SizeBytes
		}
	}
	return plan, nil, nil
}

// StartArchiveImport checks an import request and queues its job, guarded
// by guard (M13: the client that asked for it).
func (s *Service) StartArchiveImport(stagingID string, req ArchiveImportRequest, guard JobGuard) (string, *Error, error) {
	if req.Target == nil {
		return "", badRequest("Missing target"), nil
	}
	a, f, apiErr, err := s.openArchive(stagingID)
	if apiErr != nil || err != nil {
		return "", apiErr, err
	}
	f.Close()
	t, apiErr, err := s.resolveArchiveTarget(a, *req.Target)
	if apiErr != nil || err != nil {
		return "", apiErr, err
	}
	if apiErr := s.romConflict(a, t); apiErr != nil {
		return "", apiErr, nil
	}
	target := *req.Target
	// The archive is reserved until the job ends, however it ends: run,
	// cancelled while queued, or dropped by a revocation (the cleanup of
	// the job, Codex review).
	markArchive(stagingID, 1)
	id, apiErr, err := s.submitWithCleanup(JobArchiveImport, t.label, guard, func(ctx context.Context, report func(int64, *int64)) (map[string]any, *Error) {
		return s.runArchiveImport(ctx, stagingID, target, report)
	}, func() { markArchive(stagingID, -1) })
	if apiErr != nil || err != nil {
		markArchive(stagingID, -1)
		return "", apiErr, err
	}
	s.log.Info("archive import queued", "job", id, "game", t.game.Path, "new", t.newGame)
	return id, nil, nil
}

func (s *Service) runArchiveImport(ctx context.Context, stagingID string, target ArchiveTarget, report func(int64, *int64)) (map[string]any, *Error) {
	cancelled := func() *Error { return newError(http.StatusServiceUnavailable, CodeShuttingDown, "Cancelled") }
	a, f, apiErr, err := s.openArchive(stagingID)
	if err != nil {
		s.log.Error("archive import: open", "error", err)
		return nil, internal("Cannot read the archive")
	}
	if apiErr != nil {
		return nil, apiErr
	}
	defer f.Close()
	t, apiErr, err := s.resolveArchiveTarget(a, target)
	if err != nil {
		s.log.Error("archive import: target", "error", err)
		return nil, internal("Internal error")
	}
	if apiErr != nil {
		return nil, apiErr
	}

	// 2. Extract what the import will need, checked, into the uploads
	// folder (same card: the final renames are atomic).
	planned := s.planImport(a, t)
	var need int64
	for _, it := range planned {
		if it.plan.Action != ArchiveSkip {
			need += it.entry.SizeBytes
		}
	}
	dir := s.opts.Layout.UploadsDir()
	if err := s.dataDir(dir); err != nil {
		return nil, internal("Cannot create the temporary files")
	}
	if apiErr := s.checkPatchSpace(dir, need); apiErr != nil {
		return nil, apiErr
	}
	report(0, &need)
	temps := map[string]string{}   // entry key → extracted file
	reasons := map[string]string{} // entry key → why it was not extracted
	keep := false                  // a pending replacement of the trash names them
	defer func() {
		if !keep {
			for _, p := range temps {
				os.Remove(p)
			}
		}
	}()
	var done int64
	var lastReport time.Time
	for _, it := range planned {
		if it.plan.Action == ArchiveSkip {
			reasons[it.entry.Key()] = *it.plan.Reason
			continue
		}
		tmp := filepath.Join(dir, archiveTempPrefix+s.randomHex(12)+partExt)
		base := done
		n, err := a.Extract(ctx, it.entry, tmp, func(n int64) {
			if time.Since(lastReport) >= 200*time.Millisecond {
				lastReport = time.Now()
				report(base+n, &need)
			}
		})
		if err != nil {
			os.Remove(tmp)
			switch {
			case ctx.Err() != nil:
				return nil, cancelled()
			case errors.Is(err, syscall.ENOSPC):
				free, _ := s.opts.FreeSpace(dir)
				return nil, insufficientStorage(required(need), free)
			}
			if fe, ok := gamearchive.IsFormatError(err); ok {
				return nil, unsupportedFormat(fe.Reason)
			}
			s.log.Error("archive import: extract", "error", err)
			return nil, internal("Cannot write the temporary files")
		}
		temps[it.entry.Key()] = tmp
		done += n
	}
	report(done, &need)
	if ctx.Err() != nil {
		return nil, cancelled()
	}

	// 3. The write lock (a job waits for its turn without a time limit),
	// then the install lock, as the patches.
	if err := s.opts.Writes.Acquire(ctx); err != nil {
		return nil, cancelled()
	}
	defer s.opts.Writes.Release()
	if !jobAllowed(ctx) {
		return nil, errJobRevoked
	}
	s.installMu.Lock()
	defer s.installMu.Unlock()
	if ctx.Err() != nil {
		return nil, cancelled()
	}
	result, apiErr, keepTemps, err := s.installArchive(a, target, temps, reasons)
	keep = keepTemps
	if err != nil {
		s.log.Error("archive import: install", "error", err)
		return nil, internal("Cannot install the files of the archive")
	}
	if apiErr != nil {
		return nil, apiErr
	}
	// The staging entry is no longer needed; a failure to delete it is
	// logged (the purge removes it after 24 h).
	if p, err := s.StagingFile(stagingID); err == nil {
		if err := os.RemoveAll(filepath.Dir(p)); err != nil {
			s.log.Warn("archive import: cannot delete the staging entry", "staging", stagingID, "error", err)
		}
	}
	return result, nil
}

// installArchive installs the extracted files (temps, by entry key), with
// the write and install locks held. keep reports that the temporary files
// must stay (a pending replacement of the trash names them).
func (s *Service) installArchive(a *gamearchive.Archive, target ArchiveTarget, temps, reasons map[string]string) (map[string]any, *Error, bool, error) {
	// The target and the destinations again: the card may have changed
	// since the extraction.
	t, apiErr, err := s.resolveArchiveTarget(a, target)
	if apiErr != nil || err != nil {
		return nil, apiErr, false, err
	}
	if apiErr := s.romConflict(a, t); apiErr != nil {
		return nil, apiErr, false, nil
	}
	g := t.game
	var items []store.MoveItem
	var installs []store.Install
	moved := map[string]bool{}
	dsts := map[string]bool{}
	skipped := []SkippedFile{}
	saveReplaced := false
	var saveCur, rtcCur string // the save and clock file replaced
	for _, it := range s.planImport(a, t) {
		tmp, extracted := temps[it.entry.Key()]
		if it.plan.Action == ArchiveSkip || !extracted {
			// Skipped now, or importable only since the extraction (then
			// with the reason it had).
			reason := reasons[it.entry.Key()]
			if it.plan.Reason != nil {
				reason = *it.plan.Reason
			}
			skipped = append(skipped, SkippedFile{Role: it.entry.Role, Slot: it.entry.Slot, Reason: reason})
			continue
		}
		// Paths as on the card (existingFile gives the real entry): on a
		// case-sensitive file system two names differing by case are two
		// files.
		key := it.dst
		if dsts[key] {
			return nil, nil, false, fmt.Errorf("archive: two files for one destination")
		}
		dsts[key] = true
		for _, p := range it.replaces {
			if !moved[p] {
				moved[p] = true
				items = append(items, store.MoveItem{Path: p})
			}
		}
		// The save and its clock file are one pair: replacing either
		// backs up both first (before-import, Codex review).
		if (it.entry.Role == gamearchive.RoleSave || it.entry.Role == gamearchive.RoleRTC) && it.plan.Action == ArchiveReplace {
			saveReplaced = true
			if it.entry.Role == gamearchive.RoleSave {
				saveCur = it.dst
			} else {
				rtcCur = it.dst
			}
		}
		// The folders of the destination are made by Trash.Replace,
		// confined to the card (never through a link, Codex review).
		installs = append(installs, store.Install{Src: tmp, Dst: it.dst, Size: it.entry.SizeBytes, SHA256: it.entry.SHA256})
	}
	// A destination that is also moved away (a stale thumbnail of a state
	// replaced, and the thumbnail of the archive) is fine: the move comes
	// first. Anything else installed over must have been moved.
	for _, in := range installs {
		if cur := existingFile(in.Dst); cur != "" && !moved[cur] {
			return nil, s.conflictError(in.Dst), false, nil
		}
	}
	var backupID any
	if saveReplaced {
		var b *saves.Backup
		var err error
		if t.newGame {
			// A save no game uses (left by a deleted ROM): backed up by
			// its path, the game does not exist yet (Codex review).
			if saveCur == "" {
				saveCur = existingFile(s.opts.Adapter.SavePath(g))
			}
			if rtcCur == "" && saveCur != "" {
				rtcCur = existingFile(strings.TrimSuffix(saveCur, filepath.Ext(saveCur)) + ".rtc")
			}
			if saveCur != "" {
				b, err = s.opts.Saves.BackupFilesLocked(g, saveCur, rtcCur, saves.ReasonBeforeImport)
			}
		} else {
			b, err = s.opts.Saves.BackupLocked(g, saves.ReasonBeforeImport)
		}
		if err != nil {
			return nil, nil, false, fmt.Errorf("archive: back up the save: %w", err)
		}
		if b != nil {
			backupID = b.ID
		}
	}
	var trashID any
	if len(installs) > 0 {
		res, err := s.opts.Trash.Replace(store.ReasonImportArchive, g.Name, items, installs)
		if err != nil {
			return nil, nil, errors.Is(err, store.ErrReplaceFailed), err
		}
		if res.Entry.ID != "" {
			trashID = res.Entry.ID
		}
	}
	s.log.Info("archive imported", "game", g.Path, "new", t.newGame, "files", len(installs), "skipped", len(skipped))
	return map[string]any{
		"gameId":       library.EncodeID(g.Path),
		"imported":     len(installs),
		"skipped":      skipped,
		"trashEntryId": trashID,
		"saveBackupId": backupID,
	}, nil, false, nil
}
