package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/transfer"
)

// Headers of the transfer routes.
const (
	HeaderUploadOffset = "Upload-Offset"
	HeaderConfirm      = "X-RL-Confirm"
	// ConfirmEmptyTrash is the value of X-RL-Confirm required by the
	// permanent deletions.
	ConfirmEmptyTrash = "empty-trash"
	// TransfersRetryAfter is the Retry-After (seconds) of a 429 answered
	// when too many uploads or jobs are pending.
	TransfersRetryAfter = 5
)

// decodeJSON reads the (already verified, at most 1 MiB) JSON body into v.
// Unknown fields are ignored; data after the object is refused.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "Invalid JSON", nil)
		return false
	}
	if _, err := dec.Token(); err != io.EOF {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "Data after the JSON object", nil)
		return false
	}
	return true
}

// transferError answers an API error of internal/transfer, or an internal
// error.
func (s *Server) transferError(w http.ResponseWriter, r *http.Request, apiErr *transfer.Error, err error) {
	if apiErr != nil {
		if apiErr.Status == http.StatusTooManyRequests {
			retry := TransfersRetryAfter
			if apiErr.Message == transfer.ErrorDeviceBusy().Message {
				retry = BusyRetryAfter
			}
			w.Header().Set("Retry-After", strconv.Itoa(retry))
		}
		auth.WriteError(w, apiErr.Status, apiErr.Code, apiErr.Message, apiErr.Details)
		return
	}
	s.internal(w, r, err)
}

// --- Delete a game and the trash -------------------------------------------

// keepSaveParam parses the keepSave query parameter of DELETE
// /games/{gameId} and of its preview.
func keepSaveParam(w http.ResponseWriter, r *http.Request) (keepSave, ok bool) {
	if q := r.URL.Query(); q.Has("keepSave") {
		switch q.Get("keepSave") {
		case "true":
			return true, true
		case "false":
		default:
			auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "keepSave must be true or false", nil)
			return false, false
		}
	}
	return false, true
}

func (s *Server) handleDeletionPreview(w http.ResponseWriter, r *http.Request) {
	keepSave, ok := keepSaveParam(w, r)
	if !ok {
		return
	}
	p, err := s.opts.Library.PreviewDeletion(r.PathValue("gameId"), keepSave)
	if err != nil {
		s.libraryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleDeleteGame(w http.ResponseWriter, r *http.Request) {
	keepSave, ok := keepSaveParam(w, r)
	if !ok {
		return
	}
	res, err := s.opts.Transfers.DeleteGame(r.PathValue("gameId"), keepSave)
	if err != nil {
		s.libraryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleTrash(w http.ResponseWriter, r *http.Request) {
	entries, total, err := s.opts.Trash.List()
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Entries    []store.TrashEntry `json:"entries"`
		TotalBytes int64              `json:"totalBytes"`
	}{entries, total})
}

type freedBytes struct {
	FreedBytes int64 `json:"freedBytes"`
}

// confirmed checks X-RL-Confirm: empty-trash.
func confirmed(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get(HeaderConfirm) != ConfirmEmptyTrash {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "Missing X-RL-Confirm: empty-trash", nil)
		return false
	}
	return true
}

func (s *Server) handleEmptyTrash(w http.ResponseWriter, r *http.Request) {
	if !confirmed(w, r) {
		return
	}
	freed, err := s.opts.Trash.Empty()
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, freedBytes{freed})
}

func (s *Server) handleDeleteTrashEntry(w http.ResponseWriter, r *http.Request) {
	if !confirmed(w, r) {
		return
	}
	freed, err := s.opts.Trash.Delete(r.PathValue("entryId"))
	switch {
	case errors.Is(err, store.ErrInvalidTrashID):
		auth.WriteError(w, http.StatusBadRequest, CodeInvalidID, "Invalid identifier", nil)
	case errors.Is(err, store.ErrTrashNotFound):
		auth.WriteError(w, http.StatusNotFound, CodeNotFound, "Trash entry not found", nil)
	case err != nil:
		s.internal(w, r, err)
	default:
		writeJSON(w, http.StatusOK, freedBytes{freed})
	}
}

// --- Uploads ---------------------------------------------------------------

func (s *Server) handleCreateUpload(w http.ResponseWriter, r *http.Request) {
	var req transfer.CreateUploadRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	created, apiErr, err := s.opts.Transfers.CreateUpload(req)
	if apiErr != nil || err != nil {
		s.transferError(w, r, apiErr, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUploadStatus(w http.ResponseWriter, r *http.Request) {
	st, apiErr := s.opts.Transfers.Status(r.PathValue("uploadId"))
	if apiErr != nil {
		s.transferError(w, r, apiErr, nil)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// parseOffset reads a non-negative decimal without sign.
func parseOffset(v string) (int64, bool) {
	if v == "" || len(v) > 19 || (len(v) > 1 && v[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(v, 10, 64)
	return n, err == nil
}

// handleUploadChunk stores a chunk. The body (UNSIGNED, or signed and
// already read by the verifier) is streamed to the card: at most
// Content-Length bytes, refused beyond the chunk size or the bytes still
// expected. A chunked body without Content-Length is refused.
func (s *Server) handleUploadChunk(w http.ResponseWriter, r *http.Request) {
	offset, ok := parseOffset(r.Header.Get(HeaderUploadOffset))
	if !ok {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "Missing or invalid Upload-Offset", nil)
		return
	}
	if r.ContentLength < 1 {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "Missing Content-Length", nil)
		return
	}
	next, apiErr, err := s.opts.Transfers.WriteChunk(r.PathValue("uploadId"), offset, r.ContentLength, r.Body)
	if apiErr != nil || err != nil {
		if apiErr != nil && apiErr.Status == http.StatusRequestEntityTooLarge {
			w.Header().Set("Connection", "close") // the body stays unread
		}
		s.transferError(w, r, apiErr, err)
		return
	}
	w.Header().Set(HeaderUploadOffset, strconv.FormatInt(next, 10))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCancelUpload(w http.ResponseWriter, r *http.Request) {
	if apiErr := s.opts.Transfers.CancelUpload(r.PathValue("uploadId")); apiErr != nil {
		s.transferError(w, r, apiErr, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCompleteUpload(w http.ResponseWriter, r *http.Request) {
	res, apiErr, err := s.opts.Transfers.CompleteUpload(r.Context(), r.PathValue("uploadId"))
	if apiErr != nil || err != nil {
		s.transferError(w, r, apiErr, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// --- Downloads and jobs ----------------------------------------------------

func (s *Server) handleCreateDownload(w http.ResponseWriter, r *http.Request) {
	var req transfer.DownloadRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	id, apiErr, err := s.opts.Transfers.StartDownload(req)
	if apiErr != nil || err != nil {
		s.transferError(w, r, apiErr, err)
		return
	}
	writeJSON(w, http.StatusAccepted, struct {
		JobID string `json:"jobId"`
	}{id})
}

func (s *Server) handlePatchGame(w http.ResponseWriter, r *http.Request) {
	var req transfer.PatchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	id, apiErr, err := s.opts.Transfers.StartPatch(r.PathValue("gameId"), req)
	if apiErr != nil || err != nil {
		s.transferError(w, r, apiErr, err)
		return
	}
	writeJSON(w, http.StatusAccepted, struct {
		JobID string `json:"jobId"`
	}{id})
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		Jobs []transfer.Job `json:"jobs"`
	}{s.opts.Transfers.Jobs().List()})
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	j, apiErr := s.opts.Transfers.Jobs().Get(r.PathValue("jobId"))
	if apiErr != nil {
		s.transferError(w, r, apiErr, nil)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	j, apiErr := s.opts.Transfers.Jobs().Cancel(r.PathValue("jobId"))
	if apiErr != nil {
		s.transferError(w, r, apiErr, nil)
		return
	}
	writeJSON(w, http.StatusOK, j)
}
