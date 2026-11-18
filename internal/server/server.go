// Package server is the HTTP API of the pak (protocol/openapi.yaml): the
// router, the middlewares, the handlers and the HTTP server lifecycle.
package server

import (
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/device"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter"
)

// API constants.
const (
	// Prefix starts every route.
	Prefix = "/api/v1"
	// APIVersion is the major version of the API.
	APIVersion = 1
	// MinClientVersion is the oldest app version the pak supports.
	MinClientVersion = "1.0.0"
	// MaxJSONBody is the largest JSON body of an unsigned route (/pair).
	MaxJSONBody = 1 << 20
)

// Error codes of the API besides those of internal/auth
// (docs/specs/03-protocole.md §6).
const (
	CodeInvalidID     = "invalid_id"
	CodeNotFound      = "not_found"
	CodePairingFailed = "pairing_failed"
)

// Identity is what the pak says about itself.
type Identity struct {
	DeviceID   string
	DeviceName string
	PakVersion string
}

// Options are the dependencies of the server.
type Options struct {
	Identity Identity
	Adapter  osadapter.Adapter
	Library  *library.Library
	Clients  *auth.ClientStore
	Pairing  *auth.PairingSession
	// Verifier checks the signed routes. Build it with auth.NewVerifier on
	// the same clock as Now.
	Verifier *auth.Verifier
	Now      func() time.Time // nil: time.Now
	Logger   *slog.Logger     // nil: slog.Default()
	// IPv4 returns the address of the console, preferring the given
	// interface (nil: device.IPv4).
	IPv4 func(preferredInterface string) (string, bool)
	// Storage returns the space of the SD card (nil: device.StorageOf).
	Storage func(path string) (device.Storage, error)
	// OnPaired is called after a client is paired (may be nil).
	OnPaired func(auth.Client)
}

// Server serves the API. Create it with New.
type Server struct {
	opts         Options
	log          *slog.Logger
	rejectLog    *slog.Logger // sampled: lines about rejected requests
	now          func() time.Time
	started      time.Time
	shuttingDown atomic.Bool
	hello        *rateLimiter
	pair         *rateLimiter
	handler      http.Handler
	timeouts     timeouts
}

// timeouts are the durations of the server, changed by tests only.
type timeouts struct {
	bodyIdle time.Duration // BodyIdleTimeout
	shutdown time.Duration // ShutdownTimeout
	grace    time.Duration // shutdownGrace
}

// New builds the server and its routes.
func New(opts Options) (*Server, error) {
	switch {
	case opts.Adapter == nil, opts.Library == nil, opts.Clients == nil, opts.Pairing == nil, opts.Verifier == nil:
		return nil, errors.New("server: missing dependency")
	case !auth.ValidID(opts.Identity.DeviceID):
		return nil, errors.New("server: device id must be a lowercase UUID")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.IPv4 == nil {
		opts.IPv4 = device.IPv4
	}
	if opts.Storage == nil {
		opts.Storage = device.StorageOf
	}
	s := &Server{
		opts:      opts,
		log:       opts.Logger,
		rejectLog: slog.New(newSampledHandler(opts.Logger.Handler(), RejectLogRate, RejectLogBurst, opts.Now)),
		now:       opts.Now,
		started:   opts.Now(),
		hello:     newRateLimiter(HelloRate, HelloBurst, opts.Now),
		pair:      newRateLimiter(PairRate, PairBurst, opts.Now),
		timeouts: timeouts{
			bodyIdle: BodyIdleTimeout,
			shutdown: ShutdownTimeout,
			grace:    shutdownGrace,
		},
	}
	// The rejections of the verifier share the sampled logger. The
	// verifier is configured here, before it serves any request.
	opts.Verifier.Logger = s.rejectLog
	s.handler = s.middlewares(s.routes())
	return s, nil
}

// Handler returns the HTTP handler of the API.
func (s *Server) Handler() http.Handler { return s.handler }

// BeginShutdown makes every new request fail with 503 shutting_down.
func (s *Server) BeginShutdown() { s.shuttingDown.Store(true) }

// ShuttingDown reports whether BeginShutdown was called.
func (s *Server) ShuttingDown() bool { return s.shuttingDown.Load() }

// Patterns of the unsigned routes.
const (
	helloPattern = "GET " + Prefix + "/hello"
	pairPattern  = "POST " + Prefix + "/pair"
)

// routes registers the API. Signed routes are wrapped one by one with the
// body options of the route, fixed here (never deduced from the path).
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	v := s.opts.Verifier
	signed := func(pattern string, opts auth.RouteOptions, h http.HandlerFunc) {
		mux.Handle(pattern, v.Require(opts, h))
	}

	// Unsigned routes.
	mux.Handle(helloPattern, s.limit(s.hello, http.HandlerFunc(s.handleHello)))
	mux.Handle(pairPattern, s.limit(s.pair, http.HandlerFunc(s.handlePair)))

	// Device.
	signed("DELETE "+Prefix+"/clients/me", auth.RouteJSON, s.handleUnpair)
	signed("GET "+Prefix+"/device", auth.RouteJSON, s.handleDevice)

	// Library.
	signed("GET "+Prefix+"/systems", auth.RouteJSON, s.handleSystems)
	signed("GET "+Prefix+"/systems/{systemId}/games", auth.RouteJSON, s.handleSystemGames)
	signed("GET "+Prefix+"/games/{gameId}", auth.RouteJSON, s.handleGame)
	signed("GET "+Prefix+"/games/{gameId}/art", auth.RouteJSON, s.handleGameArt)

	// Routes of the next milestones register here, with their own body
	// limits:
	//   signed("PUT "+Prefix+"/games/{gameId}/save", auth.RouteSave, ...)    // M4
	//   signed("PUT "+Prefix+"/uploads/{uploadId}", auth.RouteChunk, ...)    // M5
	// Every other signed route uses auth.RouteJSON.

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		auth.WriteError(w, http.StatusNotFound, CodeNotFound, "No such route", nil)
	})
	return mux
}
