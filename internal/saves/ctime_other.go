//go:build !linux && !darwin

package saves

import (
	"io/fs"
	"time"
)

// changeTime: no status change time on this platform.
func changeTime(fs.FileInfo) (time.Time, bool) { return time.Time{}, false }
