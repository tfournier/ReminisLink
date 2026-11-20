// ReminisLink - the pak of the ReminisLink iPhone companion app. It serves
// the HTTP API on the local Wi-Fi network and announces itself with
// Bonjour (docs/specs/05-console-pak.md).
//
// Without option it shows the gabagool UI on the console; --desktop shows
// the same UI in a window against a fake SD card; --headless runs the
// server and the announcement only, without UI nor SDL.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/tfournier/ReminisLink/internal/app"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/server"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// options are the command line options.
type options struct {
	headless bool   // server and Bonjour only, no UI
	desktop  bool   // UI in a window on a fake SD card
	sd       string // SD card root; empty: NextUI environment variables
	port     int    // preferred port; 0: config.json
	name     string // device name for this run; empty: config.json
	// pairingOpen keeps pairing open in headless mode, even with paired
	// devices (default: open while no device is paired).
	pairingOpen bool
}

var errUsage = errors.New("usage")

func parseFlags(args []string, stderr io.Writer) (options, error) {
	var o options
	fs := flag.NewFlagSet("reminislink", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&o.headless, "headless", false, "run the server and the Bonjour announcement only, without UI")
	fs.BoolVar(&o.desktop, "desktop", false, "run the UI in a window against a fake SD card (needs --sd)")
	fs.StringVar(&o.sd, "sd", "", "SD card root (default: the NextUI environment variables)")
	fs.IntVar(&o.port, "port", 0, fmt.Sprintf("preferred port (default: config.json, then %d)", server.DefaultPort))
	fs.StringVar(&o.name, "name", "", "device name for this run (default: config.json)")
	fs.BoolVar(&o.pairingOpen, "pairing-open", false, "headless: keep pairing open even when a device is already paired (default: open while no device is paired)")
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
	if o.headless && o.desktop {
		fmt.Fprintln(stderr, "--headless and --desktop cannot be used together")
		return o, errUsage
	}
	if o.pairingOpen && !o.headless {
		fmt.Fprintln(stderr, "--pairing-open needs --headless (the UI opens pairing from its screens)")
		return o, errUsage
	}
	if o.desktop && o.sd == "" {
		fmt.Fprintln(stderr, "--desktop needs --sd (a copy of testdata/sdcard, see make desktop)")
		return o, errUsage
	}
	return o, nil
}

func main() {
	opts, err := parseFlags(os.Args[1:], os.Stderr)
	if err != nil {
		os.Exit(2)
	}
	if opts.headless {
		err = runHeadless(opts)
	} else {
		err = runUI(opts)
	}
	if err != nil {
		// Once the log is open, the error is logged (file and stderr):
		// print it only when it was not.
		var logged app.LoggedError
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

// appOptions are the options of the pak for a mode.
func appOptions(opts options, mode string) app.Options {
	return app.Options{
		Roots: roots(opts), Env: os.Getenv, Version: version, Mode: mode,
		Port: opts.port, Name: opts.name,
	}
}

func runHeadless(opts options) error {
	return runHeadlessTo(opts, os.Stderr)
}

// runHeadlessTo runs the headless mode; the log also goes to logOut. The
// pairing link and the QR code go to stdout only, never to the
// log. A fatal error is written to the log file and returned as an
// app.LoggedError.
func runHeadlessTo(opts options, logOut io.Writer) error {
	display := newPairingDisplay(os.Stdout, nil)
	ao := appOptions(opts, "headless")
	ao.LogOut = logOut
	ao.OnPaired = display.paired
	// No screen: pairing opens on its own (05 §7.1 keeps it on demand on
	// the console).
	ao.Pairing = app.PairingWhileUnpaired
	if opts.pairingOpen {
		ao.Pairing = app.PairingAlways
	}
	// Without Wi-Fi the headless mode (tests, development) keeps serving on
	// the loopback address.
	ao.NoWiFiIP = "127.0.0.1"
	a, err := app.Open(ao)
	if err != nil {
		return err
	}
	defer a.Close()

	// Signals are handled from here, the maintenance included: Shutdown
	// cancels it. The default handling comes back once the shutdown
	// starts: a second Ctrl-C kills the pak instead of waiting.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		stop()
		a.Shutdown()
	}()
	fail := func(err error) error {
		if errors.Is(err, app.ErrStopped) {
			return a.Shutdown()
		}
		a.Logger().Error("fatal", "error", err)
		a.Shutdown()
		return app.LoggedError{Err: err}
	}
	if err := a.Maintain(); err != nil {
		return fail(err)
	}
	ip, ok := a.WiFiIPv4()
	if !ok {
		// The UI shows "No Wi-Fi" instead; headless is for tests.
		a.Logger().Warn("no IPv4 address found, the pairing link uses 127.0.0.1")
		ip = ao.NoWiFiIP
	}
	if err := a.Start(ip); err != nil {
		return fail(err)
	}
	display.setStatus(a.Status)
	go display.run(ctx)
	select {
	case <-ctx.Done():
	case <-a.Stopped():
	}
	return a.Shutdown()
}
