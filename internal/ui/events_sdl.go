//go:build sdl

package ui

import "github.com/veandco/go-sdl2/sdl"

// pushQuit ends the gabagool screen in front (its loop stops on an SDL
// quit event). SDL_PushEvent may be called from any thread.
func pushQuit() {
	sdl.PushEvent(&sdl.QuitEvent{Type: sdl.QUIT, Timestamp: sdl.GetTicks()})
}

// flushQuit drops a pushed quit event that was not read (the screen ended
// on a button at the same time). Called on the UI thread.
func flushQuit() { sdl.FlushEvent(sdl.QUIT) }
