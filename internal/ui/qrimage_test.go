package ui

import (
	"bytes"
	"errors"
	"image/png"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestQRImageStaysOffDisk(t *testing.T) {
	data, err := QRPNG("reminislink://pair?v=1&s=secret")
	if err != nil {
		t.Fatal(err)
	}
	q := &qrImage{data: append([]byte(nil), data...)}
	p, err := q.path()
	if runtime.GOOS != "linux" {
		// No memory file: no image at all (the PIN only), nothing on disk.
		if !errors.Is(err, errNoMemoryFile) || p != "" {
			t.Fatalf("path %q, %v", p, err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	read, err := os.ReadFile(p)
	if err != nil || !bytes.Equal(read, data) {
		t.Fatalf("image at %q: %v", p, err)
	}
	if _, err := png.Decode(bytes.NewReader(read)); err != nil {
		t.Fatal(err)
	}
	// A memory file: no name in any file system.
	if !strings.HasPrefix(p, "/proc/self/fd/") {
		t.Fatalf("path %q is not a memory file", p)
	}
	target, _ := os.Readlink(p)
	if !strings.HasPrefix(target, "/memfd:") {
		t.Fatalf("%s points to %q, not a memfd", p, target)
	}
	q.close()
}
