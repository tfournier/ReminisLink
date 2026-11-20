//go:build !linux

package ui

// Off Linux (the --desktop mode on macOS) there is no memory-only file
// SDL_image can open by path, and a temporary file holding the pairing
// secret could stay on the disk after a SIGKILL (Codex review of M8). The
// QR code is then not drawn (no PIN since M13: nothing to pair with).
func newMemFile([]byte) (memFile, error) { return nil, errNoMemoryFile }
