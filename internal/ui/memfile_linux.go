//go:build linux

package ui

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// mfdCloexec is MFD_CLOEXEC of memfd_create(2).
const mfdCloexec = 0x0001

// linuxMemFile is an anonymous memory file (memfd_create, Linux 3.17+;
// the Brick runs 4.9): SDL opens it again through /proc/self/fd/<n>.
type linuxMemFile struct{ f *os.File }

func newMemFile(data []byte) (memFile, error) {
	name, err := syscall.BytePtrFromString("reminislink-qr")
	if err != nil {
		return nil, err
	}
	fd, _, errno := syscall.Syscall(sysMemfdCreate, uintptr(unsafe.Pointer(name)), mfdCloexec, 0)
	if errno != 0 {
		return nil, fmt.Errorf("%w: memfd_create: %v", errNoMemoryFile, errno)
	}
	f := os.NewFile(fd, "memfd:reminislink-qr")
	if _, err := f.Write(data); err != nil {
		f.Close()
		return nil, err
	}
	return &linuxMemFile{f: f}, nil
}

func (m *linuxMemFile) Path() string { return fmt.Sprintf("/proc/self/fd/%d", m.f.Fd()) }

// Release keeps the file: it is in memory only, and a redraw of the same
// screen opens it again.
func (m *linuxMemFile) Release() {}

func (m *linuxMemFile) Close() error { return m.f.Close() }
