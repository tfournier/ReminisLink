package transfer

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestActivitiesUploads(t *testing.T) {
	e := newEnv(t)
	if acts := e.svc.Activities(); len(acts) != 0 || e.svc.Busy() {
		t.Fatalf("idle service: %+v", acts)
	}
	data := make([]byte, 3000)
	c := e.create(romTarget("Moon.gba"), data, "")
	acts := e.svc.Activities()
	if len(acts) != 1 || acts[0].Kind != ActivityUpload || acts[0].Target != KindROM || acts[0].Name != "Moon.gba" ||
		acts[0].Done != 0 || acts[0].Total != 3000 || !e.svc.Busy() {
		t.Fatalf("new upload: %+v", acts)
	}
	e.send(c.UploadID, data, 0, 2048)
	if acts := e.svc.Activities(); len(acts) != 1 || acts[0].Done != 2048 {
		t.Fatalf("after two chunks: %+v", acts)
	}
	// Abandoned: no longer in progress after ActiveUploadWindow.
	e.clock.Advance(ActiveUploadWindow)
	if acts := e.svc.Activities(); len(acts) != 0 || e.svc.Busy() {
		t.Fatalf("abandoned upload still active: %+v", acts)
	}
}

// TestActivitiesChunkInProgress checks that the bytes of a chunk being
// received count, without waiting for the chunk.
func TestActivitiesChunkInProgress(t *testing.T) {
	e := newEnv(t)
	data := make([]byte, 1000)
	c := e.create(romTarget("Slow.gba"), data, "")
	e.clock.Advance(ActiveUploadWindow) // only the chunk makes it active
	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.svc.WriteChunk(c.UploadID, 0, int64(len(data)), pr)
	}()
	if _, err := pw.Write(data[:400]); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		acts := e.svc.Activities()
		if len(acts) == 1 && acts[0].Done == 400 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("chunk in progress: %+v", acts)
		}
		time.Sleep(time.Millisecond)
	}
	pw.Write(data[400:])
	pw.Close()
	<-done
	if acts := e.svc.Activities(); len(acts) != 1 || acts[0].Done != 1000 {
		t.Fatalf("after the chunk: %+v", acts)
	}
}

func TestActivitiesJobs(t *testing.T) {
	q := NewJobs(nil)
	defer q.Close()
	release := make(chan struct{})
	slow := func(ctx context.Context, report func(int64, *int64)) (map[string]any, *Error) {
		total := int64(10)
		report(4, &total)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, nil
	}
	a, _ := q.SubmitLabeled(JobPatch, "Sky Hack.bps", slow)
	b, _ := q.SubmitLabeled(JobPatch, "Moon", slow)
	deadline := time.Now().Add(5 * time.Second)
	for {
		act := q.Active()
		if len(act) == 2 && act[0].ID == a && act[0].Progress.Done == 4 && act[1].ID == b && act[1].State == JobQueued {
			if act[0].Label != "Sky Hack.bps" || act[1].Label != "Moon" {
				t.Fatalf("labels: %+v", act)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("active jobs: %+v", act)
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	waitJob(t, q, b, JobSucceeded)
	if act := q.Active(); len(act) != 0 {
		t.Fatalf("finished jobs still active: %+v", act)
	}
}

func TestActivitiesIncludeJobs(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	defer close(release)
	id, _ := e.svc.Jobs().SubmitLabeled(JobPatch, "Moon", func(ctx context.Context, report func(int64, *int64)) (map[string]any, *Error) {
		report(5, nil)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, nil
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		acts := e.svc.Activities()
		if len(acts) == 1 && acts[0].ID == id && acts[0].Kind == JobPatch && acts[0].Name == "Moon" &&
			acts[0].Done == 5 && acts[0].Total == -1 && !acts[0].Queued {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("activities: %+v", acts)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestJobsOnFinish(t *testing.T) {
	q := NewJobs(nil)
	defer q.Close()
	got := make(chan Job, 1)
	q.OnFinish(func(j Job) { got <- j })
	id, _ := q.SubmitLabeled(JobPatch, "A.bin", func(context.Context, func(int64, *int64)) (map[string]any, *Error) {
		return map[string]any{"ok": true}, nil
	})
	select {
	case j := <-got:
		if j.ID != id || j.State != JobSucceeded || j.Label != "A.bin" {
			t.Fatalf("finished job: %+v", j)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnFinish not called")
	}
}

func TestUploadTarget(t *testing.T) {
	e := newEnv(t)
	c := e.create(romTarget("Moon.gba"), []byte("x"), "")
	if tg, ok := e.svc.UploadTarget(c.UploadID); !ok || tg.Kind != KindROM || *tg.FileName != "Moon.gba" {
		t.Fatalf("UploadTarget = %+v %v", tg, ok)
	}
	for _, id := range []string{"up_000000000000000000000000", "../x", ""} {
		if _, ok := e.svc.UploadTarget(id); ok {
			t.Errorf("%q found", id)
		}
	}
}
