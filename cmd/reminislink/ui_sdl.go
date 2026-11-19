//go:build sdl

package main

import "github.com/tfournier/ReminisLink/internal/ui"

// runUI runs the gabagool UI: on the console, or in a window with
// --desktop. Linking gabagool also installs the root certificates of
// github.com/BrandonKowalski/certifiable in http.DefaultTransport, used by
// the downloads (decision D1).
func runUI(opts options) error {
	return ui.Run(ui.Options{App: appOptions(opts, "ui"), Desktop: opts.desktop})
}
