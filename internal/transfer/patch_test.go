package transfer

import (
	"bytes"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/testutil"
)

const (
	courier    = gbaSystem + "/Starlight Courier.gba"
	courierSav = "Saves/GBA/Starlight Courier.gba.sav"
)

func patchBytes(seed uint64, n int) []byte {
	r := rand.New(rand.NewPCG(seed, 1))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

// patchFixture writes a ROM of n random bytes to Starlight Courier.gba and
// returns it with a patched version.
func (e *env) patchFixture(n int) (src, tgt []byte) {
	e.t.Helper()
	src = patchBytes(1, n)
	if err := os.WriteFile(e.path("Roms/"+courier), src, 0o644); err != nil {
		e.t.Fatal(err)
	}
	tgt = bytes.Clone(src)
	for i := 7; i < len(tgt); i += 501 {
		tgt[i] ^= 0xa5
	}
	tgt = append(tgt, "translated"...)
	return src, tgt
}

// stage puts data in the staging area (as an upload would) and returns its
// stagingId.
func (e *env) stage(name string, data []byte) string {
	e.t.Helper()
	res := e.complete(e.uploaded(&Target{Kind: KindStaging, FileName: ptr(name)}, data, ""))
	return res["stagingId"].(string)
}

func stagingPatch(id string) *PatchSource {
	return &PatchSource{Type: ptr(PatchSourceStaging), StagingID: ptr(id)}
}

func (e *env) startPatch(game string, src *PatchSource, out *PatchOutput) string {
	e.t.Helper()
	id, apiErr, err := e.svc.StartPatch(gameID(game), PatchRequest{Patch: src, Output: out})
	if apiErr != nil || err != nil {
		e.t.Fatalf("start patch: %v %v", apiErr, err)
	}
	return id
}

func (e *env) patchJob(game string, src *PatchSource, out *PatchOutput) Job {
	e.t.Helper()
	return waitJob(e.t, e.svc.Jobs(), e.startPatch(game, src, out), JobSucceeded, JobFailed, JobCancelled)
}

func (e *env) noTemporaryFiles() {
	e.t.Helper()
	des, _ := os.ReadDir(e.layout.UploadsDir())
	for _, d := range des {
		if strings.HasPrefix(d.Name(), "pt_") {
			e.t.Errorf("temporary file left: %s", d.Name())
		}
	}
}

func TestPatchNewFile(t *testing.T) {
	e := newEnv(t)
	src, tgt := e.patchFixture(64 << 10)
	stagingID := e.stage("hack.bps", testutil.EncodeBPS(src, tgt))

	j := e.patchJob(courier, stagingPatch(stagingID), nil) // default: new-file
	want := gbaSystem + "/Starlight Courier (patched).gba"
	if j.State != JobSucceeded || j.Kind != JobPatch || j.Result["gameId"] != gameID(want) {
		t.Fatalf("job: %+v %+v", j, j.Error)
	}
	if j.Progress.Total == nil || *j.Progress.Total != int64(len(tgt)) || j.Progress.Done != int64(len(tgt)) {
		t.Fatalf("progress: %+v", j.Progress)
	}
	if e.read("Roms/"+want) != string(tgt) || e.read("Roms/"+courier) != string(src) {
		t.Fatal("contents")
	}
	if _, err := e.lib.GameByID(gameID(want)); err != nil {
		t.Fatalf("new game not listed: %v", err)
	}

	// Again: the default name is taken, " (2)".
	j = e.patchJob(courier, stagingPatch(stagingID), &PatchOutput{Mode: ptr(OutputNewFile)})
	if j.Result["gameId"] != gameID(gbaSystem+"/Starlight Courier (patched) (2).gba") {
		t.Fatalf("second: %+v", j)
	}
	// A name given by the app, cleaned; taken: " (2)".
	j = e.patchJob(courier, stagingPatch(stagingID), &PatchOutput{FileName: ptr("Courier: FR.gba")})
	if j.Result["gameId"] != gameID(gbaSystem+"/Courier_ FR.gba") {
		t.Fatalf("named: %+v %+v", j, j.Error)
	}
	j = e.patchJob(courier, stagingPatch(stagingID), &PatchOutput{FileName: ptr("courier_ fr.GBA")})
	if j.Result["gameId"] != gameID(gbaSystem+"/courier_ fr (2).GBA") {
		t.Fatalf("named, case-insensitive conflict: %+v %+v", j, j.Error)
	}
	if entries, _, _ := e.trash.List(); len(entries) != 0 {
		t.Fatalf("new-file trashed something: %+v", entries)
	}
	e.noTemporaryFiles()
	if !strings.Contains(e.logs.String(), "rom patched") {
		t.Error("not logged")
	}
}

func TestPatchReplace(t *testing.T) {
	e := newEnv(t)
	src, tgt := e.patchFixture(32 << 10)
	save := e.read(courierSav)
	states, _ := filepath.Glob(e.path(".userdata/shared/GBA-gpsp/Starlight Courier.gba.st*"))
	j := e.patchJob(courier, stagingPatch(e.stage("hack.bps", testutil.EncodeBPS(src, tgt))), &PatchOutput{Mode: ptr(OutputReplace), FileName: ptr("ignored.gba")})
	if j.State != JobSucceeded || j.Result["gameId"] != gameID(courier) {
		t.Fatalf("job: %+v %+v", j, j.Error)
	}
	if e.read("Roms/"+courier) != string(tgt) {
		t.Fatal("ROM not replaced")
	}
	if e.exists("Roms/" + gbaSystem + "/ignored.gba") {
		t.Fatal("fileName used in replace mode")
	}
	// The original is in the trash.
	entries, _, err := e.trash.List()
	if err != nil || len(entries) != 1 || entries[0].Reason != "replace-rom" {
		t.Fatalf("trash: %+v %v", entries, err)
	}
	if got := e.read(".userdata/shared/ReminisLink/trash/" + entries[0].ID + "/Roms/" + courier); got != string(src) {
		t.Fatal("original not in the trash")
	}
	// Save, states and cheats stay (same name); the save was backed up.
	if e.read(courierSav) != save || !e.exists("Cheats/GBA/Starlight Courier.gba.cht") {
		t.Fatal("save or cheats changed")
	}
	if after, _ := filepath.Glob(e.path(".userdata/shared/GBA-gpsp/Starlight Courier.gba.st*")); len(after) != len(states) {
		t.Fatalf("states: %v -> %v", states, after)
	}
	g, _ := e.lib.GameByID(gameID(courier))
	backups, err := e.opts.Saves.List(g)
	if err != nil || len(backups) != 1 || backups[0].Reason != "before-patch" {
		t.Fatalf("save backup: %+v %v", backups, err)
	}
	e.noTemporaryFiles()

	// The ROM is now the target: applying the patch again is refused.
	j = e.patchJob(courier, stagingPatch(e.stage("hack.bps", testutil.EncodeBPS(src, tgt))), &PatchOutput{Mode: ptr(OutputReplace)})
	if j.State != JobFailed || j.Error.Code != CodePatchMismatch || j.Error.Details["reason"] != "already-patched" {
		t.Fatalf("already patched: %+v %+v", j, j.Error)
	}
	if e.read("Roms/"+courier) != string(tgt) {
		t.Fatal("ROM changed by a refused patch")
	}
}

func TestPatchWrongSource(t *testing.T) {
	e := newEnv(t)
	src, tgt := e.patchFixture(16 << 10)
	other := bytes.Clone(src)
	other[100] ^= 1
	for _, mode := range []string{OutputNewFile, OutputReplace} {
		j := e.patchJob(courier, stagingPatch(e.stage("hack.bps", testutil.EncodeBPS(other, tgt))), &PatchOutput{Mode: ptr(mode)})
		if j.State != JobFailed || j.Error.Code != CodePatchMismatch || j.Error.Details["reason"] != "source-mismatch" {
			t.Fatalf("%s: %+v %+v", mode, j, j.Error)
		}
	}
	if e.read("Roms/"+courier) != string(src) || e.exists("Roms/"+gbaSystem+"/Starlight Courier (patched).gba") {
		t.Fatal("a refused patch changed the card")
	}
	if entries, _, _ := e.trash.List(); len(entries) != 0 {
		t.Fatalf("trash: %+v", entries)
	}
	e.noTemporaryFiles()
}

func TestPatchCorruptAndUnsupported(t *testing.T) {
	e := newEnv(t)
	src, tgt := e.patchFixture(4096)
	p := testutil.EncodeBPS(src, tgt)
	// Not a patch: refused at once.
	_, apiErr, _ := e.svc.StartPatch(gameID(courier), PatchRequest{Patch: stagingPatch(e.stage("hack.bps", []byte("not a patch at all")))})
	if apiErr == nil || apiErr.Status != http.StatusUnprocessableEntity || apiErr.Code != CodeUnsupportedFormat {
		t.Fatalf("not a patch: %v", apiErr)
	}
	// Truncated, wrong patch checksum: the job fails.
	bad := bytes.Clone(p)
	bad[len(bad)-1] ^= 1
	for name, data := range map[string][]byte{"truncated": p[:len(p)/2], "checksum": bad} {
		j := e.patchJob(courier, stagingPatch(e.stage("hack.bps", data)), nil)
		if j.State != JobFailed || j.Error.Code != CodeUnsupportedFormat || !strings.Contains(j.Error.Message, "corrupt") {
			t.Fatalf("%s: %+v %+v", name, j, j.Error)
		}
	}
	e.noTemporaryFiles()
}

func TestPatchFromSDCard(t *testing.T) {
	e := newEnv(t)
	src, tgt := e.patchFixture(8192)
	dir := e.layout.PatchesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Courier FR.bps"), testutil.EncodeBPS(src, tgt), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := e.lib.SDFiles(library.SDKindPatch)
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for _, f := range files {
		if strings.HasSuffix(f.Path, "patches/Courier FR.bps") {
			id = f.ID
		}
	}
	if id == "" {
		t.Fatalf("patch not listed: %+v", files)
	}
	j := e.patchJob(courier, &PatchSource{Type: ptr(PatchSourceSD), SDFileID: ptr(id)}, nil)
	if j.State != JobSucceeded {
		t.Fatalf("%+v %+v", j, j.Error)
	}
	if e.read("Roms/"+gbaSystem+"/Starlight Courier (patched).gba") != string(tgt) {
		t.Fatal("content")
	}
	// The patch file on the card is left as it is.
	if _, err := os.Stat(filepath.Join(dir, "Courier FR.bps")); err != nil {
		t.Fatal(err)
	}
}

func TestPatchRequestErrors(t *testing.T) {
	e := newEnv(t)
	src, tgt := e.patchFixture(1024)
	st := e.stage("hack.bps", testutil.EncodeBPS(src, tgt))
	cases := []struct {
		name   string
		game   string
		req    PatchRequest
		status int
		code   string
	}{
		{"no patch", courier, PatchRequest{}, 400, CodeInvalidRequest},
		{"no type", courier, PatchRequest{Patch: &PatchSource{StagingID: ptr(st)}}, 400, CodeInvalidRequest},
		{"bad type", courier, PatchRequest{Patch: &PatchSource{Type: ptr("url")}}, 400, CodeInvalidRequest},
		{"no stagingId", courier, PatchRequest{Patch: &PatchSource{Type: ptr("staging")}}, 400, CodeInvalidRequest},
		{"unknown staging", courier, PatchRequest{Patch: stagingPatch("st_20260101T000000Z_00000000")}, 404, CodeNotFound},
		{"bad staging id", courier, PatchRequest{Patch: stagingPatch("../x")}, 404, CodeNotFound},
		{"no sdFileId", courier, PatchRequest{Patch: &PatchSource{Type: ptr("sd")}}, 400, CodeInvalidRequest},
		{"bad sdFileId", courier, PatchRequest{Patch: &PatchSource{Type: ptr("sd"), SDFileID: ptr("sd_" + library.EncodeID("../x.bps"))}}, 400, CodeInvalidID},
		{"cht as patch", courier, PatchRequest{Patch: &PatchSource{Type: ptr("sd"), SDFileID: ptr("sd_" + library.EncodeID("Cheats/GBA/Starlight Courier.gba.cht"))}}, 400, CodeInvalidID},
		{"missing sd file", courier, PatchRequest{Patch: &PatchSource{Type: ptr("sd"), SDFileID: ptr("sd_" + library.EncodeID("Roms/none.ips"))}}, 404, CodeNotFound},
		{"bad mode", courier, PatchRequest{Patch: stagingPatch(st), Output: &PatchOutput{Mode: ptr("copy")}}, 400, CodeInvalidRequest},
		{"bad name", courier, PatchRequest{Patch: stagingPatch(st), Output: &PatchOutput{FileName: ptr("../x.gba")}}, 400, CodeInvalidName},
		{"not a ROM name", courier, PatchRequest{Patch: stagingPatch(st), Output: &PatchOutput{FileName: ptr("notes.txt")}}, 400, CodeInvalidName},
		{"m3u name", courier, PatchRequest{Patch: stagingPatch(st), Output: &PatchOutput{FileName: ptr("x.m3u")}}, 400, CodeInvalidName},
		{"unknown game", gbaSystem + "/None.gba", PatchRequest{Patch: stagingPatch(st)}, 404, CodeNotFound},
		{"m3u game", "Sony PlayStation (PS)/Quiet Harbor.m3u", PatchRequest{Patch: stagingPatch(st)}, 422, CodeUnsupportedFormat},
		{"folder-m3u game", "Sony PlayStation (PS)/Mossy Hollow", PatchRequest{Patch: stagingPatch(st)}, 422, CodeUnsupportedFormat},
	}
	for _, c := range cases {
		_, apiErr, err := e.svc.StartPatch(gameID(c.game), c.req)
		if err != nil || apiErr == nil || apiErr.Status != c.status || apiErr.Code != c.code {
			t.Errorf("%s: %v %v", c.name, apiErr, err)
		}
	}
	if _, apiErr, _ := e.svc.StartPatch("!!", PatchRequest{Patch: stagingPatch(st)}); apiErr == nil || apiErr.Code != CodeInvalidID {
		t.Errorf("bad game id: %v", apiErr)
	}
	// A zipped ROM.
	if err := os.WriteFile(e.path("Roms/"+gbaSystem+"/Zipped.zip"), []byte("PK"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, apiErr, _ := e.svc.StartPatch(gameID(gbaSystem+"/Zipped.zip"), PatchRequest{Patch: stagingPatch(st)}); apiErr == nil || apiErr.Code != CodeUnsupportedFormat {
		t.Errorf("zip: %v", apiErr)
	}
}

func TestPatchInsufficientStorage(t *testing.T) {
	e := newEnv(t)
	src, tgt := e.patchFixture(8192)
	st := e.stage("hack.bps", testutil.EncodeBPS(src, tgt))
	*e.free = 100
	j := e.patchJob(courier, stagingPatch(st), nil)
	if j.State != JobFailed || j.Error.Code != CodeInsufficientStorage {
		t.Fatalf("%+v %+v", j, j.Error)
	}
	e.noTemporaryFiles()
}

// holdInstall runs a patch job until its file is written, with the SD
// card lock held so it waits before installing; release lets it go on.
func (e *env) holdInstall(src *PatchSource, out *PatchOutput) (id string, release func()) {
	e.t.Helper()
	e.opts.Lock.Lock()
	id = e.startPatch(courier, src, out)
	deadline := time.Now().Add(10 * time.Second)
	for {
		j, _ := e.svc.Jobs().Get(id)
		if j.Progress.Total != nil && j.Progress.Done == *j.Progress.Total && j.Progress.Done > 0 {
			break
		}
		if j.State != JobRunning && j.State != JobQueued {
			e.opts.Lock.Unlock()
			e.t.Fatalf("job ended early: %+v %+v", j, j.Error)
		}
		if time.Now().After(deadline) {
			e.opts.Lock.Unlock()
			e.t.Fatal("job stuck")
		}
		time.Sleep(2 * time.Millisecond)
	}
	return id, e.opts.Lock.Unlock
}

func TestPatchCancel(t *testing.T) {
	e := newEnv(t)
	src, tgt := e.patchFixture(256 << 10)
	id, release := e.holdInstall(stagingPatch(e.stage("hack.bps", testutil.EncodeBPS(src, tgt))), &PatchOutput{Mode: ptr(OutputReplace)})
	done := make(chan Job, 1)
	go func() {
		j, _ := e.svc.Jobs().Cancel(id)
		done <- j
	}()
	time.Sleep(20 * time.Millisecond)
	release()
	if j := <-done; j.State != JobCancelled {
		t.Fatalf("cancel: %+v", j)
	}
	if j := waitJob(t, e.svc.Jobs(), id, JobCancelled, JobFailed, JobSucceeded); j.State != JobCancelled {
		t.Fatalf("final: %+v", j)
	}
	if e.read("Roms/"+courier) != string(src) {
		t.Fatal("ROM changed by a cancelled patch")
	}
	if entries, _, _ := e.trash.List(); len(entries) != 0 {
		t.Fatalf("trash: %+v", entries)
	}
	e.noTemporaryFiles()
}

func TestPatchROMChangedMeanwhile(t *testing.T) {
	e := newEnv(t)
	src, tgt := e.patchFixture(64 << 10)
	id, release := e.holdInstall(stagingPatch(e.stage("hack.bps", testutil.EncodeBPS(src, tgt))), &PatchOutput{Mode: ptr(OutputReplace)})
	if err := os.WriteFile(e.path("Roms/"+courier), []byte("replaced meanwhile"), 0o644); err != nil {
		release()
		t.Fatal(err)
	}
	release()
	j := waitJob(t, e.svc.Jobs(), id, JobSucceeded, JobFailed)
	if j.State != JobFailed || j.Error.Code != CodeConflict {
		t.Fatalf("%+v %+v", j, j.Error)
	}
	if e.read("Roms/"+courier) != "replaced meanwhile" {
		t.Fatal("ROM replaced")
	}
	if entries, _, _ := e.trash.List(); len(entries) != 0 {
		t.Fatalf("trash: %+v", entries)
	}
	e.noTemporaryFiles()
}

// TestPatchPowerCut: a cut while the patched file is written leaves a
// temporary file in uploads/, deleted at the next start; the ROM was only
// read.
func TestPatchPowerCut(t *testing.T) {
	e := newEnv(t)
	src, _ := e.patchFixture(1024)
	leftover := filepath.Join(e.layout.UploadsDir(), "pt_0123456789abcdef01234567.part")
	if err := os.MkdirAll(filepath.Dir(leftover), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leftover, []byte("half a patched ROM"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.svc.Close()
	e.svc = e.newService()
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Fatalf("leftover kept: %v", err)
	}
	if e.read("Roms/"+courier) != string(src) {
		t.Fatal("ROM")
	}
}
