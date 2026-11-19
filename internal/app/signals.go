package app

import "os"

// ExitOnSignal waits for the first value of sigs (SIGTERM or SIGINT, see
// signal.Notify), then runs Shutdown, cleanup (may be nil: e.g. deleting
// the QR code image, which holds the pairing secret) and Close, and calls
// exit(0). Used by the UI, whose screens cannot be interrupted: the
// process ends without waiting for the screen in front.
//
// Keep sigs registered with signal.Notify until the process exits: a
// second signal during the shutdown (or during "Closing..." on the UI
// side) is then absorbed instead of killing the pak in the middle; the
// Shutdown started by the UI and this one are the same (it runs once).
// It works at any time: during the maintenance, Shutdown cancels it.
// stop ends the wait without doing anything (closing it is optional).
func (a *App) ExitOnSignal(sigs <-chan os.Signal, stop <-chan struct{}, cleanup func(), exit func(int)) {
	select {
	case <-stop:
		return
	case sig := <-sigs:
		a.log.Info("signal received, closing", "signal", sig.String())
	}
	a.Shutdown()
	if cleanup != nil {
		cleanup()
	}
	a.Close()
	exit(0)
}
