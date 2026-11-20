package transfer

import (
	"context"
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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tfournier/ReminisLink/internal/paks"
	"github.com/tfournier/ReminisLink/internal/store"
)

// Upload limits (docs/specs/03-protocole.md §7).
const (
	DefaultChunkSize = 4 << 20
	MaxChunkSize     = 16 << 20
	// SpaceMarginPercent is the free space required on top of the size.
	SpaceMarginPercent = 5
	// MaxOpenUploads bounds the uploads in progress (429 beyond).
	MaxOpenUploads = 32
)

// Files of an upload in uploads/ (same file system as the destinations):
//
//	<id>.json  the request (target, size, sha256, onConflict), written once
//	<id>.part  the bytes received so far
//
// Uploads survive a restart of the pak: the offset is the size of the
// .part file. The .part file is not synced after each chunk, so after a
// power loss its size may cover bytes that never reached the card (zeros
// or garbage). The incremental SHA-256 is therefore kept in memory only:
// after a restart it is computed again from the bytes really present in
// the .part file (streamed, on the first chunk or completion), so the
// final check always covers what will be installed, and damaged bytes end
// in 409 checksum_mismatch, never in an installed corrupt file.
const (
	metaExt = ".json"
	partExt = ".part"
	// hashExt was a saved hash state (first M4 draft); only deleted now.
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

type upload struct {
	meta uploadMeta

	mu       sync.Mutex // held while a chunk is written or the upload completes
	offset   int64
	hash     hash.Hash // nil until computed again after a restart
	activity time.Time
	gone     bool // completed, cancelled or expired

	// For the pak screen (activity.go), read without the lock: bytes
	// received so far (chunk in progress included), last activity
	// (UnixNano, 0: none in this run) and a chunk being written.
	live     atomic.Int64
	lastSeen atomic.Int64
	writing  atomic.Bool

	// verified is closed when the hash of a resumed upload was computed
	// again in the background (nil: nothing to compute); verifyErr is
	// then set when it failed.
	verified  chan struct{}
	verifyErr error
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
	if apiErr := checkPakTarget(*req.Target, onConflict); apiErr != nil {
		return UploadCreated{}, apiErr, nil
	}
	if req.Target.Kind == KindPak && *req.SizeBytes > paks.MaxArchiveBytes {
		return UploadCreated{}, newError(http.StatusRequestEntityTooLarge, CodeTooLarge, "Pak archive too large"), nil
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
	if len(s.uploads.m) >= MaxOpenUploads {
		return UploadCreated{}, tooMany("Too many open uploads"), nil
	}
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
	u.touch(now)
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

// MaxParallelVerify bounds the resumed uploads hashed at the same time.
const MaxParallelVerify = 2

// DefaultVerifyWait is how long a chunk or a completion waits for the hash
// of a resumed upload before answering 429 (the app retries).
const DefaultVerifyWait = 10 * time.Second

// verifyAttempts and verifyRetryDelay: a failed background hash is tried
// again a few times (a transient read error of the SD card).
const verifyAttempts = 3

const verifyRetryDelay = 200 * time.Millisecond

// verify computes again, in the background, the SHA-256 of the bytes of a
// resumed upload (streamed from its .part file). It stops when the
// service closes. When it fails, the next chunk or completion tries again
// under the upload lock (ensureHash).
func (s *Service) verify(u *upload) {
	defer s.verifyWG.Done()
	defer close(u.verified)
	select {
	case s.verifySem <- struct{}{}:
		defer func() { <-s.verifySem }()
	case <-s.verifyCtx.Done():
		u.verifyErr = s.verifyCtx.Err()
		return
	}
	for attempt := 1; ; attempt++ {
		h, err := s.hashPart(s.verifyCtx, u.meta.ID, u.offset)
		if err == nil {
			u.hash = h
			s.log.Info("upload verified", "upload", u.meta.ID, "offset", u.offset)
			return
		}
		u.verifyErr = err
		if s.verifyCtx.Err() != nil {
			return
		}
		if attempt >= verifyAttempts {
			s.log.Warn("uploads: cannot verify a resumed upload", "upload", u.meta.ID, "error", err)
			return
		}
		select {
		case <-time.After(verifyRetryDelay * time.Duration(attempt)):
		case <-s.verifyCtx.Done():
			u.verifyErr = s.verifyCtx.Err()
			return
		}
	}
}

// hashPart hashes the first offset bytes of the .part file of an upload.
func (s *Service) hashPart(ctx context.Context, id string, offset int64) (hash.Hash, error) {
	f, err := os.Open(s.uploadPath(id, partExt))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 1<<20)
	var done int64
	for done < offset {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := f.Read(buf[:min(int64(len(buf)), offset-done)])
		h.Write(buf[:n])
		done += int64(n)
		if err == io.EOF && done < offset {
			return nil, errShortPart
		}
		if err != nil && err != io.EOF {
			return nil, err
		}
	}
	return h, nil
}

// errShortPart: the .part file holds fewer bytes than its offset.
var errShortPart = errors.New("partial file shorter than its offset")

// shuttingDown is the answer of a request whose wait was cancelled by the
// shutdown of the pak.
func shuttingDown() *Error {
	return newError(http.StatusServiceUnavailable, CodeShuttingDown, "Shutting down")
}

// waitVerified waits (at most the verify wait) for the background hash of
// a resumed upload: 429 too_many_attempts while it is not done, 503
// shutting_down when the pak is closing. A failed background hash is not
// an error here: ensureHash tries again.
func (s *Service) waitVerified(u *upload) *Error {
	if u.verified == nil {
		return nil
	}
	timer := time.NewTimer(s.verifyWait())
	defer timer.Stop()
	select {
	case <-u.verified:
		if errors.Is(u.verifyErr, context.Canceled) {
			return shuttingDown()
		}
		return nil
	case <-s.verifyCtx.Done():
		return shuttingDown()
	case <-timer.C:
		return tooMany("Upload is being verified")
	}
}

func (s *Service) verifyWait() time.Duration {
	if s.opts.VerifyWait > 0 {
		return s.opts.VerifyWait
	}
	return DefaultVerifyWait
}

// ensureHash computes the SHA-256 of the received bytes again when the
// background verification did not (or failed). A .part file that is gone
// or shorter than its offset is lost for good: the upload is deleted and
// 404 tells the app to create it again. Another read error is reported
// (500) and tried again at the next request. Called with u.mu held.
func (s *Service) ensureHash(u *upload) *Error {
	if u.hash != nil {
		return nil
	}
	h, err := s.hashPart(context.Background(), u.meta.ID, u.offset)
	if err == nil {
		u.hash = h
		return nil
	}
	if errors.Is(err, errShortPart) || errors.Is(err, fs.ErrNotExist) {
		s.log.Warn("uploads: partial file lost, upload deleted", "upload", u.meta.ID, "error", err)
		s.dropLocked(u, "partial file unreadable")
		return notFound("Upload data is unreadable; create the upload again")
	}
	s.log.Warn("uploads: cannot read the partial file", "upload", u.meta.ID, "error", err)
	return internal("Cannot read the upload data; try again")
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
	if apiErr := s.waitVerified(u); apiErr != nil {
		return 0, apiErr, nil
	}
	if apiErr := s.acquire(u); apiErr != nil {
		return 0, apiErr, nil
	}
	defer u.mu.Unlock()
	u.live.Store(u.offset)
	u.writing.Store(true)
	defer func() {
		u.writing.Store(false)
		u.live.Store(u.offset)
		u.touch(s.now())
	}()
	if offset != u.offset {
		return 0, offsetConflict("Unexpected upload offset", u.offset), nil
	}
	if length < 1 {
		return 0, badRequest("Missing or empty chunk"), nil
	}
	if length > s.opts.ChunkSize || length > u.meta.SizeBytes-u.offset {
		return 0, newError(http.StatusRequestEntityTooLarge, CodeTooLarge, "Chunk too large"), nil
	}
	if apiErr := s.ensureHash(u); apiErr != nil {
		return 0, apiErr, nil
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
	n, err := io.CopyN(io.MultiWriter(f, u.hash), &progressReader{r: body, live: &u.live}, length)
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
	return u.offset, nil, nil
}

// CompleteUpload checks the SHA-256 and installs the file. On a mismatch
// the upload is deleted (409 checksum_mismatch). When the destination
// refuses the file (conflict, locked state), the upload stays and can be
// completed again.
//
// A client that loses the answer of a successful complete (connection
// cut) sees 404 on GET /uploads/{id} afterwards: the outcome is uncertain
// and the app checks the destination (e.g. the game list) instead of
// reporting a failure.
//
// The final step (resolve and install) holds the write lock (M13): it
// waits for the write in progress, at most its wait, else 429 Device busy
// and the upload stays.
func (s *Service) CompleteUpload(ctx context.Context, id string) (map[string]any, *Error, error) {
	u, apiErr := s.lookup(id)
	if apiErr != nil {
		return nil, apiErr, nil
	}
	if apiErr := s.waitVerified(u); apiErr != nil {
		return nil, apiErr, nil
	}
	if apiErr := s.acquire(u); apiErr != nil {
		return nil, apiErr, nil
	}
	partPath, apiErr, err := s.checkCompleteLocked(u)
	u.mu.Unlock()
	if apiErr != nil || err != nil {
		return nil, apiErr, err
	}
	// Wait for the write lock WITHOUT holding the upload: a cancel (or a
	// status request) arriving meanwhile does not wait for the write in
	// progress (M13). The upload is checked again once the lock is held.
	if apiErr := s.acquireWrites(ctx); apiErr != nil {
		return nil, apiErr, nil
	}
	defer s.opts.Writes.Release()
	if apiErr := s.acquire(u); apiErr != nil {
		return nil, apiErr, nil // cancelled or expired while waiting
	}
	defer u.mu.Unlock()
	if partPath, apiErr, err = s.checkCompleteLocked(u); apiErr != nil || err != nil {
		return nil, apiErr, err
	}
	// Resolve and install under the install lock too: a DELETE /games of
	// the target game cannot slip in between (no orphan save recreated).
	s.installMu.Lock()
	d, apiErr, err := s.resolve(u.meta.Target, true)
	var result map[string]any
	switch {
	case apiErr != nil || err != nil:
	case d.kind == KindPak:
		// M12: the archive goes to the staging area and a job installs it.
		result, apiErr, err = s.queuePakUpload(ctx, u.meta.Target, partPath, u.meta.OnConflict)
	default:
		result, apiErr, err = s.installLocked(d, partPath, u.meta.OnConflict)
	}
	s.installMu.Unlock()
	if apiErr != nil || err != nil {
		return nil, apiErr, err
	}
	if id, ok := result["stagingId"].(string); ok {
		s.rememberStaging(u.meta.ID, id)
	}
	s.finishLocked(u)
	return result, nil, nil
}

// checkCompleteLocked checks that an upload is whole and has the
// announced SHA-256 (a mismatch deletes it), and syncs its file. Called
// with u.mu held; it returns the path of the file.
func (s *Service) checkCompleteLocked(u *upload) (string, *Error, error) {
	if u.offset != u.meta.SizeBytes {
		return "", offsetConflict("Upload is not complete", u.offset), nil
	}
	if apiErr := s.ensureHash(u); apiErr != nil {
		return "", apiErr, nil
	}
	if got := hex.EncodeToString(u.hash.Sum(nil)); got != u.meta.SHA256 {
		s.dropLocked(u, "checksum mismatch")
		return "", newError(http.StatusConflict, CodeChecksumMismatch, "SHA-256 mismatch"), nil
	}
	partPath := s.uploadPath(u.meta.ID, partExt)
	if err := syncFile(partPath); err != nil {
		return "", nil, err
	}
	return partPath, nil, nil
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
		if u.hash == nil {
			u.verified = make(chan struct{})
			s.verifyWG.Add(1)
			go s.verify(u)
		}
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
	// Never trust a saved hash state: ensureHash reads the real bytes.
	_ = os.Remove(s.uploadPath(id, hashExt))
	if u.offset == 0 {
		u.hash = sha256.New()
	}
	return u, nil
}

// purgeUploads deletes the uploads idle for more than UploadIdleExpiry and
// the leftovers of a previous run (partial downloads, orphan files).
// keep are files of uploads/ never deleted (referenced by a patch
// installation marker).
func (s *Service) purgeUploads(keep map[string]bool) error {
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
		if keep[name] {
			continue
		}
		id := strings.TrimSuffix(strings.TrimSuffix(name, ".tmp"), filepath.Ext(strings.TrimSuffix(name, ".tmp")))
		if uploadIDRe.MatchString(id) && metas[id] {
			if name != id+metaExt || keep[id+partExt] {
				continue // judged with its .json below, or kept for a replacement
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
