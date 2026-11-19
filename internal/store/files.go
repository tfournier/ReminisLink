package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StampLayout is the time format of the identifiers made of a time stamp
// and a random suffix (bk_<stamp>_<hex>, rh_<stamp>_<hex>).
const StampLayout = "20060102T150405Z"

// HashFile returns the SHA-256 (lowercase hex) and the size of a regular
// file.
func HashFile(path string) (string, int64, error) {
	return HashFileContext(context.Background(), path)
}

// HashFileContext is HashFile, stopped with ctx.Err() when ctx is done
// (checked every MiB: the maintenance at start has a time limit).
func HashFileContext(ctx context.Context, path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, ctxReader{ctx, f})
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// ctxReader reads at most 1 MiB at a time and fails once ctx is done.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > 1<<20 {
		p = p[:1<<20]
	}
	return c.r.Read(p)
}

// CopyFileAtomic copies src to dst: it writes <dst>.tmp, fsyncs it, then
// renames it over dst, so dst is either absent (or its previous content)
// or the complete copy. It returns the SHA-256 and the size of the bytes
// copied. The folder of dst is created if needed.
func CopyFileAtomic(src, dst string) (string, int64, error) {
	return CopyFileAtomicContext(context.Background(), src, dst)
}

// CopyFileAtomicContext is CopyFileAtomic, stopped (nothing renamed) with
// ctx.Err() when ctx is done.
func CopyFileAtomicContext(ctx context.Context, src, dst string) (string, int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", 0, err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", 0, err
	}
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), ctxReader{ctx, in})
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return "", 0, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return "", 0, err
	}
	SyncDir(filepath.Dir(dst))
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// WriteJSONAtomic writes v as indented JSON with WriteFileAtomic.
func WriteJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, append(data, '\n'), 0o644)
}

// NewStampID returns prefix + "_" + the UTC time stamp of at + "_" + 4 hex
// digits, for which taken reports false. The random suffix and the check
// keep identifiers unique even when the clock goes back.
func NewStampID(prefix string, at time.Time, random io.Reader, taken func(id string) bool) (string, error) {
	if random == nil {
		random = rand.Reader
	}
	for attempt := 0; attempt < 64; attempt++ {
		var b [2]byte
		if _, err := io.ReadFull(random, b[:]); err != nil {
			return "", err
		}
		id := prefix + "_" + at.UTC().Format(StampLayout) + "_" + hex.EncodeToString(b[:])
		if !taken(id) {
			return id, nil
		}
	}
	return "", errors.New("no free identifier")
}

// FindFold returns the entry of dir named name ignoring case (exFAT; an
// exact match wins), or "". dirs selects folders (true) or regular files
// (false).
func FindFold(dir, name string, dirs bool) string {
	des, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	match := ""
	for _, d := range des {
		if dirs != d.IsDir() || (!dirs && !d.Type().IsRegular()) {
			continue
		}
		if d.Name() == name {
			return filepath.Join(dir, name)
		}
		if match == "" && strings.EqualFold(d.Name(), name) {
			match = filepath.Join(dir, d.Name())
		}
	}
	return match
}

// RemoveTemps deletes the leftover "*.tmp" files of an interrupted atomic
// write directly in dir, and returns how many were removed.
func RemoveTemps(dir string) int {
	des, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, d := range des {
		if d.Type().IsRegular() && strings.HasSuffix(d.Name(), ".tmp") {
			if os.Remove(filepath.Join(dir, d.Name())) == nil {
				n++
			}
		}
	}
	return n
}

// SameFile reports whether the files at a and b have the same content:
// same size, then same SHA-256. A missing file is never the same.
func SameFile(a, b string) (bool, error) {
	fa, err := os.Stat(a)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	fb, err := os.Stat(b)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if fa.Size() != fb.Size() {
		return false, nil
	}
	ha, _, err := HashFile(a)
	if err != nil {
		return false, fmt.Errorf("hash: %w", err)
	}
	hb, _, err := HashFile(b)
	if err != nil {
		return false, fmt.Errorf("hash: %w", err)
	}
	return ha == hb, nil
}
