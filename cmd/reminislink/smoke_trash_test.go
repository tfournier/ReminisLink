package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

// TestSmokeTrash runs the M18 scenario against the real binary: a deleted
// game (with its clock file, states, cheats and art), a replaced save and
// a deleted emulator pak come back from the trash, with no loss and no
// overwrite (409, then a free name); power cuts while the pak is closed
// (before the first rename: cancelled; after one: finished; a tampered
// journal: refused); a replaced save comes back over the current save.
func TestSmokeTrash(t *testing.T) {
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
	clients, err := auth.OpenClientStore(store.Layout{Root: roots.Data}.ClientsFile())
	if err != nil {
		t.Fatal(err)
	}
	p := &pak{t: t, bin: bin, sd: sd, port: freePort(t)}
	c := testutil.PairClient(t, clients, p.url())
	p.start()
	c.HTTP = p.httpClient(30 * time.Second)
	defer p.stop()

	const gba = "Game Boy Advance (GBA)"
	game := "/api/v1/games/" + library.EncodeID(gba+"/Starlight Courier.gba")
	files := map[string]string{} // SD-relative path -> content
	for _, rel := range []string{
		"Roms/" + gba + "/Starlight Courier.gba", "Saves/GBA/Starlight Courier.gba.sav",
		".userdata/shared/GBA-gpsp/Starlight Courier.gba.st0", ".userdata/shared/GBA-gpsp/Starlight Courier.gba.st1",
		"Cheats/GBA/Starlight Courier.gba.cht",
	} {
		data, err := os.ReadFile(filepath.Join(sd, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		files[rel] = string(data)
	}
	for rel, content := range map[string]string{
		"Saves/GBA/Starlight Courier.gba.rtc": "clock", "Roms/" + gba + "/.media/Starlight Courier.png": "art",
	} {
		writeSD(t, sd, rel, content)
		files[rel] = content
	}
	checkFiles := func(what string) {
		t.Helper()
		for rel, want := range files {
			if got, err := os.ReadFile(filepath.Join(sd, filepath.FromSlash(rel))); err != nil || string(got) != want {
				t.Fatalf("%s: %s missing or changed (%v)", what, rel, err)
			}
		}
	}
	type trashList struct {
		Entries []struct {
			ID     string `json:"id"`
			Reason string `json:"reason"`
		} `json:"entries"`
	}
	trash := func() trashList {
		var l trashList
		call(t, c, http.MethodGet, "/api/v1/trash", nil, http.StatusOK, &l)
		return l
	}
	type result struct {
		Restored []string `json:"restored"`
		Renamed  bool     `json:"renamed"`
	}

	// 1. A deleted game comes back with its data.
	var deleted struct {
		TrashEntryID string `json:"trashEntryId"`
	}
	call(t, c, http.MethodDelete, game, nil, http.StatusOK, &deleted)
	var preview struct {
		Conflict bool              `json:"conflict"`
		Items    []json.RawMessage `json:"items"`
	}
	restore := "/api/v1/trash/" + deleted.TrashEntryID + "/restore"
	call(t, c, http.MethodGet, restore, nil, http.StatusOK, &preview)
	if preview.Conflict || len(preview.Items) < len(files) { // plus a thumbnail and another state
		t.Fatalf("preview: %+v", preview)
	}
	var res result
	call(t, c, http.MethodPost, restore, nil, http.StatusOK, &res)
	checkFiles("deleted game restored")
	call(t, c, http.MethodGet, game, nil, http.StatusOK, nil)
	if l := trash(); len(l.Entries) != 0 {
		t.Fatalf("trash after the restore: %+v", l)
	}
	t.Logf("deleted game restored: %d files (ROM, save, clock, states, cheats, art)", len(res.Restored))

	// 2. The same game deleted again, then sent again: 409, then a free name.
	call(t, c, http.MethodDelete, game, nil, http.StatusOK, &deleted)
	writeSD(t, sd, "Roms/"+gba+"/starlight courier.GBA", "sent again")
	restore = "/api/v1/trash/" + deleted.TrashEntryID + "/restore"
	if r := c.Do(t, http.MethodPost, restore, nil); r.Status != http.StatusConflict || !strings.Contains(string(r.Body), `"conflicts"`) {
		t.Fatalf("restore over a present file: %d %s", r.Status, r.Body)
	}
	call(t, c, http.MethodPost, restore, map[string]any{"onConflict": "rename"}, http.StatusOK, &res)
	if !res.Renamed {
		t.Fatalf("not renamed: %+v", res)
	}
	if got, _ := os.ReadFile(filepath.Join(sd, "Roms", gba, "starlight courier.GBA")); string(got) != "sent again" {
		t.Fatal("present file overwritten")
	}
	renamed := map[string]string{}
	for rel, content := range files {
		renamed[strings.Replace(rel, "Starlight Courier", "Starlight Courier (2)", 1)] = content
	}
	for rel, want := range renamed {
		if got, err := os.ReadFile(filepath.Join(sd, filepath.FromSlash(rel))); err != nil || string(got) != want {
			t.Fatalf("free name: %s missing (%v)", rel, err)
		}
	}
	call(t, c, http.MethodGet, "/api/v1/games/"+library.EncodeID(gba+"/Starlight Courier (2).gba")+"/save", nil, http.StatusOK, nil)
	t.Log("restore over a present file: 409, then every file under the free name \"Starlight Courier (2)\"")

	// 3. A replaced save comes back: its before-replace copy, deleted to
	// the trash, is restored, then the copy restores the save.
	game2 := "/api/v1/games/" + library.EncodeID(gba+"/Starlight Courier (2).gba")
	oldSave := renamed["Saves/GBA/Starlight Courier (2).gba.sav"]
	var put struct {
		Backup struct {
			ID string `json:"id"`
		} `json:"backup"`
	}
	if r := c.Do(t, http.MethodPut, game2+"/save", []byte("new save")); r.Status != http.StatusOK {
		t.Fatalf("PUT save: %d %s", r.Status, r.Body)
	} else {
		r.JSON(t, &put)
	}
	call(t, c, http.MethodDelete, game2+"/save/backups/"+put.Backup.ID, nil, http.StatusNoContent, nil)
	l := trash()
	if len(l.Entries) != 1 || l.Entries[0].Reason != "delete-backup" {
		t.Fatalf("trash: %+v", l)
	}
	call(t, c, http.MethodPost, "/api/v1/trash/"+l.Entries[0].ID+"/restore", nil, http.StatusOK, nil)
	call(t, c, http.MethodPost, game2+"/save/backups/"+put.Backup.ID+"/restore", nil, http.StatusOK, nil)
	if got, _ := os.ReadFile(filepath.Join(sd, "Saves", "GBA", "Starlight Courier (2).gba.sav")); string(got) != oldSave {
		t.Fatalf("replaced save not back: %q", got)
	}
	t.Log("replaced save: its copy restored from the trash, then the save restored from the copy")

	// 4. A deleted emulator pak comes back and is listed again.
	type pakList struct {
		Paks []struct {
			ID   string `json:"pakId"`
			Name string `json:"name"`
		} `json:"paks"`
	}
	mgba := func() string {
		var pl pakList
		call(t, c, http.MethodGet, "/api/v1/paks", nil, http.StatusOK, &pl)
		for _, p := range pl.Paks {
			if p.Name == "MGBA" {
				return p.ID
			}
		}
		return ""
	}
	pakID := mgba()
	if pakID == "" {
		t.Fatal("MGBA not listed")
	}
	var pakDeleted struct {
		TrashEntryID string `json:"trashEntryId"`
	}
	call(t, c, http.MethodDelete, "/api/v1/paks/"+pakID, nil, http.StatusOK, &pakDeleted)
	if mgba() != "" {
		t.Fatal("MGBA still listed")
	}
	call(t, c, http.MethodPost, "/api/v1/trash/"+pakDeleted.TrashEntryID+"/restore", nil, http.StatusOK, nil)
	if mgba() != pakID {
		t.Fatal("MGBA not listed after the restore")
	}
	t.Log("deleted emulator pak MGBA restored and listed again")

	// 5. Power losses, with the pak closed between the steps.
	entryOf := func(id string) (string, map[string]any) {
		t.Helper()
		f := filepath.Join(roots.Data, "trash", id, "entry.json")
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var entry map[string]any
		if err := json.Unmarshal(data, &entry); err != nil {
			t.Fatal(err)
		}
		return f, entry
	}
	writeEntry := func(f string, entry map[string]any) {
		t.Helper()
		data, _ := json.Marshal(entry)
		if err := os.WriteFile(f, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	returning := func(entry map[string]any, target func(string) string) {
		var returns []map[string]string
		for _, m := range entry["moves"].([]any) {
			returns = append(returns, map[string]string{"move": m.(string), "target": target(m.(string))})
		}
		entry["state"], entry["returns"], entry["returnMode"] = "returning", returns, "fail"
	}
	lantern := "Roms/" + gba + "/Lantern Keeper [b1].gba"
	lanternBytes, err := os.ReadFile(filepath.Join(sd, filepath.FromSlash(lantern)))
	if err != nil {
		t.Fatal(err)
	}
	writeSD(t, sd, "Saves/GBA/Lantern Keeper [b1].gba.sav", "lantern save")

	// 5a. Cut after the journal, before the first rename: cancelled at
	// the next start (a failed request never restores later).
	call(t, c, http.MethodDelete, "/api/v1/games/"+library.EncodeID(gba+"/Lantern Keeper [b1].gba"), nil, http.StatusOK, &deleted)
	p.stop()
	f, entry := entryOf(deleted.TrashEntryID)
	returning(entry, func(m string) string { return m })
	writeEntry(f, entry)
	p.start()
	if _, err := os.Stat(filepath.Join(sd, filepath.FromSlash(lantern))); err == nil {
		t.Fatal("a restore that never started was finished")
	}
	if l := trash(); len(l.Entries) != 1 {
		t.Fatalf("trash after the cancel: %+v", l)
	}
	t.Log("restore cut before its first rename: cancelled at the next start, the entry is listed again")

	// 5b. Cut after the ROM went back: finished at the next start, bytes
	// checked.
	p.stop()
	f, entry = entryOf(deleted.TrashEntryID)
	returning(entry, func(m string) string { return m })
	writeEntry(f, entry)
	if err := os.Rename(filepath.Join(roots.Data, "trash", deleted.TrashEntryID, filepath.FromSlash(lantern)), filepath.Join(sd, filepath.FromSlash(lantern))); err != nil {
		t.Fatal(err)
	}
	p.start()
	if got, _ := os.ReadFile(filepath.Join(sd, "Saves", "GBA", "Lantern Keeper [b1].gba.sav")); string(got) != "lantern save" {
		t.Fatal("interrupted restore not finished at start")
	}
	if got, _ := os.ReadFile(filepath.Join(sd, filepath.FromSlash(lantern))); string(got) != string(lanternBytes) {
		t.Fatal("ROM changed")
	}
	if l := trash(); len(l.Entries) != 0 {
		t.Fatalf("trash after the recovery: %+v", l)
	}
	t.Log("restore cut after a rename: finished at the next start, bytes unchanged")

	// 5c. A tampered journal sends the game to the tools: refused, the
	// files stay in the trash.
	call(t, c, http.MethodDelete, "/api/v1/games/"+library.EncodeID(gba+"/Lantern Keeper [b1].gba"), nil, http.StatusOK, &deleted)
	p.stop()
	f, entry = entryOf(deleted.TrashEntryID)
	returning(entry, func(m string) string {
		if strings.HasPrefix(m, "Roms/") {
			return "Tools/tg5040/Evil.pak"
		}
		return m
	})
	writeEntry(f, entry)
	// The save went back before the cut.
	if err := os.Rename(filepath.Join(roots.Data, "trash", deleted.TrashEntryID, "Saves", "GBA", "Lantern Keeper [b1].gba.sav"), filepath.Join(sd, "Saves", "GBA", "Lantern Keeper [b1].gba.sav")); err != nil {
		t.Fatal(err)
	}
	p.start()
	if _, err := os.Stat(filepath.Join(sd, "Tools", "tg5040", "Evil.pak")); err == nil {
		t.Fatal("tampered journal followed")
	}
	if got, err := os.ReadFile(filepath.Join(roots.Data, "trash", deleted.TrashEntryID, filepath.FromSlash(lantern))); err != nil || string(got) != string(lanternBytes) {
		t.Fatalf("ROM not kept in the trash: %v", err)
	}
	if l := trash(); len(l.Entries) != 1 {
		t.Fatalf("trash after a refused journal: %+v", l)
	}
	t.Log("tampered journal (a game sent to the tools): refused at the next start, the ROM stays in the trash")

	// 6. A replaced save comes back in place of the current one.
	// 6a. Its backup copy (deleted to the trash): the current save is kept
	// as a before-restore copy.
	starlightSave := filepath.Join(sd, "Saves", "GBA", "Starlight Courier (2).gba.sav")
	before, _ := os.ReadFile(starlightSave)
	if r := c.Do(t, http.MethodPut, game2+"/save", []byte("newer save")); r.Status != http.StatusOK {
		t.Fatalf("PUT save: %d %s", r.Status, r.Body)
	} else {
		r.JSON(t, &put)
	}
	call(t, c, http.MethodDelete, game2+"/save/backups/"+put.Backup.ID, nil, http.StatusNoContent, nil)
	var backupEntry string
	for _, e := range trash().Entries {
		if e.Reason == "delete-backup" {
			backupEntry = e.ID
		}
	}
	var rp struct {
		Replaceable   bool    `json:"replaceable"`
		ReplaceKeptAs *string `json:"replaceKeptAs"`
	}
	call(t, c, http.MethodGet, "/api/v1/trash/"+backupEntry+"/restore", nil, http.StatusOK, &rp)
	if !rp.Replaceable || rp.ReplaceKeptAs == nil || *rp.ReplaceKeptAs != "backup" {
		t.Fatalf("preview: %+v", rp)
	}
	call(t, c, http.MethodPost, "/api/v1/trash/"+backupEntry+"/restore", map[string]any{"onConflict": "replace"}, http.StatusOK, nil)
	if got, _ := os.ReadFile(starlightSave); string(got) != string(before) {
		t.Fatalf("live save %q, want %q", got, before)
	}
	t.Log("replaced save (its backup copy in the trash) restored over the current save, kept as a before-restore copy")

	// 6b. A save of the save folder in the trash (left by an older pak):
	// the current save and clock go to the trash first.
	p.stop()
	writeSD(t, sd, "Saves/GBA/Starlight Courier (2).gba.rtc", "current clock")
	current, _ := os.ReadFile(starlightSave)
	id := "tr_20261101T120000Z_abcd"
	writeSD(t, sd, ".userdata/shared/ReminisLink/trash/"+id+"/Saves/GBA/starlight courier (2).gba.sav", "save from the trash")
	writeEntry(filepath.Join(roots.Data, "trash", id, "entry.json"), map[string]any{
		"version": 1, "state": "done", "id": id, "trashedAt": "2026-11-01T12:00:00Z", "reason": "overwrite",
		"label": "Starlight Courier (2)", "files": []string{"Saves/GBA/starlight courier (2).gba.sav"},
		"moves": []string{"Saves/GBA/starlight courier (2).gba.sav"}, "sizeBytes": 19,
	})
	p.start()
	var rr struct {
		ReplacedEntryID *string  `json:"replacedEntryId"`
		Replaced        []string `json:"replaced"`
	}
	call(t, c, http.MethodPost, "/api/v1/trash/"+id+"/restore", map[string]any{"onConflict": "replace"}, http.StatusOK, &rr)
	if got, _ := os.ReadFile(starlightSave); string(got) != "save from the trash" {
		t.Fatalf("live save %q", got)
	}
	if rr.ReplacedEntryID == nil || len(rr.Replaced) != 2 {
		t.Fatalf("result %+v", rr)
	}
	kept := filepath.Join(roots.Data, "trash", *rr.ReplacedEntryID, "Saves", "GBA")
	if got, _ := os.ReadFile(filepath.Join(kept, "Starlight Courier (2).gba.sav")); string(got) != string(current) {
		t.Fatalf("current save not in the trash: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(kept, "Starlight Courier (2).gba.rtc")); string(got) != "current clock" {
		t.Fatal("current clock not in the trash")
	}
	call(t, c, http.MethodPost, "/api/v1/trash/"+backupEntryOr(t, trash().Entries, "delete-game")+"/restore", map[string]any{"onConflict": "replace"}, http.StatusUnprocessableEntity, nil)
	p.stop()
	if !strings.Contains(p.logs.String(), "interrupted restore finished") || !strings.Contains(p.logs.String(), "journaled place refused") {
		t.Errorf("recovery not logged:\n%s", p.logs.String())
	}
	t.Log("save in the trash restored over the current save: the current save and clock are in the trash, the live save holds the restored bytes; a game cannot replace (422)")
}

// backupEntryOr returns the id of the first entry of reason.
func backupEntryOr(t *testing.T, entries []struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}, reason string) string {
	t.Helper()
	for _, e := range entries {
		if e.Reason == reason {
			return e.ID
		}
	}
	t.Fatalf("no %s entry in %+v", reason, entries)
	return ""
}

func writeSD(t *testing.T, sd, rel, content string) {
	t.Helper()
	path := filepath.Join(sd, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
