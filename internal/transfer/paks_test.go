package transfer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfournier/ReminisLink/internal/paks"
)

// stagePak writes a checked pak folder into a new staging folder.
func (e *env) stagePak(name, launch string) paks.Staged {
	e.t.Helper()
	stage, err := e.svc.newPakStaging()
	if err != nil {
		e.t.Fatal(err)
	}
	dir := filepath.Join(stage, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "launch.sh"), []byte(launch), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return paks.Staged{Dir: dir, Name: name}
}

// A failed rename after the old pak went to the trash puts it back.
func TestPlacePakRollsBackWhenTheRenameFails(t *testing.T) {
	e := newEnv(t)
	old := filepath.Join(e.sd, "Tools", "tg5040", "Clock.pak")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "launch.sh"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	staged := e.stagePak("Clock.pak", "new")
	e.svc.rename = func(string, string) error { return errors.New("card removed") }
	_, apiErr := e.svc.placePak(context.Background(), staged, paks.KindTool, PakConflictReplace)
	if apiErr == nil || apiErr.Code != CodeInternal {
		t.Fatalf("placePak: %v", apiErr)
	}
	if got, err := os.ReadFile(filepath.Join(old, "launch.sh")); err != nil || string(got) != "old" {
		t.Fatalf("old pak not put back: %q %v", got, err)
	}
	if entries, _, _ := e.trash.List(); len(entries) != 0 {
		t.Fatalf("trash %+v", entries)
	}
}

// The staging folders of a previous run are deleted at start.
func TestMaintainDeletesPakStaging(t *testing.T) {
	e := newEnv(t)
	staged := e.stagePak("Left.pak", "x")
	stage := filepath.Dir(staged.Dir)
	if err := e.svc.Maintain(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("left: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(stage), pakStagingPrefix) {
		t.Fatal(stage)
	}
}

// A job cancelled before its install installs nothing.
func TestPlacePakCancelled(t *testing.T) {
	e := newEnv(t)
	staged := e.stagePak("Clock.pak", "x")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, apiErr := e.svc.placePak(ctx, staged, paks.KindTool, PakConflictFail); apiErr == nil || apiErr.Code != CodeShuttingDown {
		t.Fatalf("cancelled: %v", apiErr)
	}
	if _, err := os.Stat(filepath.Join(e.sd, "Tools", "tg5040", "Clock.pak")); !os.IsNotExist(err) {
		t.Fatal("installed")
	}
}
