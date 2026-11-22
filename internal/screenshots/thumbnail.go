package screenshots

import (
	"bufio"
	"bytes"
	"container/list"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"sync"
)

// Limits of the previews (GET /screenshots/{screenshotId}/thumbnail).
const (
	// ThumbSize: a preview fits in ThumbSize x ThumbSize pixels.
	ThumbSize = 320
	// MaxThumbSide and MaxThumbSourceBytes: a larger image gets no
	// preview (memory of the console: 4096 x 4096 RGBA is 64 MiB).
	MaxThumbSide        = 4096
	MaxThumbSourceBytes = 32 << 20
	// thumbQuality is the JPEG quality of a preview.
	thumbQuality = 80
	// maxDecodes previews are made at the same time at most.
	maxDecodes = 2
	// The cache keeps the recent previews in memory.
	thumbCacheEntries = 512
	thumbCacheBytes   = 8 << 20
)

// ErrNoThumbnail: the screenshot exists but no preview can be made (BMP,
// undecodable, too large): 404 not_found.
var ErrNoThumbnail = fmt.Errorf("%w: no preview", ErrNotFound)

// Thumbnail returns the JPEG preview of a screenshot, from the cache when
// the file has the same size and date as when it was made.
func (s *Service) Thumbnail(ctx context.Context, id string) ([]byte, error) {
	name, err := nameOf(id)
	if err != nil {
		return nil, err
	}
	if f := formatOf(name); f != FormatPNG && f != FormatJPEG {
		return nil, ErrNoThumbnail
	}
	file, fi, err := s.open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	// The key describes the opened file, never a later lookup.
	key := thumbKey{name: name, size: fi.Size(), mod: fi.ModTime().UnixNano()}
	if data, ok := s.thumbs.get(key); ok {
		return data, nil
	}
	if fi.Size() > MaxThumbSourceBytes {
		return nil, ErrNoThumbnail
	}
	select {
	case s.decode <- struct{}{}:
		defer func() { <-s.decode }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	data, err := MakeThumbnail(io.LimitReader(file, MaxThumbSourceBytes))
	if err != nil {
		if errors.Is(err, ErrNoThumbnail) {
			return nil, err
		}
		return nil, fmt.Errorf("screenshots: %w", err)
	}
	s.thumbs.put(key, data)
	return data, nil
}

// MakeThumbnail decodes a PNG or JPEG image (found by its signature) and
// returns a JPEG that fits in ThumbSize x ThumbSize. An image of another
// format, undecodable or larger than MaxThumbSide gives ErrNoThumbnail;
// an error reading r is returned as it is.
func MakeThumbnail(r io.Reader) ([]byte, error) {
	br := bufio.NewReader(r)
	head, err := br.Peek(8)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err // the card, not the image
	}
	var decodeConfig func(io.Reader) (image.Config, error)
	var decode func(io.Reader) (image.Image, error)
	switch {
	case isPNG(head):
		decodeConfig, decode = png.DecodeConfig, png.Decode
	case isJPEG(head):
		decodeConfig, decode = jpeg.DecodeConfig, jpeg.Decode
	default:
		return nil, ErrNoThumbnail
	}
	// The header first: the size is checked before any pixel is
	// allocated (a small file can announce a huge image).
	var buf bytes.Buffer
	cfg, err := decodeConfig(io.TeeReader(br, &buf))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > MaxThumbSide || cfg.Height > MaxThumbSide {
		return nil, ErrNoThumbnail
	}
	img, err := decode(io.MultiReader(&buf, br))
	if err != nil {
		return nil, ErrNoThumbnail
	}
	small := downscale(img, ThumbSize)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, small, &jpeg.Options{Quality: thumbQuality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// fit returns the size of a w x h image scaled down to fit in max x max
// (never enlarged, at least 1 x 1).
func fit(w, h, max int) (int, int) {
	if w <= max && h <= max {
		return w, h
	}
	if w >= h {
		return max, maxInt(1, h*max/w)
	}
	return maxInt(1, w*max/h), max
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// downscale averages the pixels of img into an opaque image that fits in
// max x max (a box filter: each target pixel is the mean of the source
// pixels it covers). Screenshots are opaque; transparency is dropped.
func downscale(img image.Image, max int) *image.RGBA {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dw, dh := fit(sw, sh, max)
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for dy := 0; dy < dh; dy++ {
		y0, y1 := dy*sh/dh, (dy+1)*sh/dh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for dx := 0; dx < dw; dx++ {
			x0, x1 := dx*sw/dw, (dx+1)*sw/dw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, bl, n uint64
			if pix, stride, ok := rawPixels(img); ok {
				// 8-bit RGBA or NRGBA (what PNG screenshots decode to):
				// read the bytes, without an interface call per pixel.
				for y := y0; y < y1; y++ {
					row := y*stride + x0*4
					for x := x0; x < x1; x++ {
						r, g, bl, n = r+uint64(pix[row]), g+uint64(pix[row+1]), bl+uint64(pix[row+2]), n+1
						row += 4
					}
				}
				r, g, bl = r*0x101, g*0x101, bl*0x101
			} else {
				for y := y0; y < y1; y++ {
					for x := x0; x < x1; x++ {
						cr, cg, cb, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
						r, g, bl, n = r+uint64(cr), g+uint64(cg), bl+uint64(cb), n+1
					}
				}
			}
			i := dst.PixOffset(dx, dy)
			dst.Pix[i] = uint8(r / n >> 8)
			dst.Pix[i+1] = uint8(g / n >> 8)
			dst.Pix[i+2] = uint8(bl / n >> 8)
			dst.Pix[i+3] = 0xFF
		}
	}
	return dst
}

// rawPixels returns the bytes of an 8-bit RGBA or NRGBA image, starting
// at its first pixel, and the length of a row.
func rawPixels(img image.Image) ([]byte, int, bool) {
	switch m := img.(type) {
	case *image.NRGBA:
		return m.Pix[m.PixOffset(m.Rect.Min.X, m.Rect.Min.Y):], m.Stride, true
	case *image.RGBA:
		return m.Pix[m.PixOffset(m.Rect.Min.X, m.Rect.Min.Y):], m.Stride, true
	}
	return nil, 0, false
}

// thumbKey names a preview: the file name, size and modification time of
// the file it was made from.
type thumbKey struct {
	name string
	size int64
	mod  int64
}

// thumbCache is a least-recently-used cache of previews, bounded in
// count and bytes.
type thumbCache struct {
	mu         sync.Mutex
	maxEntries int
	maxBytes   int
	bytes      int
	order      *list.List // front: most recent; values are *thumbEntry
	index      map[thumbKey]*list.Element
}

type thumbEntry struct {
	key  thumbKey
	data []byte
}

func newThumbCache(entries, bytes int) *thumbCache {
	return &thumbCache{maxEntries: entries, maxBytes: bytes, order: list.New(), index: map[thumbKey]*list.Element{}}
}

func (c *thumbCache) get(k thumbKey) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.index[k]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(e)
	return e.Value.(*thumbEntry).data, true
}

func (c *thumbCache) put(k thumbKey, data []byte) {
	if len(data) > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.index[k]; ok {
		c.removeLocked(e)
	}
	c.index[k] = c.order.PushFront(&thumbEntry{key: k, data: data})
	c.bytes += len(data)
	for c.order.Len() > c.maxEntries || c.bytes > c.maxBytes {
		c.removeLocked(c.order.Back())
	}
}

// forget drops the previews of a file name (moved to the trash).
func (c *thumbCache) forget(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.index {
		if k.name == name {
			c.removeLocked(e)
		}
	}
}

func (c *thumbCache) removeLocked(e *list.Element) {
	te := e.Value.(*thumbEntry)
	c.bytes -= len(te.data)
	delete(c.index, te.key)
	c.order.Remove(e)
}
