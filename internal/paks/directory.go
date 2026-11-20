package paks

import (
	"bytes"
	"encoding/binary"
	"io"
)

// MaxDirectoryBytes bounds the central directory of an archive: read
// whole by archive/zip, it must not exhaust the memory of the console.
// 10 000 entries with names of up to 1024 bytes stay well below.
const MaxDirectoryBytes = 4 << 20

// Signatures of the end records of a zip archive.
const (
	eocdSig        = 0x06054b50
	eocdLen        = 22
	zip64LocSig    = 0x07064b50
	zip64LocLen    = 20
	zip64EOCDSig   = 0x06064b50
	zip64EOCDLen   = 56
	maxCommentSize = 0xffff
)

// CheckDirectory reads only the end records of a zip archive (its end of
// central directory, and the zip64 one when present) and refuses it
// before its central directory is loaded: more than MaxEntries entries,
// or a central directory larger than MaxDirectoryBytes. A file without
// an end record is not a zip archive.
func CheckDirectory(r io.ReaderAt, size int64) error {
	tail := int64(eocdLen + maxCommentSize)
	if tail > size {
		tail = size
	}
	if tail < eocdLen {
		return refuse("not a zip archive")
	}
	buf := make([]byte, tail)
	if _, err := r.ReadAt(buf, size-tail); err != nil && err != io.EOF {
		return refuse("not a zip archive")
	}
	pos := -1
	sig := []byte{0x50, 0x4b, 0x05, 0x06}
	for i := len(buf) - eocdLen; i >= 0; i-- {
		if bytes.Equal(buf[i:i+4], sig) {
			// The comment length must reach the end of the file exactly.
			if int(binary.LittleEndian.Uint16(buf[i+20:])) == len(buf)-i-eocdLen {
				pos = i
				break
			}
		}
	}
	if pos < 0 {
		return refuse("not a zip archive")
	}
	e := buf[pos:]
	entries := uint64(binary.LittleEndian.Uint16(e[10:]))
	dirSize := uint64(binary.LittleEndian.Uint32(e[12:]))
	if entries == 0xffff || dirSize == 0xffffffff || binary.LittleEndian.Uint32(e[16:]) == 0xffffffff {
		// zip64: the locator sits just before the end record.
		eocdAt := size - tail + int64(pos)
		if eocdAt < zip64LocLen {
			return refuse("not a zip archive")
		}
		loc := make([]byte, zip64LocLen)
		if _, err := r.ReadAt(loc, eocdAt-zip64LocLen); err != nil {
			return refuse("not a zip archive")
		}
		if binary.LittleEndian.Uint32(loc) != zip64LocSig {
			return refuse("not a zip archive")
		}
		at := binary.LittleEndian.Uint64(loc[8:])
		if at > uint64(size) || uint64(size)-at < zip64EOCDLen {
			return refuse("not a zip archive")
		}
		rec := make([]byte, zip64EOCDLen)
		if _, err := r.ReadAt(rec, int64(at)); err != nil || binary.LittleEndian.Uint32(rec) != zip64EOCDSig {
			return refuse("not a zip archive")
		}
		entries = binary.LittleEndian.Uint64(rec[32:])
		dirSize = binary.LittleEndian.Uint64(rec[40:])
	}
	if entries > MaxEntries {
		return refuse("more than %d entries", MaxEntries)
	}
	if dirSize > MaxDirectoryBytes {
		return refuse("central directory larger than %d bytes", MaxDirectoryBytes)
	}
	return nil
}
