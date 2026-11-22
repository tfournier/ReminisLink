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
		SDEntryIDs []string `json:"sdEntryIds"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.SDEntryIDs) == 0 || len(req.SDEntryIDs) > library.MaxSDTrash {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "sdEntryIds must hold 1 to 100 identifiers", nil)
		return
	}
	res, apiErr, err := s.opts.Transfers.TrashSDEntries(req.SDEntryIDs)
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
