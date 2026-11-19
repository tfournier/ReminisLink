package server

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
