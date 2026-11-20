package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/transfer"
	"github.com/tfournier/ReminisLink/internal/writelock"
)

// Writes one at a time (M13, protocol/openapi.yaml "Writes one at a
// time"): every route that changes the SD card holds the write lock of
// the transfer service for its critical section, after its body was
// received and verified. A request waits at most the lock's wait (10 s),
// then gets 429 too_many_attempts "Device busy" with Retry-After.

// BusyRetryAfter is the Retry-After (seconds) of a "Device busy" answer.
const BusyRetryAfter = 2

// writeMode says how a signed route takes the write lock.
type writeMode int

const (
	// writeNone: a read, or a route that only touches the staging area.
	writeNone writeMode = iota
	// writeHandler: the whole handler runs under the lock (serialized).
	writeHandler
	// writeInner: the handler takes the lock itself, around its critical
	// section only (a cheat import fetches its source first).
	writeInner
	// writeInstall: the transfer service takes the lock around the
	// install step (an upload's final step, a job's install).
	writeInstall
	// writeStaging: a former M10 write route that only changes the
	// staging area of the pak (uploads and their chunks, cancelling a
	// job): never waits.
	writeStaging
)

// acquireWrite takes the write lock for the request, or answers 429
// Device busy (or 503 when the request is cancelled) and returns false.
// Once the lock is held, it checks again that the client was not revoked
// while it waited (401, the lock is released).
func (s *Server) acquireWrite(w http.ResponseWriter, r *http.Request) bool {
	switch err := s.writes().AcquireRequest(r.Context()); {
	case err == nil:
		if rej := s.writeCheck(r); rej != nil {
			s.writes().Release()
			auth.WriteError(w, rej.Status, rej.Code, rej.Message, rej.Details)
			return false
		}
		return true
	case errors.Is(err, writelock.ErrBusy):
		s.log.Info("write refused: device busy", "path", auth.LogString(r.URL.Path, auth.MaxLoggedPath))
		w.Header().Set("Retry-After", strconv.Itoa(BusyRetryAfter))
		auth.WriteError(w, http.StatusTooManyRequests, auth.CodeTooManyAttempts, "Device busy", nil)
	default:
		auth.WriteError(w, http.StatusServiceUnavailable, auth.CodeShuttingDown, "Request cancelled", nil)
	}
	return false
}

// serialized runs h under the write lock.
func (s *Server) serialized(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.acquireWrite(w, r) {
			return
		}
		defer s.writes().Release()
		h(w, r)
	}
}

func (s *Server) writes() *writelock.Lock { return s.opts.Transfers.Writes() }

// writeCheck checks that the client of an accepted request was not
// revoked since its acceptance (the access generation of M10).
func (s *Server) writeCheck(r *http.Request) *auth.Rejection {
	ad, ok := auth.AdmissionValue(r.Context()).(admission)
	if !ok {
		return &auth.Rejection{Status: http.StatusInternalServerError, Code: auth.CodeInternal, Message: "Internal error"}
	}
	return s.access.recheck(ad)
}

// withWriteCheck gives the transfer service the write check of the
// request, for the steps that take the write lock themselves.
func (s *Server) withWriteCheck(r *http.Request) *http.Request {
	return r.WithContext(transfer.WithWriteCheck(r.Context(), func() *transfer.Error {
		if rej := s.writeCheck(r); rej != nil {
			return &transfer.Error{Status: rej.Status, Code: rej.Code, Message: rej.Message, Details: rej.Details}
		}
		return nil
	}))
}

// ownJob records the client that started a job (cancelled if the console
// revokes it, M13), and cancels it at once if that already happened.
func (s *Server) ownJob(r *http.Request, id string) *auth.Rejection {
	c, _ := auth.ClientFromContext(r.Context())
	jobs := s.opts.Transfers.Jobs()
	jobs.SetOwner(id, c.ID)
	if rej := s.writeCheck(r); rej != nil {
		jobs.CancelOwner(c.ID)
		return rej
	}
	return nil
}
