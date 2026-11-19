package server

import (
	"bytes"
	"errors"
	"image/png"
	"io"
	"net/http"
	"syscall"

	"github.com/tfournier/ReminisLink/internal/auth"
)

// MaxArtSide is the largest width and height of an art sent with PUT
// /games/{gameId}/art (docs/specs/04-api-v1.md, M8).
const MaxArtSide = 1024

// pngSignature starts every PNG file.
var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// checkArt reports whether data is a valid PNG of at most MaxArtSide
// pixels on each side: the signature and the header first (no decoding of
// a huge image), then the whole image (a truncated or corrupt file is
// refused).
func checkArt(data []byte) bool {
	if !bytes.HasPrefix(data, pngSignature) {
		return false
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > MaxArtSide || cfg.Height > MaxArtSide {
		return false
	}
	_, err = png.Decode(bytes.NewReader(data))
	return err == nil
}

func (s *Server) handlePutArt(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.game(w, r); !ok {
		return
	}
	// The body is already read and checked by the verifier (at most
	// auth.BodyLimitArt).
	data, err := io.ReadAll(r.Body)
	if err != nil {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "Body unreadable", nil)
		return
	}
	if !checkArt(data) {
		auth.WriteError(w, http.StatusUnprocessableEntity, CodeUnsupportedFormat, "Not a PNG image of at most 1024x1024 pixels", nil)
		return
	}
	if !s.enoughSpace(w, int64(len(data))) {
		return
	}
	g, err := s.opts.Transfers.SetArt(r.PathValue("gameId"), data)
	if errors.Is(err, syscall.ENOSPC) {
		var free int64
		if st, err := s.opts.Storage(s.opts.Adapter.Roots().SD); err == nil {
			free = st.FreeBytes
		}
		auth.WriteError(w, http.StatusInsufficientStorage, CodeInsufficientStorage, "Not enough free space",
			map[string]any{"required": int64(len(data)) + SpaceMargin, "available": free})
		return
	}
	if err != nil {
		s.libraryError(w, r, err)
		return
	}
	s.log.Info("art set", "game", g.ID, "bytes", len(data))
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) handleDeleteArt(w http.ResponseWriter, r *http.Request) {
	g, err := s.opts.Transfers.DeleteArt(r.PathValue("gameId"))
	if err != nil {
		s.libraryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}
