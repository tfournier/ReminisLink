package ui

import "errors"

// The QR code image holds the pairing secret, which must never be written
// to a disk (docs/specs/07-securite.md). gabagool's DetailScreen only
// loads images from a path (SDL_image IMG_Load), so the PNG is kept in
// memory and exposed as a path that is not on a disk:
//
//   - on Linux (the console, and the screenshots under Xvfb), an anonymous
//     memory file (memfd_create) opened through /proc/self/fd/<n>: it has
//     no name in any file system and disappears with the process, even
//     after a SIGKILL;
//   - elsewhere (the --desktop mode on macOS), there is no such file: the
//     PNG goes to a private file of the temporary directory only while the
//     screen loads it (see qrImage.release), and is deleted at once. A
//     SIGKILL in that instant can leave it behind; this limit only
//     concerns the development mode on a Mac.

// errNoMemoryFile means no memory-only file can hold the image.
var errNoMemoryFile = errors.New("no memory file for the QR code image")

// qrImage is the PNG of the QR code, in memory.
type qrImage struct {
	data []byte
	mem  memFile // nil until path is called
}

// memFile is a file whose bytes stay in memory (or as close as the OS
// allows) and that SDL can open by path.
type memFile interface {
	Path() string
	// Release ends the moment the path is needed (the image is loaded).
	Release()
	Close() error
}

// path returns a path to the PNG for SDL_image.
func (q *qrImage) path() (string, error) {
	if q.mem == nil {
		m, err := newMemFile(q.data)
		if err != nil {
			return "", err
		}
		q.mem = m
	}
	return q.mem.Path(), nil
}

// release is called once the screen loaded the image.
func (q *qrImage) release() {
	if q.mem != nil {
		q.mem.Release()
	}
}

// close drops the image (the secret changed, or the pak exits).
func (q *qrImage) close() {
	if q.mem != nil {
		q.mem.Close()
		q.mem = nil
	}
	clear(q.data)
}
