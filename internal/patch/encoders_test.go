package patch

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
)

// Minimal IPS, BPS and UPS encoders, for the tests only: the pak never
// creates patches.

// memTarget is an in-memory Target, bounded so a fuzzed patch cannot make
// it grow without limit.
type memTarget struct {
	b     []byte
	limit int64
}

func (m *memTarget) WriteAt(p []byte, off int64) (int, error) {
	end := off + int64(len(p))
	if m.limit > 0 && end > m.limit {
		return 0, io.ErrShortWrite
	}
	if end > int64(len(m.b)) {
		m.b = append(m.b, make([]byte, end-int64(len(m.b)))...)
	}
	copy(m.b[off:], p)
	return len(p), nil
}

func (m *memTarget) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(m.b)) {
		return 0, io.EOF
	}
	n := copy(p, m.b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (m *memTarget) Truncate(size int64) error {
	if m.limit > 0 && size > m.limit {
		return io.ErrShortWrite
	}
	if size <= int64(len(m.b)) {
		m.b = m.b[:size]
	} else {
		m.b = append(m.b, make([]byte, size-int64(len(m.b)))...)
	}
	return nil
}

func at(b []byte, i int) byte {
	if i < len(b) {
		return b[i]
	}
	return 0
}

// encodeIPS diffs src and tgt into IPS records (runs of 4 equal bytes or
// more become RLE records), with the truncation extension when tgt is
// shorter. tgt must fit in 16 MiB of addresses where it differs.
func encodeIPS(src, tgt []byte) []byte {
	var out bytes.Buffer
	out.WriteString("PATCH")
	rec := func(off int, data []byte) {
		out.Write([]byte{byte(off >> 16), byte(off >> 8), byte(off)})
		if len(data) >= 4 && bytes.Count(data, data[:1]) == len(data) {
			out.Write([]byte{0, 0, byte(len(data) >> 8), byte(len(data)), data[0]})
			return
		}
		out.Write([]byte{byte(len(data) >> 8), byte(len(data))})
		out.Write(data)
	}
	n := len(tgt)
	for i := 0; i < n; {
		if i < len(src) && src[i] == tgt[i] {
			i++
			continue
		}
		start := i
		if start == ipsEOF { // this offset reads as "EOF": start one byte earlier
			start--
		}
		for i < n && (i >= len(src) || src[i] != tgt[i]) && i-start < 0xffff {
			i++
		}
		rec(start, tgt[start:i])
	}
	out.WriteString("EOF")
	if n < len(src) {
		out.Write([]byte{byte(n >> 16), byte(n >> 8), byte(n)})
	}
	return out.Bytes()
}

func vli(out *bytes.Buffer, v uint64) {
	for {
		x := byte(v & 0x7f)
		v >>= 7
		if v == 0 {
			out.WriteByte(0x80 | x)
			return
		}
		out.WriteByte(x)
		v--
	}
}

func footer(out *bytes.Buffer, src, tgt []byte) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], crc32.ChecksumIEEE(src))
	out.Write(b[:])
	binary.LittleEndian.PutUint32(b[:], crc32.ChecksumIEEE(tgt))
	out.Write(b[:])
	binary.LittleEndian.PutUint32(b[:], crc32.ChecksumIEEE(out.Bytes()))
	out.Write(b[:])
	return out.Bytes()
}

// encodeUPS diffs src and tgt into UPS hunks.
func encodeUPS(src, tgt []byte) []byte {
	var out bytes.Buffer
	out.WriteString("UPS1")
	vli(&out, uint64(len(src)))
	vli(&out, uint64(len(tgt)))
	n := max(len(src), len(tgt))
	last := 0
	for i := 0; i < n; {
		if at(src, i) == at(tgt, i) {
			i++
			continue
		}
		vli(&out, uint64(i-last))
		for i < n && at(src, i) != at(tgt, i) {
			out.WriteByte(at(src, i) ^ at(tgt, i))
			i++
		}
		out.WriteByte(0)
		i++
		last = i
	}
	return footer(&out, src, tgt)
}

// bpsEncoder builds BPS actions by hand.
type bpsEncoder struct {
	out            bytes.Buffer
	srcRel, tgtRel int
}

func newBPS(srcLen, tgtLen int, meta string) *bpsEncoder {
	e := &bpsEncoder{}
	e.out.WriteString("BPS1")
	vli(&e.out, uint64(srcLen))
	vli(&e.out, uint64(tgtLen))
	vli(&e.out, uint64(len(meta)))
	e.out.WriteString(meta)
	return e
}

func (e *bpsEncoder) action(cmd, length int) { vli(&e.out, uint64(length-1)<<2|uint64(cmd)) }

func (e *bpsEncoder) rel(delta int) {
	if delta < 0 {
		vli(&e.out, uint64(-delta)<<1|1)
	} else {
		vli(&e.out, uint64(delta)<<1)
	}
}

func (e *bpsEncoder) sourceRead(n int) { e.action(bpsSourceRead, n) }

func (e *bpsEncoder) targetRead(data []byte) {
	e.action(bpsTargetRead, len(data))
	e.out.Write(data)
}

func (e *bpsEncoder) sourceCopy(from, n int) {
	e.action(bpsSourceCopy, n)
	e.rel(from - e.srcRel)
	e.srcRel = from + n
}

func (e *bpsEncoder) targetCopy(from, n int) {
	e.action(bpsTargetCopy, n)
	e.rel(from - e.tgtRel)
	e.tgtRel = from + n
}

func (e *bpsEncoder) finish(src, tgt []byte) []byte { return footer(&e.out, src, tgt) }

// encodeBPS diffs src and tgt with every action: SourceRead where the
// bytes match at the same offset, TargetCopy for runs of a repeated byte
// (overlapping copy), SourceCopy where the next 8 bytes are found
// elsewhere in the source (sources up to 1 MiB are indexed), TargetRead
// otherwise.
func encodeBPS(src, tgt []byte) []byte {
	e := newBPS(len(src), len(tgt), "<test/>")
	index := map[string]int{}
	if len(src) <= 1<<20 {
		for i := 0; i+8 <= len(src); i++ {
			if _, ok := index[string(src[i:i+8])]; !ok {
				index[string(src[i:i+8])] = i
			}
		}
	}
	var lit []byte
	flush := func() {
		if len(lit) > 0 {
			e.targetRead(lit)
			lit = nil
		}
	}
	for o := 0; o < len(tgt); {
		n := 0
		for o+n < len(tgt) && o+n < len(src) && src[o+n] == tgt[o+n] {
			n++
		}
		if n >= 4 {
			flush()
			e.sourceRead(n)
			o += n
			continue
		}
		if o > 0 && tgt[o] == tgt[o-1] {
			n = 0
			for o+n < len(tgt) && tgt[o+n] == tgt[o-1] {
				n++
			}
			if n >= 4 {
				flush()
				e.targetCopy(o-1, n)
				o += n
				continue
			}
		}
		if o+8 <= len(tgt) {
			if from, ok := index[string(tgt[o:o+8])]; ok && from != o {
				n = 0
				for o+n < len(tgt) && from+n < len(src) && src[from+n] == tgt[o+n] {
					n++
				}
				flush()
				e.sourceCopy(from, n)
				o += n
				continue
			}
		}
		lit = append(lit, tgt[o])
		o++
	}
	flush()
	return e.finish(src, tgt)
}
