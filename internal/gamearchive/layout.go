package gamearchive

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"io"
	"sort"
)

// The physical layout of a game archive (Codex review of M19): archive/zip
// trusts the offsets and sizes of the central directory. Before any
// content is read, checkLayout reads the central directory itself and the
// local header of every entry, and requires a plain layout, the one the
// pak writes: no data before the first entry nor between the directory
// and its end records, every entry's local header and data inside the
// file and before the directory, no two entries sharing bytes, local and
// central names, methods and sizes in agreement. The sizes used by the
// compression-ratio check are then those of real, distinct byte ranges.

const (
	localSig       = 0x04034b50
	localLen       = 30
	centralSig     = 0x02014b50
	centralLen     = 46
	endSig         = 0x06054b50
	endLen         = 22
	zip64LocSig    = 0x07064b50
	zip64LocLen    = 20
	zip64EndSig    = 0x06064b50
	zip64EndMinLen = 56
	descriptorSig  = 0x08074b50
	flagDescriptor = 0x8
	maxComment     = 65535
)

// centralEntry is an entry of the central directory.
type centralEntry struct {
	name           []byte
	flags, method  uint16
	comp, uncomp   uint64
	offset         uint64
	start, dataEnd int64 // local header .. end of data (and descriptor)
}

// checkLayout checks the layout of the archive r of size bytes against the
// entries archive/zip read (files). Refusals are FormatErrors.
func checkLayout(r io.ReaderAt, size int64, files []*zip.File) error {
	dirStart, dirEnd, count, err := directoryBounds(r, size)
	if err != nil {
		return err
	}
	if count != uint64(len(files)) {
		return refuse("inconsistent central directory")
	}
	entries, err := readCentral(r, dirStart, dirEnd, count)
	if err != nil {
		return err
	}
	for i, e := range entries {
		f := files[i]
		if string(e.name) != f.Name || e.method != f.Method || e.comp != f.CompressedSize64 || e.uncomp != f.UncompressedSize64 {
			return refuse("inconsistent central directory")
		}
		if err := checkLocal(r, dirStart, &entries[i]); err != nil {
			return err
		}
	}
	// The entries follow each other from the start of the file to the
	// central directory: no two share a byte, nothing hides between them.
	sorted := append([]centralEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].start < sorted[j].start })
	var at int64
	for _, e := range sorted {
		if e.start < at {
			return refuse("overlapping entries")
		}
		if e.start > at {
			return refuse("unexpected data between entries")
		}
		at = e.dataEnd
	}
	if at != dirStart {
		return refuse("unexpected data before the central directory")
	}
	return nil
}

// directoryBounds finds the central directory from the end records: the
// end record must end the file (its comment included), and the directory
// must end right where the (zip64) end records start.
func directoryBounds(r io.ReaderAt, size int64) (start, end int64, count uint64, err error) {
	n := min(size, int64(endLen+maxComment))
	buf := make([]byte, n)
	if _, err := r.ReadAt(buf, size-n); err != nil && err != io.EOF {
		return 0, 0, 0, refuse("not a zip archive")
	}
	at := -1
	for p := len(buf) - endLen; p >= 0; p-- {
		if binary.LittleEndian.Uint32(buf[p:]) == endSig && p+endLen+int(binary.LittleEndian.Uint16(buf[p+20:])) == len(buf) {
			at = p
			break
		}
	}
	if at < 0 {
		return 0, 0, 0, refuse("not a zip archive")
	}
	rec := buf[at:]
	endAt := size - n + int64(at)
	if binary.LittleEndian.Uint16(rec[4:]) != 0 || binary.LittleEndian.Uint16(rec[6:]) != 0 {
		return 0, 0, 0, refuse("multi-disk archive")
	}
	count = uint64(binary.LittleEndian.Uint16(rec[10:]))
	dirSize := uint64(binary.LittleEndian.Uint32(rec[12:]))
	dirOffset := uint64(binary.LittleEndian.Uint32(rec[16:]))
	recordsAt := endAt
	if count == 0xffff || dirSize == 0xffffffff || dirOffset == 0xffffffff {
		if endAt < zip64LocLen {
			return 0, 0, 0, refuse("not a zip archive")
		}
		loc := make([]byte, zip64LocLen)
		if _, err := r.ReadAt(loc, endAt-zip64LocLen); err != nil || binary.LittleEndian.Uint32(loc) != zip64LocSig {
			return 0, 0, 0, refuse("not a zip archive")
		}
		p := binary.LittleEndian.Uint64(loc[8:])
		if p > uint64(endAt-zip64LocLen) || int64(p)+zip64EndMinLen > endAt-zip64LocLen {
			return 0, 0, 0, refuse("not a zip archive")
		}
		z := make([]byte, zip64EndMinLen)
		if _, err := r.ReadAt(z, int64(p)); err != nil || binary.LittleEndian.Uint32(z) != zip64EndSig {
			return 0, 0, 0, refuse("not a zip archive")
		}
		// The zip64 end record (and nothing else) sits before the locator.
		if int64(p)+12+int64(binary.LittleEndian.Uint64(z[4:])) != endAt-zip64LocLen {
			return 0, 0, 0, refuse("not a zip archive")
		}
		count = binary.LittleEndian.Uint64(z[32:])
		dirSize = binary.LittleEndian.Uint64(z[40:])
		dirOffset = binary.LittleEndian.Uint64(z[48:])
		recordsAt = int64(p)
	}
	if count > MaxEntries {
		return 0, 0, 0, refuse("more than %d entries", MaxEntries)
	}
	if dirOffset > uint64(recordsAt) || dirSize != uint64(recordsAt)-dirOffset {
		return 0, 0, 0, refuse("unexpected data around the central directory")
	}
	return int64(dirOffset), recordsAt, count, nil
}

// readCentral reads the count headers of the central directory, which must
// fill [start, end) exactly.
func readCentral(r io.ReaderAt, start, end int64, count uint64) ([]centralEntry, error) {
	data := make([]byte, end-start) // bounded by paks.CheckDirectoryLimit
	if _, err := r.ReadAt(data, start); err != nil {
		return nil, refuse("not a zip archive")
	}
	var out []centralEntry
	p := 0
	for i := uint64(0); i < count; i++ {
		if p+centralLen > len(data) || binary.LittleEndian.Uint32(data[p:]) != centralSig {
			return nil, refuse("inconsistent central directory")
		}
		h := data[p:]
		e := centralEntry{
			flags: binary.LittleEndian.Uint16(h[8:]), method: binary.LittleEndian.Uint16(h[10:]),
			comp: uint64(binary.LittleEndian.Uint32(h[20:])), uncomp: uint64(binary.LittleEndian.Uint32(h[24:])),
			offset: uint64(binary.LittleEndian.Uint32(h[42:])),
		}
		nl, el, cl := int(binary.LittleEndian.Uint16(h[28:])), int(binary.LittleEndian.Uint16(h[30:])), int(binary.LittleEndian.Uint16(h[32:]))
		if binary.LittleEndian.Uint16(h[34:]) != 0 || p+centralLen+nl+el+cl > len(data) {
			return nil, refuse("inconsistent central directory")
		}
		e.name = h[centralLen : centralLen+nl]
		extra := h[centralLen+nl : centralLen+nl+el]
		// The zip64 extra field gives, in order, the values set to all ones.
		if z, ok := findExtra(extra, 0x0001); ok {
			for _, v := range []*uint64{&e.uncomp, &e.comp, &e.offset} {
				if *v == 0xffffffff {
					if len(z) < 8 {
						return nil, refuse("inconsistent central directory")
					}
					*v = binary.LittleEndian.Uint64(z)
					z = z[8:]
				}
			}
		}
		out = append(out, e)
		p += centralLen + nl + el + cl
	}
	if p != len(data) {
		return nil, refuse("unexpected data in the central directory")
	}
	return out, nil
}

// checkLocal reads the local header of e and sets its byte range: header,
// name, extra, data and, when announced, data descriptor, all before the
// central directory (dirStart).
func checkLocal(r io.ReaderAt, dirStart int64, e *centralEntry) error {
	if e.offset > uint64(dirStart) || e.comp > uint64(dirStart) {
		return refuse("entry outside the archive")
	}
	start := int64(e.offset)
	if start+localLen > dirStart {
		return refuse("entry outside the archive")
	}
	h := make([]byte, localLen)
	if _, err := r.ReadAt(h, start); err != nil || binary.LittleEndian.Uint32(h) != localSig {
		return refuse("bad local header")
	}
	flags, method := binary.LittleEndian.Uint16(h[6:]), binary.LittleEndian.Uint16(h[8:])
	comp, uncomp := uint64(binary.LittleEndian.Uint32(h[18:])), uint64(binary.LittleEndian.Uint32(h[22:]))
	nl, el := int64(binary.LittleEndian.Uint16(h[26:])), int64(binary.LittleEndian.Uint16(h[28:]))
	if method != e.method || flags&flagDescriptor != e.flags&flagDescriptor || nl != int64(len(e.name)) {
		return refuse("local and central headers differ")
	}
	if start+localLen+nl+el > dirStart {
		return refuse("entry outside the archive")
	}
	ne := make([]byte, nl+el)
	if _, err := r.ReadAt(ne, start+localLen); err != nil || !bytes.Equal(ne[:nl], e.name) {
		return refuse("local and central headers differ")
	}
	if z, ok := findExtra(ne[nl:], 0x0001); ok && (comp == 0xffffffff || uncomp == 0xffffffff) {
		if len(z) < 16 {
			return refuse("local and central headers differ")
		}
		uncomp, comp = binary.LittleEndian.Uint64(z), binary.LittleEndian.Uint64(z[8:])
	}
	// With a data descriptor, the local sizes may be zero.
	described := flags&flagDescriptor != 0
	if !(described && comp == 0 && uncomp == 0) && (comp != e.comp || uncomp != e.uncomp) {
		return refuse("local and central sizes differ")
	}
	dataStart := start + localLen + nl + el
	if e.comp > uint64(dirStart-dataStart) {
		return refuse("entry outside the archive")
	}
	end := dataStart + int64(e.comp)
	if described {
		// Signature (optional), CRC-32 and the two sizes (8 bytes each in
		// zip64).
		n := int64(12)
		if e.comp >= 0xffffffff || e.uncomp >= 0xffffffff {
			n = 20
		}
		var sig [4]byte
		at := end
		if _, err := r.ReadAt(sig[:], end); err == nil && binary.LittleEndian.Uint32(sig[:]) == descriptorSig {
			n += 4
			at += 4
		}
		if end+n > dirStart {
			return refuse("entry outside the archive")
		}
		// Its sizes are those of the central directory (Codex review).
		d := make([]byte, n-(at-end))
		if _, err := r.ReadAt(d, at); err != nil {
			return refuse("entry outside the archive")
		}
		var dc, du uint64
		if len(d) == 20 {
			dc, du = binary.LittleEndian.Uint64(d[4:]), binary.LittleEndian.Uint64(d[12:])
		} else {
			dc, du = uint64(binary.LittleEndian.Uint32(d[4:])), uint64(binary.LittleEndian.Uint32(d[8:]))
		}
		if dc != e.comp || du != e.uncomp {
			return refuse("data descriptor and central sizes differ")
		}
		end += n
	}
	e.start, e.dataEnd = start, end
	return nil
}

// findExtra returns the data of the extra field id.
func findExtra(extra []byte, id uint16) ([]byte, bool) {
	for len(extra) >= 4 {
		tag, n := binary.LittleEndian.Uint16(extra), int(binary.LittleEndian.Uint16(extra[2:]))
		if 4+n > len(extra) {
			return nil, false
		}
		if tag == id {
			return extra[4 : 4+n], true
		}
		extra = extra[4+n:]
	}
	return nil, false
}
