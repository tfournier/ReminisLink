//go:build linux || darwin

package saves

import (
	"io/fs"
	"syscall"
	"time"
)

// changeTime returns the status change time (ctime) of fi, which a write
// in place updates even when the modification time is kept.
func changeTime(fi fs.FileInfo) (time.Time, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}, false
	}
	return ctimeOf(st), true
}
