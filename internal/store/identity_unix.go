//go:build unix

package store

import (
	"io/fs"
	"strconv"
	"syscall"
)

// identityOf is the device and inode of an entry ("" when unknown): what a
// rename journal records of the entry a step moves (M23).
func identityOf(fi fs.FileInfo) string {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return strconv.FormatUint(uint64(st.Dev), 10) + ":" + strconv.FormatUint(uint64(st.Ino), 10)
}
