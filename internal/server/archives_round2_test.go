package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/gamearchive"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/testutil"
	"golang.org/x/text/unicode/norm"
)

// Codex review 2 of M19.

func knulliSource(rom string) gamearchive.Source {
	return gamearchive.Source{OS: "knulli", SystemTag: "gba", SystemLabel: "Game Boy Advance",
		GameName: strings.TrimSuffix(rom, filepath.Ext(rom)), FileName: rom, RomName: rom, Kind: "file"}
}

// stagedRaw stages data, expecting the completion status want.
func (f *fixture) stagedRaw(name string, data []byte, want int) {
	f.t.Helper()
	c, _ := f.createUpload(map[string]any{"target": map[string]any{"kind": "staging", "fileName": name}, "sizeBytes": len(data), "sha256": sha(data)}, http.StatusCreated)
	f.chunk(c.UploadID, 0, data)
	f.do(http.MethodPost, "/api/v1/uploads/"+c.UploadID+"/complete", "/uploads/{uploadId}/complete", nil, want)
}

// TestStagingNeverGoesThroughALink: a link at the staging folder is never
// written, read nor deleted through; one at the uploads folder refuses
// any upload.
func TestStagingNeverGoesThroughALink(t *testing.T) {
	f := newKnulliFixture(t)
	outside := t.TempDir()
	staging := f.layout.StagingDir()
	os.RemoveAll(staging)
	os.MkdirAll(filepath.Dir(staging), 0o755)
	if err := os.Symlink(outside, staging); err != nil {
		t.Skip("no symbolic links here")
	}
	f.stagedRaw("a.reminislink.zip", []byte("zip"), http.StatusInternalServerError)
	if names, _ := os.ReadDir(outside); len(names) != 0 {
		t.Fatalf("written through the staging link: %v", names)
	}
	// An entry planted outside is neither read nor deleted.
	planted := filepath.Join(outside, "st_20261121T100000Z_0a1b2c3d")
	os.MkdirAll(planted, 0o755)
	os.WriteFile(filepath.Join(planted, "x.zip"), []byte("x"), 0o644)
	f.inspect("st_20261121T100000Z_0a1b2c3d", nil, http.StatusNotFound)
	f.do(http.MethodDelete, "/api/v1/archives/st_20261121T100000Z_0a1b2c3d", inspectPath, nil, http.StatusNotFound)
	if _, err := os.Stat(filepath.Join(planted, "x.zip")); err != nil {
		t.Fatal("an outside folder was deleted")
	}

	// A link at the uploads folder: no upload is created.
	g := newKnulliFixture(t)
	uploads := g.layout.UploadsDir()
	os.RemoveAll(uploads)
	os.MkdirAll(filepath.Dir(uploads), 0o755)
	os.Symlink(outside, uploads)
	g.createUpload(map[string]any{"target": map[string]any{"kind": "staging", "fileName": "b.zip"}, "sizeBytes": 3, "sha256": sha([]byte("zip"))}, http.StatusInternalServerError)
	if names, _ := os.ReadDir(outside); len(names) != 1 { // the planted folder only
		t.Fatalf("written through the uploads link: %v", names)
	}
}

// TestKnulliNewGameNeverTakesAnotherGamesData: on Knulli the data are
// named after the ROM without its extension: B/Starlight Courier.zip
// would share Starlight Courier.srm and its states with the GBA game.
func TestKnulliNewGameNeverTakesAnotherGamesData(t *testing.T) {
	f := newKnulliFixture(t)
	data := archiveOf(t, knulliSource("Starlight Courier.zip"),
		map[string][]byte{"rom": []byte("rom"), "save": []byte("s"), "state": []byte("st")}, map[string]int{"state": 1})
	id := f.stageFile("a.reminislink.zip", data)
	q := "?systemId=" + url.QueryEscape(library.EncodeID("gba")) + "&folderId=" + url.QueryEscape(library.EncodeID("gba/Hacks"))
	r := f.do(http.MethodGet, "/api/v1/archives/"+id+q, inspectPath, nil, http.StatusConflict)
	if !strings.Contains(string(r.Body), library.EncodeID("gba/Starlight Courier.gba")) {
		t.Errorf("409 without the game sharing the data: %s", r.Body)
	}
	f.importArchive(id, map[string]any{"systemId": library.EncodeID("gba"), "folderId": library.EncodeID("gba/Hacks")}, http.StatusConflict)
	if f.read("saves/gba/Starlight Courier.srm") == "s" {
		t.Error("the other game's save was replaced")
	}
}

// TestKnulliThumbnailsAndCheats: the thumbnail next to a Knulli state is
// exported and listed; a NextUI cheat file is skipped on Knulli, the rest
// imported.
func TestKnulliThumbnailsAndCheats(t *testing.T) {
	f := newKnulliFixture(t)
	game := "gba/Starlight Courier.gba"
	os.WriteFile(filepath.Join(f.sd, "saves/gba/Starlight Courier.state1.png"), png1x1, 0o644)
	arch, _ := f.export(game, false)
	if !roles(arch)["state-thumbnail/1"] {
		t.Errorf("Knulli thumbnail not exported: %v", roles(arch))
	}
	var l struct {
		States []struct {
			Slot      int `json:"slot"`
			Thumbnail *struct {
				SHA256 string `json:"sha256"`
			} `json:"thumbnail"`
		} `json:"states"`
	}
	f.get("/api/v1/states", "/states", http.StatusOK).JSON(t, &l)
	found := false
	for _, s := range l.States {
		if s.Slot == 1 && s.Thumbnail != nil && s.Thumbnail.SHA256 == sha([]byte(f.read("saves/gba/Starlight Courier.state1.png"))) {
			found = true
		}
	}
	if !found {
		t.Errorf("thumbnail missing from GET /states: %+v", l.States)
	}

	cht := []byte("cheats = 1\n\ncheat0_desc = \"Money\"\ncheat0_code = \"1234\"\ncheat0_enable = false\n")
	data := archiveOf(t, knulliSource("Starlight Courier.gba"), map[string][]byte{"save": []byte("new save"), "cheats": cht}, nil)
	id := f.stageFile("c.reminislink.zip", data)
	plan := f.inspect(id, url.Values{"gameId": {library.EncodeID(game)}}, http.StatusOK)
	if acts := actions(plan); acts["cheats"] != "skip:unsupported-format" || acts["save"] != "replace" {
		t.Errorf("plan %v", acts)
	}
	var accepted struct{ JobID string }
	f.importArchive(id, map[string]any{"gameId": library.EncodeID(game)}, http.StatusAccepted).JSON(t, &accepted)
	if j := f.waitJob(accepted.JobID); j.State != "succeeded" || f.read("saves/gba/Starlight Courier.srm") != "new save" {
		t.Fatalf("job %+v", j)
	}
}

// TestNewGameOverAnOrphanSave: a save left by a deleted ROM is replaced
// by the import of a new game, backed up first.
func TestNewGameOverAnOrphanSave(t *testing.T) {
	f := newFixture(t)
	os.WriteFile(filepath.Join(f.sd, "Saves/GBA/Orphan.gba.sav"), []byte("old save"), 0o644)
	src := starlightSource()
	src.GameName, src.FileName, src.RomName = "Orphan", "Orphan.gba", "Orphan.gba"
	id := f.stageFile("o.reminislink.zip", archiveOf(t, src, map[string][]byte{"rom": []byte("rom"), "save": []byte("new save")}, nil))
	var accepted struct{ JobID string }
	f.importArchive(id, map[string]any{"systemId": library.EncodeID(gbaFolder)}, http.StatusAccepted).JSON(t, &accepted)
	j := f.waitJob(accepted.JobID)
	if j.State != "succeeded" || j.Result["saveBackupId"] == nil {
		t.Fatalf("job %+v", j)
	}
	if f.read("Saves/GBA/Orphan.gba.sav") != "new save" {
		t.Error("save not imported")
	}
}

// TestCancelledQueuedImportReleasesItsArchive: an import cancelled while
// queued releases its archive (DELETE answers 204, not 409).
func TestCancelledQueuedImportReleasesItsArchive(t *testing.T) {
	f := newFixture(t)
	_, data := f.export(starlight, false)
	first := f.stageFile("q1.reminislink.zip", data)
	second := f.stageFile("q2.reminislink.zip", data)
	// The first import runs and waits for the write lock, held here; the
	// second waits in the queue.
	if !f.xfer.Writes().TryAcquire() {
		t.Fatal("write lock busy")
	}
	released := false
	release := func() {
		if !released {
			released = true
			f.xfer.Writes().Release()
		}
	}
	defer release()
	target := map[string]any{"gameId": library.EncodeID(starlight)}
	var a, b struct{ JobID string }
	f.importArchive(first, target, http.StatusAccepted).JSON(t, &a)
	f.importArchive(second, target, http.StatusAccepted).JSON(t, &b)
	f.do(http.MethodDelete, "/api/v1/archives/"+second, inspectPath, nil, http.StatusConflict)
	f.do(http.MethodDelete, "/api/v1/jobs/"+b.JobID, "/jobs/{jobId}", nil, http.StatusOK)
	deadline := time.Now().Add(5 * time.Second)
	for {
		r := f.client.Do(t, http.MethodDelete, "/api/v1/archives/"+second, nil)
		if r.Status == http.StatusNoContent {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the cancelled queued import still holds its archive: %d %s", r.Status, r.Body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	release()
	if j := f.waitJob(a.JobID); j.State != "succeeded" {
		t.Fatalf("first import %+v", j)
	}
	_ = json.Marshal
	_ = bytes.NewReader
}

// TestOneStateFilePerSlot: slot 1 is listed exactly once, with the
// SHA-256 of the bytes the slot route serves, whatever the case of the
// suffix (exFAT finds ".ST1" as ".st1").
func TestOneStateFilePerSlot(t *testing.T) {
	f := newFixture(t)
	st1 := filepath.Join(f.sd, starlightSt1)
	upper := strings.TrimSuffix(st1, ".st1") + ".ST1"
	if err := os.Rename(st1, upper); err != nil {
		t.Fatal(err)
	}
	var l struct {
		States []struct {
			GameID string `json:"gameId"`
			Slot   int    `json:"slot"`
			SHA256 string `json:"sha256"`
		} `json:"states"`
	}
	f.get("/api/v1/states", "/states", http.StatusOK).JSON(t, &l)
	var listed []string
	for _, s := range l.States {
		if s.GameID == library.EncodeID(starlight) && s.Slot == 1 {
			listed = append(listed, s.SHA256)
		}
	}
	r := f.client.Do(t, http.MethodGet, gamePath(starlight)+"/states/1", nil)
	switch {
	case r.Status == http.StatusOK:
		if len(listed) != 1 || listed[0] != sha(r.Body) {
			t.Errorf("slot 1 listed %v, served %s", listed, sha(r.Body))
		}
	case len(listed) != 0:
		t.Errorf("slot 1 listed %v but not served (%d)", listed, r.Status)
	}
	if _, err := os.Stat(st1); err == nil && len(listed) != 1 {
		t.Error("on a file system that ignores case, slot 1 must be listed once")
	}
}

// TestKnulliTargetsFollowTheFileSystemCase: on ext4, Starlight Courier.gba
// and starlight courier.gba are two games with their own data: the
// archive of the second goes to the second, and a new "starlight
// courier.gba" in another folder is no conflict with the first.
func TestKnulliTargetsFollowTheFileSystemCase(t *testing.T) {
	f := newKnulliFixtureIn(t, testutil.CaseSensitiveDir(t))
	lower := filepath.Join(f.sd, "roms/gba/starlight courier.gba")
	os.WriteFile(lower, []byte("another rom"), 0o644)
	data := archiveOf(t, knulliSource("starlight courier.gba"), map[string][]byte{"save": []byte("lower save")}, nil)
	id := f.stageFile("l.reminislink.zip", data)
	plan := f.inspect(id, nil, http.StatusOK)
	if plan.Target == nil || plan.Target.GameID == nil || *plan.Target.GameID != library.EncodeID("gba/starlight courier.gba") {
		t.Fatalf("suggested %+v", plan.Target)
	}
	os.Remove(lower)
	withROM := archiveOf(t, knulliSource("starlight courier.gba"), map[string][]byte{"rom": []byte("rom"), "save": []byte("s")}, nil)
	id = f.stageFile("m.reminislink.zip", withROM)
	f.inspect(id, url.Values{"systemId": {library.EncodeID("gba")}, "folderId": {library.EncodeID("gba/Hacks")}}, http.StatusOK)
}

// TestNFDROMIsNeverOverwritten (M19 final review): on APFS a ROM named in
// NFD is the same entry as its NFC spelling: a new game of that name is a
// 409, and the ROM is never overwritten.
func TestNFDROMIsNeverOverwritten(t *testing.T) {
	f := newFixture(t)
	nfc := norm.NFC.String("Café Rêverie (France).gba")
	dir := filepath.Join(f.sd, "Roms", gbaFolder)
	os.Rename(filepath.Join(dir, nfc), filepath.Join(dir, norm.NFD.String(nfc)))
	if _, err := os.Lstat(filepath.Join(dir, nfc)); err != nil {
		t.Skip("this file system tells NFC from NFD")
	}
	before, _ := os.ReadFile(filepath.Join(dir, nfc))
	src := starlightSource()
	src.GameName, src.FileName, src.RomName = "Café Rêverie (France)", nfc, nfc
	id := f.stageFile("n.reminislink.zip", archiveOf(t, src, map[string][]byte{"rom": []byte("other rom")}, nil))
	f.importArchive(id, map[string]any{"systemId": library.EncodeID(gbaFolder)}, http.StatusConflict)
	if after, _ := os.ReadFile(filepath.Join(dir, nfc)); string(after) != string(before) {
		t.Fatal("the NFD ROM was overwritten")
	}
}

// TestNewGameDataConflictsBeyondTheArchive (M19 final review): the 409
// covers every file the new game would use, not only those the archive
// holds, in every system sharing the data folders.
func TestNewGameDataConflictsBeyondTheArchive(t *testing.T) {
	// Knulli: a ROM-only archive whose name without extension is the one
	// of a game with a save.
	k := newKnulliFixture(t)
	id := k.stageFile("r.reminislink.zip", archiveOf(t, knulliSource("Starlight Courier.zip"), map[string][]byte{"rom": []byte("rom")}, nil))
	r := k.do(http.MethodGet, "/api/v1/archives/"+id+"?systemId="+url.QueryEscape(library.EncodeID("gba"))+"&folderId="+url.QueryEscape(library.EncodeID("gba/Hacks")), inspectPath, nil, http.StatusConflict)
	if !strings.Contains(string(r.Body), library.EncodeID("gba/Starlight Courier.gba")) {
		t.Errorf("Knulli ROM-only: %s", r.Body)
	}

	// NextUI: a second system folder with the same tag shares Saves/GBA.
	f := newFixture(t)
	other := "GBA Extra (GBA)"
	if err := os.MkdirAll(filepath.Join(f.sd, "Roms", other), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(f.sd, "Roms", other, "placeholder.gba"), []byte("x"), 0o644)
	id = f.stageFile("s.reminislink.zip", archiveOf(t, starlightSource(), map[string][]byte{"rom": []byte("rom")}, nil))
	r = f.do(http.MethodGet, "/api/v1/archives/"+id+"?systemId="+url.QueryEscape(library.EncodeID(other)), inspectPath, nil, http.StatusConflict)
	if !strings.Contains(string(r.Body), library.EncodeID(starlight)) {
		t.Errorf("NextUI same tag: %s", r.Body)
	}
}

// TestKnulliStatePathsOnExt4 (M19 final review): two games differing by
// case have their own states; each listed path is the file hashed.
func TestKnulliStatePathsOnExt4(t *testing.T) {
	f := newKnulliFixtureIn(t, testutil.CaseSensitiveDir(t))
	os.WriteFile(filepath.Join(f.sd, "roms/gba/starlight courier.gba"), []byte("another rom"), 0o644)
	os.WriteFile(filepath.Join(f.sd, "saves/gba/starlight courier.state1"), []byte("lower state"), 0o644)
	var l struct {
		States []struct {
			GameID string `json:"gameId"`
			Slot   int    `json:"slot"`
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"states"`
	}
	f.get("/api/v1/states", "/states", http.StatusOK).JSON(t, &l)
	n := 0
	for _, s := range l.States {
		if s.Slot != 1 {
			continue
		}
		n++
		if s.SHA256 != sha([]byte(f.read(s.Path))) {
			t.Errorf("%s: path %s is not the file hashed", s.GameID, s.Path)
		}
	}
	if n != 2 {
		t.Errorf("%d slot-1 states, want 2: %+v", n, l.States)
	}
}
