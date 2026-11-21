package server

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/confine"
	"github.com/tfournier/ReminisLink/internal/gamearchive"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/transfer"
)

// Game archives and the state listing (M19, protocol/openapi.yaml).

// handleListAllStates is GET /states: every state with its SHA-256.
func (s *Server) handleListAllStates(w http.ResponseWriter, r *http.Request) {
	l, err := s.opts.States.ListAll(r.Context())
	if err != nil {
		if r.Context().Err() != nil {
			if s.ShuttingDown() {
				auth.WriteError(w, http.StatusServiceUnavailable, auth.CodeShuttingDown, "Shutting down", nil)
				return
			}
			s.log.Info("state listing cancelled")
			return
		}
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

// boolQuery reads an optional boolean query parameter ("true" or
// "false"); ok is false (400 answered) for any other value.
func boolQuery(w http.ResponseWriter, r *http.Request, name string) (value, ok bool) {
	switch v := r.URL.Query().Get(name); v {
	case "", "false":
		return false, true
	case "true":
		return true, true
	}
	auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "Invalid "+name, nil)
	return false, false
}

// archiveFile is a file to put in an exported archive.
type archiveFile struct {
	role   string
	slot   *int
	fileID string // a file of GET /games/{gameId}
	thumb  string // a state thumbnail: its path
}

// handleExportArchive is GET /games/{gameId}/archive: the game's data as a
// zip, built from its current files as GET /games/{gameId} lists them and
// opened as GET /games/{gameId}/files/{fileId} does (confined). A read:
// never waits for a write.
func (s *Server) handleExportArchive(w http.ResponseWriter, r *http.Request) {
	withROM, ok := boolQuery(w, r, "rom")
	if !ok {
		return
	}
	g, ok := s.game(w, r)
	if !ok {
		return
	}
	if withROM && g.Kind != osadapter.KindFile {
		auth.WriteError(w, http.StatusUnprocessableEntity, CodeUnsupportedFormat,
			"Only the ROM of a single-file game can be exported", nil)
		return
	}
	gameID := r.PathValue("gameId")
	d, err := s.opts.Library.Game(gameID)
	if err != nil {
		s.libraryError(w, r, err)
		return
	}
	var files []archiveFile
	for _, f := range d.Files {
		switch f.Role {
		case gamearchive.RoleROM:
			if !withROM {
				continue
			}
		case gamearchive.RoleSave, gamearchive.RoleRTC, gamearchive.RoleCheats, gamearchive.RoleArt:
		case gamearchive.RoleState:
			files = append(files, archiveFile{role: f.Role, slot: f.Slot, fileID: f.FileID})
			if p, err := s.opts.States.Thumbnail(g, *f.Slot); err == nil {
				files = append(files, archiveFile{role: gamearchive.RoleStateThumbnail, slot: f.Slot, thumb: p})
			}
			continue
		default:
			continue
		}
		files = append(files, archiveFile{role: f.Role, slot: f.Slot, fileID: f.FileID})
	}
	if len(files) == 0 {
		auth.WriteError(w, http.StatusNotFound, CodeNotFound, "Nothing to export", nil)
		return
	}
	info, _ := s.opts.Adapter.DeviceInfo()
	name := s.DeviceName()
	src := gamearchive.Source{
		DeviceName: &name, OS: info.OS, SystemTag: g.System.Tag, SystemLabel: g.System.Label,
		GameName: g.Name, FileName: g.FileName, RomName: g.ROMName, Kind: string(g.Kind),
	}
	if info.Platform != "" {
		src.Platform = &info.Platform
	}
	if v := s.opts.Identity.PakVersion; v != "" {
		src.PakVersion = &v
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDisposition(g.Name+gamearchive.FileSuffix))
	w.WriteHeader(http.StatusOK)
	iw := &idleWriter{w: w, ctl: http.NewResponseController(w), timeout: s.timeouts.sendIdle}
	defer func() { _ = iw.ctl.SetWriteDeadline(time.Time{}) }() // the connection may be reused
	zw := gamearchive.NewWriter(iw)
	roots := s.opts.Adapter.Roots()
	var total int64
	for _, af := range files {
		if r.Context().Err() != nil {
			return
		}
		var f *os.File
		var fi os.FileInfo
		var fname string
		if af.thumb != "" {
			f, fi, err = confine.OpenUnder(roots.Minui, af.thumb)
			fname = filepath.Base(af.thumb)
		} else {
			f, fi, fname, err = s.opts.Library.OpenFile(gameID, af.fileID)
		}
		if err != nil {
			// Gone or refused since the listing: left out.
			if errors.Is(err, confine.ErrOutside) {
				s.log.Warn("archive: file refused", "role", af.role)
			}
			continue
		}
		// A file an import would refuse (too large, an image that is not
		// a PNG, an unreadable cheat file) is left out, as is anything
		// beyond the total an import accepts.
		if !gamearchive.Exportable(af.role, fi.Size(), f) || total+fi.Size() > gamearchive.MaxTotalBytes {
			f.Close()
			s.log.Warn("archive: file left out", "role", af.role)
			continue
		}
		total += fi.Size()
		_, err = zw.Add(af.role, af.slot, fname, fi.ModTime(), io.LimitReader(f, fi.Size()))
		f.Close()
		if err != nil {
			s.log.Warn("archive export interrupted", "error", err)
			return
		}
	}
	if err := zw.Close(src, s.now()); err != nil {
		s.log.Warn("archive export interrupted", "error", err)
		return
	}
	_ = iw.ctl.SetWriteDeadline(time.Now().Add(iw.timeout))
	_ = iw.ctl.Flush()
	s.log.Info("archive exported", "game", g.Path, "files", len(zw.Files()), "rom", withROM)
}

// archiveTargetQuery reads the target of GET /archives/{stagingId} from
// the query (nil: none given, the pak suggests one).
func archiveTargetQuery(r *http.Request) *transfer.ArchiveTarget {
	q := r.URL.Query()
	var t transfer.ArchiveTarget
	opt := func(name string) *string {
		if !q.Has(name) {
			return nil
		}
		v := q.Get(name)
		return &v
	}
	t.GameID, t.SystemID, t.FolderID = opt("gameId"), opt("systemId"), opt("folderId")
	if t.GameID == nil && t.SystemID == nil && t.FolderID == nil {
		return nil
	}
	return &t
}

// handleInspectArchive is GET /archives/{stagingId}.
func (s *Server) handleInspectArchive(w http.ResponseWriter, r *http.Request) {
	t := archiveTargetQuery(r)
	if t != nil && t.GameID == nil && t.SystemID == nil {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "folderId needs systemId", nil)
		return
	}
	plan, apiErr, err := s.opts.Transfers.InspectArchive(r.Context(), r.PathValue("stagingId"), t)
	if apiErr != nil || err != nil {
		s.transferError(w, r, apiErr, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// handleImportArchive is POST /archives/{stagingId}/import (a job).
func (s *Server) handleImportArchive(w http.ResponseWriter, r *http.Request) {
	var req transfer.ArchiveImportRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	id, apiErr, err := s.opts.Transfers.StartArchiveImport(r.PathValue("stagingId"), req, s.jobGuard(r))
	if apiErr != nil || err != nil {
		s.transferError(w, r, apiErr, err)
		return
	}
	s.jobStarted(w, r, id)
}
