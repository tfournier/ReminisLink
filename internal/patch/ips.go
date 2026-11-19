package patch

import (
	"context"
	"fmt"
	"io"
)

// IPS: "PATCH", then records until "EOF":
//
//	offset (3 bytes, big endian) size (2 bytes)  data (size bytes)
//	offset (3 bytes)             0 (2 bytes)     count (2 bytes) value (1 byte)   RLE
//
// optionally followed by the truncation extension: 3 bytes after "EOF",
// the size of the patched file. Offsets are 24-bit: an IPS patch only
// addresses the first 16 MiB (IPSMaxAddress); bytes past it are copied
// from the ROM unchanged. An offset equal to "EOF" (0x454f46) cannot be
// written, as in every IPS tool.

// IPSMaxAddress is the first offset an IPS record cannot address (16 MiB).
const IPSMaxAddress = 1 << 24

const (
	ipsRecordHeader = 5
	ipsEOF          = 0x454f46
)

type ipsHeader struct {
	info Info
}

// ipsRecord is a record being read.
type ipsRecord struct {
	off  int64
	size int64 // data bytes, or the RLE count
	rle  bool
	val  byte
}

// nextIPS reads the next record: ok false at "EOF". trunc is the size given
// by the truncation extension, -1 when absent.
func nextIPS(r *patchReader) (rec ipsRecord, ok bool, trunc int64, err error) {
	var b [3]byte
	if err := r.full(b[:]); err != nil {
		return rec, false, -1, corrupt("no EOF marker")
	}
	off := int64(b[0])<<16 | int64(b[1])<<8 | int64(b[2])
	if off == ipsEOF {
		switch r.remaining() {
		case 0:
			return rec, false, -1, nil
		case 3:
			if err := r.full(b[:]); err != nil {
				return rec, false, -1, err
			}
			return rec, false, int64(b[0])<<16 | int64(b[1])<<8 | int64(b[2]), nil
		default:
			return rec, false, -1, corrupt("%d unexpected bytes after EOF", r.remaining())
		}
	}
	var s [2]byte
	if err := r.full(s[:]); err != nil {
		return rec, false, -1, err
	}
	rec.off, rec.size = off, int64(s[0])<<8|int64(s[1])
	if rec.size == 0 {
		var c [3]byte
		if err := r.full(c[:]); err != nil {
			return rec, false, -1, err
		}
		rec.rle, rec.size, rec.val = true, int64(c[0])<<8|int64(c[1]), c[2]
	}
	return rec, true, -1, nil
}

// scanIPS validates the records and computes the size of the patched file:
// the truncation size when given, else the larger of the ROM and the end
// of the last byte written.
func scanIPS(ctx context.Context, p io.ReaderAt, pSize, srcSize int64, opts Options) (ipsHeader, error) {
	h := ipsHeader{info: Info{Format: IPS, SourceSize: -1}}
	if pSize < int64(len(ipsMagic))+3 {
		return h, corrupt("too short")
	}
	r := newPatchReader(p, int64(len(ipsMagic)), pSize)
	end := srcSize
	for n := 0; ; n++ {
		if n%checkEvery == 0 {
			if err := ctx.Err(); err != nil {
				return h, err
			}
		}
		rec, ok, trunc, err := nextIPS(r)
		if err != nil {
			return h, err
		}
		if !ok {
			if trunc >= 0 {
				end = trunc
			}
			break
		}
		if !rec.rle {
			if err := r.discard(rec.size); err != nil {
				return h, err
			}
		}
		end = max(end, rec.off+rec.size)
	}
	if end > opts.maxTarget() {
		return h, fmt.Errorf("%w: %d bytes", ErrTooLarge, end)
	}
	h.info.TargetSize = end
	return h, nil
}

func applyIPS(ctx context.Context, p io.ReaderAt, pSize int64, src io.ReaderAt, srcSize int64, dst Target, opts Options) (Info, error) {
	h, err := scanIPS(ctx, p, pSize, srcSize, opts)
	if err != nil {
		return h.info, err
	}
	size := h.info.TargetSize
	// 1. The ROM, up to the size of the patched file.
	w := newTargetWriter(ctx, dst, size, opts.Progress)
	s := newSourceReader(src, srcSize)
	scratch := make([]byte, readBuffer)
	if err := copySource(w, s, 0, min(srcSize, size), scratch); err != nil {
		return h.info, err
	}
	if err := w.flush(); err != nil {
		return h.info, err
	}
	// Past the ROM (the patch makes it longer): zeros until written.
	if err := dst.Truncate(size); err != nil {
		return h.info, fmt.Errorf("truncate patched file: %w", err)
	}
	// 2. The records, clipped to the size (truncation extension).
	r := newPatchReader(p, int64(len(ipsMagic)), pSize)
	for n := 0; ; n++ {
		if n%checkEvery == 0 {
			if err := ctx.Err(); err != nil {
				return h.info, err
			}
		}
		rec, ok, _, err := nextIPS(r)
		if err != nil {
			return h.info, err
		}
		if !ok {
			break
		}
		keep := max(min(rec.size, size-rec.off), 0)
		if rec.rle {
			for i := range scratch[:min(int64(len(scratch)), keep)] {
				scratch[i] = rec.val
			}
			for done := int64(0); done < keep; {
				k := min(int64(len(scratch)), keep-done)
				if _, err := dst.WriteAt(scratch[:k], rec.off+done); err != nil {
					return h.info, fmt.Errorf("write patched file: %w", err)
				}
				done += k
			}
			continue
		}
		data := scratch[:rec.size] // size <= 0xffff < len(scratch)
		if err := r.full(data); err != nil {
			return h.info, err
		}
		if keep > 0 {
			if _, err := dst.WriteAt(data[:keep], rec.off); err != nil {
				return h.info, fmt.Errorf("write patched file: %w", err)
			}
		}
	}
	if opts.Progress != nil {
		opts.Progress(size, size)
	}
	return h.info, nil
}
