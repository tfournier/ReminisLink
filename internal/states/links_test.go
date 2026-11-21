package states

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
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
	if _, err := e.svc.Thumbnail(g, 0); err != nil {
		t.Fatalf("thumbnail before its folder is linked: %v", err)
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
