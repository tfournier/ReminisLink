package patch

// Reference decoders for a differential check: written the simplest way,
// a byte at a time on slices (beat semantics for BPS and UPS, Flips
// semantics for IPS, with the stricter rules of this package: tail after
// EOF, truncation that cannot lengthen, UPS skip bound). Each returns
// ok=false where Apply must fail. Generators of random valid patches are
// here too (adapted from the M6 review's checker).

import (
	"bytes"
	"context"
	"errors"
	"hash/crc32"
	"math/rand/v2"
	"testing"
)

// refReader reads a patch slice with bounds checks.
type refReader struct {
	p   []byte
	i   int
	end int
	bad bool
}

func (r *refReader) byte() byte {
	if r.i >= r.end {
		r.bad = true
		return 0
	}
	b := r.p[r.i]
	r.i++
	return b
}

func (r *refReader) vli() uint64 {
	var data, shift uint64 = 0, 1
	for n := 0; ; n++ {
		if n >= maxVLIBytes {
			r.bad = true
			return 0
		}
		x := r.byte()
		if r.bad {
			return 0
		}
		data += uint64(x&0x7f) * shift
		if x&0x80 != 0 {
			return data
		}
		shift <<= 7
		data += shift
	}
}

func refBPS(p, src []byte, maxTarget int) ([]byte, bool) {
	if len(p) < 4+3+12 || string(p[:4]) != bpsMagic {
		return nil, false
	}
	r := &refReader{p: p, i: 4, end: len(p) - 12}
	ssz, tsz, meta := r.vli(), r.vli(), r.vli()
	if r.bad || tsz > uint64(maxTarget) || ssz != uint64(len(src)) || meta > uint64(r.end-r.i) {
		return nil, false
	}
	r.i += int(meta)
	tgt := make([]byte, 0, tsz)
	sRel, tRel := 0, 0
	for r.i < r.end {
		d := r.vli()
		if r.bad {
			return nil, false
		}
		cmd, n := d&3, d>>2+1
		if n > tsz-uint64(len(tgt)) {
			return nil, false
		}
		switch cmd {
		case 0:
			for ; n > 0; n-- {
				if len(tgt) >= len(src) {
					return nil, false
				}
				tgt = append(tgt, src[len(tgt)])
			}
		case 1:
			for ; n > 0; n-- {
				tgt = append(tgt, r.byte())
			}
		default:
			o := r.vli()
			delta := int(o >> 1)
			if o&1 != 0 {
				delta = -delta
			}
			if cmd == 2 {
				sRel += delta
				for ; n > 0; n-- {
					if sRel < 0 || sRel >= len(src) {
						return nil, false
					}
					tgt = append(tgt, src[sRel])
					sRel++
				}
			} else {
				tRel += delta
				for ; n > 0; n-- {
					if tRel < 0 || tRel >= len(tgt) {
						return nil, false
					}
					tgt = append(tgt, tgt[tRel])
					tRel++
				}
			}
		}
		if r.bad {
			return nil, false
		}
	}
	return tgt, uint64(len(tgt)) == tsz
}

func refUPS(p, src []byte, maxTarget int) ([]byte, bool) {
	if len(p) < 4+2+12 || string(p[:4]) != upsMagic {
		return nil, false
	}
	r := &refReader{p: p, i: 4, end: len(p) - 12}
	ssz, tsz := r.vli(), r.vli()
	if r.bad || tsz > uint64(maxTarget) || ssz != uint64(len(src)) {
		return nil, false
	}
	tgt := make([]byte, tsz)
	in := func(k int) byte {
		if k < len(src) {
			return src[k]
		}
		return 0
	}
	limit := int(tsz) // skips clamped there, as in applyUPS
	for k := range tgt {
		tgt[k] = in(k)
	}
	pos := 0
	for r.i < r.end {
		skip := r.vli()
		if r.bad {
			return nil, false
		}
		pos += int(min(skip, uint64(max(limit-pos, 0))))
		for {
			x := r.byte()
			if r.bad {
				return nil, false
			}
			if pos < len(tgt) {
				tgt[pos] = x ^ in(pos)
			}
			pos++
			if x == 0 {
				break
			}
		}
	}
	return tgt, true
}

func refIPS(p, src []byte, maxTarget int) ([]byte, bool) {
	if len(p) < 8 || string(p[:5]) != ipsMagic {
		return nil, false
	}
	type rec struct {
		off  int
		data []byte
	}
	var recs []rec
	end := len(src)
	i := 5
	need := func(n int) bool { return i+n <= len(p) }
	for {
		if !need(3) {
			return nil, false
		}
		off := int(p[i])<<16 | int(p[i+1])<<8 | int(p[i+2])
		i += 3
		if off == ipsEOF {
			break
		}
		if !need(2) {
			return nil, false
		}
		n := int(p[i])<<8 | int(p[i+1])
		i += 2
		var d []byte
		if n == 0 {
			if !need(3) {
				return nil, false
			}
			d = bytes.Repeat([]byte{p[i+2]}, int(p[i])<<8|int(p[i+1]))
			i += 3
		} else {
			if !need(n) {
				return nil, false
			}
			d = p[i : i+n]
			i += n
		}
		recs = append(recs, rec{off, d})
		end = max(end, off+len(d))
	}
	switch len(p) - i {
	case 0:
	case 3:
		t := int(p[i])<<16 | int(p[i+1])<<8 | int(p[i+2])
		if t > end {
			return nil, false
		}
		end = t
	default:
		return nil, false
	}
	if end > maxTarget {
		return nil, false
	}
	out := make([]byte, max(end, len(src)))
	copy(out, src)
	for _, r := range recs {
		for k, b := range r.data {
			if r.off+k < end {
				out[r.off+k] = b
			}
		}
	}
	return out[:end], true
}

// --- random valid patches --------------------------------------------------

func genBPS(r *rand.Rand, src []byte) ([]byte, []byte) {
	tsz := r.IntN(3*len(src)+20) + 1
	meta := ""
	if r.IntN(2) == 0 {
		meta = "<?xml version=\"1.0\"?><patch/>"
	}
	e := newBPS(len(src), tsz, meta)
	tgt := make([]byte, 0, tsz)
	for len(tgt) < tsz {
		n := r.IntN(min(tsz-len(tgt), 300)) + 1
		switch r.IntN(4) {
		case 0:
			if out := len(tgt); out+n <= len(src) {
				e.sourceRead(n)
				tgt = append(tgt, src[out:out+n]...)
			}
		case 1:
			data := make([]byte, n)
			for k := range data {
				data[k] = byte(r.IntN(256))
			}
			e.targetRead(data)
			tgt = append(tgt, data...)
		case 2:
			if n <= len(src) {
				from := r.IntN(len(src) - n + 1)
				e.sourceCopy(from, n)
				tgt = append(tgt, src[from:from+n]...)
			}
		case 3:
			if len(tgt) == 0 {
				continue
			}
			from := r.IntN(len(tgt))
			if r.IntN(2) == 0 { // short distances: runs
				from = len(tgt) - 1 - r.IntN(min(len(tgt), 4))
			}
			e.targetCopy(from, n)
			for k := 0; k < n; k++ {
				tgt = append(tgt, tgt[from+k])
			}
		}
	}
	return e.finish(src, tgt), tgt
}

// genUPSBody returns a random UPS patch without its footer.
func genUPSBody(r *rand.Rand, src []byte) []byte {
	tsz := r.IntN(2*len(src)+20) + 1
	var b bytes.Buffer
	b.WriteString(upsMagic)
	vli(&b, uint64(len(src)))
	vli(&b, uint64(tsz))
	pos, lim := 0, max(len(src), tsz)
	for pos < lim && r.IntN(20) != 0 {
		skip := r.IntN(min(lim-pos, 200) + 1)
		vli(&b, uint64(skip))
		pos += skip
		for k := r.IntN(50); k > 0; k-- {
			b.WriteByte(byte(r.IntN(255) + 1))
			pos++
		}
		b.WriteByte(0)
		pos++
	}
	return b.Bytes()
}

func genIPS(r *rand.Rand, src []byte) []byte {
	var b bytes.Buffer
	b.WriteString(ipsMagic)
	for k := r.IntN(30); k > 0; k-- {
		off := r.IntN(len(src) + 500)
		b.Write([]byte{byte(off >> 16), byte(off >> 8), byte(off)})
		if r.IntN(3) == 0 {
			c := r.IntN(400)
			b.Write([]byte{0, 0, byte(c >> 8), byte(c), byte(r.IntN(256))})
		} else {
			n := r.IntN(300) + 1
			b.Write([]byte{byte(n >> 8), byte(n)})
			for ; n > 0; n-- {
				b.WriteByte(byte(r.IntN(256)))
			}
		}
	}
	b.WriteString("EOF")
	if r.IntN(3) == 0 {
		t := r.IntN(len(src) + 600) // sometimes larger: refused
		b.Write([]byte{byte(t >> 16), byte(t >> 8), byte(t)})
	}
	return b.Bytes()
}

// withFooter appends a footer whose CRC32 are those of src and tgt.
func withFooter(body, src, tgt []byte) []byte {
	var b bytes.Buffer
	b.Write(body)
	return footer(&b, src, tgt)
}

// sameAsReference applies p and compares with the reference result.
func sameAsReference(t *testing.T, p, src, want []byte, ok bool, maxTarget int) {
	t.Helper()
	dst := &memTarget{limit: int64(maxTarget)}
	_, err := Apply(context.Background(), bytes.NewReader(p), int64(len(p)), bytes.NewReader(src), int64(len(src)), dst, Options{MaxTargetSize: int64(maxTarget)})
	switch {
	case !ok && err == nil:
		t.Fatalf("accepted a patch the reference refuses (%d bytes)", len(p))
	case ok && err != nil:
		t.Fatalf("refused a patch the reference accepts: %v", err)
	case ok && !bytes.Equal(dst.b, want):
		t.Fatalf("result differs from the reference: %d bytes, want %d", len(dst.b), len(want))
	}
}

func TestReferenceDifferential(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	const maxTarget = 1 << 20
	for it := 0; it < 3000; it++ {
		src := randomBytes(uint64(it), r.IntN(5000)+1)
		// BPS: the generator knows the target; the reference agrees.
		p, tgt := genBPS(r, src)
		if got, ok := refBPS(p, src, maxTarget); !ok || !bytes.Equal(got, tgt) {
			t.Fatalf("it %d: reference BPS disagrees with the generator", it)
		}
		sameAsReference(t, p, src, tgt, true, maxTarget)
		// UPS: the reference computes the target, then the footer.
		body := genUPSBody(r, src)
		want, ok := refUPS(withFooter(body, nil, nil), src, maxTarget)
		if !ok {
			t.Fatalf("it %d: generated UPS refused by the reference", it)
		}
		up := withFooter(body, src, want)
		if !bytes.Equal(want, src) {
			sameAsReference(t, up, src, want, true, maxTarget)
			// Backwards: refused.
			if len(want) != len(src) || crc32.ChecksumIEEE(want) != crc32.ChecksumIEEE(src) {
				if _, _, err := apply(t, up, want); !errors.Is(err, ErrAlreadyPatched) {
					t.Fatalf("it %d: UPS backwards: %v", it, err)
				}
			}
		}
		// IPS, including truncations larger than the result (refused).
		ip := genIPS(r, src)
		want, ok = refIPS(ip, src, maxTarget)
		sameAsReference(t, ip, src, want, ok, maxTarget)
	}
}

// TestTargetCopyRuns: overlapping copies at distances 1 to 5, longer than
// the copy buffer, against the reference.
func TestTargetCopyRuns(t *testing.T) {
	src := randomBytes(20, 64)
	for dist := 1; dist <= 5; dist++ {
		n := 3*readBuffer + 17
		e := newBPS(len(src), dist+n, "")
		e.targetRead(src[:dist])
		e.targetCopy(0, n)
		tgt := make([]byte, 0, dist+n)
		tgt = append(tgt, src[:dist]...)
		for k := 0; k < n; k++ {
			tgt = append(tgt, tgt[k])
		}
		p := e.finish(src, tgt)
		want, ok := refBPS(p, src, 1<<20)
		if !ok || !bytes.Equal(want, tgt) {
			t.Fatalf("dist %d: reference", dist)
		}
		sameAsReference(t, p, src, tgt, true, 1<<20)
	}
}
