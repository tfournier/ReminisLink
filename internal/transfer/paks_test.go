package transfer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
	e.svc.Jobs().Submit(JobDownload, func(ctx context.Context, _ func(int64, *int64)) (map[string]any, *Error) {
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

// Codex 4: a pak download is capped at the pak archive size, on the
// announced length and on the bytes written (no length).
func TestPakDownloadCap(t *testing.T) {
	defer func(old int64) { pakArchiveLimit = old }(pakArchiveLimit)
	pakArchiveLimit = 1000
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/announced" {
			w.Header().Set("Content-Length", "5000")
			w.WriteHeader(http.StatusOK)
			w.Write(make([]byte, 5000))
			return
		}
		w.WriteHeader(http.StatusOK) // chunked, no length
		for i := 0; i < 100; i++ {
			if _, err := w.Write(make([]byte, 1000)); err != nil {
				break
			}
			w.(http.Flusher).Flush()
		}
	}))
	defer srv.Close()
	e := newEnv(t)
	for _, p := range []string{"/announced", "/chunked"} {
		id := e.download(srv.URL+p, &Target{Kind: KindPak}, nil, ConflictFail)
		j := waitJob(t, e.svc.Jobs(), id, JobFailed, JobSucceeded, JobCancelled)
		if j.State != JobFailed || j.Error.Code != CodeTooLarge {
			t.Fatalf("%s: %+v %+v", p, j, j.Error)
		}
	}
	des, _ := os.ReadDir(e.svc.opts.Layout.UploadsDir())
	for _, d := range des {
		if fi, _ := d.Info(); fi != nil && fi.Size() > 1000 {
			t.Fatalf("more than the cap written: %s %d", d.Name(), fi.Size())
		}
	}
}
