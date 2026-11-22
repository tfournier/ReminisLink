package server

import (
	"net/http"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/library"
)

// M23: writes of the SD card browser and the renaming of a game
// (protocol/openapi.yaml POST /sd/folders, POST /sd/trash, POST
// /games/{gameId}/rename). Each runs under the write lock and publishes a
// library event when it succeeds.

func (s *Server) handleCreateSDFolder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ParentID *string `json:"parentId"`
		Name     *string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == nil {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "Missing name", nil)
		return
	}
	parent := ""
	if req.ParentID != nil {
		if *req.ParentID == "" {
			auth.WriteError(w, http.StatusBadRequest, CodeInvalidID, "Invalid identifier", nil)
			return
		}
		parent = *req.ParentID
	}
	entry, apiErr, err := s.opts.Transfers.CreateSDFolder(parent, *req.Name)
	if apiErr != nil || err != nil {
		s.transferError(w, r, apiErr, err)
		return
	}
	writeJSON(w, http.StatusCreated, entry)
}

func (s *Server) handleTrashSD(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Items []*library.SDTrashItem `json:"items"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Items) == 0 || len(req.Items) > library.MaxSDTrash {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "items must hold 1 to 100 entries", nil)
		return
	}
	items := make([]library.SDTrashItem, 0, len(req.Items))
	for _, it := range req.Items {
		// The version the client showed is required (Codex review of M23).
		if it == nil || it.ID == "" || it.ModifiedAt.IsZero() || it.Version == "" || !it.SizeGiven {
			auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "Each item needs sdEntryId, sizeBytes, modifiedAt and version", nil)
			return
		}
		items = append(items, *it)
	}
	res, apiErr, err := s.opts.Transfers.TrashSDEntries(items)
	if apiErr != nil || err != nil {
		s.transferError(w, r, apiErr, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		TrashEntryID string   `json:"trashEntryId"`
		Moved        []string `json:"moved"`
		MovedBytes   int64    `json:"movedBytes"`
	}{res.Entry.ID, nonNilStrings(res.Moved), res.MovedBytes})
}

func (s *Server) handleRenameGame(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name *string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == nil {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "Missing name", nil)
		return
	}
	res, apiErr, err := s.opts.Transfers.RenameGame(r.PathValue("gameId"), *req.Name)
	if apiErr != nil || err != nil {
		s.transferError(w, r, apiErr, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
