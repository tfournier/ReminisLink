package paks

import (
	"bufio"
	"encoding/binary"
	"io"
)

// MaxDirectoryBytes bounds the central directory of an archive: read
// whole by archive/zip, it must not exhaust the memory of the console.
// 10 000 entries with names of up to 1024 bytes stay well below.
const MaxDirectoryBytes = 4 << 20

// Records of the end of a zip archive (APPNOTE 4.3.12 to 4.3.16).
const (
	eocdSig       = 0x06054b50
	eocdLen       = 22
	zip64LocSig   = 0x07064b50
	zip64LocLen   = 20
	zip64EOCDSig  = 0x06064b50
	zip64EOCDLen  = 56
	dirHeaderSig  = 0x02014b50
	dirHeaderLen  = 46
	maxEOCDSearch = 65 * 1024
)

// CheckDirectory walks the real central directory of a zip archive,
// from where archive/zip will read it (same end record search, same base
// offset rule), within a bounded read, and refuses the archive before
// archive/zip loads it: more than MaxEntries headers or more than
// MaxDirectoryBytes of headers, whatever the end record declares
// (archive/zip reads headers until a bad one and only compares the
// count modulo 65536). A file without an end record is not a zip.
func CheckDirectory(r io.ReaderAt, size int64) error {
	endAt, end, err := findEnd(r, size)
	if err != nil {
		return err
	}
	entries := uint64(binary.LittleEndian.Uint16(end[10:]))
	dirSize := uint64(binary.LittleEndian.Uint32(end[12:]))
	dirOffset := uint64(binary.LittleEndian.Uint32(end[16:]))
	if entries == 0xffff || dirSize == 0xffffffff || dirOffset == 0xffffffff {
		if p, ok := findZip64End(r, endAt); ok {
			rec := make([]byte, zip64EOCDLen)
			if _, err := r.ReadAt(rec, p); err != nil || binary.LittleEndian.Uint32(rec) != zip64EOCDSig {
				return refuse("not a zip archive")
			}
			endAt = p
			entries = binary.LittleEndian.Uint64(rec[32:])
			dirSize = binary.LittleEndian.Uint64(rec[40:])
			dirOffset = binary.LittleEndian.Uint64(rec[48:])
		}
	}
	// archive/zip preallocates from the declared count (when the file
	// could hold it): never more than the entries allowed.
	if entries > MaxEntries && entries != 0xffff {
		return refuse("more than %d entries", MaxEntries)
	}
	if dirSize > 1<<62 || dirOffset > 1<<62 {
		return refuse("not a zip archive")
	}
	base := endAt - int64(dirSize) - int64(dirOffset)
	start := base + int64(dirOffset)
	if start < 0 || start >= size {
		return refuse("not a zip archive")
	}
	// archive/zip reads the directory at the declared offset when a
	// header parses there, else at the recomputed one: both candidates
	// are walked, so whichever it reads is bounded.
	if base > 0 && int64(dirOffset) < size && isDirHeader(r, int64(dirOffset)) {
		if err := walkDirectory(r, size, int64(dirOffset)); err != nil {
			return err
		}
	}
	return walkDirectory(r, size, start)
}

// walkDirectory counts the central directory headers from start as
// archive/zip reads them (until a bad one), reading at most one header
// beyond MaxDirectoryBytes.
func walkDirectory(r io.ReaderAt, size, start int64) error {
	limit := min(size-start, int64(MaxDirectoryBytes+dirHeaderLen+1))
	br := bufio.NewReader(io.NewSectionReader(r, start, limit))
	var count, total int64
	hdr := make([]byte, dirHeaderLen)
	for {
		if _, err := io.ReadFull(br, hdr); err != nil || binary.LittleEndian.Uint32(hdr) != dirHeaderSig {
			break
		}
		n := int64(binary.LittleEndian.Uint16(hdr[28:])) + int64(binary.LittleEndian.Uint16(hdr[30:])) +
			int64(binary.LittleEndian.Uint16(hdr[32:]))
		total += dirHeaderLen + n
		if total > MaxDirectoryBytes {
			return refuse("central directory larger than %d bytes", MaxDirectoryBytes)
		}
		if count++; count > MaxEntries {
			return refuse("more than %d entries", MaxEntries)
		}
		if _, err := br.Discard(int(n)); err != nil {
			break // truncated: archive/zip stops there too
		}
	}
	return nil
}

// isDirHeader reports whether a central directory header starts at off.
func isDirHeader(r io.ReaderAt, off int64) bool {
	var b [4]byte
	_, err := r.ReadAt(b[:], off)
	return err == nil && binary.LittleEndian.Uint32(b[:]) == dirHeaderSig
}

// findEnd finds the end of central directory record as archive/zip does:
// in the last 1 KiB, then the last 65 KiB, the last signature whose
// comment fits in the file.
func findEnd(r io.ReaderAt, size int64) (int64, []byte, error) {
	for i, n := range []int64{1024, maxEOCDSearch} {
		if n > size {
			n = size
		}
		buf := make([]byte, n)
		if _, err := r.ReadAt(buf, size-n); err != nil && err != io.EOF {
			return 0, nil, refuse("not a zip archive")
		}
		for p := len(buf) - eocdLen; p >= 0; p-- {
			if binary.LittleEndian.Uint32(buf[p:]) == eocdSig {
				if c := int(binary.LittleEndian.Uint16(buf[p+20:])); c+eocdLen+p <= len(buf) {
					return size - n + int64(p), buf[p:], nil
				}
			}
		}
		if i == 1 || n == size {
			break
		}
	}
	return 0, nil, refuse("not a zip archive")
}

// findZip64End reads the zip64 locator just before the end record, as
// archive/zip does (single disk only); ok false without one.
func findZip64End(r io.ReaderAt, endAt int64) (int64, bool) {
	if endAt < zip64LocLen {
		return 0, false
	}
	loc := make([]byte, zip64LocLen)
	if _, err := r.ReadAt(loc, endAt-zip64LocLen); err != nil {
		return 0, false
	}
	if binary.LittleEndian.Uint32(loc) != zip64LocSig || binary.LittleEndian.Uint32(loc[4:]) != 0 ||
		binary.LittleEndian.Uint32(loc[16:]) != 1 {
		return 0, false
	}
	p := binary.LittleEndian.Uint64(loc[8:])
	if p > 1<<62 {
		return 0, false
	}
	return int64(p), true
}
