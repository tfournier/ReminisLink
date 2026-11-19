package patch

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

// Fuzzing of the parsers (docs/specs/08-tests-qualite.md §2): any patch
// bytes must end in a result or an error of the package, never a panic,
// and never write more than the target bound. Short runs:
//
//	go test ./internal/patch -run '^$' -fuzz '^FuzzIPS$' -fuzztime 20s
//
// The seeds are valid patches made by the test encoders and broken ones.

const fuzzMaxTarget = 1 << 20

var fuzzSource = randomBytes(42, 2048)

func fuzzSeeds(f *testing.F, enc encoder, extra ...[]byte) {
	for _, tgt := range variants(fuzzSource) {
		p := enc(fuzzSource, tgt)
		f.Add(p)
		f.Add(p[:len(p)/2])
	}
	for _, p := range extra {
		f.Add(p)
	}
}

func fuzzApply(t *testing.T, p []byte) {
	dst := &memTarget{limit: fuzzMaxTarget}
	opts := Options{MaxTargetSize: fuzzMaxTarget}
	info, err := Apply(context.Background(), bytes.NewReader(p), int64(len(p)), bytes.NewReader(fuzzSource), int64(len(fuzzSource)), dst, opts)
	if err != nil {
		for _, known := range []error{ErrUnsupported, ErrCorrupt, ErrSourceMismatch, ErrAlreadyPatched, ErrTooLarge} {
			if errors.Is(err, known) {
				return
			}
		}
		t.Fatalf("unexpected error: %v", err)
	}
	if int64(len(dst.b)) != info.TargetSize {
		t.Fatalf("wrote %d bytes, info says %d", len(dst.b), info.TargetSize)
	}
	if _, err := Inspect(context.Background(), bytes.NewReader(p), int64(len(p)), int64(len(fuzzSource)), opts); err != nil {
		t.Fatalf("applied but Inspect fails: %v", err)
	}
}

func FuzzIPS(f *testing.F) {
	fuzzSeeds(f, encodeIPS,
		[]byte("PATCHEOF"), []byte("PATCHEOF\x00\x00\x10"), []byte("PATCH\xff\xff\xff\x00\x00\xff\xff\x01EOF"),
		[]byte("PATCH\x45\x4f\x46\x00\x01\x00EOF"))
	f.Fuzz(fuzzApply)
}

func FuzzBPS(f *testing.F) {
	e := newBPS(len(fuzzSource), 8, "")
	e.targetRead([]byte{1})
	e.targetCopy(0, 7)
	fuzzSeeds(f, encodeBPS, e.finish(fuzzSource, []byte{1, 1, 1, 1, 1, 1, 1, 1}), []byte("BPS1\x80\x80\x80"))
	f.Fuzz(fuzzApply)
}

func FuzzUPS(f *testing.F) {
	fuzzSeeds(f, encodeUPS, []byte("UPS1\x80\x80"))
	f.Fuzz(fuzzApply)
}

// FuzzIPSReference compares every fuzzed IPS patch with the reference
// decoder: same refusal, same result.
func FuzzIPSReference(f *testing.F) {
	fuzzSeeds(f, encodeIPS, []byte("PATCHEOF\x00\x10\x00"), []byte("PATCH\x00\x00\x05\x00\x00\x00\x03\x07EOF\x00\x00\x06"))
	f.Fuzz(func(t *testing.T, p []byte) {
		want, ok := refIPS(p, fuzzSource, fuzzMaxTarget)
		sameAsReference(t, p, fuzzSource, want, ok, fuzzMaxTarget)
	})
}

// FuzzBPSNoChecksum reaches the action parser past the CRC checks (which
// reject nearly every mutation): the fuzzed body gets a footer with the
// CRC32 of the source and of the reference result, and the result must
// match the reference decoder.
func FuzzBPSNoChecksum(f *testing.F) {
	for _, tgt := range variants(fuzzSource) {
		p := encodeBPS(fuzzSource, tgt)
		f.Add(p[:len(p)-12])
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		fuzzWithReference(t, body, bpsMagic, refBPS)
	})
}

// FuzzUPSNoChecksum is FuzzBPSNoChecksum for UPS.
func FuzzUPSNoChecksum(f *testing.F) {
	for _, tgt := range variants(fuzzSource) {
		p := encodeUPS(fuzzSource, tgt)
		f.Add(p[:len(p)-12])
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		fuzzWithReference(t, body, upsMagic, refUPS)
	})
}

func fuzzWithReference(t *testing.T, body []byte, magic string, ref func(p, src []byte, maxTarget int) ([]byte, bool)) {
	if len(body) < len(magic) || string(body[:len(magic)]) != magic {
		return // another format: covered by the other fuzzers
	}
	want, ok := ref(withFooter(body, nil, nil), fuzzSource, fuzzMaxTarget)
	if !ok {
		// Refused by the reference: refused here too, whatever the footer.
		sameAsReference(t, withFooter(body, fuzzSource, nil), fuzzSource, nil, false, fuzzMaxTarget)
		return
	}
	sameAsReference(t, withFooter(body, fuzzSource, want), fuzzSource, want, true, fuzzMaxTarget)
}
