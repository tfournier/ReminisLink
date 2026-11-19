package patch

import (
	"context"
	"fmt"
	"io"
)

// UPS (byuu): "UPS1", source size, target size (numbers), then hunks until
// the 12-byte footer (CRC32 of the source, of the target and of the patch
// before its last 4 bytes). A hunk is a number of bytes to keep, then XOR
// bytes applied to the following bytes, ended by a 0 byte (which also
// counts as one kept byte). Bytes past the end of the ROM read as zero;
// bytes written past the target size are ignored, as in the reference
// implementation.
//
// UPS is symmetric: applied to its target it gives back the source. A ROM
// that is the target is refused (ErrAlreadyPatched), never patched
// backwards.

type upsHeader struct {
	info  Info
	hunks int64 // offset of the first hunk
}

func readUPSHeader(p io.ReaderAt, pSize int64, opts Options) (upsHeader, error) {
	h := upsHeader{info: Info{Format: UPS}}
	if pSize < int64(len(upsMagic))+2+footerSize {
		return h, corrupt("too short")
	}
	r := newPatchReader(p, int64(len(upsMagic)), pSize-footerSize)
	var err error
	if h.info.SourceSize, err = readVLI(r); err != nil {
		return h, err
	}
	if h.info.TargetSize, err = readVLI(r); err != nil {
		return h, err
	}
	if h.info.TargetSize > opts.maxTarget() {
		return h, fmt.Errorf("%w: %d bytes", ErrTooLarge, h.info.TargetSize)
	}
	h.hunks = r.pos
	return h, nil
}

func applyUPS(ctx context.Context, p io.ReaderAt, pSize int64, src io.ReaderAt, srcSize int64, dst Target, opts Options) (Info, error) {
	h, err := readUPSHeader(p, pSize, opts)
	if err != nil {
		return h.info, err
	}
	srcCRC, tgtCRC, selfCRC, err := readFooter(p, pSize)
	if err != nil {
		return h.info, err
	}
	info := withWork(h.info, pSize, srcSize)
	m := &meter{fn: opts.Progress, total: info.Work}
	if err := checkSelf(ctx, p, pSize, selfCRC, m); err != nil {
		return info, err
	}
	if err := checkSource(ctx, src, srcSize, info, srcCRC, tgtCRC, m); err != nil {
		return info, err
	}

	size := info.TargetSize
	r := newPatchReader(p, h.hunks, pSize-footerSize)
	w := newTargetWriter(ctx, dst, m)
	s := newSourceReader(src, srcSize)
	scratch := make([]byte, readBuffer)
	// pos is the offset in the source and the target. Past the target
	// size nothing is written any more (ignored writes, as in beat), so a
	// skip is clamped at the target size: pos stays below size + pSize and
	// cannot overflow, the result is the same.
	limit := size
	var pos int64
	var one [1]byte
	for n := 0; r.remaining() > 0; n++ {
		if n%checkEvery == 0 {
			if err := ctx.Err(); err != nil {
				return info, err
			}
		}
		skip, err := readVLI(r)
		if err != nil {
			return info, err
		}
		if keep := max(min(skip, size-pos), 0); keep > 0 {
			if err := copySource(w, s, pos, keep, scratch); err != nil {
				return info, err
			}
		}
		pos += min(skip, max(limit-pos, 0))
		for {
			x, err := r.ReadByte()
			if err != nil {
				return info, err
			}
			if pos < size {
				if err := s.readAt(one[:], pos); err != nil {
					return info, err
				}
				if err := w.writeByte(one[0] ^ x); err != nil {
					return info, err
				}
			}
			pos++
			if x == 0 {
				break
			}
		}
	}
	if pos < size {
		if err := copySource(w, s, pos, size-pos, scratch); err != nil {
			return info, err
		}
	}
	if w.off() != size {
		return info, corrupt("target is %d bytes, the patch declares %d", w.off(), size)
	}
	if err := w.finish(); err != nil {
		return info, err
	}
	if w.crc.Sum32() != tgtCRC {
		return info, corrupt("target checksum mismatch")
	}
	info.TargetCRC32 = tgtCRC
	return info, nil
}
