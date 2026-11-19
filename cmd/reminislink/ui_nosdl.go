//go:build !sdl

package main

import "errors"

// runUI needs the gabagool UI, built with the "sdl" tag and cgo (make
// build, make desktop). Without it only the headless mode is available.
func runUI(options) error {
	return errors.New("this build has no UI (built without the sdl tag); use --headless")
}
