package patch

import (
	"bytes"
	"context"
	"errors"
	"hash/crc32"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

func randomBytes(seed uint64, n int) []byte {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

// variants returns targets made from src: changed bytes, a run, a block
// moved, longer and shorter.
func variants(src []byte) map[string][]byte {
	changed := bytes.Clone(src)
	for i := 3; i < len(changed); i += 97 {
		changed[i] ^= 0x5a
	}
	run := bytes.Clone(src)
	for i := 100; i < 400 && i < len(run); i++ {
		run[i] = 0xff
	}
	moved := bytes.Clone(src)
	if len(src) > 2000 {
		copy(moved[1500:], src[200:700])
	}
	longer := append(bytes.Clone(changed), randomBytes(7, 777)...)
	longer = append(longer, make([]byte, 300)...) // ends with zeros
	shorter := bytes.Clone(changed[:len(changed)*2/3])
	return map[string][]byte{"changed": changed, "run": run, "moved": moved, "longer": longer, "shorter": shorter, "same": bytes.Clone(src)}
}

type encoder func(src, tgt []byte) []byte

var encoders = map[Format]encoder{IPS: encodeIPS, BPS: encodeBPS, UPS: encodeUPS}

func apply(t *testing.T, p, src []byte) ([]byte, Info, error) {
	t.Helper()
	dst := &memTarget{}
	info, err := Apply(context.Background(), bytes.NewReader(p), int64(len(p)), bytes.NewReader(src), int64(len(src)), dst, Options{})
	return dst.b, info, err
}

func TestApplyAllFormats(t *testing.T) {
	src := randomBytes(1, 5000)
	for format, enc := range encoders {
		for name, tgt := range variants(src) {
			t.Run(string(format)+"/"+name, func(t *testing.T) {
				p := enc(src, tgt)
				if f, err := Detect(p); err != nil || f != format {
					t.Fatalf("Detect: %v %v", f, err)
				}
				info, err := Inspect(context.Background(), bytes.NewReader(p), int64(len(p)), int64(len(src)), Options{})
				if err != nil || info.TargetSize != int64(len(tgt)) || info.Format != format {
					t.Fatalf("Inspect: %+v %v", info, err)
				}
				got, info, err := apply(t, p, src)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, tgt) {
					t.Fatalf("patched file differs (%d bytes, want %d)", len(got), len(tgt))
				}
				if info.TargetSize != int64(len(tgt)) {
					t.Fatalf("info %+v", info)
				}
			})
		}
	}
}

func TestApplyToFile(t *testing.T) {
	// The real Target is an *os.File: written, read back (TargetCopy),
	// truncated.
	src := randomBytes(2, 300<<10)
	tgt := variants(src)["run"]
	tgt = append(tgt, bytes.Repeat([]byte{0xab}, 600<<10)...) // TargetCopy over several write buffers
	for format, enc := range encoders {
		p := enc(src, tgt)
		f, err := os.Create(filepath.Join(t.TempDir(), "out"))
		if err != nil {
			t.Fatal(err)
		}
		var calls int
		_, err = Apply(context.Background(), bytes.NewReader(p), int64(len(p)), bytes.NewReader(src), int64(len(src)), f,
			Options{Progress: func(done, total int64) {
				calls++
				if done > total {
					t.Errorf("progress %d > %d", done, total)
				}
			}})
		f.Close()
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		got, _ := os.ReadFile(f.Name())
		if !bytes.Equal(got, tgt) {
			t.Fatalf("%s: patched file differs", format)
		}
		if calls == 0 {
			t.Errorf("%s: no progress", format)
		}
	}
}

func TestWrongSource(t *testing.T) {
	src := randomBytes(3, 4096)
	tgt := variants(src)["changed"]
	other := bytes.Clone(src)
	other[10] ^= 1
	for _, format := range []Format{BPS, UPS} {
		p := encoders[format](src, tgt)
		if _, _, err := apply(t, p, other); !errors.Is(err, ErrSourceMismatch) {
			t.Errorf("%s, other ROM same size: %v", format, err)
		}
		if _, _, err := apply(t, p, src[:4000]); !errors.Is(err, ErrSourceMismatch) {
			t.Errorf("%s, other size: %v", format, err)
		}
		// The ROM is already the target: refused, not patched backwards.
		if _, _, err := apply(t, p, tgt); !errors.Is(err, ErrAlreadyPatched) {
			t.Errorf("%s, already patched: %v", format, err)
		}
	}
	// Different sizes: still recognised as the target.
	longer := variants(src)["longer"]
	for _, format := range []Format{BPS, UPS} {
		p := encoders[format](src, longer)
		if _, _, err := apply(t, p, longer); !errors.Is(err, ErrAlreadyPatched) {
			t.Errorf("%s, already patched (longer): %v", format, err)
		}
	}
}

func TestTruncatedPatch(t *testing.T) {
	src := randomBytes(4, 3000)
	tgt := variants(src)["changed"]
	for format, enc := range encoders {
		p := enc(src, tgt)
		for _, cut := range []int{len(p) - 1, len(p) - 3, len(p) - 13, len(p) / 2, 6, 5, 4, 1, 0} {
			if cut < 0 {
				continue
			}
			_, _, err := apply(t, p[:cut], src)
			if err == nil {
				t.Errorf("%s cut at %d: accepted", format, cut)
				continue
			}
			if !errors.Is(err, ErrCorrupt) && !errors.Is(err, ErrUnsupported) {
				t.Errorf("%s cut at %d: %v", format, cut, err)
			}
		}
	}
}

func TestPatchChecksum(t *testing.T) {
	src := randomBytes(5, 3000)
	tgt := variants(src)["changed"]
	for _, format := range []Format{BPS, UPS} {
		p := encoders[format](src, tgt)
		bad := bytes.Clone(p)
		bad[len(bad)-1] ^= 0xff // patch CRC
		if _, _, err := apply(t, bad, src); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s, wrong patch CRC: %v", format, err)
		}
		bad = bytes.Clone(p)
		bad[len(bad)/2] ^= 0x01 // a byte of the body: the patch CRC catches it
		if _, _, err := apply(t, bad, src); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s, changed body: %v", format, err)
		}
	}
	// A wrong target CRC with a consistent patch CRC: caught at the end.
	p := encodeBPS(src, tgt)
	body := bytes.Clone(p[:len(p)-12])
	var wrong bytes.Buffer
	wrong.Write(body)
	fake := bytes.Clone(tgt)
	fake[0] ^= 1
	if _, _, err := apply(t, footer(&wrong, src, fake), src); !errors.Is(err, ErrCorrupt) {
		t.Errorf("BPS, wrong target CRC: %v", err)
	}
}

func TestUnsupported(t *testing.T) {
	for _, p := range [][]byte{nil, []byte("PATC"), []byte("BPS2...."), []byte("hello world"), []byte("ips\x00")} {
		if _, _, err := apply(t, p, []byte("rom")); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%q: %v", p, err)
		}
	}
}

func TestIPSDetails(t *testing.T) {
	src := randomBytes(6, 100)
	t.Run("rle", func(t *testing.T) {
		p := []byte("PATCH\x00\x00\x10\x00\x00\x00\x05\xee\x00\x00\x50\x00\x02\xaa\xbbEOF")
		got, _, err := apply(t, p, src)
		if err != nil {
			t.Fatal(err)
		}
		want := bytes.Clone(src)
		copy(want[0x10:], bytes.Repeat([]byte{0xee}, 5))
		want[0x50], want[0x51] = 0xaa, 0xbb
		if !bytes.Equal(got, want) {
			t.Fatal("rle")
		}
	})
	t.Run("extends", func(t *testing.T) {
		p := []byte("PATCH\x00\x00\xc8\x00\x01\x42EOF") // writes at 200 > 100
		got, _, err := apply(t, p, src)
		if err != nil || len(got) != 201 || got[200] != 0x42 || got[150] != 0 || !bytes.Equal(got[:100], src) {
			t.Fatalf("%d %v", len(got), err)
		}
	})
	t.Run("truncation", func(t *testing.T) {
		p := []byte("PATCH\x00\x00\x01\x00\x01\x42EOF\x00\x00\x20")
		got, _, err := apply(t, p, src)
		if err != nil || len(got) != 0x20 || got[1] != 0x42 || !bytes.Equal(got[2:], src[2:0x20]) {
			t.Fatalf("%d %v", len(got), err)
		}
		// A record past the truncation is clipped.
		p = []byte("PATCH\x00\x00\x1e\x00\x04ABCDEOF\x00\x00\x20")
		got, _, err = apply(t, p, src)
		if err != nil || string(got[0x1e:]) != "AB" {
			t.Fatalf("%q %v", got[0x1e:], err)
		}
	})
	t.Run("junk after EOF", func(t *testing.T) {
		for _, tail := range []string{"\x00", "\x00\x00", "\x00\x00\x00\x00"} {
			if _, _, err := apply(t, []byte("PATCHEOF"+tail), src); !errors.Is(err, ErrCorrupt) {
				t.Errorf("%q: %v", tail, err)
			}
		}
	})
	t.Run("no EOF", func(t *testing.T) {
		if _, _, err := apply(t, []byte("PATCH\x00\x00\x01\x00\x01\x42"), src); !errors.Is(err, ErrCorrupt) {
			t.Error(err)
		}
	})
	t.Run("record cut", func(t *testing.T) {
		if _, _, err := apply(t, []byte("PATCH\x00\x00\x01\x00\x05ABEOF"), src); !errors.Is(err, ErrCorrupt) {
			t.Error(err)
		}
	})
}

// TestIPSLargeROM: a ROM larger than 16 MiB keeps its bytes past the IPS
// address limit; records write below it, up to the last address.
func TestIPSLargeROM(t *testing.T) {
	if testing.Short() {
		t.Skip("17 MiB")
	}
	src := randomBytes(8, IPSMaxAddress+1<<20)
	tgt := bytes.Clone(src)
	tgt[5] ^= 1
	tgt[IPSMaxAddress-1] ^= 1 // last addressable byte
	p := encodeIPS(src, tgt)
	got, info, err := apply(t, p, src)
	if err != nil || !bytes.Equal(got, tgt) || info.TargetSize != int64(len(src)) {
		t.Fatalf("%v %+v", err, info)
	}
	// A record never writes past 16 MiB (Codex review of M6): one byte at
	// the last address is fine, two bytes (normal or RLE) are refused.
	small := randomBytes(9, 10)
	got, _, err = apply(t, []byte("PATCH\xff\xff\xff\x00\x01\x07EOF"), small)
	if err != nil || len(got) != IPSMaxAddress || got[IPSMaxAddress-1] != 7 {
		t.Fatalf("last address: %d %v", len(got), err)
	}
	for _, p := range [][]byte{
		[]byte("PATCH\xff\xff\xff\x00\x02\x01\x02EOF"),     // normal record
		[]byte("PATCH\xff\xff\xfe\x00\x00\x00\x03\x09EOF"), // RLE record
	} {
		if _, _, err := apply(t, p, small); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("record past 16 MiB: %v", err)
		}
	}
	// The record at offset 0x454f46 ("EOF") is avoided by the encoder.
	tgt = bytes.Clone(src)
	tgt[ipsEOF] ^= 1
	got, _, err = apply(t, encodeIPS(src, tgt), src)
	if err != nil || !bytes.Equal(got, tgt) {
		t.Fatalf("EOF offset: %v", err)
	}
}

func TestTargetTooLarge(t *testing.T) {
	src := randomBytes(10, 100)
	tgt := randomBytes(11, 2000)
	for format, enc := range encoders {
		p := enc(src, tgt)
		_, err := Inspect(context.Background(), bytes.NewReader(p), int64(len(p)), int64(len(src)), Options{MaxTargetSize: 1000})
		if !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s Inspect: %v", format, err)
		}
		dst := &memTarget{}
		_, err = Apply(context.Background(), bytes.NewReader(p), int64(len(p)), bytes.NewReader(src), 100, dst, Options{MaxTargetSize: 1000})
		if !errors.Is(err, ErrTooLarge) || len(dst.b) != 0 {
			t.Errorf("%s Apply: %v (%d bytes written)", format, err, len(dst.b))
		}
	}
	// A BPS header declaring 2^50 bytes is refused before anything.
	e := newBPS(100, 1<<50, "")
	p := e.finish(src, tgt)
	if _, _, err := apply(t, p, src); !errors.Is(err, ErrTooLarge) {
		t.Errorf("huge BPS: %v", err)
	}
}

func TestVLI(t *testing.T) {
	for _, v := range []uint64{0, 1, 127, 128, 129, 16383, 16384, 1 << 32, 1 << 50} {
		var b bytes.Buffer
		vli(&b, v)
		got, err := readVLI(&b)
		if err != nil || uint64(got) != v {
			t.Errorf("%d: %d %v", v, got, err)
		}
	}
	// Too long: refused, no overflow.
	if _, err := readVLI(bytes.NewReader(bytes.Repeat([]byte{0}, 20))); !errors.Is(err, ErrCorrupt) {
		t.Error(err)
	}
}

func TestBPSCopiesOutOfRange(t *testing.T) {
	src := randomBytes(12, 64)
	cases := map[string]func(e *bpsEncoder){
		"source read past ROM": func(e *bpsEncoder) { e.sourceRead(65) },
		"source copy before 0": func(e *bpsEncoder) { e.sourceCopy(-1, 4) },
		"source copy past ROM": func(e *bpsEncoder) { e.sourceCopy(62, 4) },
		"target copy first":    func(e *bpsEncoder) { e.targetCopy(0, 4) },
		"target copy ahead":    func(e *bpsEncoder) { e.targetRead([]byte{1}); e.targetCopy(1, 4) },
		"past target":          func(e *bpsEncoder) { e.targetRead(make([]byte, 101)) },
		"short target":         func(e *bpsEncoder) { e.targetRead(make([]byte, 99)) },
	}
	for name, build := range cases {
		e := newBPS(len(src), 100, "")
		build(e)
		p := e.finish(src, make([]byte, 100))
		if _, _, err := apply(t, p, src); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCancel(t *testing.T) {
	src := randomBytes(13, 2<<20)
	tgt := variants(src)["changed"]
	for format, enc := range encoders {
		p := enc(src, tgt)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := Apply(ctx, bytes.NewReader(p), int64(len(p)), bytes.NewReader(src), int64(len(src)), &memTarget{}, Options{})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("%s: %v", format, err)
		}
	}
}

func TestIPSTruncationCannotLengthen(t *testing.T) {
	src := randomBytes(30, 100)
	// The result is 100 bytes (the ROM; the record ends at 0x22): a
	// truncation to 0x30 shortens it, one to 0x80 would lengthen it.
	if got, _, err := apply(t, []byte("PATCH\x00\x00\x20\x00\x02ABEOF\x00\x00\x30"), src); err != nil || len(got) != 0x30 {
		t.Fatalf("shorten: %d %v", len(got), err)
	}
	if _, _, err := apply(t, []byte("PATCH\x00\x00\x20\x00\x02ABEOF\x00\x00\x80"), src); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("lengthen: %v", err)
	}
	// Equal to the size: accepted (nothing to cut).
	if got, _, err := apply(t, []byte("PATCHEOF\x00\x00\x64"), src); err != nil || !bytes.Equal(got, src) {
		t.Fatalf("same size: %v", err)
	}
}

func TestInspectCancel(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("PATCH")
	for i := 0; i < 3*checkEvery; i++ {
		b.WriteString("\x00\x00\x01\x00\x01\x42")
	}
	b.WriteString("EOF")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Inspect(ctx, bytes.NewReader(b.Bytes()), int64(b.Len()), 10, Options{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// eofReader returns io.EOF with the last bytes, as io.ReaderAt allows.
type eofReader struct{ *bytes.Reader }

func (r eofReader) ReadAt(p []byte, off int64) (int, error) {
	n, err := r.Reader.ReadAt(p, off)
	if err == nil && off+int64(n) == r.Size() {
		err = io.EOF
	}
	return n, err
}

func TestReaderEOFWithLastBytes(t *testing.T) {
	src := randomBytes(31, 3000)
	tgt := variants(src)["changed"]
	for _, format := range []Format{BPS, UPS} {
		p := encoders[format](src, tgt)
		dst := &memTarget{}
		_, err := Apply(context.Background(), eofReader{bytes.NewReader(p)}, int64(len(p)), eofReader{bytes.NewReader(src)}, int64(len(src)), dst, Options{})
		if err != nil || !bytes.Equal(dst.b, tgt) {
			t.Errorf("%s: %v", format, err)
		}
	}
}

func TestProgressAndInfo(t *testing.T) {
	src := randomBytes(32, 300<<10)
	tgt := variants(src)["longer"]
	for format, enc := range encoders {
		p := enc(src, tgt)
		var last, total int64
		var reports int
		info, err := Apply(context.Background(), bytes.NewReader(p), int64(len(p)), bytes.NewReader(src), int64(len(src)), &memTarget{},
			Options{Progress: func(done, tot int64) {
				if done < last || done > tot {
					t.Errorf("%s: progress %d after %d, total %d", format, done, last, tot)
				}
				last, total = done, tot
				reports++
			}})
		if err != nil {
			t.Fatal(err)
		}
		if info.Verified != (format != IPS) || total != info.Work || last != info.Work {
			t.Errorf("%s: %+v, last %d/%d", format, info, last, total)
		}
		if info.TargetCRC32 != crc32.ChecksumIEEE(tgt) {
			t.Errorf("%s: target CRC32", format)
		}
		if format != IPS && (info.Work != int64(len(p))-4+int64(len(src))+int64(len(tgt)) || reports < 3) {
			t.Errorf("%s: work %d, %d reports", format, info.Work, reports)
		}
		pre, err := Inspect(context.Background(), bytes.NewReader(p), int64(len(p)), int64(len(src)), Options{})
		if err != nil || pre.Work != info.Work || pre.Verified != info.Verified {
			t.Errorf("%s: Inspect %+v %v", format, pre, err)
		}
	}
}

func TestMismatchDetails(t *testing.T) {
	src := randomBytes(33, 2000)
	tgt := variants(src)["changed"]
	other := bytes.Clone(src)
	other[0] ^= 1
	p := encodeBPS(src, tgt)
	_, _, err := apply(t, p, other)
	var m *MismatchError
	if !errors.As(err, &m) || !m.CRCKnown || m.ExpectedCRC32 != crc32.ChecksumIEEE(src) || m.ActualCRC32 != crc32.ChecksumIEEE(other) {
		t.Fatalf("%v %+v", err, m)
	}
	_, _, err = apply(t, p, src[:10])
	if !errors.As(err, &m) || m.CRCKnown || m.ActualSize != 10 || m.ExpectedSize != 2000 {
		t.Fatalf("size: %v %+v", err, m)
	}
}

// cancelOnReread cancels a context when the patch is read past at for
// the second time (the first pass checks its CRC32, the second applies it).
type cancelOnReread struct {
	r      *bytes.Reader
	at     int64
	passes int
	cancel context.CancelFunc
	last   int64
	after  int64 // bytes read once cancelled
}

func (c *cancelOnReread) ReadAt(p []byte, off int64) (int, error) {
	end := off + int64(len(p))
	if c.passes >= 2 {
		c.after += int64(len(p))
	}
	if off <= c.at && end > c.at {
		c.passes++ // a sequential pass over at
		if c.passes >= 2 {
			c.cancel()
		}
	}
	c.last = end
	return c.r.ReadAt(p, off)
}

// Codex review of M6: a long UPS block past the target size reads without
// writing; the cancellation is checked in that loop too.
func TestUPSCancelInALongBlockPastTheTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("24 MiB")
	}
	src := randomBytes(40, 24<<20)
	for i := range src {
		src[i] |= 1 // no zero byte: past the target, one single block
	}
	tgt := bytes.Clone(src[:16])
	p := encodeUPS(src, tgt)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &cancelOnReread{r: bytes.NewReader(p), at: 2 << 20, cancel: cancel}
	_, err := Apply(ctx, r, int64(len(p)), bytes.NewReader(src), int64(len(src)), &memTarget{}, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v, want context.Canceled", err)
	}
	// Stopped soon, not at the end of the 24 MiB block.
	if r.after > 8<<20 {
		t.Fatalf("%d bytes read after the cancellation", r.after)
	}
}
