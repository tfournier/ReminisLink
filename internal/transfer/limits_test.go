package transfer

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestUploadAndJobLimits(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < MaxOpenUploads; i++ {
		e.create(&Target{Kind: KindStaging, FileName: ptr("x.bin")}, []byte{1}, "")
	}
	req := CreateUploadRequest{Target: &Target{Kind: KindStaging, FileName: ptr("x.bin")}, SizeBytes: ptr(int64(1)), SHA256: ptr(sum([]byte{1}))}
	if _, apiErr, _ := e.svc.CreateUpload(req); apiErr == nil || apiErr.Status != http.StatusTooManyRequests || apiErr.Code != CodeTooManyAttempts {
		t.Fatalf("upload %d: %v", MaxOpenUploads+1, apiErr)
	}

	q := NewJobs(nil)
	release := make(chan struct{})
	defer q.Close()
	defer close(release)
	block := func(ctx context.Context, _ func(int64, *int64)) (map[string]any, *Error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, nil
	}
	for i := 0; i < MaxPendingJobs; i++ {
		if _, err := q.Submit(JobDownload, block); err != nil {
			t.Fatalf("job %d: %v", i, err)
		}
	}
	if _, err := q.Submit(JobDownload, block); err != ErrTooManyJobs {
		t.Fatalf("job %d: %v", MaxPendingJobs+1, err)
	}
}

func TestStagingTmpNameResolves(t *testing.T) {
	e := newEnv(t)
	data := []byte("x")
	c := e.create(&Target{Kind: KindStaging, FileName: ptr("patch.tmp")}, data, "")
	e.send(c.UploadID, data, 0, 1)
	res := e.complete(c.UploadID)
	if p, err := e.svc.StagingFile(res["stagingId"].(string)); err != nil || filepath.Base(p) != "patch.tmp" {
		t.Fatalf("%v %v", p, err)
	}
}

func TestDownloadROMNeedsAGameName(t *testing.T) {
	fs := newFileServer(t, []byte("data"), false)
	e := newEnv(t)
	for _, path := range []string{"/files/", "/files/readme.txt", "/files/noext"} {
		j := waitJob(t, e.svc.Jobs(), e.download(fs.srv.URL+path, &Target{Kind: KindROM, SystemID: gameID(gbaSystem)}, nil, ""), JobSucceeded, JobFailed)
		if j.State != JobFailed || j.Error.Code != CodeInvalidName {
			t.Errorf("%s: %s %+v", path, j.State, j.Error)
		}
	}
	if e.exists("Roms/"+gbaSystem+"/download") || e.exists("Roms/"+gbaSystem+"/noext") { // readme.txt was already there
		t.Fatal("installed")
	}
}

// uploaded returns a finished (not completed) upload of data.
func (e *env) uploaded(target *Target, data []byte, onConflict string) string {
	e.t.Helper()
	c := e.create(target, data, onConflict)
	e.send(c.UploadID, data, 0, len(data))
	return c.UploadID
}

func TestConcurrentCompletes(t *testing.T) {
	for _, rule := range []string{ConflictFail, ConflictOverwrite} {
		t.Run(rule, func(t *testing.T) {
			e := newEnv(t)
			a := e.uploaded(romTarget("Same.gba"), []byte("first"), rule)
			b := e.uploaded(romTarget("Same.gba"), []byte("second"), rule)
			var wg sync.WaitGroup
			errs := make([]*Error, 2)
			for i, id := range []string{a, b} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, errs[i], _ = e.svc.CompleteUpload(id)
				}()
			}
			wg.Wait()
			final := e.read("Roms/" + gbaSystem + "/Same.gba")
			entries, _, _ := e.trash.List()
			switch rule {
			case ConflictFail:
				// Exactly one wins, the other gets 409 and keeps its upload.
				if (errs[0] == nil) == (errs[1] == nil) {
					t.Fatalf("errors %v %v", errs[0], errs[1])
				}
				if len(entries) != 0 {
					t.Fatal("fail must not trash")
				}
			case ConflictOverwrite:
				// Both succeed; the loser of the race is in the trash.
				if errs[0] != nil || errs[1] != nil || len(entries) != 1 {
					t.Fatalf("errors %v %v, trash %d", errs[0], errs[1], len(entries))
				}
				kept, _ := os.ReadFile(filepath.Join(e.trash.Dir(), entries[0].ID, "Roms", gbaSystem, "Same.gba"))
				if string(kept)+final != "firstsecond" && string(kept)+final != "secondfirst" {
					t.Fatalf("kept %q final %q", kept, final)
				}
			}
		})
	}
}

func TestOverwriteDuringDeleteGame(t *testing.T) {
	e := newEnv(t)
	game := gameID(gbaSystem + "/Starlight Courier.gba")
	old := e.read("Roms/" + gbaSystem + "/Starlight Courier.gba")
	id := e.uploaded(romTarget("Starlight Courier.gba"), []byte("new"), ConflictOverwrite)
	var wg sync.WaitGroup
	wg.Add(2)
	var delErr error
	var apiErr *Error
	go func() { defer wg.Done(); _, delErr = e.svc.DeleteGame(game, false) }()
	go func() { defer wg.Done(); _, apiErr, _ = e.svc.CompleteUpload(id) }()
	wg.Wait()
	if apiErr != nil {
		t.Fatalf("complete: %v", apiErr)
	}
	// Whatever the order, the old ROM is in the trash and nothing is lost.
	found := false
	entries, _, _ := e.trash.List()
	for _, en := range entries {
		data, err := os.ReadFile(filepath.Join(e.trash.Dir(), en.ID, "Roms", gbaSystem, "Starlight Courier.gba"))
		if err == nil && string(data) == old {
			found = true
		}
	}
	if !found {
		t.Fatalf("old ROM lost (delete: %v, trash %+v)", delErr, entries)
	}
	if delErr == nil && e.exists("Roms/"+gbaSystem+"/Starlight Courier.gba") && e.read("Roms/"+gbaSystem+"/Starlight Courier.gba") != "new" {
		t.Fatal("unexpected content")
	}
}

func TestCancelJobDuringInstall(t *testing.T) {
	data := bytes.Repeat([]byte("z"), 1000)
	fs := newFileServer(t, data, false)
	e := newEnv(t)
	// Hold the install lock so the job blocks right before installing.
	e.svc.installMu.Lock()
	id := e.download(fs.srv.URL+"/files/late.bin", staging(), nil, "")
	deadline := time.Now().Add(5 * time.Second)
	for {
		j, _ := e.svc.Jobs().Get(id)
		if j.Progress.Done == int64(len(data)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("download did not reach the install")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	done := make(chan Job)
	go func() { j, _ := e.svc.Jobs().Cancel(id); done <- j }()
	time.Sleep(20 * time.Millisecond)
	e.svc.installMu.Unlock()
	j := <-done
	// Too late to cancel: the file is installed and the job says so.
	if j.State != JobSucceeded || j.Result["stagingId"] == nil {
		t.Fatalf("job %+v", j)
	}
	if _, err := e.svc.StagingFile(j.Result["stagingId"].(string)); err != nil {
		t.Fatal(err)
	}
}
