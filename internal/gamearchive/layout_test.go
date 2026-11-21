package gamearchive

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"strings"
	"testing"
	"time"
)

// le32 / le64 patch little-endian fields of an archive.
func put32(b []byte, at int, v uint32) { binary.LittleEndian.PutUint32(b[at:], v) }

// centralAt returns the offset of the i-th central directory header.
func centralAt(t *testing.T, data []byte, i int) int {
	t.Helper()
	p := bytes.LastIndex(data, []byte{0x50, 0x4b, 0x05, 0x06})
	off := int(binary.LittleEndian.Uint32(data[p+16:]))
	for ; i > 0; i-- {
		n := int(binary.LittleEndian.Uint16(data[off+28:])) + int(binary.LittleEndian.Uint16(data[off+30:])) + int(binary.LittleEndian.Uint16(data[off+32:]))
		off += centralLen + n
	}
	return off
}

func mustRefuse(t *testing.T, name string, data []byte) {
	t.Helper()
	if _, err := open(t, data); err == nil {
		t.Errorf("%s: accepted", name)
	} else if _, ok := IsFormatError(err); !ok {
		t.Errorf("%s: not a FormatError: %v", name, err)
	}
}

// storedArchive is a valid archive whose entries are all stored (no data
// descriptor): offsets and sizes are easy to patch.
func storedArchive(t *testing.T) []byte {
	t.Helper()
	a := []byte("aaaa-save")
	b := []byte("bbbb-state")
	m := &Manifest{Format: FormatName, Version: 1, CreatedAt: time.Now(), Source: source(), Files: []File{
		{Path: "save/a", Role: RoleSave, SizeBytes: int64(len(a)), SHA256: sum(a)},
		{Path: "states/0/b", Role: RoleState, Slot: intp(0), SizeBytes: int64(len(b)), SHA256: sum(b)},
	}}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range []struct {
		name string
		data []byte
	}{{"save/a", a}, {"states/0/b", b}} {
		w, err := zw.CreateRaw(&zip.FileHeader{Name: e.name, Method: zip.Store,
			CompressedSize64: uint64(len(e.data)), UncompressedSize64: uint64(len(e.data)), CRC32: crc(e.data)})
		if err != nil {
			t.Fatal(err)
		}
		w.Write(e.data)
	}
	md, _ := json.Marshal(m)
	w, _ := zw.CreateRaw(&zip.FileHeader{Name: ManifestName, Method: zip.Store,
		CompressedSize64: uint64(len(md)), UncompressedSize64: uint64(len(md)), CRC32: crc(md)})
	w.Write(md)
	zw.Close()
	data := buf.Bytes()
	if _, err := open(t, data); err != nil {
		t.Fatalf("base archive refused: %v", err)
	}
	return data
}

// TestLayoutChecks (Codex review of M19): the physical layout of the
// entries is checked before any content is read.
func TestLayoutChecks(t *testing.T) {
	base := storedArchive(t)
	clone := func() []byte { return append([]byte(nil), base...) }

	// Two entries pointing at the same local data.
	d := clone()
	first := binary.LittleEndian.Uint32(d[centralAt(t, d, 0)+42:])
	put32(d, centralAt(t, d, 1)+42, first)
	mustRefuse(t, "shared local data", d)

	// A local header offset beyond the central directory.
	d = clone()
	put32(d, centralAt(t, d, 1)+42, uint32(len(d)))
	mustRefuse(t, "offset outside", d)

	// A compressed size reaching into the central directory (central and
	// local both patched, so only the range is wrong).
	d = clone()
	put32(d, centralAt(t, d, 0)+20, 5000)
	put32(d, 18, 5000)
	mustRefuse(t, "range into the directory", d)

	// Local and central sizes that disagree.
	d = clone()
	put32(d, 22, 3) // local uncompressed size of the first entry
	mustRefuse(t, "local size differs", d)

	// Local and central names that disagree.
	d = clone()
	d[localLen] = 'X'
	mustRefuse(t, "local name differs", d)

	// Data prepended before the first entry (offsets shifted accordingly
	// by archive/zip's base offset rule).
	mustRefuse(t, "prepended data", append([]byte("garbage!"), base...))

	// Bytes hidden between the last entry and the directory.
	d = clone()
	dir := centralAt(t, d, 0)
	withGap := append(append(append([]byte(nil), d[:dir]...), []byte("hidden")...), d[dir:]...)
	end := bytes.LastIndex(withGap, []byte{0x50, 0x4b, 0x05, 0x06})
	put32(withGap, end+16, uint32(dir+6))
	mustRefuse(t, "hidden bytes", withGap)

	// The longest comment is fine; anything after the end record is not.
	d = clone()
	end = bytes.LastIndex(d, []byte{0x50, 0x4b, 0x05, 0x06})
	comment := bytes.Repeat([]byte("c"), maxComment)
	binary.LittleEndian.PutUint16(d[end+20:], maxComment)
	d = append(d, comment...)
	if _, err := open(t, d); err != nil {
		t.Errorf("max comment refused: %v", err)
	}
	mustRefuse(t, "trailing bytes", append(clone(), 0))

	// Truncated archives never panic.
	for n := 0; n < len(base); n += 7 {
		if _, err := open(t, base[:n]); err == nil {
			t.Fatalf("truncated at %d accepted", n)
		}
	}
}

// TestEntryLimitIsCheckedFirst: the 513th entry is refused from the end
// records, before archive/zip reads the directory.
func TestEntryLimitIsCheckedFirst(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := 0; i <= MaxEntries; i++ {
		zw.Create(fmt.Sprintf("d%d/", i))
	}
	zw.Close()
	_, err := open(t, buf.Bytes())
	if fe, ok := IsFormatError(err); !ok || !strings.Contains(fe.Reason, "entries") {
		t.Errorf("513 entries: %v", err)
	}
}

// TestZip64Archive: a zip64 archive (forced by archive/zip for a large
// declared size) is read with its zip64 records.
func TestZip64Archive(t *testing.T) {
	data := []byte("state")
	m := &Manifest{Format: FormatName, Version: 1, CreatedAt: time.Now(), Source: source(), Files: []File{
		{Path: "states/0/s", Role: RoleState, Slot: intp(0), SizeBytes: int64(len(data)), SHA256: sum(data)},
	}}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// A data descriptor with zip64 sizes is used when the size is unknown
	// in advance and the header asks for zip64.
	h := &zip.FileHeader{Name: "states/0/s", Method: zip.Deflate}
	h.UncompressedSize64 = 1 << 33 // marks the header zip64; the real size is written after
	w, _ := zw.CreateHeader(h)
	w.Write(data)
	md, _ := json.Marshal(m)
	w, _ = zw.Create(ManifestName)
	w.Write(md)
	zw.Close()
	if _, err := open(t, buf.Bytes()); err != nil {
		t.Errorf("zip64 archive refused: %v", err)
	}
}

// TestManifestRequiredFields: a missing required field is refused, not
// read as its zero value.
func TestManifestRequiredFields(t *testing.T) {
	data := []byte("save")
	file := map[string]any{"path": "save/a", "role": "save", "sizeBytes": len(data), "sha256": sum(data)}
	src := map[string]any{"os": "nextui", "systemTag": "GBA", "systemLabel": "GBA", "gameName": "Sky", "fileName": "Sky.gba", "romName": "Sky.gba", "kind": "file"}
	full := func() map[string]any {
		f := map[string]any{}
		for k, v := range file {
			f[k] = v
		}
		s := map[string]any{}
		for k, v := range src {
			s[k] = v
		}
		return map[string]any{"format": FormatName, "version": 1, "createdAt": "2026-11-21T10:00:00Z", "source": s, "files": []any{f}}
	}
	make := func(m map[string]any) []byte {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create("save/a")
		w.Write(data)
		md, _ := json.Marshal(m)
		w, _ = zw.Create(ManifestName)
		w.Write(md)
		zw.Close()
		return buf.Bytes()
	}
	if _, err := open(t, make(full())); err != nil {
		t.Fatalf("full manifest refused: %v", err)
	}
	for _, k := range []string{"createdAt", "source", "format"} {
		m := full()
		delete(m, k)
		mustRefuse(t, "no "+k, make(m))
	}
	m := full()
	m["source"].(map[string]any)["romName"] = nil
	mustRefuse(t, "null romName", make(m))
	m = full()
	delete(m["files"].([]any)[0].(map[string]any), "sizeBytes")
	mustRefuse(t, "no sizeBytes", make(m))
}

// TestExportOfHighlyCompressibleFiles: a big state full of zeros is
// stored rather than deflated, so the archive passes the ratio check of
// the import (Codex review).
func TestExportOfHighlyCompressibleFiles(t *testing.T) {
	state := make([]byte, 4<<20)
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if _, err := w.AddFile(RoleState, intp(1), "Sky.gba.st1", time.Now(), bytes.NewReader(state), int64(len(state))); err != nil {
		t.Fatal(err)
	}
	text := bytes.Repeat([]byte("a state that compresses well but not absurdly 0123456789"), 40_000)
	if _, err := w.AddFile(RoleSave, nil, "Sky.gba.sav", time.Now(), bytes.NewReader(text), int64(len(text))); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(source(), time.Now()); err != nil {
		t.Fatal(err)
	}
	a, err := open(t, buf.Bytes())
	if err != nil {
		t.Fatalf("own export refused: %v", err)
	}
	if e := a.Find(RoleState, intp(1)); e == nil || e.zf.Method != zip.Store {
		t.Errorf("zero state not stored: %+v", e)
	}
}

func crc(b []byte) uint32 { return crc32.ChecksumIEEE(b) }
