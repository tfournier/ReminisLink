package server

import (
	"sync"
	"time"
)

// maxArtETags bounds the ETag cache of the art (about 100 bytes an entry);
// it is emptied when full.
const maxArtETags = 8192

// artKey identifies one version of an art file: its path, size and
// modification time, as fstat gives them for the opened file.
type artKey struct {
	path    string
	size    int64
	modTime int64 // UnixNano
}

// artETags remembers the content ETag (SHA-256) of the art files already
// hashed, so that a revalidation (If-None-Match) or a second download
// neither reads the whole file to hash it again. Any change of the file
// (a new file renamed in place, a write) changes its size or its
// modification time, hence its key.
type artETags struct {
	mu sync.Mutex
	m  map[artKey]string
}

func newArtKey(path string, size int64, modTime time.Time) artKey {
	return artKey{path: path, size: size, modTime: modTime.UnixNano()}
}

func (c *artETags) get(k artKey) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	etag, ok := c.m[k]
	return etag, ok
}

func (c *artETags) put(k artKey, etag string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= maxArtETags {
		c.m = make(map[artKey]string)
	}
	c.m[k] = etag
}

func (c *artETags) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}
