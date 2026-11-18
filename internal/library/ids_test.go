package library

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNFCAndNFDGiveDifferentIDs(t *testing.T) {
	nfc := EncodeID("Café.gb")
	nfd := EncodeID("Café.gb")
	if nfc == nfd {
		t.Fatal("NFC and NFD spellings must not be merged")
	}
}

func TestDecodeIDTooLong(t *testing.T) {
	id := EncodeID(strings.Repeat("a", MaxIDPathBytes+1))
	if _, err := DecodeID(id); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("error = %v, want ErrInvalidID", err)
	}
	if _, err := DecodeID(EncodeID(strings.Repeat("a", MaxIDPathBytes))); err != nil {
		t.Fatalf("path of MaxIDPathBytes refused: %v", err)
	}
}

func TestDecodeSystemID(t *testing.T) {
	if got, err := DecodeSystemID(EncodeID("Game Boy Advance (GBA)")); err != nil || got != "Game Boy Advance (GBA)" {
		t.Fatalf("DecodeSystemID = %q, %v", got, err)
	}
	if _, err := DecodeSystemID(EncodeID("Game Boy Advance (GBA)/x.gba")); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("error = %v, want ErrInvalidID", err)
	}
	if _, err := DecodeSystemID("%%%"); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("error = %v, want ErrInvalidID", err)
	}
}

func TestResolveIDInsideRoot(t *testing.T) {
	root := t.TempDir()
	sys := filepath.Join(root, "Game Boy (GB)")
	if err := os.MkdirAll(sys, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sys, "a.gb"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"Game Boy (GB)/a.gb", "Game Boy (GB)/missing/new.gb", "Game Boy (GB)"} {
		got, err := ResolveID(root, EncodeID(rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if want := filepath.Join(root, filepath.FromSlash(rel)); got != want {
			t.Fatalf("%s: got %s, want %s", rel, got, want)
		}
	}
}

func TestResolveIDRefusesSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "Roms")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{root, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.sav"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "Evil (EV)")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	for _, rel := range []string{"Evil (EV)", "Evil (EV)/secret.sav", "Evil (EV)/new/file.gb"} {
		if _, err := ResolveID(root, EncodeID(rel)); !errors.Is(err, ErrInvalidID) {
			t.Fatalf("%s: error = %v, want ErrInvalidID", rel, err)
		}
	}
}

func TestResolveIDWithSymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "GB (GB)"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	if _, err := ResolveID(link, EncodeID("GB (GB)/x.gb")); err != nil {
		t.Fatalf("a symlinked root must work: %v", err)
	}
}

func FuzzDecodeID(f *testing.F) {
	f.Add(EncodeID("Game Boy (GB)/a.gb"))
	f.Add(EncodeID("../x"))
	f.Add("R2FtZQ==")
	f.Fuzz(func(t *testing.T, id string) {
		p, err := DecodeID(id)
		if err != nil {
			return
		}
		if strings.HasPrefix(p, "/") || strings.Contains(p, "\x00") || strings.Contains(p, "\\") {
			t.Fatalf("unsafe path accepted: %q", p)
		}
		for _, part := range strings.Split(p, "/") {
			if part == ".." || part == "." || part == "" {
				t.Fatalf("unsafe component in %q", p)
			}
		}
		if EncodeID(p) != id {
			t.Fatalf("non-canonical id accepted: %q", id)
		}
	})
}

func TestResolveIDThroughAFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "GB (GB)"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "GB (GB)", "x.gb"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// ENOTDIR is "does not exist", not an unexpected error.
	got, err := ResolveID(root, EncodeID("GB (GB)/x.gb/y"))
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if want := filepath.Join(root, "GB (GB)", "x.gb", "y"); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestResolveIDErrorsDoNotLeakPaths(t *testing.T) {
	root := t.TempDir()
	loop := filepath.Join(root, "Loop (LP)")
	if err := os.Symlink(loop, loop); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	_, err := ResolveID(root, EncodeID("Loop (LP)/x.gb"))
	if !errors.Is(err, ErrInvalidID) {
		t.Fatalf("error = %v, want ErrInvalidID", err)
	}
	if strings.Contains(err.Error(), root) {
		t.Fatalf("error leaks the absolute path: %v", err)
	}

	missing := filepath.Join(t.TempDir(), "missing-root")
	_, err = ResolveID(missing, EncodeID("GB (GB)/x.gb"))
	if !errors.Is(err, ErrResolveRoot) || errors.Is(err, ErrInvalidID) {
		t.Fatalf("error = %v, want ErrResolveRoot", err)
	}
	if strings.Contains(err.Error(), missing) {
		t.Fatalf("error leaks the absolute path: %v", err)
	}
}
