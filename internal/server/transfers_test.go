package server

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

const gbaFolder = "Game Boy Advance (GBA)"

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// do sends a signed request, validates it against the contract and checks
// its status.
func (f *fixture) do(method, target, path string, body []byte, want int) testutil.Response {
	f.t.Helper()
	r := f.client.Do(f.t, method, target, body)
	f.check(method, path, r)
	if r.Status != want {
		f.t.Fatalf("%s %s: status %d, want %d: %s", method, target, r.Status, want, r.Body)
	}
	return r
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// chunk sends an upload chunk signed UNSIGNED.
func (f *fixture) chunk(uploadID string, offset int, data []byte) testutil.Response {
	f.t.Helper()
	req := f.client.RequestUnsigned(f.t, http.MethodPut, "/api/v1/uploads/"+uploadID, data)
	req.Header.Set("Upload-Offset", strconv.Itoa(offset))
	req.Header.Set("Content-Type", "application/octet-stream")
	r := f.client.Send(f.t, req)
	f.check(http.MethodPut, "/uploads/{uploadId}", r)
	return r
}

type uploadCreated struct {
	UploadID  string `json:"uploadId"`
	ChunkSize int    `json:"chunkSize"`
	Offset    int64  `json:"offset"`
}

func (f *fixture) createUpload(body map[string]any, want int) (uploadCreated, testutil.Response) {
	f.t.Helper()
	r := f.do(http.MethodPost, "/api/v1/uploads", "/uploads", mustJSON(f.t, body), want)
	var c uploadCreated
	if want == http.StatusCreated {
		r.JSON(f.t, &c)
	}
	return c, r
}

func TestUploadOverHTTP(t *testing.T) {
	f := newFixture(t)
	data := make([]byte, 200_000)
	rand.Read(data)
	target := map[string]any{"kind": "rom", "systemId": library.EncodeID(gbaFolder), "folderId": nil, "fileName": "Sky Garden Quest DX.gba"}
	c, _ := f.createUpload(map[string]any{"target": target, "sizeBytes": len(data), "sha256": sha(data), "onConflict": "fail"}, http.StatusCreated)
	if c.ChunkSize != 64<<10 || c.Offset != 0 {
		t.Fatalf("created %+v", c)
	}

	// First chunk, then a wrong offset.
	if r := f.chunk(c.UploadID, 0, data[:c.ChunkSize]); r.Status != http.StatusNoContent || r.Header.Get("Upload-Offset") != strconv.Itoa(c.ChunkSize) {
		t.Fatalf("chunk: %d %s", r.Status, r.Body)
	}
	r := f.chunk(c.UploadID, 0, data[:10])
	var e struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	r.JSON(t, &e)
	if r.Status != http.StatusConflict || e.Error.Code != "conflict" || e.Error.Details["offset"] != float64(c.ChunkSize) {
		t.Fatalf("wrong offset: %d %s", r.Status, r.Body)
	}
	// Larger than the chunk size: refused before reading.
	if r := f.chunk(c.UploadID, c.ChunkSize, data[c.ChunkSize:3*c.ChunkSize]); r.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("big chunk: %d", r.Status)
	}
	// Missing Upload-Offset.
	req := f.client.RequestUnsigned(t, http.MethodPut, "/api/v1/uploads/"+c.UploadID, data[:10])
	if r := f.client.Send(t, req); r.Status != http.StatusBadRequest || r.ErrorCode() != "invalid_request" {
		t.Fatalf("no offset: %d %s", r.Status, r.Body)
	}
	// Resume from GET.
	var st struct {
		Offset    int64 `json:"offset"`
		SizeBytes int64 `json:"sizeBytes"`
	}
	f.do(http.MethodGet, "/api/v1/uploads/"+c.UploadID, "/uploads/{uploadId}", nil, http.StatusOK).JSON(t, &st)
	if st.Offset != int64(c.ChunkSize) || st.SizeBytes != int64(len(data)) {
		t.Fatalf("status %+v", st)
	}
	// A chunk sent with its real SHA-256 is accepted too.
	off := c.ChunkSize
	signed := f.client.Request(t, http.MethodPut, "/api/v1/uploads/"+c.UploadID, data[off:off+c.ChunkSize])
	signed.Header.Set("Upload-Offset", strconv.Itoa(off))
	if r := f.client.Send(t, signed); r.Status != http.StatusNoContent {
		t.Fatalf("signed chunk: %d %s", r.Status, r.Body)
	}
	for off += c.ChunkSize; off < len(data); off += c.ChunkSize {
		end := min(off+c.ChunkSize, len(data))
		if r := f.chunk(c.UploadID, off, data[off:end]); r.Status != http.StatusNoContent {
			t.Fatalf("chunk at %d: %d %s", off, r.Status, r.Body)
		}
	}
	var res struct {
		GameID string `json:"gameId"`
	}
	f.do(http.MethodPost, "/api/v1/uploads/"+c.UploadID+"/complete", "/uploads/{uploadId}/complete", nil, http.StatusOK).JSON(t, &res)
	if res.GameID != library.EncodeID(gbaFolder+"/Sky Garden Quest DX.gba") {
		t.Fatalf("gameId %q", res.GameID)
	}
	got, err := os.ReadFile(filepath.Join(f.sd, "Roms", gbaFolder, "Sky Garden Quest DX.gba"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("installed file: %v", err)
	}
	// The new game is listed.
	f.do(http.MethodGet, "/api/v1/games/"+res.GameID, "/games/{gameId}", nil, http.StatusOK)
	f.do(http.MethodGet, "/api/v1/uploads/"+c.UploadID, "/uploads/{uploadId}", nil, http.StatusNotFound)
	f.do(http.MethodGet, "/api/v1/uploads/not-an-id", "/uploads/{uploadId}", nil, http.StatusBadRequest)
}

func TestUploadErrorsOverHTTP(t *testing.T) {
	f := newFixture(t)
	target := map[string]any{"kind": "rom", "systemId": library.EncodeID(gbaFolder), "fileName": "Bad.gba"}

	// Checksum mismatch: 409 and the upload is gone.
	c, _ := f.createUpload(map[string]any{"target": target, "sizeBytes": 3, "sha256": sha([]byte("abd"))}, http.StatusCreated)
	if r := f.chunk(c.UploadID, 0, []byte("abc")); r.Status != http.StatusNoContent {
		t.Fatalf("chunk %d", r.Status)
	}
	r := f.do(http.MethodPost, "/api/v1/uploads/"+c.UploadID+"/complete", "/uploads/{uploadId}/complete", nil, http.StatusConflict)
	if r.ErrorCode() != "checksum_mismatch" {
		t.Fatalf("code %s", r.ErrorCode())
	}
	f.do(http.MethodDelete, "/api/v1/uploads/"+c.UploadID, "/uploads/{uploadId}", nil, http.StatusNotFound)

	// Cancel.
	c, _ = f.createUpload(map[string]any{"target": target, "sizeBytes": 3, "sha256": sha([]byte("abc"))}, http.StatusCreated)
	f.do(http.MethodDelete, "/api/v1/uploads/"+c.UploadID, "/uploads/{uploadId}", nil, http.StatusNoContent)

	// Not enough space: 507 with required and available.
	f.free.Store(100)
	_, r = f.createUpload(map[string]any{"target": target, "sizeBytes": 1000, "sha256": sha(nil)}, http.StatusInsufficientStorage)
	if !strings.Contains(string(r.Body), `"required":1050`) || !strings.Contains(string(r.Body), `"available":100`) {
		t.Fatalf("507 body %s", r.Body)
	}
	f.free.Store(1 << 40)

	// Conflict at creation, with the existing file.
	exists := map[string]any{"kind": "rom", "systemId": library.EncodeID(gbaFolder), "fileName": "Starlight Courier.gba"}
	_, r = f.createUpload(map[string]any{"target": exists, "sizeBytes": 1, "sha256": sha([]byte{1})}, http.StatusConflict)
	if !strings.Contains(string(r.Body), `"existing":"Roms/Game Boy Advance (GBA)/Starlight Courier.gba"`) {
		t.Fatalf("409 body %s", r.Body)
	}
	// Invalid name, invalid id, unknown game, invalid JSON.
	_, r = f.createUpload(map[string]any{"target": map[string]any{"kind": "staging", "fileName": "../x"}, "sizeBytes": 1, "sha256": sha([]byte{1})}, http.StatusBadRequest)
	if r.ErrorCode() != "invalid_name" {
		t.Fatalf("code %s", r.ErrorCode())
	}
	_, r = f.createUpload(map[string]any{"target": map[string]any{"kind": "save", "gameId": "Li4vZXRj"}, "sizeBytes": 1, "sha256": sha([]byte{1})}, http.StatusBadRequest)
	if r.ErrorCode() != "invalid_id" {
		t.Fatalf("code %s", r.ErrorCode())
	}
	f.createUpload(map[string]any{"target": map[string]any{"kind": "save", "gameId": library.EncodeID(gbaFolder + "/Nope.gba")}, "sizeBytes": 1, "sha256": sha([]byte{1})}, http.StatusNotFound)
	f.do(http.MethodPost, "/api/v1/uploads", "/uploads", []byte("{"), http.StatusBadRequest)
	f.do(http.MethodPost, "/api/v1/uploads", "/uploads", []byte("{}"), http.StatusBadRequest)

	// UNSIGNED is refused outside the chunk route.
	req := f.client.RequestUnsigned(t, http.MethodPost, "/api/v1/uploads", []byte("{}"))
	if r := f.client.Send(t, req); r.Status != http.StatusUnauthorized || r.ErrorCode() != "bad_signature" {
		t.Fatalf("UNSIGNED on POST: %d %s", r.Status, r.Body)
	}
}

func TestSaveUploadOverHTTP(t *testing.T) {
	f := newFixture(t)
	data := []byte("new save")
	game := library.EncodeID(gbaFolder + "/Starlight Courier.gba")
	c, _ := f.createUpload(map[string]any{"target": map[string]any{"kind": "save", "gameId": game}, "sizeBytes": len(data), "sha256": sha(data)}, http.StatusCreated)
	f.chunk(c.UploadID, 0, data)
	r := f.do(http.MethodPost, "/api/v1/uploads/"+c.UploadID+"/complete", "/uploads/{uploadId}/complete", nil, http.StatusOK)
	if string(bytes.TrimSpace(r.Body)) != `{"backup":null}` {
		t.Fatalf("body %s", r.Body)
	}
	data = []byte("state")
	c, _ = f.createUpload(map[string]any{"target": map[string]any{"kind": "state", "gameId": game, "slot": 3}, "sizeBytes": len(data), "sha256": sha(data)}, http.StatusCreated)
	f.chunk(c.UploadID, 0, data)
	r = f.do(http.MethodPost, "/api/v1/uploads/"+c.UploadID+"/complete", "/uploads/{uploadId}/complete", nil, http.StatusOK)
	if !strings.Contains(string(r.Body), `"slot":{"slot":3,`) {
		t.Fatalf("body %s", r.Body)
	}
	data = []byte("patch")
	c, _ = f.createUpload(map[string]any{"target": map[string]any{"kind": "staging", "fileName": "x.ips"}, "sizeBytes": len(data), "sha256": sha(data)}, http.StatusCreated)
	f.chunk(c.UploadID, 0, data)
	r = f.do(http.MethodPost, "/api/v1/uploads/"+c.UploadID+"/complete", "/uploads/{uploadId}/complete", nil, http.StatusOK)
	if !strings.Contains(string(r.Body), `"stagingId":"st_`) {
		t.Fatalf("body %s", r.Body)
	}
}

type trashList struct {
	Entries []struct {
		ID        string   `json:"id"`
		Reason    string   `json:"reason"`
		Label     string   `json:"label"`
		Files     []string `json:"files"`
		SizeBytes int64    `json:"sizeBytes"`
	} `json:"entries"`
	TotalBytes int64 `json:"totalBytes"`
}

type deleteResult struct {
	TrashEntryID string   `json:"trashEntryId"`
	Moved        []string `json:"moved"`
	MovedBytes   int64    `json:"movedBytes"`
}

func (f *fixture) exists(rel string) bool {
	_, err := os.Lstat(filepath.Join(f.sd, filepath.FromSlash(rel)))
	return err == nil
}

func TestDeleteGameAndTrash(t *testing.T) {
	f := newFixture(t)
	// A lock and a resume history: moved with the game.
	lock := filepath.Join(f.layout.LocksDir("GBA"), "Starlight Courier.gba", "slot0.st")
	resume := filepath.Join(f.layout.BackupsDir("GBA"), "Starlight Courier.gba", "resume", "rh_1.st")
	savedBackup := filepath.Join(f.layout.BackupsDir("GBA"), "Starlight Courier.gba", "saves", "bk_1.sav")
	for _, p := range []string{lock, resume, savedBackup} {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("copy"), 0o644)
	}

	game := library.EncodeID(gbaFolder + "/Starlight Courier.gba")
	var detail struct {
		TotalBytes int64 `json:"totalBytes"`
	}
	f.do(http.MethodGet, "/api/v1/games/"+game, "/games/{gameId}", nil, http.StatusOK).JSON(t, &detail)
	var res deleteResult
	f.do(http.MethodDelete, "/api/v1/games/"+game, "/games/{gameId}", nil, http.StatusOK).JSON(t, &res)
	sort.Strings(res.Moved)
	want := []string{
		".userdata/shared/.minui/GBA/Starlight Courier.gba.st0",
		".userdata/shared/.minui/GBA/Starlight Courier.gba.st1",
		".userdata/shared/.minui/GBA/Starlight Courier.gba.st9",
		"Cheats/GBA/Starlight Courier.gba.cht",
		"Roms/" + gbaFolder + "/.media/Starlight Courier.png",
		"Roms/" + gbaFolder + "/Starlight Courier.gba",
		"Saves/GBA/Starlight Courier.gba.sav",
	}
	if strings.Join(res.Moved, "|") != strings.Join(want, "|") || res.MovedBytes != detail.TotalBytes {
		t.Fatalf("moved %v (%d bytes, game %d)", res.Moved, res.MovedBytes, detail.TotalBytes)
	}
	for _, rel := range want {
		if f.exists(rel) {
			t.Errorf("%s still in place", rel)
		}
		if _, err := os.Stat(filepath.Join(f.trash.Dir(), res.TrashEntryID, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s not in the trash: %v", rel, err)
		}
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Error("lock kept in place")
	}
	if _, err := os.Stat(resume); !os.IsNotExist(err) {
		t.Error("resume history kept in place")
	}
	if _, err := os.Stat(savedBackup); err != nil {
		t.Error("named save backups must stay")
	}
	f.do(http.MethodGet, "/api/v1/games/"+game, "/games/{gameId}", nil, http.StatusNotFound)
	if !strings.Contains(f.logs.String(), "moved to the trash") {
		t.Error("destructive operation not logged")
	}

	// keepSave: the save and the states stay.
	pixel := library.EncodeID(gbaFolder + "/Pixel Garden 🌱.gba")
	f.do(http.MethodDelete, "/api/v1/games/"+pixel+"?keepSave=true", "/games/{gameId}", nil, http.StatusOK).JSON(t, &res)
	if !f.exists(".userdata/shared/.minui/GBA/Pixel Garden 🌱.gba.st9") || f.exists("Cheats/GBA/Pixel Garden 🌱.gba.cht") {
		t.Fatalf("keepSave: %v", res.Moved)
	}
	f.do(http.MethodDelete, "/api/v1/games/"+pixel+"?keepSave=maybe", "/games/{gameId}", nil, http.StatusBadRequest)

	// Multi-disc games: the folder of a folder-m3u game, the discs of a
	// loose .m3u.
	ps := "Sony PlayStation (PS)"
	f.do(http.MethodDelete, "/api/v1/games/"+library.EncodeID(ps+"/Mossy Hollow"), "/games/{gameId}", nil, http.StatusOK).JSON(t, &res)
	if f.exists("Roms/"+ps+"/Mossy Hollow") || f.exists("Saves/PS/Mossy Hollow.m3u.sav") || len(res.Moved) < 5 {
		t.Fatalf("folder-m3u: %v", res.Moved)
	}
	f.do(http.MethodDelete, "/api/v1/games/"+library.EncodeID(ps+"/Quiet Harbor.m3u"), "/games/{gameId}", nil, http.StatusOK).JSON(t, &res)
	for _, n := range []string{"Quiet Harbor.m3u", "Quiet Harbor (Disc 1).cue", "Quiet Harbor (Disc 2).cue"} {
		if f.exists("Roms/" + ps + "/" + n) {
			t.Errorf("%s left", n)
		}
	}
	f.do(http.MethodDelete, "/api/v1/games/Li4vZXRj", "/games/{gameId}", nil, http.StatusBadRequest)

	// The trash.
	var list trashList
	f.do(http.MethodGet, "/api/v1/trash", "/trash", nil, http.StatusOK).JSON(t, &list)
	if len(list.Entries) != 4 || list.Entries[0].Reason != "delete-game" {
		t.Fatalf("trash %+v", list)
	}
	first := list.Entries[0] // same second (fixed clock): find it by label
	for _, e := range list.Entries {
		if e.Label == "Starlight Courier" {
			first = e
		}
	}
	if first.Label != "Starlight Courier" || len(first.Files) != len(want) {
		t.Fatalf("entry %+v", first)
	}

	// Permanent deletion needs the confirmation header.
	f.do(http.MethodDelete, "/api/v1/trash/"+first.ID, "/trash/{entryId}", nil, http.StatusBadRequest)
	confirm := func(method, target, path string, want int) testutil.Response {
		req := f.client.Request(t, method, target, nil)
		req.Header.Set("X-RL-Confirm", "empty-trash")
		r := f.client.Send(t, req)
		f.check(method, path, r)
		if r.Status != want {
			t.Fatalf("%s %s: %d %s", method, target, r.Status, r.Body)
		}
		return r
	}
	var freed struct {
		FreedBytes int64 `json:"freedBytes"`
	}
	confirm(http.MethodDelete, "/api/v1/trash/"+first.ID, "/trash/{entryId}", http.StatusOK).JSON(t, &freed)
	if freed.FreedBytes != first.SizeBytes {
		t.Fatalf("freed %d, entry %d", freed.FreedBytes, first.SizeBytes)
	}
	confirm(http.MethodDelete, "/api/v1/trash/"+first.ID, "/trash/{entryId}", http.StatusNotFound)
	confirm(http.MethodDelete, "/api/v1/trash/nope", "/trash/{entryId}", http.StatusBadRequest)
	f.do(http.MethodDelete, "/api/v1/trash", "/trash", nil, http.StatusBadRequest)
	confirm(http.MethodDelete, "/api/v1/trash", "/trash", http.StatusOK)
	f.do(http.MethodGet, "/api/v1/trash", "/trash", nil, http.StatusOK).JSON(t, &list)
	if len(list.Entries) != 0 || list.TotalBytes != 0 {
		t.Fatalf("trash not empty: %+v", list)
	}
}

func TestDownloadOverHTTP(t *testing.T) {
	f := newFixture(t)
	data := []byte("downloaded patch")
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	}))
	defer src.Close()
	body := mustJSON(t, map[string]any{"url": src.URL + "/hack.bps", "target": map[string]any{"kind": "staging"}, "sha256": nil, "onConflict": "rename"})
	var accepted struct {
		JobID string `json:"jobId"`
	}
	f.do(http.MethodPost, "/api/v1/downloads", "/downloads", body, http.StatusAccepted).JSON(t, &accepted)
	var job struct {
		State  string         `json:"state"`
		Result map[string]any `json:"result"`
	}
	deadline := time.Now().Add(10 * time.Second)
	for job.State != "succeeded" && job.State != "failed" {
		if time.Now().After(deadline) {
			t.Fatalf("job stuck in %s", job.State)
		}
		time.Sleep(10 * time.Millisecond)
		f.do(http.MethodGet, "/api/v1/jobs/"+accepted.JobID, "/jobs/{jobId}", nil, http.StatusOK).JSON(t, &job)
	}
	if job.State != "succeeded" || !strings.HasPrefix(job.Result["stagingId"].(string), "st_") {
		t.Fatalf("job %+v", job)
	}
	f.do(http.MethodGet, "/api/v1/jobs", "/jobs", nil, http.StatusOK)
	// Cancelling a finished job returns it unchanged.
	f.do(http.MethodDelete, "/api/v1/jobs/"+accepted.JobID, "/jobs/{jobId}", nil, http.StatusOK).JSON(t, &job)
	if job.State != "succeeded" {
		t.Fatalf("cancel finished: %s", job.State)
	}
	f.do(http.MethodGet, "/api/v1/jobs/job_000000000000000000000000", "/jobs/{jobId}", nil, http.StatusNotFound)
	f.do(http.MethodGet, "/api/v1/jobs/x", "/jobs/{jobId}", nil, http.StatusBadRequest)
	bad := mustJSON(t, map[string]any{"url": "ftp://example.org/x", "target": map[string]any{"kind": "staging"}})
	f.do(http.MethodPost, "/api/v1/downloads", "/downloads", bad, http.StatusBadRequest)
}

func TestTooManyUploadsOverHTTP(t *testing.T) {
	f := newFixture(t)
	body := map[string]any{"target": map[string]any{"kind": "staging", "fileName": "x.bin"}, "sizeBytes": 1, "sha256": sha([]byte{1})}
	for i := 0; i < 32; i++ {
		f.createUpload(body, http.StatusCreated)
	}
	_, r := f.createUpload(body, http.StatusTooManyRequests)
	if r.ErrorCode() != "too_many_attempts" || r.Header.Get("Retry-After") == "" || !strings.Contains(string(r.Body), "Too many open uploads") {
		t.Fatalf("429: %v %s", r.Header, r.Body)
	}
}
