package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

const (
	gba = "Game Boy Advance (GBA)"
	ps  = "Sony PlayStation (PS)"
)

var id = library.EncodeID

func TestSystems(t *testing.T) {
	f := newFixture(t)
	r := f.get("/api/v1/systems", "/systems", 200)
	var body struct{ Systems []library.System }
	r.JSON(t, &body)
	var folders []string
	for _, s := range body.Systems {
		folders = append(folders, s.Folder)
	}
	if fmt.Sprint(folders) != "[01) Game Boy (GB) Game Boy Advance (GBA) Game Boy Advance (MGBA) Sony PlayStation (PS)]" {
		t.Errorf("systems = %v", folders)
	}
}

func TestSystemGames(t *testing.T) {
	f := newFixture(t)
	r := f.get("/api/v1/systems/"+id(gba)+"/games", "/systems/{systemId}/games", 200)
	var listing library.Listing
	r.JSON(t, &listing)
	if len(listing.Folders) != 1 || len(listing.Games) != 4 {
		t.Fatalf("listing = %+v", listing)
	}
	// Descend with the folderId returned by the listing.
	r = f.get("/api/v1/systems/"+id(gba)+"/games?folderId="+listing.Folders[0].ID, "/systems/{systemId}/games", 200)
	var sub library.Listing
	r.JSON(t, &sub)
	if len(sub.Games) != 1 || sub.Games[0].FileName != "Starlight Courier Remix.gba" {
		t.Fatalf("sub listing = %+v", sub)
	}
	// Multi-disc games.
	r = f.get("/api/v1/systems/"+id(ps)+"/games", "/systems/{systemId}/games", 200)
	var psListing library.Listing
	r.JSON(t, &psListing)
	if len(psListing.Games) != 2 || psListing.Games[0].Kind != "folder-m3u" || psListing.Games[1].Kind != "m3u" {
		t.Fatalf("ps listing = %+v", psListing)
	}
}

func TestSystemGamesErrors(t *testing.T) {
	f := newFixture(t)
	const path = "/systems/{systemId}/games"
	for name, tc := range map[string]struct {
		target string
		status int
		code   string
	}{
		"invalid system id":        {"/api/v1/systems/R2FtZQ==/games", 400, CodeInvalidID},
		"traversal system id":      {"/api/v1/systems/" + id("..") + "/games", 400, CodeInvalidID},
		"unknown system":           {"/api/v1/systems/" + id("Missing (X)") + "/games", 404, CodeNotFound},
		"folder of another system": {"/api/v1/systems/" + id(gba) + "/games?folderId=" + id("Game Boy Advance (MGBA)/Hacks"), 400, CodeInvalidID},
		"empty folder id":          {"/api/v1/systems/" + id(gba) + "/games?folderId=", 400, CodeInvalidID},
		"invalid folder id":        {"/api/v1/systems/" + id(gba) + "/games?folderId=%2E%2E", 400, CodeInvalidID},
		"missing folder":           {"/api/v1/systems/" + id(gba) + "/games?folderId=" + id(gba+"/Nope"), 404, CodeNotFound},
		"duplicate folder id":      {"/api/v1/systems/" + id(gba) + "/games?folderId=" + id(gba+"/Hacks") + "&folderId=" + id(gba+"/Hacks"), 400, auth.CodeInvalidRequest},
	} {
		r := f.client.Do(t, http.MethodGet, tc.target, nil)
		f.check(http.MethodGet, path, r)
		if r.Status != tc.status || r.ErrorCode() != tc.code {
			t.Errorf("%s: %d %s, want %d %s", name, r.Status, r.Body, tc.status, tc.code)
		}
	}
}

func TestGame(t *testing.T) {
	f := newFixture(t)
	r := f.get("/api/v1/games/"+id(gba+"/Starlight Courier.gba"), "/games/{gameId}", 200)
	var d library.GameDetail
	r.JSON(t, &d)
	if d.Name != "Starlight Courier" || d.SystemID != id(gba) || len(d.Files) != 7 {
		t.Errorf("detail = %+v", d)
	}
	r = f.get("/api/v1/games/"+id("Game Boy Advance (GBA)/Café Rêverie (France).gba"), "/games/{gameId}", 200)
	r.JSON(t, &d)
	if d.FileName != "Café Rêverie (France).gba" || d.Files[1].Path != "Saves/GBA/café rêverie (france).gba.sav" {
		t.Errorf("accented game = %+v", d)
	}
	r = f.get("/api/v1/games/"+id(ps+"/Mossy Hollow"), "/games/{gameId}", 200)
	r.JSON(t, &d)
	if d.Kind != "folder-m3u" {
		t.Errorf("multi-disc = %+v", d)
	}

	for name, tc := range map[string]struct {
		target string
		status int
		code   string
	}{
		"invalid id":     {"/api/v1/games/not!base64", 400, CodeInvalidID},
		"absolute path":  {"/api/v1/games/" + id("/etc/passwd"), 400, CodeInvalidID},
		"traversal":      {"/api/v1/games/" + id(gba+"/../../x"), 400, CodeInvalidID},
		"missing game":   {"/api/v1/games/" + id(gba+"/Missing.gba"), 404, CodeNotFound},
		"folder as game": {"/api/v1/games/" + id(gba+"/Hacks"), 404, CodeNotFound},
		"ignored file":   {"/api/v1/games/" + id(gba+"/readme.txt"), 404, CodeNotFound},
		"other case":     {"/api/v1/games/" + id(gba+"/starlight courier.gba"), 404, CodeNotFound},
	} {
		r := f.client.Do(t, http.MethodGet, tc.target, nil)
		f.check(http.MethodGet, "/games/{gameId}", r)
		if r.Status != tc.status || r.ErrorCode() != tc.code {
			t.Errorf("%s: %d %s, want %d %s", name, r.Status, r.Body, tc.status, tc.code)
		}
	}
}

func TestGameArt(t *testing.T) {
	f := newFixture(t)
	const path = "/games/{gameId}/art"
	target := "/api/v1/games/" + id(gba+"/Starlight Courier.gba") + "/art"
	r := f.get(target, path, 200)
	etag := r.Header.Get("ETag")
	if r.Header.Get("Content-Type") != "image/png" || len(r.Body) == 0 || etag == "" || r.Header.Get("Cache-Control") != ArtCacheControl {
		t.Fatalf("art: %v %d bytes", r.Header, len(r.Body))
	}
	for _, inm := range []string{etag, "W/" + etag, `"other", ` + etag, "*"} {
		req := f.client.Request(t, http.MethodGet, target, nil)
		req.Header.Set("If-None-Match", inm)
		r = f.client.Send(t, req)
		f.check(http.MethodGet, path, r)
		if r.Status != 304 || len(r.Body) != 0 || r.Header.Get("ETag") != etag || r.Header.Get("Cache-Control") != ArtCacheControl {
			t.Errorf("If-None-Match %s: %d", inm, r.Status)
		}
	}
	req := f.client.Request(t, http.MethodGet, target, nil)
	req.Header.Set("If-None-Match", `"stale"`)
	if r = f.client.Send(t, req); r.Status != 200 {
		t.Errorf("stale ETag: %d", r.Status)
	}
	// The ETag follows the content.
	testutil.WriteFile(t, f.sd, "Roms/"+gba+"/.media/Starlight Courier.png", "new image")
	r = f.get(target, path, 200)
	if r.Header.Get("ETag") == etag || string(r.Body) != "new image" {
		t.Errorf("changed art: ETag %s, body %q", r.Header.Get("ETag"), r.Body)
	}

	r = f.get("/api/v1/games/"+id(gba+"/Lantern Keeper [b1].gba")+"/art", path, 404)
	if r.ErrorCode() != CodeNotFound {
		t.Errorf("no art: %s", r.Body)
	}
	f.get("/api/v1/games/!/art", path, 400)
}

// The content ETag is computed once per version of the file: a
// revalidation or a second download reuses it, and a new version (same
// size, new modification time) gets its own.
func TestGameArtETagIsCachedPerVersion(t *testing.T) {
	f := newFixture(t)
	const path = "/games/{gameId}/art"
	target := "/api/v1/games/" + id(gba+"/Starlight Courier.gba") + "/art"
	art := testutil.WriteFile(t, f.sd, "Roms/"+gba+"/.media/Starlight Courier.png", "version 1")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(art, old, old); err != nil {
		t.Fatal(err)
	}
	r := f.get(target, path, 200)
	etag := r.Header.Get("ETag")
	if string(r.Body) != "version 1" || !strings.HasPrefix(etag, `"sha256-`) || f.server.artETags.len() != 1 {
		t.Fatalf("first GET: %q %s, %d cached", r.Body, etag, f.server.artETags.len())
	}
	// Served from the cache: same ETag, same body, nothing added.
	r = f.get(target, path, 200)
	if string(r.Body) != "version 1" || r.Header.Get("ETag") != etag || r.Header.Get("Content-Length") != "9" {
		t.Errorf("cached GET: %q %v", r.Body, r.Header)
	}
	req := f.client.Request(t, http.MethodGet, target, nil)
	req.Header.Set("If-None-Match", etag)
	if r = f.client.Send(t, req); r.Status != 304 || r.Header.Get("ETag") != etag {
		t.Errorf("cached revalidation: %d %s", r.Status, r.Header.Get("ETag"))
	}
	if f.server.artETags.len() != 1 {
		t.Errorf("%d cached", f.server.artETags.len())
	}
	// Same size, other content, written later: a new ETag.
	testutil.WriteFile(t, f.sd, "Roms/"+gba+"/.media/Starlight Courier.png", "version 2")
	req = f.client.Request(t, http.MethodGet, target, nil)
	req.Header.Set("If-None-Match", etag)
	r = f.client.Send(t, req)
	f.check(http.MethodGet, path, r)
	if r.Status != 200 || string(r.Body) != "version 2" || r.Header.Get("ETag") == etag {
		t.Errorf("new version: %d %q %s", r.Status, r.Body, r.Header.Get("ETag"))
	}
}

func TestArtETagsAreBounded(t *testing.T) {
	var c artETags
	for i := 0; i < maxArtETags+10; i++ {
		c.put(newArtKey("a", int64(i), time.Time{}), "e", 0, 0)
	}
	if n := c.len(); n == 0 || n > maxArtETags {
		t.Fatalf("%d entries", n)
	}
	if _, ok := c.get(newArtKey("a", maxArtETags+9, time.Time{}), 0); !ok {
		t.Fatal("last entry missing")
	}
}

// The cache follows the write lock generation: emptied when a write
// starts, never read nor filled while one runs, never filled with an ETag
// computed across a write, and left alone by a request that read an
// older generation.
func TestArtETagsFollowTheWriteGeneration(t *testing.T) {
	var c artETags
	k := newArtKey("a", 1, time.Time{})
	c.put(k, "e", 2, 2)
	if _, ok := c.get(k, 2); !ok {
		t.Fatal("not cached")
	}
	if _, ok := c.get(k, 3); ok || c.len() != 0 {
		t.Fatal("read while a write runs")
	}
	c.put(k, "e", 3, 3)
	c.put(k, "e", 2, 4)
	if c.len() != 0 {
		t.Fatal("filled during or across a write")
	}
	c.put(k, "e", 4, 4)
	if _, ok := c.get(k, 2); ok || c.len() != 1 {
		t.Fatal("an older generation read or emptied the cache")
	}
	if _, ok := c.get(k, 6); ok || c.len() != 0 {
		t.Fatal("kept after a write")
	}
}

// A pak-side change of the art gets a new ETag even when the new file has
// the same size and modification time as the old one (the cache is
// emptied by the write): through PUT /art, and through any write holding
// the lock.
func TestGameArtReplacedWithTheSameSizeAndDateGetsANewETag(t *testing.T) {
	f := newFixture(t)
	const path = "/games/{gameId}/art"
	target := "/api/v1/games/" + id(gba+"/Starlight Courier.gba") + "/art"
	date := time.Now().Add(-time.Hour).Truncate(time.Second)
	img := pngOf(t, 4, 4)
	old := bytes.Clone(img)
	old[len(old)-1] ^= 0xFF // same size, other bytes
	art := testutil.WriteFile(t, f.sd, "Roms/"+gba+"/.media/Starlight Courier.png", string(old))
	if err := os.Chtimes(art, date, date); err != nil {
		t.Fatal(err)
	}
	etag := f.get(target, path, 200).Header.Get("ETag")
	revalidate := func(etag string) testutil.Response {
		t.Helper()
		req := f.client.Request(t, http.MethodGet, target, nil)
		req.Header.Set("If-None-Match", etag)
		r := f.client.Send(t, req)
		f.check(http.MethodGet, path, r)
		return r
	}
	if r := revalidate(etag); r.Status != 304 || f.server.artETags.len() != 1 {
		t.Fatalf("revalidation: %d, %d cached", r.Status, f.server.artETags.len())
	}

	// PUT /art, then the new file gets the old size and date.
	f.send(http.MethodPut, target, path, img, map[string]string{"Content-Type": "image/png"}, 200)
	if err := os.Chtimes(art, date, date); err != nil {
		t.Fatal(err)
	}
	r := revalidate(etag)
	if r.Status != 200 || !bytes.Equal(r.Body, img) || r.Header.Get("ETag") == etag {
		t.Fatalf("after PUT: %d, ETag %s", r.Status, r.Header.Get("ETag"))
	}
	etag = r.Header.Get("ETag")

	// Any write of the pak (it holds the lock): same size, same date.
	if err := f.server.writes().Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(art, old, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(art, date, date); err != nil {
		t.Fatal(err)
	}
	// While the write runs, the cache is not used.
	if r := revalidate(etag); r.Status != 200 || !bytes.Equal(r.Body, old) {
		t.Errorf("during the write: %d", r.Status)
	}
	f.server.writes().Release()
	if r := revalidate(etag); r.Status != 200 || !bytes.Equal(r.Body, old) || r.Header.Get("ETag") == etag {
		t.Errorf("after the write: %d, ETag %s", r.Status, r.Header.Get("ETag"))
	}
}

// A pak write that replaces the art (same size, same date) between the
// read of the write lock generation and the opening of the art: the
// request opens the new file, so it must not take the old version's ETag
// from the cache (the cache was not touched since: no request ran).
func TestGameArtReplacedBetweenTheGenerationAndTheOpen(t *testing.T) {
	f := newFixture(t)
	const path = "/games/{gameId}/art"
	target := "/api/v1/games/" + id(gba+"/Starlight Courier.gba") + "/art"
	date := time.Now().Add(-time.Hour).Truncate(time.Second)
	art := testutil.WriteFile(t, f.sd, "Roms/"+gba+"/.media/Starlight Courier.png", "version A")
	if err := os.Chtimes(art, date, date); err != nil {
		t.Fatal(err)
	}
	etag := f.get(target, path, 200).Header.Get("ETag")
	if f.server.artETags.len() != 1 {
		t.Fatal("not cached")
	}
	var once sync.Once
	replace := func() {
		once.Do(func() {
			if err := f.server.writes().Acquire(context.Background()); err != nil {
				t.Error(err)
				return
			}
			defer f.server.writes().Release()
			if err := os.WriteFile(art, []byte("version B"), 0o644); err != nil {
				t.Error(err)
			}
			if err := os.Chtimes(art, date, date); err != nil {
				t.Error(err)
			}
		})
	}
	f.server.artLookupHook.Store(&replace)
	defer f.server.artLookupHook.Store(nil)
	req := f.client.Request(t, http.MethodGet, target, nil)
	req.Header.Set("If-None-Match", etag)
	r := f.client.Send(t, req)
	f.check(http.MethodGet, path, r)
	if r.Status != 200 || string(r.Body) != "version B" || r.Header.Get("ETag") == etag {
		t.Fatalf("replaced before the open: %d %q, ETag %s (old %s)", r.Status, r.Body, r.Header.Get("ETag"), etag)
	}
	sum := sha256.Sum256([]byte("version B"))
	if want := `"sha256-` + hex.EncodeToString(sum[:16]) + `"`; r.Header.Get("ETag") != want {
		t.Errorf("ETag %s, want %s", r.Header.Get("ETag"), want)
	}
}

// The card cannot be read: GET /art answers 500, never 404 (the app keeps
// its copy on an error, drops it on a 404).
func TestGameArtIOErrorIsNotNotFound(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	f := newFixture(t)
	const path = "/games/{gameId}/art"
	target := "/api/v1/games/" + id(gba+"/Starlight Courier.gba") + "/art"
	media := filepath.Join(f.sd, "Roms", gba, ".media")
	art := filepath.Join(media, "Starlight Courier.png")
	if err := os.Chmod(art, 0); err != nil {
		t.Fatal(err)
	}
	r := f.get(target, path, 500)
	if r.ErrorCode() != auth.CodeInternal {
		t.Errorf("unreadable art: %s", r.Body)
	}
	if err := os.Chmod(art, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(media, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(media, 0o755) })
	if r := f.get(target, path, 500); r.ErrorCode() != auth.CodeInternal {
		t.Errorf("unreadable .media: %s", r.Body)
	}
}

// Many concurrent GETs and revalidations while the art is replaced
// (run with -race): every 200 carries the ETag of its own body, and a 304
// answers only the ETag of the content current during the request.
func TestGameArtConcurrentRequestsAndReplacements(t *testing.T) {
	f := newFixture(t)
	const path = "/games/{gameId}/art"
	target := "/api/v1/games/" + id(gba+"/Starlight Courier.gba") + "/art"
	etagOf := func(b []byte) string {
		sum := sha256.Sum256(b)
		return `"sha256-` + hex.EncodeToString(sum[:16]) + `"`
	}
	const puts = 20
	// Distinct versions: version i is written by PUT i (version 0 is
	// there first). It can be the content from the start of PUT i to the
	// end of PUT i+1.
	versions := make([][]byte, puts+1)
	for i := range versions {
		versions[i] = pngOf(t, 4+i, 4)
	}
	art := testutil.WriteFile(t, f.sd, "Roms/"+gba+"/.media/Starlight Courier.png", "")
	if err := os.WriteFile(art, versions[0], 0o644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	starts := make([]time.Time, puts+2) // starts[i], ends[i]: PUT i
	ends := make([]time.Time, puts+2)
	far := time.Now().Add(time.Hour)
	for i := 1; i < len(ends); i++ {
		ends[i] = far // not yet ended
	}
	current := func(etag string, from, to time.Time) bool {
		mu.Lock()
		defer mu.Unlock()
		for i, v := range versions {
			if etagOf(v) != etag {
				continue
			}
			// Version i is possible from starts[i] to ends[i+1].
			return !starts[i].After(to) && !ends[i+1].Before(from) && (i == 0 || !starts[i].IsZero())
		}
		return false
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop)
		for i := 1; i <= puts; i++ {
			mu.Lock()
			starts[i] = time.Now()
			mu.Unlock()
			for {
				req := f.client.Request(t, http.MethodPut, target, versions[i])
				req.Header.Set("Content-Type", "image/png")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Error(err)
					return
				}
				resp.Body.Close()
				if resp.StatusCode == 429 {
					continue // device busy: sent again
				}
				if resp.StatusCode != 200 {
					t.Errorf("PUT: %d", resp.StatusCode)
					return
				}
				break
			}
			mu.Lock()
			ends[i] = time.Now()
			mu.Unlock()
		}
	}()
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			etag := ""
			for {
				select {
				case <-stop:
					return
				default:
				}
				req := f.client.Request(t, http.MethodGet, target, nil)
				if etag != "" {
					req.Header.Set("If-None-Match", etag)
				}
				from := time.Now()
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Error(err)
					return
				}
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				to := time.Now()
				if err != nil {
					t.Error(err)
					return
				}
				got := resp.Header.Get("ETag")
				switch resp.StatusCode {
				case 200:
					if got != etagOf(body) {
						t.Errorf("200 with ETag %s for a body of ETag %s", got, etagOf(body))
						return
					}
					etag = got
				case 304:
					if got != etag || !current(got, from, to) {
						t.Errorf("304 for ETag %s, not the content during the request", got)
						return
					}
				case 404:
					etag = "" // between the trash move and the new file
				default:
					t.Errorf("GET: %d", resp.StatusCode)
					return
				}
			}
		}()
	}
	wg.Wait()
	r := f.get(target, path, 200)
	if !bytes.Equal(r.Body, versions[puts]) || r.Header.Get("ETag") != etagOf(r.Body) {
		t.Errorf("final: ETag %s for %s", r.Header.Get("ETag"), etagOf(r.Body))
	}
}

func TestGameArtLargerThanTheHashLimit(t *testing.T) {
	f := newFixture(t)
	big := strings.Repeat("p", MaxArtBytes+10)
	testutil.WriteFile(t, f.sd, "Roms/"+gba+"/.media/Starlight Courier.png", big)
	target := "/api/v1/games/" + id(gba+"/Starlight Courier.gba") + "/art"
	r := f.get(target, "/games/{gameId}/art", 200)
	etag := r.Header.Get("ETag")
	if len(r.Body) != len(big) || !strings.HasPrefix(etag, `"mt-`) {
		t.Fatalf("large art: %d bytes, ETag %s", len(r.Body), etag)
	}
	req := f.client.Request(t, http.MethodGet, target, nil)
	req.Header.Set("If-None-Match", etag)
	if r = f.client.Send(t, req); r.Status != 304 {
		t.Fatalf("large art revalidation: %d", r.Status)
	}
}

func TestEtagMatches(t *testing.T) {
	for header, want := range map[string]bool{
		``: false, `"a"`: true, `W/"a"`: true, `"b", "a"`: true, `*`: true, `"b"`: false, `a`: false,
	} {
		if got := etagMatches(header, `"a"`); got != want {
			t.Errorf("etagMatches(%q) = %v", header, got)
		}
	}
}

// pngOf encodes a w x h image.
func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.NRGBA{R: 200, A: 255})
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSetAndDeleteArt(t *testing.T) {
	f := newFixture(t)
	var changes []Change
	f.server.opts.OnChange = func(c Change) { changes = append(changes, c) }
	const path = "/games/{gameId}/art"
	target := "/api/v1/games/" + id(gba+"/Lantern Keeper [b1].gba") + "/art"
	data := pngOf(t, 64, 48)

	// No art yet: written where NextUI reads it.
	r := f.send(http.MethodPut, target, path, data, map[string]string{"Content-Type": "image/png"}, 200)
	var g struct {
		ID     string `json:"id"`
		HasArt bool   `json:"hasArt"`
	}
	r.JSON(t, &g)
	if !g.HasArt || g.ID != id(gba+"/Lantern Keeper [b1].gba") {
		t.Fatalf("PUT art: %s", r.Body)
	}
	if f.read("Roms/"+gba+"/.media/Lantern Keeper [b1].png") != string(data) {
		t.Fatal("art not written in .media")
	}
	if len(changes) != 1 || changes[0] != (Change{Kind: ChangeArtSet, Name: "Lantern Keeper [b1]"}) {
		t.Fatalf("changes %+v", changes)
	}
	etag := f.get(target, path, 200).Header.Get("ETag")
	// Replaced: the old art goes to the trash first.
	data2 := pngOf(t, 1024, 1024)
	f.send(http.MethodPut, target, path, data2, map[string]string{"Content-Type": "image/png"}, 200)
	if f.read("Roms/"+gba+"/.media/Lantern Keeper [b1].png") != string(data2) {
		t.Fatal("art not replaced")
	}
	if got := f.get(target, path, 200); got.Header.Get("ETag") == etag || string(got.Body) != string(data2) {
		t.Fatal("GET /art: the ETag did not change with the new art")
	}
	entries, _, _ := f.trash.List()
	if len(entries) != 1 || entries[0].Reason != "overwrite" ||
		f.read(".userdata/shared/ReminisLink/trash/"+entries[0].ID+"/Roms/"+gba+"/.media/Lantern Keeper [b1].png") != string(data) {
		t.Fatalf("old art not in the trash: %+v", entries)
	}
	// Refused bodies: not a PNG, too large an image, truncated.
	for name, body := range map[string][]byte{
		"text":      []byte("not an image"),
		"empty":     {},
		"too wide":  pngOf(t, 1025, 10),
		"too tall":  pngOf(t, 10, 1025),
		"truncated": data[:len(data)-20],
	} {
		r := f.send(http.MethodPut, target, path, body, map[string]string{"Content-Type": "image/png"}, 422)
		if r.ErrorCode() != CodeUnsupportedFormat {
			t.Errorf("%s: %s", name, r.Body)
		}
	}
	// More than 4 MiB: 413 before anything is read.
	big := append(append([]byte{}, data...), bytes.Repeat([]byte{0}, 4<<20)...)
	if r := f.send(http.MethodPut, target, path, big, nil, 413); r.ErrorCode() != "too_large" {
		t.Errorf("large body: %s", r.Body)
	}
	f.send(http.MethodPut, "/api/v1/games/"+id(gba+"/Nope.gba")+"/art", path, data, nil, 404)

	// DELETE: to the trash, then 404.
	r = f.send(http.MethodDelete, target, path, nil, nil, 200)
	r.JSON(t, &g)
	if _, err := os.Stat(filepath.Join(f.sd, "Roms", gba, ".media", "Lantern Keeper [b1].png")); g.HasArt || err == nil {
		t.Fatalf("DELETE art: %s", r.Body)
	}
	if entries, _, _ := f.trash.List(); len(entries) != 2 {
		t.Fatalf("trash %+v", entries)
	}
	f.send(http.MethodDelete, target, path, nil, nil, 404)
	if changes[len(changes)-1].Kind != ChangeArtDeleted {
		t.Fatalf("changes %+v", changes)
	}
}

// The art of a game in a subfolder goes to the .media of that subfolder;
// the art of a multi-disc folder is named after the folder without its
// last extension (05 §3.1).
func TestSetArtLocations(t *testing.T) {
	f := newFixture(t)
	data := pngOf(t, 8, 8)
	testutil.WriteFile(t, f.sd, "Roms/Sony PlayStation (PS)/Sky Garden Quest Vol. 3/Sky Garden Quest Vol. 3.m3u", "a.cue\n")
	testutil.WriteFile(t, f.sd, "Roms/Sony PlayStation (PS)/Sky Garden Quest Vol. 3/a.cue", "x")
	for game, want := range map[string]string{
		"Sony PlayStation (PS)/Sky Garden Quest Vol. 3": "Roms/Sony PlayStation (PS)/.media/Sky Garden Quest Vol.png",
		gba + "/Hacks/Starlight Courier Remix.gba":      "Roms/" + gba + "/Hacks/.media/Starlight Courier Remix.png",
	} {
		r := f.send(http.MethodPut, "/api/v1/games/"+id(game)+"/art", "/games/{gameId}/art", data, nil, 200)
		if !strings.Contains(string(r.Body), `"hasArt":true`) || f.read(want) != string(data) {
			t.Errorf("%s: %s", game, r.Body)
		}
	}
}

// Codex review of M8: a .media folder linked outside Roms/ (desktop mode,
// a file system with links) is never written nor emptied through.
func TestArtRefusesALinkedMediaFolder(t *testing.T) {
	f := newFixture(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "Pixel Garden 🌱.png"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(f.sd, "Roms", gba, "Hacks", ".media")
	os.RemoveAll(media)
	if err := os.Symlink(outside, media); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	target := "/api/v1/games/" + id(gba+"/Hacks/Starlight Courier Remix.gba") + "/art"
	r := f.send(http.MethodPut, target, "/games/{gameId}/art", pngOf(t, 4, 4), nil, 400)
	wantCode(t, r, CodeInvalidID)
	if des, _ := os.ReadDir(outside); len(des) != 1 {
		t.Fatalf("written outside Roms/: %v", des)
	}
	// An art reached through the link is not moved away either.
	if err := os.WriteFile(filepath.Join(outside, "Starlight Courier Remix.png"), pngOf(t, 4, 4), 0o644); err != nil {
		t.Fatal(err)
	}
	f.send(http.MethodDelete, target, "/games/{gameId}/art", nil, nil, 400)
	if _, err := os.Stat(filepath.Join(outside, "Starlight Courier Remix.png")); err != nil {
		t.Fatal("the file outside Roms/ was moved")
	}
}

func TestDeletionPreview(t *testing.T) {
	f := newFixture(t)
	const path = "/games/{gameId}/deletion"
	target := "/api/v1/games/" + id(gba+"/Starlight Courier.gba") + "/deletion"
	r := f.get(target, path, 200)
	var p library.DeletionPreview
	r.JSON(t, &p)
	if len(p.Moved) == 0 || len(p.Kept) != 0 || p.MovedBytes == 0 || p.Moved[0].Name != "Starlight Courier.gba" || p.Moved[0].Role != "rom" {
		t.Fatalf("preview %s", r.Body)
	}
	r = f.get(target+"?keepSave=true", path, 200)
	r.JSON(t, &p)
	if len(p.Kept) == 0 {
		t.Fatalf("keepSave preview %s", r.Body)
	}
	f.get(target+"?keepSave=maybe", path, 400)
	f.get("/api/v1/games/"+id(gba+"/Nope.gba")+"/deletion", path, 404)
	// Nothing moved by a preview.
	if entries, _, _ := f.trash.List(); len(entries) != 0 {
		t.Fatalf("trash %+v", entries)
	}
}
