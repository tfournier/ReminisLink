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

func TestROMNamesFollowTheListing(t *testing.T) {
	fs := newFileServer(t, []byte("data"), false)
	e := newEnv(t)
	sys := gameID(gbaSystem)
	// Refused at creation: the library would not list it.
	req := CreateUploadRequest{Target: romTarget("notes.txt"), SizeBytes: ptr(int64(1)), SHA256: ptr(sum([]byte{1}))}
	if _, apiErr, _ := e.svc.CreateUpload(req); apiErr == nil || apiErr.Code != CodeInvalidName {
		t.Fatalf("upload notes.txt: %v", apiErr)
	}
	dl := DownloadRequest{URL: ptr(fs.srv.URL + "/files/x"), Target: &Target{Kind: KindROM, SystemID: sys, FileName: ptr("notes.txt")}}
	if _, apiErr, _ := e.svc.StartDownload(dl); apiErr == nil || apiErr.Code != CodeInvalidName {
		t.Fatalf("download notes.txt: %v", apiErr)
	}
	// A deduced name the listing ignores fails the job.
	j := waitJob(t, e.svc.Jobs(), e.download(fs.srv.URL+"/files/notes.txt", &Target{Kind: KindROM, SystemID: sys}, nil, ""), JobSucceeded, JobFailed)
	if j.State != JobFailed || j.Error.Code != CodeInvalidName || e.exists("Roms/"+gbaSystem+"/notes.txt") {
		t.Fatalf("deduced notes.txt: %+v", j)
	}
	// A name without extension is listed, so it is accepted, with a
	// gameId the library resolves.
	for _, path := range []string{"/files/", "/files/noext"} {
		j := waitJob(t, e.svc.Jobs(), e.download(fs.srv.URL+path, &Target{Kind: KindROM, SystemID: sys}, nil, ""), JobSucceeded, JobFailed)
		if j.State != JobSucceeded {
			t.Fatalf("%s: %+v", path, j.Error)
		}
		if _, err := e.lib.Game(j.Result["gameId"].(string)); err != nil {
			t.Fatalf("%s: gameId not listed: %v", path, err)
		}
	}
}

func TestResumedUploadIsVerifiedInBackground(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.VerifyWait = 50 * time.Millisecond })
	data := randomBytes(t, 3000)
	c := e.create(romTarget("Verified.gba"), data, "")
	e.send(c.UploadID, data, 0, 2048)
	e.svc.Close()
	// Restart with the verification slots taken: the hash is not ready.
	s, err := New(e.opts)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxParallelVerify; i++ {
		s.verifySem <- struct{}{}
	}
	if err := s.LoadUploads(); err != nil {
		t.Fatal(err)
	}
	e.svc = s
	// GET does not wait.
	if st, apiErr := s.Status(c.UploadID); apiErr != nil || st.Offset != 2048 {
		t.Fatalf("status %+v %v", st, apiErr)
	}
	_, apiErr, _ := s.WriteChunk(c.UploadID, 2048, 952, bytes.NewReader(data[2048:]))
	if apiErr == nil || apiErr.Status != http.StatusTooManyRequests || apiErr.Message != "Upload is being verified" {
		t.Fatalf("chunk during verification: %v", apiErr)
	}
	if _, apiErr, _ := s.CompleteUpload(c.UploadID); apiErr == nil || apiErr.Status != http.StatusTooManyRequests {
		t.Fatalf("complete during verification: %v", apiErr)
	}
	for i := 0; i < MaxParallelVerify; i++ {
		<-s.verifySem
	}
	e.send(c.UploadID, data, 2048, len(data))
	e.complete(c.UploadID)
	if e.read("Roms/"+gbaSystem+"/Verified.gba") != string(data) {
		t.Fatal("content")
	}

	// Close stops a pending verification and keeps the files.
	c = e.create(romTarget("Stopped.gba"), data, "")
	e.send(c.UploadID, data, 0, 1024)
	e.svc.Close()
	s, _ = New(e.opts)
	s.verifySem <- struct{}{}
	s.verifySem <- struct{}{}
	s.LoadUploads()
	s.Close()
	if _, err := os.Stat(filepath.Join(e.layout.UploadsDir(), c.UploadID+partExt)); err != nil {
		t.Fatal("partial file lost at close")
	}
	e.svc = e.newService()
}

func TestFinishedJobsAreBounded(t *testing.T) {
	q := NewJobs(nil)
	defer q.Close()
	quick := func(context.Context, func(int64, *int64)) (map[string]any, *Error) { return nil, nil }
	var first string
	for i := 0; i < MaxFinishedJobs+10; i++ {
		id, err := q.Submit(JobDownload, quick)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = id
		}
		waitJob(t, q, id, JobSucceeded)
	}
	if n := len(q.List()); n != MaxFinishedJobs {
		t.Fatalf("%d jobs kept", n)
	}
	if _, apiErr := q.Get(first); apiErr == nil {
		t.Fatal("oldest job kept")
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
	// Cancelled while waiting for the lock (Codex review of M4): the
	// context is checked again once the lock is held, nothing installed.
	if j.State != JobCancelled || j.Result["stagingId"] != nil {
		t.Fatalf("job %+v", j)
	}
	if des, _ := os.ReadDir(e.layout.StagingDir()); len(des) != 0 {
		t.Fatalf("installed after the cancel: %v", des)
	}
}

// restartHeld restarts the service with the verification slots taken; the
// returned release lets the background hash run.
func (e *env) restartHeld() (*Service, func()) {
	e.t.Helper()
	e.svc.Close()
	s, err := New(e.opts)
	if err != nil {
		e.t.Fatal(err)
	}
	for i := 0; i < MaxParallelVerify; i++ {
		s.verifySem <- struct{}{}
	}
	if err := s.LoadUploads(); err != nil {
		e.t.Fatal(err)
	}
	e.svc = s
	return s, func() {
		for i := 0; i < MaxParallelVerify; i++ {
			<-s.verifySem
		}
	}
}

func TestVerifyFailures(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads unreadable files")
	}
	e := newEnv(t)
	data := randomBytes(t, 3000)

	// Transient: the .part cannot be read for a while.
	c := e.create(romTarget("Transient.gba"), data, "")
	e.send(c.UploadID, data, 0, 2048)
	part := filepath.Join(e.layout.UploadsDir(), c.UploadID+partExt)
	os.Chmod(part, 0)
	s, release := e.restartHeld()
	release()
	_, apiErr, _ := s.WriteChunk(c.UploadID, 2048, 952, bytes.NewReader(data[2048:]))
	if apiErr == nil || apiErr.Code != CodeInternal {
		t.Fatalf("unreadable: %v", apiErr)
	}
	os.Chmod(part, 0o644)
	e.send(c.UploadID, data, 2048, len(data)) // tried again: works
	e.complete(c.UploadID)

	// Durable: the .part lost bytes after the restart.
	c = e.create(romTarget("Lost.gba"), data, "")
	e.send(c.UploadID, data, 0, 2048)
	s, release = e.restartHeld()
	os.Truncate(filepath.Join(e.layout.UploadsDir(), c.UploadID+partExt), 100)
	release()
	_, apiErr, _ = s.WriteChunk(c.UploadID, 2048, 952, bytes.NewReader(data[2048:]))
	if apiErr == nil || apiErr.Code != CodeNotFound {
		t.Fatalf("lost part: %v", apiErr)
	}
	if _, apiErr := s.Status(c.UploadID); apiErr == nil || apiErr.Code != CodeNotFound {
		t.Fatal("upload kept")
	}

	// Shutdown while a chunk waits for the hash: 503 shutting_down.
	c = e.create(romTarget("Closing.gba"), data, "")
	e.send(c.UploadID, data, 0, 2048)
	s, _ = e.restartHeld()
	got := make(chan *Error)
	go func() {
		_, apiErr, _ := s.WriteChunk(c.UploadID, 2048, 952, bytes.NewReader(data[2048:]))
		got <- apiErr
	}()
	time.Sleep(50 * time.Millisecond)
	s.Close()
	if apiErr := <-got; apiErr == nil || apiErr.Status != http.StatusServiceUnavailable || apiErr.Code != CodeShuttingDown {
		t.Fatalf("during shutdown: %v", apiErr)
	}
	e.svc = e.newService()
}
