package gamearchive

import (
	"archive/zip"
	"compress/flate"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/tfournier/ReminisLink/internal/cheats"
	"github.com/tfournier/ReminisLink/internal/paks"
)

// Writer writes a game archive as a stream: the files first, then the
// manifest with the size and SHA-256 of the bytes written for each.
type Writer struct {
	zw    *zip.Writer
	files []File
	used  map[string]bool // lowercase entry paths
}

// NewWriter returns a Writer to w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{zw: zip.NewWriter(w), used: map[string]bool{ManifestName: true}}
}

// Files are the files added so far.
func (w *Writer) Files() []File { return w.files }

// entryPath is the informative path of a file in the archive: a folder by
// role (and slot), and its name on the console when it is a usable entry
// name, else a generic one.
func (w *Writer) entryPath(role string, slot *int, name string) string {
	dir := role
	if slotted(role) {
		dir = "states/" + strconv.Itoa(*slot)
	}
	if role == RoleStateThumbnail {
		name = "thumbnail.png"
	}
	p := dir + "/" + name
	if paks.CheckEntryPath(p) != nil || w.used[strings.ToLower(p)] {
		p = fmt.Sprintf("%s/file%d", dir, len(w.files)+1)
	}
	w.used[strings.ToLower(p)] = true
	return p
}

// AddFile writes the first size bytes of r as a file of the archive,
// deflated unless that would compress it more than an import accepts
// (MaxRatio, Codex review: a big state full of zeros), then stored.
func (w *Writer) AddFile(role string, slot *int, name string, modified time.Time, r io.ReadSeeker, size int64) (File, error) {
	method := methodFor(role)
	if method == zip.Deflate && size > RatioMinBytes {
		compressed, err := deflatedSize(io.LimitReader(r, size))
		if err != nil {
			return File{}, err
		}
		if _, err := r.Seek(0, io.SeekStart); err != nil {
			return File{}, err
		}
		if compressed*MaxRatio < size {
			method = zip.Store
		}
	}
	return w.add(role, slot, name, modified, io.LimitReader(r, size), method)
}

// Add writes the content of r as a file of the archive. name is its name
// on the console (informative); modified its date. Use AddFile for a file
// larger than RatioMinBytes.
func (w *Writer) Add(role string, slot *int, name string, modified time.Time, r io.Reader) (File, error) {
	return w.add(role, slot, name, modified, r, methodFor(role))
}

// methodFor is the compression of a role: none for what is already
// compressed (images) or large (ROM).
func methodFor(role string) uint16 {
	if role == RoleROM || role == RoleArt || role == RoleStateThumbnail {
		return zip.Store
	}
	return zip.Deflate
}

// deflatedSize is the size of r once deflated (what the zip writer makes).
func deflatedSize(r io.Reader) (int64, error) {
	var c countWriter
	fw, err := flate.NewWriter(&c, flate.DefaultCompression)
	if err != nil {
		return 0, err
	}
	if _, err := io.Copy(fw, r); err != nil {
		return 0, err
	}
	if err := fw.Close(); err != nil {
		return 0, err
	}
	return int64(c), nil
}

type countWriter int64

func (c *countWriter) Write(p []byte) (int, error) {
	*c += countWriter(len(p))
	return len(p), nil
}

func (w *Writer) add(role string, slot *int, name string, modified time.Time, r io.Reader, method uint16) (File, error) {
	if !knownRole(role) || slotted(role) != (slot != nil) {
		return File{}, fmt.Errorf("gamearchive: invalid role %q", role)
	}
	p := w.entryPath(role, slot, name)
	hdr := &zip.FileHeader{Name: p, Method: method, Modified: modified.UTC()}
	hdr.SetMode(0o644)
	fw, err := w.zw.CreateHeader(hdr)
	if err != nil {
		return File{}, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(fw, h), r)
	if err != nil {
		return File{}, err
	}
	f := File{Path: p, Role: role, Slot: slot, SizeBytes: n, SHA256: hex.EncodeToString(h.Sum(nil))}
	w.files = append(w.files, f)
	return f, nil
}

// Close writes the manifest and the end of the archive.
func (w *Writer) Close(source Source, createdAt time.Time) error {
	m := Manifest{Format: FormatName, Version: Version, CreatedAt: createdAt.UTC().Truncate(time.Second), Source: source, Files: w.files}
	if m.Files == nil {
		m.Files = []File{}
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	hdr := &zip.FileHeader{Name: ManifestName, Method: zip.Deflate, Modified: m.CreatedAt}
	hdr.SetMode(0o644)
	fw, err := w.zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	if _, err := fw.Write(data); err != nil {
		return err
	}
	return w.zw.Close()
}

// Exportable reports whether a file of role and size, read from r, would
// pass the checks of an import (size limit of its role, PNG signature of
// the images, readable cheats), so an export never makes an archive that
// every importer refuses. r is rewound to its start.
func Exportable(role string, size int64, r io.ReadSeeker) bool {
	if !knownRole(role) || size < 0 || size > maxBytes(role) {
		return false
	}
	ok := true
	switch role {
	case RoleArt, RoleStateThumbnail:
		sig := make([]byte, len(pngSignature))
		_, err := io.ReadFull(r, sig)
		ok = err == nil && string(sig) == pngSignature
	case RoleCheats:
		data, err := io.ReadAll(io.LimitReader(r, MaxCheatsBytes+1))
		if err == nil && int64(len(data)) == size {
			_, err = cheats.ParseFile(data)
		}
		ok = err == nil && int64(len(data)) == size
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return false
	}
	return ok
}
