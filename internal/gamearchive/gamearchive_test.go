package gamearchive

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var png = append([]byte(pngSignature), "rest of a png"...)

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func intp(i int) *int { return &i }

// entry is a zip entry of a test archive.
type entry struct {
	name    string
	data    []byte
	mode    fs.FileMode
	method  uint16
	flags   uint16
	dir     bool
	rawSize int64 // when > 0: the announced uncompressed size (raw entry)
}

func source() Source {
	return Source{OS: "nextui", SystemTag: "GBA", SystemLabel: "Game Boy Advance", GameName: "Sky",
		FileName: "Sky.gba", RomName: "Sky.gba", Kind: "file"}
}

// build writes a zip with entries and, when m is not nil, a manifest.
func build(t *testing.T, entries []entry, m *Manifest) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		name := e.name
		if e.dir {
			name += "/"
		}
		h := &zip.FileHeader{Name: name, Method: e.method, Flags: e.flags}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		if e.rawSize > 0 || (e.method != zip.Store && e.method != zip.Deflate) {
			if e.rawSize == 0 {
				e.rawSize = int64(len(e.data))
			} else {
				h.Method = zip.Store
			}
			h.CompressedSize64 = uint64(len(e.data))
			h.UncompressedSize64 = uint64(e.rawSize)
			h.CRC32 = 0
			w, err := zw.CreateRaw(h)
			if err != nil {
				t.Fatal(err)
			}
			w.Write(e.data)
			continue
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if !e.dir {
			w.Write(e.data)
		}
	}
	if m != nil {
		data, _ := json.Marshal(m)
		w, _ := zw.Create(ManifestName)
		w.Write(data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// valid returns the entries and manifest of a well-formed archive.
func valid() ([]entry, *Manifest) {
	save := []byte("save bytes")
	state := bytes.Repeat([]byte("state"), 100)
	cht := []byte("cheats = 1\n\ncheat0_desc = \"Money\"\ncheat0_code = \"1234\"\ncheat0_enable = false\n")
	files := []File{
		{Path: "save/Sky.gba.sav", Role: RoleSave, SizeBytes: int64(len(save)), SHA256: sum(save)},
		{Path: "states/0/Sky.gba.st0", Role: RoleState, Slot: intp(0), SizeBytes: int64(len(state)), SHA256: sum(state)},
		{Path: "states/0/thumbnail.png", Role: RoleStateThumbnail, Slot: intp(0), SizeBytes: int64(len(png)), SHA256: sum(png)},
		{Path: "cheats/Sky.gba.cht", Role: RoleCheats, SizeBytes: int64(len(cht)), SHA256: sum(cht)},
		{Path: "art/Sky.png", Role: RoleArt, SizeBytes: int64(len(png)), SHA256: sum(png)},
	}
	entries := []entry{
		{name: "save", dir: true},
		{name: "save/Sky.gba.sav", data: save, method: zip.Deflate},
		{name: "states/0/Sky.gba.st0", data: state, method: zip.Deflate},
		{name: "states/0/thumbnail.png", data: png},
		{name: "cheats/Sky.gba.cht", data: cht},
		{name: "art/Sky.png", data: png},
		{name: "__MACOSX/._Sky.png", data: []byte("junk")},
	}
	return entries, &Manifest{Format: FormatName, Version: 1, CreatedAt: time.Now(), Source: source(), Files: files}
}

func open(t *testing.T, data []byte) (*Archive, error) {
	t.Helper()
	return Open(bytes.NewReader(data), int64(len(data)))
}

func TestOpenValid(t *testing.T) {
	entries, m := valid()
	a, err := open(t, build(t, entries, m))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Entries) != 5 || a.Find(RoleState, intp(0)) == nil || a.Find(RoleROM, nil) != nil {
		t.Fatalf("entries %+v", a.Entries)
	}
	dir := t.TempDir()
	for _, e := range a.Entries {
		dst := filepath.Join(dir, strings.ReplaceAll(e.Path, "/", "_"))
		n, err := a.Extract(context.Background(), e, dst, nil)
		if err != nil || n != e.SizeBytes {
			t.Fatalf("extract %s: %d %v", e.Path, n, err)
		}
		data, _ := os.ReadFile(dst)
		if sum(data) != e.SHA256 {
			t.Errorf("%s: content differs", e.Path)
		}
	}
}

func TestWriterRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	add := func(role string, slot *int, name string, data []byte) {
		t.Helper()
		if _, err := w.Add(role, slot, name, now, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
	add(RoleSave, nil, "Sky.gba.sav", []byte("save"))
	add(RoleRTC, nil, "Sky.gba.rtc", []byte("rtc"))
	add(RoleState, intp(3), "Sky.gba.st3", []byte("state 3"))
	add(RoleStateThumbnail, intp(3), "Sky.gba.3.bmp", png)
	add(RoleArt, nil, "Sky.png", png)
	add(RoleROM, nil, "../../evil:name.gba", []byte("rom")) // an unusable name gets a generic one
	if _, err := w.Add("other", nil, "x", now, bytes.NewReader(nil)); err == nil {
		t.Error("unknown role accepted")
	}
	if err := w.Close(source(), now); err != nil {
		t.Fatal(err)
	}
	a, err := open(t, buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Entries) != 6 || a.Manifest.Source.GameName != "Sky" || a.Manifest.Version != Version {
		t.Fatalf("manifest %+v", a.Manifest)
	}
	if p := a.Find(RoleROM, nil).Path; p != "rom/file6" {
		t.Errorf("rom path %q", p)
	}
	if p := a.Find(RoleStateThumbnail, intp(3)).Path; p != "states/3/thumbnail.png" {
		t.Errorf("thumbnail path %q", p)
	}
}

// TestHostileArchives: every archive here must be refused with a
// FormatError, before anything is extracted.
func TestHostileArchives(t *testing.T) {
	type mutate func(entries []entry, m *Manifest) ([]entry, *Manifest)
	withFile := func(name string, data []byte, f File) mutate {
		return func(entries []entry, m *Manifest) ([]entry, *Manifest) {
			f.Path, f.SizeBytes, f.SHA256 = name, int64(len(data)), sum(data)
			m.Files = append(m.Files, f)
			return append(entries, entry{name: name, data: data}), m
		}
	}
	rom := File{Role: RoleROM}
	cases := map[string]mutate{
		"zip slip":             withFile("../evil.gba", []byte("x"), rom),
		"zip slip inside":      withFile("rom/../../evil.gba", []byte("x"), rom),
		"absolute path":        withFile("/etc/evil.gba", []byte("x"), rom),
		"drive path":           withFile("C:/evil.gba", []byte("x"), rom),
		"backslash":            withFile("rom\\evil.gba", []byte("x"), rom),
		"dot component":        withFile("rom/./evil.gba", []byte("x"), rom),
		"empty component":      withFile("rom//evil.gba", []byte("x"), rom),
		"control character":    withFile("rom/evil\x01.gba", []byte("x"), rom),
		"invalid utf-8":        withFile("rom/evil\xff.gba", []byte("x"), rom),
		"exfat forbidden":      withFile("rom/evil?.gba", []byte("x"), rom),
		"long component":       withFile("rom/"+strings.Repeat("a", 256), []byte("x"), rom),
		"manifest as a file":   withFile(ManifestName+"x", []byte("x"), File{Role: RoleSave}),
		"two saves":            withFile("save/other.sav", []byte("x"), File{Role: RoleSave}),
		"two states in a slot": withFile("states/0/other", []byte("x"), File{Role: RoleState, Slot: intp(0)}),
		"unknown role":         withFile("x/y", []byte("x"), File{Role: "config"}),
		"slot on a save":       withFile("rtc/x", []byte("x"), File{Role: RoleRTC, Slot: intp(1)}),
		"state without slot":   withFile("states/x", []byte("x"), File{Role: RoleState}),
		"slot out of range":    withFile("states/100/x", []byte("x"), File{Role: RoleState, Slot: intp(100)}),
		"negative slot":        withFile("states/n/x", []byte("x"), File{Role: RoleState, Slot: intp(-1)}),
		"thumbnail not png":    withFile("states/1/t.png", []byte("BMxxxxxxxx"), File{Role: RoleStateThumbnail, Slot: intp(1)}),
		"duplicate case-insensitive": func(entries []entry, m *Manifest) ([]entry, *Manifest) {
			return append(entries, entry{name: "SAVE/sky.GBA.sav", data: []byte("x")}), m
		},
		"file and folder with one name": func(entries []entry, m *Manifest) ([]entry, *Manifest) {
			return append(entries, entry{name: "save/Sky.gba.sav/inner", data: []byte("x")}), m
		},
		"entry not in the manifest": func(entries []entry, m *Manifest) ([]entry, *Manifest) {
			return append(entries, entry{name: "extra/file", data: []byte("x")}), m
		},
		"manifest file not in the archive": func(entries []entry, m *Manifest) ([]entry, *Manifest) {
			m.Files = append(m.Files, File{Path: "rom/Sky.gba", Role: RoleROM, SizeBytes: 1, SHA256: sum([]byte("x"))})
			return entries, m
		},
		"manifest path differs by case": func(entries []entry, m *Manifest) ([]entry, *Manifest) {
			m.Files[0].Path = "SAVE/Sky.gba.sav"
			return entries, m
		},
		"size differs from the manifest": func(entries []entry, m *Manifest) ([]entry, *Manifest) {
			m.Files[0].SizeBytes++
			return entries, m
		},
		"oversize data file": func(entries []entry, m *Manifest) ([]entry, *Manifest) {
			m.Files[0].SizeBytes = MaxDataBytes + 1
			return entries, m
		},
		"oversize art": func(entries []entry, m *Manifest) ([]entry, *Manifest) {
			m.Files[4].SizeBytes = MaxArtBytes + 1
			return entries, m
		},
		"bad sha256": func(entries []entry, m *Manifest) ([]entry, *Manifest) {
			m.Files[0].SHA256 = "ZZ"
			return entries, m
		},
		"symbolic link": func(entries []entry, m *Manifest) ([]entry, *Manifest) {
			return append(entries, entry{name: "link", data: []byte("/etc/passwd"), mode: fs.ModeSymlink | 0o777}), m
		},
		"device file": func(entries []entry, m *Manifest) ([]entry, *Manifest) {
			return append(entries, entry{name: "dev", mode: fs.ModeDevice | 0o644}), m
		},
		"encrypted": func(entries []entry, m *Manifest) ([]entry, *Manifest) {
			entries[1].flags |= 0x1
			return entries, m
		},
		"unknown compression": func(entries []entry, m *Manifest) ([]entry, *Manifest) {
			entries[1].method = 99
			return entries, m
		},
		"zip bomb ratio": func(entries []entry, m *Manifest) ([]entry, *Manifest) {
			data := make([]byte, 4<<20)
			entries = append(entries, entry{name: "rom/Sky.gba", data: data, method: zip.Deflate})
			m.Files = append(m.Files, File{Path: "rom/Sky.gba", Role: RoleROM, SizeBytes: int64(len(data)), SHA256: sum(data)})
			return entries, m
		},
		"too many entries": func(entries []entry, m *Manifest) ([]entry, *Manifest) {
			for i := range MaxEntries {
				entries = append(entries, entry{name: fmt.Sprintf("d%d", i), dir: true})
			}
			return entries, m
		},
		"no manifest": func(entries []entry, _ *Manifest) ([]entry, *Manifest) { return entries, nil },
		"newer version": func(entries []entry, m *Manifest) ([]entry, *Manifest) {
			m.Version = 2
			return entries, m
		},
		"other format": func(entries []entry, m *Manifest) ([]entry, *Manifest) {
			m.Format = "something-else"
			return entries, m
		},
		"empty manifest": func(entries []entry, m *Manifest) ([]entry, *Manifest) {
			m.Files = nil
			return nil, m
		},
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			entries, m := valid()
			entries, m = mut(entries, m)
			_, err := open(t, build(t, entries, m))
			if _, ok := IsFormatError(err); !ok {
				t.Fatalf("not refused: %v", err)
			}
		})
	}
	t.Run("not a zip", func(t *testing.T) {
		if _, err := open(t, []byte("hello, not a zip archive at all")); err == nil {
			t.Fatal("accepted")
		}
	})
	t.Run("manifest too large", func(t *testing.T) {
		entries, m := valid()
		m.Source.GameName = strings.Repeat("x", MaxManifestBytes)
		if _, err := open(t, build(t, entries, m)); err == nil {
			t.Fatal("accepted")
		}
	})
}

// TestExtractChecksContent: an entry whose bytes are not those of the
// manifest (or longer than announced) is refused at extraction, and its
// file removed.
func TestExtractChecksContent(t *testing.T) {
	entries, m := valid()
	m.Files[0].SHA256 = sum([]byte("other bytes"))
	a, err := open(t, build(t, entries, m))
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "x")
	if _, err := a.Extract(context.Background(), a.Entries[0], dst, nil); err == nil {
		t.Fatal("wrong SHA-256 extracted")
	} else if _, ok := IsFormatError(err); !ok {
		t.Fatalf("error %v", err)
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("refused file left behind")
	}

	// The real data is longer than the announced size.
	long := bytes.Repeat([]byte("r"), 100)
	entries, m = valid()
	entries = append(entries, entry{name: "rtc/x.rtc", data: long, rawSize: 10})
	m.Files = append(m.Files, File{Path: "rtc/x.rtc", Role: RoleRTC, SizeBytes: 10, SHA256: sum(long[:10])})
	a, err = open(t, build(t, entries, m))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Extract(context.Background(), *a.Find(RoleRTC, nil), filepath.Join(t.TempDir(), "y"), nil); err == nil {
		t.Fatal("entry longer than announced extracted")
	}

	// An existing destination is never replaced.
	entries, m = valid()
	a, _ = open(t, build(t, entries, m))
	existing := filepath.Join(t.TempDir(), "z")
	os.WriteFile(existing, []byte("keep"), 0o644)
	if _, err := a.Extract(context.Background(), a.Entries[0], existing, nil); err == nil {
		t.Fatal("existing file replaced")
	}
	if data, _ := os.ReadFile(existing); string(data) != "keep" {
		t.Error("existing file changed")
	}
}
