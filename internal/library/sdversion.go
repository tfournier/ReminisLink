package library

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io/fs"
)

// sdVersion is the opaque version of an entry (M23, SdEntry.version): its
// kind, size and modification time in nanoseconds, hashed. No inode: the
// exFAT driver of Linux numbers inodes in memory, so the same file gets
// another one after a restart or an eviction from the cache (Codex review
// of M23, round 3); the version must not change without a real change.
func sdVersion(info fs.FileInfo) string {
	var b [17]byte
	if info.IsDir() {
		b[0] = 'd'
	} else {
		b[0] = 'f'
	}
	binary.LittleEndian.PutUint64(b[1:], uint64(info.Size()))
	binary.LittleEndian.PutUint64(b[9:], uint64(info.ModTime().UnixNano()))
	sum := sha256.Sum256(b[:])
	return "v2-" + hex.EncodeToString(sum[:8])
}
