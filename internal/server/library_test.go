package server

import (
	"fmt"
	"net/http"
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
	if len(psListing.Games) != 4 || psListing.Games[0].Kind != "folder-m3u" || psListing.Games[3].Kind != "m3u" {
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
		if r.Status != 304 || len(r.Body) != 0 || r.Header.Get("ETag") != etag {
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

func TestEtagMatches(t *testing.T) {
	for header, want := range map[string]bool{
		``: false, `"a"`: true, `W/"a"`: true, `"b", "a"`: true, `*`: true, `"b"`: false, `a`: false,
	} {
		if got := etagMatches(header, `"a"`); got != want {
			t.Errorf("etagMatches(%q) = %v", header, got)
		}
	}
}
