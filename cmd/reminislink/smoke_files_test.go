package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

// sdRel is p relative to the card, "/" separator.
func sdRel(t *testing.T, roots osadapter.Roots, p string) string {
	t.Helper()
	rel, err := filepath.Rel(roots.SD, p)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(rel)
}

// smokeFiles checks the writes of the SD card browser and the renaming of
// a game (M23) through the API of a running pak: a folder made in the
// system folder, a file uploaded into it (409 then a free name), both
// moved to the trash and restored, the protected places refused, and the
// game renamed with its save then renamed back.
func smokeFiles(t *testing.T, c *testutil.Client, roots osadapter.Roots, system, gameName, saveDir, saveName string) {
	t.Helper()
	sysID := library.SDEntryID(sdRel(t, roots, filepath.Join(roots.Roms, system)))
	var folder library.SDEntry
	call(t, c, http.MethodPost, "/api/v1/sd/folders", map[string]any{"parentId": sysID, "name": "Smoke Folder"}, http.StatusCreated, &folder)
	call(t, c, http.MethodPost, "/api/v1/sd/folders", map[string]any{"parentId": sysID, "name": "Smoke Folder"}, http.StatusConflict, nil)
	data := []byte("smoke file " + time.Now().String())
	sum := sha256.Sum256(data)
	upload := func(onConflict string, want int) map[string]any {
		var created struct {
			UploadID string `json:"uploadId"`
		}
		body := map[string]any{
			"target":    map[string]any{"kind": "sd-file", "folderId": folder.ID, "fileName": "notes.txt"},
			"sizeBytes": len(data), "sha256": hex.EncodeToString(sum[:]), "onConflict": onConflict,
		}
		r := c.Do(t, http.MethodPost, "/api/v1/uploads", mustMarshal(t, body))
		if r.Status != want {
			t.Fatalf("create sd-file upload (%s): %d %s", onConflict, r.Status, r.Body)
		}
		if want != http.StatusCreated {
			return nil
		}
		r.JSON(t, &created)
		req := c.RequestUnsigned(t, http.MethodPut, "/api/v1/uploads/"+created.UploadID, data)
		req.Header.Set("Upload-Offset", "0")
		if r := c.Send(t, req); r.Status != http.StatusNoContent {
			t.Fatalf("chunk: %d %s", r.Status, r.Body)
		}
		var res map[string]any
		call(t, c, http.MethodPost, "/api/v1/uploads/"+created.UploadID+"/complete", nil, http.StatusOK, &res)
		return res
	}
	upload("fail", http.StatusCreated)
	upload("fail", http.StatusConflict)
	upload("rename", http.StatusCreated)
	dir := filepath.Join(roots.Roms, system, "Smoke Folder")
	for _, name := range []string{"notes.txt", "notes (2).txt"} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || !bytes.Equal(b, data) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// Protected places.
	var root library.SDFolder
	call(t, c, http.MethodGet, "/api/v1/sd/browse", nil, http.StatusOK, &root)
	for _, e := range root.Entries {
		if e.Kind == library.SDEntryFolder && strings.HasPrefix(e.Name, ".") {
			call(t, c, http.MethodPost, "/api/v1/sd/folders", map[string]any{"parentId": e.ID, "name": "x"}, http.StatusConflict, nil)
			call(t, c, http.MethodPost, "/api/v1/sd/trash", trashBody(e), http.StatusConflict, nil)
		}
	}
	for _, e := range root.Entries {
		if e.ID == library.SDEntryID(sdRel(t, roots, roots.Roms)) {
			call(t, c, http.MethodPost, "/api/v1/sd/trash", trashBody(e), http.StatusConflict, nil)
		}
	}
	// To the trash and back.
	var moved struct {
		TrashEntryID string   `json:"trashEntryId"`
		Moved        []string `json:"moved"`
	}
	call(t, c, http.MethodPost, "/api/v1/sd/trash", trashBody(listed(t, c, sysID, folder.ID)), http.StatusOK, &moved)
	if _, err := os.Lstat(dir); err == nil || len(moved.Moved) != 2 {
		t.Fatalf("folder not moved: %+v", moved)
	}
	call(t, c, http.MethodPost, "/api/v1/trash/"+moved.TrashEntryID+"/restore", nil, http.StatusOK, nil)
	if b, err := os.ReadFile(filepath.Join(dir, "notes.txt")); err != nil || !bytes.Equal(b, data) {
		t.Fatalf("restored: %v", err)
	}
	call(t, c, http.MethodPost, "/api/v1/sd/trash", trashBody(listed(t, c, sysID, folder.ID)), http.StatusOK, nil)

	// Rename the game and back: its save follows.
	ext := path.Ext(gameName)
	oldStem := strings.TrimSuffix(gameName, ext)
	gameID := library.EncodeID(system + "/" + gameName)
	save, err := os.ReadFile(filepath.Join(saveDir, saveName))
	if err != nil {
		t.Fatal(err)
	}
	var renamed struct {
		Game struct {
			ID      string `json:"id"`
			HasSave bool   `json:"hasSave"`
		} `json:"game"`
		Renamed []struct{ From, To string } `json:"renamed"`
	}
	call(t, c, http.MethodPost, "/api/v1/games/"+gameID+"/rename", map[string]any{"name": "Smoke Renamed"}, http.StatusOK, &renamed)
	newSave := strings.Replace(saveName, oldStem, "Smoke Renamed", 1)
	if b, err := os.ReadFile(filepath.Join(saveDir, newSave)); err != nil || !bytes.Equal(b, save) || !renamed.Game.HasSave {
		t.Fatalf("renamed save %s: %v (%+v)", newSave, err, renamed)
	}
	if _, err := os.Lstat(filepath.Join(roots.Roms, system, "Smoke Renamed"+ext)); err != nil {
		t.Fatal(err)
	}
	call(t, c, http.MethodPost, "/api/v1/games/"+renamed.Game.ID+"/rename", map[string]any{"name": oldStem}, http.StatusOK, nil)
	if b, err := os.ReadFile(filepath.Join(saveDir, saveName)); err != nil || !bytes.Equal(b, save) {
		t.Fatalf("renamed back: %v", err)
	}
	t.Logf("files: folder, upload (conflict, free name), trash and restore, protected places; %s renamed (%d files) and back", gameName, len(renamed.Renamed))
}

// trashBody is the body of POST /sd/trash for a listed entry, with the
// version the listing gave.
func trashBody(e library.SDEntry) map[string]any {
	return map[string]any{"items": []any{map[string]any{
		"sdEntryId": e.ID, "sizeBytes": e.SizeBytes, "modifiedAt": e.ModifiedAt, "version": e.Version,
	}}}
}

// listed reads the entry id in the folder parentID as the browser lists it.
func listed(t *testing.T, c *testutil.Client, parentID, id string) library.SDEntry {
	t.Helper()
	var f library.SDFolder
	call(t, c, http.MethodGet, "/api/v1/sd/browse?folderId="+parentID, nil, http.StatusOK, &f)
	for _, e := range f.Entries {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("%s not listed", id)
	return library.SDEntry{}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestSmokeFiles runs the M23 scenario against the real binary on the
// NextUI card, and a power cut between the renames of a game: a journal
// left by an interrupted rename (two of its three files renamed) is put
// back at start, every file under its old name.
func TestSmokeFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the pak")
	}
	bin := filepath.Join(t.TempDir(), "reminislink")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	sd := testutil.CopySDCard(t)
	roots := nextui.RootsForSD(sd, "tg5040")
	layout := store.Layout{Root: roots.Data}

	// The power cut: ROM and save renamed, the states not yet.
	gba := filepath.Join(roots.Roms, "Game Boy Advance (GBA)")
	steps := [][2]string{
		{filepath.Join(gba, "Starlight Courier.gba"), filepath.Join(gba, "Cut.gba")},
		{filepath.Join(roots.Saves, "GBA", "Starlight Courier.gba.sav"), filepath.Join(roots.Saves, "GBA", "Cut.gba.sav")},
		{filepath.Join(roots.Shared, "GBA-gpsp", "Starlight Courier.gba.st0"), filepath.Join(roots.Shared, "GBA-gpsp", "Cut.gba.st0")},
	}
	var records []map[string]string
	for i, s := range steps {
		from, to := sdRel(t, roots, s[0]), sdRel(t, roots, s[1])
		records = append(records, map[string]string{"from": from, "to": to, "via": path.Join(path.Dir(from), ".reminislink-rename-rn_20261122T100000Z_abcd-"+strconv.Itoa(i))})
		if i < 2 {
			if err := os.Rename(s[0], s[1]); err != nil {
				t.Fatal(err)
			}
		}
	}
	journal, _ := json.Marshal(map[string]any{"version": 1, "id": "rn_20261122T100000Z_abcd", "label": "Starlight Courier", "steps": records})
	writeFile(t, filepath.Join(layout.RenamesDir(), "rn_20261122T100000Z_abcd", "journal.json"), string(journal))

	clients, err := auth.OpenClientStore(layout.ClientsFile())
	if err != nil {
		t.Fatal(err)
	}
	p := &pak{t: t, bin: bin, sd: sd, port: freePort(t)}
	c := testutil.PairClient(t, clients, p.url())
	p.start()
	c.HTTP = p.httpClient(30 * time.Second)
	defer p.stop()
	for _, s := range steps {
		if _, err := os.Lstat(s[0]); err != nil {
			t.Fatalf("not put back: %s", sdRel(t, roots, s[0]))
		}
		if _, err := os.Lstat(s[1]); err == nil {
			t.Fatalf("still renamed: %s", sdRel(t, roots, s[1]))
		}
	}
	if des, _ := os.ReadDir(layout.RenamesDir()); len(des) != 0 {
		t.Fatalf("journal left: %d", len(des))
	}
	t.Log("power cut between the renames: put back at start")

	smokeFiles(t, c, roots, "Game Boy Advance (GBA)", "Starlight Courier.gba", filepath.Join(roots.Saves, "GBA"), "Starlight Courier.gba.sav")
}
