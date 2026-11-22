package transfer

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/store"
)

// M23: writes of the SD card browser (upload target "sd-file", POST
// /sd/folders, POST /sd/trash) and the renaming of a game (POST
// /games/{gameId}/rename). Every one runs under the install lock, as the
// other installs, and the server holds the write lock around it.

// KindSDFile is the upload target of a file added to a folder of the SD
// card browser.
const KindSDFile = "sd-file"

// checkSDTarget refuses the conflict rule overwrite for an sd-file target:
// a file of the browser is never replaced.
func checkSDTarget(t Target, onConflict string) *Error {
	if t.Kind == KindSDFile && onConflict == ConflictOverwrite {
		return badRequest("onConflict overwrite is not allowed for an sd-file target")
	}
	return nil
}

// protectedError is 409 conflict with details.protected (SdProtection).
func protectedError(p *library.ProtectedError) *Error {
	details := map[string]any{"protected": p.Reason}
	if len(p.IDs) > 0 {
		details["refused"] = p.IDs
	}
	return &Error{Status: http.StatusConflict, Code: CodeConflict, Message: "Protected place", Details: details}
}

// sdError maps an error of the SD browser of internal/library.
func sdError(err error, what string) *Error {
	var p *library.ProtectedError
	var m *library.MissingError
	switch {
	case errors.As(err, &p):
		return protectedError(p)
	case errors.As(err, &m):
		return &Error{Status: http.StatusNotFound, Code: CodeNotFound, Message: what + " not found", Details: map[string]any{"missing": m.IDs}}
	}
	return libraryError(err, what)
}

// resolveSDFile resolves an sd-file target: its cleaned name and its
// writable folder.
func (s *Service) resolveSDFile(t Target, d destination) (destination, *Error, error) {
	if t.FileName == nil {
		return d, badRequest("Missing target.fileName"), nil
	}
	name, err := CleanFileName(*t.FileName)
	if err != nil {
		return d, invalidName("Invalid file name"), nil
	}
	d.name = name
	folderID := ""
	if t.FolderID != nil {
		if *t.FolderID == "" {
			return d, newError(http.StatusBadRequest, CodeInvalidID, "Invalid identifier"), nil
		}
		folderID = *t.FolderID
	}
	dir, rel, err := s.opts.Library.SDFolderDir(folderID)
	if err != nil {
		if apiErr := sdError(err, "Folder"); apiErr != nil {
			return d, apiErr, nil
		}
		return d, nil, err
	}
	d.dir, d.rel = dir, rel
	return d, nil, nil
}

// sdConflictError is 409 conflict for a taken name of the browser, with
// the free name onConflict=rename would give (null when none).
func (s *Service) sdConflictError(dir, existing, name string) *Error {
	e := s.conflictError(filepath.Join(dir, existing))
	var free any
	if f := freeName(dir, name); f != "" {
		free = f
	}
	e.Details["freeName"] = free
	return e
}

// installSDFile installs an uploaded file in a folder of the browser,
// never replacing an entry. Called with s.installMu held.
func (s *Service) installSDFile(d destination, tmp, onConflict string) (map[string]any, *Error, error) {
	name := d.name
	if existing := findFold(d.dir, name); existing != "" {
		if onConflict != ConflictRename {
			return nil, s.sdConflictError(d.dir, existing, name), nil
		}
		if name = freeName(d.dir, name); name == "" {
			return nil, s.sdConflictError(d.dir, existing, d.name), nil
		}
	}
	dst := filepath.Join(d.dir, name)
	if err := s.opts.Trash.RenameNoReplace(tmp, dst); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, s.sdConflictError(d.dir, name, name), nil
		}
		return nil, nil, fmt.Errorf("install sd file: %w", err)
	}
	store.SyncDir(d.dir)
	store.SyncDir(filepath.Dir(tmp))
	rel := path.Join(d.rel, name)
	s.log.Info("sd file added", "path", rel)
	return map[string]any{"sdEntryId": library.SDEntryID(rel)}, nil, nil
}

// CreateSDFolder makes the folder name in the folder parentID ("" for the
// root of the card) of the browser (POST /sd/folders).
func (s *Service) CreateSDFolder(parentID, name string) (library.SDEntry, *Error, error) {
	clean, err := CleanFileName(name)
	if err != nil {
		return library.SDEntry{}, invalidName("Invalid folder name"), nil
	}
	s.installMu.Lock()
	defer s.installMu.Unlock()
	dir, rel, err := s.opts.Library.SDFolderDir(parentID)
	if err != nil {
		if apiErr := sdError(err, "Folder"); apiErr != nil {
			return library.SDEntry{}, apiErr, nil
		}
		return library.SDEntry{}, nil, err
	}
	if existing := findFold(dir, clean); existing != "" {
		return library.SDEntry{}, s.conflictError(filepath.Join(dir, existing)), nil
	}
	root, err := os.OpenRoot(s.opts.Adapter.Roots().SD)
	if err != nil {
		return library.SDEntry{}, nil, err
	}
	defer root.Close()
	newRel := path.Join(rel, clean)
	if err := root.Mkdir(filepath.FromSlash(newRel), 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return library.SDEntry{}, s.conflictError(filepath.Join(dir, clean)), nil
		}
		return library.SDEntry{}, nil, fmt.Errorf("create folder: %w", err)
	}
	store.SyncDir(dir)
	s.log.Info("sd folder created", "path", newRel)
	entry, err := s.opts.Library.SDEntryAt(newRel)
	if err != nil {
		return library.SDEntry{}, nil, err
	}
	return entry, nil, nil
}

// TrashSDEntries moves entries of the browser to the trash in one entry
// (POST /sd/trash, reason delete-file).
func (s *Service) TrashSDEntries(ids []string) (store.MoveResult, *Error, error) {
	s.installMu.Lock()
	defer s.installMu.Unlock()
	sel, err := s.opts.Library.SelectSDTrash(ids)
	if err != nil {
		if apiErr := sdError(err, "Entry"); apiErr != nil {
			return store.MoveResult{}, apiErr, nil
		}
		return store.MoveResult{}, nil, err
	}
	res, err := s.opts.Trash.Move(store.ReasonDeleteFile, sel.Label, sel.Items)
	if errors.Is(err, store.ErrChanged) {
		return store.MoveResult{}, notFound("Entry changed"), nil
	}
	if err != nil {
		return store.MoveResult{}, nil, err
	}
	s.log.Info("sd entries moved to the trash", "entry", res.Entry.ID, "items", len(sel.Items))
	return res, nil, nil
}

// RenameResult is the answer of POST /games/{gameId}/rename.
type RenameResult struct {
	Game    library.Game      `json:"game"`
	Renamed []library.Renamed `json:"renamed"`
}

// CleanGameName checks the new name of a game (POST
// /games/{gameId}/rename): NFC, then refused when it holds a character
// exFAT forbids (never replaced, unlike a file name of an upload), or when
// CleanFileName refuses or changes it (a trailing space or dot).
func CleanGameName(name string) (string, error) {
	n := norm.NFC.String(name)
	if strings.ContainsAny(n, exFATForbidden) {
		return "", ErrInvalidName
	}
	clean, err := CleanFileName(n)
	if err != nil || clean != n {
		return "", ErrInvalidName
	}
	return clean, nil
}

// RenameGame renames a game and all its files as one journaled group.
func (s *Service) RenameGame(gameID, name string) (RenameResult, *Error, error) {
	clean, err := CleanGameName(name)
	if err != nil {
		return RenameResult{}, invalidName("Invalid game name"), nil
	}
	s.installMu.Lock()
	defer s.installMu.Unlock()
	plan, err := s.opts.Library.PlanRename(gameID, clean)
	var conflict *library.RenameConflictError
	switch {
	case errors.As(err, &conflict):
		details := map[string]any{"conflicts": nonNil(conflict.Conflicts)}
		if conflict.Shared {
			details["shared"] = true
		}
		return RenameResult{}, &Error{Status: http.StatusConflict, Code: CodeConflict, Message: "Name already taken", Details: details}, nil
	case errors.Is(err, library.ErrInvalidName):
		return RenameResult{}, invalidName("Invalid game name"), nil
	case errors.Is(err, osadapter.ErrUnsupportedFormat):
		return RenameResult{}, unsupportedFormat(err.Error()), nil
	case err != nil:
		if apiErr := libraryError(err, "Game"); apiErr != nil {
			return RenameResult{}, apiErr, nil
		}
		return RenameResult{}, nil, err
	}
	if err := s.opts.Trash.RenameGroup(plan.Old.Name, plan.Steps); err != nil {
		var taken *store.RenameConflictError
		if errors.As(err, &taken) {
			return RenameResult{}, &Error{Status: http.StatusConflict, Code: CodeConflict, Message: "Name already taken",
				Details: map[string]any{"conflicts": nonNil(taken.Conflicts)}}, nil
		}
		return RenameResult{}, nil, err
	}
	g, err := s.opts.Library.GameSummary(library.EncodeID(plan.New.Path))
	if err != nil {
		return RenameResult{}, nil, fmt.Errorf("renamed game: %w", err)
	}
	if len(plan.Steps) > 0 {
		s.log.Info("game renamed", "from", plan.Old.FileName, "to", plan.New.FileName, "files", len(plan.Steps))
	}
	return RenameResult{Game: g, Renamed: nonNilRenamed(plan.Renamed)}, nil, nil
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func nonNilRenamed(v []library.Renamed) []library.Renamed {
	if v == nil {
		return []library.Renamed{}
	}
	return v
}
