package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/testutil"
	"github.com/tfournier/ReminisLink/internal/tlsid"
)

// SmokeSizeEnv sets the size in MiB of the file uploaded by
// TestSmokeTransfers (default 24; scripts/smoke-transfers.sh uses 200).
const SmokeSizeEnv = "RL_SMOKE_MB"

// pak is a headless pak running as a separate process.
type pak struct {
	t    *testing.T
	bin  string
	sd   string
	port int
	cmd  *exec.Cmd
	logs bytes.Buffer
	pin  string // of the pak's certificate, read from the card (M13)
}

// readPin returns the pin of the certificate the pak keeps on the card,
// or "" while it does not exist yet.
func (p *pak) readPin() string {
	data, err := os.ReadFile(filepath.Join(nextui.RootsForSD(p.sd, "tg5040").Data, "tls", tlsid.CertFile))
	if err != nil {
		return ""
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return ""
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return ""
	}
	return auth.CertificatePin(cert.RawSubjectPublicKeyInfo)
}

// httpClient talks HTTPS to the pak, pinned to its certificate.
func (p *pak) httpClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: &http.Transport{TLSClientConfig: tlsid.ClientConfig(p.pin)}}
}

func (p *pak) start() {
	p.t.Helper()
	p.cmd = exec.Command(p.bin, "--headless", "--sd", p.sd, "--port", strconv.Itoa(p.port), "--name", "Smoke test")
	p.cmd.Env = append(os.Environ(), "PLATFORM=tg5040")
	p.cmd.Stdout = io.Discard
	p.cmd.Stderr = &p.logs
	if err := p.cmd.Start(); err != nil {
		p.t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if p.pin == "" {
			p.pin = p.readPin()
		}
		resp, err := p.httpClient(5 * time.Second).Get(p.url() + "/api/v1/hello")
		if p.pin != "" && err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			p.stop()
			p.t.Fatalf("the pak did not start:\n%s", p.logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (p *pak) url() string { return "https://127.0.0.1:" + strconv.Itoa(p.port) }

// stop sends SIGINT and waits for the pak to exit.
func (p *pak) stop() {
	p.t.Helper()
	if p.cmd == nil || p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Signal(syscall.SIGINT)
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		_ = p.cmd.Process.Kill()
		<-done
		p.t.Errorf("the pak did not stop on SIGINT")
	}
	p.cmd = nil
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func call(t *testing.T, c *testutil.Client, method, target string, body any, want int, out any) {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	r := c.Do(t, method, target, raw)
	if r.Status != want {
		t.Fatalf("%s %s: %d %s", method, target, r.Status, r.Body)
	}
	if out != nil {
		r.JSON(t, out)
	}
}

// TestSmokeTransfers runs the M4 scenario against the real binary: a large
// upload cut by SIGINT in the middle and resumed after a restart, a
// download from a local HTTP server, a game deleted to the trash.
func TestSmokeTransfers(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the pak")
	}
	sizeMB := 24
	if v := os.Getenv(SmokeSizeEnv); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("%s=%q", SmokeSizeEnv, v)
		}
		sizeMB = n
	}
	bin := filepath.Join(t.TempDir(), "reminislink")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	sd := testutil.CopySDCard(t)

	// Pair a client the way a pairing would (clients.json).
	roots := nextui.RootsForSD(sd, "tg5040")
	clients, err := auth.OpenClientStore(store.Layout{Root: roots.Data}.ClientsFile())
	if err != nil {
		t.Fatal(err)
	}
	p := &pak{t: t, bin: bin, sd: sd, port: freePort(t)}
	c := testutil.PairClient(t, clients, p.url())
	p.start()
	c.HTTP = p.httpClient(60 * time.Second)
	defer p.stop()

	// 1. Upload, SIGINT in the middle, restart, resume.
	data := make([]byte, sizeMB<<20)
	rand.Read(data)
	sum := sha256.Sum256(data)
	var created struct {
		UploadID  string `json:"uploadId"`
		ChunkSize int    `json:"chunkSize"`
	}
	call(t, c, http.MethodPost, "/api/v1/uploads", map[string]any{
		"target":    map[string]any{"kind": "rom", "systemId": library.EncodeID("Game Boy Advance (GBA)"), "fileName": "Smoke Test.gba"},
		"sizeBytes": len(data), "sha256": hex.EncodeToString(sum[:]),
	}, http.StatusCreated, &created)
	sendChunks := func(from, to int) {
		for off := from; off < to; off += created.ChunkSize {
			end := min(off+created.ChunkSize, len(data))
			req := c.RequestUnsigned(t, http.MethodPut, "/api/v1/uploads/"+created.UploadID, data[off:end])
			req.Header.Set("Upload-Offset", strconv.Itoa(off))
			if r := c.Send(t, req); r.Status != http.StatusNoContent {
				t.Fatalf("chunk at %d: %d %s", off, r.Status, r.Body)
			}
		}
	}
	half := (len(data) / 2 / created.ChunkSize) * created.ChunkSize
	start := time.Now()
	sendChunks(0, half)
	p.stop() // SIGINT
	t.Logf("sent %d MiB, pak stopped", half>>20)
	p.start()
	var status struct {
		Offset int `json:"offset"`
	}
	call(t, c, http.MethodGet, "/api/v1/uploads/"+created.UploadID, nil, http.StatusOK, &status)
	if status.Offset != half {
		t.Fatalf("offset after restart %d, want %d", status.Offset, half)
	}
	sendChunks(status.Offset, len(data))
	var result struct {
		GameID string `json:"gameId"`
	}
	call(t, c, http.MethodPost, "/api/v1/uploads/"+created.UploadID+"/complete", nil, http.StatusOK, &result)
	installed, err := os.ReadFile(filepath.Join(sd, "Roms", "Game Boy Advance (GBA)", "Smoke Test.gba"))
	if err != nil || sha256.Sum256(installed) != sum {
		t.Fatalf("installed file: wrong SHA-256 (%v)", err)
	}
	t.Logf("upload of %d MiB resumed at %d MiB and verified in %v", sizeMB, half>>20, time.Since(start).Round(time.Millisecond))

	// 2. Download from a local HTTP server.
	patch := []byte("local patch content")
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="Smoke Hack.bps"`)
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(patch))
	}))
	defer src.Close()
	var accepted struct {
		JobID string `json:"jobId"`
	}
	call(t, c, http.MethodPost, "/api/v1/downloads", map[string]any{"url": src.URL + "/dl?id=1", "target": map[string]any{"kind": "staging"}}, http.StatusAccepted, &accepted)
	var job struct {
		State  string         `json:"state"`
		Result map[string]any `json:"result"`
		Error  any            `json:"error"`
	}
	for deadline := time.Now().Add(15 * time.Second); job.State != "succeeded"; time.Sleep(100 * time.Millisecond) {
		if job.State == "failed" || time.Now().After(deadline) {
			t.Fatalf("download job %+v", job)
		}
		call(t, c, http.MethodGet, "/api/v1/jobs/"+accepted.JobID, nil, http.StatusOK, &job)
	}
	staged := filepath.Join(roots.Data, "staging", job.Result["stagingId"].(string), "Smoke Hack.bps")
	if got, err := os.ReadFile(staged); err != nil || !bytes.Equal(got, patch) {
		t.Fatalf("downloaded file: %v", err)
	}
	t.Log("download from a local server: ok")

	// 3. Delete a game, check the trash.
	var deleted struct {
		TrashEntryID string   `json:"trashEntryId"`
		Moved        []string `json:"moved"`
	}
	call(t, c, http.MethodDelete, "/api/v1/games/"+library.EncodeID("Game Boy Advance (GBA)/Starlight Courier.gba"), nil, http.StatusOK, &deleted)
	var trash struct {
		Entries []struct {
			ID    string   `json:"id"`
			Files []string `json:"files"`
		} `json:"entries"`
	}
	call(t, c, http.MethodGet, "/api/v1/trash", nil, http.StatusOK, &trash)
	if len(trash.Entries) != 1 || trash.Entries[0].ID != deleted.TrashEntryID || len(trash.Entries[0].Files) != len(deleted.Moved) {
		t.Fatalf("trash %+v, deleted %+v", trash, deleted)
	}
	for _, rel := range deleted.Moved {
		if _, err := os.Stat(filepath.Join(roots.Data, "trash", deleted.TrashEntryID, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s not in the trash", rel)
		}
		if _, err := os.Stat(filepath.Join(sd, filepath.FromSlash(rel))); err == nil {
			t.Errorf("%s still in place", rel)
		}
	}
	t.Logf("game deleted: %d files in trash entry %s", len(deleted.Moved), deleted.TrashEntryID)
}
