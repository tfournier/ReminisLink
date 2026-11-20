package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"

	"github.com/tfournier/ReminisLink/internal/events"
)

// HTTP server settings (docs/specs/03-protocole.md §1, 05-console-pak.md §2,
// 07-securite.md §4).
const (
	// DefaultPort is the preferred port; PortAttempts ports are tried.
	DefaultPort  = 47821
	PortAttempts = 10
	// ReadHeaderTimeout bounds the reading of the request line and headers.
	ReadHeaderTimeout = 10 * time.Second
	// IdleTimeout closes keep-alive connections without a request.
	IdleTimeout = 60 * time.Second
	// MaxHeaderBytes bounds the request line and headers (the signed
	// requests need well under 1 KiB of headers); beyond it, 431.
	MaxHeaderBytes = 16 << 10
	// ShutdownTimeout is how long running requests may finish at shutdown.
	ShutdownTimeout = 10 * time.Second
	// shutdownGrace lets cancelled requests answer before the connections
	// are closed.
	shutdownGrace = time.Second
)

// Listen opens a TCP listener on host, on the first free port among
// preferred and the PortAttempts-1 next ones.
func Listen(host string, preferred int) (net.Listener, int, error) {
	if preferred <= 0 || preferred > 65535 {
		preferred = DefaultPort
	}
	var lastErr error
	for port := preferred; port < preferred+PortAttempts && port <= 65535; port++ {
		ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err == nil {
			return ln, port, nil
		}
		lastErr = err
		if !errors.Is(err, syscall.EADDRINUSE) {
			break
		}
	}
	return nil, 0, fmt.Errorf("no free port from %d: %w", preferred, lastErr)
}

// Serve serves the API on ln until ctx is done, then shuts down
// (docs/specs/05-console-pak.md §2): new requests get 503 shutting_down,
// running ones get ShutdownTimeout to finish, then the base context of
// every request is cancelled (requests still waiting for the body memory
// budget answer 503) and the connections are closed. It returns nil after
// a shutdown requested by ctx.
//
// Note: http.Server.Shutdown closes the listener at once, so new
// connections are refused at the TCP level; the 503 shutting_down answer
// only reaches requests on connections already accepted (keep-alive
// connections, or a request read just before the shutdown started).
//
// With Options.TLS (always, in the pak: M13), every connection is TLS:
// the handshake is bounded by ReadHeaderTimeout and plain HTTP gets no
// answer from the API.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	if s.opts.TLS != nil {
		ln = tls.NewListener(ln, s.opts.TLS)
	}
	baseCtx, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()
	srv := s.httpServer(baseCtx)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	s.log.Info("shutting down")
	s.BeginShutdown()
	// The streams get the shutdown event (unless the pak already sent it
	// with its reason) and end, before the server waits for the requests.
	s.CloseEvents(events.ReasonStopped)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.timeouts.shutdown)
	err := srv.Shutdown(shutdownCtx)
	cancel()
	if err != nil {
		s.log.Warn("requests still running after the shutdown timeout, cancelling them")
		cancelBase()
		graceCtx, cancel := context.WithTimeout(context.Background(), s.timeouts.grace)
		_ = srv.Shutdown(graceCtx)
		cancel()
		_ = srv.Close()
	}
	<-errc
	return nil
}

// httpServer returns the http.Server of the API; every request context
// derives from baseCtx.
func (s *Server) httpServer(baseCtx context.Context) *http.Server {
	return &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: ReadHeaderTimeout,
		IdleTimeout:       IdleTimeout,
		MaxHeaderBytes:    MaxHeaderBytes,
		// No ReadTimeout nor WriteTimeout: transfers can be long and the
		// event streams stay open. The body has an inactivity deadline
		// instead (BodyIdleTimeout), and so do the file downloads and the
		// event streams (SendIdleTimeout, per write).
		BaseContext: func(net.Listener) context.Context { return baseCtx },
		// net/http's own messages (bad requests, TLS attempts...) can be
		// caused by anyone: sampled like the rejected requests.
		ErrorLog: slog.NewLogLogger(s.rejectLog.Handler(), slog.LevelWarn),
	}
}
