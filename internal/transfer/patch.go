package transfer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/patch"
	"github.com/tfournier/ReminisLink/internal/saves"
	"github.com/tfournier/ReminisLink/internal/store"
)

// POST /games/{gameId}/patch (docs/specs/04-api-v1.md, 05 §5.5): a job
// applies an IPS, BPS or UPS patch to the ROM of a game.
//
// Order of the operations (rule 1 of CLAUDE.md; a power cut at any step
// leaves a usable card):
//
//  1. The patched file is written to uploads/pt_<hex>.part (the
//     ReminisLink area, same card as the ROM), checked (CRC32 of BPS and
//     UPS) and synced. The ROM is only read. A cut here leaves a
//     temporary file, deleted at the next start (Maintain).
//  2. Under the SD card lock, the ROM is checked unchanged since the
//     start of the job, then:
//     - new-file: the file is renamed next to the ROM, under a free name;
//     - replace: the save is backed up (before-patch), the ROM goes to
//     the trash (reason replace-rom), the file is renamed to the ROM's
//     name. The save, states, locks and cheats keep their names, so they
//     stay with the game. A cut between the trash and the rename leaves
//     the original ROM in a complete trash entry (restorable) and no
//     patched file: the patch must be applied again; nothing of the user
//     is lost.

// Patch sources and output modes (PatchRequest in protocol/openapi.yaml).
const (
	PatchSourceStaging = "staging"
	PatchSourceSD      = "sd"
	OutputNewFile      = "new-file"
	OutputReplace      = "replace"
)

// CodePatchMismatch: the ROM is not the one the patch was made for.
const CodePatchMismatch = "patch_mismatch"

// patchedSuffix is added to the default name of a new-file output.
const patchedSuffix = " (patched)"

// archiveExts are ROM containers a patch cannot apply to: the patch is
// made for the ROM inside.
var archiveExts = map[string]bool{".zip": true, ".7z": true}

// PatchRequest is the body of POST /games/{gameId}/patch.
type PatchRequest struct {
	Patch  *PatchSource `json:"patch"`
	Output *PatchOutput `json:"output"`
}

// PatchSource is where the patch file is.
type PatchSource struct {
	Type      *string `json:"type"`
	StagingID *string `json:"stagingId"`
	SDFileID  *string `json:"sdFileId"`
}

// PatchOutput is what to do with the patched file.
type PatchOutput struct {
	Mode     *string `json:"mode"`
	FileName *string `json:"fileName"`
}

// patchJob is a checked patch request.
type patchJob struct {
	gameID    string
	patchPath string
	mode      string
	name      string // new-file: cleaned name given by the app, "" for the default
}

func patchMismatch(message string, details map[string]any) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: CodePatchMismatch, Message: message, Details: details}
}

// StartPatch checks a patch request and queues its job.
func (s *Service) StartPatch(gameID string, req PatchRequest) (string, *Error, error) {
	if req.Patch == nil || req.Patch.Type == nil {
		return "", badRequest("Missing patch.type"), nil
	}
	j := patchJob{gameID: gameID, mode: OutputNewFile}
	if req.Output != nil {
		if req.Output.Mode != nil {
			j.mode = *req.Output.Mode
		}
		if j.mode != OutputNewFile && j.mode != OutputReplace {
			return "", badRequest("Invalid output.mode"), nil
		}
		if j.mode == OutputNewFile && req.Output.FileName != nil {
			name, err := CleanFileName(*req.Output.FileName)
			if err != nil {
				return "", invalidName("Invalid file name"), nil
			}
			if !s.opts.Adapter.IsGameFile(name) || strings.EqualFold(filepath.Ext(name), ".m3u") {
				// Same rule as the uploads: a produced ROM is always listed
				// (and as a single-file game).
				return "", invalidName("Not a ROM file name"), nil
			}
			j.name = name
		}
	}
	g, apiErr, err := s.patchableGame(gameID)
	if apiErr != nil || err != nil {
		return "", apiErr, err
	}
	switch *req.Patch.Type {
	case PatchSourceStaging:
		if req.Patch.StagingID == nil {
			return "", badRequest("Missing patch.stagingId"), nil
		}
		if j.patchPath, err = s.StagingFile(*req.Patch.StagingID); err != nil {
			return "", notFound("Staging file not found"), nil
		}
	case PatchSourceSD:
		if req.Patch.SDFileID == nil {
			return "", badRequest("Missing patch.sdFileId"), nil
		}
		if j.patchPath, err = s.opts.Library.ResolveSDFile(*req.Patch.SDFileID, library.SDKindPatch); err != nil {
			if apiErr := libraryError(err, "Patch file"); apiErr != nil {
				return "", apiErr, nil
			}
			return "", nil, err
		}
	default:
		return "", badRequest("Unknown patch.type"), nil
	}
	// The format is known from the header: refuse other files at once.
	if apiErr := detectPatch(j.patchPath); apiErr != nil {
		return "", apiErr, nil
	}
	id, err := s.jobs.Submit(JobPatch, func(ctx context.Context, report func(int64, *int64)) (map[string]any, *Error) {
		return s.runPatch(ctx, j, report)
	})
	if errors.Is(err, ErrTooManyJobs) {
		return "", tooMany("Too many jobs"), nil
	}
	if errors.Is(err, ErrJobsClosed) {
		return "", newError(http.StatusServiceUnavailable, CodeShuttingDown, "Shutting down"), nil
	}
	if err != nil {
		return "", nil, err
	}
	s.log.Info("patch queued", "job", id, "game", g.Path, "mode", j.mode)
	return id, nil, nil
}

// patchableGame resolves a game whose ROM is a single file a patch can
// apply to.
func (s *Service) patchableGame(gameID string) (osadapter.Game, *Error, error) {
	g, err := s.opts.Library.GameByID(gameID)
	if err != nil {
		if apiErr := libraryError(err, "Game"); apiErr != nil {
			return g, apiErr, nil
		}
		return g, nil, err
	}
	if g.Kind != osadapter.KindFile {
		return g, unsupportedFormat("A patch applies to a single ROM file, not to a multi-disc (.m3u) game"), nil
	}
	if archiveExts[strings.ToLower(filepath.Ext(g.FileName))] {
		return g, unsupportedFormat("A patch cannot apply to a compressed ROM (.zip, .7z): extract it first"), nil
	}
	return g, nil, nil
}

func detectPatch(path string) *Error {
	f, err := os.Open(path)
	if err != nil {
		return notFound("Patch file not found")
	}
	defer f.Close()
	var hdr [5]byte
	n, _ := f.Read(hdr[:])
	if _, err := patch.Detect(hdr[:n]); err != nil {
		return unsupportedFormat("Not an IPS, BPS or UPS patch")
	}
	return nil
}

// romFile returns the ROM file of a single-file game.
func (s *Service) romFile(g osadapter.Game) (osadapter.File, error) {
	files, err := s.opts.Adapter.GameFiles(g)
	if err != nil {
		return osadapter.File{}, err
	}
	if len(files.ROM) != 1 {
		return osadapter.File{}, fmt.Errorf("game has %d ROM files", len(files.ROM))
	}
	return files.ROM[0], nil
}

// patchError maps an error of internal/patch.
func patchError(err error) *Error {
	switch {
	case errors.Is(err, patch.ErrUnsupported):
		return unsupportedFormat("Not an IPS, BPS or UPS patch")
	case errors.Is(err, patch.ErrCorrupt):
		return unsupportedFormat(err.Error()) // "corrupt patch: <reason>"
	case errors.Is(err, patch.ErrAlreadyPatched):
		return patchMismatch("This ROM is already patched (it is the target of the patch)", map[string]any{"reason": "already-patched"})
	case errors.Is(err, patch.ErrSourceMismatch):
		return patchMismatch("The patch does not match this ROM", map[string]any{"reason": "source-mismatch"})
	case errors.Is(err, patch.ErrTooLarge):
		return newError(http.StatusRequestEntityTooLarge, CodeTooLarge, "Patched file too large")
	}
	return nil
}

func (s *Service) runPatch(ctx context.Context, j patchJob, report func(int64, *int64)) (map[string]any, *Error) {
	cancelled := func() *Error { return newError(http.StatusServiceUnavailable, CodeShuttingDown, "Cancelled") }
	g, apiErr, err := s.patchableGame(j.gameID)
	if err != nil {
		s.log.Error("patch: game", "error", err)
		return nil, internal("Internal error")
	}
	if apiErr != nil {
		return nil, apiErr
	}
	rom, err := s.romFile(g)
	if err != nil {
		s.log.Error("patch: ROM file", "game", g.Path, "error", err)
		return nil, notFound("Game not found")
	}
	pf, err := os.Open(j.patchPath)
	if err != nil {
		return nil, notFound("Patch file not found")
	}
	defer pf.Close()
	pInfo, err := pf.Stat()
	if err != nil || !pInfo.Mode().IsRegular() {
		return nil, notFound("Patch file not found")
	}
	src, err := os.Open(rom.Path)
	if err != nil {
		return nil, notFound("Game not found")
	}
	defer src.Close()
	srcInfo, err := src.Stat()
	if err != nil || !srcInfo.Mode().IsRegular() {
		return nil, notFound("Game not found")
	}

	opts := patch.Options{MaxTargetSize: MaxDownloadBytes}
	info, err := patch.Inspect(pf, pInfo.Size(), srcInfo.Size(), opts)
	if err != nil {
		if apiErr := patchError(err); apiErr != nil {
			return nil, apiErr
		}
		s.log.Error("patch: read", "error", err)
		return nil, internal("Cannot read the patch")
	}
	dir := s.opts.Layout.UploadsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, internal("Cannot create the temporary file")
	}
	if free, err := s.opts.FreeSpace(dir); err == nil {
		if need := required(info.TargetSize); free < need {
			return nil, insufficientStorage(need, free)
		}
	}
	total := info.TargetSize
	report(0, &total)

	tmp := filepath.Join(dir, "pt_"+s.randomHex(12)+partExt)
	out, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, internal("Cannot create the temporary file")
	}
	installed := false
	defer func() {
		if out != nil {
			out.Close()
		}
		if !installed {
			os.Remove(tmp)
		}
	}()
	var lastReport time.Time
	opts.Progress = func(done, total int64) {
		if done == total || time.Since(lastReport) >= 200*time.Millisecond {
			lastReport = time.Now()
			report(done, &total)
		}
	}
	if _, err := patch.Apply(ctx, pf, pInfo.Size(), src, srcInfo.Size(), out, opts); err != nil {
		if ctx.Err() != nil {
			return nil, cancelled()
		}
		if apiErr := patchError(err); apiErr != nil {
			s.log.Info("patch refused", "game", g.Path, "format", info.Format, "error", err)
			return nil, apiErr
		}
		if errors.Is(err, syscall.ENOSPC) {
			free, _ := s.opts.FreeSpace(dir)
			return nil, insufficientStorage(required(info.TargetSize), free)
		}
		s.log.Error("patch: apply", "game", g.Path, "error", err)
		return nil, internal("Cannot write the patched file")
	}
	if err := out.Sync(); err != nil {
		return nil, internal("Cannot write the patched file")
	}
	if err := out.Close(); err != nil {
		out = nil
		return nil, internal("Cannot write the patched file")
	}
	out = nil
	if ctx.Err() != nil {
		return nil, cancelled()
	}

	s.installMu.Lock()
	if ctx.Err() != nil {
		// Cancelled while waiting for the lock: nothing installed yet.
		s.installMu.Unlock()
		return nil, cancelled()
	}
	result, apiErr, err := s.installPatch(j, srcInfo, tmp)
	s.installMu.Unlock()
	if err != nil {
		s.log.Error("patch: install", "game", g.Path, "error", err)
		return nil, internal("Internal error")
	}
	if apiErr != nil {
		return nil, apiErr
	}
	installed = true
	s.log.Info("rom patched", "game", g.Path, "format", info.Format, "mode", j.mode, "bytes", info.TargetSize)
	return result, nil
}

// installPatch installs the synced patched file tmp. Called with
// s.installMu held.
func (s *Service) installPatch(j patchJob, before os.FileInfo, tmp string) (map[string]any, *Error, error) {
	// The game and its ROM, again: they may have changed while patching.
	g, apiErr, err := s.patchableGame(j.gameID)
	if apiErr != nil || err != nil {
		return nil, apiErr, err
	}
	rom, err := s.romFile(g)
	if err != nil {
		return nil, notFound("Game not found"), nil
	}
	now, err := os.Lstat(rom.Path)
	if err != nil {
		return nil, notFound("Game not found"), nil
	}
	if !os.SameFile(before, now) || now.Size() != before.Size() || !now.ModTime().Equal(before.ModTime()) {
		return nil, &Error{Status: http.StatusConflict, Code: CodeConflict, Message: "The ROM changed while it was being patched",
			Details: map[string]any{"existing": s.relToSD(rom.Path)}}, nil
	}
	dir := filepath.Dir(rom.Path)
	var dst string
	switch j.mode {
	case OutputReplace:
		if _, err := s.opts.Saves.BackupLocked(g, saves.ReasonBeforePatch); err != nil {
			return nil, nil, fmt.Errorf("save backup: %w", err)
		}
		if _, err := s.opts.Trash.Move(store.ReasonReplaceROM, g.Name, []store.MoveItem{{Path: rom.Path}}); err != nil {
			return nil, nil, err
		}
		dst = rom.Path
	default:
		name := j.name
		if name == "" {
			ext := filepath.Ext(g.FileName)
			name = truncateName(strings.TrimSuffix(g.FileName, ext) + patchedSuffix + ext)
		}
		free := freeName(dir, name)
		if free == "" {
			return nil, s.conflictError(filepath.Join(dir, name)), nil
		}
		dst = filepath.Join(dir, free)
	}
	if err := os.Rename(tmp, dst); err != nil {
		if j.mode == OutputReplace {
			// The original is in its trash entry, safe and restorable.
			return nil, nil, fmt.Errorf("rename patched ROM (original ROM in the trash): %w", err)
		}
		return nil, nil, fmt.Errorf("rename patched ROM: %w", err)
	}
	store.SyncDir(dir)
	store.SyncDir(filepath.Dir(tmp))
	rel, err := s.opts.Library.RelToRoms(dst)
	if err != nil {
		return nil, nil, err
	}
	return map[string]any{"gameId": library.EncodeID(rel)}, nil, nil
}
