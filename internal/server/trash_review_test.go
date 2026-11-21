package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfournier/ReminisLink/internal/store"
)

// Codex review of M18, round 1, over HTTP: bodies, no free name, and the
// "replace" rule.

type replacePreview struct {
	Conflict            bool     `json:"conflict"`
	FreeNameUnavailable *string  `json:"freeNameUnavailable"`
	Replaceable         bool     `json:"replaceable"`
	ReplaceDisplaced    []string `json:"replaceDisplaced"`
	ReplaceKeptAs       *string  `json:"replaceKeptAs"`
	Items               []struct {
		Name        string  `json:"name"`
		FreeName    *string `json:"freeName"`
		ReplaceName *string `json:"replaceName"`
	} `json:"items"`
}

type replaceResult struct {
	Restored        []string `json:"restored"`
	ReplacedEntryID *string  `json:"replacedEntryId"`
	Replaced        []string `json:"replaced"`
	ReplacedKeptAs  *string  `json:"replacedKeptAs"`
}

func TestRestoreRefusesInvalidBodies(t *testing.T) {
	f := newFixture(t)
	id := f.deleteStarlight()
	for _, body := range []string{`null`, `{"onConflict":null}`, `[]`, `{"onConflict":"overwrite"}`, `{"onConflict":1}`} {
		f.do(http.MethodPost, f.restoreURL(id), restorePath, []byte(body), http.StatusBadRequest)
	}
	if !f.exists("Roms/"+starlight) && len(f.preview(id).Items) == 0 {
		t.Fatal("entry changed")
	}
	f.do(http.MethodPost, f.restoreURL(id), restorePath, []byte(`{}`), http.StatusOK)
}

func TestRestoreWithoutAFreeNameOverHTTP(t *testing.T) {
	f := newFixture(t)
	f.writeSD("Saves/GBA/Lone.gba.sav", "lone")
	moved, err := f.trash.Move(store.ReasonOverwrite, "Lone", []store.MoveItem{{Path: filepath.Join(f.sd, "Saves", "GBA", "Lone.gba.sav")}})
	if err != nil {
		t.Fatal(err)
	}
	// A file where the folder of the save was.
	if err := removeAll(filepath.Join(f.sd, "Saves", "GBA")); err != nil {
		t.Fatal(err)
	}
	f.writeSD("Saves/GBA", "a file")
	var p replacePreview
	f.get(f.restoreURL(moved.Entry.ID), restorePath, http.StatusOK).JSON(t, &p)
	if !p.Conflict || p.FreeNameUnavailable == nil || *p.FreeNameUnavailable != "path-blocked" || p.Items[0].FreeName != nil {
		t.Fatalf("preview %+v", p)
	}
	r := f.do(http.MethodPost, f.restoreURL(moved.Entry.ID), restorePath, []byte(`{"onConflict":"rename"}`), http.StatusConflict)
	if !strings.Contains(string(r.Body), `"freeNameUnavailable":"path-blocked"`) {
		t.Fatalf("409 %s", r.Body)
	}
}

func TestRestoreReplacesTheCurrentSaveOverHTTP(t *testing.T) {
	f := newFixture(t)
	// A save of the save folder in the trash (an older pak trashed the
	// save it replaced), then a current save and clock of another case.
	f.writeSD("Saves/GBA/Starlight Courier.gba.sav", "older")
	moved, err := f.trash.Move(store.ReasonOverwrite, "Starlight Courier", []store.MoveItem{{Path: filepath.Join(f.sd, "Saves", "GBA", "Starlight Courier.gba.sav")}})
	if err != nil {
		t.Fatal(err)
	}
	f.writeSD("Saves/GBA/STARLIGHT COURIER.gba.sav", "current")
	f.writeSD("Saves/GBA/STARLIGHT COURIER.gba.rtc", "current clock")
	var p replacePreview
	f.get(f.restoreURL(moved.Entry.ID), restorePath, http.StatusOK).JSON(t, &p)
	if !p.Replaceable || p.ReplaceKeptAs == nil || *p.ReplaceKeptAs != "trash" || len(p.ReplaceDisplaced) != 2 ||
		p.Items[0].ReplaceName == nil || *p.Items[0].ReplaceName != "Saves/GBA/STARLIGHT COURIER.gba.sav" {
		t.Fatalf("preview %+v", p)
	}
	var res replaceResult
	f.do(http.MethodPost, f.restoreURL(moved.Entry.ID), restorePath, []byte(`{"onConflict":"replace"}`), http.StatusOK).JSON(t, &res)
	if f.read("Saves/GBA/STARLIGHT COURIER.gba.sav") != "older" || f.exists("Saves/GBA/STARLIGHT COURIER.gba.rtc") {
		t.Fatal("save not replaced")
	}
	if res.ReplacedEntryID == nil || len(res.Replaced) != 2 || *res.ReplacedKeptAs != "trash" {
		t.Fatalf("result %+v", res)
	}
	entries, _, _ := f.trash.List()
	if len(entries) != 1 || entries[0].ID != *res.ReplacedEntryID || entries[0].Reason != store.ReasonRestore {
		t.Fatalf("trash %+v", entries)
	}
	dir := filepath.Join(f.trash.Dir(), *res.ReplacedEntryID)
	if readFile(t, filepath.Join(dir, "Saves", "GBA", "STARLIGHT COURIER.gba.sav")) != "current" ||
		readFile(t, filepath.Join(dir, "Saves", "GBA", "STARLIGHT COURIER.gba.rtc")) != "current clock" {
		t.Fatal("current save not in the trash")
	}
	// The save of the game is the restored one.
	if r := f.get(gamePath(starlight)+"/save", "/games/{gameId}/save", http.StatusOK); string(r.Body) != "older" {
		t.Fatalf("download %q", r.Body)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func removeAll(p string) error { return os.RemoveAll(p) }

func TestRestoreReplacesWithABackupCopyOverHTTP(t *testing.T) {
	f := newFixture(t)
	g := gamePath(starlight)
	old := f.read("Saves/GBA/Starlight Courier.gba.sav")
	var put struct {
		Backup *backupJSON `json:"backup"`
	}
	f.do(http.MethodPut, g+"/save", "/games/{gameId}/save", []byte("new save"), http.StatusOK).JSON(t, &put)
	f.do(http.MethodDelete, g+"/save/backups/"+put.Backup.ID, "/games/{gameId}/save/backups/{backupId}", nil, http.StatusNoContent)
	entries, _, _ := f.trash.List()
	var p replacePreview
	f.get(f.restoreURL(entries[0].ID), restorePath, http.StatusOK).JSON(t, &p)
	if !p.Replaceable || p.ReplaceKeptAs == nil || *p.ReplaceKeptAs != "backup" || len(p.ReplaceDisplaced) != 1 {
		t.Fatalf("preview %+v", p)
	}
	var res replaceResult
	f.do(http.MethodPost, f.restoreURL(entries[0].ID), restorePath, []byte(`{"onConflict":"replace"}`), http.StatusOK).JSON(t, &res)
	if f.read("Saves/GBA/Starlight Courier.gba.sav") != old {
		t.Fatal("save not replaced by the copy")
	}
	if *res.ReplacedKeptAs != "backup" || len(res.Restored) != 1 || res.Restored[0] != "Saves/GBA/Starlight Courier.gba.sav" {
		t.Fatalf("result %+v", res)
	}
	var list struct {
		Backups []backupJSON `json:"backups"`
	}
	f.get(g+"/save/backups", "/games/{gameId}/save/backups", http.StatusOK).JSON(t, &list)
	found := false
	for _, b := range list.Backups {
		found = found || b.Reason == "before-restore"
	}
	if !found {
		t.Fatalf("current save not kept as a backup copy: %+v", list)
	}
	if entries, _, _ := f.trash.List(); len(entries) != 0 {
		t.Fatalf("trash %+v", entries)
	}
}

func TestReplaceIsRefusedForAGameOverHTTP(t *testing.T) {
	f := newFixture(t)
	id := f.deleteStarlight()
	r := f.do(http.MethodPost, f.restoreURL(id), restorePath, []byte(`{"onConflict":"replace"}`), http.StatusUnprocessableEntity)
	var body struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(r.Body, &body); err != nil || body.Error.Code != "unsupported_format" || body.Error.Details["reason"] != "not-a-save" {
		t.Fatalf("422 %s", r.Body)
	}
	if f.exists("Roms/" + starlight) {
		t.Fatal("restored")
	}
}

// A game restored under a free name is read again by the library with its
// save (of another case), its locked state and its files.
func TestRenamedGameIsReadAgainWithItsData(t *testing.T) {
	f := newFixture(t)
	g := gamePath(starlight)
	f.do(http.MethodPut, g+"/states/0/lock", "/games/{gameId}/states/{slot}/lock", nil, http.StatusOK)
	save := f.read("Saves/GBA/Starlight Courier.gba.sav")
	if err := os.Rename(filepath.Join(f.sd, "Saves", "GBA", "Starlight Courier.gba.sav"), filepath.Join(f.sd, "Saves", "GBA", "STARLIGHT COURIER.gba.sav")); err != nil {
		t.Fatal(err)
	}
	id := f.deleteStarlight()
	f.writeSD("Roms/"+gbaFolder+"/starlight courier.gba", "sent again")
	f.do(http.MethodPost, f.restoreURL(id), restorePath, []byte(`{"onConflict":"rename"}`), http.StatusOK)
	g2 := gamePath(gbaFolder + "/Starlight Courier (2).gba")
	if r := f.get(g2+"/save", "/games/{gameId}/save", http.StatusOK); string(r.Body) != save {
		t.Fatalf("save of the renamed game %q", r.Body)
	}
	var states struct {
		Slots []struct {
			Slot   int  `json:"slot"`
			Locked bool `json:"locked"`
		} `json:"slots"`
	}
	f.get(g2+"/states", "/games/{gameId}/states", http.StatusOK).JSON(t, &states)
	locked := false
	for _, s := range states.Slots {
		locked = locked || (s.Slot == 0 && s.Locked)
	}
	if !locked {
		t.Fatalf("lock not read again: %+v", states)
	}
	if f.read("Roms/"+gbaFolder+"/starlight courier.gba") != "sent again" {
		t.Fatal("present game changed")
	}
}
