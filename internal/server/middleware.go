package server

import (
	"io"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
)

// BodyIdleTimeout is the longest wait for the next bytes of a request
// body (docs/specs/07-securite.md §4: no global read timeout, an
// inactivity timeout instead).
const BodyIdleTimeout = 60 * time.Second

// middlewares wraps the router, outermost first: panic recovery, access
// log, shutdown gate, idle read deadline on the body.
func (s *Server) middlewares(next http.Handler) http.Handler {
	h := s.idleBody(next)
	h = s.shutdownGate(h)
	h = s.accessLog(h)
	return s.recoverPanics(h)
}

// statusRecorder remembers the status of a response. Unwrap keeps
// http.ResponseController working through it.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w}
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				panic(v)
			}
			s.log.Error("panic in handler", "method", auth.LogString(r.Method, auth.MaxLoggedMethod), "path", auth.LogString(r.URL.Path, auth.MaxLoggedPath), "panic", v, "stack", string(debug.Stack()))
			if rec.status == 0 {
				auth.WriteError(rec, http.StatusInternalServerError, auth.CodeInternal, "Internal error", nil)
			}
		}()
		next.ServeHTTP(rec, r)
	})
}

// accessLog logs one line per request: method, route, path, status,
// duration, client id. Never headers, bodies, keys, nonces or signatures.
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec, ok := w.(*statusRecorder)
		if !ok {
			rec = &statusRecorder{ResponseWriter: w}
		}
		next.ServeHTTP(rec, r)
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		log := s.log
		if status >= 400 || r.Pattern == helloPattern || r.Pattern == pairPattern {
			// Anyone can cause these lines (no signature): sampled.
			log = s.rejectLog
		}
		log.Info("request",
			"method", auth.LogString(r.Method, auth.MaxLoggedMethod),
			"route", r.Pattern,
			"path", auth.LogString(r.URL.Path, auth.MaxLoggedPath),
			"status", status,
			"bytes", rec.bytes,
			"ms", time.Since(start).Milliseconds(),
			"client", auth.LogClient(r.Header.Get(auth.HeaderClient)),
			"remote", remoteIP(r),
		)
	})
}

// shutdownGate answers 503 shutting_down once the pak is closing.
func (s *Server) shutdownGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.ShuttingDown() {
			w.Header().Set("Connection", "close")
			auth.WriteError(w, http.StatusServiceUnavailable, auth.CodeShuttingDown, "Shutting down", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// idleBody sets a read deadline before each read of the request body, so a
// client that stops sending its body is cut off after BodyIdleTimeout
// while a slow but steady upload is not. The server clears the deadline
// itself once the body is read.
func (s *Server) idleBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = &idleReader{ReadCloser: r.Body, ctl: http.NewResponseController(w), timeout: s.timeouts.bodyIdle}
		}
		next.ServeHTTP(w, r)
	})
}

type idleReader struct {
	io.ReadCloser
	ctl     *http.ResponseController
	timeout time.Duration
}

func (b *idleReader) Read(p []byte) (int, error) {
	_ = b.ctl.SetReadDeadline(time.Now().Add(b.timeout))
	return b.ReadCloser.Read(p)
}

// limit applies a per-IP rate limit (429 too_many_attempts with
// Retry-After).
func (s *Server) limit(l *rateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ok, wait := l.allow(limitKey(remoteIP(r))); !ok {
			secs := int64((wait + time.Second - 1) / time.Second)
			w.Header().Set("Retry-After", strconv.FormatInt(max(secs, 1), 10))
			s.rejectLog.Info("rate limited", "path", auth.LogString(r.URL.Path, auth.MaxLoggedPath), "remote", remoteIP(r))
			auth.WriteError(w, http.StatusTooManyRequests, auth.CodeTooManyAttempts, "Too many requests", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
