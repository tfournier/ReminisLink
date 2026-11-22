package main

import (
	"bytes"
	"image/jpeg"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/screenshots"
	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

// smokeScreenshots checks the screenshots of a card (M22) through the API
// of a running pak: the list matches the folder of the adapter, the
// newest one downloads byte for byte and gets a JPEG preview, then goes
// to the trash and comes back with the M18 restore.
func smokeScreenshots(t *testing.T, c *testutil.Client, roots osadapter.Roots) {
	t.Helper()
	var list screenshots.List
	call(t, c, http.MethodGet, "/api/v1/screenshots", nil, http.StatusOK, &list)
	des, err := os.ReadDir(roots.Screenshots)
	if err != nil {
		t.Fatalf("screenshots folder: %v", err)
	}
	images := 0
	for _, d := range des {
		// Names with a control character have no identifier.
		if d.Type().IsRegular() && strings.HasSuffix(strings.ToLower(d.Name()), ".png") && !strings.HasPrefix(d.Name(), ".") &&
			!strings.ContainsFunc(d.Name(), func(r rune) bool { return r < 0x20 }) {
			images++
		}
	}
	if len(list.Screenshots) == 0 || len(list.Screenshots) != images {
		t.Fatalf("listed %d screenshots, %d images in %s", len(list.Screenshots), images, roots.Screenshots)
	}
	first := list.Screenshots[0]
	onCard, err := os.ReadFile(filepath.Join(roots.Screenshots, first.Name))
	if err != nil {
		t.Fatal(err)
	}
	r := c.Do(t, http.MethodGet, "/api/v1/screenshots/"+first.ID, nil)
	if r.Status != http.StatusOK || !bytes.Equal(r.Body, onCard) || r.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("download of %s: %d, %d bytes, %s", first.Name, r.Status, len(r.Body), r.Header.Get("Content-Type"))
	}
	r = c.Do(t, http.MethodGet, "/api/v1/screenshots/"+first.ID+"/thumbnail", nil)
	if cfg, err := jpeg.DecodeConfig(bytes.NewReader(r.Body)); r.Status != http.StatusOK || err != nil || cfg.Width > screenshots.ThumbSize {
		t.Fatalf("preview of %s: %d %v", first.Name, r.Status, err)
	}
	var moved struct {
		TrashEntryID string `json:"trashEntryId"`
	}
	call(t, c, http.MethodPost, "/api/v1/screenshots/trash", map[string]any{"screenshotIds": []string{first.ID}}, http.StatusOK, &moved)
	if _, err := os.Lstat(filepath.Join(roots.Screenshots, first.Name)); err == nil {
		t.Fatalf("%s still in the folder", first.Name)
	}
	call(t, c, http.MethodPost, "/api/v1/trash/"+moved.TrashEntryID+"/restore", nil, http.StatusOK, nil)
	if back, err := os.ReadFile(filepath.Join(roots.Screenshots, first.Name)); err != nil || !bytes.Equal(back, onCard) {
		t.Fatalf("%s not restored byte for byte (%v)", first.Name, err)
	}
	t.Logf("screenshots: %d listed, %s downloaded, previewed, trashed and restored", len(list.Screenshots), first.Name)
}

// TestSmokeScreenshots runs the M22 scenario against the real binary on
// the NextUI card: the screenshots of Screenshots/ are listed (hidden
// files, other files, links and names without an identifier left out),
// downloaded, previewed (a forged huge PNG gets no preview), moved to the
// trash in one entry with a screenshots event, refused through a link,
// and restored (409 over a new file of the same name, then a free name).
func TestSmokeScreenshots(t *testing.T) {
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
	dir := roots.Screenshots
	// Hostile and ignored entries.
	secret := filepath.Join(t.TempDir(), "secret.png")
	writeFile(t, secret, "secret")
	if err := os.Symlink(secret, filepath.Join(dir, "Link.2026-11-22-10-00-00.png")); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}
	writeFile(t, filepath.Join(dir, ".hidden.png"), "x")
	writeFile(t, filepath.Join(dir, "notes.txt"), "x")
	writeFile(t, filepath.Join(dir, "tab\there.png"), "x")
	// A small file announcing a 100000 x 100000 image.
	writeFile(t, filepath.Join(dir, "Huge.2026-11-19-08-00-00.png"), "\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR\x00\x01\x86\xa0\x00\x01\x86\xa0\x08\x06\x00\x00\x00\x00\x00\x00\x00")

	clients, err := auth.OpenClientStore(store.Layout{Root: roots.Data}.ClientsFile())
	if err != nil {
		t.Fatal(err)
	}
	p := &pak{t: t, bin: bin, sd: sd, port: freePort(t)}
	c := testutil.PairClient(t, clients, p.url())
	p.start()
	c.HTTP = p.httpClient(30 * time.Second)
	defer p.stop()

	var list screenshots.List
	call(t, c, http.MethodGet, "/api/v1/screenshots", nil, http.StatusOK, &list)
	var got []string
	for _, s := range list.Screenshots {
		got = append(got, s.Name)
	}
	if strings.Join(got, "|") != "Starlight Courier.2026-11-21-18-30-00.png|Café Rêverie.2026-11-20-09-15-42.png|Huge.2026-11-19-08-00-00.png" {
		t.Fatalf("list: %q", got)
	}
	huge := list.Screenshots[2]
	if r := c.Do(t, http.MethodGet, "/api/v1/screenshots/"+huge.ID+"/thumbnail", nil); r.Status != http.StatusNotFound {
		t.Fatalf("preview of a forged PNG: %d", r.Status)
	}
	for _, id := range []string{screenshots.ID("Link.2026-11-22-10-00-00.png"), screenshots.ID("../Roms/x.png"), "sc_Li4"} {
		r := c.Do(t, http.MethodGet, "/api/v1/screenshots/"+id, nil)
		if r.Status != http.StatusNotFound && r.Status != http.StatusBadRequest {
			t.Fatalf("GET %s: %d", id, r.Status)
		}
	}

	// The common checks of every layout.
	smokeScreenshots(t, c, roots)

	// Two at once: one trash entry, a screenshots event.
	events := testutil.OpenSSE(t, c)
	if ev, err := events.NextEvent(10 * time.Second); err != nil || ev.Type != "hello" {
		t.Fatalf("hello: %+v %v", ev, err)
	}
	a, b := list.Screenshots[0], list.Screenshots[1]
	var moved struct {
		TrashEntryID string   `json:"trashEntryId"`
		Moved        []string `json:"moved"`
	}
	call(t, c, http.MethodPost, "/api/v1/screenshots/trash", map[string]any{"screenshotIds": []string{a.ID, b.ID}}, http.StatusOK, &moved)
	if len(moved.Moved) != 2 {
		t.Fatalf("moved %+v", moved)
	}
	for {
		ev, err := events.NextEvent(10 * time.Second)
		if err != nil {
			t.Fatalf("no screenshots event: %v", err)
		}
		if ev.Type == "screenshots" {
			break
		}
	}
	// A new screenshot with the same name: 409, then a free name.
	writeFile(t, filepath.Join(dir, a.Name), "new")
	r := c.Do(t, http.MethodPost, "/api/v1/trash/"+moved.TrashEntryID+"/restore", nil)
	if r.Status != http.StatusConflict {
		t.Fatalf("restore over a present file: %d %s", r.Status, r.Body)
	}
	call(t, c, http.MethodPost, "/api/v1/trash/"+moved.TrashEntryID+"/restore", map[string]any{"onConflict": "rename"}, http.StatusOK, nil)
	if data, err := os.ReadFile(filepath.Join(dir, a.Name)); err != nil || string(data) != "new" {
		t.Fatal("the new screenshot was overwritten")
	}
	renamed := strings.TrimSuffix(a.Name, ".png") + " (2).png"
	if _, err := os.Stat(filepath.Join(dir, renamed)); err != nil {
		t.Fatalf("%s: %v", renamed, err)
	}
	if data, _ := os.ReadFile(secret); string(data) != "secret" {
		t.Fatal("the target of the link changed")
	}
	call(t, c, http.MethodDelete, "/api/v1/clients/me", nil, http.StatusNoContent, nil)
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
