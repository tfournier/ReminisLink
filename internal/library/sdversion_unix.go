//go:build unix

package library

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io/fs"
	"syscall"
)

// sdVersion is the opaque version of an entry (M23, SdEntry.version):
// device, inode, size and modification time, hashed. It changes when the
// entry is replaced by another file or folder of the same name, or
// modified. "" when the file system does not give an inode.
func sdVersion(info fs.FileInfo) string {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	var b [32]byte
	binary.LittleEndian.PutUint64(b[0:], uint64(st.Dev))
	binary.LittleEndian.PutUint64(b[8:], uint64(st.Ino))
	binary.LittleEndian.PutUint64(b[16:], uint64(info.Size()))
	binary.LittleEndian.PutUint64(b[24:], uint64(info.ModTime().UnixNano()))
	sum := sha256.Sum256(b[:])
	return "v1-" + hex.EncodeToString(sum[:8])
}
