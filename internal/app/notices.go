package app

import (
	"sync"
	"time"

	"github.com/tfournier/ReminisLink/internal/server"
	"github.com/tfournier/ReminisLink/internal/transfer"
)

// NoticeDuration is how long the banner of the main screen shows a change
// of the SD card (docs/specs/05-console-pak.md §7.1).
const NoticeDuration = 5 * time.Second

// Notice kinds: the server.Change kinds, plus these.
const (
	NoticeDownloaded     = "downloaded"
	NoticeDownloadFailed = "download-failed"
	NoticePatched        = "patched"
	NoticePatchFailed    = "patch-failed"
	NoticePaired         = "paired"
)

// Notice is the subject of the banner.
type Notice struct {
	Kind string // a server.Change kind or a Notice* constant
	Name string // game, file or device name ("" when unknown)
	At   time.Time
}

// notices keeps the last notice. Its methods never block for long: they
// are called from the request handlers and the job queue.
type notices struct {
	now  func() time.Time
	mu   sync.Mutex
	last Notice
}

func newNotices(now func() time.Time) *notices { return &notices{now: now} }

func (n *notices) push(x Notice) {
	n.mu.Lock()
	defer n.mu.Unlock()
	x.At = n.now()
	n.last = x
}

// change is the server's OnChange callback.
func (n *notices) change(c server.Change) { n.push(Notice{Kind: c.Kind, Name: c.Name}) }

// jobFinished is the OnFinish callback of the job queue.
func (n *notices) jobFinished(j transfer.Job) {
	var kind string
	switch {
	case j.Kind == transfer.JobDownload && j.State == transfer.JobSucceeded:
		kind = NoticeDownloaded
	case j.Kind == transfer.JobDownload && j.State == transfer.JobFailed:
		kind = NoticeDownloadFailed
	case j.Kind == transfer.JobPatch && j.State == transfer.JobSucceeded:
		kind = NoticePatched
	case j.Kind == transfer.JobPatch && j.State == transfer.JobFailed:
		kind = NoticePatchFailed
	default:
		return // cancelled: asked by the user, nothing to say
	}
	n.push(Notice{Kind: kind, Name: j.Label})
}

// current returns the last notice if it is younger than NoticeDuration.
func (n *notices) current() (Notice, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.last.Kind == "" || n.now().Sub(n.last.At) >= NoticeDuration {
		return Notice{}, false
	}
	return n.last, true
}
