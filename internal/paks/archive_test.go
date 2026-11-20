package paks

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tfournier/ReminisLink/internal/store"
)

// zipEntry is an entry of a test archive.
type zipEntry struct {
	name string
	data string
	mode fs.FileMode // 0: regular file (or folder when name ends with /)
}

func makeZip(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(e.mode)
		} else if !strings.HasSuffix(e.name, "/") {
			h.SetMode(0o644)
		}
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(e.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func extract(t *testing.T, data []byte) (Staged, string, error) {
	t.Helper()
	dest := t.TempDir()
	s, err := ExtractZip(context.Background(), bytes.NewReader(data), int64(len(data)), dest, ExtractOptions{Platform: "tg5040"})
	return s, dest, err
}

// wantRefused checks a FormatError whose reason contains want, and that
// nothing was written to the staging folder.
func wantRefused(t *testing.T, data []byte, want string) {
	t.Helper()
	_, dest, err := extract(t, data)
	fe, ok := IsFormatError(err)
	if !ok {
		t.Fatalf("got %v, want a refusal (%s)", err, want)
	}
	if !strings.Contains(fe.Reason, want) {
		t.Fatalf("reason %q, want %q", fe.Reason, want)
	}
	des, _ := os.ReadDir(dest)
	if len(des) != 0 && want != "larger than announced" && want != "corrupt" {
		t.Fatalf("something was written: %v", des)
	}
}

var launch = zipEntry{name: "Clock.pak/launch.sh", data: "#!/bin/sh\necho clock\n"}

func TestExtractSimplePak(t *testing.T) {
	data := makeZip(t,
		zipEntry{name: "Clock.pak/"},
		launch,
		zipEntry{name: "Clock.pak/res/font.ttf", data: "font"},
		zipEntry{name: "Clock.pak/pak.json", data: `{"name":"Clock","version":"1.2","type":"TOOL"}`},
		zipEntry{name: "README.md", data: "ignored"},
		zipEntry{name: "__MACOSX/Clock.pak/._launch.sh", data: "fork"},
	)
	s, dest, err := extract(t, data)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "Clock.pak" || s.Kind != "" || s.Dir != filepath.Join(dest, "Clock.pak") {
		t.Fatalf("staged %+v", s)
	}
	for rel, want := range map[string]string{"launch.sh": launch.data, "res/font.ttf": "font"} {
		got, err := os.ReadFile(filepath.Join(s.Dir, filepath.FromSlash(rel)))
		if err != nil || string(got) != want {
			t.Fatalf("%s: %q %v", rel, got, err)
		}
	}
	for _, rel := range []string{"README.md", "__MACOSX"} {
		if _, err := os.Lstat(filepath.Join(dest, rel)); err == nil {
			t.Fatalf("%s extracted", rel)
		}
	}
	v, typ := ReadMetadata(s.Dir)
	if v == nil || *v != "1.2" || KindFromMetadata(typ) != KindTool {
		t.Fatalf("metadata %v %q", v, typ)
	}
}

func TestExtractKindFromLayout(t *testing.T) {
	for _, tc := range []struct{ prefix, kind string }{{"Tools/tg5040/", KindTool}, {"Emus/TG5040/", KindEmu}, {"tools/tg5040/", KindTool}} {
		data := makeZip(t,
			zipEntry{name: strings.SplitN(tc.prefix, "/", 2)[0] + "/"},
			zipEntry{name: tc.prefix},
			zipEntry{name: tc.prefix + "GBA.pak/launch.sh", data: "x"},
			// Another platform: ignored.
			zipEntry{name: "Emus/tg5050/GBA.pak/launch.sh", data: "y"},
		)
		s, _, err := extract(t, data)
		if err != nil {
			t.Fatalf("%s: %v", tc.prefix, err)
		}
		if s.Kind != tc.kind || s.Name != "GBA.pak" {
			t.Fatalf("%s: %+v", tc.prefix, s)
		}
	}
}

func TestExtractRefusesBadPaths(t *testing.T) {
	long := strings.Repeat("a", 256)
	for name, entry := range map[string]zipEntry{
		"dot path component": {name: "Clock.pak/../evil.sh", data: "x"},
		"parent at the top":  {name: "../Clock.pak/evil.sh", data: "x"},
		"absolute":           {name: "/etc/Clock.pak/x", data: "x"},
		"drive":              {name: "C:/Clock.pak/x", data: "x"},
		"backslash":          {name: "Clock.pak\\..\\evil.sh", data: "x"},
		"NUL":                {name: "Clock.pak/evil\x00.sh", data: "x"},
		"control":            {name: "Clock.pak/evil\n.sh", data: "x"},
		"empty component":    {name: "Clock.pak//evil.sh", data: "x"},
		"dot component":      {name: "Clock.pak/./evil.sh", data: "x"},
		"long component":     {name: "Clock.pak/" + long, data: "x"},
		"long path":          {name: "Clock.pak/" + strings.Repeat("abcdefgh/", 120) + "x", data: "x"},
		"exFAT character":    {name: "Clock.pak/what?.sh", data: "x"},
		"invalid UTF-8":      {name: "Clock.pak/\xff.sh", data: "x"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := extract(t, makeZip(t, launch, entry))
			if _, ok := IsFormatError(err); !ok {
				t.Fatalf("got %v, want a refusal", err)
			}
		})
	}
}

func TestExtractRefusesLinksAndSpecialFiles(t *testing.T) {
	wantRefused(t, makeZip(t, launch, zipEntry{name: "Clock.pak/link", data: "/etc/passwd", mode: fs.ModeSymlink | 0o777}), "symbolic link")
	wantRefused(t, makeZip(t, launch, zipEntry{name: "Clock.pak/dev", mode: fs.ModeDevice | 0o644}), "neither a file nor a folder")
	wantRefused(t, makeZip(t, launch, zipEntry{name: "Clock.pak/fifo", mode: fs.ModeNamedPipe | 0o644}), "neither a file nor a folder")
}

func TestExtractRefusesBadLayouts(t *testing.T) {
	wantRefused(t, makeZip(t, zipEntry{name: "README.md", data: "x"}), "no .pak folder")
	wantRefused(t, makeZip(t, zipEntry{name: "launch.sh", data: "x"}), "cannot name the pak")
	wantRefused(t, makeZip(t, launch, zipEntry{name: "Clock.pak/res/"}, zipEntry{name: "Clock.pak/RES/"}), "same name")
	wantRefused(t, makeZip(t, zipEntry{name: "Clock.pak/"}, zipEntry{name: "clock.PAK/"}, launch), "same name")
	wantRefused(t, makeZip(t, zipEntry{name: "Clock.pak/pak.json", data: "{}"}), "no launch.sh")
	wantRefused(t, makeZip(t, zipEntry{name: "Clock.pak/bin/launch.sh", data: "x"}), "no launch.sh")
	wantRefused(t, makeZip(t, zipEntry{name: "Clock.pak/launch.sh/", data: ""}), "no launch.sh")
	wantRefused(t, makeZip(t, launch, zipEntry{name: "Other.pak/launch.sh", data: "x"}), "more than one pak")
	wantRefused(t, makeZip(t, launch, zipEntry{name: "Tools/tg5040/Other.pak/launch.sh", data: "x"}), "more than one pak")
	wantRefused(t, makeZip(t, launch, zipEntry{name: "lib/libx.so", data: "x"}), "outside the pak folder")
	wantRefused(t, makeZip(t, launch, zipEntry{name: "Tools/tg5040/notes.txt", data: "x"}), "outside the pak folder")
	wantRefused(t, makeZip(t, zipEntry{name: "Clock.pak", data: "x"}), "is a file")
	wantRefused(t, makeZip(t, zipEntry{name: ".Hidden.pak/launch.sh", data: "x"}), "invalid pak folder name")
	wantRefused(t, makeZip(t, launch, zipEntry{name: "Clock.pak/LAUNCH.SH", data: "x"}), "same name")
	wantRefused(t, makeZip(t, launch, zipEntry{name: "Clock.pak/a", data: "x"}, zipEntry{name: "Clock.pak/A/b", data: "x"}), "same name")
	// Two spellings of the pak folder are one folder on exFAT.
	if _, _, err := extract(t, makeZip(t, launch, zipEntry{name: "CLOCK.PAK/res/x", data: "x"})); err != nil {
		t.Fatalf("case of the pak folder: %v", err)
	}
	if _, _, err := extract(t, []byte("not a zip at all")); err == nil {
		t.Fatal("not a zip accepted")
	} else if fe, ok := IsFormatError(err); !ok || fe.Reason != "not a zip archive" {
		t.Fatalf("not a zip: %v", err)
	}
}

func TestExtractRefusesTooManyEntries(t *testing.T) {
	entries := []zipEntry{launch}
	for i := 0; i < MaxEntries; i++ {
		entries = append(entries, zipEntry{name: "Clock.pak/d/" + strconv.Itoa(i)})
	}
	wantRefused(t, makeZip(t, entries...), "more than 10000 entries")
}

// rawZip writes entries with forged headers (sizes, method) and raw data.
func rawZip(t *testing.T, entries ...struct {
	h    zip.FileHeader
	data []byte
}) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		h := e.h
		f, err := w.CreateRaw(&h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func deflate(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	fw, _ := flate.NewWriter(&buf, flate.BestCompression)
	fw.Write(data)
	fw.Close()
	return buf.Bytes()
}

type rawEntry = struct {
	h    zip.FileHeader
	data []byte
}

func TestExtractRefusesZipBombs(t *testing.T) {
	// Ratio: 4 MiB of zeros compress to a few KiB.
	wantRefused(t, makeZip(t, launch, zipEntry{name: "Clock.pak/zeros", data: string(make([]byte, 4<<20))}), "compressed more than")

	// Announced size beyond the limit (ratio within bounds).
	small := deflate(t, []byte("x"))
	big := rawEntry{h: zip.FileHeader{Name: "Clock.pak/big", Method: zip.Deflate, CompressedSize64: 600 << 20 / 100, UncompressedSize64: 600 << 20}, data: small}
	big2 := big
	big2.h.Name = "Clock.pak/big2"
	l := rawEntry{h: zip.FileHeader{Name: "Clock.pak/launch.sh", Method: zip.Store, CompressedSize64: 1, UncompressedSize64: 1, CRC32: crc32.ChecksumIEEE([]byte("x"))}, data: []byte("x")}
	wantRefused(t, rawZip(t, l, big, big2), "bytes uncompressed")

	// Real size beyond the announced one: refused while extracting.
	payload := bytes.Repeat([]byte("abcdefghij"), 1000)
	lying := rawEntry{h: zip.FileHeader{Name: "Clock.pak/lying", Method: zip.Deflate, UncompressedSize64: 100}, data: deflate(t, payload)}
	lying.h.CompressedSize64 = uint64(len(lying.data))
	_, _, err := extract(t, rawZip(t, l, lying))
	if _, ok := IsFormatError(err); !ok {
		t.Fatalf("lying size: %v", err)
	}

	// Unknown compression method.
	odd := rawEntry{h: zip.FileHeader{Name: "Clock.pak/odd", Method: 99, CompressedSize64: 1, UncompressedSize64: 1}, data: []byte("x")}
	wantRefused(t, rawZip(t, l, odd), "compression method")
}

func TestExtractChecksSpaceFirst(t *testing.T) {
	data := makeZip(t, launch, zipEntry{name: "Clock.pak/data", data: "0123456789"})
	dest := t.TempDir()
	full := errors.New("card full")
	var asked int64
	_, err := ExtractZip(context.Background(), bytes.NewReader(data), int64(len(data)), dest, ExtractOptions{Platform: "tg5040",
		Space: func(need int64) error { asked = need; return full }})
	if !errors.Is(err, full) || asked != int64(len(launch.data))+10 {
		t.Fatalf("space: %v asked %d", err, asked)
	}
	if des, _ := os.ReadDir(dest); len(des) != 0 {
		t.Fatal("written before the space check")
	}
}

func TestExtractStopsWhenCancelled(t *testing.T) {
	data := makeZip(t, launch, zipEntry{name: "Clock.pak/data", data: "x"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ExtractZip(ctx, bytes.NewReader(data), int64(len(data)), t.TempDir(), ExtractOptions{Platform: "tg5040"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}

func extractNamed(t *testing.T, data []byte, name string) (Staged, error) {
	t.Helper()
	return ExtractZip(context.Background(), bytes.NewReader(data), int64(len(data)), t.TempDir(),
		ExtractOptions{Platform: "tg5040", Name: name, Clean: func(n string) (string, error) {
			return strings.ReplaceAll(n, "?", "_"), nil
		}})
}

// Loose archives (M12 review): launch.sh at the top of the archive.
func TestExtractLooseArchive(t *testing.T) {
	loose := makeZip(t, zipEntry{name: "launch.sh", data: "x"}, zipEntry{name: "bin/tool", data: "y"},
		zipEntry{name: "__MACOSX/._launch.sh", data: "z"})
	s, err := extractNamed(t, loose, "Clock.zip")
	if err != nil || s.Name != "Clock.pak" || s.Kind != "" {
		t.Fatalf("from the archive name: %+v %v", s, err)
	}
	if data, _ := os.ReadFile(filepath.Join(s.Dir, "bin", "tool")); string(data) != "y" {
		t.Fatalf("content %q", data)
	}
	if _, err := os.Lstat(filepath.Join(s.Dir, "__MACOSX")); err == nil {
		t.Fatal("__MACOSX extracted")
	}
	if s, err := extractNamed(t, loose, "Clock.pak.ZIP"); err != nil || s.Name != "Clock.pak" {
		t.Fatalf("pak.zip: %+v %v", s, err)
	}
	if s, err := extractNamed(t, loose, "What?.zip"); err != nil || s.Name != "What_.pak" {
		t.Fatalf("cleaned: %+v %v", s, err)
	}
	// The name of pak.json wins.
	named := makeZip(t, zipEntry{name: "launch.sh", data: "x"}, zipEntry{name: "pak.json", data: `{"name":"Nice Clock","type":"EMU"}`})
	if s, err := extractNamed(t, named, "archive.zip"); err != nil || s.Name != "Nice Clock.pak" {
		t.Fatalf("pak.json name: %+v %v", s, err)
	}
	// An unusable pak.json name falls back to the archive name.
	bad := makeZip(t, zipEntry{name: "launch.sh", data: "x"}, zipEntry{name: "pak.json", data: `{"name":"../evil"}`})
	if s, err := extractNamed(t, bad, "Safe.zip"); err != nil || s.Name != "Safe.pak" {
		t.Fatalf("bad pak.json name: %+v %v", s, err)
	}
	if _, err := extractNamed(t, bad, ".zip"); !isRefusal(err, "cannot name") {
		t.Fatalf("no name: %v", err)
	}
	// Same protections.
	if _, err := extractNamed(t, makeZip(t, zipEntry{name: "launch.sh", data: "x"}, zipEntry{name: "../evil", data: "x"}), "A.zip"); !isRefusal(err, "dot path") {
		t.Fatalf("zip-slip: %v", err)
	}
	if _, err := extractNamed(t, makeZip(t, zipEntry{name: "launch.sh", data: "x"}, zipEntry{name: "l", data: "/etc", mode: fs.ModeSymlink | 0o777}), "A.zip"); !isRefusal(err, "symbolic link") {
		t.Fatalf("link: %v", err)
	}
}

// eocd builds an end of central directory record.
func eocd(entries uint16, dirSize, dirOffset uint32) []byte {
	b := make([]byte, 22)
	binary.LittleEndian.PutUint32(b, 0x06054b50)
	binary.LittleEndian.PutUint16(b[8:], entries)
	binary.LittleEndian.PutUint16(b[10:], entries)
	binary.LittleEndian.PutUint32(b[12:], dirSize)
	binary.LittleEndian.PutUint32(b[16:], dirOffset)
	return b
}

// The central directory is bounded before archive/zip loads it (M12
// review: millions of empty entries in a small archive).
func TestCheckDirectory(t *testing.T) {
	if err := CheckDirectory(bytes.NewReader(eocd(20000, 100, 0)), 22); !isRefusal(err, "more than 10000 entries") {
		t.Fatalf("entries: %v", err)
	}
	if err := CheckDirectory(bytes.NewReader(eocd(10, 8<<20, 0)), 22); !isRefusal(err, "central directory larger") {
		t.Fatalf("directory size: %v", err)
	}
	// zip64: the counts of the zip64 record are used.
	rec := make([]byte, 56)
	binary.LittleEndian.PutUint32(rec, 0x06064b50)
	binary.LittleEndian.PutUint64(rec[32:], 3_000_000)
	binary.LittleEndian.PutUint64(rec[40:], 1000)
	loc := make([]byte, 20)
	binary.LittleEndian.PutUint32(loc, 0x07064b50)
	binary.LittleEndian.PutUint64(loc[8:], 0)
	data := append(append(rec, loc...), eocd(0xffff, 0xffffffff, 0xffffffff)...)
	if err := CheckDirectory(bytes.NewReader(data), int64(len(data))); !isRefusal(err, "more than 10000 entries") {
		t.Fatalf("zip64: %v", err)
	}
	if err := CheckDirectory(bytes.NewReader([]byte("short")), 5); !isRefusal(err, "not a zip") {
		t.Fatalf("short: %v", err)
	}
	// A real archive with a comment passes.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	w.SetComment("comment PK\x05\x06 inside")
	w.Create("a")
	w.Close()
	if err := CheckDirectory(bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil {
		t.Fatalf("real archive: %v", err)
	}
	// And through ExtractZip, before any read of the directory.
	if _, _, err := extract(t, eocd(20000, 100, 0)); !isRefusal(err, "more than 10000") {
		t.Fatalf("extract: %v", err)
	}
}

// A folder sync failure fails the extraction (M12 review).
func TestExtractSyncFailure(t *testing.T) {
	failure := errors.New("sync failed")
	syncDir = func(string) error { return failure }
	defer func() { syncDir = store.SyncDirChecked }()
	if _, _, err := extract(t, makeZip(t, launch, zipEntry{name: "Clock.pak/a/b", data: "x"})); !errors.Is(err, failure) {
		t.Fatalf("sync: %v", err)
	}
}
