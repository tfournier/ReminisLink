package transfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/tfournier/ReminisLink/internal/confine"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/paks"
	"github.com/tfournier/ReminisLink/internal/saves"
	"github.com/tfournier/ReminisLink/internal/states"
	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/writelock"
)

// Target kinds (UploadTarget in protocol/openapi.yaml).
const (
	KindROM     = "rom"
	KindSave    = "save"
	KindState   = "state"
	KindStaging = "staging"
	// KindPak (M12) is declared with the pak installation (paks.go).
)

// Conflict rules (OnConflict in protocol/openapi.yaml).
const (
	ConflictFail      = "fail"
	ConflictRename    = "rename"
	ConflictOverwrite = "overwrite"
)

// Retention of the temporary files (docs/specs/05-console-pak.md §2.3).
const (
	// UploadIdleExpiry: an upload without activity for this long is deleted.
	UploadIdleExpiry = 24 * time.Hour
	// StagingExpiry: a staging file older than this is deleted.
	StagingExpiry = 24 * time.Hour
)

// Target is the destination of an upload.
type Target struct {
	Kind     string  `json:"kind"`
	SystemID string  `json:"systemId,omitempty"`
	FolderID *string `json:"folderId,omitempty"`
	FileName *string `json:"fileName,omitempty"`
	GameID   string  `json:"gameId,omitempty"`
	Slot     *int    `json:"slot,omitempty"`
	// PakKind is the kind of a "pak" target (tool, emu, auto; M12).
	PakKind *string `json:"pakKind,omitempty"`
}

// destination is a resolved Target.
type destination struct {
	kind string
	dir  string // rom: destination folder
	name string // rom, staging: cleaned file name ("" while unknown)
	game osadapter.Game
	slot int
}

// Options are the dependencies of a Service.
type Options struct {
	Library *library.Library
	Adapter osadapter.Adapter
	Layout  store.Layout
	Trash   *store.Trash
	// Saves and States install the files of the "save" and "state"
	// targets (backup copy, locks).
	Saves  *saves.Service
	States *states.Service
	// Lock serializes the changes of the SD card files; share it with
	// Saves, States and the cheats (nil: a lock of its own).
	Lock *sync.Mutex
	// Writes runs the API writes one at a time (M13); the server shares it
	// through Service.Writes. An upload's final step and a job's install
	// step hold it (nil: a lock of its own).
	Writes *writelock.Lock
	// FreeSpace returns the free bytes of the file system holding path
	// (the SD card). Tests inject it to simulate a full card.
	FreeSpace func(path string) (int64, error)
	Now       func() time.Time // nil: time.Now
	Logger    *slog.Logger     // nil: slog.Default()
	// ChunkSize is the size of upload chunks (0: DefaultChunkSize).
	ChunkSize int64
	// VerifyWait bounds the wait for the hash of a resumed upload (0:
	// DefaultVerifyWait).
	VerifyWait time.Duration
	// Paks knows the installed paks and the running one (M12; nil: the
	// paks of the adapter roots, none of them the running pak).
	Paks *paks.Manager
}

// Service holds the uploads and the jobs.
type Service struct {
	opts Options
	log  *slog.Logger
	now  func() time.Time
	rand io.Reader

	// installMu serializes the installation of files at their
	// destination: check for a conflict, move the old file to the trash,
	// rename. rename(2) replaces an existing file silently, so two
	// installs to the same name must never run together.
	installMu *sync.Mutex

	uploads uploads
	jobs    *Jobs
	paks    *paks.Manager

	// completedStaging maps the uploadId of a completed staging upload
	// to its stagingId (cheat import with an "upload" source). In memory:
	// lost when the pak closes, then the app uses the stagingId.
	stagingMu        sync.Mutex
	completedStaging map[string]string
	stagingOrder     []string

	// Background verification of the resumed uploads.
	verifyCtx    context.Context
	verifyCancel context.CancelFunc
	verifyWG     sync.WaitGroup
	verifySem    chan struct{}

	// rename is os.Rename; tests replace it to simulate failures.
	rename func(oldpath, newpath string) error
}

// New returns a Service. Call LoadUploads at start (after the maintenance)
// to resume the uploads of a previous run, and Close at shutdown.
func New(opts Options) (*Service, error) {
	if opts.Library == nil || opts.Adapter == nil || opts.Trash == nil || opts.Layout.Root == "" ||
		opts.Saves == nil || opts.States == nil {
		return nil, errors.New("transfer: missing dependency")
	}
	if opts.Lock == nil {
		opts.Lock = &sync.Mutex{}
	}
	if opts.Writes == nil {
		opts.Writes = writelock.New(0)
	}
	if opts.FreeSpace == nil {
		return nil, errors.New("transfer: missing FreeSpace")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.ChunkSize <= 0 {
		opts.ChunkSize = DefaultChunkSize
	}
	if opts.ChunkSize > MaxChunkSize {
		opts.ChunkSize = MaxChunkSize
	}
	if opts.Paks == nil {
		opts.Paks = paks.NewManager(opts.Adapter.Roots(), "")
	}
	s := &Service{opts: opts, log: opts.Logger, now: opts.Now, rand: rand.Reader, installMu: opts.Lock, rename: os.Rename, paks: opts.Paks}
	s.uploads.m = map[string]*upload{}
	s.jobs = NewJobs(opts.Now)
	s.verifyCtx, s.verifyCancel = context.WithCancel(context.Background())
	s.verifySem = make(chan struct{}, MaxParallelVerify)
	return s, nil
}

// Jobs returns the job queue.
func (s *Service) Jobs() *Jobs { return s.jobs }

// Close cancels the jobs and waits for them (their partial files are
// deleted). Uploads stay on the card and are resumed at the next start.
func (s *Service) Close() {
	s.verifyCancel()
	s.verifyWG.Wait()
	s.jobs.Close()
}

func (s *Service) randomHex(n int) string {
	b := make([]byte, n)
	if _, err := io.ReadFull(s.rand, b); err != nil {
		panic(fmt.Sprintf("transfer: random: %v", err)) // crypto/rand never fails on Linux
	}
	return hex.EncodeToString(b)
}

// DeleteGame moves a game and its data to the trash (library.DeleteGame)
// under the install lock, so no upload or job can install a file
// between the checks of the move and its renames (or its roll back).
func (s *Service) DeleteGame(gameID string, keepSave bool) (library.DeleteResult, error) {
	s.installMu.Lock()
	defer s.installMu.Unlock()
	return s.opts.Library.DeleteGame(gameID, keepSave, s.opts.Trash)
}

// libraryError maps an error of internal/library.
func libraryError(err error, what string) *Error {
	switch {
	case errors.Is(err, library.ErrInvalidID):
		return newError(http.StatusBadRequest, CodeInvalidID, "Invalid identifier")
	case errors.Is(err, library.ErrNotFound):
		return notFound(what + " not found")
	}
	return nil
}

// resolve checks a target and finds its destination; rom and staging
// targets require fileName.
func (s *Service) resolve(t Target) (destination, *Error, error) {
	d := destination{kind: t.Kind}
	switch t.Kind {
	case KindROM, KindStaging:
		if t.FileName != nil {
			name, err := CleanFileName(*t.FileName)
			if err != nil {
				return d, invalidName("Invalid file name"), nil
			}
			d.name = name
		} else {
			return d, badRequest("Missing target.fileName"), nil
		}
		if t.Kind == KindStaging {
			return d, nil, nil
		}
		if d.name != "" && !s.opts.Adapter.IsGameFile(d.name) {
			// Same rule as the library listing: an accepted ROM always
			// gets a usable gameId.
			return d, invalidName("Not a ROM file name"), nil
		}
		if t.SystemID == "" {
			return d, badRequest("Missing target.systemId"), nil
		}
		folderID := ""
		if t.FolderID != nil {
			if *t.FolderID == "" {
				return d, newError(http.StatusBadRequest, CodeInvalidID, "Invalid identifier"), nil
			}
			folderID = *t.FolderID
		}
		dir, _, err := s.opts.Library.FolderDir(t.SystemID, folderID)
		if err != nil {
			if apiErr := libraryError(err, "Folder"); apiErr != nil {
				return d, apiErr, nil
			}
			return d, nil, err
		}
		d.dir = dir
	case KindPak:
		if t.FileName != nil {
			// Informative (the label of the job): cleaned, never used as a path.
			name, err := CleanFileName(*t.FileName)
			if err != nil {
				return d, invalidName("Invalid file name"), nil
			}
			d.name = name
		}
		if !validPakKind(pakKindOf(t)) {
			return d, badRequest("Invalid target.pakKind"), nil
		}
	case KindSave, KindState:
		if t.GameID == "" {
			return d, badRequest("Missing target.gameId"), nil
		}
		if t.Kind == KindState {
			if t.Slot == nil {
				return d, badRequest("Missing target.slot"), nil
			}
			if !states.ValidSlot(*t.Slot) {
				return d, badRequest("Invalid target.slot"), nil
			}
			d.slot = *t.Slot
		}
		g, err := s.opts.Library.GameByID(t.GameID)
		if err != nil {
			if apiErr := libraryError(err, "Game"); apiErr != nil {
				return d, apiErr, nil
			}
			return d, nil, err
		}
		d.game = g
		if t.Kind == KindState && states.IsLocked(s.opts.Layout, g, d.slot) {
			return d, stateLocked(), nil
		}
		check := s.opts.Adapter.CheckSaveFormat
		if t.Kind == KindState {
			check = s.opts.Adapter.CheckStateFormat
			if s.opts.Adapter.StatePath(g, d.slot) == "" {
				return d, unsupportedFormat("No state folder for this system (emulator pak not found)"), nil
			}
		}
		if err := check(g); err != nil {
			return d, unsupportedFormat(err.Error()), nil
		}
	default:
		return d, badRequest("Unknown target.kind"), nil
	}
	return d, nil, nil
}

// unsupportedFormat: the saves or states of the card use a format the
// pak does not write.
func unsupportedFormat(message string) *Error {
	return newError(http.StatusUnprocessableEntity, CodeUnsupportedFormat, message)
}

func tooMany(message string) *Error {
	return newError(http.StatusTooManyRequests, CodeTooManyAttempts, message)
}

func stateLocked() *Error {
	return newError(http.StatusConflict, CodeStateLocked, "State slot is locked")
}

func validConflict(c string) bool {
	return c == ConflictFail || c == ConflictRename || c == ConflictOverwrite
}

// relToSD returns p relative to the SD card ("/" separator), for the
// informative paths of the API.
func (s *Service) relToSD(p string) string {
	rel, err := filepath.Rel(s.opts.Adapter.Roots().SD, p)
	if err != nil {
		return filepath.Base(p)
	}
	return filepath.ToSlash(rel)
}

// conflictError is 409 conflict with details.existing (path relative to
// the SD card).
func (s *Service) conflictError(existing string) *Error {
	return &Error{
		Status: http.StatusConflict, Code: CodeConflict, Message: "Target file exists",
		Details: map[string]any{"existing": s.relToSD(existing)},
	}
}

// checkConflict reports early (at creation) that a rom target already
// exists with onConflict=fail. install checks again.
func (s *Service) checkConflict(d destination, onConflict string) *Error {
	if d.kind != KindROM || onConflict != ConflictFail || d.name == "" {
		return nil
	}
	if existing := findFold(d.dir, d.name); existing != "" {
		return s.conflictError(filepath.Join(d.dir, existing))
	}
	return nil
}

// stagingIDRe matches st_<YYYYMMDDTHHMMSSZ>_<8 hex>.
var stagingIDRe = regexp.MustCompile(`^st_(\d{8}T\d{6}Z)_[0-9a-f]{8}$`)

const idTimeLayout = "20060102T150405Z"

// StagingFile returns the file of a staging entry (for the cheat import
// and the patches): ErrStagingNotFound when it does not exist. A staging
// folder holds exactly one file, whatever its name (no suffix is ignored),
// so a returned stagingId always resolves.
func (s *Service) StagingFile(stagingID string) (string, error) {
	if !stagingIDRe.MatchString(stagingID) {
		return "", ErrStagingNotFound
	}
	dir := filepath.Join(s.opts.Layout.StagingDir(), stagingID)
	if confine.CheckDir(s.opts.Layout.Root, dir) != nil {
		return "", ErrStagingNotFound // never through a link
	}
	des, err := os.ReadDir(dir)
	if err != nil {
		return "", ErrStagingNotFound
	}
	for _, d := range des {
		if d.Type().IsRegular() {
			return filepath.Join(dir, d.Name()), nil
		}
	}
	return "", ErrStagingNotFound
}

// MaxRememberedStaging bounds the completed staging uploads remembered.
const MaxRememberedStaging = 256

func (s *Service) rememberStaging(uploadID, stagingID string) {
	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()
	if s.completedStaging == nil {
		s.completedStaging = map[string]string{}
	}
	if _, ok := s.completedStaging[uploadID]; !ok {
		s.stagingOrder = append(s.stagingOrder, uploadID)
	}
	s.completedStaging[uploadID] = stagingID
	for len(s.stagingOrder) > MaxRememberedStaging {
		delete(s.completedStaging, s.stagingOrder[0])
		s.stagingOrder = s.stagingOrder[1:]
	}
}

// StagingForUpload returns the file of the staging entry made by a
// completed upload (target "staging") of this run: ErrStagingNotFound when
// the upload is unknown, not completed, or its file was purged.
func (s *Service) StagingForUpload(uploadID string) (string, error) {
	s.stagingMu.Lock()
	id, ok := s.completedStaging[uploadID]
	s.stagingMu.Unlock()
	if !ok {
		return "", ErrStagingNotFound
	}
	return s.StagingFile(id)
}

// ErrStagingNotFound means the stagingId names no file.
var ErrStagingNotFound = errors.New("staging file not found")

// install moves the synced temporary file tmp to its destination,
// applying onConflict, and returns the created resource (UploadResult).
// The file being replaced, if any, goes to the trash first.
//
// Power loss during an overwrite: the replacement is journaled in its
// trash entry (store.Trash.Replace): the next start finishes it when the
// new file is complete, or puts the old file back, and the old file is
// never purged before.
func (s *Service) install(d destination, tmp, onConflict string) (map[string]any, *Error, error) {
	s.installMu.Lock()
	defer s.installMu.Unlock()
	return s.installLocked(d, tmp, onConflict)
}

// installLocked is install with s.installMu held.
func (s *Service) installLocked(d destination, tmp, onConflict string) (map[string]any, *Error, error) {
	switch d.kind {
	case KindROM:
		return s.installROM(d, tmp, onConflict)
	case KindStaging:
		id := "st_" + s.now().UTC().Format(idTimeLayout) + "_" + s.randomHex(4)
		dir := filepath.Join(s.opts.Layout.StagingDir(), id)
		if err := s.dataDir(dir); err != nil {
			return nil, nil, err
		}
		if err := os.Rename(tmp, filepath.Join(dir, d.name)); err != nil {
			os.Remove(dir)
			return nil, nil, err
		}
		store.SyncDir(dir)
		store.SyncDir(filepath.Dir(tmp))
		return map[string]any{"stagingId": id}, nil, nil
	case KindSave:
		// The previous save is kept as a before-replace backup copy.
		backup, err := s.opts.Saves.InstallLocked(d.game, tmp)
		if errors.Is(err, osadapter.ErrUnsupportedFormat) {
			return nil, unsupportedFormat(err.Error()), nil
		}
		if err != nil {
			return nil, nil, err
		}
		return map[string]any{"backup": backup}, nil, nil
	case KindState:
		slot, err := s.opts.States.InstallLocked(d.game, d.slot, tmp)
		if errors.Is(err, states.ErrLocked) {
			return nil, stateLocked(), nil
		}
		if errors.Is(err, osadapter.ErrUnsupportedFormat) {
			return nil, unsupportedFormat(err.Error()), nil
		}
		if err != nil {
			return nil, nil, err
		}
		return map[string]any{"slot": slot}, nil, nil
	}
	return nil, nil, fmt.Errorf("install: unknown kind %q", d.kind)
}

func (s *Service) installROM(d destination, tmp, onConflict string) (map[string]any, *Error, error) {
	name := d.name
	if existing := findFold(d.dir, name); existing != "" {
		path := filepath.Join(d.dir, existing)
		switch onConflict {
		case ConflictRename:
			if name = freeName(d.dir, name); name == "" {
				return nil, s.conflictError(path), nil
			}
		case ConflictOverwrite:
			fi, err := os.Lstat(path)
			if err != nil {
				return nil, nil, err
			}
			if !fi.Mode().IsRegular() {
				// Never replace a folder (a multi-disc game) by a file.
				return nil, s.conflictError(path), nil
			}
			src, err := os.Lstat(tmp)
			if err != nil {
				return nil, nil, err
			}
			dst := filepath.Join(d.dir, name)
			if _, err := s.opts.Trash.Replace(store.ReasonOverwrite, existing, []store.MoveItem{{Path: path}},
				[]store.Install{{Src: tmp, Dst: dst, Size: src.Size()}}); err != nil {
				return nil, nil, fmt.Errorf("install rom: %w", err)
			}
			return s.romAdded(dst)
		default:
			return nil, s.conflictError(path), nil
		}
	}
	dst := filepath.Join(d.dir, name)
	if err := os.Rename(tmp, dst); err != nil {
		return nil, nil, fmt.Errorf("install rom: %w", err)
	}
	store.SyncDir(d.dir)
	store.SyncDir(filepath.Dir(tmp))
	return s.romAdded(dst)
}

// romAdded is the result of an installed ROM.
func (s *Service) romAdded(dst string) (map[string]any, *Error, error) {
	rel, err := s.opts.Library.RelToRoms(dst)
	if err != nil {
		return nil, nil, err
	}
	s.log.Info("rom added", "path", s.relToSD(dst))
	return map[string]any{"gameId": library.EncodeID(rel)}, nil, nil
}

// Maintain deletes the expired temporary files at start
// (docs/specs/05-console-pak.md §2.3): uploads idle for more than
// UploadIdleExpiry, staging files older than StagingExpiry, temporary
// files of a previous run. The patch installations a power cut
// interrupted are finished first. Call it after the trash recovery
// (store.Trash.Recover) and before LoadUploads.
func (s *Service) Maintain() error {
	keep := s.recoverPatches()
	// The temporary files of a replacement the trash could not finish nor
	// roll back stay for the next start.
	for p := range s.opts.Trash.PendingSources() {
		if filepath.Dir(p) == filepath.Clean(s.opts.Layout.UploadsDir()) {
			if keep == nil {
				keep = map[string]bool{}
			}
			keep[filepath.Base(p)] = true
		}
	}
	if err := s.purgeUploads(keep); err != nil {
		return err
	}
	s.purgePakStaging()
	return s.purgeStaging()
}

func (s *Service) purgeStaging() error {
	dir := s.opts.Layout.StagingDir()
	if err := confine.CheckDir(s.opts.Layout.Root, dir); err != nil {
		// Never purged through a link; every use of the folder refuses
		// it too.
		s.log.Error("staging: folder reached through a link, left as it is", "error", err)
		return nil
	}
	des, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("staging: %w", err)
	}
	limit := s.now().Add(-StagingExpiry)
	for _, d := range des {
		path := filepath.Join(dir, d.Name())
		expired := true
		if m := stagingIDRe.FindStringSubmatch(d.Name()); m != nil {
			if at, err := time.Parse(idTimeLayout, m[1]); err == nil {
				expired = at.Before(limit)
			}
		} else if info, err := d.Info(); err == nil {
			expired = info.ModTime().Before(limit)
		}
		if !expired {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			s.log.Warn("staging: cannot delete an expired file", "entry", d.Name(), "error", err)
			continue
		}
		s.log.Info("staging: deleted an expired file", "entry", d.Name())
	}
	return nil
}

// SetArt writes the PNG data (already checked) to a synced temporary file
// of the uploads folder, then installs it as the art of a game under the
// install lock (library.SetArt). An error wrapping syscall.ENOSPC means
// the card is full.
func (s *Service) SetArt(gameID string, data []byte) (library.Game, error) {
	dir := s.opts.Layout.UploadsDir()
	if err := s.dataDir(dir); err != nil {
		return library.Game{}, err
	}
	tmp := filepath.Join(dir, "art_"+s.randomHex(12)+partExt)
	if err := store.WriteFileAtomic(tmp, data, 0o644); err != nil {
		os.Remove(tmp)
		return library.Game{}, err
	}
	s.installMu.Lock()
	defer s.installMu.Unlock()
	g, err := s.opts.Library.SetArt(gameID, tmp, s.opts.Trash)
	if !errors.Is(err, store.ErrReplaceFailed) {
		os.Remove(tmp) // already gone once installed; kept for a pending replacement
	}
	return g, err
}

// DeleteArt moves the art of a game to the trash under the install lock.
func (s *Service) DeleteArt(gameID string) (library.Game, error) {
	s.installMu.Lock()
	defer s.installMu.Unlock()
	return s.opts.Library.DeleteArt(gameID, s.opts.Trash)
}

// Writes returns the lock that runs the API writes one at a time (M13).
func (s *Service) Writes() *writelock.Lock { return s.opts.Writes }

// ErrorDeviceBusy is the answer to a request that waited too long for the
// write in progress (429 device_busy, M13).
func ErrorDeviceBusy() *Error {
	return newError(http.StatusTooManyRequests, CodeDeviceBusy, "Device busy")
}

type writeCheckKey struct{}

// WithWriteCheck returns a context whose write, once it holds the write
// lock, first calls check: a non-nil error refuses the write (the server
// checks there that the client was not disconnected or forgotten while
// it waited, M13).
func WithWriteCheck(ctx context.Context, check func() *Error) context.Context {
	return context.WithValue(ctx, writeCheckKey{}, check)
}

// acquireWrites takes the write lock for a request: busy after its wait,
// shutting_down when ctx ends first, or the error of the write check of
// ctx (the lock is then released).
func (s *Service) acquireWrites(ctx context.Context) *Error {
	switch err := s.opts.Writes.AcquireRequest(ctx); {
	case err == nil:
		if check, ok := ctx.Value(writeCheckKey{}).(func() *Error); ok {
			if apiErr := check(); apiErr != nil {
				s.opts.Writes.Release()
				return apiErr
			}
		}
		return nil
	case errors.Is(err, writelock.ErrBusy):
		return ErrorDeviceBusy()
	default:
		return newError(http.StatusServiceUnavailable, CodeShuttingDown, "Request cancelled")
	}
}

// submitGuarded queues a job under the admission of its guard (M13).
func (s *Service) submitGuarded(kind, label string, guard JobGuard, run RunFunc) (string, *Error, error) {
	return s.submitWithCleanup(kind, label, guard, run, nil)
}

// submitWithCleanup is submitGuarded with the cleanup of the job (M12).
func (s *Service) submitWithCleanup(kind, label string, guard JobGuard, run RunFunc, cleanup func()) (string, *Error, error) {
	if guard.Admit != nil {
		unlock, refused := guard.Admit()
		if refused != nil {
			return "", refused, nil
		}
		defer unlock()
	}
	id, err := s.jobs.SubmitWithCleanup(kind, label, guard, run, cleanup)
	if errors.Is(err, ErrTooManyJobs) {
		return "", tooMany("Too many jobs"), nil
	}
	if errors.Is(err, ErrJobsClosed) {
		return "", newError(http.StatusServiceUnavailable, CodeShuttingDown, "Shutting down"), nil
	}
	if err != nil {
		return "", nil, err
	}
	return id, nil, nil
}

// dataDir checks that dir, a folder of the ReminisLink data area
// (uploads, staging), is reached without a link from the data root (the
// pak refuses to start when the root itself is one, M20), then creates it.
// No upload, staging file or extracted archive is ever written, opened or
// deleted through a link (rule 2, M19 Codex review: on Knulli the data
// area is on an ext4 volume where a link can point anywhere).
func (s *Service) dataDir(dir string) error {
	root := s.opts.Layout.Root
	if err := confine.CheckDir(root, dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return confine.CheckDir(root, dir)
}
