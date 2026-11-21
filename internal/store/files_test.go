package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCopyAndHash(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "sub", "b")
	sum, n, err := CopyFileAtomic(src, dst)
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if err != nil || n != 5 || sum != want {
		t.Fatalf("copy: %s %d %v", sum, n, err)
	}
	if h, n, err := HashFile(dst); err != nil || h != want || n != 5 {
		t.Fatalf("hash: %s %d %v", h, n, err)
	}
	if _, err := os.Stat(dst + ".tmp"); err == nil {
		t.Fatal("temporary file left")
	}
	if _, _, err := CopyFileAtomic(filepath.Join(dir, "missing"), dst); err == nil {
		t.Fatal("copy of a missing file")
	}
	same, err := SameFile(src, dst)
	if err != nil || !same {
		t.Fatalf("same: %v %v", same, err)
	}
	os.WriteFile(dst, []byte("hellO"), 0o644)
	if same, _ := SameFile(src, dst); same {
		t.Fatal("different content")
	}
	if same, err := SameFile(src, filepath.Join(dir, "missing")); same || err != nil {
		t.Fatalf("missing: %v %v", same, err)
	}
	os.WriteFile(filepath.Join(dir, "x.tmp"), nil, 0o644)
	if RemoveTemps(dir) != 1 {
		t.Fatal("RemoveTemps")
	}
}

func TestStampIDAndFindFold(t *testing.T) {
	at := time.Date(2026, 11, 18, 17, 0, 0, 0, time.UTC)
	taken := map[string]bool{}
	// A fixed random source repeats its suffix: the check finds another.
	src := bytes.NewReader(append(bytes.Repeat([]byte{0xab, 0xcd}, 3), 0x01, 0x02))
	for i := 0; i < 2; i++ {
		id, err := NewStampID("bk", at, src, func(id string) bool { return taken[id] })
		if err != nil {
			t.Fatal(err)
		}
		taken[id] = true
	}
	if !taken["bk_20261118T170000Z_abcd"] || !taken["bk_20261118T170000Z_0102"] {
		t.Fatalf("ids %v", taken)
	}
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "Game.GBA"), 0o755)
	os.WriteFile(filepath.Join(dir, "Slot1.st"), nil, 0o644)
	// Another case is found where the file system ignores case (exFAT,
	// APFS); on a case-sensitive one it is another entry
	// (TestFindFoldCaseSensitive).
	if _, err := os.Lstat(filepath.Join(dir, "GAME.GBA")); err == nil {
		if FindFold(dir, "game.gba", true) != filepath.Join(dir, "Game.GBA") || FindFold(dir, "game.gba", false) != "" {
			t.Fatal("folder")
		}
		if FindFold(dir, "slot1.st", false) != filepath.Join(dir, "Slot1.st") {
			t.Fatal("file")
		}
	}
	if FindFold(dir, "Game.GBA", true) != filepath.Join(dir, "Game.GBA") || FindFold(dir, "Slot1.st", false) != filepath.Join(dir, "Slot1.st") || FindFold(dir, "nope", false) != "" {
		t.Fatal("exact")
	}
}

// On a case-sensitive file system (Knulli on ext4, M20), an entry of
// another case is another file: FindFold does not take it.
func TestFindFoldCaseSensitive(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "game.gba"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "GAME.GBA")); err == nil {
		// Case-insensitive (macOS): the entry of another case is the one
		// the file system opens.
		if got := FindFold(dir, "Game.gba", false); got != filepath.Join(dir, "game.gba") {
			t.Fatalf("case-insensitive: %q", got)
		}
		return
	}
	if got := FindFold(dir, "Game.gba", false); got != "" {
		t.Fatalf("case-sensitive: %q", got)
	}
	if err := os.Mkdir(filepath.Join(dir, "Game.gba"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindFold(dir, "Game.gba", true); got != filepath.Join(dir, "Game.gba") {
		t.Fatalf("exact folder: %q", got)
	}
}
