package transfer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/paks"
	"github.com/tfournier/ReminisLink/internal/store"
)

// Pak installation (M12, protocol/openapi.yaml POST /paks/install and the
// "pak" upload and download targets). Every source ends in the same
// pipeline: the pak is extracted (or copied) into a folder of the staging
// area and checked there (internal/paks), then, holding the write lock
// and the install lock, the old pak of the same name goes to the trash
// and the staged folder is renamed into place.

// KindPak is the "pak" upload and download target.
const KindPak = "pak"

// JobPakInstall is the kind of the job of POST /paks/install and of a
// completed "pak" upload.
const JobPakInstall = "pak-install"

// Conflict rules of POST /paks/install.
const (
	PakConflictFail    = "fail"
	PakConflictReplace = "replace"
)

// pakStagingPrefix starts the staging folders of the pak installations
// (pk_<YYYYMMDDTHHMMSSZ>_<8 hex>); none survives a restart.
const pakStagingPrefix = "pk_"

var pakStagingRe = regexp.MustCompile(`^pk_\d{8}T\d{6}Z_[0-9a-f]{8}$`)

// pakArchiveLimit bounds an archive uploaded or downloaded as a pak
// (paks.MaxArchiveBytes; lowered by tests).
var pakArchiveLimit int64 = paks.MaxArchiveBytes

// pakArchiveName is the name of an uploaded archive in its staging folder.
const pakArchiveName = "archive.zip"

// Paks returns the manager of the installed paks.
func (s *Service) Paks() *paks.Manager { return s.paks }

// validPakKind checks the pakKind of a target or the kind of a request.
func validPakKind(k string) bool {
	return k == paks.KindTool || k == paks.KindEmu || k == paks.KindAuto
}

// pakKindOf returns the pakKind of a target (auto when absent).
func pakKindOf(t Target) string {
	if t.PakKind == nil {
		return paks.KindAuto
	}
	return *t.PakKind
}

// pakConflict maps the onConflict of a "pak" upload or download target.
func pakConflict(onConflict string) string {
	if onConflict == ConflictOverwrite {
		return PakConflictReplace
	}
	return PakConflictFail
}

// checkPakTarget checks the fields of a "pak" target and its onConflict.
func checkPakTarget(t Target, onConflict string) *Error {
	if t.Kind != KindPak {
		return nil
	}
	if !validPakKind(pakKindOf(t)) {
		return badRequest("Invalid target.pakKind")
	}
	if onConflict == ConflictRename {
		return badRequest("onConflict rename is not allowed for a pak")
	}
	return nil
}

type jobGuardKey2 struct{}

// WithJobGuard returns a context whose upload completion, when it queues
// a job (a "pak" target), guards it with guard (M13).
func WithJobGuard(ctx context.Context, guard func() JobGuard) context.Context {
	return context.WithValue(ctx, jobGuardKey2{}, guard)
}

func jobGuardOf(ctx context.Context) JobGuard {
	if fn, ok := ctx.Value(jobGuardKey2{}).(func() JobGuard); ok && fn != nil {
		return fn()
	}
	return JobGuard{}
}

// newPakStaging creates an empty staging folder for one installation.
func (s *Service) newPakStaging() (string, error) {
	dir := filepath.Join(s.opts.Layout.StagingDir(),
		pakStagingPrefix+s.now().UTC().Format(idTimeLayout)+"_"+s.randomHex(4))
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// removeStaging deletes a staging folder of a pak installation, unless a
// replacement the trash could not finish nor roll back still names a
// folder inside it (store.ErrReplaceFailed: the next start finishes it).
func (s *Service) removeStaging(stage string) bool {
	stage = filepath.Clean(stage)
	for p := range s.opts.Trash.PendingSources() {
		if p == stage || strings.HasPrefix(p, stage+string(filepath.Separator)) {
			s.log.Warn("staging: pak kept for an interrupted replacement", "entry", filepath.Base(stage))
			return false
		}
	}
	return os.RemoveAll(stage) == nil
}

// purgePakStaging deletes the staging folders of the pak installations
// left by a previous run (called at start, after the trash recovery; no
// job runs), except those an unfinished replacement still needs.
func (s *Service) purgePakStaging() {
	des, err := os.ReadDir(s.opts.Layout.StagingDir())
	if err != nil {
		return
	}
	for _, d := range des {
		if pakStagingRe.MatchString(d.Name()) && s.removeStaging(filepath.Join(s.opts.Layout.StagingDir(), d.Name())) {
			s.log.Info("staging: deleted a pak left by a previous run", "entry", d.Name())
		}
	}
}

// spaceFor returns the free space check of an extraction into dir.
func (s *Service) spaceFor(dir string) paks.SpaceFunc {
	return func(need int64) error {
		free, err := s.opts.FreeSpace(dir)
		if err != nil {
			return fmt.Errorf("free space: %w", err)
		}
		if req := required(need); free < req {
			return insufficientStorage(req, free)
		}
		return nil
	}
}

// pakError maps an error of the pak pipeline to the error of its job.
func (s *Service) pakError(ctx context.Context, err error) *Error {
	var apiErr *Error
	switch {
	case errors.As(err, &apiErr):
		return apiErr
	case ctx.Err() != nil:
		return newError(http.StatusServiceUnavailable, CodeShuttingDown, "Cancelled")
	}
	if fe, ok := paks.IsFormatError(err); ok {
		return unsupportedFormat("Invalid pak: " + fe.Reason)
	}
	s.log.Error("pak install", "error", withoutPath(err))
	return internal("Internal error")
}

// withoutPath drops the path of an fs.PathError (logs never hold it here).
func withoutPath(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return fmt.Errorf("%s: %w", pe.Op, pe.Err)
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return fmt.Errorf("%s: %w", le.Op, le.Err)
	}
	return err
}

// runPakArchive installs the zip archive at archive (a synced file of the
// uploads or staging area, left in place) as a pak.
// name is the file name of the archive, which names a loose archive
// without a name in its pak.json.
func (s *Service) runPakArchive(ctx context.Context, archive, kind, onConflict, name string, report func(int64, *int64)) (map[string]any, *Error) {
	stage, err := s.newPakStaging()
	if err != nil {
		return nil, s.pakError(ctx, err)
	}
	defer s.removeStaging(stage)
	f, err := os.Open(archive)
	if err != nil {
		return nil, s.pakError(ctx, err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, s.pakError(ctx, err)
	}
	staged, err := paks.ExtractZip(ctx, f, fi.Size(), stage, s.extractOptions(stage, name, report))
	f.Close()
	if err != nil {
		return nil, s.pakError(ctx, err)
	}
	return s.placePak(ctx, staged, kind, onConflict)
}

// extractOptions are the options of an extraction into stage.
func (s *Service) extractOptions(stage, name string, report func(int64, *int64)) paks.ExtractOptions {
	return paks.ExtractOptions{Platform: s.paks.Platform(), Name: name, Clean: CleanFileName,
		Space: s.spaceFor(stage), Progress: progressOf(report)}
}

func progressOf(report func(int64, *int64)) paks.Progress {
	if report == nil {
		return nil
	}
	return func(done, total int64) { report(done, &total) }
}

// resolveKind is the folder a staged pak goes to.
func resolveKind(requested string, staged paks.Staged) string {
	if requested == paks.KindTool || requested == paks.KindEmu {
		return requested
	}
	if staged.Kind != "" {
		return staged.Kind
	}
	if _, typ := paks.ReadMetadata(staged.Dir); paks.KindFromMetadata(typ) != "" {
		return paks.KindFromMetadata(typ)
	}
	return paks.KindTool
}

// pakConflictError is 409 conflict with details.existing.
func (s *Service) pakConflictError(existing string) *Error {
	e := s.conflictError(existing)
	e.Message = "Pak exists"
	return e
}

// selfError refuses to delete or replace the running pak.
func selfError() *Error {
	return &Error{Status: http.StatusConflict, Code: CodeConflict, Message: "ReminisLink cannot delete or replace itself",
		Details: map[string]any{"self": true}}
}

// placePak installs a staged pak (M13: the job waits for the write lock
// without a time limit, then checks its client again).
func (s *Service) placePak(ctx context.Context, staged paks.Staged, requested, onConflict string) (map[string]any, *Error) {
	kind := resolveKind(requested, staged)
	if err := s.opts.Writes.Acquire(ctx); err != nil {
		return nil, newError(http.StatusServiceUnavailable, CodeShuttingDown, "Cancelled")
	}
	defer s.opts.Writes.Release()
	if !jobAllowed(ctx) {
		return nil, errJobRevoked
	}
	s.installMu.Lock()
	defer s.installMu.Unlock()
	if ctx.Err() != nil {
		return nil, newError(http.StatusServiceUnavailable, CodeShuttingDown, "Cancelled")
	}
	// The folder of the paks: created under the SD card only, no link at
	// any component (the desktop mode runs on file systems with links).
	parent, err := s.paks.EnsureDir(kind)
	if err != nil {
		return nil, s.pakError(ctx, err)
	}
	dest := filepath.Join(parent, staged.Name)
	if file := store.FindFold(parent, staged.Name, false); file != "" {
		return nil, s.pakConflictError(file) // never replace a file by a folder
	}
	var items []store.MoveItem
	label := paks.DisplayName(staged.Name)
	if existing := store.FindFold(parent, staged.Name, true); existing != "" {
		if s.paks.IsSelf(existing) {
			return nil, selfError()
		}
		if onConflict != PakConflictReplace {
			return nil, s.pakConflictError(existing)
		}
		items = []store.MoveItem{{Path: existing}}
		label = paks.DisplayName(filepath.Base(existing))
	} else if _, err := os.Lstat(dest); err == nil || !errors.Is(err, fs.ErrNotExist) {
		// Something FindFold does not list (a link): never replaced.
		return nil, s.pakConflictError(dest)
	}
	// Journaled replacement (rule 1 of CLAUDE.md, M8): the old pak moves
	// to a trash entry "installing", the staged folder is renamed into
	// place, every folder synced (any error rolls back); a power cut is
	// finished or rolled back by the trash recovery at the next start.
	install := store.Install{Src: staged.Dir, Dst: dest, Size: paks.FolderSize(staged.Dir), Dir: true}
	if _, err := s.opts.Trash.Replace(store.ReasonReplacePak, label, items, []store.Install{install}); err != nil {
		if errors.Is(err, store.ErrReplaceFailed) {
			s.log.Error("pak install: replacement left for the next start", "pak", staged.Name)
		}
		return nil, s.pakError(ctx, err)
	}
	s.log.Info("pak installed", "pak", staged.Name, "kind", kind, "replaced", items != nil)
	return map[string]any{"pakId": s.paks.ID(dest)}, nil
}

// PakInstallRequest is the body of POST /paks/install.
type PakInstallRequest struct {
	SDEntryID  *string `json:"sdEntryId"`
	Kind       *string `json:"kind"`
	OnConflict *string `json:"onConflict"`
}

// StartPakInstall checks a POST /paks/install request and queues its job,
// guarded by guard (M13).
func (s *Service) StartPakInstall(req PakInstallRequest, guard JobGuard) (string, *Error, error) {
	if req.SDEntryID == nil {
		return "", badRequest("Missing sdEntryId"), nil
	}
	kind := paks.KindAuto
	if req.Kind != nil {
		kind = *req.Kind
	}
	if !validPakKind(kind) {
		return "", badRequest("Invalid kind"), nil
	}
	onConflict := PakConflictFail
	if req.OnConflict != nil {
		onConflict = *req.OnConflict
	}
	if onConflict != PakConflictFail && onConflict != PakConflictReplace {
		return "", badRequest("Invalid onConflict"), nil
	}
	id := *req.SDEntryID
	_, fi, apiErr, err := s.resolveSDSource(id)
	if apiErr != nil || err != nil {
		return "", apiErr, err
	}
	label := paks.DisplayName(fi.Name())
	jobID, apiErr, err := s.submitGuarded(JobPakInstall, label, guard, func(ctx context.Context, report func(int64, *int64)) (map[string]any, *Error) {
		return s.runPakFromSD(ctx, id, kind, onConflict, report)
	})
	if apiErr != nil || err != nil {
		return "", apiErr, err
	}
	s.log.Info("pak install queued", "job", jobID, "source", "sd")
	return jobID, nil, nil
}

// resolveSDSource resolves the sdEntryId of a pak source: a regular file
// (an archive) or a ".pak" folder.
func (s *Service) resolveSDSource(id string) (string, fs.FileInfo, *Error, error) {
	p, fi, err := s.opts.Library.ResolveSDEntry(id)
	switch {
	case errors.Is(err, library.ErrInvalidID):
		return "", nil, newError(http.StatusBadRequest, CodeInvalidID, "Invalid identifier"), nil
	case errors.Is(err, library.ErrNotFound):
		return "", nil, notFound("SD entry not found"), nil
	case err != nil:
		return "", nil, nil, err
	case fi.IsDir() && !paks.HasSuffix(fi.Name()):
		return "", nil, unsupportedFormat("Not a .pak folder nor an archive"), nil
	}
	return p, fi, nil, nil
}

// runPakFromSD installs the pak of an SD entry, resolved again.
func (s *Service) runPakFromSD(ctx context.Context, id, kind, onConflict string, report func(int64, *int64)) (map[string]any, *Error) {
	p, fi, apiErr, err := s.resolveSDSource(id)
	if apiErr != nil {
		return nil, apiErr
	}
	if err != nil {
		return nil, s.pakError(ctx, err)
	}
	if !fi.IsDir() {
		return s.runPakSDArchive(ctx, id, kind, onConflict, report)
	}
	stage, err := s.newPakStaging()
	if err != nil {
		return nil, s.pakError(ctx, err)
	}
	defer s.removeStaging(stage)
	staged, err := paks.CopyFolder(ctx, p, stage, s.spaceFor(stage), progressOf(report))
	if err != nil {
		return nil, s.pakError(ctx, err)
	}
	if kind == paks.KindAuto {
		if k := s.paks.KindOf(filepath.Dir(p)); k != "" {
			staged.Kind = k
		}
	}
	return s.placePak(ctx, staged, kind, onConflict)
}

// runPakSDArchive installs the zip file of an SD entry, read through the
// SD card root.
func (s *Service) runPakSDArchive(ctx context.Context, id, kind, onConflict string, report func(int64, *int64)) (map[string]any, *Error) {
	f, fi, _, err := s.opts.Library.OpenSDEntry(id)
	if errors.Is(err, library.ErrNotFound) {
		return nil, notFound("SD entry not found")
	}
	if err != nil {
		return nil, s.pakError(ctx, err)
	}
	defer f.Close()
	stage, err := s.newPakStaging()
	if err != nil {
		return nil, s.pakError(ctx, err)
	}
	defer s.removeStaging(stage)
	staged, err := paks.ExtractZip(ctx, f, fi.Size(), stage, s.extractOptions(stage, fi.Name(), report))
	if err != nil {
		return nil, s.pakError(ctx, err)
	}
	return s.placePak(ctx, staged, kind, onConflict)
}

// queuePakUpload moves a completed "pak" upload (part, synced) into a
// staging folder and queues its pak-install job (called with the write
// lock held; the job waits for its own turn). On a refused job the file
// goes back to part, so the upload stays.
func (s *Service) queuePakUpload(ctx context.Context, t Target, part, onConflict string) (map[string]any, *Error, error) {
	stage, err := s.newPakStaging()
	if err != nil {
		return nil, nil, err
	}
	archive := filepath.Join(stage, pakArchiveName)
	if err := s.rename(part, archive); err != nil {
		os.Remove(stage)
		return nil, nil, err
	}
	if err := errors.Join(store.SyncDirChecked(stage), store.SyncDirChecked(filepath.Dir(part))); err != nil {
		if rerr := s.rename(archive, part); rerr == nil {
			os.Remove(stage)
		}
		return nil, nil, err
	}
	label, name := "", ""
	if t.FileName != nil {
		name = *t.FileName
		label = paks.DisplayName(strings.TrimSuffix(name, filepath.Ext(name)))
	}
	kind, conflict := pakKindOf(t), pakConflict(onConflict)
	id, apiErr, err := s.submitWithCleanup(JobPakInstall, label, jobGuardOf(ctx), func(jctx context.Context, report func(int64, *int64)) (map[string]any, *Error) {
		return s.runPakArchive(jctx, archive, kind, conflict, name, report)
	}, func() { s.removeStaging(stage) })
	if apiErr != nil || err != nil {
		if rerr := s.rename(archive, part); rerr == nil {
			os.Remove(stage)
		}
		return nil, apiErr, err
	}
	s.log.Info("pak install queued", "job", id, "source", "upload")
	return map[string]any{"jobId": id}, nil, nil
}

// PakDeleteResult is the answer of DELETE /paks/{pakId}.
type PakDeleteResult struct {
	TrashEntryID string   `json:"trashEntryId"`
	Moved        []string `json:"moved"`
	MovedBytes   int64    `json:"movedBytes"`
}

// DeletePak moves an installed pak to the trash (the running pak is
// refused with 409) under the install lock.
func (s *Service) DeletePak(pakID string) (PakDeleteResult, *Error, error) {
	s.installMu.Lock()
	defer s.installMu.Unlock()
	p, dir, err := s.paks.Resolve(pakID)
	switch {
	case errors.Is(err, paks.ErrInvalidID):
		return PakDeleteResult{}, newError(http.StatusBadRequest, CodeInvalidID, "Invalid identifier"), nil
	case errors.Is(err, paks.ErrNotFound):
		return PakDeleteResult{}, notFound("Pak not found"), nil
	case err != nil:
		return PakDeleteResult{}, nil, err
	case p.Self:
		return PakDeleteResult{}, selfError(), nil
	}
	res, err := s.opts.Trash.Move(store.ReasonDeletePak, p.Name, []store.MoveItem{{Path: dir}})
	if err != nil {
		return PakDeleteResult{}, nil, err
	}
	s.log.Info("pak deleted", "pak", p.Name, "entry", res.Entry.ID)
	return PakDeleteResult{TrashEntryID: res.Entry.ID, Moved: res.Moved, MovedBytes: res.MovedBytes}, nil, nil
}
