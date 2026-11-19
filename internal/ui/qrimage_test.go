package ui

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
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
	if runtime.GOOS == "linux" {
		// A memory file: no name in any file system.
		if !strings.HasPrefix(p, "/proc/self/fd/") {
			t.Fatalf("path %q is not a memory file", p)
		}
		target, _ := os.Readlink(p)
		if !strings.HasPrefix(target, "/memfd:") {
			t.Fatalf("%s points to %q, not a memfd", p, target)
		}
	} else {
		// Elsewhere the file only exists until the screen loaded it.
		q.release()
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("file %q kept after release: %v", p, err)
		}
		if again, err := q.path(); err != nil || again == "" {
			t.Fatalf("path after release: %q %v", again, err)
		}
	}
	q.close()
	if _, err := os.Stat(p); !os.IsNotExist(err) && runtime.GOOS != "linux" {
		t.Fatalf("file %q kept after close", p)
	}
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "reminislink-qr-*"))
	if len(matches) != 0 {
		t.Fatalf("QR files left in the temporary directory: %v", matches)
	}
}
