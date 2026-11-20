package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

// TestSmokePatch runs the M6 scenario against the real binary: a test BPS
// patch (made by the test encoder, no commercial content) is uploaded to
// the staging area, applied as a new file, then in place of the ROM (the
// original goes to the trash); a patch made for another ROM is refused
// with patch_mismatch.
func TestSmokePatch(t *testing.T) {
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
	// A 4 MiB ROM of random bytes and its "translated" version.
	gameRel := "Game Boy Advance (GBA)/Starlight Courier.gba"
	romPath := filepath.Join(sd, "Roms", filepath.FromSlash(gameRel))
	src := make([]byte, 4<<20)
	rand.Read(src)
	if err := os.WriteFile(romPath, src, 0o644); err != nil {
		t.Fatal(err)
	}
	tgt := bytes.Clone(src)
	for i := 0; i < len(tgt); i += 4099 {
		tgt[i] ^= 0x5a
	}
	tgt = append(tgt, []byte("translation patch tail")...)

	clients, err := auth.OpenClientStore(store.Layout{Root: roots.Data}.ClientsFile())
	if err != nil {
		t.Fatal(err)
	}
	p := &pak{t: t, bin: bin, sd: sd, port: freePort(t)}
	c := testutil.PairClient(t, clients, p.url())
	p.start()
	c.HTTP = p.httpClient(30 * time.Second)
	defer p.stop()

	stage := func(name string, data []byte) string {
		sum := sha256.Sum256(data)
		var created struct {
			UploadID  string `json:"uploadId"`
			ChunkSize int    `json:"chunkSize"`
		}
		call(t, c, http.MethodPost, "/api/v1/uploads", map[string]any{
			"target": map[string]any{"kind": "staging", "fileName": name}, "sizeBytes": len(data), "sha256": hex.EncodeToString(sum[:]),
		}, http.StatusCreated, &created)
		for off := 0; off < len(data); off += created.ChunkSize {
			req := c.RequestUnsigned(t, http.MethodPut, "/api/v1/uploads/"+created.UploadID, data[off:min(off+created.ChunkSize, len(data))])
			req.Header.Set("Upload-Offset", strconv.Itoa(off))
			if r := c.Send(t, req); r.Status != http.StatusNoContent {
				t.Fatalf("chunk: %d %s", r.Status, r.Body)
			}
		}
		var res struct {
			StagingID string `json:"stagingId"`
		}
		call(t, c, http.MethodPost, "/api/v1/uploads/"+created.UploadID+"/complete", nil, http.StatusOK, &res)
		return res.StagingID
	}
	type job struct {
		State  string         `json:"state"`
		Result map[string]any `json:"result"`
		Error  *struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	patch := func(stagingID, mode string) job {
		var accepted struct {
			JobID string `json:"jobId"`
		}
		call(t, c, http.MethodPost, "/api/v1/games/"+library.EncodeID(gameRel)+"/patch", map[string]any{
			"patch": map[string]any{"type": "staging", "stagingId": stagingID}, "output": map[string]any{"mode": mode},
		}, http.StatusAccepted, &accepted)
		var j job
		for deadline := time.Now().Add(30 * time.Second); j.State != "succeeded" && j.State != "failed"; time.Sleep(50 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("patch job stuck: %+v", j)
			}
			call(t, c, http.MethodGet, "/api/v1/jobs/"+accepted.JobID, nil, http.StatusOK, &j)
		}
		return j
	}
	stagingID := stage("Smoke Translation.bps", testutil.EncodeBPS(src, tgt))

	// 1. New file next to the ROM.
	j := patch(stagingID, "new-file")
	newRel := "Game Boy Advance (GBA)/Starlight Courier (patched).gba"
	if j.State != "succeeded" || j.Result["gameId"] != library.EncodeID(newRel) {
		t.Fatalf("new-file: %+v %+v", j, j.Error)
	}
	if got, _ := os.ReadFile(filepath.Join(sd, "Roms", filepath.FromSlash(newRel))); !bytes.Equal(got, tgt) {
		t.Fatal("new file: wrong content")
	}
	if got, _ := os.ReadFile(romPath); !bytes.Equal(got, src) {
		t.Fatal("new-file changed the original ROM")
	}
	t.Logf("BPS applied as a new file: %s (%d bytes)", newRel, len(tgt))

	// 2. A patch for another ROM: refused, nothing changes.
	other := bytes.Clone(src)
	other[1234] ^= 1
	j = patch(stage("Other.bps", testutil.EncodeBPS(other, tgt)), "replace")
	if j.State != "failed" || j.Error == nil || j.Error.Code != "patch_mismatch" {
		t.Fatalf("wrong source: %+v %+v", j, j.Error)
	}
	if got, _ := os.ReadFile(romPath); !bytes.Equal(got, src) {
		t.Fatal("refused patch changed the ROM")
	}
	t.Logf("BPS for another ROM refused: %s (%s)", j.Error.Code, j.Error.Message)

	// 3. Replace: the original goes to the trash.
	j = patch(stagingID, "replace")
	if j.State != "succeeded" || j.Result["gameId"] != library.EncodeID(gameRel) {
		t.Fatalf("replace: %+v %+v", j, j.Error)
	}
	if got, _ := os.ReadFile(romPath); !bytes.Equal(got, tgt) {
		t.Fatal("replace: wrong content")
	}
	var trash struct {
		Entries []struct {
			ID     string   `json:"id"`
			Reason string   `json:"reason"`
			Files  []string `json:"files"`
		} `json:"entries"`
	}
	call(t, c, http.MethodGet, "/api/v1/trash", nil, http.StatusOK, &trash)
	if len(trash.Entries) != 1 || trash.Entries[0].Reason != "replace-rom" {
		t.Fatalf("trash: %+v", trash)
	}
	trashed := filepath.Join(roots.Data, "trash", trash.Entries[0].ID, "Roms", filepath.FromSlash(gameRel))
	if got, _ := os.ReadFile(trashed); !bytes.Equal(got, src) {
		t.Fatal("original ROM not in the trash")
	}
	t.Logf("BPS applied in place: original ROM in trash entry %s (%s)", trash.Entries[0].ID, trash.Entries[0].Reason)

	// The log is read once the pak has exited (it writes it until then).
	p.stop()
	if !bytes.Contains(p.logs.Bytes(), []byte("rom patched")) || !bytes.Contains(p.logs.Bytes(), []byte("patch refused")) {
		t.Errorf("patches not logged:\n%s", p.logs.String())
	}
}
