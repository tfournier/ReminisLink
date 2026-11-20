package transfer

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// M13 (Codex review): a cancel that arrives while the final step of an
// upload waits for the write lock does not wait for the write in
// progress; it wins, and nothing is installed.
func TestCancelWhileCompleteWaitsForTheWriteLock(t *testing.T) {
	e := newEnv(t)
	data := randomBytes(t, 3000)
	c := e.create(romTarget("Waiting.gba"), data, "")
	e.send(c.UploadID, data, 0, len(data))
	e.svc.Writes().SetWait(5 * time.Second)
	if !e.svc.Writes().TryAcquire() {
		t.Fatal("write lock held")
	}
	type result struct {
		res    map[string]any
		apiErr *Error
		err    error
	}
	done := make(chan result, 1)
	go func() {
		res, apiErr, err := e.svc.CompleteUpload(context.Background(), c.UploadID)
		done <- result{res, apiErr, err}
	}()
	time.Sleep(100 * time.Millisecond) // complete is waiting for the lock
	cancelled := make(chan *Error, 1)
	go func() { cancelled <- e.svc.CancelUpload(c.UploadID) }()
	select {
	case apiErr := <-cancelled:
		if apiErr != nil {
			t.Fatalf("cancel: %v", apiErr)
		}
	case <-time.After(2 * time.Second):
		e.svc.Writes().Release()
		t.Fatal("the cancel waited for the write in progress")
	}
	e.svc.Writes().Release()
	r := <-done
	if r.err != nil || r.apiErr == nil || r.apiErr.Status != http.StatusNotFound {
		t.Fatalf("complete after the cancel: %v %v %v", r.res, r.apiErr, r.err)
	}
	if e.exists("Roms/" + gbaSystem + "/Waiting.gba") {
		t.Fatal("installed although cancelled")
	}
}

// The write check of the context runs once the lock is held: a refusal
// installs nothing, keeps the upload and frees the lock.
func TestCompleteWriteCheck(t *testing.T) {
	e := newEnv(t)
	data := randomBytes(t, 1000)
	c := e.create(romTarget("Checked.gba"), data, "")
	e.send(c.UploadID, data, 0, len(data))
	refused := newError(http.StatusUnauthorized, "disconnected", "Disconnected by the console")
	ctx := WithWriteCheck(context.Background(), func() *Error { return refused })
	if _, apiErr, err := e.svc.CompleteUpload(ctx, c.UploadID); apiErr != refused || err != nil {
		t.Fatalf("complete: %v %v", apiErr, err)
	}
	if e.exists("Roms/"+gbaSystem+"/Checked.gba") || !e.svc.Writes().TryAcquire() {
		t.Fatal("installed, or the lock was kept")
	}
	e.svc.Writes().Release()
	e.complete(c.UploadID)
}

// M13 (Codex round 2): the guard of a job is checked at its install step,
// under the write lock: a revoked client's job ends cancelled and installs
// nothing; CancelStale never touches a job of the current generation.
func TestJobGuardAtInstall(t *testing.T) {
	e := newEnv(t)
	data := []byte("guarded download")
	fs := newFileServer(t, data, false)
	valid := atomic.Bool{}
	guard := JobGuard{Owner: "b", Gen: 2, Valid: valid.Load}
	url := fs.srv.URL + "/files/g.bin"
	id, apiErr, err := e.svc.StartDownload(DownloadRequest{URL: &url, Target: staging()}, guard)
	if apiErr != nil || err != nil {
		t.Fatal(apiErr, err)
	}
	if j := waitJob(t, e.svc.Jobs(), id, JobSucceeded, JobFailed, JobCancelled); j.State != JobCancelled || j.Result != nil {
		t.Fatalf("job with an invalid guard: %+v", j)
	}
	valid.Store(true)
	id, _, _ = e.svc.StartDownload(DownloadRequest{URL: &url, Target: staging()}, guard)
	if n := e.svc.Jobs().CancelStale("b", 2); n != 0 {
		t.Fatalf("CancelStale cancelled %d jobs of the current generation", n)
	}
	if j := waitJob(t, e.svc.Jobs(), id, JobSucceeded, JobFailed, JobCancelled); j.State != JobSucceeded {
		t.Fatalf("job with a valid guard: %+v", j)
	}
}
