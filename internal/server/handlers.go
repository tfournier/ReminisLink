package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/library"
)

const (
	// MaxClientNameRunes is the longest clientName (docs/specs/04-api-v1.md).
	MaxClientNameRunes = 64
	// MaxArtBytes is the largest art file served (it is read in memory to
	// compute its ETag).
	MaxArtBytes = 8 << 20
	// ArtCacheControl is the Cache-Control of GET /games/{gameId}/art.
	ArtCacheControl = "private, max-age=86400"
	// unknownIPv4 is reported when the console has no IPv4 address (the
	// contract requires one).
	unknownIPv4 = "0.0.0.0"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		auth.WriteError(w, http.StatusInternalServerError, auth.CodeInternal, "Internal error", nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func timestamp(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

// --- Device ----------------------------------------------------------------

type helloResponse struct {
	DeviceID         string    `json:"deviceId"`
	DeviceName       string    `json:"deviceName"`
	Model            string    `json:"model"`
	Platform         string    `json:"platform"`
	OS               string    `json:"os"`
	OSVersion        *string   `json:"osVersion"`
	PakVersion       string    `json:"pakVersion"`
	APIVersion       int       `json:"apiVersion"`
	MinClientVersion string    `json:"minClientVersion"`
	ServerTime       time.Time `json:"serverTime"`
	PairingOpen      bool      `json:"pairingOpen"`
}

func (s *Server) handleHello(w http.ResponseWriter, r *http.Request) {
	info, err := s.opts.Adapter.DeviceInfo()
	if err != nil {
		s.internal(w, r, err)
		return
	}
	snap, err := s.opts.Pairing.Snapshot()
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, helloResponse{
		DeviceID:         s.opts.Identity.DeviceID,
		DeviceName:       s.opts.Identity.DeviceName,
		Model:            info.Model,
		Platform:         info.Platform,
		OS:               info.OS,
		OSVersion:        info.OSVersion,
		PakVersion:       s.opts.Identity.PakVersion,
		APIVersion:       APIVersion,
		MinClientVersion: MinClientVersion,
		ServerTime:       timestamp(s.now()),
		PairingOpen:      snap.BlockedUntil.IsZero(),
	})
}

type pairRequest struct {
	Method      *string `json:"method"`
	ClientID    *string `json:"clientId"`
	ClientName  *string `json:"clientName"`
	ClientNonce *string `json:"clientNonce"`
	Proof       *string `json:"proof"`
}

type pairResponse struct {
	DeviceID    string `json:"deviceId"`
	DeviceName  string `json:"deviceName"`
	DeviceNonce string `json:"deviceNonce"`
	Proof       string `json:"proof"`
}

// validClientName: 1 to 64 characters, valid UTF-8, no control character.
func validClientName(name string) bool {
	n := utf8.RuneCountInString(name)
	if !utf8.ValidString(name) || n < 1 || n > MaxClientNameRunes || strings.TrimSpace(name) == "" {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxJSONBody)
	var req pairRequest
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			auth.WriteError(w, http.StatusRequestEntityTooLarge, auth.CodeTooLarge, "Body too large", nil)
			return
		}
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "Invalid JSON", nil)
		return
	}
	if _, err := dec.Token(); err != io.EOF {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "Data after the JSON object", nil)
		return
	}
	if req.Method == nil || req.ClientID == nil || req.ClientName == nil || req.ClientNonce == nil || req.Proof == nil {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "Missing field", nil)
		return
	}
	if !validClientName(*req.ClientName) {
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, "clientName must be 1 to 64 characters", nil)
		return
	}
	res, retryAfter, err := s.opts.Pairing.Attempt(auth.PairRequest{
		Method:      auth.PairingMethod(*req.Method),
		ClientID:    *req.ClientID,
		ClientNonce: *req.ClientNonce,
		Proof:       *req.Proof,
	})
	switch {
	case errors.Is(err, auth.ErrInvalidPairRequest):
		auth.WriteError(w, http.StatusBadRequest, auth.CodeInvalidRequest, err.Error(), nil)
		return
	case errors.Is(err, auth.ErrPairingFailed):
		s.log.Info("pairing failed", "method", *req.Method, "remote", remoteIP(r))
		auth.WriteError(w, http.StatusUnauthorized, CodePairingFailed, "Pairing failed", nil)
		return
	case errors.Is(err, auth.ErrTooManyAttempts):
		secs := int64((retryAfter + time.Second - 1) / time.Second)
		w.Header().Set("Retry-After", strconv.FormatInt(max(secs, 1), 10))
		auth.WriteError(w, http.StatusTooManyRequests, auth.CodeTooManyAttempts, "Pairing is blocked", nil)
		return
	case err != nil:
		s.internal(w, r, err)
		return
	}
	client := auth.Client{ID: res.ClientID, Name: *req.ClientName, Key: res.Key, PairedAt: s.now()}
	if err := s.opts.Clients.Put(client); err != nil {
		s.internal(w, r, err)
		return
	}
	// Never log the key, the nonces or the proofs.
	s.log.Info("client paired", "client", client.ID, "name", client.Name, "method", *req.Method)
	if s.opts.OnPaired != nil {
		s.opts.OnPaired(client)
	}
	writeJSON(w, http.StatusOK, pairResponse{
		DeviceID:    s.opts.Identity.DeviceID,
		DeviceName:  s.opts.Identity.DeviceName,
		DeviceNonce: res.DeviceNonce,
		Proof:       res.Proof,
	})
}

func (s *Server) handleUnpair(w http.ResponseWriter, r *http.Request) {
	client, ok := auth.ClientFromContext(r.Context())
	if !ok {
		s.internal(w, r, errors.New("no client in context"))
		return
	}
	if _, err := s.opts.Clients.Remove(client.ID); err != nil {
		s.internal(w, r, err)
		return
	}
	s.log.Info("client unpaired", "client", client.ID)
	w.WriteHeader(http.StatusNoContent)
}

type deviceResponse struct {
	DeviceID      string          `json:"deviceId"`
	DeviceName    string          `json:"deviceName"`
	Model         string          `json:"model"`
	Platform      string          `json:"platform"`
	OS            string          `json:"os"`
	OSVersion     *string         `json:"osVersion"`
	PakVersion    string          `json:"pakVersion"`
	Network       networkInfo     `json:"network"`
	Battery       *batteryInfo    `json:"battery"`
	Storage       []storageVolume `json:"storage"`
	UptimeSeconds int64           `json:"uptimeSeconds"`
}

type networkInfo struct {
	IPv4 string  `json:"ipv4"`
	SSID *string `json:"ssid"`
}

type batteryInfo struct {
	Percent  int  `json:"percent"`
	Charging bool `json:"charging"`
}

type storageVolume struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	TotalBytes int64  `json:"totalBytes"`
	FreeBytes  int64  `json:"freeBytes"`
}

func (s *Server) handleDevice(w http.ResponseWriter, r *http.Request) {
	info, err := s.opts.Adapter.DeviceInfo()
	if err != nil {
		s.internal(w, r, err)
		return
	}
	resp := deviceResponse{
		DeviceID:   s.opts.Identity.DeviceID,
		DeviceName: s.opts.Identity.DeviceName,
		Model:      info.Model,
		Platform:   info.Platform,
		OS:         info.OS,
		OSVersion:  info.OSVersion,
		PakVersion: s.opts.Identity.PakVersion,
		// TODO(M0): the SSID source on NextUI is not known yet: null.
		Network:       networkInfo{IPv4: unknownIPv4},
		Storage:       []storageVolume{},
		UptimeSeconds: max(int64(s.now().Sub(s.started)/time.Second), 0),
	}
	if ip, ok := s.opts.IPv4(); ok {
		resp.Network.IPv4 = ip
	}
	if info.Battery != nil {
		resp.Battery = &batteryInfo{Percent: info.Battery.Percent, Charging: info.Battery.Charging}
	}
	if st, err := s.opts.Storage(s.opts.Adapter.Roots().SD); err == nil {
		resp.Storage = append(resp.Storage, storageVolume{ID: "sd", Label: "SD", TotalBytes: st.TotalBytes, FreeBytes: st.FreeBytes})
	} else {
		s.log.Warn("cannot read the SD card space", "error", err)
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- Library ---------------------------------------------------------------

// libraryError answers an error of internal/library.
func (s *Server) libraryError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, library.ErrInvalidID):
		auth.WriteError(w, http.StatusBadRequest, CodeInvalidID, "Invalid identifier", nil)
	case errors.Is(err, library.ErrNotFound):
		auth.WriteError(w, http.StatusNotFound, CodeNotFound, notFoundMessage(err), nil)
	default:
		s.internal(w, r, err)
	}
}

func notFoundMessage(err error) string {
	msg := err.Error()
	switch {
	case strings.HasSuffix(msg, ": system"):
		return "System not found"
	case strings.HasSuffix(msg, ": folder"):
		return "Folder not found"
	case strings.HasSuffix(msg, ": art"):
		return "Art not found"
	}
	return "Game not found"
}

func (s *Server) internal(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("internal error", "method", r.Method, "path", r.URL.Path, "error", err)
	auth.WriteError(w, http.StatusInternalServerError, auth.CodeInternal, "Internal error", nil)
}

func (s *Server) handleSystems(w http.ResponseWriter, r *http.Request) {
	systems, err := s.opts.Library.Systems()
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Systems []library.System `json:"systems"`
	}{systems})
}

func (s *Server) handleSystemGames(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	folderID := q.Get("folderId")
	if q.Has("folderId") && folderID == "" {
		auth.WriteError(w, http.StatusBadRequest, CodeInvalidID, "Invalid identifier", nil)
		return
	}
	listing, err := s.opts.Library.ListGames(r.PathValue("systemId"), folderID)
	if err != nil {
		s.libraryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, listing)
}

func (s *Server) handleGame(w http.ResponseWriter, r *http.Request) {
	d, err := s.opts.Library.Game(r.PathValue("gameId"))
	if err != nil {
		s.libraryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleGameArt(w http.ResponseWriter, r *http.Request) {
	f, err := s.opts.Library.Art(r.PathValue("gameId"))
	if err != nil {
		s.libraryError(w, r, err)
		return
	}
	if f.Size > MaxArtBytes {
		s.internal(w, r, errors.New("art file too large"))
		return
	}
	data, err := readLimited(f.Path, MaxArtBytes)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	sum := sha256.Sum256(data)
	etag := `"sha256-` + hex.EncodeToString(sum[:16]) + `"`
	w.Header().Set("ETag", etag)
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Cache-Control", ArtCacheControl)
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

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
		return nil, errors.New("file too large")
	}
	return data, nil
}

// etagMatches implements the weak comparison of If-None-Match (RFC 9110
// §13.1.2): "*" or one of the listed tags, with or without W/.
func etagMatches(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "*" || strings.TrimPrefix(part, "W/") == etag {
			return true
		}
	}
	return false
}
