package transfer

import (
	"io"
	"sort"
	"sync/atomic"
	"time"
)

// What the pak screen shows of the transfers in progress
// (docs/specs/05-console-pak.md §7.1). Nothing here is part of the API.

// ActiveUploadWindow is how long an upload counts as in progress after
// its last received bytes. An upload the app abandoned stays on the card
// (it can be resumed) but no longer counts.
const ActiveUploadWindow = 30 * time.Second

// ActivityUpload is the Kind of an upload; jobs use their job kind
// (JobPatch, JobPakInstall).
const ActivityUpload = "upload"

// Activity is a transfer or a job in progress.
type Activity struct {
	ID     string // upload or job id
	Kind   string // ActivityUpload, JobPatch or JobPakInstall
	Target string // uploads: target kind (KindROM, KindSave...)
	Name   string // file or game name, "" when unknown
	Done   int64  // bytes done
	Total  int64  // bytes expected, -1 when unknown
	Queued bool   // a job waiting for the running one
}

// Activities returns the uploads that received bytes in the last
// ActiveUploadWindow (or are receiving a chunk), then the running and
// queued jobs. It never waits for a chunk in progress.
func (s *Service) Activities() []Activity {
	now := s.now()
	var out []Activity
	s.uploads.mu.Lock()
	for _, u := range s.uploads.m {
		if !u.active(now) {
			continue
		}
		name := ""
		if u.meta.Target.FileName != nil {
			name = *u.meta.Target.FileName
		}
		out = append(out, Activity{
			ID: u.meta.ID, Kind: ActivityUpload, Target: u.meta.Target.Kind, Name: name,
			Done: u.live.Load(), Total: u.meta.SizeBytes,
		})
	}
	s.uploads.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	for i, j := range s.jobs.Active() {
		total := int64(-1)
		if j.Progress.Total != nil {
			total = *j.Progress.Total
		}
		out = append(out, Activity{
			ID: j.ID, Kind: j.Kind, Name: j.Label, Done: j.Progress.Done, Total: total,
			Queued: i > 0 || j.State == JobQueued,
		})
	}
	return out
}

// Busy reports whether a transfer or a job is in progress (the exit of the
// pak asks for a confirmation then, 05 §7.3).
func (s *Service) Busy() bool {
	return len(s.Activities()) > 0
}

// active reports whether the upload received bytes recently.
func (u *upload) active(now time.Time) bool {
	if u.writing.Load() {
		return true
	}
	last := u.lastSeen.Load()
	return last != 0 && now.Sub(time.Unix(0, last)) < ActiveUploadWindow
}

// touch records activity on the upload.
func (u *upload) touch(now time.Time) { u.lastSeen.Store(now.UnixNano()) }

// progressReader counts the bytes read into an upload's live counter.
type progressReader struct {
	r    io.Reader
	live *atomic.Int64
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.live.Add(int64(n))
	return n, err
}

// UploadTarget returns the target of a live upload (ok false when the id
// is unknown).
func (s *Service) UploadTarget(id string) (Target, bool) {
	if !uploadIDRe.MatchString(id) {
		return Target{}, false
	}
	s.uploads.mu.Lock()
	defer s.uploads.mu.Unlock()
	u, ok := s.uploads.m[id]
	if !ok {
		return Target{}, false
	}
	return u.meta.Target, true
}
