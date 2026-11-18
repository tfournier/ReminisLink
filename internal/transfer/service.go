package transfer

import (
	"crypto/rand"
	"crypto/x509"
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

	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/saves"
	"github.com/tfournier/ReminisLink/internal/states"
	"github.com/tfournier/ReminisLink/internal/store"
)

// Target kinds (UploadTarget in protocol/openapi.yaml).
const (
	KindROM     = "rom"
	KindSave    = "save"
	KindState   = "state"
	KindStaging = "staging"
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

// Target is the destination of an upload or a download.
type Target struct {
	Kind     string  `json:"kind"`
	SystemID string  `json:"systemId,omitempty"`
	FolderID *string `json:"folderId,omitempty"`
	FileName *string `json:"fileName,omitempty"`
	GameID   string  `json:"gameId,omitempty"`
	Slot     *int    `json:"slot,omitempty"`
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
	// FreeSpace returns the free bytes of the file system holding path
	// (the SD card). Tests inject it to simulate a full card.
	FreeSpace func(path string) (int64, error)
	Now       func() time.Time // nil: time.Now
	Logger    *slog.Logger     // nil: slog.Default()
	// ChunkSize is the size of upload chunks (0: DefaultChunkSize).
	ChunkSize int64
	// Downloads configures the HTTP client of POST /downloads.
	Downloads DownloadOptions
}

// DownloadOptions configure the downloads.
type DownloadOptions struct {
	// RootCAs are the trusted certificate authorities (nil: LoadRootCAs
	// with CABundle).
	RootCAs *x509.CertPool
	// CABundle is the PEM bundle used when the system has no certificate
	// store (empty: DefaultCABundle).
	CABundle string
	// Transport replaces the HTTP transport (tests).
	Transport http.RoundTripper
	// RetryDelay is the wait before the first retry, doubled each time
	// (0: DefaultRetryDelay).
	RetryDelay time.Duration
	// IdleTimeout is the longest wait for the next bytes (0:
	// DownloadIdleTimeout).
	IdleTimeout time.Duration
}

// Service holds the uploads, the jobs and the downloads.
type Service struct {
	opts Options
	log  *slog.Logger
	now  func() time.Time
	rand io.Reader

	// installMu serializes the installation of files at their
	// destination: check for a conflict, move the old file to the trash,
	// rename. rename(2) replaces an existing file silently, so two
	// installs to the same name must never run together.
	installMu sync.Mutex

	uploads uploads
	jobs    *Jobs

	clientOnce sync.Once
	client     *http.Client
	clientErr  error
}

// New returns a Service. Call LoadUploads at start (after the maintenance)
// to resume the uploads of a previous run, and Close at shutdown.
func New(opts Options) (*Service, error) {
	if opts.Library == nil || opts.Adapter == nil || opts.Trash == nil || opts.Layout.Root == "" {
		return nil, errors.New("transfer: missing dependency")
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
	s := &Service{opts: opts, log: opts.Logger, now: opts.Now, rand: rand.Reader}
	s.uploads.m = map[string]*upload{}
	s.jobs = NewJobs(opts.Now)
	return s, nil
}

// Jobs returns the job queue.
func (s *Service) Jobs() *Jobs { return s.jobs }

// Close cancels the jobs and waits for them (their partial files are
// deleted). Uploads stay on the card and are resumed at the next start.
func (s *Service) Close() { s.jobs.Close() }

func (s *Service) randomHex(n int) string {
	b := make([]byte, n)
	if _, err := io.ReadFull(s.rand, b); err != nil {
		panic(fmt.Sprintf("transfer: random: %v", err)) // crypto/rand never fails on Linux
	}
	return hex.EncodeToString(b)
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

// resolve checks a target and finds its destination. needName requires
// fileName for rom and staging targets (uploads; downloads may deduce it).
func (s *Service) resolve(t Target, needName bool) (destination, *Error, error) {
	d := destination{kind: t.Kind}
	switch t.Kind {
	case KindROM, KindStaging:
		if t.FileName != nil {
			name, err := CleanFileName(*t.FileName)
			if err != nil {
				return d, invalidName("Invalid file name"), nil
			}
			d.name = name
		} else if needName {
			return d, badRequest("Missing target.fileName"), nil
		}
		if t.Kind == KindStaging {
			return d, nil, nil
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
	default:
		return d, badRequest("Unknown target.kind"), nil
	}
	return d, nil, nil
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
// and the patches): ErrStagingNotFound when it does not exist.
func (s *Service) StagingFile(stagingID string) (string, error) {
	if !stagingIDRe.MatchString(stagingID) {
		return "", ErrStagingNotFound
	}
	dir := filepath.Join(s.opts.Layout.StagingDir(), stagingID)
	des, err := os.ReadDir(dir)
	if err != nil {
		return "", ErrStagingNotFound
	}
	for _, d := range des {
		if d.Type().IsRegular() && !isTemp(d.Name()) {
			return filepath.Join(dir, d.Name()), nil
		}
	}
	return "", ErrStagingNotFound
}

// ErrStagingNotFound means the stagingId names no file.
var ErrStagingNotFound = errors.New("staging file not found")

func isTemp(name string) bool { return filepath.Ext(name) == ".tmp" }

// install moves the synced temporary file tmp to its destination,
// applying onConflict, and returns the created resource (UploadResult).
// The file being replaced, if any, goes to the trash first.
func (s *Service) install(d destination, tmp, onConflict string) (map[string]any, *Error, error) {
	s.installMu.Lock()
	defer s.installMu.Unlock()
	switch d.kind {
	case KindROM:
		return s.installROM(d, tmp, onConflict)
	case KindStaging:
		id := "st_" + s.now().UTC().Format(idTimeLayout) + "_" + s.randomHex(4)
		dir := filepath.Join(s.opts.Layout.StagingDir(), id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
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
		backup, err := saves.Install(s.opts.Adapter, s.opts.Trash, d.game, tmp)
		if err != nil {
			return nil, nil, err
		}
		s.log.Info("save replaced", "game", d.game.Path)
		return map[string]any{"backup": backup}, nil, nil
	case KindState:
		slot, err := states.Install(s.opts.Adapter, s.opts.Trash, s.opts.Layout, d.game, d.slot, tmp)
		if errors.Is(err, states.ErrLocked) {
			return nil, stateLocked(), nil
		}
		if err != nil {
			return nil, nil, err
		}
		s.log.Info("state replaced", "game", d.game.Path, "slot", d.slot)
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
			if _, err := s.opts.Trash.Move(store.ReasonOverwrite, existing, []store.MoveItem{{Path: path}}); err != nil {
				return nil, nil, err
			}
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
	rel, err := s.opts.Library.RelToRoms(dst)
	if err != nil {
		return nil, nil, err
	}
	s.log.Info("rom added", "path", s.relToSD(dst))
	return map[string]any{"gameId": library.EncodeID(rel)}, nil, nil
}

// Maintain deletes the expired temporary files at start
// (docs/specs/05-console-pak.md §2.3): uploads idle for more than
// UploadIdleExpiry, staging files older than StagingExpiry, partial
// downloads of a previous run. Call it before LoadUploads.
func (s *Service) Maintain() error {
	if err := s.purgeUploads(); err != nil {
		return err
	}
	return s.purgeStaging()
}

func (s *Service) purgeStaging() error {
	dir := s.opts.Layout.StagingDir()
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
