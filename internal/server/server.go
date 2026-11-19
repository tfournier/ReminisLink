// Package server is the HTTP API of the pak (protocol/openapi.yaml): the
// router, the middlewares, the handlers and the HTTP server lifecycle.
package server

import (
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/cheats"
	"github.com/tfournier/ReminisLink/internal/device"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/saves"
	"github.com/tfournier/ReminisLink/internal/states"
	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/transfer"
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
	// Trash receives the deleted and replaced files.
	Trash *store.Trash
	// Transfers holds the uploads, the downloads and the jobs.
	Transfers *transfer.Service
	// Saves, States and Cheats serve the routes of the games' data; they
	// share their SD card lock with Transfers.
	Saves  *saves.Service
	States *states.Service
	Cheats *cheats.Service
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
	// OnChange is called after a request changed the files of the SD card
	// (the banner of the pak screen, docs/specs/05-console-pak.md §7.1);
	// may be nil. It must not block.
	OnChange func(Change)
	// LogFull reports that the log file reached its cap: the sampled lines
	// (unsigned and rejected requests) are then no longer written (nil:
	// never full).
	LogFull func() bool
}

// Server serves the API. Create it with New.
type Server struct {
	opts         Options
	log          *slog.Logger
	rejectLog    *slog.Logger // sampled: lines about rejected requests
	presence     *presence
	lease        *auth.WriteLease // M10, in memory only
	disconnected *clientSet       // M10: disconnected by the console
	// access serializes the lease acquisitions with the revocations of a
	// client (disconnect, forget, unpair), M10.
	access sync.Mutex
	// gateHook, when set (tests), is called after each gate decision.
	gateHook atomic.Pointer[func()]
	// routeTable lists the signed routes with their options, as
	// registered (walked by the tests: every write route takes the lease).
	routeTable   []signedRoute
	now          func() time.Time
	started      time.Time
	shuttingDown atomic.Bool
	deviceName   atomic.Pointer[string]
	hello        *rateLimiter
	pair         *rateLimiter
	handler      http.Handler
	timeouts     timeouts
	// fetchSlot allows one cheat import from a URL at a time: its body
	// (up to 5 MiB) is read in memory outside the signed body budget.
	fetchSlot chan struct{}
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
	case opts.Adapter == nil, opts.Library == nil, opts.Clients == nil, opts.Pairing == nil, opts.Verifier == nil,
		opts.Trash == nil, opts.Transfers == nil, opts.Saves == nil, opts.States == nil, opts.Cheats == nil:
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
		opts:         opts,
		log:          opts.Logger,
		rejectLog:    slog.New(newSampledHandler(opts.Logger.Handler(), RejectLogRate, RejectLogBurst, opts.Now, opts.LogFull)),
		now:          opts.Now,
		started:      opts.Now(),
		hello:        newRateLimiter(HelloRate, HelloBurst, opts.Now),
		pair:         newRateLimiter(PairRate, PairBurst, opts.Now),
		presence:     newPresence(),
		lease:        auth.NewWriteLease(opts.Now),
		disconnected: newClientSet(),
		fetchSlot:    make(chan struct{}, 1),
		timeouts: timeouts{
			bodyIdle: BodyIdleTimeout,
			shutdown: ShutdownTimeout,
			grace:    shutdownGrace,
		},
	}
	name := opts.Identity.DeviceName
	s.deviceName.Store(&name)
	// The rejections of the verifier share the sampled logger. The
	// verifier is configured here, before it serves any request.
	opts.Verifier.Logger = s.rejectLog
	// The disconnection and the write lease (M10) are checked in one
	// place, before any body is read.
	opts.Verifier.Gate = s.gate
	s.handler = s.middlewares(s.routes())
	return s, nil
}

// DeviceName returns the name of the console.
func (s *Server) DeviceName() string { return *s.deviceName.Load() }

// SetDeviceName changes the name returned by /hello, /pair and /device
// (renamed in the pak settings).
func (s *Server) SetDeviceName(name string) { s.deviceName.Store(&name) }

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

// signedRoute is a signed route as registered.
type signedRoute struct {
	pattern string
	opts    auth.RouteOptions
}

// writes marks a route that changes the SD card: it takes the write lease
// (M10) or answers 423 read_only, before its body is read. Every route
// whose responses include 423 in protocol/openapi.yaml uses it, except
// PUT /writer (which takes the lease itself).
func writes(o auth.RouteOptions) auth.RouteOptions {
	o.TakesLease = true
	return o
}

// routes registers the API. Signed routes are wrapped one by one with the
// options of the route, fixed here (never deduced from the path): body
// limit, lease (writes) and disconnection.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	v := s.opts.Verifier
	s.routeTable = nil
	signed := func(pattern string, opts auth.RouteOptions, h http.HandlerFunc) {
		s.routeTable = append(s.routeTable, signedRoute{pattern, opts})
		mux.Handle(pattern, v.Require(opts, s.markSeen(h)))
	}
	jsonWrite := writes(auth.RouteJSON)

	// Unsigned routes.
	mux.Handle(helloPattern, s.limit(s.hello, http.HandlerFunc(s.handleHello)))
	mux.Handle(pairPattern, s.limit(s.pair, http.HandlerFunc(s.handlePair)))

	// Device.
	signed("DELETE "+Prefix+"/clients/me", auth.RouteJSON, s.handleUnpair)
	signed("GET "+Prefix+"/device", auth.RouteJSON, s.handleDevice)
	reconnect := auth.RouteJSON
	reconnect.WhileDisconnected = true
	signed("POST "+Prefix+"/clients/me/connect", reconnect, s.handleConnect)

	// Write lease (M10): never guarded by the lease itself.
	signed("GET "+Prefix+"/writer", auth.RouteJSON, s.handleGetWriter)
	signed("PUT "+Prefix+"/writer", auth.RouteJSON, s.handleTakeWriter)
	signed("DELETE "+Prefix+"/writer", auth.RouteJSON, s.handleReleaseWriter)

	// Library.
	signed("GET "+Prefix+"/systems", auth.RouteJSON, s.handleSystems)
	signed("GET "+Prefix+"/systems/{systemId}/games", auth.RouteJSON, s.handleSystemGames)
	signed("GET "+Prefix+"/games/{gameId}", auth.RouteJSON, s.handleGame)
	signed("GET "+Prefix+"/games/{gameId}/art", auth.RouteJSON, s.handleGameArt)
	// PUT /art takes a PNG of at most 4 MiB.
	signed("PUT "+Prefix+"/games/{gameId}/art", writes(auth.RouteArt), s.notify(ChangeArtSet, s.gameName, s.handlePutArt))
	signed("DELETE "+Prefix+"/games/{gameId}/art", jsonWrite, s.notify(ChangeArtDeleted, s.gameName, s.handleDeleteArt))

	signed("GET "+Prefix+"/games/{gameId}/deletion", auth.RouteJSON, s.handleDeletionPreview)
	signed("DELETE "+Prefix+"/games/{gameId}", jsonWrite, s.notify(ChangeGameDeleted, s.gameName, s.handleDeleteGame))

	// Trash.
	signed("GET "+Prefix+"/trash", auth.RouteJSON, s.handleTrash)
	signed("DELETE "+Prefix+"/trash", jsonWrite, s.notify(ChangeTrashEmptied, nil, s.handleEmptyTrash))
	signed("DELETE "+Prefix+"/trash/{entryId}", jsonWrite, s.notify(ChangeTrashEntryDeleted, nil, s.handleDeleteTrashEntry))

	// Transfers. Only the chunk route accepts UNSIGNED bodies (read as a
	// stream by the handler, bounded by the chunk size).
	signed("POST "+Prefix+"/uploads", jsonWrite, s.handleCreateUpload)
	signed("GET "+Prefix+"/uploads/{uploadId}", auth.RouteJSON, s.handleUploadStatus)
	signed("PUT "+Prefix+"/uploads/{uploadId}", writes(auth.RouteChunk), s.handleUploadChunk)
	signed("DELETE "+Prefix+"/uploads/{uploadId}", jsonWrite, s.handleCancelUpload)
	signed("POST "+Prefix+"/uploads/{uploadId}/complete", jsonWrite, s.notifyUpload(s.handleCompleteUpload))
	signed("POST "+Prefix+"/downloads", jsonWrite, s.handleCreateDownload)
	signed("GET "+Prefix+"/jobs", auth.RouteJSON, s.handleJobs)
	signed("GET "+Prefix+"/jobs/{jobId}", auth.RouteJSON, s.handleJob)
	signed("DELETE "+Prefix+"/jobs/{jobId}", jsonWrite, s.handleCancelJob)

	// Saves. Only PUT /save takes a larger body (64 MiB).
	signed("GET "+Prefix+"/saves", auth.RouteJSON, s.handleListSaves)
	signed("GET "+Prefix+"/games/{gameId}/save", auth.RouteJSON, s.handleGetSave)
	signed("GET "+Prefix+"/games/{gameId}/save/rtc", auth.RouteJSON, s.handleGetSaveClock)
	signed("PUT "+Prefix+"/games/{gameId}/save", writes(auth.RouteSave), s.notify(ChangeSaveReplaced, s.gameName, s.handlePutSave))
	signed("GET "+Prefix+"/games/{gameId}/save/backups", auth.RouteJSON, s.handleListBackups)
	signed("POST "+Prefix+"/games/{gameId}/save/backups", jsonWrite, s.handleCreateBackup)
	signed("GET "+Prefix+"/games/{gameId}/save/backups/{backupId}", auth.RouteJSON, s.handleGetBackup)
	signed("DELETE "+Prefix+"/games/{gameId}/save/backups/{backupId}", jsonWrite, s.handleDeleteBackup)
	signed("POST "+Prefix+"/games/{gameId}/save/backups/{backupId}/restore", jsonWrite, s.notify(ChangeSaveRestored, s.gameName, s.handleRestoreBackup))

	// States, locks and resume history.
	signed("GET "+Prefix+"/games/{gameId}/states", auth.RouteJSON, s.handleListStates)
	signed("GET "+Prefix+"/games/{gameId}/states/{slot}", auth.RouteJSON, s.handleGetState)
	signed("DELETE "+Prefix+"/games/{gameId}/states/{slot}", jsonWrite, s.notify(ChangeStateDeleted, s.gameName, s.handleDeleteState))
	signed("GET "+Prefix+"/games/{gameId}/states/{slot}/thumbnail", auth.RouteJSON, s.handleStateThumbnail)
	signed("PUT "+Prefix+"/games/{gameId}/states/{slot}/lock", jsonWrite, s.handleLockState)
	signed("DELETE "+Prefix+"/games/{gameId}/states/{slot}/lock", jsonWrite, s.handleUnlockState)
	signed("POST "+Prefix+"/games/{gameId}/states/{slot}/restore", jsonWrite, s.notify(ChangeStateRestored, s.gameName, s.handleRestoreState))
	signed("GET "+Prefix+"/games/{gameId}/resume-history", auth.RouteJSON, s.handleResumeHistory)
	signed("POST "+Prefix+"/games/{gameId}/resume-history/{entryId}/restore", jsonWrite, s.notify(ChangeStateRestored, s.gameName, s.handleRestoreResume))

	// Cheats.
	signed("GET "+Prefix+"/games/{gameId}/cheats", auth.RouteJSON, s.handleGetCheats)
	signed("PUT "+Prefix+"/games/{gameId}/cheats", jsonWrite, s.notify(ChangeCheatsUpdated, s.gameName, s.handlePutCheats))
	signed("POST "+Prefix+"/games/{gameId}/cheats/import", jsonWrite, s.notify(ChangeCheatsUpdated, s.gameName, s.handleImportCheats))
	signed("GET "+Prefix+"/sd/files", auth.RouteJSON, s.handleSDFiles)

	// Patches (a job, like the downloads).
	signed("POST "+Prefix+"/games/{gameId}/patch", jsonWrite, s.handlePatchGame)

	// Every signed route besides PUT /save, PUT /art and the upload chunks
	// uses auth.RouteJSON (jsonWrite when it changes the SD card).

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		auth.WriteError(w, http.StatusNotFound, CodeNotFound, "No such route", nil)
	})
	return mux
}
