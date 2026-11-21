package server

import (
	"net/http"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/tfournier/ReminisLink/internal/events"
	"github.com/tfournier/ReminisLink/internal/store"
)

type restorePreview struct {
	EntryID  string `json:"entryId"`
	Reason   string `json:"reason"`
	Label    string `json:"label"`
	Conflict bool   `json:"conflict"`
	Items    []struct {
		Name     string `json:"name"`
		Size     int64  `json:"size"`
		Conflict bool   `json:"conflict"`
		FreeName string `json:"freeName"`
	} `json:"items"`
	RestoredBytes int64 `json:"restoredBytes"`
}

type restoreResult struct {
	Restored      []string `json:"restored"`
	RestoredBytes int64    `json:"restoredBytes"`
	Renamed       bool     `json:"renamed"`
}

const (
	restorePath = "/trash/{entryId}/restore"
	starlight   = gbaFolder + "/Starlight Courier.gba"
)

func (f *fixture) restoreURL(id string) string { return "/api/v1/trash/" + id + "/restore" }

func (f *fixture) preview(id string) restorePreview {
	f.t.Helper()
	var p restorePreview
	f.get(f.restoreURL(id), restorePath, http.StatusOK).JSON(f.t, &p)
	return p
}

// deleteStarlight deletes Starlight Courier with a clock file, a state,
// cheats and art; it returns the trash entry.
func (f *fixture) deleteStarlight() string {
	f.t.Helper()
	f.writeSD("Saves/GBA/Starlight Courier.gba.rtc", "clock")
	f.writeSD("Roms/"+gbaFolder+"/.media/Starlight Courier.png", "art")
	var res struct {
		TrashEntryID string `json:"trashEntryId"`
	}
	f.do(http.MethodDelete, gamePath(starlight), "/games/{gameId}", nil, http.StatusOK).JSON(f.t, &res)
	if f.exists("Roms/" + starlight) {
		f.t.Fatal("game still in place")
	}
	return res.TrashEntryID
}

func TestRestoreDeletedGameOverHTTP(t *testing.T) {
	log := &changeLog{}
	f := newFixture(t, func(o *Options) { o.OnChange = log.add })
	f.fastEvents(events.Options{})
	before := map[string]string{}
	for _, rel := range []string{"Roms/" + starlight, "Saves/GBA/Starlight Courier.gba.sav"} {
		before[rel] = f.read(rel)
	}
	id := f.deleteStarlight()
	log.take()

	p := f.preview(id)
	if p.EntryID != id || p.Reason != "delete-game" || p.Label != "Starlight Courier" || p.Conflict {
		t.Fatalf("preview %+v", p)
	}
	names := map[string]bool{}
	for _, it := range p.Items {
		if it.Conflict || it.FreeName != it.Name {
			t.Fatalf("item %+v", it)
		}
		names[it.Name] = true
	}
	for _, want := range []string{"Roms/" + starlight, "Saves/GBA/Starlight Courier.gba.sav", "Saves/GBA/Starlight Courier.gba.rtc", "Roms/" + gbaFolder + "/.media/Starlight Courier.png"} {
		if !names[want] {
			t.Errorf("%s not in the preview %v", want, names)
		}
	}

	other := f.pairNamed("iPad")
	s := f.openEvents(other)
	var res restoreResult
	f.do(http.MethodPost, f.restoreURL(id), restorePath, nil, http.StatusOK).JSON(t, &res)
	if res.Renamed || len(res.Restored) != len(p.Items) || res.RestoredBytes != p.RestoredBytes {
		t.Fatalf("result %+v", res)
	}
	for rel, want := range before {
		if got := f.read(rel); got != want {
			t.Errorf("%s changed", rel)
		}
	}
	if f.read("Saves/GBA/Starlight Courier.gba.rtc") != "clock" || f.read("Roms/"+gbaFolder+"/.media/Starlight Courier.png") != "art" {
		t.Fatal("clock file or art not back")
	}
	f.get(gamePath(starlight), "/games/{gameId}", http.StatusOK)
	if entries, _, _ := f.trash.List(); len(entries) != 0 {
		t.Fatalf("trash %+v", entries)
	}
	if ev := f.nextEvent(s); ev.Type != events.TypeLibrary || ev.Data != `{"systemIds":[]}` {
		t.Fatalf("event %+v", ev)
	}
	if got := log.take(); len(got) != 1 || got[0] != (Change{Kind: ChangeTrashRestored, Name: "Starlight Courier"}) {
		t.Fatalf("changes %+v", got)
	}
	// Gone now; malformed ids.
	f.get(f.restoreURL(id), restorePath, http.StatusNotFound)
	f.do(http.MethodPost, f.restoreURL(id), restorePath, nil, http.StatusNotFound)
	f.do(http.MethodPost, f.restoreURL("nope"), restorePath, nil, http.StatusBadRequest)
	f.get(f.restoreURL("nope"), restorePath, http.StatusBadRequest)
}

func TestRestoreConflictsOverHTTP(t *testing.T) {
	f := newFixture(t)
	id := f.deleteStarlight()
	// The game was sent again meanwhile (another case: the same file on exFAT).
	f.writeSD("Roms/"+gbaFolder+"/STARLIGHT COURIER.gba", "new rom")

	p := f.preview(id)
	if !p.Conflict {
		t.Fatalf("preview %+v", p)
	}
	for _, it := range p.Items {
		if it.Name == "Roms/"+starlight && (!it.Conflict || it.FreeName != "Roms/"+gbaFolder+"/Starlight Courier (2).gba") {
			t.Fatalf("rom %+v", it)
		}
		if it.Name == "Saves/GBA/Starlight Courier.gba.sav" && (it.Conflict || it.FreeName != "Saves/GBA/Starlight Courier (2).gba.sav") {
			t.Fatalf("save %+v", it)
		}
	}

	r := f.do(http.MethodPost, f.restoreURL(id), restorePath, []byte(`{"onConflict":"fail"}`), http.StatusConflict)
	if !strings.Contains(string(r.Body), `"conflicts":["Roms/`+gbaFolder+`/Starlight Courier.gba"]`) {
		t.Fatalf("409 %s", r.Body)
	}
	f.do(http.MethodPost, f.restoreURL(id), restorePath, []byte(`{"onConflict":"overwrite"}`), http.StatusBadRequest)
	f.do(http.MethodPost, f.restoreURL(id), restorePath, []byte(`{`), http.StatusBadRequest)
	if f.read("Roms/"+gbaFolder+"/STARLIGHT COURIER.gba") != "new rom" {
		t.Fatal("present file changed")
	}

	var res restoreResult
	f.do(http.MethodPost, f.restoreURL(id), restorePath, []byte(`{"onConflict":"rename"}`), http.StatusOK).JSON(t, &res)
	if !res.Renamed {
		t.Fatalf("result %+v", res)
	}
	if f.read("Roms/"+gbaFolder+"/STARLIGHT COURIER.gba") != "new rom" || !f.exists("Roms/"+gbaFolder+"/Starlight Courier (2).gba") ||
		!f.exists("Saves/GBA/Starlight Courier (2).gba.sav") || f.read("Saves/GBA/Starlight Courier (2).gba.rtc") != "clock" {
		t.Fatal("not restored under the free name")
	}
	// The restored game is listed with its own save.
	f.get(gamePath(gbaFolder+"/Starlight Courier (2).gba")+"/save", "/games/{gameId}/save", http.StatusOK)
}

func TestRestoreReplacedSaveOverHTTP(t *testing.T) {
	f := newFixture(t)
	g := gamePath(starlight)
	old := f.read("Saves/GBA/Starlight Courier.gba.sav")
	// A replaced save is kept as a before-replace copy (M5); deleting
	// the copy (or the automatic limits) moves it to the trash.
	var put struct {
		Backup *backupJSON `json:"backup"`
	}
	f.do(http.MethodPut, g+"/save", "/games/{gameId}/save", []byte("new save"), http.StatusOK).JSON(t, &put)
	f.do(http.MethodDelete, g+"/save/backups/"+put.Backup.ID, "/games/{gameId}/save/backups/{backupId}", nil, http.StatusNoContent)
	entries, _, _ := f.trash.List()
	if len(entries) != 1 || entries[0].Reason != store.ReasonDeleteBackup {
		t.Fatalf("trash %+v", entries)
	}
	var res restoreResult
	f.do(http.MethodPost, f.restoreURL(entries[0].ID), restorePath, nil, http.StatusOK).JSON(t, &res)
	var list struct {
		Backups []backupJSON `json:"backups"`
	}
	f.get(g+"/save/backups", "/games/{gameId}/save/backups", http.StatusOK).JSON(t, &list)
	if len(list.Backups) != 1 || list.Backups[0].ID != put.Backup.ID || list.Backups[0].Reason != "before-replace" {
		t.Fatalf("backups after the restore %+v", list)
	}
	// The copy is back: the replaced save comes back on the card.
	f.do(http.MethodPost, g+"/save/backups/"+put.Backup.ID+"/restore", "/games/{gameId}/save/backups/{backupId}/restore", nil, http.StatusOK)
	if f.read("Saves/GBA/Starlight Courier.gba.sav") != old {
		t.Fatal("save not back")
	}

	// A save in the trash whose place is taken: 409, then a free name.
	f.writeSD("Saves/GBA/Starlight Courier.gba.sav", "older")
	moved, err := f.trash.Move(store.ReasonOverwrite, "Starlight Courier", []store.MoveItem{{Path: filepath.Join(f.sd, "Saves", "GBA", "Starlight Courier.gba.sav")}})
	if err != nil {
		t.Fatal(err)
	}
	f.writeSD("Saves/GBA/Starlight Courier.gba.sav", "current")
	r := f.do(http.MethodPost, f.restoreURL(moved.Entry.ID), restorePath, nil, http.StatusConflict)
	if !strings.Contains(string(r.Body), "Saves/GBA/Starlight Courier.gba.sav") {
		t.Fatalf("409 %s", r.Body)
	}
	f.do(http.MethodPost, f.restoreURL(moved.Entry.ID), restorePath, []byte(`{"onConflict":"rename"}`), http.StatusOK).JSON(t, &res)
	if !reflect.DeepEqual(res.Restored, []string{"Saves/GBA/Starlight Courier.gba (2).sav"}) {
		t.Fatalf("restored %v", res.Restored)
	}
	if f.read("Saves/GBA/Starlight Courier.gba.sav") != "current" || f.read("Saves/GBA/Starlight Courier.gba (2).sav") != "older" {
		t.Fatal("save files")
	}
}

func TestRestoreDeletedPakOverHTTP(t *testing.T) {
	f := newFixture(t)
	f.fastEvents(events.Options{})
	f.writeSD("Tools/tg5040/Clock.pak/launch.sh", "x")
	f.writeSD("Tools/tg5040/Clock.pak/res/font.ttf", "font")
	clock := f.listPaks()["Tools/tg5040/Clock.pak"]
	f.do(http.MethodDelete, "/api/v1/paks/"+clock.ID, "/paks/{pakId}", nil, http.StatusOK)
	entries, _, _ := f.trash.List()
	s := f.openEvents(f.client)
	var res restoreResult
	f.do(http.MethodPost, f.restoreURL(entries[0].ID), restorePath, nil, http.StatusOK).JSON(t, &res)
	sort.Strings(res.Restored)
	if !reflect.DeepEqual(res.Restored, []string{"Tools/tg5040/Clock.pak/launch.sh", "Tools/tg5040/Clock.pak/res/font.ttf"}) {
		t.Fatalf("restored %v", res.Restored)
	}
	if got := f.listPaks()["Tools/tg5040/Clock.pak"]; got.ID != clock.ID {
		t.Fatalf("pak not listed again: %+v", got)
	}
	seen := map[string]bool{}
	for len(seen) < 2 {
		ev := f.nextEvent(s)
		seen[ev.Type] = true
		if ev.Type != events.TypePaks && ev.Type != events.TypeLibrary {
			t.Fatalf("event %+v", ev)
		}
	}
}

func TestRestoreRefusesABadPakFolder(t *testing.T) {
	f := newFixture(t)
	// A pak folder without launch.sh in the trash (an older pak, or the
	// card edited by hand): never installed by a restore.
	f.writeSD("Emus/tg5040/Broken.pak/readme.txt", "x")
	res, err := f.trash.Move(store.ReasonDeletePak, "Broken", []store.MoveItem{{Path: filepath.Join(f.sd, "Emus", "tg5040", "Broken.pak")}})
	if err != nil {
		t.Fatal(err)
	}
	r := f.do(http.MethodPost, f.restoreURL(res.Entry.ID), restorePath, nil, http.StatusUnprocessableEntity)
	if !strings.Contains(string(r.Body), "launch.sh") {
		t.Fatalf("422 %s", r.Body)
	}
	if f.exists("Emus/tg5040/Broken.pak") {
		t.Fatal("restored")
	}
	if entries, _, _ := f.trash.List(); len(entries) != 1 {
		t.Fatal("entry changed")
	}
}
