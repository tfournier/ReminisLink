package transfer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

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
			if b, ok := s.backupEntry(p); ok {
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

// replaceSpec computes a "replace" restore with the rules of the OS: the
// entry must hold only the save (".sav") and the clock file (".rtc") of
// one game that still exists, in its save folder; they go to the names
// the game uses now (the current files, matched ignoring case like the
// adapter does, or the names the adapter gives), after the current save
// and clock file went to the trash.
func (s *Service) replaceSpec(p store.RestorePlan) (*store.ReplaceSpec, error) {
	refuse := func(reason string) (*store.ReplaceSpec, error) {
		return nil, &store.NotReplaceableError{Reason: reason}
	}
	saves := s.opts.Adapter.Roots().Saves
	sd := s.opts.Adapter.Roots().SD
	tag, romName, hasSave := "", "", false
	for _, it := range p.Items {
		if it.Dir || it.Internal {
			return refuse("not-a-save")
		}
		abs := filepath.Join(sd, filepath.FromSlash(it.Rel))
		rel, err := filepath.Rel(saves, abs)
		if err != nil || !filepath.IsLocal(rel) {
			return refuse("not-a-save")
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) != 2 {
			return refuse("not-a-save")
		}
		ext := strings.ToLower(filepath.Ext(parts[1]))
		if ext != ".sav" && ext != ".rtc" {
			return refuse("not-a-save")
		}
		name := parts[1][:len(parts[1])-len(ext)]
		if (tag != "" && tag != parts[0]) || (romName != "" && !strings.EqualFold(romName, name)) || name == "" {
			return refuse("not-a-save")
		}
		tag, hasSave = parts[0], hasSave || ext == ".sav"
		if romName == "" || ext == ".sav" {
			romName = name
		}
	}
	if !hasSave {
		return refuse("not-a-save")
	}
	if !s.opts.Adapter.GameExists(tag, romName) {
		return refuse("game-missing")
	}
	g := osadapter.Game{System: osadapter.System{Tag: tag}, ROMName: romName}
	if err := s.opts.Adapter.CheckSaveFormat(g); err != nil {
		return refuse("save-format")
	}
	save, rtc := s.opts.Adapter.SaveDir(tag).Files(g)
	savePath := s.opts.Adapter.SavePath(g)
	if save != nil {
		savePath = save.Path
	}
	rtcPath := strings.TrimSuffix(savePath, filepath.Ext(savePath)) + ".rtc"
	if rtc != nil {
		rtcPath = rtc.Path
	}
	spec := &store.ReplaceSpec{Targets: map[string]string{}, Label: strings.TrimSuffix(romName, filepath.Ext(romName))}
	for _, it := range p.Items {
		target := savePath
		if strings.EqualFold(filepath.Ext(it.Rel), ".rtc") {
			target = rtcPath
		}
		rel, err := filepath.Rel(sd, target)
		if err != nil || !filepath.IsLocal(rel) {
			return refuse("not-a-save")
		}
		spec.Targets[it.Rel] = filepath.ToSlash(rel)
	}
	if save != nil {
		spec.Displace = append(spec.Displace, save.Path)
	}
	if rtc != nil {
		spec.Displace = append(spec.Displace, rtc.Path)
	}
	return spec, nil
}

// PreviewTrashRestore is GET /trash/{entryId}/restore: the plan of the
// store, with "replace" also offered for the backup copy of a save
// (backupEntry), which then goes through internal/saves.
func (s *Service) PreviewTrashRestore(id string) (store.RestorePlan, error) {
	p, err := s.opts.Trash.PlanRestore(id)
	if err != nil || p.Replaceable {
		return p, err
	}
	b, ok := s.backupEntry(p)
	if !ok {
		return p, nil
	}
	p.Replaceable, p.ReplaceKeptAs = true, ptr(store.ReplaceKeptBackup)
	save, rtc := s.opts.Adapter.SaveDir(b.game.System.Tag).Files(b.game)
	sd := s.opts.Adapter.Roots().SD
	savePath := s.opts.Adapter.SavePath(b.game)
	for _, f := range []*osadapter.File{save, rtc} {
		if f != nil {
			if rel, err := filepath.Rel(sd, f.Path); err == nil {
				p.ReplaceDisplaced = append(p.ReplaceDisplaced, filepath.ToSlash(rel))
			}
		}
	}
	if save != nil {
		savePath = save.Path
	}
	rtcPath := strings.TrimSuffix(savePath, filepath.Ext(savePath)) + ".rtc"
	if rtc != nil {
		rtcPath = rtc.Path
	}
	for i, f := range p.Files {
		target := savePath
		if strings.EqualFold(filepath.Ext(f.Name), ".rtc") {
			target = rtcPath
		}
		if rel, err := filepath.Rel(sd, target); err == nil {
			p.Files[i].ReplaceName = ptr(filepath.ToSlash(rel))
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
}

// backupEntry recognises such an entry whose game still exists and whose
// save format the pak writes.
func (s *Service) backupEntry(p store.RestorePlan) (backupOfSave, bool) {
	sd := s.opts.Adapter.Roots().SD
	backups := filepath.Dir(s.opts.Layout.BackupsDir("x"))
	tag, romDir, id := "", "", ""
	copies := 0
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
		case ext == ".rtc" && !it.Internal, ext == ".json" && it.Internal:
		default:
			return backupOfSave{}, false
		}
		tag, romDir, id = parts[0], parts[1], bid
	}
	if copies != 1 {
		return backupOfSave{}, false
	}
	g, ok := s.findGame(tag, romDir)
	if !ok || s.opts.Adapter.CheckSaveFormat(g) != nil {
		return backupOfSave{}, false
	}
	return backupOfSave{game: g, id: id}, true
}

// findGame returns the game of a tag whose ROM file name is romName,
// ignoring case (the first one, as the adapter pairs its data).
func (s *Service) findGame(tag, romName string) (osadapter.Game, bool) {
	if !s.opts.Adapter.GameExists(tag, romName) {
		return osadapter.Game{}, false
	}
	systems, err := s.opts.Adapter.Systems()
	if err != nil {
		return osadapter.Game{}, false
	}
	var found osadapter.Game
	ok := false
	stop := errors.New("found")
	for _, sys := range systems {
		if sys.Tag != tag {
			continue
		}
		_ = s.opts.Adapter.WalkGames(context.Background(), sys, func(g osadapter.Game) error {
			if strings.EqualFold(g.ROMName, romName) {
				found, ok = g, true
				return stop
			}
			return nil
		})
		if ok {
			break
		}
	}
	return found, ok
}

// replaceWithBackup restores the backup copy of a save in place of the
// current save: the copy first goes back to the backups of its game (a
// plain restore, never over a present file), then internal/saves restores
// it, keeping the current save (and clock file) as a "before-restore"
// backup copy first (rule 1 of CLAUDE.md), journaled by the store like any
// save restore. If that second step fails, the copy stays listed in the
// backups of the game (nothing is lost) and the error says so.
func (s *Service) replaceWithBackup(id string, b backupOfSave) (TrashRestoreResult, *Error, error) {
	s.installMu.Lock()
	res, err := s.opts.Trash.Restore(id, store.RestoreFail, nil)
	s.installMu.Unlock()
	if err != nil {
		return s.restoreOutcome(res, err)
	}
	before, _ := s.opts.Adapter.SaveDir(b.game.System.Tag).Files(b.game)
	backup, err := s.opts.Saves.Restore(b.game, b.id)
	if err != nil {
		s.log.Error("trash: backup copy restored to the backups, but the save was not replaced", "entry", id, "backup", b.id, "error", err)
		return TrashRestoreResult{}, nil, fmt.Errorf("the backup copy is back in the backups of the game, but the save was not replaced: %w", err)
	}
	sd := s.opts.Adapter.Roots().SD
	out := store.RestoreResult{Restored: []string{}, Replaced: []string{}, RestoredBytes: res.RestoredBytes, ReplacedKeptAs: ptr(store.ReplaceKeptBackup)}
	if f, _ := s.opts.Adapter.SaveDir(b.game.System.Tag).Files(b.game); f != nil {
		if rel, err := filepath.Rel(sd, f.Path); err == nil {
			out.Restored = append(out.Restored, filepath.ToSlash(rel))
		}
	}
	if before != nil {
		if rel, err := filepath.Rel(sd, before.Path); err == nil {
			out.Replaced = append(out.Replaced, filepath.ToSlash(rel))
		}
	}
	s.log.Info("trash: save replaced by a restored backup copy", "entry", id, "game", b.game.Path, "backup", b.id, "keptAs", backup)
	return TrashRestoreResult{RestoreResult: out}, nil, nil
}
