package server

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/cheats"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/saves"
	"github.com/tfournier/ReminisLink/internal/states"
	"github.com/tfournier/ReminisLink/internal/transfer"
)

// Error codes of the save, state and cheat routes.
const (
	CodeConflict            = "conflict"
	CodeStateLocked         = "state_locked"
	CodeUnsupportedFormat   = "unsupported_format"
	CodeInsufficientStorage = "insufficient_storage"
)

// FetchRetryAfter is the Retry-After (seconds) answered when a cheat import
// from a URL could not start because another one was running.
const FetchRetryAfter = 5

// SpaceMargin is the free space kept on the SD card by the copies of the
// save and state routes, on top of the bytes they write.
const SpaceMargin = 1 << 20

// game resolves the gameId of the request (answers the error itself).
func (s *Server) game(w http.ResponseWriter, r *http.Request) (osadapter.Game, bool) {
	g, err := s.opts.Library.GameByID(r.PathValue("gameId"))
	if err != nil {
		s.libraryError(w, r, err)
		return osadapter.Game{}, false
	}
	return g, true
}

// slot parses the {slot} of the request: a decimal without sign or
// leading zero, within the slots of the pak.
func slotParam(w http.ResponseWriter, r *http.Request) (int, bool) {
	v := r.PathValue("slot")
	n, ok := parseOffset(v)
	if !ok || n > int64(states.MaxSlot) {
		auth.WriteError(w, http.StatusBadRequest, CodeInvalidID, "Invalid slot", nil)
		return 0, false
	}
	return int(n), true
}

// contentDisposition is `attachment; filename="<name>"`, with the RFC 6266
// filename* form as well when the name is not plain ASCII.
func contentDisposition(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	v := `attachment; filename="` + ascii + `"`
	if ascii != name {
		if ext := mime.FormatMediaType("attachment", map[string]string{"filename": name}); ext != "" {
			if _, star, ok := strings.Cut(ext, "; "); ok {
				v += "; " + star
			}
		}
	}
	return v
}

// sendFile streams an opened file as an attachment and closes it.
func (s *Server) sendFile(w http.ResponseWriter, r *http.Request, f *os.File, name, contentType string) {
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.Header().Set("Content-Type", contentType)
	if contentType == "application/octet-stream" {
		w.Header().Set("Content-Disposition", contentDisposition(name))
	}
	w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	w.WriteHeader(http.StatusOK)
	// No WriteTimeout on the server: a slow transfer of a large file
	// (a disc of hundreds of MB) goes on as long as bytes flow, and a
	// client that stops reading is cut off after SendIdleTimeout.
	iw := &idleWriter{w: w, ctl: http.NewResponseController(w), timeout: s.timeouts.sendIdle}
	_, err = io.CopyN(iw, f, fi.Size())
	if err == nil {
		// net/http buffers the end of the body: it is sent under a
		// deadline too, before the deadline is cleared.
		_ = iw.ctl.SetWriteDeadline(time.Now().Add(iw.timeout))
		if ferr := iw.ctl.Flush(); ferr != nil && !errors.Is(ferr, http.ErrNotSupported) {
			err = ferr
		}
	}
	_ = iw.ctl.SetWriteDeadline(time.Time{}) // the connection may be reused
	if err != nil {
		s.log.Warn("file transfer interrupted", "error", err)
	}
}

// SendChunk is the most bytes written under one write deadline: a client
// that accepts less than SendChunk bytes in SendIdleTimeout is cut off.
const SendChunk = 32 << 10

// idleWriter writes a response body in pieces of at most SendChunk bytes,
// each with a new write deadline, so a client that stops reading is cut
// off after timeout while a slow but steady download is not.
type idleWriter struct {
	w       http.ResponseWriter
	ctl     *http.ResponseController
	timeout time.Duration
}

func (iw *idleWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		chunk := p[:min(len(p), SendChunk)]
		_ = iw.ctl.SetWriteDeadline(time.Now().Add(iw.timeout))
		n, err := iw.w.Write(chunk)
		written += n
		if err != nil {
			return written, err
		}
		p = p[n:]
	}
	return written, nil
}

// enoughSpace answers 507 insufficient_storage when the SD card has less
// than need bytes free (plus SpaceMargin).
func (s *Server) enoughSpace(w http.ResponseWriter, need int64) bool {
	st, err := s.opts.Storage(s.opts.Adapter.Roots().SD)
	if err != nil {
		return true // unknown: let the write fail if it must
	}
	if st.FreeBytes < need+SpaceMargin {
		auth.WriteError(w, http.StatusInsufficientStorage, CodeInsufficientStorage, "Not enough free space",
			map[string]any{"required": need + SpaceMargin, "available": st.FreeBytes})
		return false
	}
	return true
}

func fileSize(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.Size()
	}
	return 0
}

// --- Saves -----------------------------------------------------------------

// saveError answers an error of internal/saves.
func (s *Server) saveError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, saves.ErrNoSave):
		auth.WriteError(w, http.StatusNotFound, CodeNotFound, "Save not found", nil)
	case errors.Is(err, saves.ErrNoClock):
		auth.WriteError(w, http.StatusNotFound, CodeNotFound, "Clock file not found", nil)
	case errors.Is(err, saves.ErrBackupNotFound):
		auth.WriteError(w, http.StatusNotFound, CodeNotFound, "Backup not found", nil)
	case errors.Is(err, saves.ErrInvalidBackupID):
		auth.WriteError(w, http.StatusBadRequest, CodeInvalidID, "Invalid identifier", nil)
	case errors.Is(err, osadapter.ErrUnsupportedFormat):
		auth.WriteError(w, http.StatusUnprocessableEntity, CodeUnsupportedFormat, err.Error(), nil)
	case errors.Is(err, saves.ErrCorruptBackup):
		auth.WriteError(w, http.StatusConflict, CodeConflict, "The backup does not match its checksum (damaged copy), nothing was changed", nil)
	case errors.Is(err, saves.ErrInvalidLabel):
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "label must be at most 80 characters, without control characters", nil)
	default:
		s.internal(w, r, err)
	}
}

type backupResult struct {
	Backup *saves.Backup `json:"backup"`
}

func (s *Server) handleGetSave(w http.ResponseWriter, r *http.Request) {
	g, ok := s.game(w, r)
	if !ok {
		return
	}
	f, name, err := s.opts.Saves.OpenSave(g)
	if err != nil {
		s.saveError(w, r, err)
		return
	}
	s.sendFile(w, r, f, name, "application/octet-stream")
}

func (s *Server) handleGetSaveClock(w http.ResponseWriter, r *http.Request) {
	g, ok := s.game(w, r)
	if !ok {
		return
	}
	f, name, err := s.opts.Saves.OpenClock(g)
	if err != nil {
		s.saveError(w, r, err)
		return
	}
	s.sendFile(w, r, f, name, "application/octet-stream")
}

// handleListSaves answers GET /saves. A request whose client went away
// stops without an answer.
func (s *Server) handleListSaves(w http.ResponseWriter, r *http.Request) {
	l, err := s.opts.Saves.ListAll(r.Context())
	if err != nil {
		if r.Context().Err() != nil {
			if s.ShuttingDown() {
				auth.WriteError(w, http.StatusServiceUnavailable, auth.CodeShuttingDown, "Shutting down", nil)
				return
			}
			s.log.Info("save listing cancelled")
			return
		}
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) handlePutSave(w http.ResponseWriter, r *http.Request) {
	g, ok := s.game(w, r)
	if !ok {
		return
	}
	// The body is already read and checked by the verifier (at most
	// auth.BodyLimitSave).
	data, err := io.ReadAll(r.Body)
	if err != nil {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "Body unreadable", nil)
		return
	}
	if len(data) == 0 {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "Empty save", nil)
		return
	}
	current := int64(0)
	if save, err := s.opts.Saves.SaveFile(g); err == nil {
		current = save.Size
	}
	if !s.enoughSpace(w, int64(len(data))+current) {
		return
	}
	tmp, err := s.opts.Saves.WriteTemp(data)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	// The install lock is shared with the uploads, the deletions...
	b, err := s.opts.Saves.Replace(g, tmp)
	if err != nil {
		s.saveError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, backupResult{b})
}

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	g, ok := s.game(w, r)
	if !ok {
		return
	}
	list, err := s.opts.Saves.List(g)
	if err != nil {
		s.saveError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Backups []saves.Backup `json:"backups"`
	}{list})
}

func (s *Server) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	g, ok := s.game(w, r)
	if !ok {
		return
	}
	var req struct {
		Label *string `json:"label"`
	}
	// The body is optional.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "Body unreadable", nil)
		return
	}
	if len(bytes.TrimSpace(body)) > 0 {
		r.Body = io.NopCloser(bytes.NewReader(body))
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	if save, err := s.opts.Saves.SaveFile(g); err == nil && !s.enoughSpace(w, save.Size) {
		return
	}
	b, err := s.opts.Saves.Create(g, req.Label)
	if err != nil {
		s.saveError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

func (s *Server) handleGetBackup(w http.ResponseWriter, r *http.Request) {
	g, ok := s.game(w, r)
	if !ok {
		return
	}
	f, name, err := s.opts.Saves.OpenBackup(g, r.PathValue("backupId"))
	if err != nil {
		s.saveError(w, r, err)
		return
	}
	s.sendFile(w, r, f, name, "application/octet-stream")
}

func (s *Server) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	g, ok := s.game(w, r)
	if !ok {
		return
	}
	if err := s.opts.Saves.Delete(g, r.PathValue("backupId")); err != nil {
		s.saveError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	g, ok := s.game(w, r)
	if !ok {
		return
	}
	need := int64(0)
	if save, err := s.opts.Saves.SaveFile(g); err == nil {
		need += save.Size
	}
	if f, _, err := s.opts.Saves.OpenBackup(g, r.PathValue("backupId")); err == nil {
		if fi, err := f.Stat(); err == nil {
			need += fi.Size()
		}
		f.Close()
	}
	if !s.enoughSpace(w, need) {
		return
	}
	b, err := s.opts.Saves.Restore(g, r.PathValue("backupId"))
	if err != nil {
		s.saveError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, backupResult{b})
}

// --- States ----------------------------------------------------------------

// stateError answers an error of internal/states.
func (s *Server) stateError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, states.ErrInvalidSlot):
		auth.WriteError(w, http.StatusBadRequest, CodeInvalidID, "Invalid slot", nil)
	case errors.Is(err, states.ErrInvalidEntryID):
		auth.WriteError(w, http.StatusBadRequest, CodeInvalidID, "Invalid identifier", nil)
	case errors.Is(err, states.ErrNoState):
		auth.WriteError(w, http.StatusNotFound, CodeNotFound, "State not found", nil)
	case errors.Is(err, states.ErrNotLocked):
		auth.WriteError(w, http.StatusNotFound, CodeNotFound, "State slot is not locked", nil)
	case errors.Is(err, states.ErrNoThumbnail):
		auth.WriteError(w, http.StatusNotFound, CodeNotFound, "Thumbnail not found", nil)
	case errors.Is(err, states.ErrEntryNotFound):
		auth.WriteError(w, http.StatusNotFound, CodeNotFound, "Resume history entry not found", nil)
	case errors.Is(err, states.ErrNoResumeSlot):
		auth.WriteError(w, http.StatusUnprocessableEntity, CodeUnsupportedFormat, "No resume slot on this console (or state format not supported)", nil)
	case errors.Is(err, states.ErrCorruptCopy):
		auth.WriteError(w, http.StatusConflict, CodeConflict, "The copy does not match its checksum (damaged copy), nothing was changed", nil)
	case errors.Is(err, states.ErrCoreChanged):
		auth.WriteError(w, http.StatusConflict, CodeConflict, "The copy comes from another emulator core, nothing was changed", nil)
	case errors.Is(err, states.ErrLocked):
		auth.WriteError(w, http.StatusConflict, CodeStateLocked, "State slot is locked", nil)
	case errors.Is(err, osadapter.ErrUnsupportedFormat):
		auth.WriteError(w, http.StatusUnprocessableEntity, CodeUnsupportedFormat, err.Error(), nil)
	default:
		s.internal(w, r, err)
	}
}

func (s *Server) handleListStates(w http.ResponseWriter, r *http.Request) {
	g, ok := s.game(w, r)
	if !ok {
		return
	}
	list, err := s.opts.States.List(g)
	if err != nil {
		s.stateError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetState(w http.ResponseWriter, r *http.Request) {
	g, ok := s.game(w, r)
	if !ok {
		return
	}
	slot, ok := slotParam(w, r)
	if !ok {
		return
	}
	f, name, err := s.opts.States.Open(g, slot)
	if err != nil {
		s.stateError(w, r, err)
		return
	}
	s.sendFile(w, r, f, name, "application/octet-stream")
}

func (s *Server) handleDeleteState(w http.ResponseWriter, r *http.Request) {
	g, ok := s.game(w, r)
	if !ok {
		return
	}
	slot, ok := slotParam(w, r)
	if !ok {
		return
	}
	res, err := s.opts.States.Delete(g, slot)
	if err != nil {
		s.stateError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, library.DeleteResult{TrashEntryID: res.Entry.ID, Moved: res.Moved, MovedBytes: res.MovedBytes})
}

func (s *Server) handleStateThumbnail(w http.ResponseWriter, r *http.Request) {
	g, ok := s.game(w, r)
	if !ok {
		return
	}
	slot, ok := slotParam(w, r)
	if !ok {
		return
	}
	p, err := s.opts.States.Thumbnail(g, slot)
	if err != nil {
		s.stateError(w, r, err)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		s.stateError(w, r, states.ErrNoThumbnail)
		return
	}
	s.sendFile(w, r, f, "", "image/png")
}

func (s *Server) handleLockState(w http.ResponseWriter, r *http.Request) {
	g, ok := s.game(w, r)
	if !ok {
		return
	}
	slot, ok := slotParam(w, r)
	if !ok {
		return
	}
	if !s.enoughSpace(w, fileSize(s.opts.Adapter.StatePath(g, slot))) {
		return
	}
	sl, err := s.opts.States.Lock(g, slot)
	if err != nil {
		s.stateError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sl)
}

func (s *Server) handleUnlockState(w http.ResponseWriter, r *http.Request) {
	g, ok := s.game(w, r)
	if !ok {
		return
	}
	slot, ok := slotParam(w, r)
	if !ok {
		return
	}
	sl, err := s.opts.States.Unlock(g, slot)
	if err != nil {
		s.stateError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sl)
}

func (s *Server) handleRestoreState(w http.ResponseWriter, r *http.Request) {
	g, ok := s.game(w, r)
	if !ok {
		return
	}
	slot, ok := slotParam(w, r)
	if !ok {
		return
	}
	if !s.enoughSpace(w, fileSize(s.opts.Adapter.StatePath(g, slot))) {
		return
	}
	sl, err := s.opts.States.RestoreLock(g, slot)
	if err != nil {
		s.stateError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sl)
}

type resumeHistory struct {
	Entries []states.ResumeEntry `json:"entries"`
}

func (s *Server) handleResumeHistory(w http.ResponseWriter, r *http.Request) {
	g, ok := s.game(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, resumeHistory{s.opts.States.ResumeHistory(g)})
}

func (s *Server) handleRestoreResume(w http.ResponseWriter, r *http.Request) {
	g, ok := s.game(w, r)
	if !ok {
		return
	}
	if slot, ok := s.opts.Adapter.ResumeSlot(); ok {
		// The current state is archived, the entry copied in place.
		if !s.enoughSpace(w, 2*fileSize(s.opts.Adapter.StatePath(g, slot))) {
			return
		}
	}
	entries, err := s.opts.States.RestoreResume(g, r.PathValue("entryId"))
	if err != nil {
		s.stateError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resumeHistory{entries})
}

// --- Cheats ----------------------------------------------------------------

// cheatError answers an error of internal/cheats.
func (s *Server) cheatError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, cheats.ErrConflict):
		auth.WriteError(w, http.StatusPreconditionFailed, CodeConflict, "Cheat file changed", nil)
	case errors.Is(err, cheats.ErrInvalidCheat), errors.Is(err, cheats.ErrInvalidMode):
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, err.Error(), nil)
	case errors.Is(err, cheats.ErrTooLarge):
		auth.WriteError(w, http.StatusRequestEntityTooLarge, auth.CodeTooLarge, "Cheat file too large", nil)
	case errors.Is(err, cheats.ErrUnreadable):
		auth.WriteError(w, http.StatusUnprocessableEntity, CodeUnsupportedFormat, "Cheat file unreadable", nil)
	case errors.Is(err, cheats.ErrNoCheat):
		auth.WriteError(w, http.StatusUnprocessableEntity, CodeUnsupportedFormat, "No cheat found", nil)
	default:
		s.internal(w, r, err)
	}
}

func (s *Server) writeCheats(w http.ResponseWriter, list cheats.List) {
	w.Header().Set("ETag", list.ETag)
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetCheats(w http.ResponseWriter, r *http.Request) {
	g, ok := s.game(w, r)
	if !ok {
		return
	}
	list, err := s.opts.Cheats.Get(g)
	if err != nil {
		s.cheatError(w, r, err)
		return
	}
	s.writeCheats(w, list)
}

func (s *Server) handlePutCheats(w http.ResponseWriter, r *http.Request) {
	g, ok := s.game(w, r)
	if !ok {
		return
	}
	ifMatch := r.Header.Get("If-Match")
	if ifMatch == "" {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "Missing If-Match", nil)
		return
	}
	var req struct {
		Cheats *[]cheats.APICheat `json:"cheats"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Cheats == nil {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "Missing cheats", nil)
		return
	}
	list, err := s.opts.Cheats.Put(g, ifMatch, *req.Cheats)
	if err != nil {
		s.cheatError(w, r, err)
		return
	}
	s.writeCheats(w, list)
}

type cheatImportRequest struct {
	Source *struct {
		Type      string  `json:"type"`
		URL       *string `json:"url"`
		SDFileID  *string `json:"sdFileId"`
		Text      *string `json:"text"`
		UploadID  *string `json:"uploadId"`
		StagingID *string `json:"stagingId"`
	} `json:"source"`
	Mode *string `json:"mode"`
}

// readLimited reads a file of at most limit bytes (cheats.ErrTooLarge
// beyond).
func readLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, cheats.ErrTooLarge
	}
	return data, nil
}

func (s *Server) handleImportCheats(w http.ResponseWriter, r *http.Request) {
	g, ok := s.game(w, r)
	if !ok {
		return
	}
	var req cheatImportRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Source == nil || req.Mode == nil {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "Missing field", nil)
		return
	}
	if *req.Mode != cheats.ModeMerge && *req.Mode != cheats.ModeReplace {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "mode must be merge or replace", nil)
		return
	}
	src := req.Source
	missing := func() {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "Missing source field", nil)
	}
	stagingNotFound := func() {
		auth.WriteError(w, http.StatusNotFound, CodeNotFound, "Staging file not found", nil)
	}
	var data []byte
	var err error
	switch src.Type {
	case "url":
		if src.URL == nil {
			missing()
			return
		}
		select {
		case s.fetchSlot <- struct{}{}:
		case <-r.Context().Done():
			if s.ShuttingDown() {
				auth.WriteError(w, http.StatusServiceUnavailable, auth.CodeShuttingDown, "Shutting down", nil)
				return
			}
			// The client gave up while another import was running.
			w.Header().Set("Retry-After", strconv.Itoa(FetchRetryAfter))
			auth.WriteError(w, http.StatusTooManyRequests, auth.CodeTooManyAttempts, "Another cheat import is running", nil)
			return
		}
		var apiErr *transfer.Error
		data, apiErr = s.opts.Transfers.FetchSmall(r.Context(), *src.URL, cheats.MaxImportBytes)
		<-s.fetchSlot
		if apiErr != nil {
			s.transferError(w, r, apiErr, nil)
			return
		}
	case "sd":
		if src.SDFileID == nil {
			missing()
			return
		}
		p, err := s.opts.Library.ResolveSDFile(*src.SDFileID, library.SDKindCheat)
		if err != nil {
			if errors.Is(err, library.ErrNotFound) {
				auth.WriteError(w, http.StatusNotFound, CodeNotFound, "File not found", nil)
				return
			}
			s.libraryError(w, r, err)
			return
		}
		if data, err = readLimited(p, cheats.MaxImportBytes); err != nil {
			s.cheatError(w, r, err)
			return
		}
	case "inline":
		if src.Text == nil {
			missing()
			return
		}
		if len(*src.Text) > cheats.MaxInlineBytes {
			auth.WriteError(w, http.StatusRequestEntityTooLarge, auth.CodeTooLarge, "Text too large", nil)
			return
		}
		data = []byte(*src.Text)
	case "upload", "staging":
		var p string
		switch {
		case src.Type == "upload" && src.UploadID != nil:
			p, err = s.opts.Transfers.StagingForUpload(*src.UploadID)
		case src.Type == "staging" && src.StagingID != nil:
			p, err = s.opts.Transfers.StagingFile(*src.StagingID)
		default:
			missing()
			return
		}
		if err != nil {
			stagingNotFound()
			return
		}
		if data, err = readLimited(p, cheats.MaxImportBytes); err != nil {
			s.cheatError(w, r, err)
			return
		}
	default:
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "Unknown source.type", nil)
		return
	}
	// The source is read: the import itself waits for its turn (M13).
	if !s.acquireWrite(w, r) {
		return
	}
	res, err := s.opts.Cheats.Import(g, data, *req.Mode)
	s.writes().Release()
	if err != nil {
		s.cheatError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// --- SD card files ---------------------------------------------------------

func (s *Server) handleSDFiles(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("type")
	files, err := s.opts.Library.SDFiles(kind)
	if errors.Is(err, library.ErrBadKind) {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "type must be cht or patch", nil)
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Files []library.SDFile `json:"files"`
	}{files})
}
