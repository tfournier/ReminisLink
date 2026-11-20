package server

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/events"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/paks"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

// pakZip builds a zip archive from name → content ("" with a trailing
// slash: a folder).
func pakZip(t *testing.T, files ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for i := 0; i+1 < len(files); i += 2 {
		h := &zip.FileHeader{Name: files[i], Method: zip.Deflate}
		if !strings.HasSuffix(files[i], "/") {
			h.SetMode(0o755)
		}
		fw, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write([]byte(files[i+1]))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// uploadPak sends an archive with the "pak" target and completes it; it
// returns the job id.
func (f *fixture) uploadPak(data []byte, target map[string]any, onConflict string) string {
	f.t.Helper()
	target["kind"] = "pak"
	c, _ := f.createUpload(map[string]any{"target": target, "sizeBytes": len(data), "sha256": sha(data), "onConflict": onConflict}, http.StatusCreated)
	for off := 0; off < len(data); off += c.ChunkSize {
		if r := f.chunk(c.UploadID, off, data[off:min(off+c.ChunkSize, len(data))]); r.Status != http.StatusNoContent {
			f.t.Fatalf("chunk: %d %s", r.Status, r.Body)
		}
	}
	var res struct {
		JobID string `json:"jobId"`
	}
	f.do(http.MethodPost, "/api/v1/uploads/"+c.UploadID+"/complete", "/uploads/{uploadId}/complete", nil, http.StatusOK).JSON(f.t, &res)
	if res.JobID == "" {
		f.t.Fatal("complete of a pak upload without jobId")
	}
	return res.JobID
}

type pakJSON struct {
	ID      string  `json:"pakId"`
	Name    string  `json:"name"`
	Kind    string  `json:"kind"`
	Path    string  `json:"path"`
	Size    int64   `json:"sizeBytes"`
	Version *string `json:"version"`
	Self    bool    `json:"self"`
}

func (f *fixture) listPaks() map[string]pakJSON {
	f.t.Helper()
	var list struct {
		Paks []pakJSON `json:"paks"`
	}
	f.get("/api/v1/paks", "/paks", http.StatusOK).JSON(f.t, &list)
	out := map[string]pakJSON{}
	for _, p := range list.Paks {
		out[p.Path] = p
	}
	return out
}

// noPakStaging checks that no installation left its staging folder.
func (f *fixture) noPakStaging() {
	f.t.Helper()
	des, _ := os.ReadDir(f.layout.StagingDir())
	for _, d := range des {
		if strings.HasPrefix(d.Name(), "pk_") {
			f.t.Fatalf("staging folder left: %s", d.Name())
		}
	}
}

func clockZip(t *testing.T, version string) []byte {
	return pakZip(t, "Clock.pak/", "", "Clock.pak/launch.sh", "#!/bin/sh\necho "+version+"\n",
		"Clock.pak/pak.json", `{"name":"Clock","version":"`+version+`","type":"TOOL"}`)
}

func TestInstallPakFromUpload(t *testing.T) {
	f := newFixture(t)
	f.fastEvents(events.Options{})
	s := f.openEvents(f.client)

	j := f.waitJob(f.uploadPak(clockZip(t, "1.0"), map[string]any{"fileName": "Clock.pak.zip"}, "fail"))
	if j.State != "succeeded" || j.Kind != "pak-install" {
		t.Fatalf("install: %+v", j)
	}
	list := f.listPaks()
	clock, ok := list["Tools/tg5040/Clock.pak"]
	if !ok || clock.ID != j.Result["pakId"] || clock.Kind != "tool" || clock.Version == nil || *clock.Version != "1.0" || clock.Self {
		t.Fatalf("listed %+v (result %v)", list, j.Result)
	}
	if mgba := list["Emus/tg5040/MGBA.pak"]; mgba.Kind != "emu" {
		t.Fatalf("emulator paks: %+v", list)
	}
	// The job event, then the paks event (no library event).
	for {
		ev := f.nextEvent(s)
		if ev.Type == events.TypeJob {
			continue
		}
		if ev.Type != events.TypePaks || ev.Data != "{}" {
			t.Fatalf("event %+v", ev)
		}
		break
	}

	// Same name again: refused with fail, nothing changed.
	j = f.waitJob(f.uploadPak(clockZip(t, "2.0"), map[string]any{}, "fail"))
	if j.State != "failed" || j.Error.Code != "conflict" || j.Error.Details["existing"] != "Tools/tg5040/Clock.pak" {
		t.Fatalf("conflict: %+v", j)
	}
	if got := f.read("Tools/tg5040/Clock.pak/launch.sh"); !strings.Contains(got, "1.0") {
		t.Fatalf("replaced on fail: %q", got)
	}
	// overwrite: the old pak goes to the trash first.
	j = f.waitJob(f.uploadPak(clockZip(t, "2.0"), map[string]any{"pakKind": "auto"}, "overwrite"))
	if j.State != "succeeded" {
		t.Fatalf("replace: %+v", j)
	}
	if got := f.read("Tools/tg5040/Clock.pak/launch.sh"); !strings.Contains(got, "2.0") {
		t.Fatalf("not replaced: %q", got)
	}
	entries, _, err := f.trash.List()
	if err != nil || len(entries) != 1 || entries[0].Reason != "replace-pak" || entries[0].Label != "Clock" ||
		!strings.Contains(strings.Join(entries[0].Files, ","), "Tools/tg5040/Clock.pak/launch.sh") {
		t.Fatalf("trash %+v %v", entries, err)
	}
	f.get("/api/v1/trash", "/trash", http.StatusOK)
	f.noPakStaging()
}

func TestInstallPakKinds(t *testing.T) {
	f := newFixture(t)
	// The layout of the archive gives the kind.
	j := f.waitJob(f.uploadPak(pakZip(t, "Emus/tg5040/GBA.pak/launch.sh", "x"), map[string]any{}, "fail"))
	if j.State != "succeeded" || !f.exists("Emus/tg5040/GBA.pak/launch.sh") {
		t.Fatalf("emu from layout: %+v", j)
	}
	// pak.json type EMU.
	j = f.waitJob(f.uploadPak(pakZip(t, "PS.pak/launch.sh", "x", "PS.pak/pak.json", `{"type":"EMU"}`), map[string]any{}, "fail"))
	if j.State != "succeeded" || !f.exists("Emus/tg5040/PS.pak/launch.sh") {
		t.Fatalf("emu from pak.json: %+v", j)
	}
	// An explicit kind wins; no hint: a tool.
	j = f.waitJob(f.uploadPak(pakZip(t, "Emus/tg5040/MD.pak/launch.sh", "x"), map[string]any{"pakKind": "tool"}, "fail"))
	if j.State != "succeeded" || !f.exists("Tools/tg5040/MD.pak/launch.sh") {
		t.Fatalf("explicit kind: %+v", j)
	}
	j = f.waitJob(f.uploadPak(pakZip(t, "Plain.pak/launch.sh", "x"), map[string]any{}, "fail"))
	if j.State != "succeeded" || !f.exists("Tools/tg5040/Plain.pak/launch.sh") {
		t.Fatalf("default kind: %+v", j)
	}
}

func TestInstallPakRefusesBadArchives(t *testing.T) {
	f := newFixture(t)
	for name, data := range map[string][]byte{
		"zip-slip":  pakZip(t, "Evil.pak/launch.sh", "x", "Evil.pak/../../../../Roms/evil.gba", "x"),
		"two paks":  pakZip(t, "A.pak/launch.sh", "x", "B.pak/launch.sh", "x"),
		"no launch": pakZip(t, "A.pak/run.sh", "x"),
		"not a zip": []byte("plain text"),
	} {
		j := f.waitJob(f.uploadPak(data, map[string]any{}, "fail"))
		if j.State != "failed" || j.Error.Code != "unsupported_format" {
			t.Fatalf("%s: %+v", name, j)
		}
	}
	if f.exists("Roms/evil.gba") || f.exists("Tools/tg5040/Evil.pak") || f.exists("Tools/tg5040/A.pak") {
		t.Fatal("something was installed")
	}
	f.noPakStaging()
}

func TestUploadPakTargetChecks(t *testing.T) {
	f := newFixture(t)
	body := func(target map[string]any, size int, onConflict string) map[string]any {
		target["kind"] = "pak"
		return map[string]any{"target": target, "sizeBytes": size, "sha256": sha(nil), "onConflict": onConflict}
	}
	_, r := f.createUpload(body(map[string]any{}, 10, "rename"), http.StatusBadRequest)
	wantCode(t, r, "invalid_request")
	_, r = f.createUpload(body(map[string]any{"pakKind": "app"}, 10, "fail"), http.StatusBadRequest)
	wantCode(t, r, "invalid_request")
	_, r = f.createUpload(body(map[string]any{}, paks.MaxArchiveBytes+1, "fail"), http.StatusRequestEntityTooLarge)
	wantCode(t, r, "too_large")
	_, r = f.createUpload(body(map[string]any{"fileName": "../x.zip"}, 10, "fail"), http.StatusBadRequest)
	wantCode(t, r, "invalid_name")
}

func TestSelfPakIsProtected(t *testing.T) {
	f := newFixture(t)
	f.writeSD("Tools/tg5040/ReminisLink.pak/launch.sh", "#!/bin/sh\n")
	self := f.listPaks()["Tools/tg5040/ReminisLink.pak"]
	if !self.Self {
		t.Fatalf("self %+v", self)
	}
	r := f.do(http.MethodDelete, "/api/v1/paks/"+self.ID, "/paks/{pakId}", nil, http.StatusConflict)
	wantCode(t, r, "conflict")
	// Neither replaced (whatever the case of its name).
	j := f.waitJob(f.uploadPak(pakZip(t, "reminislink.PAK/launch.sh", "evil"), map[string]any{}, "overwrite"))
	if j.State != "failed" || j.Error.Code != "conflict" || j.Error.Details["self"] != true {
		t.Fatalf("self replaced: %+v", j)
	}
	if got := f.read("Tools/tg5040/ReminisLink.pak/launch.sh"); got != "#!/bin/sh\n" {
		t.Fatalf("self changed: %q", got)
	}
	if entries, _, _ := f.trash.List(); len(entries) != 0 {
		t.Fatalf("trash %+v", entries)
	}
}

func TestDeletePak(t *testing.T) {
	f := newFixture(t)
	f.fastEvents(events.Options{})
	f.writeSD("Tools/tg5040/Clock.pak/launch.sh", "x")
	f.writeSD("Tools/tg5040/Clock.pak/res/font.ttf", "font")
	s := f.openEvents(f.client)
	clock := f.listPaks()["Tools/tg5040/Clock.pak"]
	var res struct {
		TrashEntryID string   `json:"trashEntryId"`
		Moved        []string `json:"moved"`
		MovedBytes   int64    `json:"movedBytes"`
	}
	f.do(http.MethodDelete, "/api/v1/paks/"+clock.ID, "/paks/{pakId}", nil, http.StatusOK).JSON(t, &res)
	if len(res.Moved) != 2 || res.MovedBytes != 5 || f.exists("Tools/tg5040/Clock.pak") {
		t.Fatalf("deleted %+v", res)
	}
	entries, _, _ := f.trash.List()
	if len(entries) != 1 || entries[0].Reason != "delete-pak" || entries[0].ID != res.TrashEntryID {
		t.Fatalf("trash %+v", entries)
	}
	if ev := f.nextEvent(s); ev.Type != events.TypePaks {
		t.Fatalf("event %+v", ev)
	}
	// Gone, malformed, not a pak folder.
	f.do(http.MethodDelete, "/api/v1/paks/"+clock.ID, "/paks/{pakId}", nil, http.StatusNotFound)
	f.do(http.MethodDelete, "/api/v1/paks/pk_"+library.EncodeID("Roms/x.pak"), "/paks/{pakId}", nil, http.StatusBadRequest)
	f.do(http.MethodDelete, "/api/v1/paks/nope", "/paks/{pakId}", nil, http.StatusBadRequest)
}

// sdEntry finds the sdEntryId of a path by browsing from the root.
func (f *fixture) sdEntry(rel string) string {
	f.t.Helper()
	folder := ""
	parts := strings.Split(rel, "/")
	for i, name := range parts {
		target := "/api/v1/sd/browse"
		if folder != "" {
			target += "?folderId=" + url.QueryEscape(folder)
		}
		var list library.SDFolder
		f.get(target, "/sd/browse", http.StatusOK).JSON(f.t, &list)
		found := ""
		for _, e := range list.Entries {
			if e.Name == name {
				found = e.ID
			}
		}
		if found == "" {
			f.t.Fatalf("%s not listed in %q", name, strings.Join(parts[:i], "/"))
		}
		folder = found
	}
	return folder
}

func (f *fixture) installFromSD(body map[string]any, want int) testutil.Response {
	f.t.Helper()
	return f.do(http.MethodPost, "/api/v1/paks/install", "/paks/install", mustJSON(f.t, body), want)
}

func jobID(t *testing.T, r testutil.Response) string {
	t.Helper()
	var accepted struct {
		JobID string `json:"jobId"`
	}
	r.JSON(t, &accepted)
	return accepted.JobID
}

func TestInstallPakFromSD(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(filepath.Join(f.sd, "Clock.pak.zip"), clockZip(t, "3.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.writeSD("Downloads/GBA.pak/launch.sh", "x")
	f.writeSD("Downloads/GBA.pak/pak.json", `{"type":"EMU","version":"0.1"}`)
	f.writeSD("Downloads/notes.txt", "x")
	f.writeSD("Downloads/Folder/launch.sh", "x")

	j := f.waitJob(jobID(t, f.installFromSD(map[string]any{"sdEntryId": f.sdEntry("Clock.pak.zip")}, http.StatusAccepted)))
	if j.State != "succeeded" || j.Kind != "pak-install" || !f.exists("Tools/tg5040/Clock.pak/pak.json") {
		t.Fatalf("zip from SD: %+v", j)
	}
	if !f.exists("Clock.pak.zip") {
		t.Fatal("the source archive was removed")
	}
	j = f.waitJob(jobID(t, f.installFromSD(map[string]any{"sdEntryId": f.sdEntry("Downloads/GBA.pak"), "kind": "auto", "onConflict": "fail"}, http.StatusAccepted)))
	if j.State != "succeeded" || !f.exists("Emus/tg5040/GBA.pak/launch.sh") || !f.exists("Downloads/GBA.pak/launch.sh") {
		t.Fatalf("folder from SD: %+v", j)
	}
	// An installed pak copied onto itself (replace): the old one goes to
	// the trash, the copy takes its place; its kind comes from its folder.
	j = f.waitJob(jobID(t, f.installFromSD(map[string]any{"sdEntryId": f.sdEntry("Tools/tg5040/Clock.pak"), "onConflict": "replace"}, http.StatusAccepted)))
	if j.State != "succeeded" || !f.exists("Tools/tg5040/Clock.pak/launch.sh") {
		t.Fatalf("self copy: %+v", j)
	}
	// A file that is not an archive fails its job; refusals at once.
	j = f.waitJob(jobID(t, f.installFromSD(map[string]any{"sdEntryId": f.sdEntry("Downloads/notes.txt")}, http.StatusAccepted)))
	if j.State != "failed" || j.Error.Code != "unsupported_format" {
		t.Fatalf("text file: %+v", j)
	}
	wantCode(t, f.installFromSD(map[string]any{"sdEntryId": f.sdEntry("Downloads/Folder")}, http.StatusUnprocessableEntity), "unsupported_format")
	wantCode(t, f.installFromSD(map[string]any{"sdEntryId": library.SDEntryID("Downloads/Missing.zip")}, http.StatusNotFound), "not_found")
	wantCode(t, f.installFromSD(map[string]any{"sdEntryId": "se_" + library.EncodeID("../x.zip")}, http.StatusBadRequest), "invalid_id")
	wantCode(t, f.installFromSD(map[string]any{"sdEntryId": library.SDEntryID(".userdata/shared/ReminisLink/clients.json")}, http.StatusNotFound), "not_found")
	wantCode(t, f.installFromSD(map[string]any{}, http.StatusBadRequest), "invalid_request")
	wantCode(t, f.installFromSD(map[string]any{"sdEntryId": f.sdEntry("Clock.pak.zip"), "kind": "app"}, http.StatusBadRequest), "invalid_request")
	wantCode(t, f.installFromSD(map[string]any{"sdEntryId": f.sdEntry("Clock.pak.zip"), "onConflict": "overwrite"}, http.StatusBadRequest), "invalid_request")
	f.noPakStaging()
}

func TestInstallPakFromDownload(t *testing.T) {
	f := newFixture(t)
	data := clockZip(t, "4.0")
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	}))
	defer src.Close()
	body := mustJSON(t, map[string]any{"url": src.URL + "/Clock.pak.zip", "target": map[string]any{"kind": "pak", "pakKind": "tool"}, "onConflict": "fail"})
	j := f.waitJob(jobID(t, f.do(http.MethodPost, "/api/v1/downloads", "/downloads", body, http.StatusAccepted)))
	if j.State != "succeeded" || j.Kind != "download" || j.Result["pakId"] == nil || !f.exists("Tools/tg5040/Clock.pak/launch.sh") {
		t.Fatalf("download: %+v", j)
	}
	des, _ := os.ReadDir(f.layout.UploadsDir())
	for _, d := range des {
		if strings.HasPrefix(d.Name(), "dl_") {
			t.Fatalf("downloaded archive left: %s", d.Name())
		}
	}
	bad := mustJSON(t, map[string]any{"url": src.URL + "/x.zip", "target": map[string]any{"kind": "pak"}, "onConflict": "rename"})
	wantCode(t, f.do(http.MethodPost, "/api/v1/downloads", "/downloads", bad, http.StatusBadRequest), "invalid_request")
}

func TestInstallPakOfARevokedClient(t *testing.T) {
	f := newFixture(t)
	b := f.pairNamed("iPhone B")
	if err := os.WriteFile(filepath.Join(f.sd, "Clock.pak.zip"), clockZip(t, "1.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	id := f.sdEntry("Clock.pak.zip")
	f.holdWrites(20 * time.Millisecond)
	r := f.as(b, http.MethodPost, "/api/v1/paks/install", "/paks/install", mustJSON(t, map[string]any{"sdEntryId": id}), http.StatusAccepted)
	f.waitWriteWaiter() // extracted, waits for its install
	if _, err := f.server.Forget(b.ClientID); err != nil {
		t.Fatal(err)
	}
	f.server.writes().Release()
	if j := f.waitJob(jobID(t, r)); j.State != "cancelled" {
		t.Fatalf("install of a revoked client: %+v", j)
	}
	if f.exists("Tools/tg5040/Clock.pak") {
		t.Fatal("installed for a revoked client")
	}
	f.noPakStaging()
}

func TestDeletePakWaitsForTheWriteLock(t *testing.T) {
	f := newFixture(t)
	f.writeSD("Tools/tg5040/Clock.pak/launch.sh", "x")
	clock := f.listPaks()["Tools/tg5040/Clock.pak"]
	f.holdWrites(20 * time.Millisecond)
	r := f.do(http.MethodDelete, "/api/v1/paks/"+clock.ID, "/paks/{pakId}", nil, http.StatusTooManyRequests)
	wantBusy(t, r)
	f.server.writes().Release()
	if !f.exists("Tools/tg5040/Clock.pak/launch.sh") {
		t.Fatal("deleted while busy")
	}
}

func TestBrowseSDOverHTTP(t *testing.T) {
	f := newFixture(t)
	f.writeSD(".userdata/shared/ReminisLink/tls/key.pem", "secret")
	var root library.SDFolder
	f.get("/api/v1/sd/browse", "/sd/browse", http.StatusOK).JSON(t, &root)
	if root.FolderID != nil || root.ParentID != nil || len(root.Entries) == 0 {
		t.Fatalf("root %+v", root)
	}
	readme := f.sdEntry("README.md")
	r := f.get("/api/v1/sd/browse/"+readme+"/content", "/sd/browse/{sdEntryId}/content", http.StatusOK)
	want, _ := os.ReadFile(filepath.Join(f.sd, "README.md"))
	if !bytes.Equal(r.Body, want) || !strings.Contains(r.Header.Get("Content-Disposition"), "README.md") {
		t.Fatalf("content %q %v", r.Body, r.Header)
	}
	shared := f.sdEntry(".userdata/shared")
	var list library.SDFolder
	f.get("/api/v1/sd/browse?folderId="+shared, "/sd/browse", http.StatusOK).JSON(t, &list)
	for _, e := range list.Entries {
		if strings.EqualFold(e.Name, "ReminisLink") {
			t.Fatal("ReminisLink data listed")
		}
	}
	for _, rel := range []string{".userdata/shared/ReminisLink", ".userdata/shared/ReminisLink/tls/key.pem"} {
		id := library.SDEntryID(rel)
		f.get("/api/v1/sd/browse?folderId="+id, "/sd/browse", http.StatusNotFound)
		f.get("/api/v1/sd/browse/"+id+"/content", "/sd/browse/{sdEntryId}/content", http.StatusNotFound)
	}
	// A folder has no content; a file is not a folder.
	f.get("/api/v1/sd/browse/"+shared+"/content", "/sd/browse/{sdEntryId}/content", http.StatusNotFound)
	f.get("/api/v1/sd/browse?folderId="+readme, "/sd/browse", http.StatusNotFound)
	// Traversal and malformed identifiers.
	f.get("/api/v1/sd/browse?folderId=se_"+library.EncodeID("../etc"), "/sd/browse", http.StatusBadRequest)
	f.get("/api/v1/sd/browse?folderId=", "/sd/browse", http.StatusBadRequest)
	f.get("/api/v1/sd/browse/se_"+library.EncodeID("/etc/passwd")+"/content", "/sd/browse/{sdEntryId}/content", http.StatusBadRequest)
	// A link is never followed.
	if err := os.Symlink(filepath.Join(f.sd, "README.md"), filepath.Join(f.sd, "link.md")); err == nil {
		f.get("/api/v1/sd/browse/"+library.SDEntryID("link.md")+"/content", "/sd/browse/{sdEntryId}/content", http.StatusNotFound)
	}
}

// Review B: loose archives (launch.sh at the top) are named by their
// pak.json, else by the archive name.
func TestInstallLooseArchives(t *testing.T) {
	f := newFixture(t)
	loose := pakZip(t, "launch.sh", "#!/bin/sh\n", "bin/tool", "x")
	j := f.waitJob(f.uploadPak(loose, map[string]any{"fileName": "Loose Tool.zip"}, "fail"))
	if j.State != "succeeded" || !f.exists("Tools/tg5040/Loose Tool.pak/bin/tool") {
		t.Fatalf("named by the upload: %+v", j)
	}
	named := pakZip(t, "launch.sh", "x", "pak.json", `{"name":"Fancy","type":"EMU"}`)
	j = f.waitJob(f.uploadPak(named, map[string]any{"fileName": "whatever.zip"}, "fail"))
	if j.State != "succeeded" || !f.exists("Emus/tg5040/Fancy.pak/launch.sh") {
		t.Fatalf("named by pak.json: %+v", j)
	}
	if err := os.WriteFile(filepath.Join(f.sd, "Clock.pak.zip"), loose, 0o644); err != nil {
		t.Fatal(err)
	}
	j = f.waitJob(jobID(t, f.installFromSD(map[string]any{"sdEntryId": f.sdEntry("Clock.pak.zip")}, http.StatusAccepted)))
	if j.State != "succeeded" || !f.exists("Tools/tg5040/Clock.pak/launch.sh") {
		t.Fatalf("named by the SD entry: %+v", j)
	}
	// No name at all: refused.
	j = f.waitJob(f.uploadPak(loose, map[string]any{}, "fail"))
	if j.State != "failed" || j.Error.Code != "unsupported_format" {
		t.Fatalf("no name: %+v", j)
	}
	f.noPakStaging()
}
