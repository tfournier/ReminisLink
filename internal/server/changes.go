package server

import (
	"net/http"

	"github.com/tfournier/ReminisLink/internal/transfer"
)

// Change is a change of the SD card files made by a request, shown as a
// short banner on the pak screen (docs/specs/05-console-pak.md §7.1).
type Change struct {
	Kind string // one of the Change* constants
	Name string // game or file name, "" when unknown
}

// Kinds of Change.
const (
	ChangeROMAdded      = "rom-added"
	ChangeSaveReplaced  = "save-replaced"
	ChangeSaveRestored  = "save-restored"
	ChangeStateReplaced = "state-replaced"
	ChangeStateRestored = "state-restored"
	ChangeStateDeleted  = "state-deleted"
	ChangeGameDeleted   = "game-deleted"
	ChangeCheatsUpdated = "cheats-updated"
	ChangeTrashEmptied  = "trash-emptied"
	// ChangeTrashEntryDeleted: one trash entry erased for good.
	ChangeTrashEntryDeleted = "trash-entry-deleted"
)

// notify calls OnChange with kind after a successful (2xx) response. name,
// evaluated before the handler (a deleted game has no name afterwards),
// gives the subject of the change.
func (s *Server) notify(kind string, name func(*http.Request) string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.opts.OnChange == nil {
			h(w, r)
			return
		}
		subject := ""
		if name != nil {
			subject = name(r)
		}
		rec := &statusRecorder{ResponseWriter: w}
		h(rec, r)
		if rec.status >= 200 && rec.status < 300 {
			s.opts.OnChange(Change{Kind: kind, Name: subject})
		}
	}
}

// notifyUpload is notify for the completion of an upload: the kind and
// the name depend on its target (a staging file changes nothing visible).
func (s *Server) notifyUpload(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.opts.OnChange == nil {
			h(w, r)
			return
		}
		target, ok := s.opts.Transfers.UploadTarget(r.PathValue("uploadId"))
		kind, subject := "", ""
		if ok {
			switch target.Kind {
			case transfer.KindROM:
				kind = ChangeROMAdded
				if target.FileName != nil {
					subject = *target.FileName
				}
			case transfer.KindSave:
				kind, subject = ChangeSaveReplaced, s.gameNameOf(target.GameID)
			case transfer.KindState:
				kind, subject = ChangeStateReplaced, s.gameNameOf(target.GameID)
			}
		}
		rec := &statusRecorder{ResponseWriter: w}
		h(rec, r)
		if kind != "" && rec.status >= 200 && rec.status < 300 {
			s.opts.OnChange(Change{Kind: kind, Name: subject})
		}
	}
}

// gameName is the display name of the {gameId} of the request.
func (s *Server) gameName(r *http.Request) string { return s.gameNameOf(r.PathValue("gameId")) }

// gameNameOf is the display name of a game, "" when the id names none.
func (s *Server) gameNameOf(gameID string) string {
	if gameID == "" {
		return ""
	}
	g, err := s.opts.Library.GameByID(gameID)
	if err != nil {
		return ""
	}
	return g.Name
}
