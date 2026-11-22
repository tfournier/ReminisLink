package server

import (
	"bytes"
	"image/jpeg"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/events"
	"github.com/tfournier/ReminisLink/internal/screenshots"
)

// M22: the screenshots of the console.

const (
	shotStarlight = "Starlight Courier.2026-11-21-18-30-00.png"
	shotCafe      = "Café Rêverie.2026-11-20-09-15-42.png"
	shotsPath     = "/screenshots"
	shotPath      = "/screenshots/{screenshotId}"
	shotThumbPath = "/screenshots/{screenshotId}/thumbnail"
	shotTrashPath = "/screenshots/trash"
)

func shotURL(name string) string { return "/api/v1/screenshots/" + screenshots.ID(name) }

func (f *fixture) listShots() screenshots.List {
	f.t.Helper()
	var l screenshots.List
	f.get("/api/v1/screenshots", shotsPath, http.StatusOK).JSON(f.t, &l)
	return l
}

func TestScreenshotsRoutes(t *testing.T) {
	f := newFixture(t)
	l := f.listShots()
	if len(l.Screenshots) != 2 || l.Screenshots[0].Name != shotStarlight || l.Screenshots[1].Name != shotCafe {
		t.Fatalf("list %+v", l)
	}
	// Download: the bytes, the media type and the file name.
	r := f.get(shotURL(shotStarlight), shotPath, http.StatusOK)
	if r.Header.Get("Content-Type") != "image/png" || r.Header.Get("Content-Disposition") == "" ||
		string(r.Body) != f.read("Screenshots/"+shotStarlight) {
		t.Fatalf("download: %v", r.Header)
	}
	// Preview: a JPEG within 320 x 320.
	r = f.get(shotURL(shotStarlight)+"/thumbnail", shotThumbPath, http.StatusOK)
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(r.Body))
	if err != nil || cfg.Width > 320 || cfg.Height > 320 || r.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("preview %+v %v", cfg, err)
	}
	// Not found, invalid, no preview.
	f.get(shotURL("Missing.png"), shotPath, http.StatusNotFound)
	f.get("/api/v1/screenshots/sc_Li4vUm9tcy94LnBuZw", shotPath, http.StatusBadRequest) // "../Roms/x.png"
	f.get("/api/v1/screenshots/nope", shotPath, http.StatusBadRequest)
	f.writeSD("Screenshots/old.bmp", "BM....")
	f.get(shotURL("old.bmp")+"/thumbnail", shotThumbPath, http.StatusNotFound)
	f.get(shotURL("old.bmp"), shotPath, http.StatusOK)
}

func TestScreenshotsReadsNeverWait(t *testing.T) {
	f := newFixture(t)
	f.holdWrites(50 * time.Millisecond)
	defer f.server.writes().Release()
	f.listShots()
	f.get(shotURL(shotStarlight), shotPath, http.StatusOK)
	f.get(shotURL(shotStarlight)+"/thumbnail", shotThumbPath, http.StatusOK)
	r := f.client.Do(t, http.MethodPost, "/api/v1/screenshots/trash", mustJSON(t, map[string]any{"screenshotIds": []string{screenshots.ID(shotCafe)}}))
	wantBusy(t, r)
}

func TestTrashScreenshots(t *testing.T) {
	f := newFixture(t)
	f.fastEvents(events.Options{})
	s := f.openEvents(f.client)
	body := func(ids ...string) []byte { return mustJSON(t, map[string]any{"screenshotIds": ids}) }
	// Refused requests move nothing.
	f.do(http.MethodPost, "/api/v1/screenshots/trash", shotTrashPath, body(), http.StatusBadRequest)
	f.do(http.MethodPost, "/api/v1/screenshots/trash", shotTrashPath, body("nope"), http.StatusBadRequest)
	f.do(http.MethodPost, "/api/v1/screenshots/trash", shotTrashPath, []byte(`{"screenshotIds":"x"}`), http.StatusBadRequest)
	r := f.do(http.MethodPost, "/api/v1/screenshots/trash", shotTrashPath, body(screenshots.ID(shotCafe), screenshots.ID("Gone.png")), http.StatusNotFound)
	if r.ErrorCode() != "not_found" || !bytes.Contains(r.Body, []byte(screenshots.ID("Gone.png"))) {
		t.Fatalf("missing: %s", r.Body)
	}
	if !f.exists("Screenshots/" + shotCafe) {
		t.Fatal("moved despite a missing screenshot")
	}
	var res struct {
		TrashEntryID string   `json:"trashEntryId"`
		Moved        []string `json:"moved"`
		MovedBytes   int64    `json:"movedBytes"`
	}
	f.do(http.MethodPost, "/api/v1/screenshots/trash", shotTrashPath, body(screenshots.ID(shotCafe), screenshots.ID(shotStarlight)), http.StatusOK).JSON(t, &res)
	if len(res.Moved) != 2 || f.exists("Screenshots/"+shotCafe) || len(f.listShots().Screenshots) != 0 {
		t.Fatalf("moved %+v", res)
	}
	entries, _, _ := f.trash.List()
	if len(entries) != 1 || entries[0].Reason != "delete-screenshot" || entries[0].ID != res.TrashEntryID {
		t.Fatalf("trash %+v", entries)
	}
	// A screenshots event, no library event.
	if ev := f.nextEvent(s); ev.Type != events.TypeScreenshots || ev.Data != "{}" {
		t.Fatalf("event %+v", ev)
	}
	// The trash entry is listed by the contract, and restored by M18 with
	// a screenshots event besides the library one.
	f.get("/api/v1/trash", "/trash", http.StatusOK)
	f.do(http.MethodPost, f.restoreURL(res.TrashEntryID), restorePath, nil, http.StatusOK)
	if !f.exists("Screenshots/"+shotCafe) || len(f.listShots().Screenshots) != 2 {
		t.Fatal("not restored")
	}
	got := map[string]bool{}
	for len(got) < 2 {
		got[f.nextEvent(s).Type] = true
	}
	if !got[events.TypeLibrary] || !got[events.TypeScreenshots] {
		t.Fatalf("events after the restore: %v", got)
	}
}

func TestScreenshotsThroughALink(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Join(f.sd, "Screenshots")
	outside := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Rename(dir, outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, dir); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}
	if l := f.listShots(); len(l.Screenshots) != 0 {
		t.Fatalf("listed through a link: %+v", l)
	}
	f.get(shotURL(shotStarlight), shotPath, http.StatusNotFound)
	f.do(http.MethodPost, "/api/v1/screenshots/trash", shotTrashPath, mustJSON(t, map[string]any{"screenshotIds": []string{screenshots.ID(shotStarlight)}}), http.StatusNotFound)
}

func TestKnulliScreenshots(t *testing.T) {
	f := newKnulliFixture(t)
	l := f.listShots()
	if len(l.Screenshots) != 2 || l.Screenshots[0].GameName == nil || *l.Screenshots[0].GameName != "Starlight Courier" {
		t.Fatalf("list %+v", l)
	}
	f.get("/api/v1/screenshots/"+l.Screenshots[1].ID+"/thumbnail", shotThumbPath, http.StatusOK)
}

// Codex review of M22: a move that fails midway may leave a screenshot in
// a trash entry: the answer is 500 and a screenshots event still follows.
func TestTrashScreenshotsFailingMidwayPublishes(t *testing.T) {
	f := newFixture(t)
	f.fastEvents(events.Options{})
	s := f.openEvents(f.client)
	calls := 0
	f.trash.SetRename(func(oldpath, newpath string) error {
		calls++
		if calls >= 2 {
			return os.ErrPermission
		}
		return os.Rename(oldpath, newpath)
	})
	r := f.do(http.MethodPost, "/api/v1/screenshots/trash", shotTrashPath,
		mustJSON(t, map[string]any{"screenshotIds": []string{screenshots.ID(shotStarlight), screenshots.ID(shotCafe)}}), http.StatusInternalServerError)
	// The answer names what stayed in the trash.
	if !bytes.Contains(r.Body, []byte(`"kept":["`+screenshots.ID(shotStarlight)+`"]`)) {
		t.Fatalf("body %s", r.Body)
	}
	if ev := f.nextEvent(s); ev.Type != events.TypeScreenshots {
		t.Fatalf("event %+v", ev)
	}
	entries, _, _ := f.trash.List()
	if len(entries) != 1 || len(entries[0].Files) != 1 {
		t.Fatalf("left in the trash: %+v", entries)
	}
}
