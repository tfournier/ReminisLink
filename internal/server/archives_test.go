package server

import (
	"archive/zip"
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
	"github.com/tfournier/ReminisLink/internal/transfer"
)

// starlight is declared with the trash tests (M18), same value.
const (
	archivePath   = "/games/{gameId}/archive"
	inspectPath   = "/archives/{stagingId}"
	importPath    = "/archives/{stagingId}/import"
	starlightSave = "Saves/GBA/Starlight Courier.gba.sav"
	starlightSt0  = ".userdata/shared/GBA-gpsp/Starlight Courier.gba.st0"
	starlightSt1  = ".userdata/shared/GBA-gpsp/Starlight Courier.gba.st1"
	starlightTh0  = ".userdata/shared/.minui/GBA/Starlight Courier.gba.0.bmp"
	starlightCht  = "Cheats/GBA/Starlight Courier.gba.cht"
	starlightArt  = "Roms/" + gbaFolder + "/.media/Starlight Courier.png"
	starlightROM  = "Roms/" + starlight
)

// export downloads the archive of a game and checks it.
func (f *fixture) export(game string, rom bool) (*gamearchive.Archive, []byte) {
	f.t.Helper()
	target := gamePath(game) + "/archive"
	if rom {
		target += "?rom=true"
	}
	r := f.get(target, archivePath, http.StatusOK)
	if ct := r.Header.Get("Content-Type"); ct != "application/zip" {
		f.t.Fatalf("Content-Type %q", ct)
	}
	if cd := r.Header.Get("Content-Disposition"); !strings.Contains(cd, gamearchive.FileSuffix) {
		f.t.Errorf("Content-Disposition %q", cd)
	}
	a, err := gamearchive.Open(bytes.NewReader(r.Body), int64(len(r.Body)))
	if err != nil {
		f.t.Fatal(err)
	}
	return a, r.Body
}

func (f *fixture) inspect(stagingID string, query url.Values, want int) transfer.ArchivePlan {
	f.t.Helper()
	target := "/api/v1/archives/" + stagingID
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var plan transfer.ArchivePlan
	r := f.get(target, inspectPath, want)
	if want == http.StatusOK {
		r.JSON(f.t, &plan)
	}
	return plan
}

func (f *fixture) importArchive(stagingID string, target map[string]any, want int) testutil.Response {
	f.t.Helper()
	return f.do(http.MethodPost, "/api/v1/archives/"+stagingID+"/import", importPath, mustJSON(f.t, map[string]any{"target": target}), want)
}

func roles(a *gamearchive.Archive) map[string]bool {
	out := map[string]bool{}
	for _, e := range a.Entries {
		out[e.Key()] = true
	}
	return out
}

func actions(p transfer.ArchivePlan) map[string]string {
	out := map[string]string{}
	for _, e := range p.Entries {
		k := e.Role
		if e.Slot != nil {
			k += "/" + string(rune('0'+*e.Slot))
		}
		v := e.Action
		if e.Reason != nil {
			v += ":" + *e.Reason
		}
		out[k] = v
	}
	return out
}

// TestArchiveMovesAGameBetweenConsoles: a game exported with its ROM from
// one console is imported on another one that does not have it; every
// file lands where the second console expects it, byte for byte.
func TestArchiveMovesAGameBetweenConsoles(t *testing.T) {
	a := newFixture(t)
	arch, data := a.export(starlight, true)
	want := []string{"rom", "save", "state/0", "state/1", "state/9", "state-thumbnail/0", "cheats", "art"}
	got := roles(arch)
	for _, k := range want {
		if !got[k] {
			t.Errorf("archive lacks %s: %v", k, got)
		}
	}
	if arch.Manifest.Source.SystemTag != "GBA" || arch.Manifest.Source.RomName != "Starlight Courier.gba" {
		t.Errorf("source %+v", arch.Manifest.Source)
	}

	b := newFixture(t)
	originals := map[string]string{}
	for _, rel := range []string{starlightROM, starlightSave, starlightSt0, starlightSt1, starlightTh0, starlightCht, starlightArt} {
		originals[rel] = a.read(rel)
		if err := os.Remove(filepath.Join(b.sd, filepath.FromSlash(rel))); err != nil {
			t.Fatal(err)
		}
	}
	os.Remove(filepath.Join(b.sd, ".userdata/shared/GBA-gpsp/Starlight Courier.gba.st9"))
	stagingID := b.stageFile("Starlight Courier.reminislink.zip", data)

	plan := b.inspect(stagingID, nil, http.StatusOK)
	if !plan.Suggested || plan.Target == nil || plan.Target.SystemID == nil || *plan.Target.SystemID != library.EncodeID(gbaFolder) {
		t.Fatalf("suggested target %+v", plan.Target)
	}
	if acts := actions(plan); acts["rom"] != "create" || acts["save"] != "create" || acts["state/0"] != "create" {
		t.Errorf("plan %v", acts)
	}

	var accepted struct{ JobID string }
	b.importArchive(stagingID, map[string]any{"systemId": *plan.Target.SystemID}, http.StatusAccepted).JSON(t, &accepted)
	j := b.waitJob(accepted.JobID)
	if j.State != "succeeded" || j.Kind != transfer.JobArchiveImport {
		t.Fatalf("job %+v", j)
	}
	if j.Result["gameId"] != library.EncodeID(starlight) || j.Result["trashEntryId"] != nil {
		t.Errorf("result %+v", j.Result)
	}
	for rel, content := range originals {
		if got := b.read(rel); got != content {
			t.Errorf("%s differs after the import", rel)
		}
	}
	// The staging entry is gone; the game is listed.
	b.inspect(stagingID, nil, http.StatusNotFound)
	b.detail(starlight)
}

// TestArchiveImportBacksUpWhatItReplaces: importing into an existing game
// backs up its save, moves every replaced file to one trash entry, and
// never touches a locked state.
func TestArchiveImportBacksUpWhatItReplaces(t *testing.T) {
	f := newFixture(t)
	_, data := f.export(starlight, false)
	saveBefore, st0Before, st1Before := f.read(starlightSave), f.read(starlightSt0), f.read(starlightSt1)
	// The game changes on the console after the export.
	os.WriteFile(filepath.Join(f.sd, starlightSave), []byte("newer save"), 0o644)
	os.WriteFile(filepath.Join(f.sd, starlightSt0), []byte("newer state"), 0o644)
	os.WriteFile(filepath.Join(f.sd, starlightSt1), []byte("locked state"), 0o644)
	f.do(http.MethodPut, gamePath(starlight)+"/states/1/lock", "/games/{gameId}/states/{slot}/lock", nil, http.StatusOK)

	stagingID := f.stageFile("a.zip", data)
	gameID := library.EncodeID(starlight)
	plan := f.inspect(stagingID, url.Values{"gameId": {gameID}}, http.StatusOK)
	acts := actions(plan)
	if plan.Suggested || acts["save"] != "replace" || acts["state/1"] != "skip:locked" || acts["state/0"] != "replace" {
		t.Errorf("plan %v", acts)
	}
	// Without a target, the same game is suggested.
	if p := f.inspect(stagingID, nil, http.StatusOK); p.Target == nil || p.Target.GameID == nil || *p.Target.GameID != gameID {
		t.Errorf("suggested %+v", p.Target)
	}

	var accepted struct{ JobID string }
	f.importArchive(stagingID, map[string]any{"gameId": gameID}, http.StatusAccepted).JSON(t, &accepted)
	j := f.waitJob(accepted.JobID)
	if j.State != "succeeded" {
		t.Fatalf("job %+v", j)
	}
	if f.read(starlightSave) != saveBefore || f.read(starlightSt0) != st0Before {
		t.Error("save or state 0 not imported")
	}
	if f.read(starlightSt1) != "locked state" || st1Before == "locked state" {
		t.Error("a locked state was replaced")
	}
	skipped, _ := json.Marshal(j.Result["skipped"])
	if !strings.Contains(string(skipped), `"locked"`) {
		t.Errorf("skipped %s", skipped)
	}

	// The save backup (before-import) holds the newer save.
	var backups struct {
		Backups []struct {
			ID, Reason string
		}
	}
	f.get(gamePath(starlight)+"/save/backups", "/games/{gameId}/save/backups", http.StatusOK).JSON(t, &backups)
	if len(backups.Backups) == 0 || backups.Backups[0].Reason != "before-import" || backups.Backups[0].ID != j.Result["saveBackupId"] {
		t.Errorf("backups %+v, result %+v", backups, j.Result)
	}
	// One trash entry with the replaced files.
	var trash struct {
		Entries []struct {
			ID, Reason string
			Files      []string
		}
	}
	f.get("/api/v1/trash", "/trash", http.StatusOK).JSON(t, &trash)
	var entry *struct {
		ID, Reason string
		Files      []string
	}
	for i := range trash.Entries {
		if trash.Entries[i].ID == j.Result["trashEntryId"] {
			entry = &trash.Entries[i]
		}
	}
	if entry == nil || entry.Reason != "import-archive" {
		t.Fatalf("trash %+v, result %+v", trash, j.Result)
	}
	joined := strings.Join(entry.Files, "|")
	for _, rel := range []string{starlightSave, starlightSt0, starlightTh0} {
		if !strings.Contains(joined, rel) {
			t.Errorf("trash entry lacks %s: %v", rel, entry.Files)
		}
	}
	if strings.Contains(joined, starlightSt1) {
		t.Error("the locked state went to the trash")
	}
}

func TestArchiveErrors(t *testing.T) {
	f := newFixture(t)
	_, data := f.export(starlight, true)
	stagingID := f.stageFile("a.zip", data)
	gameID, systemID := library.EncodeID(starlight), library.EncodeID(gbaFolder)

	// A new game whose ROM is taken: 409 with the game in its place.
	r := f.do(http.MethodGet, "/api/v1/archives/"+stagingID+"?systemId="+systemID, inspectPath, nil, http.StatusConflict)
	if !strings.Contains(string(r.Body), gameID) {
		t.Errorf("409 without the existing game: %s", r.Body)
	}
	f.importArchive(stagingID, map[string]any{"systemId": systemID}, http.StatusConflict)
	f.importArchive(stagingID, map[string]any{"systemId": systemID, "gameId": gameID}, http.StatusBadRequest)
	f.importArchive(stagingID, map[string]any{}, http.StatusBadRequest)
	f.inspect(stagingID, url.Values{"folderId": {systemID}}, http.StatusBadRequest)
	f.inspect(stagingID, url.Values{"gameId": {library.EncodeID(gbaFolder + "/Nope.gba")}}, http.StatusNotFound)
	f.inspect("st_20261121T100000Z_0a1b2c3d", nil, http.StatusNotFound)
	f.get(gamePath(starlight)+"/archive?rom=maybe", archivePath, http.StatusBadRequest)
	f.get(gamePath(gbaFolder+"/Nope.gba")+"/archive", archivePath, http.StatusNotFound)

	// A hostile archive is refused by the preview and the import.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("../../Saves/GBA/evil.sav")
	w.Write([]byte("x"))
	zw.Close()
	evil := f.stageFile("evil.zip", buf.Bytes())
	f.inspect(evil, nil, http.StatusUnprocessableEntity)
	f.importArchive(evil, map[string]any{"gameId": gameID}, http.StatusUnprocessableEntity)
	if f.exists("Saves/GBA/evil.sav") {
		t.Error("a hostile entry was written")
	}

	// An archive without ROM cannot make a new game.
	_, noROM := f.export(starlight, false)
	id := f.stageFile("b.zip", noROM)
	f.importArchive(id, map[string]any{"systemId": systemID}, http.StatusUnprocessableEntity)
}

func TestListAllStatesRoute(t *testing.T) {
	f := newFixture(t)
	var l struct {
		States []struct {
			GameID    string `json:"gameId"`
			Slot      int    `json:"slot"`
			SHA256    string `json:"sha256"`
			Thumbnail *struct {
				SHA256 string `json:"sha256"`
			} `json:"thumbnail"`
		} `json:"states"`
	}
	f.get("/api/v1/states", "/states", http.StatusOK).JSON(t, &l)
	n := 0
	for _, s := range l.States {
		if s.GameID == library.EncodeID(starlight) {
			n++
			if s.Slot == 0 && (s.Thumbnail == nil || s.SHA256 != sha([]byte(f.read(starlightSt0)))) {
				t.Errorf("slot 0: %+v", s)
			}
		}
	}
	if n != 3 {
		t.Errorf("%d states of Starlight Courier, want 3: %+v", n, l.States)
	}
}

// TestArchiveExportLeavesOutWhatAnImportRefuses: a file every importer
// would refuse (art that is not a PNG) is left out of the archive, which
// stays importable.
func TestArchiveExportLeavesOutWhatAnImportRefuses(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(filepath.Join(f.sd, starlightArt), []byte("GIF89a not a png"), 0o644); err != nil {
		t.Fatal(err)
	}
	arch, _ := f.export(starlight, false)
	got := roles(arch)
	if got["art"] || !got["save"] || !got["state/0"] {
		t.Errorf("roles %v", got)
	}
}

// archiveOf builds a game archive with the writer of the pak.
func archiveOf(t *testing.T, src gamearchive.Source, files map[string][]byte, slots map[string]int) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gamearchive.NewWriter(&buf)
	for role, data := range files {
		var slot *int
		if s, ok := slots[role]; ok {
			slot = &s
		}
		r := role
		if strings.HasPrefix(role, "state") {
			r = strings.SplitN(role, "#", 2)[0]
		}
		if _, err := w.Add(r, slot, "x", time.Now(), bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(src, time.Now()); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func starlightSource() gamearchive.Source {
	return gamearchive.Source{OS: "nextui", SystemTag: "GBA", SystemLabel: "Game Boy Advance",
		GameName: "Starlight Courier", FileName: "Starlight Courier.gba", RomName: "Starlight Courier.gba", Kind: "file"}
}

// TestArchiveReviewFixes covers the Codex review of M19: the clock file
// alone is backed up with its save, a new game never takes over the data
// of a game of another folder, an abandoned archive can be deleted, a
// game with nothing importable answers 404.
func TestArchiveReviewFixes(t *testing.T) {
	f := newFixture(t)
	gameID := library.EncodeID(starlight)
	rtcPath := "Saves/GBA/Starlight Courier.gba.rtc"
	os.WriteFile(filepath.Join(f.sd, rtcPath), []byte("old clock"), 0o644)

	// Only the clock file: the save/clock pair is backed up first.
	id := f.stageFile("a.reminislink.zip", archiveOf(t, starlightSource(), map[string][]byte{"rtc": []byte("new clock")}, nil))
	var accepted struct{ JobID string }
	f.importArchive(id, map[string]any{"gameId": gameID}, http.StatusAccepted).JSON(t, &accepted)
	j := f.waitJob(accepted.JobID)
	if j.State != "succeeded" || j.Result["saveBackupId"] == nil {
		t.Fatalf("clock only: %+v", j)
	}
	if f.read(rtcPath) != "new clock" {
		t.Error("clock not imported")
	}

	// A new game in another folder whose ROM name is the one of an
	// existing game: 409 with that game (they would share their data).
	rom := []byte("rom bytes")
	data := archiveOf(t, starlightSource(), map[string][]byte{"rom": rom, "save": []byte("s")}, nil)
	id = f.stageFile("b.reminislink.zip", data)
	hacks := library.EncodeID(gbaFolder + "/Hacks")
	r := f.do(http.MethodGet, "/api/v1/archives/"+id+"?systemId="+url.QueryEscape(library.EncodeID(gbaFolder))+"&folderId="+url.QueryEscape(hacks), inspectPath, nil, http.StatusConflict)
	if !strings.Contains(string(r.Body), gameID) {
		t.Errorf("409 without the game sharing the data: %s", r.Body)
	}

	// The abandoned archive is deleted; a second delete is 404.
	f.do(http.MethodDelete, "/api/v1/archives/"+id, inspectPath, nil, http.StatusNoContent)
	f.do(http.MethodDelete, "/api/v1/archives/"+id, inspectPath, nil, http.StatusNotFound)
	f.inspect(id, nil, http.StatusNotFound)

	// Nothing importable (only an art that is not a PNG): 404, before any
	// archive byte.
	lantern := gbaFolder + "/Lantern Keeper [b1].gba"
	os.WriteFile(filepath.Join(f.sd, "Roms", gbaFolder, ".media", "Lantern Keeper [b1].png"), []byte("GIF89a"), 0o644)
	f.get(gamePath(lantern)+"/archive", archivePath, http.StatusNotFound)
}

// TestArchiveImportNeverWritesThroughALink: a folder of a destination
// that is a link out of the card (desktop pak) is never followed.
func TestArchiveImportNeverWritesThroughALink(t *testing.T) {
	f := newFixture(t)
	outside := filepath.Join(t.TempDir(), "elsewhere")
	media := filepath.Join(f.sd, "Roms", gbaFolder, ".media")
	os.RemoveAll(media)
	if err := os.Symlink(outside, media); err != nil {
		t.Skip("no symbolic links here")
	}
	id := f.stageFile("c.reminislink.zip", archiveOf(t, starlightSource(), map[string][]byte{"art": png1x1}, nil))
	var accepted struct{ JobID string }
	f.importArchive(id, map[string]any{"gameId": library.EncodeID(starlight)}, http.StatusAccepted).JSON(t, &accepted)
	if j := f.waitJob(accepted.JobID); j.State == "succeeded" {
		t.Errorf("imported through a link: %+v", j)
	}
	if _, err := os.Lstat(outside); err == nil {
		t.Error("a folder was made outside the card")
	}
}

// TestStateSlotNamesAreCanonical: ".st01" is not slot 1, so the listing
// announces the SHA-256 of the file the download serves.
func TestStateSlotNamesAreCanonical(t *testing.T) {
	f := newFixture(t)
	os.WriteFile(filepath.Join(f.sd, ".userdata/shared/GBA-gpsp/Starlight Courier.gba.st01"), []byte("not slot 1"), 0o644)
	var l struct {
		States []struct {
			GameID string `json:"gameId"`
			Slot   int    `json:"slot"`
			SHA256 string `json:"sha256"`
		} `json:"states"`
	}
	f.get("/api/v1/states", "/states", http.StatusOK).JSON(t, &l)
	for _, s := range l.States {
		if s.GameID == library.EncodeID(starlight) && s.Slot == 1 && s.SHA256 != sha([]byte(f.read(starlightSt1))) {
			t.Error("slot 1 hashed from another file than .st1")
		}
	}
	arch, _ := f.export(starlight, false)
	if !roles(arch)["state/1"] {
		t.Error("state 1 missing")
	}
}

// png1x1 starts with the PNG signature (what an import checks).
var png1x1 = append([]byte("\x89PNG\r\n\x1a\n"), "IHDR"...)
