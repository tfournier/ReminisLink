package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

const (
	scPath   = gbaFolder + "/Starlight Courier.gba"
	statesGB = ".userdata/shared/GBA-gpsp/"
)

func gamePath(rel string) string { return "/api/v1/games/" + library.EncodeID(rel) }

func (f *fixture) read(rel string) string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.sd, filepath.FromSlash(rel)))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(data)
}

// send sends a signed request with extra headers (not signed), validates
// the response and checks its status.
func (f *fixture) send(method, target, path string, body []byte, headers map[string]string, want int) testutil.Response {
	f.t.Helper()
	req := f.client.Request(f.t, method, target, body)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	r := f.client.Send(f.t, req)
	f.check(method, path, r)
	if r.Status != want {
		f.t.Fatalf("%s %s: status %d, want %d: %s", method, target, r.Status, want, r.Body)
	}
	return r
}

func wantCode(t *testing.T, r testutil.Response, code string) {
	t.Helper()
	if r.ErrorCode() != code {
		t.Fatalf("code %q, want %q: %s", r.ErrorCode(), code, r.Body)
	}
}

type backupJSON struct {
	ID     string  `json:"id"`
	Label  *string `json:"label"`
	Reason string  `json:"reason"`
}

func TestSaveRoutes(t *testing.T) {
	f := newFixture(t)
	g := gamePath(scPath)
	old := f.read("Saves/GBA/Starlight Courier.gba.sav")

	r := f.do(http.MethodGet, g+"/save", "/games/{gameId}/save", nil, http.StatusOK)
	if string(r.Body) != old || r.Header.Get("Content-Disposition") != `attachment; filename="Starlight Courier.gba.sav"` {
		t.Fatalf("download: %q %v", r.Body, r.Header)
	}
	// Non-ASCII name: ASCII fallback and RFC 6266 filename*.
	r = f.do(http.MethodGet, gamePath(gbaFolder+"/Café Rêverie (France).gba")+"/save", "/games/{gameId}/save", nil, http.StatusOK)
	if cd := r.Header.Get("Content-Disposition"); !strings.Contains(cd, `filename="caf_ r_verie (france).gba.sav"`) || !strings.Contains(cd, "filename*=utf-8''caf%C3%A9") {
		t.Fatalf("content disposition %q", cd)
	}
	lk := gamePath(gbaFolder + "/Lantern Keeper [b1].gba")
	wantCode(t, f.do(http.MethodGet, lk+"/save", "/games/{gameId}/save", nil, http.StatusNotFound), "not_found")

	// Replace: the previous save becomes a before-replace backup.
	r = f.do(http.MethodPut, g+"/save", "/games/{gameId}/save", []byte("new save"), http.StatusOK)
	var res struct {
		Backup *backupJSON `json:"backup"`
	}
	r.JSON(t, &res)
	if res.Backup == nil || res.Backup.Reason != "before-replace" || f.read("Saves/GBA/Starlight Courier.gba.sav") != "new save" {
		t.Fatalf("replace: %s", r.Body)
	}
	wantCode(t, f.do(http.MethodPut, g+"/save", "/games/{gameId}/save", nil, http.StatusBadRequest), "invalid_request")
	r = f.do(http.MethodPut, lk+"/save", "/games/{gameId}/save", []byte("first"), http.StatusOK)
	if strings.TrimSpace(string(r.Body)) != `{"backup":null}` {
		t.Fatalf("first save: %s", r.Body)
	}

	// Manual backups.
	r = f.do(http.MethodPost, g+"/save/backups", "/games/{gameId}/save/backups", []byte(`{"label":"Avant le boss final"}`), http.StatusCreated)
	var manual backupJSON
	r.JSON(t, &manual)
	if manual.Reason != "manual" || manual.Label == nil || *manual.Label != "Avant le boss final" {
		t.Fatalf("create: %s", r.Body)
	}
	f.do(http.MethodPost, g+"/save/backups", "/games/{gameId}/save/backups", nil, http.StatusCreated)
	wantCode(t, f.do(http.MethodPost, g+"/save/backups", "/games/{gameId}/save/backups", []byte(`{"label":"`+strings.Repeat("x", 81)+`"}`), http.StatusBadRequest), "invalid_request")
	wantCode(t, f.do(http.MethodPost, gamePath(gbaFolder+"/Hacks/Deeper/Tiny Trial.gba")+"/save/backups", "/games/{gameId}/save/backups", nil, http.StatusNotFound), "not_found")

	r = f.do(http.MethodGet, g+"/save/backups", "/games/{gameId}/save/backups", nil, http.StatusOK)
	var list struct {
		Backups []backupJSON `json:"backups"`
	}
	r.JSON(t, &list)
	if len(list.Backups) != 3 || list.Backups[1].ID != manual.ID || list.Backups[2].ID != res.Backup.ID {
		t.Fatalf("list: %s", r.Body)
	}
	bk := g + "/save/backups/" + res.Backup.ID
	r = f.do(http.MethodGet, bk, "/games/{gameId}/save/backups/{backupId}", nil, http.StatusOK)
	if string(r.Body) != old {
		t.Fatal("backup content")
	}

	// Restore: the current save is backed up first.
	r = f.do(http.MethodPost, bk+"/restore", "/games/{gameId}/save/backups/{backupId}/restore", nil, http.StatusOK)
	var restored struct {
		Backup *backupJSON `json:"backup"`
	}
	r.JSON(t, &restored)
	if restored.Backup == nil || restored.Backup.Reason != "before-restore" || f.read("Saves/GBA/Starlight Courier.gba.sav") != old {
		t.Fatalf("restore: %s", r.Body)
	}

	// Delete (to the trash), then 404.
	f.do(http.MethodDelete, bk, "/games/{gameId}/save/backups/{backupId}", nil, http.StatusNoContent)
	wantCode(t, f.do(http.MethodGet, bk, "/games/{gameId}/save/backups/{backupId}", nil, http.StatusNotFound), "not_found")
	wantCode(t, f.do(http.MethodGet, g+"/save/backups/bk_bad", "/games/{gameId}/save/backups/{backupId}", nil, http.StatusBadRequest), "invalid_id")
	r = f.do(http.MethodGet, "/api/v1/trash", "/trash", nil, http.StatusOK)
	if !strings.Contains(string(r.Body), `"reason":"delete-backup"`) {
		t.Fatalf("trash: %s", r.Body)
	}

	// Not enough space: 507 before anything is written.
	f.storage.Store(10)
	wantCode(t, f.do(http.MethodPut, g+"/save", "/games/{gameId}/save", []byte("x"), http.StatusInsufficientStorage), "insufficient_storage")
	f.storage.Store(1 << 40)

	// Non-default save format: writes refused (422).
	testutil.WriteFile(t, f.sd, ".userdata/shared/minuisettings.txt", "saveFormat=3\n")
	r = f.do(http.MethodPut, g+"/save", "/games/{gameId}/save", []byte("x"), http.StatusUnprocessableEntity)
	wantCode(t, r, "unsupported_format")
	if !strings.Contains(string(r.Body), "saveFormat=3") {
		t.Fatalf("message: %s", r.Body)
	}
	_, r = f.createUpload(map[string]any{"target": map[string]any{"kind": "save", "gameId": library.EncodeID(scPath)}, "sizeBytes": 1, "sha256": sha([]byte{1})}, http.StatusUnprocessableEntity)
	wantCode(t, r, "unsupported_format")
}

type slotJSON struct {
	Slot         int     `json:"slot"`
	HasThumbnail bool    `json:"hasThumbnail"`
	Locked       bool    `json:"locked"`
	LockStatus   *string `json:"lockStatus"`
}

func TestStateRoutes(t *testing.T) {
	f := newFixture(t)
	g := gamePath(scPath)
	r := f.do(http.MethodGet, g+"/states", "/games/{gameId}/states", nil, http.StatusOK)
	var list struct {
		Slots      []slotJSON `json:"slots"`
		ResumeSlot *int       `json:"resumeSlot"`
	}
	r.JSON(t, &list)
	if len(list.Slots) != 3 || !list.Slots[0].HasThumbnail || list.ResumeSlot == nil || *list.ResumeSlot != 9 {
		t.Fatalf("list: %s", r.Body)
	}
	r = f.do(http.MethodGet, g+"/states/1", "/games/{gameId}/states/{slot}", nil, http.StatusOK)
	if string(r.Body) != f.read(statesGB+"Starlight Courier.gba.st1") {
		t.Fatal("state content")
	}
	r = f.do(http.MethodGet, g+"/states/0/thumbnail", "/games/{gameId}/states/{slot}/thumbnail", nil, http.StatusOK)
	if r.Header.Get("Content-Type") != "image/png" || !strings.HasPrefix(string(r.Body), "\x89PNG") {
		t.Fatalf("thumbnail: %v", r.Header)
	}
	f.do(http.MethodGet, g+"/states/1/thumbnail", "/games/{gameId}/states/{slot}/thumbnail", nil, http.StatusNotFound)
	for _, bad := range []string{"01", "-1", "100", "x"} {
		wantCode(t, f.do(http.MethodGet, g+"/states/"+bad, "/games/{gameId}/states/{slot}", nil, http.StatusBadRequest), "invalid_id")
	}
	f.do(http.MethodGet, g+"/states/5", "/games/{gameId}/states/{slot}", nil, http.StatusNotFound)

	// Lock, then the slot cannot be deleted nor uploaded to.
	r = f.do(http.MethodPut, g+"/states/1/lock", "/games/{gameId}/states/{slot}/lock", nil, http.StatusOK)
	var s slotJSON
	r.JSON(t, &s)
	if !s.Locked || s.LockStatus == nil || *s.LockStatus != "intact" {
		t.Fatalf("lock: %s", r.Body)
	}
	wantCode(t, f.do(http.MethodDelete, g+"/states/1", "/games/{gameId}/states/{slot}", nil, http.StatusConflict), "state_locked")
	f.do(http.MethodPut, g+"/states/5/lock", "/games/{gameId}/states/{slot}/lock", nil, http.StatusNotFound)

	// Overwritten in game, restored by the app.
	orig := f.read(statesGB + "Starlight Courier.gba.st1")
	testutil.WriteFile(t, f.sd, statesGB+"Starlight Courier.gba.st1", "played")
	f.do(http.MethodPost, g+"/states/1/restore", "/games/{gameId}/states/{slot}/restore", nil, http.StatusOK)
	if f.read(statesGB+"Starlight Courier.gba.st1") != orig {
		t.Fatal("not restored")
	}
	wantCode(t, f.do(http.MethodPost, g+"/states/0/restore", "/games/{gameId}/states/{slot}/restore", nil, http.StatusNotFound), "not_found")

	r = f.do(http.MethodDelete, g+"/states/1/lock", "/games/{gameId}/states/{slot}/lock", nil, http.StatusOK)
	r.JSON(t, &s)
	if s.Locked || s.LockStatus != nil {
		t.Fatalf("unlock: %s", r.Body)
	}
	r = f.do(http.MethodDelete, g+"/states/1", "/games/{gameId}/states/{slot}", nil, http.StatusOK)
	if !strings.Contains(string(r.Body), `"trashEntryId":"tr_`) {
		t.Fatalf("delete: %s", r.Body)
	}

	// Resume history: archived by the maintenance, restored by the app.
	f.states.Maintain(context.Background())
	testutil.WriteFile(t, f.sd, statesGB+"Starlight Courier.gba.st9", "newer resume")
	r = f.do(http.MethodGet, g+"/resume-history", "/games/{gameId}/resume-history", nil, http.StatusOK)
	var h struct {
		Entries []struct {
			ID string `json:"id"`
		} `json:"entries"`
	}
	r.JSON(t, &h)
	if len(h.Entries) != 1 {
		t.Fatalf("history: %s", r.Body)
	}
	r = f.do(http.MethodPost, g+"/resume-history/"+h.Entries[0].ID+"/restore", "/games/{gameId}/resume-history/{entryId}/restore", nil, http.StatusOK)
	r.JSON(t, &h)
	if len(h.Entries) != 2 || f.read(statesGB+"Starlight Courier.gba.st9") == "newer resume" {
		t.Fatalf("restore resume: %s", r.Body)
	}
	f.do(http.MethodPost, g+"/resume-history/rh_20261118T165500Z_dead/restore", "/games/{gameId}/resume-history/{entryId}/restore", nil, http.StatusNotFound)
	wantCode(t, f.do(http.MethodPost, g+"/resume-history/nope/restore", "/games/{gameId}/resume-history/{entryId}/restore", nil, http.StatusBadRequest), "invalid_id")
	f.do(http.MethodPut, g+"/states/9/lock", "/games/{gameId}/states/{slot}/lock", nil, http.StatusOK)
	wantCode(t, f.do(http.MethodPost, g+"/resume-history/"+h.Entries[0].ID+"/restore", "/games/{gameId}/resume-history/{entryId}/restore", nil, http.StatusConflict), "state_locked")

	// Non-default state format: writes refused.
	testutil.WriteFile(t, f.sd, ".userdata/shared/minuisettings.txt", "stateFormat=4\n")
	wantCode(t, f.do(http.MethodPut, g+"/states/0/lock", "/games/{gameId}/states/{slot}/lock", nil, http.StatusUnprocessableEntity), "unsupported_format")
	_, r = f.createUpload(map[string]any{"target": map[string]any{"kind": "state", "gameId": library.EncodeID(scPath), "slot": 2}, "sizeBytes": 1, "sha256": sha([]byte{1})}, http.StatusUnprocessableEntity)
	wantCode(t, r, "unsupported_format")
	r = f.do(http.MethodGet, g+"/states", "/games/{gameId}/states", nil, http.StatusOK)
	if !strings.Contains(string(r.Body), `"resumeSlot":null`) {
		t.Fatalf("resume slot: %s", r.Body)
	}
}

type cheatList struct {
	Cheats []map[string]any `json:"cheats"`
	ETag   string           `json:"etag"`
}

func TestCheatRoutes(t *testing.T) {
	f := newFixture(t)
	g := gamePath(scPath)
	r := f.do(http.MethodGet, g+"/cheats", "/games/{gameId}/cheats", nil, http.StatusOK)
	var list cheatList
	r.JSON(t, &list)
	if len(list.Cheats) != 2 || r.Header.Get("ETag") != list.ETag {
		t.Fatalf("get: %s %v", r.Body, r.Header)
	}
	body := mustJSON(t, map[string]any{"cheats": []any{list.Cheats[1], list.Cheats[0]}})

	wantCode(t, f.send(http.MethodPut, g+"/cheats", "/games/{gameId}/cheats", body, nil, http.StatusBadRequest), "invalid_request")
	r = f.send(http.MethodPut, g+"/cheats", "/games/{gameId}/cheats", body, map[string]string{"If-Match": list.ETag}, http.StatusOK)
	var put cheatList
	r.JSON(t, &put)
	if put.Cheats[0]["description"] != "Moon jump" || r.Header.Get("ETag") != put.ETag || put.ETag == list.ETag {
		t.Fatalf("put: %s", r.Body)
	}
	// Someone else changed the file: 412 conflict.
	wantCode(t, f.send(http.MethodPut, g+"/cheats", "/games/{gameId}/cheats", body, map[string]string{"If-Match": list.ETag}, http.StatusPreconditionFailed), "conflict")
	if f.read("Cheats/GBA/Starlight Courier.gba.cht.bak") == "" {
		t.Fatal("no .bak")
	}
	wantCode(t, f.send(http.MethodPut, g+"/cheats", "/games/{gameId}/cheats", []byte(`{"cheats":[{"description":"x","code":"1","enabled":true,"extra":{"desc":"x"}}]}`), map[string]string{"If-Match": put.ETag}, http.StatusBadRequest), "invalid_request")

	// The game listing counts what was written.
	r = f.do(http.MethodGet, "/api/v1/systems/"+library.EncodeID(gbaFolder)+"/games", "/systems/{systemId}/games", nil, http.StatusOK)
	if !strings.Contains(string(r.Body), `"fileName":"Starlight Courier.gba","kind":"file"`) || !strings.Contains(string(r.Body), `"cheatCount":2`) {
		t.Fatalf("listing: %s", r.Body)
	}

	imp := func(source map[string]any, mode string, want int) testutil.Response {
		t.Helper()
		return f.do(http.MethodPost, g+"/cheats/import", "/games/{gameId}/cheats/import", mustJSON(t, map[string]any{"source": source, "mode": mode}), want)
	}
	cht := "cheat0_desc = \"Imported\"\ncheat0_code = \"1\"\ncheat0_enable = true\n"
	r = imp(map[string]any{"type": "inline", "text": cht}, "merge", http.StatusOK)
	if strings.TrimSpace(string(r.Body)) != `{"imported":1,"total":3}` {
		t.Fatalf("inline: %s", r.Body)
	}
	wantCode(t, imp(map[string]any{"type": "inline", "text": "nothing here"}, "merge", http.StatusUnprocessableEntity), "unsupported_format")
	wantCode(t, imp(map[string]any{"type": "inline", "text": cht}, "append", http.StatusBadRequest), "invalid_request")
	wantCode(t, imp(map[string]any{"type": "ftp"}, "merge", http.StatusBadRequest), "invalid_request")
	wantCode(t, imp(map[string]any{"type": "url"}, "merge", http.StatusBadRequest), "invalid_request")

	// URL: served by a local server; the URL path and query never reach the log.
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.cht":
			w.Write([]byte(cht))
		case "/big.cht":
			w.Write([]byte(strings.Repeat("#", 5<<20+1)))
		case "/broken":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer src.Close()
	r = imp(map[string]any{"type": "url", "url": src.URL + "/ok.cht?token=secret123"}, "replace", http.StatusOK)
	if strings.TrimSpace(string(r.Body)) != `{"imported":1,"total":1}` {
		t.Fatalf("url: %s", r.Body)
	}
	wantCode(t, imp(map[string]any{"type": "url", "url": src.URL + "/missing.cht"}, "merge", http.StatusNotFound), "not_found")
	wantCode(t, imp(map[string]any{"type": "url", "url": src.URL + "/big.cht"}, "merge", http.StatusRequestEntityTooLarge), "too_large")
	wantCode(t, imp(map[string]any{"type": "url", "url": src.URL + "/broken"}, "merge", http.StatusUnprocessableEntity), "unsupported_format")
	wantCode(t, imp(map[string]any{"type": "url", "url": "file:///etc/passwd"}, "merge", http.StatusBadRequest), "invalid_request")
	if strings.Contains(f.logs.String(), "secret123") || strings.Contains(f.logs.String(), "ok.cht") {
		t.Fatal("URL logged in full")
	}

	// SD card file, from GET /sd/files.
	r = f.do(http.MethodGet, "/api/v1/sd/files?type=cht", "/sd/files", nil, http.StatusOK)
	var files struct {
		Files []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"files"`
	}
	r.JSON(t, &files)
	if len(files.Files) != 2 || files.Files[0].Path != "Cheats/GBA/Pixel Garden 🌱.gba.cht" {
		t.Fatalf("sd files: %s", r.Body)
	}
	r = imp(map[string]any{"type": "sd", "sdFileId": files.Files[0].ID}, "merge", http.StatusOK)
	if strings.TrimSpace(string(r.Body)) != `{"imported":1,"total":2}` {
		t.Fatalf("sd: %s", r.Body)
	}
	wantCode(t, imp(map[string]any{"type": "sd", "sdFileId": "sd_" + library.EncodeID("Saves/GBA/Starlight Courier.gba.sav")}, "merge", http.StatusBadRequest), "invalid_id")
	f.do(http.MethodGet, "/api/v1/sd/files?type=patch", "/sd/files", nil, http.StatusOK)
	wantCode(t, f.do(http.MethodGet, "/api/v1/sd/files", "/sd/files", nil, http.StatusBadRequest), "invalid_request")

	// Staging upload: by uploadId (this run) or by stagingId.
	data := []byte(cht)
	c, _ := f.createUpload(map[string]any{"target": map[string]any{"kind": "staging", "fileName": "game.cht"}, "sizeBytes": len(data), "sha256": sha(data)}, http.StatusCreated)
	f.chunk(c.UploadID, 0, data)
	r = f.do(http.MethodPost, "/api/v1/uploads/"+c.UploadID+"/complete", "/uploads/{uploadId}/complete", nil, http.StatusOK)
	var staged struct {
		StagingID string `json:"stagingId"`
	}
	r.JSON(t, &staged)
	r = imp(map[string]any{"type": "upload", "uploadId": c.UploadID}, "merge", http.StatusOK)
	if strings.TrimSpace(string(r.Body)) != `{"imported":1,"total":3}` {
		t.Fatalf("upload: %s", r.Body)
	}
	imp(map[string]any{"type": "staging", "stagingId": staged.StagingID}, "merge", http.StatusOK)
	imp(map[string]any{"type": "upload", "uploadId": "up_unknown"}, "merge", http.StatusNotFound)
	imp(map[string]any{"type": "staging", "stagingId": "st_20261118T170000Z_00000000"}, "merge", http.StatusNotFound)
}

func TestSaveRouteBodyLimit(t *testing.T) {
	f := newFixture(t)
	g := gamePath(scPath)
	// The 64 MiB limit of PUT /save (the other signed routes keep 1 MiB).
	big := make([]byte, 2<<20)
	f.do(http.MethodPut, g+"/save", "/games/{gameId}/save", big, http.StatusOK)
	r := f.client.Do(t, http.MethodPost, g+"/save/backups", big)
	if r.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("JSON route with 2 MiB: %d", r.Status)
	}
	if fi, err := os.Stat(filepath.Join(f.sd, "Saves", "GBA", "Starlight Courier.gba.sav")); err != nil || fi.Size() != int64(len(big)) {
		t.Fatalf("saved %v", err)
	}
}
