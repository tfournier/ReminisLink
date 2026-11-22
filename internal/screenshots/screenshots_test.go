package screenshots

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/osadapter/knulli"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

type env struct {
	sd    string
	dir   string // the screenshots folder
	trash *store.Trash
	svc   *Service
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newEnv(t *testing.T) *env {
	t.Helper()
	sd := testutil.CopySDCard(t)
	roots := nextui.RootsForSD(sd, "tg5040")
	return newEnvOn(t, sd, roots, nextui.New(roots, nil))
}

func newEnvOn(t *testing.T, sd string, roots osadapter.Roots, a osadapter.Adapter) *env {
	t.Helper()
	trash := store.NewTrash(store.Layout{Root: roots.Data}, sd, nil, quiet())
	svc, err := New(Options{Adapter: a, Trash: trash, Logger: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	return &env{sd: sd, dir: roots.Screenshots, trash: trash, svc: svc}
}

func pngOf(t testing.TB, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 200, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func (e *env) write(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(e.dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *env) list(t *testing.T) List {
	t.Helper()
	l, err := e.svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func names(l List) string {
	var out []string
	for _, s := range l.Screenshots {
		out = append(out, s.Name)
	}
	return strings.Join(out, "|")
}

func TestListTestCard(t *testing.T) {
	e := newEnv(t)
	l := e.list(t)
	if got := names(l); got != "Starlight Courier.2026-11-21-18-30-00.png|Café Rêverie.2026-11-20-09-15-42.png" {
		t.Fatalf("list: %s", got)
	}
	s := l.Screenshots[0]
	want := time.Date(2026, 11, 21, 18, 30, 0, 0, time.Local).UTC()
	if s.ID != ID(s.Name) || !strings.HasPrefix(s.ID, IDPrefix) || s.GameName == nil || *s.GameName != "Starlight Courier" ||
		!s.TakenAt.Equal(want) || s.TakenAt.Location() != time.UTC || s.Format != FormatPNG || !s.HasThumbnail || s.SizeBytes == 0 {
		t.Fatalf("screenshot %+v", s)
	}
	if l.TotalBytes != l.Screenshots[0].SizeBytes+l.Screenshots[1].SizeBytes || l.Truncated {
		t.Fatalf("total %d truncated %v", l.TotalBytes, l.Truncated)
	}
}

func TestListFiltersAndSorts(t *testing.T) {
	e := newEnv(t)
	img := pngOf(t, 4, 4)
	e.write(t, "shot.JPG", img) // the extension decides the format
	e.write(t, "old.bmp", []byte("BM...."))
	e.write(t, ".hidden.png", img)
	e.write(t, "notes.txt", []byte("x"))
	e.write(t, "video.mp4", []byte("x"))
	e.write(t, "Game.2026-11-21-18-30-00.png.tmp", img)
	e.write(t, "line\nbreak.png", img) // a control character: no identifier
	if err := os.Mkdir(filepath.Join(e.dir, "folder.png"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.write(t, "sub/Inner.2026-11-22-10-00-00.png", img) // not directly in the folder
	old := time.Date(2020, 1, 1, 12, 0, 0, 0, time.UTC)
	for _, n := range []string{"shot.JPG", "old.bmp"} {
		if err := os.Chtimes(filepath.Join(e.dir, n), old, old.Add(time.Duration(len(n))*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	l := e.list(t)
	got := names(l)
	if got != "Starlight Courier.2026-11-21-18-30-00.png|Café Rêverie.2026-11-20-09-15-42.png|shot.JPG|old.bmp" {
		t.Fatalf("list: %s", got)
	}
	shot, bmp := l.Screenshots[2], l.Screenshots[3]
	if shot.Format != FormatJPEG || !shot.HasThumbnail || shot.GameName != nil || !shot.TakenAt.Equal(old.Add(8*time.Second)) {
		t.Fatalf("jpeg %+v", shot)
	}
	if bmp.Format != FormatBMP || bmp.HasThumbnail {
		t.Fatalf("bmp %+v", bmp)
	}
}

func TestListWithoutFolder(t *testing.T) {
	e := newEnv(t)
	if err := os.RemoveAll(e.dir); err != nil {
		t.Fatal(err)
	}
	l := e.list(t)
	if len(l.Screenshots) != 0 || l.Screenshots == nil || l.TotalBytes != 0 {
		t.Fatalf("list %+v", l)
	}
	if _, err := e.svc.Trash([]string{ID("a.png")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("trash without folder: %v", err)
	}
}

func TestListTruncated(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < MaxList+3; i++ {
		name := filepath.Join(e.dir, "s"+strings.Repeat("0", 5-len(itoa(i)))+itoa(i)+".png")
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	l := e.list(t)
	if len(l.Screenshots) != MaxList || !l.Truncated {
		t.Fatalf("%d listed, truncated %v", len(l.Screenshots), l.Truncated)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}

// Rule 2: a link is never listed, opened nor moved, and a folder reached
// through a link has no screenshot.
func TestLinks(t *testing.T) {
	e := newEnv(t)
	outside := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(outside, pngOf(t, 2, 2), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(e.dir, "link.png")); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}
	if strings.Contains(names(e.list(t)), "link.png") {
		t.Fatal("a link is listed")
	}
	if _, _, _, _, err := e.svc.Open(ID("link.png")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("open of a link: %v", err)
	}
	if _, err := e.svc.Thumbnail(context.Background(), ID("link.png")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("preview of a link: %v", err)
	}
	if _, err := e.svc.Trash([]string{ID("link.png")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("trash of a link: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(e.dir, "link.png")); err != nil {
		t.Fatal("the link moved")
	}

	// The whole folder replaced by a link to a folder of images.
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Rename(e.dir, elsewhere); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, e.dir); err != nil {
		t.Fatal(err)
	}
	if l := e.list(t); len(l.Screenshots) != 0 {
		t.Fatalf("listed through a link: %s", names(l))
	}
	name := "Starlight Courier.2026-11-21-18-30-00.png"
	if _, _, _, _, err := e.svc.Open(ID(name)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("open through a link: %v", err)
	}
	if _, err := e.svc.Trash([]string{ID(name)}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("trash through a link: %v", err)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, name)); err != nil {
		t.Fatal("moved through a link")
	}
}

// A link at the root of the card itself is allowed (h700: /mnt/SDCARD ->
// /mnt/sdcard), nothing below it.
func TestCardReachedThroughALink(t *testing.T) {
	sd := testutil.CopySDCard(t)
	link := filepath.Join(t.TempDir(), "SDCARD")
	if err := os.Symlink(sd, link); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}
	roots := nextui.RootsForSD(link, "h700")
	e := newEnvOn(t, link, roots, nextui.New(roots, nil))
	if l := e.list(t); len(l.Screenshots) != 2 {
		t.Fatalf("list: %s", names(l))
	}
	f, _, _, _, err := e.svc.Open(ID("Starlight Courier.2026-11-21-18-30-00.png"))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func TestHostileIDs(t *testing.T) {
	e := newEnv(t)
	b64 := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	for _, id := range []string{
		"", "sc_", "Starlight", b64("Starlight Courier.2026-11-21-18-30-00.png"), // no prefix
		"sc_" + b64("../Roms/x.png"), "sc_" + b64("/etc/passwd.png"), "sc_" + b64("sub/x.png"),
		"sc_" + b64(".."), "sc_" + b64(".hidden.png"), "sc_" + b64("readme.txt"),
		"sc_" + b64("a\x00.png"), "sc_" + b64("a\n.png"), "sc_" + b64("a\\b.png"), "sc_" + b64("\xff.png"),
		"sc_" + b64("x.png") + "=", "sc_" + b64("x.png") + "\n", "sc_../x",
	} {
		if _, _, _, _, err := e.svc.Open(id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Open(%q): %v", id, err)
		}
		if _, err := e.svc.Thumbnail(context.Background(), id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Thumbnail(%q): %v", id, err)
		}
		if _, err := e.svc.Trash([]string{ID("Café Rêverie.2026-11-20-09-15-42.png"), id}); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Trash(%q): %v", id, err)
		}
	}
	if l := e.list(t); len(l.Screenshots) != 2 {
		t.Fatal("a refused trash moved something")
	}
	if _, err := e.svc.Trash(nil); !errors.Is(err, ErrInvalidID) {
		t.Errorf("empty trash request: %v", err)
	}
	many := make([]string, MaxTrash+1)
	for i := range many {
		many[i] = ID("Café Rêverie.2026-11-20-09-15-42.png")
	}
	if _, err := e.svc.Trash(many); !errors.Is(err, ErrInvalidID) {
		t.Errorf("501 identifiers: %v", err)
	}
}

// The identifier names the exact file name: another case is another name
// (on exFAT it would open the same file under a name the list does not
// show).
func TestOpenExactName(t *testing.T) {
	e := newEnv(t)
	name := "Starlight Courier.2026-11-21-18-30-00.png"
	f, fi, got, ct, err := e.svc.Open(ID(name))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(f)
	f.Close()
	want, _ := os.ReadFile(filepath.Join(e.dir, name))
	if got != name || ct != "image/png" || !bytes.Equal(data, want) || fi.Size() != int64(len(want)) {
		t.Fatalf("open: %q %q %d bytes", got, ct, len(data))
	}
	for _, other := range []string{strings.ToUpper(name), "starlight courier.2026-11-21-18-30-00.png", "Missing.png"} {
		if _, _, _, _, err := e.svc.Open(ID(other)); !errors.Is(err, ErrNotFound) {
			t.Errorf("open %q: %v", other, err)
		}
	}
	// The media type comes from the content.
	e.write(t, "fake.png", []byte("not an image"))
	f, _, _, ct, err = e.svc.Open(ID("fake.png"))
	if err != nil || ct != "application/octet-stream" {
		t.Fatalf("fake png: %q %v", ct, err)
	}
	f.Close()
	jp := e.write(t, "photo.png", nil)
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewGray(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jp, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	f, _, _, ct, err = e.svc.Open(ID("photo.png"))
	if err != nil || ct != "image/jpeg" {
		t.Fatalf("jpeg named .png: %q %v", ct, err)
	}
	f.Close()
}

func decodeJPEG(t *testing.T, data []byte) image.Config {
	t.Helper()
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not a JPEG: %v", err)
	}
	return cfg
}

func TestThumbnail(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.write(t, "wide.png", pngOf(t, 1024, 768))
	data, err := e.svc.Thumbnail(ctx, ID("wide.png"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg := decodeJPEG(t, data); cfg.Width != 320 || cfg.Height != 240 {
		t.Fatalf("preview %dx%d", cfg.Width, cfg.Height)
	}
	again, err := e.svc.Thumbnail(ctx, ID("wide.png"))
	if err != nil || !bytes.Equal(again, data) {
		t.Fatalf("cached preview differs: %v", err)
	}
	// A changed file gets a new preview (size and date in the key).
	e.write(t, "wide.png", pngOf(t, 200, 400))
	data, err = e.svc.Thumbnail(ctx, ID("wide.png"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg := decodeJPEG(t, data); cfg.Width != 160 || cfg.Height != 320 {
		t.Fatalf("new preview %dx%d", cfg.Width, cfg.Height)
	}
	// Never enlarged; a JPEG source works too.
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 100, 50)), nil); err != nil {
		t.Fatal(err)
	}
	e.write(t, "small.jpeg", b.Bytes())
	data, err = e.svc.Thumbnail(ctx, ID("small.jpeg"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg := decodeJPEG(t, data); cfg.Width != 100 || cfg.Height != 50 {
		t.Fatalf("small preview %dx%d", cfg.Width, cfg.Height)
	}
}

// pngHeader is a PNG signature and an IHDR announcing w x h (no pixel
// data): a small file claiming a huge image.
func pngHeader(w, h uint32) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 6 // 8-bit RGBA
	chunk := append([]byte("IHDR"), ihdr...)
	_ = binary.Write(&b, binary.BigEndian, uint32(13))
	b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return b.Bytes()
}

func TestThumbnailRefusesHostileImages(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	full := pngOf(t, 64, 64)
	cases := map[string][]byte{
		"huge.png":      pngHeader(100000, 100000),
		"wide.png":      pngHeader(MaxThumbSide+1, 1),
		"truncated.png": full[:len(full)/2],
		"text.png":      []byte("hello"),
		"empty.png":     nil,
		"bitmap.bmp":    []byte("BM" + strings.Repeat("\x00", 60)),
		"jpeg.jpg":      {0xFF, 0xD8, 0xFF, 0xE0, 0, 0},
	}
	for name, data := range cases {
		e.write(t, name, data)
		if _, err := e.svc.Thumbnail(ctx, ID(name)); !errors.Is(err, ErrNoThumbnail) || !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := e.svc.Thumbnail(ctx, ID("missing.png")); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	// Too large on the card: no preview, without reading it.
	big := e.write(t, "big.png", nil)
	if err := os.Truncate(big, MaxThumbSourceBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Thumbnail(ctx, ID("big.png")); !errors.Is(err, ErrNoThumbnail) {
		t.Errorf("big: %v", err)
	}
	for _, s := range e.list(t).Screenshots {
		if s.Name == "big.png" && s.HasThumbnail {
			t.Error("hasThumbnail for a file larger than the limit")
		}
	}
}

func TestThumbnailCacheBounds(t *testing.T) {
	c := newThumbCache(2, 10)
	c.put(thumbKey{name: "a"}, []byte("1234"))
	c.put(thumbKey{name: "b"}, []byte("1234"))
	c.get(thumbKey{name: "a"}) // a is the most recent
	c.put(thumbKey{name: "c"}, []byte("1234"))
	if _, ok := c.get(thumbKey{name: "b"}); ok {
		t.Error("the least recent entry is kept")
	}
	if _, ok := c.get(thumbKey{name: "a"}); !ok {
		t.Error("a recent entry is dropped")
	}
	c.put(thumbKey{name: "d"}, []byte("12345678")) // over 10 bytes with a
	if c.bytes > 10 || c.order.Len() != len(c.index) {
		t.Errorf("%d bytes, %d entries, %d indexed", c.bytes, c.order.Len(), len(c.index))
	}
	c.put(thumbKey{name: "e"}, make([]byte, 11)) // larger than the cache
	if _, ok := c.get(thumbKey{name: "e"}); ok {
		t.Error("an entry larger than the cache is kept")
	}
	c.forget("d")
	if _, ok := c.get(thumbKey{name: "d"}); ok {
		t.Error("forget keeps the entry")
	}
}

func TestTrashAndRestore(t *testing.T) {
	e := newEnv(t)
	a, b := "Starlight Courier.2026-11-21-18-30-00.png", "Café Rêverie.2026-11-20-09-15-42.png"
	before, _ := os.ReadFile(filepath.Join(e.dir, a))
	// Missing ones: nothing moves.
	_, err := e.svc.Trash([]string{ID(a), ID("Gone.png")})
	var missing *MissingError
	if !errors.As(err, &missing) || len(missing.IDs) != 1 || missing.IDs[0] != ID("Gone.png") {
		t.Fatalf("missing: %v", err)
	}
	if len(e.list(t).Screenshots) != 2 {
		t.Fatal("moved despite a missing screenshot")
	}
	// Duplicates count once; one entry for all.
	res, err := e.svc.Trash([]string{ID(a), ID(b), ID(a)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Moved) != 2 || res.Entry.Reason != store.ReasonDeleteScreenshot || res.Entry.Label != "2 screenshots" ||
		res.Moved[0] != "Screenshots/"+a || res.MovedBytes == 0 {
		t.Fatalf("result %+v", res)
	}
	if len(e.list(t).Screenshots) != 0 {
		t.Fatal("still listed")
	}
	// Restored by the restore of M18, at the same place.
	if _, err := e.trash.Restore(res.Entry.ID, store.RestoreFail, nil); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(e.dir, a))
	if !bytes.Equal(before, after) || len(e.list(t).Screenshots) != 2 {
		t.Fatal("not restored")
	}
	// A screenshot alone: labelled with its name; a new one with the same
	// name taken meanwhile: a conflict, then a free name.
	res, err = e.svc.Trash([]string{ID(a)})
	if err != nil || res.Entry.Label != a {
		t.Fatalf("one screenshot: %+v %v", res, err)
	}
	e.write(t, a, pngOf(t, 2, 2))
	_, err = e.trash.Restore(res.Entry.ID, store.RestoreFail, nil)
	var conflict *store.RestoreConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("conflict: %v", err)
	}
	if _, err := e.trash.Restore(res.Entry.ID, store.RestoreRename, nil); err != nil {
		t.Fatal(err)
	}
	renamed := "Starlight Courier.2026-11-21-18-30-00 (2).png"
	if got, _ := os.ReadFile(filepath.Join(e.dir, renamed)); !bytes.Equal(got, before) {
		t.Fatal("not restored under a free name")
	}
	if !e.svc.InFolder(filepath.Join(e.dir, renamed)) || e.svc.InFolder(filepath.Join(e.sd, "Roms", "x.png")) {
		t.Fatal("InFolder")
	}
}

// Knulli: /userdata/screenshots, its names, and on ext4 two names that
// differ by case are two screenshots.
func TestKnulli(t *testing.T) {
	userdata, devEnv := testutil.CopyKnulli(t, t.TempDir())
	roots := knulli.RootsForSD(userdata)
	e := newEnvOn(t, userdata, roots, knulli.New(roots, func(k string) string { return devEnv[k] }))
	l := e.list(t)
	if got := names(l); got != "Starlight Courier-261121-183000.png|knulli-screenshot_256x192_20261120_09h15m42s.png" {
		t.Fatalf("list: %s", got)
	}
	if g := l.Screenshots[0].GameName; g == nil || *g != "Starlight Courier" || l.Screenshots[1].GameName != nil {
		t.Fatalf("games %+v", l.Screenshots)
	}
	res, err := e.svc.Trash([]string{l.Screenshots[1].ID})
	if err != nil || res.Moved[0] != "screenshots/knulli-screenshot_256x192_20261120_09h15m42s.png" {
		t.Fatalf("trash %+v %v", res, err)
	}
}

func TestKnulliCaseSensitive(t *testing.T) {
	userdata, devEnv := testutil.CopyKnulli(t, testutil.CaseSensitiveDir(t))
	roots := knulli.RootsForSD(userdata)
	e := newEnvOn(t, userdata, roots, knulli.New(roots, func(k string) string { return devEnv[k] }))
	e.write(t, "game-261121-183000.png", pngOf(t, 2, 2))
	e.write(t, "GAME-261121-183000.png", pngOf(t, 3, 3))
	if len(e.list(t).Screenshots) != 4 {
		t.Fatalf("list: %s", names(e.list(t)))
	}
	if _, err := e.svc.Trash([]string{ID("GAME-261121-183000.png")}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "game-261121-183000.png")); err != nil {
		t.Fatal("the other case moved too")
	}
}
