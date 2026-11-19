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

// TestSmokeSaves runs the M5 scenario against the real binary: a locked
// state changed by hand on the SD card while the pak is closed is restored
// at the next start (lockStatus "restored", then "intact"); a save is
// backed up, replaced and restored; cheats are imported and edited, with an
// ETag conflict.
func TestSmokeSaves(t *testing.T) {
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
	c.HTTP = &http.Client{Timeout: 30 * time.Second}
	p.start()
	defer p.stop()

	game := "/api/v1/games/" + library.EncodeID("Game Boy Advance (GBA)/Starlight Courier.gba")
	statePath := filepath.Join(sd, ".userdata", "shared", "GBA-gpsp", "Starlight Courier.gba.st0")
	type slot struct {
		Slot       int     `json:"slot"`
		Locked     bool    `json:"locked"`
		LockStatus *string `json:"lockStatus"`
	}
	slot0 := func() slot {
		var list struct {
			Slots []slot `json:"slots"`
		}
		call(t, c, http.MethodGet, game+"/states", nil, http.StatusOK, &list)
		for _, s := range list.Slots {
			if s.Slot == 0 {
				return s
			}
		}
		t.Fatal("slot 0 missing")
		return slot{}
	}
	status := func(s slot) string {
		if s.LockStatus == nil {
			return "null"
		}
		return *s.LockStatus
	}

	// 1. Lock slot 0, change it by hand while the pak is closed, restart.
	original, _ := os.ReadFile(statePath)
	var locked slot
	call(t, c, http.MethodPut, game+"/states/0/lock", nil, http.StatusOK, &locked)
	if !locked.Locked || status(locked) != "intact" {
		t.Fatalf("lock: %+v", locked)
	}
	p.stop()
	if err := os.WriteFile(statePath, []byte("overwritten in game"), 0o644); err != nil {
		t.Fatal(err)
	}
	p.start()
	if got, _ := os.ReadFile(statePath); string(got) != string(original) {
		t.Fatalf("state not restored at start: %q", got)
	}
	if s := slot0(); status(s) != "restored" {
		t.Fatalf("first GET /states: %s", status(s))
	}
	if s := slot0(); status(s) != "intact" {
		t.Fatalf("second GET /states: %s", status(s))
	}
	var trash struct {
		Entries []struct {
			Reason string `json:"reason"`
		} `json:"entries"`
	}
	call(t, c, http.MethodGet, "/api/v1/trash", nil, http.StatusOK, &trash)
	if len(trash.Entries) != 1 || trash.Entries[0].Reason != "lock-restore" {
		t.Fatalf("overwritten state not in the trash: %+v", trash)
	}
	t.Log("locked state overwritten by hand: restored at start, lockStatus restored then intact")

	// 2. Save: backup, replace, restore.
	savePath := filepath.Join(sd, "Saves", "GBA", "Starlight Courier.gba.sav")
	oldSave, _ := os.ReadFile(savePath)
	var manual struct {
		ID string `json:"id"`
	}
	call(t, c, http.MethodPost, game+"/save/backups", map[string]any{"label": "Avant le boss"}, http.StatusCreated, &manual)
	if r := c.Do(t, http.MethodPut, game+"/save", []byte("new save")); r.Status != http.StatusOK || !strings.Contains(string(r.Body), "before-replace") {
		t.Fatalf("PUT save: %d %s", r.Status, r.Body)
	}
	var restored struct {
		Backup struct {
			Reason string `json:"reason"`
		} `json:"backup"`
	}
	call(t, c, http.MethodPost, game+"/save/backups/"+manual.ID+"/restore", nil, http.StatusOK, &restored)
	if got, _ := os.ReadFile(savePath); string(got) != string(oldSave) || restored.Backup.Reason != "before-restore" {
		t.Fatalf("restore: %q %+v", got, restored)
	}
	var backups struct {
		Backups []json.RawMessage `json:"backups"`
	}
	call(t, c, http.MethodGet, game+"/save/backups", nil, http.StatusOK, &backups)
	if len(backups.Backups) != 3 {
		t.Fatalf("backups: %d", len(backups.Backups))
	}
	t.Log("save: manual backup, replace (before-replace), restore (before-restore): ok")

	// 3. Cheats: import, edit, conflict.
	var imported struct {
		Imported, Total int
	}
	call(t, c, http.MethodPost, game+"/cheats/import", map[string]any{
		"source": map[string]any{"type": "inline", "text": "cheat0_desc = \"Smoke\"\ncheat0_code = \"1\"\n"}, "mode": "merge",
	}, http.StatusOK, &imported)
	if imported.Imported != 1 || imported.Total != 3 {
		t.Fatalf("import: %+v", imported)
	}
	var list struct {
		Cheats []map[string]any `json:"cheats"`
		ETag   string           `json:"etag"`
	}
	call(t, c, http.MethodGet, game+"/cheats", nil, http.StatusOK, &list)
	body, _ := json.Marshal(map[string]any{"cheats": list.Cheats[:2]})
	put := func(etag string) int {
		req := c.Request(t, http.MethodPut, game+"/cheats", body)
		req.Header.Set("If-Match", etag)
		return c.Send(t, req).Status
	}
	if st := put(list.ETag); st != http.StatusOK {
		t.Fatalf("PUT cheats: %d", st)
	}
	if st := put(list.ETag); st != http.StatusPreconditionFailed {
		t.Fatalf("stale ETag: %d", st)
	}
	if _, err := os.Stat(filepath.Join(sd, "Cheats", "GBA", "Starlight Courier.gba.cht.bak")); err != nil {
		t.Fatal("no .bak")
	}
	t.Log("cheats: import, edit with If-Match, stale ETag answered 412: ok")

	// The log is read once the pak has exited (it writes it until then).
	p.stop()
	if !strings.Contains(p.logs.String(), "locked state restored") {
		t.Errorf("restoration not logged:\n%s", p.logs.String())
	}
}
