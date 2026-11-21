// Package app runs the pak (docs/specs/05-console-pak.md §2): it opens the
// log and the configuration, builds the services, runs the maintenance,
// serves the API in a goroutine, announces it with Bonjour and shuts it
// down. The headless mode and the gabagool UI (internal/ui) both drive it;
// the UI reads its state with Status, which never waits for a request.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/cheats"
	"github.com/tfournier/ReminisLink/internal/device"
	"github.com/tfournier/ReminisLink/internal/discovery"
	"github.com/tfournier/ReminisLink/internal/events"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/paks"
	"github.com/tfournier/ReminisLink/internal/saves"
	"github.com/tfournier/ReminisLink/internal/server"
	"github.com/tfournier/ReminisLink/internal/states"
	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/tlsid"
	"github.com/tfournier/ReminisLink/internal/transfer"
)

// MaintenanceTimeLimit bounds the checks of the states at start (resume
// history and locks): what is left is checked at the next start.
const MaintenanceTimeLimit = 20 * time.Second

// Options configure the pak.
type Options struct {
	Roots osadapter.Roots
	// OS is the firmware: "nextui" (default when empty) or "knulli"
	// (milestone M20, experimental).
	OS      string
	Env     nextui.Env // DEVICE, PLATFORM (nil: none)
	Version string     // pak version
	Mode    string     // headless, ui or desktop (logged)
	Port    int        // preferred port for this run (0: config.json)
	Name    string     // device name for this run (empty: config.json)
	// LogOut also receives the log lines (stderr in headless mode; nil:
	// the log file only).
	LogOut io.Writer
	// OnPaired is called after a client is paired, besides the banner
	// (the headless mode prints it; may be nil).
	OnPaired func(auth.Client)
	// NoWiFiIP is the address used while no Wi-Fi address is found
	// ("127.0.0.1" in headless mode). Empty: the pak is offline then
	// (Status.WiFi false) until an address comes back.
	NoWiFiIP string
	// Pairing says who opens pairing (PairingManual: the UI).
	Pairing PairingMode
}

// ErrStopped is returned by Maintain and Start once Shutdown has begun
// (a signal arrived during the maintenance).
var ErrStopped = errors.New("app: stopped")

// NetworkCheckEvery is how often the address of the Wi-Fi interface is
// checked: a sleep of the console (short press on the power button) stops
// the Wi-Fi, and DHCP may give another address on wake-up.
const NetworkCheckEvery = 3 * time.Second

// LoggedError is an error already written to the log file (and to LogOut).
type LoggedError struct{ Err error }

func (e LoggedError) Error() string { return e.Err.Error() }
func (e LoggedError) Unwrap() error { return e.Err }

// announcer registers the Bonjour service; tests replace it.
type announcer func(name string, port int, info discovery.Info) (withdrawer, error)

type withdrawer interface{ Shutdown() }

func bonjour(name string, port int, info discovery.Info) (withdrawer, error) {
	return discovery.Announce(name, port, info, nil)
}

// App is a running pak. Open it, run Maintain, then Start; Shutdown stops
// the server and Close releases the log.
type App struct {
	opts Options
	log  *slog.Logger
	logf *logFile

	roots   osadapter.Roots
	adapter deviceAdapter
	info    osadapter.DeviceInfo
	layout  store.Layout
	config  *store.ConfigFile

	deviceID string // immutable

	mu   sync.Mutex
	conf store.Config // config.json as last saved
	name string       // current device name (Options.Name until renamed)
	ip   string       // address of the pairing link ("" while offline)
	wifi bool         // a Wi-Fi address is known (or NoWiFiIP is used)

	clients *auth.ClientStore
	pairing *auth.PairingSession
	tls     tlsid.Identity // the certificate the app pins (M13)
	lib     *library.Library
	trash   *store.Trash
	saves   *saves.Service
	states  *states.Service
	cheats  *cheats.Service
	xfer    *transfer.Service

	stopCredit  func()
	notices     *notices
	announce    announcer
	ipv4        func(preferred string) (string, bool)
	ifaceIPv4   func(name string) (string, bool)
	maintenance func(ctx context.Context) error // tests

	// Maintenance, cancelled by Shutdown.
	maintMu     sync.Mutex // held while Maintain runs
	maintCtx    context.Context
	maintCancel context.CancelFunc

	// life guards the start against a concurrent Shutdown.
	life     sync.Mutex
	stopping bool

	// Set by Start.
	srv        *server.Server
	port       int
	annMu      sync.Mutex
	ann        withdrawer
	annFailing bool // the last registration failed (logged once)
	cancel     context.CancelFunc
	served     chan struct{} // closed when the server stopped
	serveErr   error         // set before served is closed
	stopWatch  chan struct{} // stops the network watch
	watchDone  chan struct{}
	checkEvery time.Duration // NetworkCheckEvery (tests change it)

	batMu sync.Mutex // guards bat and batAt
	bat   *osadapter.Battery
	batAt time.Time

	stopOnce sync.Once
	stopErr  error
}

// Open opens the log and the configuration and builds the services. An
// error after the log is open is logged and returned as a LoggedError.
func Open(opts Options) (a *App, err error) {
	if opts.Env == nil {
		opts.Env = func(string) string { return "" }
	}
	logger, logf, err := openLog(opts.Roots.Logs, opts.LogOut)
	if err != nil {
		return nil, err
	}
	a = &App{opts: opts, log: logger, logf: logf, roots: opts.Roots, notices: newNotices(time.Now),
		announce: bonjour, ipv4: device.IPv4, ifaceIPv4: device.IPv4On, checkEvery: NetworkCheckEvery}
	a.maintCtx, a.maintCancel = context.WithCancel(context.Background())
	defer func() {
		if err != nil {
			logger.Error("fatal", "error", err)
			logf.f.Close()
			a, err = nil, LoggedError{err}
		}
	}()
	slog.SetDefault(logger)

	if a.adapter, err = newAdapter(opts); err != nil {
		return a, err
	}
	if a.info, err = a.adapter.DeviceInfo(); err != nil {
		return a, err
	}
	a.layout = store.Layout{Root: opts.Roots.Data}
	if a.conf, err = store.LoadConfig(a.layout.ConfigFile(), func(id string) string {
		return nextui.DefaultDeviceName(a.info.Model, id)
	}, nil); err != nil {
		return a, err
	}
	a.deviceID = a.conf.DeviceID
	a.name = a.conf.DeviceName
	if opts.Name != "" {
		a.name = opts.Name
	}
	logger.Info("ReminisLink started", "version", opts.Version, "mode", opts.Mode, "sd", opts.Roots.SD,
		"device", a.deviceID, "name", a.name, "model", a.info.Model, "platform", a.info.Platform)

	if a.clients, err = auth.OpenClientStore(a.layout.ClientsFile()); err != nil {
		return a, err
	}
	if n := a.clients.WipedRL1(); n > 0 {
		// M13 migration: RL1 pairings (shared keys) must pair again.
		// Never the keys, only the count.
		logger.Warn("RL1 clients erased: they must pair again", "count", n)
	}
	if a.tls, err = tlsid.LoadOrCreate(tlsid.Options{Dir: a.layout.TLSDir(), DeviceID: a.deviceID, Logger: logger}); err != nil {
		return a, err
	}
	if a.pairing, err = auth.NewPairingSession(a.deviceID, nil, nil); err != nil {
		return a, err
	}
	a.lib = library.New(a.adapter, a.layout)
	a.trash = store.NewTrash(a.layout, opts.Roots.SD, nil, logger)
	// From here config.json is only written through a.config.
	a.config = store.NewConfigFile(a.layout.ConfigFile())
	// The credited-age clock of the trash (05 §4): saved every 5 minutes
	// and marked closed when the pak exits.
	credit, err := store.NewCreditClock(a.config, nil, nil)
	if err != nil {
		return a, err
	}
	a.trash.UseCreditClock(credit)
	stop := make(chan struct{})
	go credit.Run(stop)
	a.stopCredit = sync.OnceFunc(func() {
		close(stop)
		if err := credit.Close(); err != nil {
			logger.Warn("cannot save the trash credit clock", "error", err)
		}
	})
	defer func() {
		if err != nil {
			a.stopCredit()
		}
	}()
	// One lock for every change of the SD card files (uploads, saves,
	// states, cheats, deletions).
	sdLock := &sync.Mutex{}
	if a.saves, err = saves.New(saves.Options{Adapter: a.adapter, Layout: a.layout, Trash: a.trash, Lock: sdLock, Logger: logger}); err != nil {
		return a, err
	}
	if a.states, err = states.New(states.Options{Adapter: a.adapter, Layout: a.layout, Trash: a.trash, Lock: sdLock, Logger: logger}); err != nil {
		return a, err
	}
	if a.cheats, err = cheats.New(cheats.Options{Adapter: a.adapter, Trash: a.trash, Lock: sdLock, Logger: logger}); err != nil {
		return a, err
	}
	if a.xfer, err = transfer.New(transfer.Options{
		Library: a.lib, Adapter: a.adapter, Layout: a.layout, Trash: a.trash,
		Saves: a.saves, States: a.states, Lock: sdLock,
		FreeSpace: func(path string) (int64, error) {
			st, err := device.StorageOf(path)
			return st.FreeBytes, err
		},
		Logger: logger,
		// The running pak can be neither deleted nor replaced (M12).
		Paks: paks.NewManager(opts.Roots, selfPakDir()),
	}); err != nil {
		return a, err
	}
	a.xfer.Jobs().OnFinish(a.notices.jobFinished)
	return a, nil
}

// Logger returns the logger of the pak.
func (a *App) Logger() *slog.Logger { return a.log }

// LogPath returns the path of the log file.
func (a *App) LogPath() string { return a.logf.f.Name() }

// Version returns the version of the pak.
func (a *App) Version() string { return a.opts.Version }

// Pin returns the pin of the TLS certificate of the pak (M13): the "k" of
// the pairing link, b64u(SHA-256(SPKI DER)). Not a secret.
func (a *App) Pin() string { return a.tls.Pin }

// DeviceID returns the identifier of the console.
func (a *App) DeviceID() string { return a.deviceID }

// Info returns what the OS adapter says about the console.
func (a *App) Info() osadapter.DeviceInfo { return a.info }

// IsDesktop reports whether the pak runs off the console (no NextUI
// DEVICE variable): any network interface is then accepted.
func (a *App) IsDesktop() bool { return a.info.Model == nextui.ModelDesktop }

// WiFiIPv4 returns the IPv4 address the pak is reached at (05 §2): on the
// console, a non-loopback address of the Wi-Fi interface (wlan0); off the
// console, the address of any interface. ok is false without one.
func (a *App) WiFiIPv4() (string, bool) {
	if a.IsDesktop() || a.info.WirelessInterface == "" {
		return a.ipv4(a.info.WirelessInterface)
	}
	return a.ifaceIPv4(a.info.WirelessInterface)
}

// Maintain runs the maintenance of 05 §2.3 before the server opens:
// interrupted trash operations are finished, the resume states that
// changed are archived and the locked states that were overwritten or
// removed are restored (at most MaintenanceTimeLimit for both), the trash
// entries with a credited age of 30 days are erased, the uploads idle for
// 24 h and the staging files older than 24 h are deleted, and the other
// uploads are resumed.
//
// Shutdown cancels it: the state checks stop at once (what is left is
// checked at the next start), the other steps are not started, and
// Maintain returns ErrStopped.
func (a *App) Maintain() error {
	a.maintMu.Lock()
	defer a.maintMu.Unlock()
	stopped := func() bool { return a.maintCtx.Err() != nil }
	if stopped() {
		return ErrStopped
	}
	if a.maintenance != nil {
		if err := a.maintenance(a.maintCtx); err != nil {
			return err
		}
		if stopped() {
			return ErrStopped
		}
		return nil
	}
	if err := a.trash.Recover(); err != nil {
		return fmt.Errorf("maintenance: %w", err)
	}
	ctx, cancel := context.WithTimeout(a.maintCtx, MaintenanceTimeLimit)
	start := time.Now()
	report := a.states.Maintain(ctx)
	cancel()
	for _, r := range report.Restored {
		a.log.Info("maintenance: locked state restored", "tag", r.Tag, "rom", r.ROMName, "slot", r.Slot, "wasMissing", r.Missing)
	}
	a.log.Info("maintenance: states checked", "duration", time.Since(start).Round(time.Millisecond),
		"resumeArchived", report.Archived, "locksChecked", report.LocksChecked, "locksIntact", report.LocksIntact,
		"locksRestored", len(report.Restored), "failed", report.Failed, "skipped", report.Skipped)
	if stopped() {
		a.log.Info("maintenance: stopped by the shutdown, the rest is checked at the next start")
		return ErrStopped
	}
	if report.Skipped > 0 {
		a.log.Warn("maintenance: time limit reached, the rest is checked at the next start", "limit", MaintenanceTimeLimit, "skipped", report.Skipped)
	}
	if freed, err := a.trash.Purge(store.TrashRetention); err != nil {
		return fmt.Errorf("maintenance: %w", err)
	} else if freed > 0 {
		a.log.Info("trash purged", "freedBytes", freed)
	}
	if stopped() {
		return ErrStopped
	}
	if err := a.xfer.Maintain(); err != nil {
		return fmt.Errorf("maintenance: %w", err)
	}
	if stopped() {
		return ErrStopped
	}
	if err := a.xfer.LoadUploads(); err != nil {
		return fmt.Errorf("maintenance: %w", err)
	}
	return nil
}

// Start opens the server on the first free port from the preferred one,
// serves the API in a goroutine and announces it with Bonjour. ip is the
// address put in the pairing link. A goroutine then follows the address of
// the Wi-Fi interface (see watchNetwork). ErrStopped once Shutdown began.
func (a *App) Start(ip string) error {
	a.life.Lock()
	defer a.life.Unlock()
	if a.stopping {
		return ErrStopped
	}
	port := a.conf.Port
	if a.opts.Port != 0 {
		port = a.opts.Port
	}
	ln, port, err := server.Listen("0.0.0.0", port)
	if err != nil {
		return err
	}
	srv, err := server.New(server.Options{
		Identity:  server.Identity{DeviceID: a.deviceID, DeviceName: a.DeviceName(), PakVersion: a.opts.Version},
		Adapter:   a.adapter,
		Library:   a.lib,
		Clients:   a.clients,
		Pairing:   a.pairing,
		Trash:     a.trash,
		Transfers: a.xfer,
		Saves:     a.saves,
		States:    a.states,
		Cheats:    a.cheats,
		Verifier:  auth.NewVerifier(a.clients, nil, 0, a.log),
		Logger:    a.log,
		OnPaired:  a.paired,
		OnChange:  a.notices.change,
		LogFull:   a.logf.full,
		// HTTPS only (M13).
		TLS: a.tls.ServerConfig(),
	})
	if err != nil {
		ln.Close()
		return err
	}
	a.mu.Lock()
	a.ip, a.wifi = ip, true
	a.mu.Unlock()
	a.srv, a.port = srv, port
	a.syncPairing()
	a.log.Info("listening (HTTPS)", "address", ln.Addr().String(), "ip", ip, "port", port, "pin", a.tls.Pin)
	if err := a.reannounce(); err != nil {
		ln.Close()
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.served = make(chan struct{})
	go func() {
		a.serveErr = srv.Serve(ctx, ln)
		close(a.served)
	}()
	a.stopWatch, a.watchDone = make(chan struct{}), make(chan struct{})
	go a.watchNetwork()
	return nil
}

// Stopped is closed when the server stops, on Shutdown or on its own
// after an error (nil before Start).
func (a *App) Stopped() <-chan struct{} { return a.served }

// Port returns the port the API listens on (0 before Start).
func (a *App) Port() int { return a.port }

// IP returns the address put in the pairing link ("" before Start and
// while offline).
func (a *App) IP() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ip
}

// paired is the OnPaired callback of the server.
func (a *App) paired(c auth.Client) {
	a.syncPairing()
	a.notices.push(Notice{Kind: NoticePaired, Name: c.Name})
	if a.opts.OnPaired != nil {
		a.opts.OnPaired(c)
	}
}

// reannounce replaces the Bonjour announcement with one for the current
// name (05 §7.2: the name of the console is the Bonjour instance).
func (a *App) reannounce() error {
	a.annMu.Lock()
	defer a.annMu.Unlock()
	if a.ann != nil {
		a.ann.Shutdown()
		a.ann = nil
	}
	osv := ""
	if a.info.OSVersion != nil {
		osv = *a.info.OSVersion
	}
	name := a.DeviceName()
	ann, err := a.announce(name, a.port, discovery.Info{
		DeviceID: a.deviceID, APIVersion: server.APIVersion, Model: a.info.Model, Platform: a.info.Platform,
		OS: a.info.OS, OSVersion: osv, PakVersion: a.opts.Version,
	})
	if err != nil {
		return err
	}
	a.ann, a.annFailing = ann, false
	a.log.Info("announced", "service", discovery.ServiceType, "instance", discovery.InstanceName(name))
	return nil
}

// withdraw removes the Bonjour announcement.
func (a *App) withdraw() {
	a.annMu.Lock()
	defer a.annMu.Unlock()
	if a.ann != nil {
		a.ann.Shutdown()
		a.ann = nil
	}
}

// Shutdown stops the pak (05 §2.6); the event streams get a shutdown
// event with the reason "stopped" (see Quit). It is also the SIGTERM path: when
// NextUI powers off, procd waits about one second before SIGKILL and the
// network is already down, so what protects the data comes first:
//
//  1. a running maintenance is cancelled and waited for (it stops between
//     two files) and Start is refused (ErrStopped);
//  2. new requests get 503 shutting_down, and the event streams get the
//     shutdown event (it is only queued: the streams write it while the
//     next steps run, before the HTTP server stops);
//  3. the trash credit clock is saved and the SD card is synced: what is
//     written is on the card;
//  4. then the rest, which a SIGKILL may cut without harm (every write is
//     atomic and resumed at the next start): the network watch and the
//     announcement stop (the Bonjour goodbye never waits for the requests
//     nor the event streams), the running requests get up to
//     server.ShutdownTimeout, the running job is cancelled (its
//     partial file deleted), and the card is synced again.
//
// It returns the error of the server, if any. Safe to call more than once
// and from several goroutines: the later calls wait for the first one.
func (a *App) Shutdown() error { return a.shutdown(events.ReasonStopped) }

// Quit is Shutdown when the user leaves ReminisLink on the console: the
// event streams get the shutdown event with the reason "quit".
func (a *App) Quit() error { return a.shutdown(events.ReasonQuit) }

// shutdown is Shutdown with the reason of the shutdown event; the first
// call decides it.
func (a *App) shutdown(reason string) error {
	a.stopOnce.Do(func() {
		a.maintCancel()
		a.maintMu.Lock() // waits for a running maintenance
		a.maintMu.Unlock()
		a.life.Lock()
		a.stopping = true
		a.life.Unlock()

		if a.srv != nil {
			a.srv.BeginShutdown()
			a.srv.CloseEvents(reason)
		}
		a.stopCredit()
		syscall.Sync()

		// The watch first: it could announce again.
		if a.stopWatch != nil {
			close(a.stopWatch)
			<-a.watchDone
		}
		a.withdraw()
		if a.cancel != nil {
			a.cancel()
			<-a.served
			a.stopErr = a.serveErr
		}
		a.xfer.Close()
		syscall.Sync()
		a.log.Info("ReminisLink stopped")
	})
	return a.stopErr
}

// Close closes the log file. Call it last.
func (a *App) Close() error { return a.logf.f.Close() }

// Busy reports whether a transfer or a job is in progress (05 §7.3).
func (a *App) Busy() bool { return a.xfer.Busy() }

// selfPakDir is the folder of the running executable: the pak folder of
// ReminisLink on the console (launch.sh runs ./reminislink from it), a
// build folder elsewhere ("" when unknown).
func selfPakDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Dir(exe)
}
