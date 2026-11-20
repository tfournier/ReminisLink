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
	// CancelRequested: DELETE /jobs/{jobId} was received for a running job
	// that has not stopped yet. The state stays "running" until the real
	// end: "cancelled" (nothing installed) or "succeeded" (too late).
	CancelRequested bool `json:"cancelRequested"`
	// Label is what the pak screen shows of the job (a file or game
	// name, "" when unknown). Not part of the API.
	Label string `json:"-"`
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
	order  uint64        // finishing order, to drop the oldest
	guard  JobGuard      // the client that started the job (M13)
	// cleanup, when set, deletes what the job owns (a staged archive,
	// M12) once it ends, whatever the way: run, cancelled while queued,
	// revoked, closed.
	cleanup func()
}

// cleanupAsync runs the cleanup of a job that ended without running
// (called with the queue lock held: never blocks).
func (j *job) cleanupAsync() {
	if j.cleanup != nil {
		go j.cleanup()
	}
}

// JobGuard ties a job to the client that started it (M13), fixed when the
// job is submitted, before it can start: its install step, once it holds
// the write lock, calls Valid, and a false answer ends the job cancelled
// without installing anything (the client was revoked since). Gen is the
// access generation of the request that started it: CancelStale cancels
// the jobs of an older generation. The zero value guards nothing.
type JobGuard struct {
	Owner string
	Gen   uint64
	Valid func() bool
	// Admit, when set, is called just before the job is queued: it takes
	// the lock of the revocations and checks the client again; the job is
	// queued while it holds it (one atomic step: a revoked client never
	// gets a job, nor its outbound request), then unlock is called. A
	// non-nil error refuses the job (unlock is then not called).
	Admit func() (unlock func(), refused *Error)
}

type jobGuardKey struct{}

// jobAllowed reports whether the job running with ctx may install its
// file (its JobGuard, when it has one, is still valid).
func jobAllowed(ctx context.Context) bool {
	g, ok := ctx.Value(jobGuardKey{}).(JobGuard)
	return !ok || g.Valid == nil || g.Valid()
}

// errJobRevoked ends a job whose client was revoked: it is reported as
// cancelled.
var errJobRevoked = newError(http.StatusServiceUnavailable, CodeShuttingDown, "Cancelled: client revoked")

// Jobs is the job queue.
type Jobs struct {
	now func() time.Time

	mu      sync.Mutex
	jobs    map[string]*job
	queue   []*job
	running *job
	closed  bool
	ended   uint64 // finished jobs so far
	wg      sync.WaitGroup
	// onFinish is called, outside the lock, when a job that ran ends.
	onFinish func(Job)
	// onUpdate is called, with the lock held, after every change of a
	// job (the event stream, M14).
	onUpdate func(Job)
}

// OnUpdate sets the function called after every change of a job: queued,
// running, progress, cancel requested, finished. It is called with the
// queue lock held, in the order of the changes: it must not block nor
// call the queue. Call it before submitting jobs.
func (q *Jobs) OnUpdate(fn func(Job)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.onUpdate = fn
}

// updatedLocked reports a change of j.
func (q *Jobs) updatedLocked(j *job) {
	if q.onUpdate != nil {
		q.onUpdate(j.Job)
	}
}

// OnFinish sets the function called when a job that ran ends (succeeded,
// failed or cancelled). Call it before submitting jobs.
func (q *Jobs) OnFinish(fn func(Job)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.onFinish = fn
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
	return q.SubmitLabeled(kind, "", run)
}

// SubmitLabeled queues a job with the label shown on the pak screen.
func (q *Jobs) SubmitLabeled(kind, label string, run RunFunc) (string, error) {
	return q.SubmitGuarded(kind, label, JobGuard{}, run)
}

// SubmitGuarded queues a job with its label and its guard (set before the
// job can start).
func (q *Jobs) SubmitGuarded(kind, label string, guard JobGuard, run RunFunc) (string, error) {
	return q.SubmitWithCleanup(kind, label, guard, run, nil)
}

// SubmitWithCleanup is SubmitGuarded with a cleanup called once the job
// ended, however it ends (nil: none). It is not called when the job is
// refused (the caller still owns its files).
func (q *Jobs) SubmitWithCleanup(kind, label string, guard JobGuard, run RunFunc, cleanup func()) (string, error) {
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
		Job: Job{ID: "job_" + hex.EncodeToString(b[:]), Kind: kind, State: JobQueued, CreatedAt: now, UpdatedAt: now, Label: label},
		run: run, done: make(chan struct{}), guard: guard, cleanup: cleanup,
	}
	q.jobs[j.ID] = j
	q.queue = append(q.queue, j)
	q.updatedLocked(j)
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
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), jobGuardKey{}, j.guard))
	j.cancel = cancel
	j.State = JobRunning
	j.UpdatedAt = q.stamp()
	q.running = j
	q.updatedLocked(j)
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
		q.updatedLocked(j)
	}
	result, apiErr := runSafely(ctx, j.run, report)
	if j.cleanup != nil {
		j.cleanup() // before the end is announced
	}
	q.mu.Lock()
	cancelled := ctx.Err() != nil || apiErr == errJobRevoked
	j.cancel()
	switch {
	case apiErr == nil:
		// Even when cancelled too late: the file is installed, say so.
		j.State = JobSucceeded
		j.Result = result
	case cancelled:
		j.State = JobCancelled
	default:
		j.State = JobFailed
		body := apiErr.Body()
		j.Error = &body
	}
	j.UpdatedAt = q.stamp()
	q.ended++
	j.order = q.ended
	close(j.done)
	q.updatedLocked(j)
	q.running = nil
	q.startNextLocked()
	finished, onFinish := j.Job, q.onFinish
	q.mu.Unlock()
	if onFinish != nil {
		onFinish(finished)
	}
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

// MaxFinishedJobs bounds the finished jobs kept (the oldest are dropped).
const MaxFinishedJobs = 200

// pruneLocked forgets the jobs finished more than JobRetention ago, and
// the oldest finished ones beyond MaxFinishedJobs.
func (q *Jobs) pruneLocked() {
	limit := q.now().Add(-JobRetention)
	var done []*job
	for id, j := range q.jobs {
		if !finished(j.State) {
			continue
		}
		if j.UpdatedAt.Before(limit) {
			delete(q.jobs, id)
			continue
		}
		done = append(done, j)
	}
	if len(done) <= MaxFinishedJobs {
		return
	}
	sort.Slice(done, func(a, b int) bool { return done[a].order < done[b].order })
	for _, j := range done[:len(done)-MaxFinishedJobs] {
		delete(q.jobs, j.ID)
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

// Active returns the running job then the queued ones, in queue order.
func (q *Jobs) Active() []Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Job, 0, len(q.queue)+1)
	if q.running != nil {
		out = append(out, q.running.Job)
	}
	for _, j := range q.queue {
		out = append(out, j.Job)
	}
	return out
}

// Running reports whether a job is queued or running.
func (q *Jobs) Running() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.running != nil || len(q.queue) > 0
}

// Cancel cancels a queued or running job and returns it in its real state.
// A queued job is cancelled at once. A running job is asked to stop
// (CancelRequested) and waited for, at most CancelWait: it stays "running"
// until it really ends, "cancelled" (partial file deleted, nothing
// installed) or "succeeded" when the cancel came too late (its file was
// being installed). A finished job is returned unchanged.
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
		q.ended++
		j.order = q.ended
		close(j.done)
		q.updatedLocked(j)
		out, cleanup := j.Job, j.cleanup
		q.mu.Unlock()
		// An explicit cancel answers once the job's files are gone.
		if cleanup != nil {
			cleanup()
		}
		return out, nil
	case JobRunning:
		// Only requested: the job may still finish (installed).
		j.CancelRequested = true
		j.UpdatedAt = q.stamp()
		q.updatedLocked(j)
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

// CancelStale cancels, without waiting, the queued and running jobs of
// owner whose guard generation is not current (M13: the client was
// revoked; current is its new access generation). A job submitted under
// the current generation is never touched. A running job stops before
// its install step; one already installing finishes. It never blocks: it
// may be called with other locks held. It returns how many jobs it
// cancelled.
func (q *Jobs) CancelStale(owner string, current uint64) int {
	if owner == "" {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	stale := func(j *job) bool { return j.guard.Owner == owner && j.guard.Gen != current }
	n := 0
	kept := q.queue[:0]
	for _, j := range q.queue {
		if !stale(j) {
			kept = append(kept, j)
			continue
		}
		j.State = JobCancelled
		j.UpdatedAt = q.stamp()
		q.ended++
		j.order = q.ended
		close(j.done)
		q.updatedLocked(j)
		j.cleanupAsync()
		n++
	}
	q.queue = kept
	if j := q.running; j != nil && stale(j) && !j.CancelRequested {
		j.CancelRequested = true
		j.UpdatedAt = q.stamp()
		q.updatedLocked(j)
		j.cancel()
		n++
	}
	return n
}

// Close refuses new jobs, cancels the queued and running ones and waits
// for the running one to stop.
func (q *Jobs) Close() {
	q.mu.Lock()
	q.closed = true
	var cleanups []func()
	for _, j := range q.queue {
		j.State = JobCancelled
		j.UpdatedAt = q.stamp()
		close(j.done)
		q.updatedLocked(j)
		if j.cleanup != nil {
			cleanups = append(cleanups, j.cleanup)
		}
	}
	q.queue = nil
	if q.running != nil {
		q.running.cancel()
	}
	q.mu.Unlock()
	for _, c := range cleanups {
		c()
	}
	q.wg.Wait()
}
