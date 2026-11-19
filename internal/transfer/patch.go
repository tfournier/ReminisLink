package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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
//     - new-file: the file is renamed next to the ROM, under a free name
//     (one atomic rename);
//     - replace: the save is backed up (before-patch), a marker
//     uploads/pt_<hex>.pending.json is written (atomic: the temporary
//     file, its size and CRC32, the ROM), the ROM goes to the trash
//     (reason replace-rom), the file is renamed to the ROM's name, the
//     marker is deleted. If the rename fails, the ROM is put back from
//     the trash. After a power cut, Maintain finishes the installation
//     from the marker (checked temporary file present) or puts the ROM
//     back from the trash. The save, states, locks and cheats keep their
//     names, so they stay with the game.

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

// Names of the patch files in uploads/.
const (
	patchTempPrefix = "pt_"
	pendingExt      = ".pending.json"
)

var patchTempRe = regexp.MustCompile(`^pt_[0-9a-f]{24}\.part$`)

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
	if req.Output != nil && req.Output.Mode != nil {
		j.mode = *req.Output.Mode
	}
	if j.mode != OutputNewFile && j.mode != OutputReplace {
		return "", badRequest("Invalid output.mode"), nil
	}
	g, apiErr, err := s.patchableGame(gameID)
	if apiErr != nil || err != nil {
		return "", apiErr, err
	}
	if j.mode == OutputNewFile && req.Output != nil && req.Output.FileName != nil {
		if j.name, apiErr = s.outputName(g, *req.Output.FileName); apiErr != nil {
			return "", apiErr, nil
		}
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

// outputName checks the name of a new-file output: cleaned, ending with
// the extension of the original ROM (added when the name does not already
// end with it, whatever it holds: "Hack v1.2" gives "Hack v1.2.gba",
// "Hack.zip" gives "Hack.zip.gba"): the patched ROM is the same kind of
// file. Never refused for its extension.
func (s *Service) outputName(g osadapter.Game, given string) (string, *Error) {
	name, err := CleanFileName(given)
	if err != nil {
		return "", invalidName("Invalid file name")
	}
	ext := filepath.Ext(g.FileName)
	if !strings.HasSuffix(strings.ToLower(name), strings.ToLower(ext)) {
		if name, err = CleanFileName(name + ext); err != nil {
			return "", invalidName("Invalid file name")
		}
	}
	if !s.opts.Adapter.IsGameFile(name) {
		// Same rule as the uploads: a produced ROM is always listed.
		return "", invalidName("Not a ROM file name")
	}
	return name, nil
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
	n, _ := io.ReadFull(f, hdr[:])
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

func crcHex(v uint32) string { return fmt.Sprintf("%08x", v) }

// patchError maps an error of internal/patch.
func patchError(err error) *Error {
	var mismatch *patch.MismatchError
	switch {
	case errors.Is(err, patch.ErrUnsupported):
		return unsupportedFormat("Not an IPS, BPS or UPS patch")
	case errors.Is(err, patch.ErrCorrupt):
		return unsupportedFormat(err.Error()) // "corrupt patch: <reason>"
	case errors.Is(err, patch.ErrAlreadyPatched):
		return patchMismatch("This ROM is already patched (it is the target of the patch)", map[string]any{"reason": "already-patched"})
	case errors.As(err, &mismatch):
		d := map[string]any{"reason": "source-mismatch", "expectedSize": mismatch.ExpectedSize, "actualSize": mismatch.ActualSize,
			"expectedCrc32": crcHex(mismatch.ExpectedCRC32)}
		if mismatch.CRCKnown {
			d["actualCrc32"] = crcHex(mismatch.ActualCRC32)
		}
		return patchMismatch("The patch does not match this ROM", d)
	case errors.Is(err, patch.ErrTooLarge):
		return newError(http.StatusRequestEntityTooLarge, CodeTooLarge, "Patched file too large")
	}
	return nil
}

// checkPatchSpace requires the free space for the patched file, beyond
// the bytes the open uploads still expect.
func (s *Service) checkPatchSpace(dir string, size int64) *Error {
	free, err := s.opts.FreeSpace(dir)
	if err != nil {
		s.log.Error("patch: free space", "error", err)
		return internal("Cannot read the free space")
	}
	s.uploads.mu.Lock()
	available := free - s.reservedLocked()
	s.uploads.mu.Unlock()
	if need := required(size); available < need {
		return insufficientStorage(need, available)
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
	info, err := patch.Inspect(ctx, pf, pInfo.Size(), srcInfo.Size(), opts)
	if err != nil {
		if ctx.Err() != nil {
			return nil, cancelled()
		}
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
	if apiErr := s.checkPatchSpace(dir, info.TargetSize); apiErr != nil {
		return nil, apiErr
	}
	work := info.Work
	report(0, &work)

	tmp := filepath.Join(dir, patchTempPrefix+s.randomHex(12)+partExt)
	out, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, internal("Cannot create the temporary file")
	}
	keep := false // the temporary file was installed, or is referenced by a marker
	defer func() {
		if out != nil {
			out.Close()
		}
		if !keep {
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
	info, err = patch.Apply(ctx, pf, pInfo.Size(), src, srcInfo.Size(), out, opts)
	if err != nil {
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
	err = out.Close()
	out = nil
	if err != nil {
		return nil, internal("Cannot write the patched file")
	}
	if ctx.Err() != nil {
		return nil, cancelled()
	}

	s.installMu.Lock()
	if ctx.Err() != nil {
		// Cancelled while waiting for the lock: nothing installed yet.
		s.installMu.Unlock()
		return nil, cancelled()
	}
	result, apiErr, keep, err := s.installPatch(j, srcInfo, tmp, info)
	s.installMu.Unlock()
	if err != nil {
		s.log.Error("patch: install", "game", g.Path, "error", err)
		return nil, internal("Cannot install the patched ROM")
	}
	if apiErr != nil {
		return nil, apiErr
	}
	s.log.Info("rom patched", "game", g.Path, "format", info.Format, "mode", j.mode, "bytes", info.TargetSize, "verified", info.Verified)
	return result, nil
}

// installPatch installs the synced patched file tmp. Called with
// s.installMu held. keep reports that tmp must not be deleted (installed,
// or referenced by a marker for the next start).
func (s *Service) installPatch(j patchJob, before os.FileInfo, tmp string, info patch.Info) (result map[string]any, apiErr *Error, keep bool, err error) {
	// The game and its ROM, again: they may have changed while patching.
	g, apiErr, err := s.patchableGame(j.gameID)
	if apiErr != nil || err != nil {
		return nil, apiErr, false, err
	}
	rom, err := s.romFile(g)
	if err != nil {
		return nil, notFound("Game not found"), false, nil
	}
	now, err := os.Lstat(rom.Path)
	if err != nil {
		return nil, notFound("Game not found"), false, nil
	}
	if !os.SameFile(before, now) || now.Size() != before.Size() || !now.ModTime().Equal(before.ModTime()) {
		return nil, &Error{Status: http.StatusConflict, Code: CodeConflict, Message: "The ROM changed while it was being patched",
			Details: map[string]any{"existing": s.relToSD(rom.Path)}}, false, nil
	}
	dir := filepath.Dir(rom.Path)
	var dst string
	switch j.mode {
	case OutputReplace:
		if keep, err = s.replaceROM(g, rom.Path, tmp, info); err != nil {
			return nil, nil, keep, err
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
			return nil, s.conflictError(filepath.Join(dir, name)), false, nil
		}
		dst = filepath.Join(dir, free)
		if err := s.rename(tmp, dst); err != nil {
			return nil, nil, false, fmt.Errorf("rename patched ROM: %w", err)
		}
		store.SyncDir(dir)
		store.SyncDir(filepath.Dir(tmp))
	}
	rel, err := s.opts.Library.RelToRoms(dst)
	if err != nil {
		return nil, nil, true, err
	}
	return map[string]any{"gameId": library.EncodeID(rel), "verified": info.Verified}, nil, true, nil
}

// patchPending is the marker of a replace being installed
// (uploads/pt_<hex>.pending.json).
type patchPending struct {
	Version      int    `json:"version"`
	Temp         string `json:"temp"` // file name in uploads/
	Dest         string `json:"dest"` // the ROM, relative to the SD card
	Size         int64  `json:"size"`
	CRC32        uint32 `json:"crc32"`
	TrashEntryID string `json:"trashEntryId,omitempty"`
}

func pendingPath(tmp string) string { return strings.TrimSuffix(tmp, partExt) + pendingExt }

// replaceROM backs up the save, moves the ROM to the trash and renames tmp
// to its place, with a marker on the card for a power cut in between. If
// the rename fails, the ROM is put back. keep reports that tmp must stay
// (installed, or referenced by a marker left for the next start).
func (s *Service) replaceROM(g osadapter.Game, romPath, tmp string, info patch.Info) (keep bool, err error) {
	if _, err := s.opts.Saves.BackupLocked(g, saves.ReasonBeforePatch); err != nil {
		return false, fmt.Errorf("save backup: %w", err)
	}
	marker := pendingPath(tmp)
	pending := patchPending{Version: 1, Temp: filepath.Base(tmp), Dest: s.relToSD(romPath), Size: info.TargetSize, CRC32: info.TargetCRC32}
	if err := store.WriteJSONAtomic(marker, pending); err != nil {
		return false, err
	}
	res, err := s.opts.Trash.Move(store.ReasonReplaceROM, g.Name, []store.MoveItem{{Path: romPath}})
	if err != nil {
		os.Remove(marker) // nothing moved (Move is all or nothing)
		return false, err
	}
	pending.TrashEntryID = res.Entry.ID
	if err := store.WriteJSONAtomic(marker, pending); err != nil {
		// The first marker still tells the next start what to do.
		s.log.Warn("patch: cannot record the trash entry in the marker", "error", err)
	}
	if err := s.rename(tmp, romPath); err != nil {
		if uerr := s.opts.Trash.Undo(res.Entry.ID); uerr != nil {
			// The ROM stays in the trash; the marker and tmp stay for the
			// next start, which finishes the installation.
			s.log.Error("patch: cannot put the ROM back from the trash", "entry", res.Entry.ID, "error", uerr)
			return true, fmt.Errorf("rename patched ROM: %w", err)
		}
		os.Remove(marker)
		s.log.Warn("patch: rename failed, original ROM put back", "rom", pending.Dest)
		return false, fmt.Errorf("rename patched ROM (original put back): %w", err)
	}
	store.SyncDir(filepath.Dir(romPath))
	store.SyncDir(filepath.Dir(tmp))
	os.Remove(marker)
	return true, nil
}

// recoverPatches finishes the replace installations a power cut
// interrupted (markers in uploads/), after trash.Recover. It returns the
// temporary files still referenced by a marker it could not handle (never
// deleted by the purge).
func (s *Service) recoverPatches() map[string]bool {
	dir := s.opts.Layout.UploadsDir()
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	keep := map[string]bool{}
	for _, d := range des {
		if !strings.HasSuffix(d.Name(), pendingExt) {
			continue
		}
		marker := filepath.Join(dir, d.Name())
		if done := s.recoverPatch(marker); done {
			os.Remove(marker)
			continue
		}
		var p patchPending
		if data, err := os.ReadFile(marker); err == nil && json.Unmarshal(data, &p) == nil && patchTempRe.MatchString(p.Temp) {
			keep[p.Temp] = true
		}
		keep[d.Name()] = true
	}
	return keep
}

// recoverPatch handles one marker; done reports that it can be deleted.
func (s *Service) recoverPatch(marker string) (done bool) {
	data, err := os.ReadFile(marker)
	if err != nil {
		return false
	}
	var p patchPending
	if json.Unmarshal(data, &p) != nil || !patchTempRe.MatchString(p.Temp) || p.Temp != strings.TrimSuffix(filepath.Base(marker), pendingExt)+partExt {
		s.log.Warn("patch: unreadable installation marker, ignored", "marker", filepath.Base(marker))
		return true
	}
	// The destination must be a ROM path under Roms/ (the marker is a
	// file on the card: never trusted beyond that).
	if !filepath.IsLocal(filepath.FromSlash(p.Dest)) {
		return true
	}
	dest := filepath.Join(s.opts.Adapter.Roots().SD, filepath.FromSlash(p.Dest))
	if _, err := s.opts.Library.RelToRoms(dest); err != nil {
		return true
	}
	tmp := filepath.Join(filepath.Dir(marker), p.Temp)
	if _, err := os.Lstat(dest); err == nil {
		// The ROM is in place: the installation never moved it (or the
		// trash put it back), or it finished. Nothing to do.
		return true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if checkPatched(tmp, p.Size, p.CRC32) {
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return false
		}
		if err := s.rename(tmp, dest); err != nil {
			s.log.Error("patch: cannot finish an interrupted installation", "rom", p.Dest, "error", err)
			return false
		}
		store.SyncDir(filepath.Dir(dest))
		store.SyncDir(filepath.Dir(tmp))
		s.log.Info("patch: interrupted installation finished", "rom", p.Dest)
		return true
	}
	// No usable patched file: the original goes back.
	if p.TrashEntryID == "" {
		s.log.Warn("patch: interrupted installation, patched file missing; the original ROM is in the trash", "rom", p.Dest)
		return true
	}
	if err := s.opts.Trash.Undo(p.TrashEntryID); err != nil {
		s.log.Error("patch: cannot put the original ROM back; it stays in the trash", "rom", p.Dest, "entry", p.TrashEntryID, "error", err)
		return true
	}
	s.log.Info("patch: interrupted installation, original ROM put back", "rom", p.Dest)
	return true
}

// checkPatched reports whether path is a regular file of size bytes with
// the CRC32 sum.
func checkPatched(path string, size int64, sum uint32) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() || fi.Size() != size {
		return false
	}
	h := crc32.NewIEEE()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return h.Sum32() == sum
}
