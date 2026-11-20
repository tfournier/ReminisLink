package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

const gameFilePath = "/games/{gameId}/files/{fileId}"

// detail reads GET /games/{gameId} of the game at rel (relative to Roms/).
func (f *fixture) detail(rel string) library.GameDetail {
	f.t.Helper()
	var d library.GameDetail
	f.get(gamePath(rel), "/games/{gameId}", http.StatusOK).JSON(f.t, &d)
	return d
}

// fileOf returns the entry of d with this role (and slot for a state).
func fileOf(t *testing.T, d library.GameDetail, role string, slot int) library.GameFile {
	t.Helper()
	for _, file := range d.Files {
		if file.Role == role && (file.Slot == nil || *file.Slot == slot) {
			return file
		}
	}
	t.Fatalf("no %s file in %+v", role, d.Files)
	return library.GameFile{}
}

// download fetches a file of a game and checks the response against the
// contract and the status.
func (f *fixture) download(gameRel, fileID string, want int) testutil.Response {
	f.t.Helper()
	return f.get(gamePath(gameRel)+"/files/"+fileID, gameFilePath, want)
}

// checkDownload downloads every file of the game at rel and compares it
// with the file on the SD card; it returns the roles seen.
func (f *fixture) checkDownload(rel string) map[string]int {
	f.t.Helper()
	roles := map[string]int{}
	d := f.detail(rel)
	for _, file := range d.Files {
		if file.FileID != library.EncodeID(file.Path) {
			f.t.Errorf("%s: fileId %q is not the id of its path", file.Path, file.FileID)
		}
		r := f.download(rel, file.FileID, http.StatusOK)
		want := f.read(file.Path)
		if string(r.Body) != want {
			f.t.Errorf("%s: body %q, want %q", file.Path, r.Body, want)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/octet-stream" {
			f.t.Errorf("%s: Content-Type %q", file.Path, ct)
		}
		if cl := r.Header.Get("Content-Length"); cl != strconv.Itoa(len(want)) {
			f.t.Errorf("%s: Content-Length %q, want %d", file.Path, cl, len(want))
		}
		if cd := r.Header.Get("Content-Disposition"); cd != contentDisposition(path.Base(file.Path)) || !strings.HasPrefix(cd, "attachment; filename=") {
			f.t.Errorf("%s: Content-Disposition %q", file.Path, cd)
		}
		for _, h := range []string{"Accept-Ranges", "Content-Range", "Last-Modified", "ETag"} {
			if v := r.Header.Get(h); v != "" {
				f.t.Errorf("%s: unexpected %s %q", file.Path, h, v)
			}
		}
		roles[file.Role]++
	}
	return roles
}

// Every file of GET /games/{gameId}, whatever its role, downloads by its
// fileId with the content of the SD card and the headers of the contract.
func TestGameFileDownloadEachRole(t *testing.T) {
	f := newFixture(t)
	testutil.WriteFile(t, f.sd, "Saves/GBA/Starlight Courier.gba.rtc", "clock")
	roles := f.checkDownload(scPath)
	for role, n := range map[string]int{"rom": 1, "save": 1, "rtc": 1, "state": 3, "cheats": 1, "art": 1} {
		if roles[role] != n {
			t.Errorf("Starlight Courier: %d %s files, want %d (%v)", roles[role], role, n, roles)
		}
	}
	// Discs of a folder-m3u game (the .m3u, the .cue and .bin files).
	if roles := f.checkDownload(ps + "/Mossy Hollow"); roles["rom"] != 5 || roles["save"] != 1 || roles["state"] != 1 || roles["art"] != 1 {
		t.Errorf("Mossy Hollow: %v", roles)
	}
	// Discs of an .m3u file game.
	if roles := f.checkDownload(ps + "/Quiet Harbor.m3u"); roles["rom"] != 3 {
		t.Errorf("Quiet Harbor: %v", roles)
	}
	// A folder-cue game.
	testutil.WriteFile(t, f.sd, "Roms/"+ps+"/Tide Pool/Tide Pool.cue", `FILE "Tide Pool.bin" BINARY`)
	testutil.WriteFile(t, f.sd, "Roms/"+ps+"/Tide Pool/Tide Pool.bin", "disc data")
	if roles := f.checkDownload(ps + "/Tide Pool"); roles["rom"] != 2 {
		t.Errorf("Tide Pool: %v", roles)
	}
	// A game in a subfolder, with a non-ASCII name.
	f.checkDownload(gba + "/Hacks/Starlight Courier Remix.gba")
	f.checkDownload(gba + "/Café Rêverie (France).gba")

	// The fileId stays the same while the file keeps its path.
	a, b := f.detail(scPath), f.detail(scPath)
	testutil.WriteFile(t, f.sd, "Saves/GBA/Starlight Courier.gba.sav", "changed content")
	c := f.detail(scPath)
	for i := range a.Files {
		if a.Files[i].FileID != b.Files[i].FileID || a.Files[i].FileID != c.Files[i].FileID {
			t.Errorf("fileId changed: %q %q %q", a.Files[i].FileID, b.Files[i].FileID, c.Files[i].FileID)
		}
	}
	save := fileOf(t, c, "save", 0)
	if r := f.download(scPath, save.FileID, http.StatusOK); string(r.Body) != "changed content" {
		t.Errorf("save after change: %q", r.Body)
	}
}

// A large file is streamed whole with its Content-Length.
func TestGameFileDownloadLarge(t *testing.T) {
	f := newFixture(t)
	big := bytes.Repeat([]byte("0123456789abcdef"), 1<<20) // 16 MiB
	rel := ps + "/Big Disc.bin"
	if err := os.WriteFile(filepath.Join(f.sd, "Roms", ps, "Big Disc.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	d := f.detail(rel)
	r := f.download(rel, d.Files[0].FileID, http.StatusOK)
	if !bytes.Equal(r.Body, big) || r.Header.Get("Content-Length") != strconv.Itoa(len(big)) {
		t.Fatalf("large file: %d bytes, Content-Length %q", len(r.Body), r.Header.Get("Content-Length"))
	}
}

// Only a file of the game's current list is served: anything else is 404
// not_found, even a well-formed id of an existing file.
func TestGameFileNotInList(t *testing.T) {
	f := newFixture(t)
	sc := f.detail(scPath)
	mossy := f.detail(ps + "/Mossy Hollow")
	save := fileOf(t, sc, "save", 0)
	for name, fileID := range map[string]string{
		"unknown":                   library.EncodeID("nope"),
		"file of another game":      fileOf(t, mossy, "save", 0).FileID,
		"save of a game in Hacks":   library.EncodeID("Saves/GBA/Starlight Courier Remix.gba.sav"),
		"save of another system":    library.EncodeID("Saves/MGBA/Starlight Courier.gba.sav"),
		"userdata file":             library.EncodeID(".userdata/shared/minuisettings.txt"),
		"state thumbnail":           library.EncodeID(".userdata/shared/.minui/GBA/Starlight Courier.gba.0.bmp"),
		"pairing data":              library.EncodeID(".userdata/shared/ReminisLink/clients.json"),
		"readme of the system":      library.EncodeID("Roms/" + gba + "/readme.txt"),
		"traversal":                 library.EncodeID("Saves/GBA/../../.userdata/shared/minuisettings.txt"),
		"absolute":                  library.EncodeID(filepath.Join(f.sd, "Saves/GBA/Starlight Courier.gba.sav")),
		"path relative to Roms":     library.EncodeID(scPath),
		"id with a different case":  library.EncodeID(strings.ToLower(save.Path)),
		"padded id of the save":     save.FileID + "=",
		"standard base64 of a file": strings.NewReplacer("-", "+", "_", "/").Replace(save.FileID) + "x",
	} {
		r := f.client.Do(t, http.MethodGet, gamePath(scPath)+"/files/"+fileID, nil)
		f.check(http.MethodGet, gameFilePath, r)
		switch {
		case strings.Contains(fileID, "=") || strings.Contains(fileID, "+") || strings.Contains(fileID, "/"):
			// Not an opaque id: 400 invalid_id (or no route for a "/").
			if r.Status != http.StatusBadRequest && r.Status != http.StatusNotFound {
				t.Errorf("%s: status %d", name, r.Status)
			}
		case r.Status != http.StatusNotFound || r.ErrorCode() != CodeNotFound:
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	wantCode(t, f.get(gamePath(scPath)+"/files/a.b", gameFilePath, http.StatusBadRequest), CodeInvalidID)
	// Unknown game, invalid game id.
	wantCode(t, f.get(gamePath(gba+"/Missing.gba")+"/files/"+save.FileID, gameFilePath, http.StatusNotFound), CodeNotFound)
	wantCode(t, f.get("/api/v1/games/"+library.EncodeID("..")+"/files/"+save.FileID, gameFilePath, http.StatusBadRequest), CodeInvalidID)

	// A file deleted since the listing.
	if err := os.Remove(filepath.Join(f.sd, filepath.FromSlash(save.Path))); err != nil {
		t.Fatal(err)
	}
	r := f.download(scPath, save.FileID, http.StatusNotFound)
	if wantCode(t, r, CodeNotFound); !strings.Contains(string(r.Body), "File not found") {
		t.Errorf("message: %s", r.Body)
	}
	// A state removed since the listing.
	st := fileOf(t, sc, "state", 1)
	if err := os.Remove(filepath.Join(f.sd, filepath.FromSlash(st.Path))); err != nil {
		t.Fatal(err)
	}
	wantCode(t, f.download(scPath, st.FileID, http.StatusNotFound), CodeNotFound)
}

// Links are never followed: a file replaced by a link, or reached through
// a linked folder, is 404 even when it is listed.
func TestGameFileLinks(t *testing.T) {
	f := newFixture(t)
	outside := t.TempDir()
	testutil.WriteFile(t, outside, "secret.sav", "secret")
	testutil.WriteFile(t, outside, "GBA/Starlight Courier.gba.cht", "secret cheats")
	sc := f.detail(scPath)
	save := fileOf(t, sc, "save", 0)
	savePath := filepath.Join(f.sd, filepath.FromSlash(save.Path))
	if err := os.Remove(savePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.sav"), savePath); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	// The save replaced by a link after the listing.
	wantCode(t, f.download(scPath, save.FileID, http.StatusNotFound), CodeNotFound)

	// Cheats/GBA is a link to a folder outside the card: the file is
	// listed (the adapter reads through the folder) but never served.
	cheatsDir := filepath.Join(f.sd, "Cheats", "GBA")
	if err := os.RemoveAll(cheatsDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "GBA"), cheatsDir); err != nil {
		t.Fatal(err)
	}
	// .media of the system is a link to a folder inside the card.
	media := filepath.Join(f.sd, "Roms", gba, ".media")
	if err := os.Rename(media, filepath.Join(f.sd, "Roms", gba, "media-real")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("media-real", media); err != nil {
		t.Fatal(err)
	}
	d := f.detail(scPath)
	for _, role := range []string{"cheats", "art"} {
		file := fileOf(t, d, role, 0)
		wantCode(t, f.download(scPath, file.FileID, http.StatusNotFound), CodeNotFound)
	}
	if !strings.Contains(f.logs.String(), "game file refused") {
		t.Error("refusal not logged")
	}
	if strings.Contains(f.logs.String(), outside) {
		t.Error("absolute path in the logs")
	}
	// The other files of the game are still served.
	rom := fileOf(t, d, "rom", 0)
	f.download(scPath, rom.FileID, http.StatusOK)
}

// Another client lists and downloads while a write holds the write lock
// (M13): reads never wait.
func TestGameFilesDuringAWrite(t *testing.T) {
	f := newFixture(t)
	b := f.pairNamed("iPhone B")
	if !f.server.writes().TryAcquire() {
		t.Fatal("write lock held")
	}
	defer f.server.writes().Release()
	var d library.GameDetail
	f.as(b, "GET", gamePath(scPath), "/games/{gameId}", nil, http.StatusOK).JSON(t, &d)
	r := f.as(b, "GET", gamePath(scPath)+"/files/"+fileOf(t, d, "cheats", 0).FileID, gameFilePath, nil, http.StatusOK)
	if string(r.Body) != f.read("Cheats/GBA/Starlight Courier.gba.cht") {
		t.Errorf("body %q", r.Body)
	}
	f.as(b, "GET", "/api/v1/games", "/games", nil, http.StatusOK)
	for _, rt := range f.server.routeTable {
		if (rt.pattern == "GET "+Prefix+"/games" || rt.pattern == "GET "+Prefix+gameFilePath) && rt.write != writeNone {
			t.Errorf("%s takes the write lock", rt.pattern)
		}
	}
}

type allGamesJSON struct {
	Games []struct {
		library.Game
		SystemID   string  `json:"systemId"`
		FolderID   *string `json:"folderId"`
		FolderPath *string `json:"folderPath"`
	} `json:"games"`
	Truncated   bool      `json:"truncated"`
	GeneratedAt time.Time `json:"generatedAt"`
}

// listAll walks GET /systems/{systemId}/games of every system, every
// folder, and returns the games by id with the folderId given to the
// folder that holds them ("" at the root).
func (f *fixture) listAll() (map[string]library.Game, map[string]string, map[string]string) {
	f.t.Helper()
	var systems struct{ Systems []library.System }
	f.get("/api/v1/systems", "/systems", http.StatusOK).JSON(f.t, &systems)
	games, folderOf, systemOf := map[string]library.Game{}, map[string]string{}, map[string]string{}
	for _, sys := range systems.Systems {
		var walk func(folderID string)
		walk = func(folderID string) {
			target := "/api/v1/systems/" + sys.ID + "/games"
			if folderID != "" {
				target += "?folderId=" + folderID
			}
			var l library.Listing
			f.get(target, "/systems/{systemId}/games", http.StatusOK).JSON(f.t, &l)
			for _, g := range l.Games {
				games[g.ID], folderOf[g.ID], systemOf[g.ID] = g, folderID, sys.ID
			}
			for _, sub := range l.Folders {
				walk(sub.ID)
			}
		}
		walk("")
	}
	return games, folderOf, systemOf
}

func TestAllGames(t *testing.T) {
	f := newFixture(t)
	// Locked state and a deeper folder.
	f.do(http.MethodPut, gamePath(scPath)+"/states/1/lock", "/games/{gameId}/states/{slot}/lock", nil, http.StatusOK)
	testutil.WriteFile(t, f.sd, "Roms/"+gba+"/Hacks/Deeper/Deepest/Bottom.gba", "r")
	var all allGamesJSON
	f.get("/api/v1/games", "/games", http.StatusOK).JSON(t, &all)
	if all.Truncated || !all.GeneratedAt.Equal(timestamp(f.clock.Now())) {
		t.Errorf("truncated %v generatedAt %v", all.Truncated, all.GeneratedAt)
	}
	games, folderOf, systemOf := f.listAll()
	if len(all.Games) != len(games) {
		t.Errorf("%d games, the listings give %d", len(all.Games), len(games))
	}
	var keys []string
	systems := map[string]bool{}
	for _, g := range all.Games {
		want, ok := games[g.ID]
		if !ok {
			t.Errorf("%s: not in the listings", g.FileName)
			continue
		}
		if !reflect.DeepEqual(g.Game, want) {
			t.Errorf("%s: %+v, listing %+v", g.FileName, g.Game, want)
		}
		if g.SystemID != systemOf[g.ID] {
			t.Errorf("%s: systemId %q, want %q", g.FileName, g.SystemID, systemOf[g.ID])
		}
		rel, _ := library.DecodeID(g.ID)
		sysFolder, _ := library.DecodeSystemID(g.SystemID)
		systems[sysFolder] = true
		dir := path.Dir(rel)
		switch {
		case folderOf[g.ID] == "":
			if g.FolderID != nil || g.FolderPath != nil {
				t.Errorf("%s: folder %v %v at the root", g.FileName, g.FolderID, g.FolderPath)
			}
		case g.FolderID == nil || *g.FolderID != folderOf[g.ID] || g.FolderPath == nil || sysFolder+"/"+*g.FolderPath != dir:
			t.Errorf("%s: folderId %v folderPath %v, want %q in %q", g.FileName, g.FolderID, g.FolderPath, folderOf[g.ID], dir)
		}
		keys = append(keys, strings.ToLower(sysFolder)+"\x00"+strings.ToLower(rel))
	}
	if !slices.IsSorted(keys) {
		t.Errorf("not sorted by system folder then path: %q", keys)
	}
	// Systems with the same label (GBA, MGBA) and without emulator (PS).
	for _, s := range []string{gba, "Game Boy Advance (MGBA)", ps, "01) Game Boy (GB)"} {
		if !systems[s] {
			t.Errorf("no game of %s", s)
		}
	}
	find := func(fileName string) int {
		for i, g := range all.Games {
			if g.FileName == fileName {
				return i
			}
		}
		t.Fatalf("%s not listed", fileName)
		return -1
	}
	if g := all.Games[find("Bottom.gba")]; g.FolderPath == nil || *g.FolderPath != "Hacks/Deeper/Deepest" || *g.FolderID != library.EncodeID(gba+"/Hacks/Deeper/Deepest") {
		t.Errorf("deep game: %+v", g)
	}
	var sc library.Game
	for _, g := range all.Games {
		if g.ID == library.EncodeID(scPath) {
			sc = g.Game
		}
	}
	if sc.LockedStateCount != 1 || sc.StateCount != 3 || !sc.HasSave || sc.CheatCount == 0 || !sc.HasArt {
		t.Errorf("Starlight Courier: %+v", sc)
	}

	// Truncation: the first games in the same order.
	for _, limit := range []int{1, 3, len(all.Games) - 1, len(all.Games)} {
		f.server.allGamesLimit = limit
		var cut allGamesJSON
		f.get("/api/v1/games", "/games", http.StatusOK).JSON(t, &cut)
		if len(cut.Games) != limit || cut.Truncated != (limit < len(all.Games)) {
			t.Errorf("limit %d: %d games, truncated %v", limit, len(cut.Games), cut.Truncated)
			continue
		}
		for i := range cut.Games {
			if cut.Games[i].ID != all.Games[i].ID {
				t.Errorf("limit %d: game %d is %s, want %s", limit, i, cut.Games[i].FileName, all.Games[i].FileName)
			}
		}
	}
}

// A client that stops reading a download is cut off after the send idle
// timeout; no write deadline ends a transfer that keeps going.
func TestGameFileStalledClient(t *testing.T) {
	f := newFixture(t)
	f.server.timeouts.sendIdle = 200 * time.Millisecond
	rel := ps + "/Huge Disc.bin"
	big := bytes.Repeat([]byte{0x5a}, 64<<20)
	if err := os.WriteFile(filepath.Join(f.sd, "Roms", ps, "Huge Disc.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	d := f.detail(rel)
	conn := dial(t, strings.TrimPrefix(f.http.URL, "http://"))
	defer conn.Close()
	if _, err := conn.Write(signedRaw(t, f, http.MethodGet, gamePath(rel)+"/files/"+d.Files[0].FileID, nil)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(f.logs.String(), "file transfer interrupted") {
		if time.Now().After(deadline) {
			t.Fatal("the stalled download was not cut off")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// chunkRecorder records the size of each write.
type chunkRecorder struct {
	*httptest.ResponseRecorder
	sizes []int
}

func (c *chunkRecorder) Write(p []byte) (int, error) {
	c.sizes = append(c.sizes, len(p))
	return c.ResponseRecorder.Write(p)
}

// idleWriter writes at most SendChunk bytes under each deadline, so a
// stalled client is one that accepts less than SendChunk bytes in the send
// idle timeout.
func TestIdleWriterChunks(t *testing.T) {
	rec := &chunkRecorder{ResponseRecorder: httptest.NewRecorder()}
	iw := &idleWriter{w: rec, ctl: http.NewResponseController(rec), timeout: time.Second}
	data := bytes.Repeat([]byte("x"), 3*SendChunk+5)
	if n, err := iw.Write(data); err != nil || n != len(data) {
		t.Fatalf("Write: %d %v", n, err)
	}
	if !slices.Equal(rec.sizes, []int{SendChunk, SendChunk, SendChunk, 5}) || rec.Body.Len() != len(data) {
		t.Errorf("writes %v, %d bytes", rec.sizes, rec.Body.Len())
	}
}

// deadlineWriter is a ResponseWriter that records its writes, flushes
// and write deadlines (through http.ResponseController), in order.
type deadlineWriter struct {
	t      *testing.T
	header http.Header
	body   bytes.Buffer
	delay  time.Duration // each write takes this long (a slow client)
	events []string
}

func (d *deadlineWriter) Header() http.Header { return d.header }
func (d *deadlineWriter) WriteHeader(int)     {}

func (d *deadlineWriter) Write(p []byte) (int, error) {
	if len(p) > SendChunk {
		d.t.Errorf("write of %d bytes", len(p))
	}
	d.events = append(d.events, "write")
	time.Sleep(d.delay)
	return d.body.Write(p)
}

func (d *deadlineWriter) SetWriteDeadline(t time.Time) error {
	switch {
	case t.IsZero():
		d.events = append(d.events, "clear")
	case !t.After(time.Now()):
		d.t.Errorf("deadline %v already past when set", t)
		d.events = append(d.events, "past")
	default:
		d.events = append(d.events, "deadline")
	}
	return nil
}

func (d *deadlineWriter) FlushError() error {
	d.events = append(d.events, "flush")
	return nil
}

// sendFile renews the write deadline for each piece of the body, so a
// slow client that keeps reading gets the whole file however long it
// takes, and keeps it for the final flush of what net/http buffered
// before clearing it.
func TestSendFileDeadlines(t *testing.T) {
	f := newFixture(t)
	f.server.timeouts.sendIdle = 100 * time.Millisecond
	data := bytes.Repeat([]byte("0123456789abcdef"), 10*SendChunk/16+7) // 10 pieces and 112 bytes
	p := filepath.Join(t.TempDir(), "disc.bin")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	w := &deadlineWriter{t: t, header: http.Header{}, delay: 30 * time.Millisecond}
	start := time.Now()
	f.server.sendFile(w, httptest.NewRequest(http.MethodGet, "/", nil), file, "disc.bin", "application/octet-stream")
	if elapsed := time.Since(start); elapsed < f.server.timeouts.sendIdle {
		t.Errorf("transfer took %v, want more than the idle timeout", elapsed)
	}
	if !bytes.Equal(w.body.Bytes(), data) {
		t.Errorf("body: %d bytes, want %d", w.body.Len(), len(data))
	}
	// A new deadline before every write, then before the flush, then the
	// clear: "deadline write" × n, "deadline flush clear".
	want := strings.Repeat("deadline write ", strings.Count(strings.Join(w.events, " "), "write")) + "deadline flush clear"
	if got := strings.Join(w.events, " "); got != want || !strings.Contains(got, strings.Repeat("deadline write ", 11)) {
		t.Errorf("events %q, want %q (at least 11 writes)", got, want)
	}
	if strings.Contains(f.logs.String(), "file transfer interrupted") {
		t.Error("transfer reported as interrupted")
	}
}

// GET /games answers 200 with the readable systems when one system folder
// cannot be read.
func TestAllGamesUnreadableSystem(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Join(f.sd, "Roms", ps)
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("permissions not enforced here")
	}
	var all allGamesJSON
	f.get("/api/v1/games", "/games", http.StatusOK).JSON(t, &all)
	systems := map[string]bool{}
	for _, g := range all.Games {
		systems[g.SystemID] = true
	}
	if systems[library.EncodeID(ps)] || !systems[library.EncodeID(gba)] {
		t.Errorf("systems listed: %v", systems)
	}
}
