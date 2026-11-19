package patch

import (
	"context"
	"fmt"
	"io"
)

// BPS (byuu): "BPS1", source size, target size, metadata size (numbers),
// metadata, then actions until the 12-byte footer (CRC32 of the source, of
// the target and of the patch before its last 4 bytes). An action is a
// number: low 2 bits the command, the rest the length minus one.
//
//	SourceRead  copy the ROM at the current output offset
//	TargetRead  copy bytes from the patch
//	SourceCopy  copy the ROM from a relative (signed) source offset
//	TargetCopy  copy the target already written, from a relative (signed)
//	            target offset; it may overlap the bytes it writes (runs)

const (
	bpsSourceRead = iota
	bpsTargetRead
	bpsSourceCopy
	bpsTargetCopy
)

type bpsHeader struct {
	info    Info
	actions int64 // offset of the first action
}

func readBPSHeader(p io.ReaderAt, pSize int64, opts Options) (bpsHeader, error) {
	h := bpsHeader{info: Info{Format: BPS}}
	if pSize < int64(len(bpsMagic))+3+footerSize {
		return h, corrupt("too short")
	}
	r := newPatchReader(p, int64(len(bpsMagic)), pSize-footerSize)
	var err error
	if h.info.SourceSize, err = readVLI(r); err != nil {
		return h, err
	}
	if h.info.TargetSize, err = readVLI(r); err != nil {
		return h, err
	}
	meta, err := readVLI(r)
	if err != nil {
		return h, err
	}
	if h.info.TargetSize > opts.maxTarget() {
		return h, fmt.Errorf("%w: %d bytes", ErrTooLarge, h.info.TargetSize)
	}
	if meta > r.remaining() {
		return h, corrupt("metadata past the end")
	}
	h.actions = r.pos + meta
	return h, nil
}

func applyBPS(ctx context.Context, p io.ReaderAt, pSize int64, src io.ReaderAt, srcSize int64, dst Target, opts Options) (Info, error) {
	h, err := readBPSHeader(p, pSize, opts)
	if err != nil {
		return h.info, err
	}
	srcCRC, tgtCRC, selfCRC, err := readFooter(p, pSize)
	if err != nil {
		return h.info, err
	}
	// Patch, then ROM, before anything is written.
	if err := checkSelf(ctx, p, pSize, selfCRC); err != nil {
		return h.info, err
	}
	if err := checkSource(ctx, src, srcSize, h.info, srcCRC, tgtCRC); err != nil {
		return h.info, err
	}

	size := h.info.TargetSize
	r := newPatchReader(p, h.actions, pSize-footerSize)
	w := newTargetWriter(ctx, dst, size, opts.Progress)
	s := newSourceReader(src, srcSize)
	scratch := make([]byte, readBuffer)
	var srcRel, tgtRel int64
	for n := 0; r.remaining() > 0; n++ {
		if n%checkEvery == 0 {
			if err := ctx.Err(); err != nil {
				return h.info, err
			}
		}
		data, err := readVLI(r)
		if err != nil {
			return h.info, err
		}
		cmd, length := data&3, data>>2+1
		out := w.off()
		if length > size-out {
			return h.info, corrupt("action past the end of the target")
		}
		switch cmd {
		case bpsSourceRead:
			if out+length > srcSize {
				return h.info, corrupt("source read past the end of the ROM")
			}
			if err := copySource(w, s, out, length, scratch); err != nil {
				return h.info, err
			}
		case bpsTargetRead:
			if length > r.remaining() {
				return h.info, corrupt("truncated")
			}
			for done := int64(0); done < length; {
				k := min(int64(len(scratch)), length-done)
				if err := r.full(scratch[:k]); err != nil {
					return h.info, err
				}
				if err := w.write(scratch[:k]); err != nil {
					return h.info, err
				}
				done += k
			}
		case bpsSourceCopy, bpsTargetCopy:
			d, err := readVLI(r)
			if err != nil {
				return h.info, err
			}
			delta := d >> 1
			if d&1 != 0 {
				delta = -delta
			}
			if cmd == bpsSourceCopy {
				srcRel += delta
				if srcRel < 0 || srcRel > srcSize || length > srcSize-srcRel {
					return h.info, corrupt("source copy outside the ROM")
				}
				if err := copySource(w, s, srcRel, length, scratch); err != nil {
					return h.info, err
				}
				srcRel += length
				continue
			}
			tgtRel += delta
			if tgtRel < 0 || tgtRel >= out {
				return h.info, corrupt("target copy outside the written target")
			}
			// The distance stays the same while copying: chunks of at
			// most that distance only read bytes already written.
			dist := out - tgtRel
			for left := length; left > 0; {
				k := min(int64(len(scratch)), left, dist)
				if err := w.readAt(scratch[:k], tgtRel); err != nil {
					return h.info, err
				}
				if err := w.write(scratch[:k]); err != nil {
					return h.info, err
				}
				tgtRel += k
				left -= k
			}
		}
	}
	if w.off() != size {
		return h.info, corrupt("target is %d bytes, the patch declares %d", w.off(), size)
	}
	if err := w.finish(); err != nil {
		return h.info, err
	}
	if w.crc.Sum32() != tgtCRC {
		return h.info, corrupt("target checksum mismatch")
	}
	return h.info, nil
}
