package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

// M23: writes of the SD card browser and the renaming of a game.

const (
	sdFoldersPath = "/sd/folders"
	sdTrashPath   = "/sd/trash"
	renamePath    = "/games/{gameId}/rename"
)

// uploadSDFile sends data with the sd-file target and completes it; it
// returns the answer of the completion.
func (f *fixture) uploadSDFile(folderID any, name, data, onConflict string, wantComplete int) testutil.Response {
	f.t.Helper()
	body := []byte(data)
	c, r := f.createUpload(map[string]any{
		"target":    map[string]any{"kind": "sd-file", "folderId": folderID, "fileName": name},
		"sizeBytes": len(body), "sha256": sha(body), "onConflict": onConflict,
	}, http.StatusCreated)
	_ = r
	if len(body) > 0 {
		if r := f.chunk(c.UploadID, 0, body); r.Status != http.StatusNoContent {
			f.t.Fatalf("chunk: %d %s", r.Status, r.Body)
		}
	}
	return f.do(http.MethodPost, "/api/v1/uploads/"+c.UploadID+"/complete", "/uploads/{uploadId}/complete", nil, wantComplete)
}

func TestSDFileUpload(t *testing.T) {
	f := newFixture(t)
	hacks := library.SDEntryID("Roms/Game Boy Advance (GBA)/Hacks")
	r := f.uploadSDFile(hacks, "notes.txt", "hello", "fail", http.StatusOK)
	var res struct {
		SDEntryID string `json:"sdEntryId"`
	}
	r.JSON(t, &res)
	if res.SDEntryID != library.SDEntryID("Roms/Game Boy Advance (GBA)/Hacks/notes.txt") || f.read("Roms/Game Boy Advance (GBA)/Hacks/notes.txt") != "hello" {
		t.Fatalf("added: %+v", res)
	}
	// Taken: 409 at creation with the free name, then rename.
	_, r = f.createUpload(map[string]any{
		"target":    map[string]any{"kind": "sd-file", "folderId": hacks, "fileName": "notes.txt"},
		"sizeBytes": 3, "sha256": sha([]byte("new")), "onConflict": "fail",
	}, http.StatusConflict)
	if !strings.Contains(string(r.Body), `"freeName":"notes (2).txt"`) {
		t.Fatalf("conflict: %s", r.Body)
	}
	f.uploadSDFile(hacks, "notes.txt", "new", "rename", http.StatusOK)
	if f.read("Roms/Game Boy Advance (GBA)/Hacks/notes (2).txt") != "new" || f.read("Roms/Game Boy Advance (GBA)/Hacks/notes.txt") != "hello" {
		t.Fatal("rename")
	}
	// Never overwrite; the root of the card; protected folders refused.
	f.createUpload(map[string]any{"target": map[string]any{"kind": "sd-file", "folderId": hacks, "fileName": "x"}, "sizeBytes": 1, "sha256": sha([]byte("x")), "onConflict": "overwrite"}, http.StatusBadRequest)
	f.uploadSDFile(nil, "root.txt", "r", "fail", http.StatusOK)
	if f.read("root.txt") != "r" {
		t.Fatal("root")
	}
	for _, id := range []string{library.SDEntryID(".userdata/shared"), library.SDEntryID(".system")} {
		_, r := f.createUpload(map[string]any{"target": map[string]any{"kind": "sd-file", "folderId": id, "fileName": "x.txt"}, "sizeBytes": 1, "sha256": sha([]byte("x"))}, http.StatusConflict)
		if !strings.Contains(string(r.Body), `"protected":"system"`) {
			t.Errorf("%s: %s", id, r.Body)
		}
	}
	// Hostile names and identifiers.
	for _, name := range []string{"../x", "a/b", ".", "..", "", "\x00x"} {
		f.createUpload(map[string]any{"target": map[string]any{"kind": "sd-file", "folderId": hacks, "fileName": name}, "sizeBytes": 1, "sha256": sha([]byte("x"))}, http.StatusBadRequest)
	}
	f.createUpload(map[string]any{"target": map[string]any{"kind": "sd-file", "folderId": "se_" + library.EncodeID("../etc"), "fileName": "x"}, "sizeBytes": 1, "sha256": sha([]byte("x"))}, http.StatusBadRequest)
	f.createUpload(map[string]any{"target": map[string]any{"kind": "sd-file", "folderId": library.SDEntryID("Nope"), "fileName": "x"}, "sizeBytes": 1, "sha256": sha([]byte("x"))}, http.StatusNotFound)
	f.createUpload(map[string]any{"target": map[string]any{"kind": "sd-file", "folderId": library.SDEntryID(".userdata/shared/ReminisLink"), "fileName": "x"}, "sizeBytes": 1, "sha256": sha([]byte("x"))}, http.StatusNotFound)
}

func TestSDFoldersAndTrash(t *testing.T) {
	f := newFixture(t)
	gba := library.SDEntryID("Roms/Game Boy Advance (GBA)")
	var entry library.SDEntry
	f.do(http.MethodPost, "/api/v1/sd/folders", sdFoldersPath, mustJSON(t, map[string]any{"parentId": gba, "name": "New Folder"}), http.StatusCreated).JSON(t, &entry)
	if entry.Kind != "folder" || entry.Name != "New Folder" || !entry.Deletable || !f.exists("Roms/Game Boy Advance (GBA)/New Folder") {
		t.Fatalf("folder %+v", entry)
	}
	f.do(http.MethodPost, "/api/v1/sd/folders", sdFoldersPath, mustJSON(t, map[string]any{"parentId": gba, "name": "New Folder"}), http.StatusConflict)
	f.do(http.MethodPost, "/api/v1/sd/folders", sdFoldersPath, mustJSON(t, map[string]any{"parentId": library.SDEntryID(".system"), "name": "x"}), http.StatusConflict)
	f.do(http.MethodPost, "/api/v1/sd/folders", sdFoldersPath, mustJSON(t, map[string]any{"parentId": gba, "name": "../x"}), http.StatusBadRequest)
	f.do(http.MethodPost, "/api/v1/sd/folders", sdFoldersPath, mustJSON(t, map[string]any{"parentId": nil, "name": "Top"}), http.StatusCreated)

	// Browse says what can change.
	var folder library.SDFolder
	f.get("/api/v1/sd/browse?folderId="+gba, "/sd/browse", http.StatusOK).JSON(t, &folder)
	if !folder.Writable {
		t.Fatal("gba not writable")
	}

	// Trash: refused requests move nothing; then the folder and a file.
	// The items carry the version the client listed (Codex review of M23).
	body := func(ids ...string) []byte {
		var items []map[string]any
		for _, id := range ids {
			items = append(items, f.sdItem(id))
		}
		return mustJSON(t, map[string]any{"items": items})
	}
	f.do(http.MethodPost, "/api/v1/sd/trash", sdTrashPath, body(), http.StatusBadRequest)
	r := f.do(http.MethodPost, "/api/v1/sd/trash", sdTrashPath, body(entry.ID, library.SDEntryID("Roms")), http.StatusConflict)
	if !strings.Contains(string(r.Body), `"protected":"root"`) || !f.exists("Roms/Game Boy Advance (GBA)/New Folder") {
		t.Fatalf("protected: %s", r.Body)
	}
	f.do(http.MethodPost, "/api/v1/sd/trash", sdTrashPath, body(library.SDEntryID("Roms/nope")), http.StatusNotFound)
	f.do(http.MethodPost, "/api/v1/sd/trash", sdTrashPath, body("se_"+library.EncodeID("../x")), http.StatusBadRequest)
	readme := library.SDEntryID("Roms/Game Boy Advance (GBA)/readme.txt")
	var moved struct {
		TrashEntryID string   `json:"trashEntryId"`
		Moved        []string `json:"moved"`
	}
	f.do(http.MethodPost, "/api/v1/sd/trash", sdTrashPath, body(entry.ID, readme), http.StatusOK).JSON(t, &moved)
	if f.exists("Roms/Game Boy Advance (GBA)/readme.txt") || f.exists("Roms/Game Boy Advance (GBA)/New Folder") || len(moved.Moved) != 1 {
		t.Fatalf("moved %+v", moved)
	}
	// The trash lists it (delete-file) and the restore puts it back.
	var list struct {
		Entries []struct {
			ID     string `json:"id"`
			Reason string `json:"reason"`
			Label  string `json:"label"`
		} `json:"entries"`
	}
	f.get("/api/v1/trash", "/trash", http.StatusOK).JSON(t, &list)
	if len(list.Entries) == 0 || list.Entries[0].Reason != "delete-file" || list.Entries[0].Label != "2 items" {
		t.Fatalf("trash %+v", list)
	}
	f.do(http.MethodPost, "/api/v1/trash/"+moved.TrashEntryID+"/restore", "/trash/{entryId}/restore", mustJSON(t, map[string]any{"onConflict": "fail"}), http.StatusOK)
	if !f.exists("Roms/Game Boy Advance (GBA)/readme.txt") || !f.exists("Roms/Game Boy Advance (GBA)/New Folder") {
		t.Fatal("not restored")
	}
}

// sdItem is the item of POST /sd/trash for an entry, from the listing of
// its folder (a zero version when it is not listed).
func (f *fixture) sdItem(id string) map[string]any {
	f.t.Helper()
	item := map[string]any{"sdEntryId": id, "sizeBytes": nil, "modifiedAt": "2000-01-01T00:00:00Z", "version": "v1-unlisted"}
	rel, err := library.DecodeID(strings.TrimPrefix(id, "se_"))
	if err != nil {
		return item
	}
	parent := ""
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		parent = "?folderId=" + library.SDEntryID(rel[:i])
	}
	var folder library.SDFolder
	r := f.client.Do(f.t, http.MethodGet, "/api/v1/sd/browse"+parent, nil)
	if r.Status != http.StatusOK {
		return item
	}
	r.JSON(f.t, &folder)
	for _, e := range folder.Entries {
		if e.ID == id {
			item["sizeBytes"] = e.SizeBytes
			item["modifiedAt"] = e.ModifiedAt
			item["version"] = e.Version
		}
	}
	return item
}

// A file replaced since it was listed is never moved in its place.
func TestSDTrashRefusesAChangedFile(t *testing.T) {
	f := newFixture(t)
	f.writeSD("Stuff/notes.txt", "seen")
	id := library.SDEntryID("Stuff/notes.txt")
	seen := f.sdItem(id)
	f.writeSD("Stuff/notes.txt", "replaced by another client, longer")
	r := f.do(http.MethodPost, "/api/v1/sd/trash", sdTrashPath, mustJSON(t, map[string]any{"items": []any{seen}}), http.StatusConflict)
	if !strings.Contains(string(r.Body), `"changed":["`+id+`"]`) || !f.exists("Stuff/notes.txt") {
		t.Fatalf("changed: %s", r.Body)
	}
	f.do(http.MethodPost, "/api/v1/sd/trash", sdTrashPath, mustJSON(t, map[string]any{"items": []any{map[string]any{"sdEntryId": id}}}), http.StatusBadRequest)
	// The version is required and never empty: the same date and size
	// without it would move a replaced file (Codex review of M23, round 2).
	noVersion := f.sdItem(id)
	noVersion["version"] = ""
	f.do(http.MethodPost, "/api/v1/sd/trash", sdTrashPath, mustJSON(t, map[string]any{"items": []any{noVersion}}), http.StatusBadRequest)
	delete(noVersion, "version")
	f.do(http.MethodPost, "/api/v1/sd/trash", sdTrashPath, mustJSON(t, map[string]any{"items": []any{noVersion}}), http.StatusBadRequest)
	// sizeBytes is required, null for a folder: absent is refused (Codex
	// review of M23, round 3).
	f.do(http.MethodPost, "/api/v1/sd/folders", sdFoldersPath, mustJSON(t, map[string]any{"parentId": library.SDEntryID("Stuff"), "name": "Sub"}), http.StatusCreated)
	sub := f.sdItem(library.SDEntryID("Stuff/Sub"))
	delete(sub, "sizeBytes")
	f.do(http.MethodPost, "/api/v1/sd/trash", sdTrashPath, mustJSON(t, map[string]any{"items": []any{sub}}), http.StatusBadRequest)
	if !f.exists("Stuff/Sub") {
		t.Fatal("folder moved without sizeBytes")
	}
	f.do(http.MethodPost, "/api/v1/sd/trash", sdTrashPath, mustJSON(t, map[string]any{"sdEntryIds": []string{id}}), http.StatusBadRequest)
	f.do(http.MethodPost, "/api/v1/sd/trash", sdTrashPath, mustJSON(t, map[string]any{"items": []any{f.sdItem(id)}}), http.StatusOK)
}

func TestRenameGameRoute(t *testing.T) {
	f := newFixture(t)
	id := library.EncodeID("Game Boy Advance (GBA)/Starlight Courier.gba")
	url := "/api/v1/games/" + id + "/rename"
	for _, bad := range []string{"", "a/b", "x:y", "trailing ", "trailing.", ".hidden", "CON", "a‮b", "a\x01b"} {
		f.do(http.MethodPost, url, renamePath, mustJSON(t, map[string]any{"name": bad}), http.StatusBadRequest)
	}
	f.do(http.MethodPost, url, renamePath, mustJSON(t, map[string]any{}), http.StatusBadRequest)
	r := f.do(http.MethodPost, url, renamePath, mustJSON(t, map[string]any{"name": "Café Rêverie (France)"}), http.StatusConflict)
	if !strings.Contains(string(r.Body), "conflicts") {
		t.Fatalf("conflict: %s", r.Body)
	}
	var res struct {
		Game struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"game"`
		Renamed []struct{ From, To string } `json:"renamed"`
	}
	f.do(http.MethodPost, url, renamePath, mustJSON(t, map[string]any{"name": "Star Courier"}), http.StatusOK).JSON(t, &res)
	if res.Game.ID != library.EncodeID("Game Boy Advance (GBA)/Star Courier.gba") || res.Game.Name != "Star Courier" || len(res.Renamed) < 5 {
		t.Fatalf("renamed %+v", res)
	}
	if !f.exists("Saves/GBA/Star Courier.gba.sav") || f.exists("Roms/Game Boy Advance (GBA)/Starlight Courier.gba") {
		t.Fatal("files")
	}
	// The old id is gone; the same name changes nothing.
	f.do(http.MethodPost, url, renamePath, mustJSON(t, map[string]any{"name": "X"}), http.StatusNotFound)
	newURL := "/api/v1/games/" + res.Game.ID + "/rename"
	f.do(http.MethodPost, newURL, renamePath, mustJSON(t, map[string]any{"name": "Star Courier"}), http.StatusOK).JSON(t, &res)
	if len(res.Renamed) != 0 {
		t.Fatalf("same name renamed %+v", res.Renamed)
	}
}
