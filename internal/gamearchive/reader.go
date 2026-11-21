package gamearchive

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/tfournier/ReminisLink/internal/cheats"
	"github.com/tfournier/ReminisLink/internal/paks"
)

// Entry is a file of a checked archive: its manifest description and its
// zip entry.
type Entry struct {
	File
	zf *zip.File
}

// Archive is a checked game archive.
type Archive struct {
	Manifest Manifest
	// Entries are the files of the manifest, in its order.
	Entries []Entry
}

// Total is the sum of the sizes of the files.
func (a *Archive) Total() int64 {
	var n int64
	for _, e := range a.Entries {
		n += e.SizeBytes
	}
	return n
}

// Find returns the entry of a role (and slot), nil when absent.
func (a *Archive) Find(role string, slot *int) *Entry {
	key := File{Role: role, Slot: slot}.Key()
	for i := range a.Entries {
		if a.Entries[i].Key() == key {
			return &a.Entries[i]
		}
	}
	return nil
}

// Open checks the zip archive r of size bytes (see the package comment)
// and returns it. Refusals are FormatErrors; other errors are I/O errors.
func Open(r io.ReaderAt, size int64) (*Archive, error) {
	// The central directory is bounded (entries and size, read from the
	// end records) before archive/zip reads it.
	if err := paks.CheckDirectoryLimit(r, size, MaxEntries); err != nil {
		if fe, ok := paks.IsFormatError(err); ok {
			return nil, refuse("%s", fe.Reason)
		}
		return nil, err
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, refuse("not a zip archive")
	}
	if len(zr.File) > MaxEntries {
		return nil, refuse("more than %d entries", MaxEntries)
	}
	if err := checkLayout(r, size, zr.File); err != nil {
		return nil, err
	}
	files := map[string]*zip.File{} // lowercase path → file entry
	seen := map[string]bool{}       // lowercase path of every entry
	var manifest *zip.File
	for _, f := range zr.File {
		name := f.Name
		if strings.ContainsRune(name, 0) {
			return nil, refuse("entry name with a NUL byte")
		}
		mode := f.Mode()
		isDir := strings.HasSuffix(name, "/") || mode.IsDir()
		name = strings.TrimSuffix(name, "/")
		if err := paks.CheckEntryPath(name); err != nil {
			fe, _ := paks.IsFormatError(err)
			return nil, refuse("%s", fe.Reason)
		}
		switch {
		case mode&fs.ModeSymlink != 0:
			return nil, refuse("symbolic link in the archive")
		case !isDir && !mode.IsRegular():
			return nil, refuse("entry that is neither a file nor a folder")
		case isDir && mode.Type() != 0 && !mode.IsDir():
			return nil, refuse("entry that is neither a file nor a folder")
		case f.Flags&0x1 != 0:
			return nil, refuse("encrypted archive")
		}
		key := strings.ToLower(name)
		if seen[key] {
			return nil, refuse("two entries with the same name (ignoring case)")
		}
		seen[key] = true
		if isDir || strings.EqualFold(strings.SplitN(name, "/", 2)[0], macOSFolder) {
			continue
		}
		if f.Method != zip.Store && f.Method != zip.Deflate {
			return nil, refuse("unsupported compression method")
		}
		if f.UncompressedSize64 > RatioMinBytes && f.CompressedSize64*MaxRatio < f.UncompressedSize64 {
			return nil, refuse("entry compressed more than %d times", MaxRatio)
		}
		if name == ManifestName {
			manifest = f
			continue
		}
		files[key] = f
	}
	// A path used both as a file and as a folder of another entry.
	for key := range files {
		for p := key; strings.Contains(p, "/"); {
			p = p[:strings.LastIndex(p, "/")]
			if _, ok := files[p]; ok {
				return nil, refuse("two entries with the same name (ignoring case)")
			}
		}
	}
	if manifest == nil {
		return nil, refuse("no %s", ManifestName)
	}
	m, err := readManifest(manifest)
	if err != nil {
		return nil, err
	}
	a := &Archive{Manifest: m}
	listed := map[string]bool{}
	keys := map[string]bool{}
	var total uint64
	for _, mf := range m.Files {
		if err := checkFile(mf); err != nil {
			return nil, err
		}
		key := strings.ToLower(mf.Path)
		zf, ok := files[key]
		if !ok || zf.Name != mf.Path {
			return nil, refuse("a file of the manifest is not in the archive")
		}
		if listed[key] {
			return nil, refuse("a file listed twice in the manifest")
		}
		listed[key] = true
		if keys[mf.Key()] {
			return nil, refuse("two files for the same place (%s)", mf.Key())
		}
		keys[mf.Key()] = true
		if zf.UncompressedSize64 != uint64(mf.SizeBytes) {
			return nil, refuse("a file size differs from the manifest")
		}
		if total += zf.UncompressedSize64; total > MaxTotalBytes {
			return nil, refuse("more than %d bytes in all", int64(MaxTotalBytes))
		}
		e := Entry{File: mf, zf: zf}
		if err := checkDeflate(zf); err != nil {
			return nil, err
		}
		if err := checkContent(e); err != nil {
			return nil, err
		}
		a.Entries = append(a.Entries, e)
	}
	if len(listed) != len(files) {
		return nil, refuse("a file of the archive is not in the manifest")
	}
	return a, nil
}

// readManifest reads and checks manifest.json.
func readManifest(f *zip.File) (Manifest, error) {
	var m Manifest
	if f.UncompressedSize64 > MaxManifestBytes {
		return m, refuse("%s larger than %d bytes", ManifestName, MaxManifestBytes)
	}
	data, err := readAll(f, MaxManifestBytes)
	if err != nil {
		return m, err
	}
	// Strict reading of the known fields: unknown fields are allowed
	// (a newer version adds them), but every known one must have its type.
	if err := json.Unmarshal(data, &m); err != nil {
		return m, refuse("unreadable %s", ManifestName)
	}
	if err := checkRequired(data); err != nil {
		return m, err
	}
	switch {
	case m.Format != FormatName:
		return m, refuse("not a ReminisLink game archive")
	case m.Version > Version:
		return m, refuse("made by a newer ReminisLink (format version %d)", m.Version)
	case m.Version < 1:
		return m, refuse("unknown format version")
	}
	s := m.Source
	for _, text := range []string{s.OS, s.SystemTag, s.SystemLabel, s.GameName, s.FileName, s.RomName, s.Kind} {
		if len(text) > maxSourceTextBytes || !utf8.ValidString(text) {
			return m, refuse("invalid source in %s", ManifestName)
		}
	}
	if len(m.Files) == 0 {
		return m, refuse("no file in the archive")
	}
	if len(m.Files) > MaxEntries {
		return m, refuse("more than %d entries", MaxEntries)
	}
	return m, nil
}

// checkFile checks a file of the manifest on its own.
func checkFile(f File) error {
	if err := paks.CheckEntryPath(f.Path); err != nil || f.Path == ManifestName {
		return refuse("invalid file path in the manifest")
	}
	if !knownRole(f.Role) {
		return refuse("unknown file role in the manifest")
	}
	if slotted(f.Role) != (f.Slot != nil) {
		return refuse("a slot is missing or not expected in the manifest")
	}
	if f.Slot != nil && (*f.Slot < 0 || *f.Slot > MaxSlot) {
		return refuse("invalid slot in the manifest")
	}
	if f.SizeBytes < 0 || f.SizeBytes > maxBytes(f.Role) {
		return refuse("a %s file is too large", f.Role)
	}
	if !sha256Re.MatchString(f.SHA256) {
		return refuse("invalid SHA-256 in the manifest")
	}
	return nil
}

// checkContent checks what can be checked of a file before its
// extraction: the PNG signature of the images, the format of the cheats.
func checkContent(e Entry) error {
	switch e.Role {
	case RoleArt, RoleStateThumbnail:
		rc, err := e.zf.Open()
		if err != nil {
			return refuse("unreadable entry")
		}
		defer rc.Close()
		sig := make([]byte, len(pngSignature))
		if _, err := io.ReadFull(rc, sig); err != nil || string(sig) != pngSignature {
			return refuse("an image is not a PNG")
		}
	case RoleCheats:
		data, err := readAll(e.zf, MaxCheatsBytes)
		if err != nil {
			return err
		}
		// Any encoding (a console's own file may not be UTF-8); only a
		// file the cheat parser cannot read to its end is refused.
		if _, err := cheats.ParseFile(data); err != nil {
			return refuse("unreadable cheat file")
		}
	}
	return nil
}

// readAll reads a whole entry of at most limit bytes, checking its
// announced size.
func readAll(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, refuse("unreadable entry")
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil || int64(len(data)) > limit || uint64(len(data)) != f.UncompressedSize64 {
		return nil, refuse("corrupt archive")
	}
	return data, nil
}

// Extract writes the file of e to dst (created, never replaced), checks
// its size and SHA-256 against the manifest, syncs it, and returns the
// bytes written. A mismatch removes dst and is a FormatError. progress,
// when set, gets the bytes written so far.
func (a *Archive) Extract(ctx context.Context, e Entry, dst string, progress func(int64)) (int64, error) {
	rc, err := e.zf.Open()
	if err != nil {
		return 0, refuse("unreadable entry")
	}
	defer rc.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, err
	}
	ok := false
	defer func() {
		if !ok {
			out.Close()
			os.Remove(dst)
		}
	}()
	h := sha256.New()
	buf := make([]byte, 64<<10)
	var n int64
	limited := io.LimitReader(rc, e.SizeBytes+1)
	for {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		c, rerr := limited.Read(buf)
		if c > 0 {
			h.Write(buf[:c])
			if _, werr := out.Write(buf[:c]); werr != nil {
				return n, werr
			}
			n += int64(c)
			if progress != nil {
				progress(n)
			}
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return n, refuse("corrupt archive")
		}
	}
	if n != e.SizeBytes || hex.EncodeToString(h.Sum(nil)) != e.SHA256 {
		return n, refuse("a file does not match its SHA-256 or size in the manifest")
	}
	if err := out.Sync(); err != nil {
		return n, err
	}
	if err := out.Close(); err != nil {
		return n, err
	}
	ok = true
	return n, nil
}

// PNG reports whether data starts with the PNG signature.
func PNG(data []byte) bool { return bytes.HasPrefix(data, []byte(pngSignature)) }

// checkRequired checks that the required fields of the manifest are
// present and not null (GameArchiveManifest, GameArchiveSource,
// GameArchiveFile): a missing field would read as its zero value (Codex
// review).
func checkRequired(data []byte) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return refuse("unreadable %s", ManifestName)
	}
	present := func(o map[string]json.RawMessage, keys ...string) bool {
		for _, k := range keys {
			if v, ok := o[k]; !ok || string(v) == "null" {
				return false
			}
		}
		return true
	}
	if !present(top, "format", "version", "createdAt", "source", "files") {
		return refuse("a required field is missing in %s", ManifestName)
	}
	var source map[string]json.RawMessage
	if json.Unmarshal(top["source"], &source) != nil ||
		!present(source, "os", "systemTag", "systemLabel", "gameName", "fileName", "romName", "kind") {
		return refuse("a required field is missing in %s", ManifestName)
	}
	var files []map[string]json.RawMessage
	if json.Unmarshal(top["files"], &files) != nil {
		return refuse("unreadable %s", ManifestName)
	}
	for _, f := range files {
		if !present(f, "path", "role", "sizeBytes", "sha256") {
			return refuse("a required field is missing in %s", ManifestName)
		}
	}
	return nil
}

// checkDeflate inflates a deflated entry (output discarded, bounded by its
// announced size) and requires the stream to produce exactly that size
// and to end exactly at the end of its compressed range: no byte of the
// range is ignored by the decompressor, so the sizes the ratio check uses
// are those of the data really inflated (Codex review).
func checkDeflate(zf *zip.File) error {
	if zf.Method != zip.Deflate {
		return nil
	}
	raw, err := zf.OpenRaw()
	if err != nil {
		return refuse("unreadable entry")
	}
	cr := &countingReader{br: bufio.NewReader(raw)}
	fr := flate.NewReader(cr)
	defer fr.Close()
	n, err := io.Copy(io.Discard, io.LimitReader(fr, int64(zf.UncompressedSize64)+1))
	if err != nil || uint64(n) != zf.UncompressedSize64 {
		return refuse("corrupt archive")
	}
	if uint64(cr.n) != zf.CompressedSize64 {
		return refuse("bytes after the end of a compressed entry")
	}
	return nil
}

// countingReader counts the bytes the decompressor consumed: flate reads
// byte by byte from an io.ByteReader, never ahead.
type countingReader struct {
	br *bufio.Reader
	n  int64
}

func (c *countingReader) ReadByte() (byte, error) {
	b, err := c.br.ReadByte()
	if err == nil {
		c.n++
	}
	return b, err
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.br.Read(p)
	c.n += int64(n)
	return n, err
}
