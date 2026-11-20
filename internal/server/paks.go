package server

import (
	"errors"
	"net/http"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/paks"
	"github.com/tfournier/ReminisLink/internal/transfer"
)

// M12: the paks of the console and the read-only SD browser
// (protocol/openapi.yaml, tags Paks and SD).

func (s *Server) handleListPaks(w http.ResponseWriter, r *http.Request) {
	list, err := s.opts.Transfers.Paks().List()
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Paks []paks.Pak `json:"paks"`
	}{list})
}

func (s *Server) handleDeletePak(w http.ResponseWriter, r *http.Request) {
	res, apiErr, err := s.opts.Transfers.DeletePak(r.PathValue("pakId"))
	if apiErr != nil || err != nil {
		s.transferError(w, r, apiErr, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// pakName is the display name of the {pakId} of the request ("" when it
// names none), for the banner of the pak screen.
func (s *Server) pakName(r *http.Request) string {
	p, _, err := s.opts.Transfers.Paks().Resolve(r.PathValue("pakId"))
	if err != nil {
		return ""
	}
	return p.Name
}

func (s *Server) handleInstallPak(w http.ResponseWriter, r *http.Request) {
	var req transfer.PakInstallRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	id, apiErr, err := s.opts.Transfers.StartPakInstall(req, s.jobGuard(r))
	if apiErr != nil || err != nil {
		s.transferError(w, r, apiErr, err)
		return
	}
	s.jobStarted(w, r, id)
}

// paksChanged publishes a paks event (M14) after a successful (2xx)
// answer.
func (s *Server) paksChanged(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w}
		h(rec, r)
		if rec.status >= 200 && rec.status < 300 {
			s.events.Paks()
		}
	}
}

func (s *Server) handleBrowseSD(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	folderID := q.Get("folderId")
	if q.Has("folderId") && folderID == "" {
		auth.WriteError(w, http.StatusBadRequest, CodeInvalidID, "Invalid identifier", nil)
		return
	}
	folder, err := s.opts.Library.Browse(folderID)
	if err != nil {
		s.sdError(w, r, err, "Folder not found")
		return
	}
	writeJSON(w, http.StatusOK, folder)
}

func (s *Server) handleSDContent(w http.ResponseWriter, r *http.Request) {
	f, _, name, err := s.opts.Library.OpenSDEntry(r.PathValue("sdEntryId"))
	if err != nil {
		s.sdError(w, r, err, "File not found")
		return
	}
	s.sendFile(w, r, f, name, "application/octet-stream")
}

// sdError answers an error of the SD browser.
func (s *Server) sdError(w http.ResponseWriter, r *http.Request, err error, notFound string) {
	switch {
	case errors.Is(err, library.ErrInvalidID):
		auth.WriteError(w, http.StatusBadRequest, CodeInvalidID, "Invalid identifier", nil)
	case errors.Is(err, library.ErrNotFound):
		auth.WriteError(w, http.StatusNotFound, CodeNotFound, notFound, nil)
	default:
		s.internal(w, r, err)
	}
}
