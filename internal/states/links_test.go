package states

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfournier/ReminisLink/internal/confine"
)

// Codex review of M20: a state folder reached through a symbolic link is
// neither read nor written (rule 2); the states are reported missing.
func TestStatesFolderThroughALink(t *testing.T) {
	e := newEnv(t)
	g := e.game("Starlight Courier.gba")
	dir := filepath.Join(e.sd, ".userdata", "shared", "GBA-gpsp")
	outside := filepath.Join(filepath.Dir(e.sd), "elsewhere")
	if err := os.Rename(dir, outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, dir); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}
	if _, _, err := e.svc.Open(g, 0); !errors.Is(err, ErrNoState) {
		t.Fatalf("state read through a link: %v", err)
	}
	// The descriptor served is the one checked, from its first byte.
	f, p, err := e.svc.OpenThumbnail(g, 0)
	if err != nil {
		t.Fatalf("thumbnail before its folder is linked: %v", err)
	}
	served, _ := io.ReadAll(f)
	f.Close()
	if want, _ := os.ReadFile(p); string(served) != string(want) || !strings.HasPrefix(string(served), pngSignature) {
		t.Fatal("OpenThumbnail does not serve the whole PNG")
	}
	minui := filepath.Join(e.sd, ".userdata", "shared", ".minui", "GBA")
	if err := os.Rename(minui, outside+"-minui"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside+"-minui", minui); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Thumbnail(g, 0); err == nil {
		t.Fatal("thumbnail through a link")
	}
	before, _ := os.ReadDir(outside)
	if _, err := e.svc.Lock(g, 0); err == nil {
		t.Error("lock of a state through a link")
	}
	if after, _ := os.ReadDir(outside); len(after) != len(before) {
		t.Errorf("files written through the link: %d -> %d", len(before), len(after))
	}
}

// Codex review round 2: a lock folder (or a folder above it in the
// ReminisLink data area) replaced by a link: locking writes nothing there.
func TestLockFolderThroughALink(t *testing.T) {
	e := newEnv(t)
	g := e.game("Starlight Courier.gba") // has a state in slot 0
	outside := filepath.Join(filepath.Dir(e.sd), "elsewhere")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	lockDir := filepath.Join(e.layout.LocksDir("GBA"), g.ROMName)
	if err := os.MkdirAll(filepath.Dir(lockDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, lockDir); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}
	if _, err := e.svc.Lock(g, 0); !errors.Is(err, confine.ErrOutside) {
		t.Fatalf("locked through a link: %v", err)
	}
	if des, _ := os.ReadDir(outside); len(des) != 0 {
		t.Fatalf("written through the link: %v", des)
	}
	// The same with the locks folder of the tag.
	if err := os.Remove(lockDir); err != nil {
		t.Fatal(err)
	}
	tagDir := e.layout.LocksDir("GBA")
	if err := os.Remove(tagDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, tagDir); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Lock(g, 0); !errors.Is(err, confine.ErrOutside) {
		t.Fatalf("locked through a linked tag folder: %v", err)
	}
	if des, _ := os.ReadDir(outside); len(des) != 0 {
		t.Fatalf("written through the link: %v", des)
	}
}
