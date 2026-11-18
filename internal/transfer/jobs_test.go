package transfer

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"
)

func waitJob(t *testing.T, q *Jobs, id string, states ...string) Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		j, apiErr := q.Get(id)
		if apiErr != nil {
			t.Fatalf("get %s: %v", id, apiErr)
		}
		for _, s := range states {
			if j.State == s {
				return j
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s stuck in %s (want %v)", id, j.State, states)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestJobsRunOneAtATime(t *testing.T) {
	clk := &testClock{t: time.Date(2026, 11, 18, 17, 0, 0, 0, time.UTC)}
	q := NewJobs(clk.Now)
	defer q.Close()
	release := make(chan struct{})
	defer close(release) // before Close: unblocks a job left running
	var mu sync.Mutex
	running, maxRunning := 0, 0
	block := func(ctx context.Context, report func(int64, *int64)) (map[string]any, *Error) {
		mu.Lock()
		running++
		maxRunning = max(maxRunning, running)
		mu.Unlock()
		total := int64(10)
		report(5, &total)
		<-release
		mu.Lock()
		running--
		mu.Unlock()
		return map[string]any{"gameId": "x"}, nil
	}
	a, _ := q.Submit(JobDownload, block)
	b, _ := q.Submit(JobDownload, block)
	waitJob(t, q, a, JobRunning)
	if j, _ := q.Get(b); j.State != JobQueued {
		t.Fatalf("second job %s", j.State)
	}
	if !q.Running() {
		t.Fatal("Running() false")
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		j, _ := q.Get(a)
		if j.Progress.Done == 5 && j.Progress.Total != nil && *j.Progress.Total == 10 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("progress %+v", j.Progress)
		}
	}
	release <- struct{}{}
	ja := waitJob(t, q, a, JobSucceeded)
	if ja.Result["gameId"] != "x" || ja.Error != nil {
		t.Fatalf("result %+v", ja)
	}
	waitJob(t, q, b, JobRunning)
	release <- struct{}{}
	waitJob(t, q, b, JobSucceeded)
	if maxRunning != 1 {
		t.Fatalf("%d jobs ran together", maxRunning)
	}
	if list := q.List(); len(list) != 2 {
		t.Fatalf("list %d", len(list))
	}
	// Kept one hour after the end.
	clk.Advance(59 * time.Minute)
	if len(q.List()) != 2 {
		t.Fatal("pruned too early")
	}
	clk.Advance(2 * time.Minute)
	if len(q.List()) != 0 {
		t.Fatal("not pruned after an hour")
	}
	if _, apiErr := q.Get(a); apiErr == nil || apiErr.Status != http.StatusNotFound {
		t.Fatalf("pruned job: %v", apiErr)
	}
}

func TestJobsFailCancelClose(t *testing.T) {
	q := NewJobs(nil)
	failing, _ := q.Submit(JobDownload, func(context.Context, func(int64, *int64)) (map[string]any, *Error) {
		return nil, notFound("Download failed: HTTP 404")
	})
	j := waitJob(t, q, failing, JobFailed)
	if j.Error == nil || j.Error.Code != CodeNotFound || j.Error.Details == nil || j.Result != nil {
		t.Fatalf("failed job %+v", j)
	}
	panicking, _ := q.Submit(JobDownload, func(context.Context, func(int64, *int64)) (map[string]any, *Error) {
		panic("boom")
	})
	if j := waitJob(t, q, panicking, JobFailed); j.Error.Code != CodeInternal {
		t.Fatalf("panic: %+v", j)
	}

	stopped := make(chan struct{})
	wait := func(ctx context.Context, _ func(int64, *int64)) (map[string]any, *Error) {
		<-ctx.Done()
		close(stopped) // the partial file is deleted here
		return nil, internal("cancelled")
	}
	running, _ := q.Submit(JobDownload, wait)
	queued, _ := q.Submit(JobDownload, wait)
	waitJob(t, q, running, JobRunning)
	if j, apiErr := q.Cancel(queued); apiErr != nil || j.State != JobCancelled {
		t.Fatalf("cancel queued: %+v %v", j, apiErr)
	}
	j, apiErr := q.Cancel(running)
	if apiErr != nil || j.State != JobCancelled {
		t.Fatalf("cancel running: %+v %v", j, apiErr)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("Cancel returned before the job stopped")
	}
	if j, _ := q.Cancel(running); j.State != JobCancelled {
		t.Fatal("cancel twice")
	}
	if _, apiErr := q.Cancel("job_x"); apiErr == nil || apiErr.Code != CodeInvalidID {
		t.Fatal("bad id")
	}
	if _, apiErr := q.Cancel("job_000000000000000000000000"); apiErr == nil || apiErr.Code != CodeNotFound {
		t.Fatal("unknown id")
	}

	// Close cancels the running job and refuses new ones.
	started := make(chan struct{})
	last, _ := q.Submit(JobDownload, func(ctx context.Context, _ func(int64, *int64)) (map[string]any, *Error) {
		close(started)
		<-ctx.Done()
		return nil, internal("cancelled")
	})
	<-started
	q.Close()
	if j, _ := q.Get(last); j.State != JobCancelled {
		t.Fatalf("after close: %s", j.State)
	}
	if _, err := q.Submit(JobDownload, wait); err != ErrJobsClosed {
		t.Fatalf("submit after close: %v", err)
	}
}
