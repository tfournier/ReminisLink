package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/screenshots"
)

// M22: the screenshots of the console (protocol/openapi.yaml, tag
// Screenshots).

func (s *Server) handleListScreenshots(w http.ResponseWriter, r *http.Request) {
	list, err := s.opts.Screenshots.List(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetScreenshot(w http.ResponseWriter, r *http.Request) {
	// The descriptor checked by Open is the one served.
	f, _, name, contentType, err := s.opts.Screenshots.Open(r.PathValue("screenshotId"))
	if err != nil {
		s.screenshotError(w, r, err)
		return
	}
	w.Header().Set("Content-Disposition", contentDisposition(name))
	s.sendFile(w, r, f, name, contentType)
}

func (s *Server) handleScreenshotThumbnail(w http.ResponseWriter, r *http.Request) {
	data, err := s.opts.Screenshots.Thumbnail(r.Context(), r.PathValue("screenshotId"))
	if err != nil {
		s.screenshotError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleTrashScreenshots(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ScreenshotIDs []string `json:"screenshotIds"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.ScreenshotIDs) == 0 || len(req.ScreenshotIDs) > screenshots.MaxTrash {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "screenshotIds must hold 1 to 500 identifiers", nil)
		return
	}
	res, err := s.opts.Screenshots.Trash(req.ScreenshotIDs)
	var partial *screenshots.PartialError
	if errors.As(err, &partial) {
		// The folder may have changed (a file that could not go back
		// stays in a trash entry): the clients reload it.
		s.events.Screenshots()
	}
	if err != nil {
		s.screenshotError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		TrashEntryID string   `json:"trashEntryId"`
		Moved        []string `json:"moved"`
		MovedBytes   int64    `json:"movedBytes"`
	}{res.Entry.ID, res.Moved, res.MovedBytes})
}

// screenshotsChanged publishes a screenshots event (M14, M22) after a
// successful (2xx) answer.
func (s *Server) screenshotsChanged(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w}
		h(rec, r)
		if rec.status >= 200 && rec.status < 300 {
			s.events.Screenshots()
		}
	}
}

// screenshotError answers an error of internal/screenshots.
func (s *Server) screenshotError(w http.ResponseWriter, r *http.Request, err error) {
	var missing *screenshots.MissingError
	var partial *screenshots.PartialError
	switch {
	case errors.As(err, &partial):
		// Codex review of M22: the clients learn what stayed in the trash.
		s.log.Error("screenshots: move to the trash failed midway", "error", err)
		auth.WriteError(w, http.StatusInternalServerError, auth.CodeInternal, "Some screenshots could not be moved",
			map[string]any{"kept": nonNilStrings(partial.Kept)})
	case errors.Is(err, screenshots.ErrInvalidID):
		auth.WriteError(w, http.StatusBadRequest, CodeInvalidID, "Invalid identifier", nil)
	case errors.As(err, &missing):
		auth.WriteError(w, http.StatusNotFound, CodeNotFound, "Screenshot not found", map[string]any{"missing": missing.IDs})
	case errors.Is(err, screenshots.ErrNotFound):
		auth.WriteError(w, http.StatusNotFound, CodeNotFound, "Screenshot not found", nil)
	default:
		s.internal(w, r, err)
	}
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
