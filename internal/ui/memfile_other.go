//go:build !linux

package ui

import "os"

// tempMemFile is the fallback off Linux (desktop mode on macOS): the PNG
// is written to a private file of the temporary directory only while the
// screen loads it.
type tempMemFile struct {
	data []byte
	path string
}

func newMemFile(data []byte) (memFile, error) { return &tempMemFile{data: data}, nil }

// Path writes the file (again) and returns its path; "" when it cannot be
// written (the screen then shows no image).
func (m *tempMemFile) Path() string {
	if m.path != "" {
		return m.path
	}
	f, err := os.CreateTemp("", "reminislink-qr-*.png")
	if err != nil {
		return ""
	}
	_, werr := f.Write(m.data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(f.Name())
		return ""
	}
	m.path = f.Name()
	return m.path
}

// Release deletes the file: the screen has loaded it.
func (m *tempMemFile) Release() {
	if m.path != "" {
		os.Remove(m.path)
		m.path = ""
	}
}

func (m *tempMemFile) Close() error {
	m.Release()
	return nil
}
