// Package server is the HTTP API of the pak (protocol/openapi.yaml): the
// router, the middlewares, the handlers and the HTTP server lifecycle.
package server

import (
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/cheats"
	"github.com/tfournier/ReminisLink/internal/device"
	"github.com/tfournier/ReminisLink/internal/events"
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
	// APIVersion is the version of the API: 2 since M13 (TLS, pairing v2,
	// RL2-ED25519 signing). The path prefix stays /api/v1.
	APIVersion = 2
	// MinClientVersion is the oldest client protocol version the pak
	// supports (X.Y.Z, M17): one value for every client (iOS, Android,
	// web), compared by each with its own protocol version constant, not
	// with an app store version. Raise it when the pak drops something
	// older clients need; they then ask the user to update the app.
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
	// Transfers holds the uploads and the jobs.
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
	// TLS is the configuration of the HTTPS server (M13: the pak serves
	// TLS only, tlsid.Identity.ServerConfig). Nil serves plain HTTP
	// (handler tests only).
	TLS *tls.Config
	// LogFull reports that the log file reached its cap: the sampled lines
	// (unsigned and rejected requests) are then no longer written (nil:
	// never full).
	LogFull func() bool
}

// Server serves the API. Create it with New.
type Server struct {
	opts      Options
	log       *slog.Logger
	rejectLog *slog.Logger // sampled: lines about rejected requests
	presence  *presence
	// access is the M10 state (disconnections, access generations)
	// behind one mutex, in memory only.
	access *access
	// gateHook, when set (tests), is called after each gate decision.
	gateHook atomic.Pointer[func()]
	// jobHook, when set (tests), is called by a request that queued a
	// job, before it checks its client again.
	jobHook atomic.Pointer[func(id string)]
	// enqueueHook, when set (tests), is called by a request about to
	// queue a job, after its acceptance.
	enqueueHook atomic.Pointer[func()]
	// routeTable lists the signed routes with their options, as
	// registered (walked by the tests: every write route takes the write
	// lock).
	routeTable   []signedRoute
	now          func() time.Time
	started      time.Time
	shuttingDown atomic.Bool
	deviceName   atomic.Pointer[string]
	hello        *rateLimiter
	pair         *rateLimiter
	handler      http.Handler
	timeouts     timeouts
	// allGamesLimit is the most games of GET /games (library.MaxAllGames;
	// changed by tests).
	allGamesLimit int
	// artETags caches the content ETags of GET /games/{gameId}/art.
	artETags artETags
	// artLookupHook, when set (tests), is called by GET /art between the
	// read of the write lock generation and the lookup of the art.
	artLookupHook atomic.Pointer[func()]
	// events is the hub of GET /events (M14); sampler reads the device
	// state while a stream is open.
	events  *events.Hub
	sampler deviceSampler
	// streamHook, when set (tests), is called by a stream after it took
	// an event, before writing it.
	streamHook atomic.Pointer[func(events.Event)]
	// sampleHook, when set (tests), is called after each device sample
	// (changed: the streams were marked).
	sampleHook atomic.Pointer[func(changed bool)]
	// deviceHook, when set (tests), is called when a stream starts
	// building a device event.
	deviceHook atomic.Pointer[func()]
}

// timeouts are the durations of the server, changed by tests only.
type timeouts struct {
	bodyIdle time.Duration // BodyIdleTimeout
	sendIdle time.Duration // SendIdleTimeout
	shutdown time.Duration // ShutdownTimeout
	grace    time.Duration // shutdownGrace
	// keepAlive: KeepAliveInterval; deviceSample and deviceRate (the
	// least time between two device events of a stream):
	// DeviceSampleEvery.
	keepAlive    time.Duration
	deviceSample time.Duration
	deviceRate   time.Duration
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
		opts:          opts,
		log:           opts.Logger,
		rejectLog:     slog.New(newSampledHandler(opts.Logger.Handler(), RejectLogRate, RejectLogBurst, opts.Now, opts.LogFull)),
		now:           opts.Now,
		started:       opts.Now(),
		hello:         newRateLimiter(HelloRate, HelloBurst, opts.Now),
		pair:          newRateLimiter(PairRate, PairBurst, opts.Now),
		presence:      newPresence(),
		allGamesLimit: library.MaxAllGames,
		timeouts: timeouts{
			bodyIdle:     BodyIdleTimeout,
			sendIdle:     SendIdleTimeout,
			shutdown:     ShutdownTimeout,
			grace:        shutdownGrace,
			keepAlive:    KeepAliveInterval,
			deviceSample: DeviceSampleEvery,
			deviceRate:   DeviceSampleEvery,
		},
	}
	s.events = s.newEventHub(events.Options{})
	s.access = newAccess(opts.Now, opts.Clients, s.presence)
	// A revoked client (disconnected, forgotten, unpaired, paired again)
	// loses its event streams and its jobs at once (M13: a job is not
	// installed for a revoked client).
	s.access.onRevoke = func(id string, gen uint64) {
		s.events.CloseClient(id)
		opts.Transfers.Jobs().CancelStale(id, gen)
	}
	s.access.onPresence = s.presenceChanged
	opts.Transfers.Jobs().OnUpdate(s.jobUpdated)
	name := opts.Identity.DeviceName
	s.deviceName.Store(&name)
	// The rejections of the verifier share the sampled logger. The
	// verifier is configured here, before it serves any request.
	opts.Verifier.Logger = s.rejectLog
	// The disconnection (M10) is checked in one place, before any body is
	// read.
	opts.Verifier.Gate = s.gate
	// Clients are read through access, with their access generation.
	opts.Verifier.Clients = s.access
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
	// library: a successful answer publishes a library event (M14).
	library bool
	// write: how the route takes the write lock (M13, writes.go).
	write writeMode
}

// routes registers the API. Signed routes are wrapped one by one with the
// options of the route, fixed here (never deduced from the path): body
// limit, disconnection and how the route takes the write lock.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	v := s.opts.Verifier
	s.routeTable = nil
	reg := func(pattern string, opts auth.RouteOptions, mode writeMode, library bool, h http.HandlerFunc) {
		if mode == writeHandler {
			// The lock is taken once the body was verified (the verifier
			// runs first), never while a body is read.
			h = s.serialized(h)
		}
		if library {
			h = s.changes(h)
		}
		s.routeTable = append(s.routeTable, signedRoute{pattern: pattern, opts: opts, library: library, write: mode})
		mux.Handle(pattern, v.Require(opts, s.markSeen(h)))
	}
	// signed: a read (or a route that changes nothing on the card).
	signed := func(pattern string, opts auth.RouteOptions, h http.HandlerFunc) {
		reg(pattern, opts, writeNone, false, h)
	}
	// changing: a write serialized as a whole; it publishes a library
	// event when it succeeds (M14).
	changing := func(pattern string, opts auth.RouteOptions, h http.HandlerFunc) {
		reg(pattern, opts, writeHandler, true, h)
	}

	// Unsigned routes.
	mux.Handle(helloPattern, s.limit(s.hello, http.HandlerFunc(s.handleHello)))
	mux.Handle(pairPattern, s.limit(s.pair, http.HandlerFunc(s.handlePair)))

	// Device.
	signed("DELETE "+Prefix+"/clients/me", auth.RouteJSON, s.handleUnpair)
	signed("GET "+Prefix+"/device", auth.RouteJSON, s.handleDevice)
	reconnect := auth.RouteJSON
	reconnect.WhileDisconnected = true
	signed("POST "+Prefix+"/clients/me/connect", reconnect, s.handleConnect)
	// Event stream (M14): a read route, never waits for a write.
	signed(eventsPattern, auth.RouteJSON, s.handleEvents)

	// Library.
	signed("GET "+Prefix+"/systems", auth.RouteJSON, s.handleSystems)
	signed("GET "+Prefix+"/systems/{systemId}/games", auth.RouteJSON, s.handleSystemGames)
	signed("GET "+Prefix+"/games", auth.RouteJSON, s.handleAllGames)
	signed("GET "+Prefix+"/games/{gameId}", auth.RouteJSON, s.handleGame)
	// Read route (M11): never waits for a write.
	signed("GET "+Prefix+"/games/{gameId}/files/{fileId}", auth.RouteJSON, s.handleGameFile)
	signed("GET "+Prefix+"/games/{gameId}/art", auth.RouteJSON, s.handleGameArt)
	// PUT /art takes a PNG of at most 4 MiB.
	changing("PUT "+Prefix+"/games/{gameId}/art", auth.RouteArt, s.notify(ChangeArtSet, s.gameName, s.handlePutArt))
	changing("DELETE "+Prefix+"/games/{gameId}/art", auth.RouteJSON, s.notify(ChangeArtDeleted, s.gameName, s.handleDeleteArt))

	signed("GET "+Prefix+"/games/{gameId}/deletion", auth.RouteJSON, s.handleDeletionPreview)
	changing("DELETE "+Prefix+"/games/{gameId}", auth.RouteJSON, s.notify(ChangeGameDeleted, s.gameName, s.handleDeleteGame))

	// Trash.
	signed("GET "+Prefix+"/trash", auth.RouteJSON, s.handleTrash)
	changing("DELETE "+Prefix+"/trash", auth.RouteJSON, s.notify(ChangeTrashEmptied, nil, s.handleEmptyTrash))
	changing("DELETE "+Prefix+"/trash/{entryId}", auth.RouteJSON, s.notify(ChangeTrashEntryDeleted, nil, s.handleDeleteTrashEntry))

	// Transfers. Only the chunk route accepts UNSIGNED bodies (read as a
	// stream by the handler, bounded by the chunk size). Uploads and their
	// chunks go to the staging area without waiting for a write; the final
	// step holds the write lock around the install (M13).
	reg("POST "+Prefix+"/uploads", auth.RouteJSON, writeStaging, false, s.handleCreateUpload)
	signed("GET "+Prefix+"/uploads/{uploadId}", auth.RouteJSON, s.handleUploadStatus)
	reg("PUT "+Prefix+"/uploads/{uploadId}", auth.RouteChunk, writeStaging, false, s.handleUploadChunk)
	reg("DELETE "+Prefix+"/uploads/{uploadId}", auth.RouteJSON, writeStaging, false, s.handleCancelUpload)
	// changesUpload publishes its own library event (only for a file
	// installed in Roms/).
	reg("POST "+Prefix+"/uploads/{uploadId}/complete", auth.RouteJSON, writeInstall, false, s.changesUpload(s.notifyUpload(s.handleCompleteUpload)))
	s.routeTable[len(s.routeTable)-1].library = true
	signed("GET "+Prefix+"/jobs", auth.RouteJSON, s.handleJobs)
	signed("GET "+Prefix+"/jobs/{jobId}", auth.RouteJSON, s.handleJob)
	reg("DELETE "+Prefix+"/jobs/{jobId}", auth.RouteJSON, writeStaging, false, s.handleCancelJob)

	// Saves. Only PUT /save takes a larger body (64 MiB).
	signed("GET "+Prefix+"/saves", auth.RouteJSON, s.handleListSaves)
	signed("GET "+Prefix+"/games/{gameId}/save", auth.RouteJSON, s.handleGetSave)
	signed("GET "+Prefix+"/games/{gameId}/save/rtc", auth.RouteJSON, s.handleGetSaveClock)
	changing("PUT "+Prefix+"/games/{gameId}/save", auth.RouteSave, s.notify(ChangeSaveReplaced, s.gameName, s.handlePutSave))
	signed("GET "+Prefix+"/games/{gameId}/save/backups", auth.RouteJSON, s.handleListBackups)
	changing("POST "+Prefix+"/games/{gameId}/save/backups", auth.RouteJSON, s.handleCreateBackup)
	signed("GET "+Prefix+"/games/{gameId}/save/backups/{backupId}", auth.RouteJSON, s.handleGetBackup)
	changing("DELETE "+Prefix+"/games/{gameId}/save/backups/{backupId}", auth.RouteJSON, s.handleDeleteBackup)
	changing("POST "+Prefix+"/games/{gameId}/save/backups/{backupId}/restore", auth.RouteJSON, s.notify(ChangeSaveRestored, s.gameName, s.handleRestoreBackup))

	// States, locks and resume history.
	signed("GET "+Prefix+"/games/{gameId}/states", auth.RouteJSON, s.handleListStates)
	signed("GET "+Prefix+"/games/{gameId}/states/{slot}", auth.RouteJSON, s.handleGetState)
	changing("DELETE "+Prefix+"/games/{gameId}/states/{slot}", auth.RouteJSON, s.notify(ChangeStateDeleted, s.gameName, s.handleDeleteState))
	signed("GET "+Prefix+"/games/{gameId}/states/{slot}/thumbnail", auth.RouteJSON, s.handleStateThumbnail)
	changing("PUT "+Prefix+"/games/{gameId}/states/{slot}/lock", auth.RouteJSON, s.handleLockState)
	changing("DELETE "+Prefix+"/games/{gameId}/states/{slot}/lock", auth.RouteJSON, s.handleUnlockState)
	changing("POST "+Prefix+"/games/{gameId}/states/{slot}/restore", auth.RouteJSON, s.notify(ChangeStateRestored, s.gameName, s.handleRestoreState))
	signed("GET "+Prefix+"/games/{gameId}/resume-history", auth.RouteJSON, s.handleResumeHistory)
	changing("POST "+Prefix+"/games/{gameId}/resume-history/{entryId}/restore", auth.RouteJSON, s.notify(ChangeStateRestored, s.gameName, s.handleRestoreResume))

	// Cheats.
	signed("GET "+Prefix+"/games/{gameId}/cheats", auth.RouteJSON, s.handleGetCheats)
	changing("PUT "+Prefix+"/games/{gameId}/cheats", auth.RouteJSON, s.notify(ChangeCheatsUpdated, s.gameName, s.handlePutCheats))
	// The import reads its source first, then takes the write lock.
	reg("POST "+Prefix+"/games/{gameId}/cheats/import", auth.RouteJSON, writeInner, true, s.notify(ChangeCheatsUpdated, s.gameName, s.handleImportCheats))
	signed("GET "+Prefix+"/sd/files", auth.RouteJSON, s.handleSDFiles)

	// Patches (a job; it waits for the write lock at its install step only).
	reg("POST "+Prefix+"/games/{gameId}/patch", auth.RouteJSON, writeInstall, false, s.handlePatchGame)

	// Paks (M12). The deletion runs under the write lock; an installation
	// is a job that waits for it at its install step. Both publish a paks
	// event, never a library one.
	signed("GET "+Prefix+"/paks", auth.RouteJSON, s.handleListPaks)
	reg("DELETE "+Prefix+"/paks/{pakId}", auth.RouteJSON, writeHandler, false, s.paksChanged(s.notify(ChangePakDeleted, s.pakName, s.handleDeletePak)))
	reg("POST "+Prefix+"/paks/install", auth.RouteJSON, writeInstall, false, s.handleInstallPak)

	// Read-only SD browser (M12): reads, never wait for a write.
	signed("GET "+Prefix+"/sd/browse", auth.RouteJSON, s.handleBrowseSD)
	signed("GET "+Prefix+"/sd/browse/{sdEntryId}/content", auth.RouteJSON, s.handleSDContent)

	// Every signed route besides PUT /save, PUT /art and the upload chunks
	// uses auth.RouteJSON.

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		auth.WriteError(w, http.StatusNotFound, CodeNotFound, "No such route", nil)
	})
	return mux
}
