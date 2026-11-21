package transfer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/tfournier/ReminisLink/internal/confine"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/paks"
	"github.com/tfournier/ReminisLink/internal/saves"
	"github.com/tfournier/ReminisLink/internal/store"
)

// TrashRestoreResult is the outcome of RestoreTrash.
type TrashRestoreResult struct {
	store.RestoreResult
	// PakRestored: a pak folder came back (a paks event follows).
	PakRestored bool `json:"-"`
}

// RestoreTrash puts a trash entry back (M18, POST
// /trash/{entryId}/restore) under the install lock: onConflict is
// store.RestoreFail, store.RestoreRename or store.RestoreReplace. The pak
// checks and the "replace" rule are the hooks of the store (restoreCheck,
// replaceSpec), so the recovery at the start applies them too.
func (s *Service) RestoreTrash(id, onConflict string) (TrashRestoreResult, *Error, error) {
	if onConflict == store.RestoreReplace {
		if p, err := s.opts.Trash.PlanRestore(id); err == nil {
			if b, isBackup := s.backupEntry(p); isBackup {
				if b.refusal != "" {
					return TrashRestoreResult{}, &Error{Status: http.StatusUnprocessableEntity, Code: CodeUnsupportedFormat,
						Message: "Only the save of an existing game can replace the current one", Details: map[string]any{"reason": b.refusal}}, nil
				}
				return s.replaceWithBackup(id, b)
			}
		}
	}
	s.installMu.Lock()
	defer s.installMu.Unlock()
	res, err := s.opts.Trash.Restore(id, onConflict, nil)
	return s.restoreOutcome(res, err)
}

// restoreOutcome maps the outcome of store.Trash.Restore to the API.
func (s *Service) restoreOutcome(res store.RestoreResult, err error) (TrashRestoreResult, *Error, error) {
	var conflict *store.RestoreConflictError
	var notReplaceable *store.NotReplaceableError
	switch {
	case err == nil:
	case errors.Is(err, store.ErrInvalidTrashID):
		return TrashRestoreResult{}, newError(http.StatusBadRequest, CodeInvalidID, "Invalid identifier"), nil
	case errors.Is(err, store.ErrTrashNotFound), errors.Is(err, store.ErrNothingToMove):
		return TrashRestoreResult{}, notFound("Trash entry not found"), nil
	case errors.As(err, &conflict):
		details := map[string]any{"conflicts": conflict.Conflicts}
		if conflict.NoFreeName != "" {
			details["freeNameUnavailable"] = conflict.NoFreeName
		}
		return TrashRestoreResult{}, &Error{Status: http.StatusConflict, Code: CodeConflict,
			Message: "A file exists where the entry goes back", Details: details}, nil
	case errors.As(err, &notReplaceable):
		return TrashRestoreResult{}, &Error{Status: http.StatusUnprocessableEntity, Code: CodeUnsupportedFormat,
			Message: "Only the save of an existing game can replace the current one", Details: map[string]any{"reason": notReplaceable.Reason}}, nil
	default:
		if fe, ok := paks.IsFormatError(err); ok {
			return TrashRestoreResult{}, newError(http.StatusUnprocessableEntity, CodeUnsupportedFormat, fe.Reason), nil
		}
		return TrashRestoreResult{}, nil, err
	}
	pak := false
	for _, it := range res.Plan.Items {
		if it.Dir && s.paks != nil && s.paks.KindOf(filepath.Dir(it.Dst)) != "" {
			pak = true
		}
	}
	return TrashRestoreResult{RestoreResult: res, PakRestored: pak}, nil, nil
}

// restoreCheck runs before any item of a restore goes back (a request or
// the recovery at the start): a folder going to a folder of the paks goes
// through the checks of an installation (paks.CheckFolder), whatever the
// journal says of it.
func (s *Service) restoreCheck(it store.RestoreItem) error {
	if !it.Dir || s.paks == nil || s.paks.KindOf(filepath.Dir(it.Dst)) == "" {
		return nil
	}
	return paks.CheckFolder(context.Background(), it.Src)
}

// replaceSpec computes a "replace" restore with the rules of the adapter
// (NextUI: Saves/<TAG>/<ROM file>.sav; Knulli: saves/<system>/<ROM name
// without extension>.srm; both with a .rtc of the same stem): the entry
// must hold only the save (and the clock file) of one game that still
// exists, recognised by the save path the adapter gives that game, names
// compared with the case rule of the folder (M20); they go to the names
// the game uses now (its current files, or the adapter's names), after
// the current save and clock file went to the trash. It runs for the
// preview, the request and the recovery at the start alike.
func (s *Service) replaceSpec(p store.RestorePlan) (*store.ReplaceSpec, error) {
	refuse := func(reason string) (*store.ReplaceSpec, error) {
		return nil, &store.NotReplaceableError{Reason: reason}
	}
	sd := s.opts.Adapter.Roots().SD
	var saveItem, rtcItem string
	for _, it := range p.Items {
		if it.Dir || it.Internal {
			return refuse("not-a-save")
		}
		if strings.EqualFold(filepath.Ext(it.Rel), ".rtc") && rtcItem == "" {
			rtcItem = it.Rel
		} else if saveItem == "" {
			saveItem = it.Rel
		} else {
			return refuse("not-a-save")
		}
	}
	if saveItem == "" {
		return refuse("not-a-save")
	}
	g, ok := s.gameOfSave(filepath.Join(sd, filepath.FromSlash(saveItem)))
	if !ok {
		return refuse("not-a-save")
	}
	if rtcItem != "" && !s.samePlace(filepath.Join(sd, filepath.FromSlash(rtcItem)), rtcOf(s.opts.Adapter.SavePath(g))) {
		return refuse("not-a-save")
	}
	if err := s.opts.Adapter.CheckSaveFormat(g); err != nil {
		return refuse("save-format")
	}
	save, rtc := s.opts.Adapter.SaveDir(g.System.Tag).Files(g)
	savePath := s.opts.Adapter.SavePath(g)
	if save != nil {
		savePath = save.Path
	}
	rtcPath := rtcOf(savePath)
	if rtc != nil {
		rtcPath = rtc.Path
	}
	spec := &store.ReplaceSpec{Targets: map[string]string{}, Label: g.Name}
	for item, target := range map[string]string{saveItem: savePath, rtcItem: rtcPath} {
		if item == "" {
			continue
		}
		rel, err := filepath.Rel(sd, target)
		if err != nil || !filepath.IsLocal(rel) {
			return refuse("not-a-save")
		}
		spec.Targets[item] = filepath.ToSlash(rel)
	}
	if save != nil {
		spec.Displace = append(spec.Displace, save.Path)
	}
	if rtc != nil {
		spec.Displace = append(spec.Displace, rtc.Path)
	}
	return spec, nil
}

// rtcOf is the clock file next to a save: the save path without its
// extension, then ".rtc" (Game.gba.sav: Game.gba.rtc; Game.srm: Game.rtc).
func rtcOf(save string) string { return strings.TrimSuffix(save, filepath.Ext(save)) + ".rtc" }

// samePlace reports whether a and b (absolute) name the same file: equal,
// or equal ignoring case in a folder that ignores case (the M20 rule,
// store.FindFold). A missing file is compared by name in its folder.
func (s *Service) samePlace(a, b string) bool {
	if a == b {
		return true
	}
	if !strings.EqualFold(a, b) {
		return false
	}
	if !s.caseFromFS {
		return true // NextUI: exFAT ignores case
	}
	if fa, err := os.Lstat(a); err == nil {
		fb, err := os.Lstat(b)
		return err == nil && os.SameFile(fa, fb)
	}
	return store.FindFold(filepath.Dir(b), filepath.Base(a), false) != "" || foldsDir(filepath.Dir(b))
}

// foldsDir reports whether the folder dir ignores case (one of its
// entries is found under another case); true when it cannot tell.
func foldsDir(dir string) bool {
	des, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	for _, d := range des {
		other := strings.Map(func(r rune) rune {
			if unicode.IsUpper(r) {
				return unicode.ToLower(r)
			}
			return unicode.ToUpper(r)
		}, d.Name())
		if other == d.Name() {
			continue
		}
		fa, errA := os.Lstat(filepath.Join(dir, d.Name()))
		fb, errB := os.Lstat(filepath.Join(dir, other))
		return errA == nil && errB == nil && os.SameFile(fa, fb)
	}
	return true
}

// gameOfSave returns the game whose save, by the adapter's rules, is the
// file save (an exact name first, another case only in a folder that
// ignores case).
func (s *Service) gameOfSave(save string) (osadapter.Game, bool) {
	systems, err := s.opts.Adapter.Systems()
	if err != nil {
		return osadapter.Game{}, false
	}
	var fold osadapter.Game
	foundFold := false
	for _, sys := range systems {
		var exact osadapter.Game
		found := false
		_ = s.opts.Adapter.WalkGames(context.Background(), sys, func(g osadapter.Game) error {
			sp := s.opts.Adapter.SavePath(g)
			switch {
			case sp == save:
				exact, found = g, true
				return errStopWalk
			case !foundFold && strings.EqualFold(sp, save) && s.samePlace(save, sp):
				fold, foundFold = g, true
			}
			return nil
		})
		if found {
			return exact, true
		}
	}
	return fold, foundFold
}

var errStopWalk = errors.New("stop")

// PreviewTrashRestore is GET /trash/{entryId}/restore: the plan of the
// store, with "replace" also computed for the backup copy of a save
// (backupEntry), which then goes through internal/saves. What it says is
// exact: replaceable only when the restore would succeed now, the files
// that would really move away, and where they are kept.
func (s *Service) PreviewTrashRestore(id string) (store.RestorePlan, error) {
	p, err := s.opts.Trash.PlanRestore(id)
	if err != nil || p.Replaceable {
		return p, err
	}
	b, isBackup := s.backupEntry(p)
	if !isBackup {
		return p, nil
	}
	if b.refusal != "" {
		p.ReplaceUnavailable = ptr(b.refusal)
		return p, nil
	}
	sd := s.opts.Adapter.Roots().SD
	p.Replaceable, p.ReplaceUnavailable, p.ReplaceKeptAs = true, nil, b.keptAs
	p.ReplaceDisplaced = []string{}
	for _, d := range b.displaced {
		if rel, err := filepath.Rel(sd, d); err == nil {
			p.ReplaceDisplaced = append(p.ReplaceDisplaced, filepath.ToSlash(rel))
		}
	}
	for i, f := range p.Files {
		target := b.savePath
		if strings.EqualFold(filepath.Ext(f.Name), ".rtc") {
			target = b.rtcPath
		}
		if rel, err := filepath.Rel(sd, target); err == nil {
			p.Files[i].ReplaceName = ptr(filepath.ToSlash(rel))
		} else {
			p.Files[i].ReplaceName = nil
		}
	}
	return p, nil
}

func ptr[T any](v T) *T { return &v }

// backupOfSave is a trash entry holding one backup copy of a save
// (internal/saves: <backups>/<tag>/<ROM>/saves/<id>.sav, its clock file
// <id>.rtc and its metadata <id>.json), the copy deleted by the app or
// pushed out by the automatic limits: a replaced save.
type backupOfSave struct {
	game osadapter.Game
	id   string
	// refusal is why it cannot replace the current save now ("" when it
	// can): game-missing, save-format, backup-taken, place-blocked.
	refusal string
	// savePath and rtcPath are where the copy and its clock file go;
	// displaced the current files the restore moves away (the clock file
	// only when the copy has one: internal/saves leaves it otherwise);
	// keptAs where they are kept (nil when nothing moves).
	savePath, rtcPath string
	displaced         []string
	keptAs            *string
}

// backupEntry recognises such an entry (false otherwise) and computes what
// a replace would do now.
func (s *Service) backupEntry(p store.RestorePlan) (backupOfSave, bool) {
	sd := s.opts.Adapter.Roots().SD
	backups := filepath.Dir(s.opts.Layout.BackupsDir("x"))
	tag, romDir, id := "", "", ""
	copies, clocks := 0, 0
	for _, it := range p.Items {
		if it.Dir {
			return backupOfSave{}, false
		}
		rel, err := filepath.Rel(backups, filepath.Join(sd, filepath.FromSlash(it.Rel)))
		if err != nil || !filepath.IsLocal(rel) {
			return backupOfSave{}, false
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) != 4 || !strings.EqualFold(parts[2], "saves") {
			return backupOfSave{}, false
		}
		ext := strings.ToLower(filepath.Ext(parts[3]))
		bid := strings.TrimSuffix(parts[3], filepath.Ext(parts[3]))
		if !saves.ValidBackupID(bid) || (id != "" && bid != id) || (tag != "" && (tag != parts[0] || romDir != parts[1])) {
			return backupOfSave{}, false
		}
		switch {
		case ext == ".sav" && !it.Internal:
			copies++
		case ext == ".rtc" && !it.Internal:
			clocks++
		case ext == ".json" && it.Internal:
		default:
			return backupOfSave{}, false
		}
		tag, romDir, id = parts[0], parts[1], bid
	}
	if copies != 1 {
		return backupOfSave{}, false
	}
	b := backupOfSave{id: id}
	g, ok := s.findGame(tag, romDir)
	switch {
	case !ok:
		b.refusal = "game-missing"
		return b, true
	case s.opts.Adapter.CheckSaveFormat(g) != nil:
		b.refusal = "save-format"
		return b, true
	case p.Conflict:
		// The copy cannot go back to the backups of the game first.
		b.refusal = "backup-taken"
		return b, true
	}
	b.game = g
	save, rtc := s.opts.Adapter.SaveDir(g.System.Tag).Files(g)
	b.savePath = s.opts.Adapter.SavePath(g)
	if save != nil {
		b.savePath = save.Path
	}
	b.rtcPath = strings.TrimSuffix(b.savePath, filepath.Ext(b.savePath)) + ".rtc"
	if rtc != nil {
		b.rtcPath = rtc.Path
	}
	// Anything else at those places (a folder, a link), or a link on the
	// way to the save folder, would make the restore fail after the copy
	// came back.
	r := s.opts.Adapter.Roots()
	if confine.CheckDir(confine.Base(r.SD, r.Saves), filepath.Dir(b.savePath)) != nil ||
		(save == nil && occupied(b.savePath)) || (clocks > 0 && rtc == nil && occupied(b.rtcPath)) {
		b.refusal = store.ReplacePlaceBlocked
		return b, true
	}
	if save != nil {
		b.displaced = append(b.displaced, save.Path)
	}
	if rtc != nil && clocks > 0 {
		b.displaced = append(b.displaced, rtc.Path)
	}
	switch {
	case save != nil:
		b.keptAs = ptr(store.ReplaceKeptBackup)
	case len(b.displaced) > 0:
		b.keptAs = ptr(store.ReplaceKeptTrash)
	}
	return b, true
}

// occupied reports whether something holds the place p: the file
// system finds it (ignoring case on exFAT, exactly on ext4: the M20 rule),
// or the folder cannot be read.
func occupied(p string) bool {
	_, err := os.Lstat(p)
	return err == nil || !errors.Is(err, fs.ErrNotExist)
}

// findGame returns the game of a tag whose backups are in the folder
// romDir of the backups of that tag: the game of that exact ROM file name,
// or of another case only when the backups folder of the game resolves to
// romDir by the case rule of its folder (store.FindFold: never on ext4,
// where Game.gba and game.gba are two games).
func (s *Service) findGame(tag, romDir string) (osadapter.Game, bool) {
	systems, err := s.opts.Adapter.Systems()
	if err != nil {
		return osadapter.Game{}, false
	}
	tagDir := s.opts.Layout.BackupsDir(tag)
	want := filepath.Join(tagDir, romDir)
	var fold osadapter.Game
	foundFold := false
	for _, sys := range systems {
		if sys.Tag != tag {
			continue
		}
		var exact osadapter.Game
		found := false
		_ = s.opts.Adapter.WalkGames(context.Background(), sys, func(g osadapter.Game) error {
			switch {
			case g.ROMName == romDir:
				exact, found = g, true
				return errStopWalk
			case !foundFold && strings.EqualFold(g.ROMName, romDir) && (!s.caseFromFS || store.FindFold(tagDir, g.ROMName, true) == want):
				fold, foundFold = g, true
			}
			return nil
		})
		if found {
			return exact, true
		}
	}
	return fold, foundFold
}

// replaceWithBackup restores the backup copy of a save in place of the
// current save: the copy first goes back to the backups of its game (a
// plain restore, never over a present file), then internal/saves restores
// it: the current save is kept as a "before-restore" backup copy, and the
// save and its clock file go in as one journaled replacement of the trash
// (a power cut never leaves the save of one pair with the clock of the
// other); the restored copy stays among the backups. If that second step
// fails, the copy stays listed in the backups of the game (nothing is
// lost) and the error says so.
func (s *Service) replaceWithBackup(id string, b backupOfSave) (TrashRestoreResult, *Error, error) {
	s.installMu.Lock()
	res, err := s.opts.Trash.Restore(id, store.RestoreFail, nil)
	s.installMu.Unlock()
	if err != nil {
		return s.restoreOutcome(res, err)
	}
	if _, err := s.opts.Saves.Restore(b.game, b.id); err != nil {
		s.log.Error("trash: backup copy restored to the backups, but the save was not replaced", "entry", id, "backup", b.id, "error", err)
		return TrashRestoreResult{}, nil, fmt.Errorf("the backup copy is back in the backups of the game, but the save was not replaced: %w", err)
	}
	sd := s.opts.Adapter.Roots().SD
	out := store.RestoreResult{Restored: []string{}, Replaced: []string{}, RestoredBytes: res.RestoredBytes, ReplacedKeptAs: b.keptAs}
	if f, _ := s.opts.Adapter.SaveDir(b.game.System.Tag).Files(b.game); f != nil {
		if rel, err := filepath.Rel(sd, f.Path); err == nil {
			out.Restored = append(out.Restored, filepath.ToSlash(rel))
		}
	}
	for _, d := range b.displaced {
		if rel, err := filepath.Rel(sd, d); err == nil {
			out.Replaced = append(out.Replaced, filepath.ToSlash(rel))
		}
	}
	s.log.Info("trash: save replaced by a restored backup copy", "entry", id, "game", b.game.Path, "backup", b.id, "keptAs", b.keptAs)
	return TrashRestoreResult{RestoreResult: out}, nil, nil
}
