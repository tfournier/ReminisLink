// Package patch applies IPS, BPS and UPS patches to a ROM
// (docs/specs/05-console-pak.md §5.5), in pure Go.
//
// Patches come from the network or the SD card: every parser treats its
// input as hostile. Declared sizes, variable-length numbers and offsets are
// bounded before use, nothing is allocated from a size read in a patch,
// and a malformed patch returns ErrCorrupt rather than panicking.
//
// Memory: the patched file is written in order through a small buffer;
// IPS and UPS stream the source, BPS reads the source and the already
// written target back with random access (pread), so memory stays a few
// hundred KiB whatever the size of the ROM.
//
// The format is detected from the header (PATCH, BPS1, UPS1), never from
// the file name.
package patch

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
)

// Format is a patch format.
type Format string

// Supported formats.
const (
	IPS Format = "ips"
	BPS Format = "bps"
	UPS Format = "ups"
)

// Errors of the package. The functions wrap them with a reason
// (errors.Is works).
var (
	// ErrUnsupported: the file is not an IPS, BPS or UPS patch.
	ErrUnsupported = errors.New("unsupported patch format")
	// ErrCorrupt: the patch is truncated, inconsistent or fails its own
	// checksum.
	ErrCorrupt = errors.New("corrupt patch")
	// ErrSourceMismatch: the ROM is not the one the patch was made for
	// (size or CRC32 of the source).
	ErrSourceMismatch = errors.New("patch does not match the ROM")
	// ErrAlreadyPatched: the ROM is the target of the patch (BPS, UPS):
	// it is refused rather than patched backwards.
	ErrAlreadyPatched = errors.New("ROM is already patched")
	// ErrTooLarge: the patched file would exceed Options.MaxTargetSize.
	ErrTooLarge = errors.New("patched file too large")
)

// MaxTargetSize is the default bound of a patched file (4 GiB, the bound
// of the downloads).
const MaxTargetSize int64 = 4 << 30

// Header magics.
const (
	ipsMagic = "PATCH"
	bpsMagic = "BPS1"
	upsMagic = "UPS1"
)

// footerSize is the CRC32 footer of BPS and UPS: source, target, patch.
const footerSize = 12

// Sizes of the buffers.
const (
	writeBuffer = 256 << 10
	readBuffer  = 64 << 10
	// checkEvery is how many loop turns run between two checks of the
	// context, when a turn writes nothing.
	checkEvery = 4096
)

// Info describes a patch.
type Info struct {
	Format Format
	// SourceSize is the size of the ROM the patch expects, -1 when the
	// format does not say (IPS).
	SourceSize int64
	// TargetSize is the size of the patched file.
	TargetSize int64
	// Verified: the format checks the ROM and the result (BPS, UPS: CRC32
	// of the source and the target). IPS has no checksum.
	Verified bool
	// Work is the total of the progress reports of Apply: the bytes read
	// for the checksums before writing (BPS, UPS) plus the target.
	Work int64
	// TargetCRC32 is the CRC32 of the patched file, set by Apply.
	TargetCRC32 uint32
}

// MismatchError is an ErrSourceMismatch with what was expected and found.
type MismatchError struct {
	ExpectedSize, ActualSize   int64
	ExpectedCRC32, ActualCRC32 uint32
	// CRCKnown: the CRC32 of the ROM was computed (its size matched).
	CRCKnown bool
}

func (e *MismatchError) Error() string {
	if !e.CRCKnown {
		return fmt.Sprintf("%v: size %d, the patch expects %d", ErrSourceMismatch, e.ActualSize, e.ExpectedSize)
	}
	return fmt.Sprintf("%v: CRC32 %08x, the patch expects %08x", ErrSourceMismatch, e.ActualCRC32, e.ExpectedCRC32)
}

// Is makes errors.Is(err, ErrSourceMismatch) true.
func (e *MismatchError) Is(target error) bool { return target == ErrSourceMismatch }

// meter turns the progress of each phase into a progress of the whole
// work.
type meter struct {
	fn          func(done, total int64)
	total, base int64
}

func (m *meter) report(done int64) {
	if m.fn != nil {
		m.fn(min(m.base+done, m.total), m.total)
	}
}

// next moves the base past a finished phase of n bytes.
func (m *meter) next(n int64) { m.base += n }

// Target is where the patched file is written: written in order, read back
// (BPS TargetCopy), truncated to its final size. *os.File implements it.
type Target interface {
	io.WriterAt
	io.ReaderAt
	Truncate(size int64) error
}

// Options tune Apply.
type Options struct {
	// MaxTargetSize bounds the patched file (0: MaxTargetSize).
	MaxTargetSize int64
	// Progress, if set, is called as the patched file is written, with
	// the bytes done and the total.
	Progress func(done, total int64)
}

func (o Options) maxTarget() int64 {
	if o.MaxTargetSize > 0 {
		return o.MaxTargetSize
	}
	return MaxTargetSize
}

// Detect returns the format of a patch from its first bytes.
func Detect(header []byte) (Format, error) {
	switch {
	case len(header) >= len(ipsMagic) && string(header[:len(ipsMagic)]) == ipsMagic:
		return IPS, nil
	case len(header) >= len(bpsMagic) && string(header[:len(bpsMagic)]) == bpsMagic:
		return BPS, nil
	case len(header) >= len(upsMagic) && string(header[:len(upsMagic)]) == upsMagic:
		return UPS, nil
	}
	return "", ErrUnsupported
}

func detectAt(p io.ReaderAt, size int64) (Format, error) {
	var hdr [5]byte
	n, err := p.ReadAt(hdr[:min(int64(len(hdr)), max(size, 0))], 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return Detect(hdr[:n])
}

// Inspect reads the header of a patch (and, for IPS, its records) and
// returns what it expects and produces. srcSize is the size of the ROM
// (needed for the size of an IPS result). It does not check the
// checksums: Apply does. ctx stops the scan of a large IPS patch.
func Inspect(ctx context.Context, p io.ReaderAt, pSize, srcSize int64, opts Options) (Info, error) {
	f, err := detectAt(p, pSize)
	if err != nil {
		return Info{}, err
	}
	var info Info
	switch f {
	case IPS:
		var h ipsHeader
		h, err = scanIPS(ctx, p, pSize, srcSize, opts)
		info = h.info
	case BPS:
		var h bpsHeader
		h, err = readBPSHeader(p, pSize, opts)
		info = h.info
	default:
		var h upsHeader
		h, err = readUPSHeader(p, pSize, opts)
		info = h.info
	}
	if err != nil {
		return info, err
	}
	return withWork(info, pSize, srcSize), nil
}

// withWork fills Verified and Work.
func withWork(info Info, pSize, srcSize int64) Info {
	info.Verified = info.Format != IPS
	info.Work = info.TargetSize
	if info.Verified {
		info.Work += pSize - 4 + srcSize
	}
	return info
}

// Apply applies the patch p (pSize bytes) to the ROM src (srcSize bytes)
// and writes the patched file to dst, truncated to its size. The checksums
// of BPS and UPS (patch, source, target) are all checked: the patch and the
// source before anything is written, the target at the end. ctx stops the
// work (the error is then ctx.Err()); dst is left partial, the caller
// deletes it.
func Apply(ctx context.Context, p io.ReaderAt, pSize int64, src io.ReaderAt, srcSize int64, dst Target, opts Options) (Info, error) {
	if pSize < 0 || srcSize < 0 {
		return Info{}, fmt.Errorf("%w: negative size", ErrCorrupt)
	}
	f, err := detectAt(p, pSize)
	if err != nil {
		return Info{}, err
	}
	switch f {
	case IPS:
		return applyIPS(ctx, p, pSize, src, srcSize, dst, opts)
	case BPS:
		return applyBPS(ctx, p, pSize, src, srcSize, dst, opts)
	default:
		return applyUPS(ctx, p, pSize, src, srcSize, dst, opts)
	}
}

// --- helpers shared by the formats ------------------------------------------

func corrupt(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrCorrupt, fmt.Sprintf(format, args...))
}

// patchReader reads a patch in order, counting the bytes read; reading past
// its end (the footer for BPS and UPS) is ErrCorrupt.
type patchReader struct {
	r   *bufio.Reader
	pos int64 // absolute offset of the next byte
	end int64
}

func newPatchReader(p io.ReaderAt, start, end int64) *patchReader {
	return &patchReader{r: bufio.NewReaderSize(io.NewSectionReader(p, start, max(end-start, 0)), readBuffer), pos: start, end: end}
}

func (r *patchReader) ReadByte() (byte, error) {
	b, err := r.r.ReadByte()
	if err != nil {
		return 0, readErr(err)
	}
	r.pos++
	return b, nil
}

func (r *patchReader) full(b []byte) error {
	n, err := io.ReadFull(r.r, b)
	r.pos += int64(n)
	if err != nil {
		return readErr(err)
	}
	return nil
}

func (r *patchReader) discard(n int64) error {
	for n > 0 {
		step := int(min(n, 1<<30))
		d, err := r.r.Discard(step)
		r.pos += int64(d)
		n -= int64(d)
		if err != nil {
			return readErr(err)
		}
	}
	return nil
}

func (r *patchReader) remaining() int64 { return r.end - r.pos }

// readErr maps an early end of the patch to ErrCorrupt and keeps real
// I/O errors.
func readErr(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return corrupt("truncated")
	}
	return err
}

// maxVLIBytes bounds a variable-length number: 8 bytes encode at most
// about 2^57, more than any size accepted here, and cannot overflow.
const maxVLIBytes = 8

// readVLI decodes the variable-length number of BPS and UPS (byuu's
// encoding: 7 bits per byte, low bits first, the last byte has bit 7 set,
// each continuation adds the next power so every number has one encoding).
func readVLI(r io.ByteReader) (int64, error) {
	var data, shift uint64 = 0, 1
	for i := 0; ; i++ {
		if i >= maxVLIBytes {
			return 0, corrupt("number too long")
		}
		x, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		data += uint64(x&0x7f) * shift
		if x&0x80 != 0 {
			break
		}
		shift <<= 7
		data += shift
	}
	return int64(data), nil // < 2^58: fits
}

// crcOf computes the CRC32 (IEEE) of r[0:n], checking ctx and reporting
// the progress (m may be nil) between blocks.
func crcOf(ctx context.Context, r io.ReaderAt, n int64, m *meter) (uint32, error) {
	h := crc32.NewIEEE()
	buf := make([]byte, readBuffer)
	for off := int64(0); off < n; {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		k := int(min(int64(len(buf)), n-off))
		got, err := r.ReadAt(buf[:k], off)
		h.Write(buf[:got])
		off += int64(got)
		if got < k {
			if err == nil || errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return 0, err
		}
		if m != nil {
			m.report(off)
		}
	}
	if m != nil {
		m.next(n)
	}
	return h.Sum32(), nil
}

// readFooter reads the 12-byte CRC32 footer of BPS and UPS (little endian).
// A reader may return io.EOF with the last bytes: 12 bytes read is enough.
func readFooter(p io.ReaderAt, pSize int64) (src, tgt, self uint32, err error) {
	var b [footerSize]byte
	if n, err := p.ReadAt(b[:], pSize-footerSize); n < footerSize {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return 0, 0, 0, readErr(err)
	}
	le := func(b []byte) uint32 { return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24 }
	return le(b[0:4]), le(b[4:8]), le(b[8:12]), nil
}

// checkSelf verifies the CRC32 of the patch (all bytes but the last 4).
func checkSelf(ctx context.Context, p io.ReaderAt, pSize int64, want uint32, m *meter) error {
	got, err := crcOf(ctx, p, pSize-4, m)
	if err != nil {
		return readErr(err)
	}
	if got != want {
		return corrupt("patch checksum mismatch")
	}
	return nil
}

// checkSource compares the ROM with the source (and target) the patch
// declares: nil when it is the source, ErrAlreadyPatched when it is the
// target, a *MismatchError otherwise.
func checkSource(ctx context.Context, src io.ReaderAt, srcSize int64, info Info, srcCRC, tgtCRC uint32, m *meter) error {
	mismatch := &MismatchError{ExpectedSize: info.SourceSize, ActualSize: srcSize, ExpectedCRC32: srcCRC}
	if srcSize != info.SourceSize && srcSize != info.TargetSize {
		return mismatch
	}
	got, err := crcOf(ctx, src, srcSize, m)
	if err != nil {
		return err
	}
	if srcSize == info.SourceSize && got == srcCRC {
		return nil
	}
	if srcSize == info.TargetSize && got == tgtCRC {
		return ErrAlreadyPatched
	}
	mismatch.ActualCRC32, mismatch.CRCKnown = got, true
	return mismatch
}

// sourceReader reads the ROM with random access through a one-block cache
// (BPS and UPS read it nearly in order, often a few bytes at a time).
// Bytes past its end read as zero (UPS).
type sourceReader struct {
	r     io.ReaderAt
	size  int64
	buf   []byte
	start int64 // offset of buf[0]
	n     int   // valid bytes in buf
}

func newSourceReader(r io.ReaderAt, size int64) *sourceReader {
	return &sourceReader{r: r, size: size, buf: make([]byte, readBuffer)}
}

// readAt fills p from off; past the end of the ROM it writes zeros.
func (s *sourceReader) readAt(p []byte, off int64) error {
	for len(p) > 0 {
		if off >= s.size {
			clear(p)
			return nil
		}
		if off < s.start || off >= s.start+int64(s.n) {
			k := int(min(int64(len(s.buf)), s.size-off))
			got, err := s.r.ReadAt(s.buf[:k], off)
			if got < k {
				if err == nil || errors.Is(err, io.EOF) {
					err = io.ErrUnexpectedEOF
				}
				return fmt.Errorf("read ROM: %w", err)
			}
			s.start, s.n = off, k
		}
		c := copy(p, s.buf[off-s.start:s.n])
		p, off = p[c:], off+int64(c)
	}
	return nil
}

// targetWriter writes the patched file in order through a buffer, keeping
// the CRC32 of what was written, and reads back the bytes already written
// (from the buffer or the file).
type targetWriter struct {
	ctx      context.Context
	dst      Target
	buf      []byte
	flushed  int64 // bytes already in dst
	crc      hash.Hash32
	progress *meter
}

func newTargetWriter(ctx context.Context, dst Target, progress *meter) *targetWriter {
	return &targetWriter{ctx: ctx, dst: dst, buf: make([]byte, 0, writeBuffer), crc: crc32.NewIEEE(), progress: progress}
}

// off is the number of bytes written so far.
func (w *targetWriter) off() int64 { return w.flushed + int64(len(w.buf)) }

func (w *targetWriter) write(p []byte) error {
	for len(p) > 0 {
		if len(w.buf) == cap(w.buf) {
			if err := w.flush(); err != nil {
				return err
			}
		}
		n := copy(w.buf[len(w.buf):cap(w.buf)], p)
		w.buf = w.buf[:len(w.buf)+n]
		p = p[n:]
	}
	return nil
}

func (w *targetWriter) writeByte(b byte) error {
	if len(w.buf) == cap(w.buf) {
		if err := w.flush(); err != nil {
			return err
		}
	}
	w.buf = append(w.buf, b)
	return nil
}

// flush writes the buffer to dst, checks ctx and reports the progress.
func (w *targetWriter) flush() error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if len(w.buf) > 0 {
		if _, err := w.dst.WriteAt(w.buf, w.flushed); err != nil {
			return fmt.Errorf("write patched file: %w", err)
		}
		w.crc.Write(w.buf)
		w.flushed += int64(len(w.buf))
		w.buf = w.buf[:0]
	}
	w.progress.report(w.flushed)
	return nil
}

// readAt reads back written bytes: off+len(p) must be <= w.off().
func (w *targetWriter) readAt(p []byte, off int64) error {
	if off < w.flushed {
		k := int(min(int64(len(p)), w.flushed-off))
		got, err := w.dst.ReadAt(p[:k], off)
		if got < k {
			if err == nil || errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return fmt.Errorf("read patched file: %w", err)
		}
		p, off = p[k:], off+int64(k)
	}
	if len(p) > 0 {
		copy(p, w.buf[off-w.flushed:])
	}
	return nil
}

// finish flushes and truncates dst to the bytes written.
func (w *targetWriter) finish() error {
	if err := w.flush(); err != nil {
		return err
	}
	if err := w.dst.Truncate(w.flushed); err != nil {
		return fmt.Errorf("truncate patched file: %w", err)
	}
	return nil
}

// copySource writes n bytes of the ROM from off.
func copySource(w *targetWriter, s *sourceReader, off, n int64, scratch []byte) error {
	for n > 0 {
		k := int(min(int64(len(scratch)), n))
		if err := s.readAt(scratch[:k], off); err != nil {
			return err
		}
		if err := w.write(scratch[:k]); err != nil {
			return err
		}
		off += int64(k)
		n -= int64(k)
	}
	return nil
}
