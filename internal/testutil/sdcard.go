// Package testutil holds test helpers shared by the packages of the pak: a
// copy of the fake SD card and a client that signs its requests like the
// app. It is only imported by tests.
package testutil

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// RepoRoot returns the root of the repository.
func RepoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("testutil: cannot locate the source file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// SDCardSource is the fake SD card of the repository (testdata/sdcard).
func SDCardSource() string { return filepath.Join(RepoRoot(), "testdata", "sdcard") }

// CopySDCard copies the fake SD card into a new temporary folder and returns
// its path. The copy is removed at the end of the test.
func CopySDCard(t testing.TB) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "sdcard")
	if err := CopyTree(SDCardSource(), dst); err != nil {
		t.Fatalf("copy fake SD card: %v", err)
	}
	return dst
}

// CopyTree copies the regular files and folders of src into dst.
func CopyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copyFile(p, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// WriteFile creates a file (and its folders) under root for a test.
func WriteFile(t testing.TB, root, rel, content string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
