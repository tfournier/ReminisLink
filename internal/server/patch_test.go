package server

import (
	"bytes"
	"crypto/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

type jobState struct {
	State  string         `json:"state"`
	Kind   string         `json:"kind"`
	Result map[string]any `json:"result"`
	Error  *struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

// waitJob polls GET /jobs/{jobId} (validated) until the job ends.
func (f *fixture) waitJob(id string) jobState {
	f.t.Helper()
	var j jobState
	deadline := time.Now().Add(10 * time.Second)
	for j.State != "succeeded" && j.State != "failed" && j.State != "cancelled" {
		if time.Now().After(deadline) {
			f.t.Fatalf("job stuck in %s", j.State)
		}
		time.Sleep(5 * time.Millisecond)
		f.do(http.MethodGet, "/api/v1/jobs/"+id, "/jobs/{jobId}", nil, http.StatusOK).JSON(f.t, &j)
	}
	return j
}

// stageFile uploads data to the staging area and returns its stagingId.
func (f *fixture) stageFile(name string, data []byte) string {
	f.t.Helper()
	c, _ := f.createUpload(map[string]any{"target": map[string]any{"kind": "staging", "fileName": name}, "sizeBytes": len(data), "sha256": sha(data)}, http.StatusCreated)
	for off := 0; off < len(data); off += c.ChunkSize {
		if r := f.chunk(c.UploadID, off, data[off:min(off+c.ChunkSize, len(data))]); r.Status != http.StatusNoContent {
			f.t.Fatalf("chunk: %d %s", r.Status, r.Body)
		}
	}
	var res struct {
		StagingID string `json:"stagingId"`
	}
	f.do(http.MethodPost, "/api/v1/uploads/"+c.UploadID+"/complete", "/uploads/{uploadId}/complete", nil, http.StatusOK).JSON(f.t, &res)
	return res.StagingID
}

func (f *fixture) patch(game string, body map[string]any, want int) testutil.Response {
	f.t.Helper()
	return f.do(http.MethodPost, "/api/v1/games/"+library.EncodeID(game)+"/patch", "/games/{gameId}/patch", mustJSON(f.t, body), want)
}

func TestPatchOverHTTP(t *testing.T) {
	f := newFixture(t)
	game := gbaFolder + "/Starlight Courier.gba"
	romPath := filepath.Join(f.sd, "Roms", filepath.FromSlash(game))
	src := make([]byte, 150_000)
	rand.Read(src)
	if err := os.WriteFile(romPath, src, 0o644); err != nil {
		t.Fatal(err)
	}
	tgt := append(bytes.Clone(src[:100_000]), "patched tail"...)
	tgt[5] ^= 0xff
	stagingID := f.stageFile("hack.bps", testutil.EncodeBPS(src, tgt))

	// New file.
	var accepted struct {
		JobID string `json:"jobId"`
	}
	f.patch(game, map[string]any{"patch": map[string]any{"type": "staging", "stagingId": stagingID}, "output": map[string]any{"mode": "new-file"}}, http.StatusAccepted).JSON(t, &accepted)
	j := f.waitJob(accepted.JobID)
	newGame := gbaFolder + "/Starlight Courier (patched).gba"
	if j.State != "succeeded" || j.Kind != "patch" || j.Result["gameId"] != library.EncodeID(newGame) {
		t.Fatalf("new-file: %+v", j)
	}
	f.do(http.MethodGet, "/api/v1/games/"+library.EncodeID(newGame), "/games/{gameId}", nil, http.StatusOK)
	if got, _ := os.ReadFile(filepath.Join(f.sd, "Roms", filepath.FromSlash(newGame))); !bytes.Equal(got, tgt) {
		t.Fatal("new file content")
	}

	// Replace: the original goes to the trash.
	f.patch(game, map[string]any{"patch": map[string]any{"type": "staging", "stagingId": stagingID}, "output": map[string]any{"mode": "replace"}}, http.StatusAccepted).JSON(t, &accepted)
	if j := f.waitJob(accepted.JobID); j.State != "succeeded" || j.Result["gameId"] != library.EncodeID(game) {
		t.Fatalf("replace: %+v", j)
	}
	if got, _ := os.ReadFile(romPath); !bytes.Equal(got, tgt) {
		t.Fatal("replaced content")
	}
	r := f.do(http.MethodGet, "/api/v1/trash", "/trash", nil, http.StatusOK)
	if !strings.Contains(string(r.Body), `"replace-rom"`) {
		t.Fatalf("trash: %s", r.Body)
	}

	// Again: the ROM is now the target, refused (job error validated).
	f.patch(game, map[string]any{"patch": map[string]any{"type": "staging", "stagingId": stagingID}}, http.StatusAccepted).JSON(t, &accepted)
	if j := f.waitJob(accepted.JobID); j.State != "failed" || j.Error == nil || j.Error.Code != "patch_mismatch" {
		t.Fatalf("already patched: %+v", j)
	}
	// Wrong source.
	other := f.stageFile("other.bps", testutil.EncodeBPS([]byte("another ROM entirely"), []byte("another target")))
	f.patch(game, map[string]any{"patch": map[string]any{"type": "staging", "stagingId": other}}, http.StatusAccepted).JSON(t, &accepted)
	if j := f.waitJob(accepted.JobID); j.State != "failed" || j.Error.Code != "patch_mismatch" || j.Error.Details["reason"] != "source-mismatch" {
		t.Fatalf("wrong source: %+v", j)
	}
	f.do(http.MethodGet, "/api/v1/jobs", "/jobs", nil, http.StatusOK)
}

func TestPatchErrorsOverHTTP(t *testing.T) {
	f := newFixture(t)
	game := gbaFolder + "/Starlight Courier.gba"
	notPatch := f.stageFile("readme.bps", []byte("this is not a patch"))
	wantCode(t, f.patch(game, map[string]any{"patch": map[string]any{"type": "staging", "stagingId": notPatch}}, http.StatusUnprocessableEntity), "unsupported_format")
	wantCode(t, f.patch(game, map[string]any{}, http.StatusBadRequest), "invalid_request")
	wantCode(t, f.patch(game, map[string]any{"patch": map[string]any{"type": "staging", "stagingId": "st_20260101T000000Z_00000000"}}, http.StatusNotFound), "not_found")
	wantCode(t, f.patch(game, map[string]any{"patch": map[string]any{"type": "staging", "stagingId": notPatch}, "output": map[string]any{"fileName": "a/b.gba"}}, http.StatusBadRequest), "invalid_name")
	wantCode(t, f.patch("Sony PlayStation (PS)/Quiet Harbor.m3u", map[string]any{"patch": map[string]any{"type": "staging", "stagingId": notPatch}}, http.StatusUnprocessableEntity), "unsupported_format")
	wantCode(t, f.patch(gbaFolder+"/None.gba", map[string]any{"patch": map[string]any{"type": "staging", "stagingId": notPatch}}, http.StatusNotFound), "not_found")
	r := f.do(http.MethodPost, "/api/v1/games/%2E%2E/patch", "/games/{gameId}/patch", mustJSON(t, map[string]any{"patch": map[string]any{"type": "sd", "sdFileId": "x"}}), http.StatusBadRequest)
	wantCode(t, r, "invalid_id")
	wantCode(t, f.do(http.MethodPost, "/api/v1/games/"+library.EncodeID(game)+"/patch", "/games/{gameId}/patch", []byte("{"), http.StatusBadRequest), "invalid_request")
}
