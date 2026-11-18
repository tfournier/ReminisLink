// ReminisLink - the pak of the ReminisLink iPhone companion app. It serves
// the HTTP API on the local Wi-Fi network and announces itself with
// Bonjour (docs/specs/05-console-pak.md).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/device"
	"github.com/tfournier/ReminisLink/internal/discovery"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/server"
	"github.com/tfournier/ReminisLink/internal/store"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// LogFileName is the log file in $LOGS_PATH.
const LogFileName = "ReminisLink.txt"

// options are the command line options.
type options struct {
	headless bool   // server and Bonjour only, no UI
	desktop  bool   // UI in a window on a fake SD card (milestone M7)
	sd       string // SD card root; empty: NextUI environment variables
	port     int    // preferred port; 0: config.json
	name     string // device name for this run; empty: config.json
}

var errUsage = errors.New("usage")

func parseFlags(args []string, stderr io.Writer) (options, error) {
	var o options
	fs := flag.NewFlagSet("reminislink", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&o.headless, "headless", false, "run the server and the Bonjour announcement only, without UI")
	fs.BoolVar(&o.desktop, "desktop", false, "run the UI in a window against a fake SD card (not implemented yet)")
	fs.StringVar(&o.sd, "sd", "", "SD card root (default: the NextUI environment variables)")
	fs.IntVar(&o.port, "port", 0, fmt.Sprintf("preferred port (default: config.json, then %d)", server.DefaultPort))
	fs.StringVar(&o.name, "name", "", "device name for this run (default: config.json)")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected argument %q\n", fs.Arg(0))
		return o, errUsage
	}
	if o.port < 0 || o.port > 65535 {
		fmt.Fprintf(stderr, "invalid port %d\n", o.port)
		return o, errUsage
	}
	return o, nil
}

func main() {
	opts, err := parseFlags(os.Args[1:], os.Stderr)
	if err != nil {
		os.Exit(2)
	}
	switch {
	case opts.desktop:
		fmt.Fprintln(os.Stderr, "reminislink: --desktop is not implemented yet (milestone M7); use --headless")
		os.Exit(2)
	case !opts.headless:
		fmt.Fprintln(os.Stderr, "reminislink: the on-device UI is not implemented yet (milestone M7); use --headless")
		os.Exit(2)
	}
	if err := runHeadless(opts); err != nil {
		// Once the log is open, the error is logged (file and stderr):
		// print it only when it was not.
		var logged loggedError
		if !errors.As(err, &logged) {
			fmt.Fprintln(os.Stderr, "reminislink:", err)
		}
		os.Exit(1)
	}
}

func roots(opts options) osadapter.Roots {
	if opts.sd != "" {
		return nextui.RootsForSD(opts.sd, os.Getenv("PLATFORM"))
	}
	return nextui.RootsFromEnv(os.Getenv)
}

// Size limits of the log file on the SD card.
const (
	// LogRotateBytes: a larger log file is renamed LogOldFileName at launch.
	LogRotateBytes = 5 << 20
	// LogCapBytes: past this size during a run, the sampled lines
	// (unsigned and rejected requests) are no longer written.
	LogCapBytes = 20 << 20
	// LogOldFileName keeps the previous log file.
	LogOldFileName = "ReminisLink.old.txt"
)

// logFile is the log file and its size, counted as it is written.
type logFile struct {
	f    *os.File
	size atomic.Int64
}

func (l *logFile) Write(p []byte) (int, error) {
	n, err := l.f.Write(p)
	l.size.Add(int64(n))
	return n, err
}

// full reports that the file reached LogCapBytes.
func (l *logFile) full() bool { return l.size.Load() >= LogCapBytes }

// openLog sends slog to $LOGS_PATH/ReminisLink.txt, and to also (stderr in
// headless mode). A file larger than LogRotateBytes is first renamed
// ReminisLink.old.txt (replacing the previous one).
func openLog(dir string, also io.Writer) (*slog.Logger, *logFile, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create log folder: %w", err)
	}
	path := filepath.Join(dir, LogFileName)
	if fi, err := os.Stat(path); err == nil && fi.Size() > LogRotateBytes {
		if err := os.Rename(path, filepath.Join(dir, LogOldFileName)); err != nil {
			return nil, nil, fmt.Errorf("rotate log: %w", err)
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("open log: %w", err)
	}
	lf := &logFile{f: f}
	if fi, err := f.Stat(); err == nil {
		lf.size.Store(fi.Size())
	}
	var w io.Writer = lf
	if also != nil {
		w = io.MultiWriter(lf, also)
	}
	return slog.New(slog.NewTextHandler(w, nil)), lf, nil
}

func runHeadless(opts options) error {
	return runHeadlessTo(opts, os.Stderr)
}

// loggedError is an error already written to the log (and to stderr in
// headless mode).
type loggedError struct{ err error }

func (e loggedError) Error() string { return e.err.Error() }
func (e loggedError) Unwrap() error { return e.err }

// runHeadlessTo runs the headless mode; the log also goes to logOut. A
// fatal error is written to the log file before it is closed.
func runHeadlessTo(opts options, logOut io.Writer) (err error) {
	r := roots(opts)
	logger, logf, err := openLog(r.Logs, logOut)
	if err != nil {
		return err
	}
	defer logf.f.Close()
	defer func() {
		if err != nil {
			logger.Error("fatal", "error", err)
			err = loggedError{err}
		}
	}()
	slog.SetDefault(logger)

	adapter := nextui.New(r, os.Getenv)
	info, err := adapter.DeviceInfo()
	if err != nil {
		return err
	}
	layout := store.Layout{Root: r.Data}
	conf, err := store.LoadConfig(layout.ConfigFile(), func(id string) string {
		return nextui.DefaultDeviceName(info.Model, id)
	}, nil)
	if err != nil {
		return err
	}
	name := conf.DeviceName
	if opts.name != "" {
		name = opts.name
	}
	port := conf.Port
	if opts.port != 0 {
		port = opts.port
	}
	slog.Info("ReminisLink started", "version", version, "mode", "headless", "sd", r.SD, "device", conf.DeviceID,
		"name", name, "model", info.Model, "platform", info.Platform)

	ip, ok := device.IPv4(info.WirelessInterface)
	if !ok {
		// On the console the UI will refuse to start without Wi-Fi (M7).
		slog.Warn("no IPv4 address found, the pairing link uses 127.0.0.1")
		ip = "127.0.0.1"
	}

	clients, err := auth.OpenClientStore(layout.ClientsFile())
	if err != nil {
		return err
	}
	pairing, err := auth.NewPairingSession(conf.DeviceID, nil, nil)
	if err != nil {
		return err
	}
	ln, port, err := server.Listen("0.0.0.0", port)
	if err != nil {
		return err
	}
	display := newPairingDisplay(os.Stdout, pairing, conf.DeviceID, name, ip, port)
	srv, err := server.New(server.Options{
		Identity: server.Identity{DeviceID: conf.DeviceID, DeviceName: name, PakVersion: version},
		Adapter:  adapter,
		Library:  library.New(adapter, layout),
		Clients:  clients,
		Pairing:  pairing,
		Verifier: auth.NewVerifier(clients, nil, 0, logger),
		Logger:   logger,
		OnPaired: display.paired,
		LogFull:  logf.full,
	})
	if err != nil {
		ln.Close()
		return err
	}
	slog.Info("listening", "address", ln.Addr().String(), "ip", ip, "port", port)

	osv := ""
	if info.OSVersion != nil {
		osv = *info.OSVersion
	}
	announcement, err := discovery.Announce(name, port, discovery.Info{
		DeviceID: conf.DeviceID, APIVersion: server.APIVersion, Model: info.Model, Platform: info.Platform,
		OS: info.OS, OSVersion: osv, PakVersion: version,
	}, nil)
	if err != nil {
		ln.Close()
		return err
	}
	slog.Info("announced", "service", discovery.ServiceType, "instance", discovery.InstanceName(name))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		// Restore the default signal handling once the shutdown starts: a
		// second Ctrl-C kills the pak instead of waiting for the shutdown.
		<-ctx.Done()
		stop()
	}()
	go display.run(ctx)

	err = srv.Serve(ctx, ln)
	announcement.Shutdown()
	syscall.Sync()
	slog.Info("ReminisLink stopped")
	return err
}
