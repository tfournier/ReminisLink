package transfer

import (
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tfournier/ReminisLink/internal/store"
)

// Upload limits (docs/specs/03-protocole.md §7).
const (
	DefaultChunkSize = 4 << 20
	MaxChunkSize     = 16 << 20
	// SpaceMarginPercent is the free space required on top of the size.
	SpaceMarginPercent = 5
)

// Files of an upload in uploads/ (same file system as the destinations):
//
//	<id>.json  the request (target, size, sha256, onConflict), written once
//	<id>.part  the bytes received so far
//	<id>.hash  the SHA-256 state after the last chunk, to resume the hash
//	           after a restart without reading the file again
//
// Uploads survive a restart of the pak: the offset is the size of the
// .part file. The .hash file is written without fsync (cheap); after a
// power loss it may be behind or ahead of the .part file, and the hash is
// then computed again from the file. Bytes lost or damaged by a power loss
// are caught by the final SHA-256 check (409 checksum_mismatch).
const (
	metaExt = ".json"
	partExt = ".part"
	hashExt = ".hash"
)

// uploadIDRe matches the upload identifiers made by the pak.
var uploadIDRe = regexp.MustCompile(`^up_[0-9a-f]{24}$`)

// CreateUploadRequest is the body of POST /uploads.
type CreateUploadRequest struct {
	Target     *Target `json:"target"`
	SizeBytes  *int64  `json:"sizeBytes"`
	SHA256     *string `json:"sha256"`
	OnConflict *string `json:"onConflict"`
}

// UploadCreated is the answer of POST /uploads.
type UploadCreated struct {
	UploadID  string    `json:"uploadId"`
	ChunkSize int64     `json:"chunkSize"`
	Offset    int64     `json:"offset"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// UploadStatus is the answer of GET /uploads/{uploadId}.
type UploadStatus struct {
	UploadID  string `json:"uploadId"`
	Offset    int64  `json:"offset"`
	SizeBytes int64  `json:"sizeBytes"`
}

type uploadMeta struct {
	Version    int       `json:"version"`
	ID         string    `json:"id"`
	Target     Target    `json:"target"`
	SizeBytes  int64     `json:"sizeBytes"`
	SHA256     string    `json:"sha256"`
	OnConflict string    `json:"onConflict"`
	CreatedAt  time.Time `json:"createdAt"`
}

type hashState struct {
	Offset int64  `json:"offset"`
	State  []byte `json:"state"`
}

type upload struct {
	meta uploadMeta

	mu       sync.Mutex // held while a chunk is written or the upload completes
	offset   int64
	hash     hash.Hash // nil until computed again after a restart
	activity time.Time
	gone     bool // completed, cancelled or expired
}

type uploads struct {
	mu sync.Mutex
	m  map[string]*upload
}

func (s *Service) uploadPath(id, ext string) string {
	return filepath.Join(s.opts.Layout.UploadsDir(), id+ext)
}

func validSHA256(v string) bool {
	if len(v) != 64 {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// required returns size + SpaceMarginPercent %.
func required(size int64) int64 {
	return size + (size*SpaceMarginPercent+99)/100
}

// reservedLocked returns the bytes still expected by the other uploads.
// Called with s.uploads.mu held.
func (s *Service) reservedLocked() int64 {
	var n int64
	for _, u := range s.uploads.m {
		n += max(u.meta.SizeBytes-u.offsetHint(), 0)
	}
	return n
}

// offsetHint reads the offset without waiting for a chunk in progress.
func (u *upload) offsetHint() int64 {
	if u.mu.TryLock() {
		defer u.mu.Unlock()
		return u.offset
	}
	return 0
}

// CreateUpload checks the request, the destination and the free space,
// then creates an empty upload.
func (s *Service) CreateUpload(req CreateUploadRequest) (UploadCreated, *Error, error) {
	if req.Target == nil || req.SizeBytes == nil || req.SHA256 == nil {
		return UploadCreated{}, badRequest("Missing field"), nil
	}
	if *req.SizeBytes < 0 || *req.SizeBytes > MaxUploadBytes {
		return UploadCreated{}, badRequest("Invalid sizeBytes"), nil
	}
	if !validSHA256(*req.SHA256) {
		return UploadCreated{}, badRequest("Invalid sha256"), nil
	}
	onConflict := ConflictFail
	if req.OnConflict != nil {
		onConflict = *req.OnConflict
	}
	if !validConflict(onConflict) {
		return UploadCreated{}, badRequest("Invalid onConflict"), nil
	}
	d, apiErr, err := s.resolve(*req.Target, true)
	if apiErr != nil || err != nil {
		return UploadCreated{}, apiErr, err
	}
	if apiErr := s.checkConflict(d, onConflict); apiErr != nil {
		return UploadCreated{}, apiErr, nil
	}
	target := *req.Target
	if d.name != "" {
		target.FileName = &d.name // keep the cleaned name
	}
	dir := s.opts.Layout.UploadsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return UploadCreated{}, nil, err
	}

	s.uploads.mu.Lock()
	defer s.uploads.mu.Unlock()
	free, err := s.opts.FreeSpace(dir)
	if err != nil {
		return UploadCreated{}, nil, fmt.Errorf("free space: %w", err)
	}
	need := required(*req.SizeBytes)
	if available := free - s.reservedLocked(); available < need {
		return UploadCreated{}, insufficientStorage(need, available), nil
	}
	now := s.now()
	u := &upload{
		meta: uploadMeta{
			Version: 1, ID: "up_" + s.randomHex(12), Target: target, SizeBytes: *req.SizeBytes,
			SHA256: *req.SHA256, OnConflict: onConflict, CreatedAt: now.UTC().Truncate(time.Second),
		},
		hash:     sha256.New(),
		activity: now,
	}
	part, err := os.OpenFile(s.uploadPath(u.meta.ID, partExt), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return UploadCreated{}, nil, err
	}
	part.Close()
	data, _ := json.Marshal(u.meta)
	if err := store.WriteFileAtomic(s.uploadPath(u.meta.ID, metaExt), data, 0o644); err != nil {
		os.Remove(s.uploadPath(u.meta.ID, partExt))
		return UploadCreated{}, nil, err
	}
	s.uploads.m[u.meta.ID] = u
	s.log.Info("upload created", "upload", u.meta.ID, "kind", target.Kind, "bytes", u.meta.SizeBytes)
	return UploadCreated{
		UploadID: u.meta.ID, ChunkSize: s.opts.ChunkSize, Offset: 0,
		ExpiresAt: now.Add(UploadIdleExpiry).UTC().Truncate(time.Second),
	}, nil, nil
}

// MaxUploadBytes bounds the announced size (exFAT files are smaller).
const MaxUploadBytes = 1 << 50

// lookup returns a live upload: 400 invalid_id for a malformed id, 404
// for an unknown, finished or expired one. An expired upload is deleted.
func (s *Service) lookup(id string) (*upload, *Error) {
	if !uploadIDRe.MatchString(id) {
		return nil, newError(http.StatusBadRequest, CodeInvalidID, "Invalid identifier")
	}
	s.uploads.mu.Lock()
	u, ok := s.uploads.m[id]
	s.uploads.mu.Unlock()
	if !ok {
		return nil, notFound("Upload not found")
	}
	return u, nil
}

// acquire locks an upload for a change; it fails with 404 if the upload
// ended or expired meanwhile.
func (s *Service) acquire(u *upload) *Error {
	u.mu.Lock()
	if u.gone {
		u.mu.Unlock()
		return notFound("Upload not found")
	}
	if s.now().Sub(u.activity) > UploadIdleExpiry {
		s.dropLocked(u, "expired")
		u.mu.Unlock()
		return notFound("Upload not found")
	}
	return nil
}

// dropLocked deletes the files of an upload. Called with u.mu held.
func (s *Service) dropLocked(u *upload, why string) {
	u.gone = true
	s.uploads.mu.Lock()
	delete(s.uploads.m, u.meta.ID)
	s.uploads.mu.Unlock()
	s.removeUploadFiles(u.meta.ID)
	s.log.Info("upload deleted", "upload", u.meta.ID, "why", why)
}

func (s *Service) removeUploadFiles(id string) {
	for _, ext := range []string{partExt, hashExt, hashExt + ".tmp", metaExt} {
		_ = os.Remove(s.uploadPath(id, ext))
	}
}

// Status returns the offset of an upload.
func (s *Service) Status(id string) (UploadStatus, *Error) {
	u, apiErr := s.lookup(id)
	if apiErr != nil {
		return UploadStatus{}, apiErr
	}
	if apiErr := s.acquire(u); apiErr != nil {
		return UploadStatus{}, apiErr
	}
	defer u.mu.Unlock()
	return UploadStatus{UploadID: id, Offset: u.offset, SizeBytes: u.meta.SizeBytes}, nil
}

// offsetConflict is 409 conflict with the expected offset.
func offsetConflict(message string, offset int64) *Error {
	return &Error{Status: http.StatusConflict, Code: CodeConflict, Message: message, Details: map[string]any{"offset": offset}}
}

// ensureHash computes the SHA-256 of the received bytes again after a
// restart. Called with u.mu held.
func (s *Service) ensureHash(u *upload) error {
	if u.hash != nil {
		return nil
	}
	h := sha256.New()
	f, err := os.Open(s.uploadPath(u.meta.ID, partExt))
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(h, io.LimitReader(f, u.offset))
	if err != nil {
		return err
	}
	if n != u.offset {
		return fmt.Errorf("upload %s: short file", u.meta.ID)
	}
	u.hash = h
	return nil
}

// WriteChunk appends a chunk of length bytes read from body at offset. It
// reads the body as a stream, at most length bytes, and never more than
// the chunk size or the bytes still expected (413 too_large). A body
// shorter than length leaves the upload unchanged (400 invalid_request).
func (s *Service) WriteChunk(id string, offset, length int64, body io.Reader) (int64, *Error, error) {
	u, apiErr := s.lookup(id)
	if apiErr != nil {
		return 0, apiErr, nil
	}
	if !u.mu.TryLock() {
		// Another chunk of the same upload is being written.
		return 0, newError(http.StatusConflict, CodeConflict, "Another chunk is in progress"), nil
	}
	u.mu.Unlock()
	if apiErr := s.acquire(u); apiErr != nil {
		return 0, apiErr, nil
	}
	defer u.mu.Unlock()
	if offset != u.offset {
		return 0, offsetConflict("Unexpected upload offset", u.offset), nil
	}
	if length < 1 {
		return 0, badRequest("Missing or empty chunk"), nil
	}
	if length > s.opts.ChunkSize || length > u.meta.SizeBytes-u.offset {
		return 0, newError(http.StatusRequestEntityTooLarge, CodeTooLarge, "Chunk too large"), nil
	}
	if err := s.ensureHash(u); err != nil {
		return 0, nil, err
	}
	saved, err := u.hash.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		return 0, nil, err
	}
	partPath := s.uploadPath(id, partExt)
	f, err := os.OpenFile(partPath, os.O_WRONLY, 0)
	if err != nil {
		return 0, nil, err
	}
	rollback := func() {
		_ = f.Truncate(u.offset)
		_ = u.hash.(encoding.BinaryUnmarshaler).UnmarshalBinary(saved)
	}
	if _, err := f.Seek(u.offset, io.SeekStart); err != nil {
		f.Close()
		return 0, nil, err
	}
	n, err := io.CopyN(io.MultiWriter(f, u.hash), body, length)
	if err != nil {
		rollback()
		f.Close()
		if errors.Is(err, syscall.ENOSPC) {
			free, _ := s.opts.FreeSpace(s.opts.Layout.UploadsDir())
			return 0, insufficientStorage(required(u.meta.SizeBytes-u.offset), free), nil
		}
		var pe *fs.PathError
		if errors.As(err, &pe) {
			return 0, nil, err // write error
		}
		// The body ended early or the client went away.
		return 0, badRequest("Chunk body truncated"), nil
	}
	if err := f.Close(); err != nil {
		rollback()
		return 0, nil, err
	}
	u.offset += n
	u.activity = s.now()
	s.saveHashState(u)
	return u.offset, nil, nil
}

// saveHashState writes the hash state of an upload (no fsync, see above).
func (s *Service) saveHashState(u *upload) {
	state, err := u.hash.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		return
	}
	data, _ := json.Marshal(hashState{Offset: u.offset, State: state})
	p := s.uploadPath(u.meta.ID, hashExt)
	if err := os.WriteFile(p+".tmp", data, 0o644); err == nil {
		_ = os.Rename(p+".tmp", p)
	}
}

// CompleteUpload checks the SHA-256 and installs the file. On a mismatch
// the upload is deleted (409 checksum_mismatch). When the destination
// refuses the file (conflict, locked state), the upload stays and can be
// completed again.
func (s *Service) CompleteUpload(id string) (map[string]any, *Error, error) {
	u, apiErr := s.lookup(id)
	if apiErr != nil {
		return nil, apiErr, nil
	}
	if apiErr := s.acquire(u); apiErr != nil {
		return nil, apiErr, nil
	}
	defer u.mu.Unlock()
	if u.offset != u.meta.SizeBytes {
		return nil, offsetConflict("Upload is not complete", u.offset), nil
	}
	if err := s.ensureHash(u); err != nil {
		return nil, nil, err
	}
	if got := hex.EncodeToString(u.hash.Sum(nil)); got != u.meta.SHA256 {
		s.dropLocked(u, "checksum mismatch")
		return nil, newError(http.StatusConflict, CodeChecksumMismatch, "SHA-256 mismatch"), nil
	}
	partPath := s.uploadPath(id, partExt)
	if err := syncFile(partPath); err != nil {
		return nil, nil, err
	}
	d, apiErr, err := s.resolve(u.meta.Target, true)
	if apiErr != nil || err != nil {
		return nil, apiErr, err
	}
	result, apiErr, err := s.install(d, partPath, u.meta.OnConflict)
	if apiErr != nil || err != nil {
		return nil, apiErr, err
	}
	s.finishLocked(u)
	return result, nil, nil
}

// finishLocked forgets a completed upload (its .part file was renamed).
// Called with u.mu held.
func (s *Service) finishLocked(u *upload) {
	u.gone = true
	s.uploads.mu.Lock()
	delete(s.uploads.m, u.meta.ID)
	s.uploads.mu.Unlock()
	s.removeUploadFiles(u.meta.ID)
	s.log.Info("upload completed", "upload", u.meta.ID)
}

// CancelUpload deletes an upload and its temporary file.
func (s *Service) CancelUpload(id string) *Error {
	u, apiErr := s.lookup(id)
	if apiErr != nil {
		return apiErr
	}
	if apiErr := s.acquire(u); apiErr != nil {
		return apiErr
	}
	defer u.mu.Unlock()
	s.dropLocked(u, "cancelled")
	return nil
}

func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// LoadUploads resumes the uploads of a previous run: the offset is the
// size of the .part file; the hash state is reused when it matches.
// Broken uploads (missing or oversized .part, unreadable request) are
// deleted. Call it after Maintain.
func (s *Service) LoadUploads() error {
	dir := s.opts.Layout.UploadsDir()
	des, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("uploads: %w", err)
	}
	s.uploads.mu.Lock()
	defer s.uploads.mu.Unlock()
	for _, d := range des {
		id, ok := strings.CutSuffix(d.Name(), metaExt)
		if !ok || !uploadIDRe.MatchString(id) {
			continue
		}
		u, err := s.loadUpload(id)
		if err != nil {
			s.log.Warn("uploads: dropping a broken upload", "upload", id, "error", err)
			s.removeUploadFiles(id)
			continue
		}
		s.uploads.m[id] = u
		s.log.Info("upload resumed", "upload", id, "offset", u.offset, "bytes", u.meta.SizeBytes)
	}
	return nil
}

func (s *Service) loadUpload(id string) (*upload, error) {
	data, err := os.ReadFile(s.uploadPath(id, metaExt))
	if err != nil {
		return nil, err
	}
	var m uploadMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m.Version != 1 || m.ID != id || !validSHA256(m.SHA256) || m.SizeBytes < 0 || !validConflict(m.OnConflict) {
		return nil, errors.New("invalid upload file")
	}
	fi, err := os.Stat(s.uploadPath(id, partExt))
	if err != nil {
		return nil, err
	}
	if fi.Size() > m.SizeBytes {
		return nil, errors.New("partial file larger than announced")
	}
	u := &upload{meta: m, offset: fi.Size(), activity: fi.ModTime()}
	if m.CreatedAt.After(u.activity) {
		u.activity = m.CreatedAt
	}
	// Reuse the hash state if the file holds at least its bytes: the
	// file is cut back to that offset (bytes past it were written but
	// their hash state was not saved).
	if raw, err := os.ReadFile(s.uploadPath(id, hashExt)); err == nil {
		var hs hashState
		if json.Unmarshal(raw, &hs) == nil && hs.Offset >= 0 && hs.Offset <= u.offset {
			h := sha256.New()
			if h.(encoding.BinaryUnmarshaler).UnmarshalBinary(hs.State) == nil {
				if hs.Offset < u.offset {
					if err := os.Truncate(s.uploadPath(id, partExt), hs.Offset); err != nil {
						return nil, err
					}
					u.offset = hs.Offset
				}
				u.hash = h
			}
		}
	}
	if u.offset == 0 {
		u.hash = sha256.New()
	}
	return u, nil
}

// purgeUploads deletes the uploads idle for more than UploadIdleExpiry and
// the leftovers of a previous run (partial downloads, orphan files).
func (s *Service) purgeUploads() error {
	dir := s.opts.Layout.UploadsDir()
	des, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("uploads: %w", err)
	}
	limit := s.now().Add(-UploadIdleExpiry)
	metas := map[string]bool{}
	for _, d := range des {
		if id, ok := strings.CutSuffix(d.Name(), metaExt); ok && uploadIDRe.MatchString(id) {
			metas[id] = true
		}
	}
	for _, d := range des {
		name := d.Name()
		id := strings.TrimSuffix(strings.TrimSuffix(name, ".tmp"), filepath.Ext(strings.TrimSuffix(name, ".tmp")))
		if uploadIDRe.MatchString(id) && metas[id] {
			if name != id+metaExt {
				continue // judged with its .json below
			}
			activity := time.Time{}
			for _, ext := range []string{metaExt, partExt} {
				if fi, err := os.Stat(s.uploadPath(id, ext)); err == nil && fi.ModTime().After(activity) {
					activity = fi.ModTime()
				}
			}
			if activity.Before(limit) {
				s.removeUploadFiles(id)
				s.log.Info("uploads: deleted an expired upload", "upload", id)
			}
			continue
		}
		// Not part of a known upload: a partial download of a previous run
		// (jobs do not survive a restart) or an orphan file.
		if err := os.RemoveAll(filepath.Join(dir, name)); err == nil {
			s.log.Info("uploads: deleted a leftover temporary file", "file", name)
		}
	}
	return nil
}
