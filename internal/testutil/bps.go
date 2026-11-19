package testutil

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
)

// EncodeBPS returns a BPS patch turning src into tgt, for the tests of the
// patch job and route (the full encoders of the three formats are in the
// tests of internal/patch). Bytes equal at the same offset become
// SourceRead actions, the others TargetRead actions.
func EncodeBPS(src, tgt []byte) []byte {
	var out bytes.Buffer
	vli := func(v uint64) {
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
	out.WriteString("BPS1")
	vli(uint64(len(src)))
	vli(uint64(len(tgt)))
	vli(0)
	for o := 0; o < len(tgt); {
		n := 0
		for o+n < len(tgt) && o+n < len(src) && src[o+n] == tgt[o+n] {
			n++
		}
		if n > 0 {
			vli(uint64(n-1) << 2) // SourceRead
			o += n
			continue
		}
		for o+n < len(tgt) && (o+n >= len(src) || src[o+n] != tgt[o+n]) {
			n++
		}
		vli(uint64(n-1)<<2 | 1) // TargetRead
		out.Write(tgt[o : o+n])
		o += n
	}
	var b [4]byte
	for _, sum := range []uint32{crc32.ChecksumIEEE(src), crc32.ChecksumIEEE(tgt)} {
		binary.LittleEndian.PutUint32(b[:], sum)
		out.Write(b[:])
	}
	binary.LittleEndian.PutUint32(b[:], crc32.ChecksumIEEE(out.Bytes()))
	out.Write(b[:])
	return out.Bytes()
}
