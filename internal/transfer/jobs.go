package transfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"sync"
	"time"
)

// Jobs (docs/specs/03-protocole.md §8): one job runs at a time, the others
// wait in a queue. Finished jobs are kept JobRetention in memory.

// JobRetention is how long a finished job stays listed.
const JobRetention = time.Hour

// CancelWait bounds the wait for a running job to stop after a cancel.
const CancelWait = 10 * time.Second

// Job kinds and states.
const (
	JobDownload = "download"
	JobPatch    = "patch"

	JobQueued    = "queued"
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"
	JobCancelled = "cancelled"
)

// MaxPendingJobs bounds the queued and running jobs.
const MaxPendingJobs = 100

// Errors of Submit.
var (
	ErrJobsClosed  = errors.New("jobs: closed")
	ErrTooManyJobs = errors.New("jobs: too many pending jobs")
)

var jobIDRe = regexp.MustCompile(`^job_[0-9a-f]{24}$`)

// Progress is the progress of a job.
type Progress struct {
	Done  int64  `json:"done"`
	Total *int64 `json:"total"`
}

// Job is the public state of a job (Job in protocol/openapi.yaml).
type Job struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	State     string         `json:"state"`
	Progress  Progress       `json:"progress"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	Result    map[string]any `json:"result"`
	Error     *ErrorBody     `json:"error"`
}

// RunFunc does the work of a job. It reports its progress with report,
// stops when ctx is cancelled (deleting its partial files) and returns
// the created resource or an API error.
type RunFunc func(ctx context.Context, report func(done int64, total *int64)) (map[string]any, *Error)

type job struct {
	Job
	run    RunFunc
	cancel context.CancelFunc
	done   chan struct{} // closed when run returned
}

// Jobs is the job queue.
type Jobs struct {
	now func() time.Time

	mu      sync.Mutex
	jobs    map[string]*job
	queue   []*job
	running *job
	closed  bool
	wg      sync.WaitGroup
}

// NewJobs returns an empty queue (now nil: time.Now).
func NewJobs(now func() time.Time) *Jobs {
	if now == nil {
		now = time.Now
	}
	return &Jobs{now: now, jobs: map[string]*job{}}
}

func (q *Jobs) stamp() time.Time { return q.now().UTC().Truncate(time.Second) }

// Submit queues a job and returns its id.
func (q *Jobs) Submit(kind string, run RunFunc) (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return "", ErrJobsClosed
	}
	pending := len(q.queue)
	if q.running != nil {
		pending++
	}
	if pending >= MaxPendingJobs {
		return "", ErrTooManyJobs
	}
	q.pruneLocked()
	now := q.stamp()
	j := &job{
		Job: Job{ID: "job_" + hex.EncodeToString(b[:]), Kind: kind, State: JobQueued, CreatedAt: now, UpdatedAt: now},
		run: run, done: make(chan struct{}),
	}
	q.jobs[j.ID] = j
	q.queue = append(q.queue, j)
	q.startNextLocked()
	return j.ID, nil
}

// startNextLocked starts the first queued job if none is running.
func (q *Jobs) startNextLocked() {
	if q.running != nil || len(q.queue) == 0 || q.closed {
		return
	}
	j := q.queue[0]
	q.queue = q.queue[1:]
	ctx, cancel := context.WithCancel(context.Background())
	j.cancel = cancel
	j.State = JobRunning
	j.UpdatedAt = q.stamp()
	q.running = j
	q.wg.Add(1)
	go q.execute(ctx, j)
}

func (q *Jobs) execute(ctx context.Context, j *job) {
	defer q.wg.Done()
	report := func(done int64, total *int64) {
		q.mu.Lock()
		defer q.mu.Unlock()
		j.Progress = Progress{Done: done, Total: total}
		j.UpdatedAt = q.stamp()
	}
	result, apiErr := runSafely(ctx, j.run, report)
	q.mu.Lock()
	defer q.mu.Unlock()
	cancelled := ctx.Err() != nil
	j.cancel()
	switch {
	case apiErr == nil:
		// Even when cancelled too late: the file is installed, say so.
		j.State = JobSucceeded
		j.Result = result
	case j.State == JobCancelled || cancelled:
		j.State = JobCancelled
	default:
		j.State = JobFailed
		body := apiErr.Body()
		j.Error = &body
	}
	j.UpdatedAt = q.stamp()
	close(j.done)
	q.running = nil
	q.startNextLocked()
}

// runSafely runs a job and turns a panic into an internal error.
func runSafely(ctx context.Context, run RunFunc, report func(int64, *int64)) (result map[string]any, apiErr *Error) {
	defer func() {
		if v := recover(); v != nil {
			result, apiErr = nil, internal("Job failed")
		}
	}()
	return run(ctx, report)
}

// pruneLocked forgets the jobs finished more than JobRetention ago.
func (q *Jobs) pruneLocked() {
	limit := q.now().Add(-JobRetention)
	for id, j := range q.jobs {
		if finished(j.State) && j.UpdatedAt.Before(limit) {
			delete(q.jobs, id)
		}
	}
}

func finished(state string) bool {
	return state == JobSucceeded || state == JobFailed || state == JobCancelled
}

// List returns the jobs, newest first.
func (q *Jobs) List() []Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pruneLocked()
	out := make([]Job, 0, len(q.jobs))
	for _, j := range q.jobs {
		out = append(out, j.Job)
	}
	sort.Slice(out, func(a, b int) bool {
		if !out[a].CreatedAt.Equal(out[b].CreatedAt) {
			return out[a].CreatedAt.After(out[b].CreatedAt)
		}
		return out[a].ID > out[b].ID
	})
	return out
}

// Get returns a job: 400 invalid_id or 404 not_found.
func (q *Jobs) Get(id string) (Job, *Error) {
	if !jobIDRe.MatchString(id) {
		return Job{}, newError(http.StatusBadRequest, CodeInvalidID, "Invalid identifier")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pruneLocked()
	j, ok := q.jobs[id]
	if !ok {
		return Job{}, notFound("Job not found")
	}
	return j.Job, nil
}

// Running reports whether a job is queued or running.
func (q *Jobs) Running() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.running != nil || len(q.queue) > 0
}

// Cancel cancels a queued or running job and returns it. A running job is
// waited for (at most CancelWait) so its partial file is gone when Cancel
// returns. A finished job is returned unchanged.
func (q *Jobs) Cancel(id string) (Job, *Error) {
	if !jobIDRe.MatchString(id) {
		return Job{}, newError(http.StatusBadRequest, CodeInvalidID, "Invalid identifier")
	}
	q.mu.Lock()
	j, ok := q.jobs[id]
	if !ok {
		q.mu.Unlock()
		return Job{}, notFound("Job not found")
	}
	switch j.State {
	case JobQueued:
		for i, x := range q.queue {
			if x == j {
				q.queue = append(q.queue[:i], q.queue[i+1:]...)
				break
			}
		}
		j.State = JobCancelled
		j.UpdatedAt = q.stamp()
		close(j.done)
		out := j.Job
		q.mu.Unlock()
		return out, nil
	case JobRunning:
		j.State = JobCancelled
		j.UpdatedAt = q.stamp()
		j.cancel()
		q.mu.Unlock()
		select {
		case <-j.done:
		case <-time.After(CancelWait):
		}
		q.mu.Lock()
		defer q.mu.Unlock()
		return j.Job, nil
	}
	defer q.mu.Unlock()
	return j.Job, nil
}

// Close refuses new jobs, cancels the queued and running ones and waits
// for the running one to stop.
func (q *Jobs) Close() {
	q.mu.Lock()
	q.closed = true
	for _, j := range q.queue {
		j.State = JobCancelled
		j.UpdatedAt = q.stamp()
		close(j.done)
	}
	q.queue = nil
	if q.running != nil {
		q.running.cancel()
	}
	q.mu.Unlock()
	q.wg.Wait()
}
