package transfer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
	installs := 0
	e.trash.SetRename(func(from, to string) error {
		if from == staged.Dir { // the install of the new pak, after the old one moved
			installs++
			return errors.New("card removed")
		}
		return os.Rename(from, to)
	})
	_, apiErr := e.svc.placePak(context.Background(), staged, paks.KindTool, PakConflictReplace)
	if apiErr == nil || apiErr.Code != CodeInternal {
		t.Fatalf("placePak: %v", apiErr)
	}
	if got, err := os.ReadFile(filepath.Join(old, "launch.sh")); err != nil || string(got) != "old" {
		t.Fatalf("old pak not put back: %q %v", got, err)
	}
	if entries, _, _ := e.trash.List(); len(entries) != 0 || installs != 1 {
		t.Fatalf("trash %+v (installs %d)", entries, installs)
	}
}

// Review A: a sync failure after the new pak was renamed into place
// fails the job and puts the old pak back (nothing announced as done).
func TestPlacePakSyncFailureRestoresTheOldPak(t *testing.T) {
	e := newEnv(t)
	tools := filepath.Join(e.sd, "Tools", "tg5040")
	old := filepath.Join(tools, "Clock.pak")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "launch.sh"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	staged := e.stagePak("Clock.pak", "new")
	failed := false
	e.trash.SetSyncDir(func(dir string) error {
		if data, _ := os.ReadFile(filepath.Join(old, "launch.sh")); !failed && dir == tools && string(data) == "new" {
			failed = true
			return errors.New("simulated fsync failure")
		}
		return nil
	})
	if _, apiErr := e.svc.placePak(context.Background(), staged, paks.KindTool, PakConflictReplace); apiErr == nil {
		t.Fatal("fsync failure ignored")
	}
	if data, _ := os.ReadFile(filepath.Join(old, "launch.sh")); !failed || string(data) != "old" {
		t.Fatalf("old pak not back: %q (failed %v)", data, failed)
	}
}

// Codex 1: the folder of the paks reached through a link is refused,
// nothing is written outside the card.
func TestPlacePakRefusesALinkedPakFolder(t *testing.T) {
	e := newEnv(t)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(e.sd, "Tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(e.sd, "Tools", "tg5040")); err != nil {
		t.Skip("no symbolic links here")
	}
	staged := e.stagePak("Clock.pak", "x")
	if _, apiErr := e.svc.placePak(context.Background(), staged, paks.KindTool, PakConflictFail); apiErr == nil {
		t.Fatal("installed through a link")
	}
	if des, _ := os.ReadDir(outside); len(des) != 0 {
		t.Fatalf("written outside the card: %v", des)
	}
}

// Review A: the start keeps the staged pak of a replacement the trash
// still has to finish.
func TestMaintainKeepsThePakOfAPendingReplacement(t *testing.T) {
	e := newEnv(t)
	old := filepath.Join(e.sd, "Tools", "tg5040", "Clock.pak")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	staged := e.stagePak("Clock.pak", "new")
	// Installed, then its folder cannot be synced and the new pak cannot
	// go back: the replacement stays for the next start.
	e.trash.SetRename(func(from, to string) error {
		if to == staged.Dir {
			return errors.New("cannot go back")
		}
		return os.Rename(from, to)
	})
	e.trash.SetSyncDir(func(dir string) error {
		if data, _ := os.ReadFile(filepath.Join(old, "launch.sh")); dir == filepath.Dir(old) && string(data) == "new" {
			return errors.New("fsync")
		}
		return nil
	})
	e.svc.placePak(context.Background(), staged, paks.KindTool, PakConflictReplace)
	if !e.trash.PendingSources()[staged.Dir] {
		t.Fatalf("no pending replacement: %v", e.trash.PendingSources())
	}
	if e.svc.removeStaging(filepath.Dir(staged.Dir)) {
		t.Fatal("the staged pak of a pending replacement was deleted")
	}
}

// Codex 5: a queued pak-install job cancelled before it runs deletes its
// staged archive.
func TestCancelledQueuedPakJobCleansUp(t *testing.T) {
	e := newEnv(t)
	stage, _ := e.svc.newPakStaging()
	block := make(chan struct{})
	e.svc.Jobs().Submit(JobPatch, func(ctx context.Context, _ func(int64, *int64)) (map[string]any, *Error) {
		<-block
		return nil, nil
	})
	defer close(block)
	id, apiErr, err := e.svc.submitWithCleanup(JobPakInstall, "x", JobGuard{}, func(context.Context, func(int64, *int64)) (map[string]any, *Error) {
		t.Error("ran")
		return nil, nil
	}, func() { e.svc.removeStaging(stage) })
	if apiErr != nil || err != nil {
		t.Fatal(apiErr, err)
	}
	if j, _ := e.svc.Jobs().Cancel(id); j.State != JobCancelled {
		t.Fatalf("state %s", j.State)
	}
	// Gone when the cancel answers (review 5), not later.
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("staged archive left after the cancel: %v", err)
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

// Review 4: two cancels of a queued job both answer after its cleanup.
func TestConcurrentCancelsWaitForTheCleanup(t *testing.T) {
	e := newEnv(t)
	block := make(chan struct{})
	e.svc.Jobs().Submit(JobPatch, func(ctx context.Context, _ func(int64, *int64)) (map[string]any, *Error) {
		<-block
		return nil, nil
	})
	defer close(block)
	release := make(chan struct{})
	var cleaned atomic.Bool
	id, apiErr, err := e.svc.submitWithCleanup(JobPakInstall, "x", JobGuard{}, func(context.Context, func(int64, *int64)) (map[string]any, *Error) {
		return nil, nil
	}, func() { <-release; cleaned.Store(true) })
	if apiErr != nil || err != nil {
		t.Fatal(apiErr, err)
	}
	answered := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		go func() {
			e.svc.Jobs().Cancel(id)
			answered <- cleaned.Load()
		}()
	}
	select {
	case <-answered:
		t.Fatal("a cancel answered before the cleanup")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	for i := 0; i < 2; i++ {
		if !<-answered {
			t.Fatal("answered before the cleanup finished")
		}
	}
}
