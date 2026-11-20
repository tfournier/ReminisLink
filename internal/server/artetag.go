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
// does not read the whole file to hash it again.
//
// Size and modification time alone cannot tell two contents apart (a
// date kept by a copy, a clock set back, two writes in the same 10 ms of
// exFAT), so the cache also relies on who writes the card:
//
//   - while the pak runs, NextUI does not, and the pak is the only writer
//     of the card. A change made by something else meanwhile (a shell over
//     SSH, a card swapped under the pak) with the same size and date is not
//     seen until the pak restarts; this cache is in memory only.
//   - every write of the pak holds the write lock (M13, "Writes one at a
//     time"): PUT and DELETE art, game deletion, uploads, patches, jobs,
//     trash. The cache is tied to the lock's generation: it is emptied as
//     soon as a write starts, the handler only reads it when no write
//     started between the beginning of the request and the opening of the
//     file, and an ETag is kept only when no write ran while it was
//     computed. Hence a pak-side change of an art, even with the same size
//     and date, always gets a new SHA-256 ETag. (Arts over MaxArtBytes are
//     not hashed: their "mt-" ETag is the size and date, whoever writes.)
type artETags struct {
	mu  sync.Mutex
	gen uint64 // the write lock generation of m
	m   map[artKey]string
}

func newArtKey(path string, size int64, modTime time.Time) artKey {
	return artKey{path: path, size: size, modTime: modTime.UnixNano()}
}

// get returns the cached ETag of k at the write lock generation gen
// (never while a write is in progress: gen odd).
func (c *artETags) get(k artKey, gen uint64) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.syncLocked(gen) || gen%2 != 0 {
		return "", false
	}
	etag, ok := c.m[k]
	return etag, ok
}

// put caches the ETag of k computed between the generations before and
// after: only when no write ran meanwhile.
func (c *artETags) put(k artKey, etag string, before, after uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.syncLocked(after) || before != after || after%2 != 0 {
		return
	}
	if len(c.m) >= maxArtETags {
		clear(c.m)
	}
	if c.m == nil {
		c.m = make(map[artKey]string)
	}
	c.m[k] = etag
}

// syncLocked empties the cache when the write lock moved since it was
// filled (a write started or ended). It returns false for a generation
// older than the cache's (a request that read it before a later write):
// that request neither reads nor fills the cache.
func (c *artETags) syncLocked(gen uint64) bool {
	switch {
	case gen < c.gen:
		return false
	case gen > c.gen:
		clear(c.m)
		c.gen = gen
	}
	return true
}

func (c *artETags) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}
