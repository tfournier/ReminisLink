package transfer

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/saves"
	"github.com/tfournier/ReminisLink/internal/store"
)

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// create starts an upload and fails the test on error.
func (e *env) create(target *Target, data []byte, onConflict string) UploadCreated {
	e.t.Helper()
	req := CreateUploadRequest{Target: target, SizeBytes: ptr(int64(len(data))), SHA256: ptr(sum(data))}
	if onConflict != "" {
		req.OnConflict = &onConflict
	}
	c, apiErr, err := e.svc.CreateUpload(req)
	if apiErr != nil || err != nil {
		e.t.Fatalf("create: %v %v", apiErr, err)
	}
	return c
}

// send writes data[from:to] in chunks of the service chunk size.
func (e *env) send(id string, data []byte, from, to int) {
	e.t.Helper()
	cs := int(e.svc.opts.ChunkSize)
	for off := from; off < to; off += cs {
		end := min(off+cs, to)
		got, apiErr, err := e.svc.WriteChunk(id, int64(off), int64(end-off), bytes.NewReader(data[off:end]))
		if apiErr != nil || err != nil || got != int64(end) {
			e.t.Fatalf("chunk at %d: %d %v %v", off, got, apiErr, err)
		}
	}
}

func (e *env) complete(id string) map[string]any {
	e.t.Helper()
	res, apiErr, err := e.svc.CompleteUpload(context.Background(), id)
	if apiErr != nil || err != nil {
		e.t.Fatalf("complete: %v %v", apiErr, err)
	}
	return res
}

func TestUploadROM(t *testing.T) {
	e := newEnv(t)
	data := randomBytes(t, 5000)
	c := e.create(romTarget("New: Game?.gba"), data, "")
	if c.ChunkSize != 1<<10 || c.Offset != 0 || !c.ExpiresAt.Equal(e.clock.Now().Add(24*time.Hour).UTC()) {
		t.Fatalf("created %+v", c)
	}
	e.send(c.UploadID, data, 0, 3000)
	st, apiErr := e.svc.Status(c.UploadID)
	if apiErr != nil || st.Offset != 3000 || st.SizeBytes != 5000 {
		t.Fatalf("status %+v %v", st, apiErr)
	}
	e.send(c.UploadID, data, 3000, len(data))
	res := e.complete(c.UploadID)
	rel := "Roms/" + gbaSystem + "/New_ Game_.gba"
	if res["gameId"] != gameID(gbaSystem+"/New_ Game_.gba") || e.read(rel) != string(data) {
		t.Fatalf("result %v", res)
	}
	if _, apiErr := e.svc.Status(c.UploadID); apiErr == nil || apiErr.Status != http.StatusNotFound {
		t.Fatalf("finished upload still there: %v", apiErr)
	}
	if des, _ := os.ReadDir(e.layout.UploadsDir()); len(des) != 0 {
		t.Fatalf("leftovers in uploads/: %v", des)
	}
}

func TestUploadIntoSubfolderAndEmptyFile(t *testing.T) {
	e := newEnv(t)
	target := romTarget("Empty.gba")
	target.FolderID = ptr(gameID(gbaSystem + "/Hacks"))
	c := e.create(target, nil, "")
	res := e.complete(c.UploadID)
	if res["gameId"] != gameID(gbaSystem+"/Hacks/Empty.gba") || e.read("Roms/"+gbaSystem+"/Hacks/Empty.gba") != "" {
		t.Fatalf("result %v", res)
	}
}

func TestUploadResumesAfterRestart(t *testing.T) {
	for _, mode := range []string{"clean", "unsaved bytes", "zeroed tail"} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t)
			data := randomBytes(t, 4000)
			c := e.create(romTarget("Resumed.gba"), data, "")
			e.send(c.UploadID, data, 0, 2048)
			part := filepath.Join(e.layout.UploadsDir(), c.UploadID+partExt)
			want := int64(2048)
			switch mode {
			case "unsaved bytes":
				// Bytes on the card past the last answered chunk: kept.
				f, _ := os.OpenFile(part, os.O_WRONLY|os.O_APPEND, 0)
				f.Write(data[2048:2100])
				f.Close()
				want = 2100
			case "zeroed tail":
				// Power loss: the size of the file persisted, not its data.
				f, _ := os.OpenFile(part, os.O_WRONLY, 0)
				f.WriteAt(make([]byte, 100), 1948)
				f.Close()
			}
			// A stale hash state of the first draft is ignored.
			os.WriteFile(filepath.Join(e.layout.UploadsDir(), c.UploadID+hashExt), []byte(`{"offset":2048}`), 0o644)
			e.svc.Close()
			e.svc = e.newService() // the pak restarts
			st, apiErr := e.svc.Status(c.UploadID)
			if apiErr != nil || st.Offset != want {
				t.Fatalf("after restart %+v %v", st, apiErr)
			}
			e.send(c.UploadID, data, int(st.Offset), len(data))
			res, apiErr, err := e.svc.CompleteUpload(context.Background(), c.UploadID)
			if mode == "zeroed tail" {
				if apiErr == nil || apiErr.Code != CodeChecksumMismatch || e.exists("Roms/"+gbaSystem+"/Resumed.gba") {
					t.Fatalf("corrupt file installed: %v %v %v", res, apiErr, err)
				}
				return
			}
			if apiErr != nil || err != nil || e.read("Roms/"+gbaSystem+"/Resumed.gba") != string(data) {
				t.Fatalf("complete: %v %v", apiErr, err)
			}
		})
	}
}

func TestUploadChunkErrors(t *testing.T) {
	e := newEnv(t)
	data := randomBytes(t, 3000)
	c := e.create(romTarget("Errors.gba"), data, "")
	e.send(c.UploadID, data, 0, 1024)

	_, apiErr, _ := e.svc.WriteChunk(c.UploadID, 0, 10, bytes.NewReader(data[:10]))
	if apiErr == nil || apiErr.Code != CodeConflict || apiErr.Details["offset"] != int64(1024) {
		t.Fatalf("wrong offset: %v", apiErr)
	}
	_, apiErr, _ = e.svc.WriteChunk(c.UploadID, 1024, 1025, bytes.NewReader(data[1024:2049]))
	if apiErr == nil || apiErr.Code != CodeTooLarge {
		t.Fatalf("chunk larger than chunkSize: %v", apiErr)
	}
	_, apiErr, _ = e.svc.WriteChunk(c.UploadID, 1024, 0, bytes.NewReader(nil))
	if apiErr == nil || apiErr.Code != CodeInvalidRequest {
		t.Fatalf("empty chunk: %v", apiErr)
	}
	// A body shorter than announced leaves the upload unchanged.
	_, apiErr, _ = e.svc.WriteChunk(c.UploadID, 1024, 1000, bytes.NewReader(data[1024:1500]))
	if apiErr == nil || apiErr.Code != CodeInvalidRequest {
		t.Fatalf("short body: %v", apiErr)
	}
	// A body that fails while reading too.
	_, apiErr, _ = e.svc.WriteChunk(c.UploadID, 1024, 1000, io.MultiReader(bytes.NewReader(data[1024:1100]), errReader{}))
	if apiErr == nil || apiErr.Code != CodeInvalidRequest {
		t.Fatalf("broken body: %v", apiErr)
	}
	if st, _ := e.svc.Status(c.UploadID); st.Offset != 1024 {
		t.Fatalf("offset moved to %d", st.Offset)
	}
	if fi, _ := os.Stat(filepath.Join(e.layout.UploadsDir(), c.UploadID+partExt)); fi.Size() != 1024 {
		t.Fatalf("partial file is %d bytes", fi.Size())
	}
	// Not complete yet.
	if _, apiErr, _ := e.svc.CompleteUpload(context.Background(), c.UploadID); apiErr == nil || apiErr.Code != CodeConflict {
		t.Fatalf("early complete: %v", apiErr)
	}
	e.send(c.UploadID, data, 1024, len(data))
	// The last chunk cannot go past the size.
	if _, apiErr, _ := e.svc.WriteChunk(c.UploadID, 3000, 1, bytes.NewReader([]byte{1})); apiErr == nil || apiErr.Code != CodeTooLarge {
		t.Fatalf("past the end: %v", apiErr)
	}
	e.complete(c.UploadID)

	for _, id := range []string{"", "up_../x", "UP_000000000000000000000000"} {
		if _, apiErr := e.svc.Status(id); apiErr == nil || apiErr.Code != CodeInvalidID {
			t.Errorf("id %q: %v", id, apiErr)
		}
	}
	if _, apiErr := e.svc.Status("up_000000000000000000000000"); apiErr == nil || apiErr.Code != CodeNotFound {
		t.Errorf("unknown id: %v", apiErr)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

func TestUploadChecksumMismatch(t *testing.T) {
	e := newEnv(t)
	data := randomBytes(t, 2000)
	req := CreateUploadRequest{Target: romTarget("Bad.gba"), SizeBytes: ptr(int64(len(data))), SHA256: ptr(sum([]byte("other")))}
	c, _, _ := e.svc.CreateUpload(req)
	e.send(c.UploadID, data, 0, len(data))
	if _, apiErr, _ := e.svc.CompleteUpload(context.Background(), c.UploadID); apiErr == nil || apiErr.Code != CodeChecksumMismatch || apiErr.Status != http.StatusConflict {
		t.Fatalf("mismatch: %v", apiErr)
	}
	if e.exists("Roms/" + gbaSystem + "/Bad.gba") {
		t.Fatal("file installed")
	}
	if _, apiErr := e.svc.Status(c.UploadID); apiErr == nil || apiErr.Code != CodeNotFound {
		t.Fatal("upload not deleted")
	}
}

func TestUploadInsufficientStorage(t *testing.T) {
	e := newEnv(t)
	*e.free = 1050
	req := CreateUploadRequest{Target: romTarget("Big.gba"), SizeBytes: ptr(int64(1000)), SHA256: ptr(sum(nil))}
	if _, apiErr, _ := e.svc.CreateUpload(req); apiErr != nil {
		t.Fatalf("1000 bytes + 5%% fit in 1050: %v", apiErr)
	}
	// The first upload still expects 1000 bytes.
	req.SizeBytes = ptr(int64(100))
	_, apiErr, _ := e.svc.CreateUpload(req)
	if apiErr == nil || apiErr.Status != http.StatusInsufficientStorage || apiErr.Details["required"] != int64(105) || apiErr.Details["available"] != int64(50) {
		t.Fatalf("got %+v", apiErr)
	}
}

func TestUploadRequestErrors(t *testing.T) {
	e := newEnv(t)
	good := func() CreateUploadRequest {
		return CreateUploadRequest{Target: romTarget("x.gba"), SizeBytes: ptr(int64(1)), SHA256: ptr(sum([]byte{1}))}
	}
	cases := map[string]struct {
		mut  func(*CreateUploadRequest)
		code string
	}{
		"no target":      {func(r *CreateUploadRequest) { r.Target = nil }, CodeInvalidRequest},
		"negative size":  {func(r *CreateUploadRequest) { r.SizeBytes = ptr(int64(-1)) }, CodeInvalidRequest},
		"bad sha":        {func(r *CreateUploadRequest) { r.SHA256 = ptr("ABC") }, CodeInvalidRequest},
		"bad conflict":   {func(r *CreateUploadRequest) { r.OnConflict = ptr("replace") }, CodeInvalidRequest},
		"bad kind":       {func(r *CreateUploadRequest) { r.Target.Kind = "bios" }, CodeInvalidRequest},
		"no name":        {func(r *CreateUploadRequest) { r.Target.FileName = nil }, CodeInvalidRequest},
		"bad name":       {func(r *CreateUploadRequest) { r.Target.FileName = ptr("../x.gba") }, CodeInvalidName},
		"bad system":     {func(r *CreateUploadRequest) { r.Target.SystemID = "Li4" }, CodeInvalidID},
		"unknown system": {func(r *CreateUploadRequest) { r.Target.SystemID = gameID("Nope (NO)") }, CodeNotFound},
		"empty folder":   {func(r *CreateUploadRequest) { r.Target.FolderID = ptr("") }, CodeInvalidID},
		"other system folder": {func(r *CreateUploadRequest) {
			r.Target.FolderID = ptr(gameID("Sony PlayStation (PS)/Mossy Hollow"))
		}, CodeInvalidID},
		"game folder": {func(r *CreateUploadRequest) {
			r.Target.SystemID = gameID("Sony PlayStation (PS)")
			r.Target.FolderID = ptr(gameID("Sony PlayStation (PS)/Mossy Hollow"))
		}, CodeNotFound},
		"save no game": {func(r *CreateUploadRequest) { r.Target = &Target{Kind: KindSave} }, CodeInvalidRequest},
		"state no slot": {func(r *CreateUploadRequest) {
			r.Target = &Target{Kind: KindState, GameID: gameID(gbaSystem + "/Starlight Courier.gba")}
		}, CodeInvalidRequest},
		"state bad slot": {func(r *CreateUploadRequest) {
			r.Target = &Target{Kind: KindState, GameID: gameID(gbaSystem + "/Starlight Courier.gba"), Slot: ptr(100)}
		}, CodeInvalidRequest},
		"unknown game": {func(r *CreateUploadRequest) {
			r.Target = &Target{Kind: KindSave, GameID: gameID(gbaSystem + "/Nope.gba")}
		}, CodeNotFound},
		"rom exists": {func(r *CreateUploadRequest) { r.Target.FileName = ptr("starlight courier.GBA") }, CodeConflict},
	}
	for name, c := range cases {
		r := good()
		c.mut(&r)
		_, apiErr, err := e.svc.CreateUpload(r)
		if err != nil || apiErr == nil || apiErr.Code != c.code {
			t.Errorf("%s: %v %v, want %s", name, apiErr, err, c.code)
		}
		if name == "rom exists" && apiErr != nil && apiErr.Details["existing"] != "Roms/"+gbaSystem+"/Starlight Courier.gba" {
			t.Errorf("existing = %v", apiErr.Details["existing"])
		}
	}
}

func TestUploadConflictRules(t *testing.T) {
	e := newEnv(t)
	existing := "Roms/" + gbaSystem + "/Starlight Courier.gba"
	old := e.read(existing)

	// rename: " (2)".
	data := []byte("renamed")
	c := e.create(romTarget("Starlight Courier.gba"), data, ConflictRename)
	e.send(c.UploadID, data, 0, len(data))
	res := e.complete(c.UploadID)
	if res["gameId"] != gameID(gbaSystem+"/Starlight Courier (2).gba") || e.read(existing) != old {
		t.Fatalf("rename: %v", res)
	}

	// fail, checked again at completion: the file appeared meanwhile.
	data = []byte("late")
	c = e.create(romTarget("Late.gba"), data, ConflictFail)
	e.send(c.UploadID, data, 0, len(data))
	if err := os.WriteFile(e.path("Roms/"+gbaSystem+"/LATE.gba"), []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, apiErr, _ := e.svc.CompleteUpload(context.Background(), c.UploadID); apiErr == nil || apiErr.Code != CodeConflict {
		t.Fatalf("late conflict: %v", apiErr)
	}
	if st, apiErr := e.svc.Status(c.UploadID); apiErr != nil || st.Offset != int64(len(data)) {
		t.Fatal("upload must stay after a conflict")
	}

	// overwrite: the old file goes to the trash first.
	data = []byte("new content")
	c = e.create(romTarget("starlight courier.gba"), data, ConflictOverwrite)
	e.send(c.UploadID, data, 0, len(data))
	e.complete(c.UploadID)
	if e.read("Roms/"+gbaSystem+"/starlight courier.gba") != "new content" {
		t.Fatal("not replaced")
	}
	entries, _, _ := e.trash.List()
	if len(entries) != 1 || entries[0].Reason != store.ReasonOverwrite || entries[0].Files[0] != existing {
		t.Fatalf("trash %+v", entries)
	}
	kept, err := os.ReadFile(filepath.Join(e.trash.Dir(), entries[0].ID, filepath.FromSlash(existing)))
	if err != nil || string(kept) != old {
		t.Fatalf("old file not kept: %v", err)
	}

	// overwrite never replaces a folder (multi-disc game).
	data = []byte("x")
	ps := &Target{Kind: KindROM, SystemID: gameID("Sony PlayStation (PS)"), FileName: ptr("Mossy Hollow")}
	c = e.create(ps, data, ConflictOverwrite)
	e.send(c.UploadID, data, 0, 1)
	if _, apiErr, _ := e.svc.CompleteUpload(context.Background(), c.UploadID); apiErr == nil || apiErr.Code != CodeConflict {
		t.Fatalf("folder replaced: %v", apiErr)
	}
}

func TestUploadSaveStateStaging(t *testing.T) {
	e := newEnv(t)
	game := gameID(gbaSystem + "/Starlight Courier.gba")

	// save: the previous save is kept as a before-replace backup copy.
	oldSave := e.read("Saves/GBA/Starlight Courier.gba.sav")
	data := []byte("new save")
	c := e.create(&Target{Kind: KindSave, GameID: game}, data, "")
	e.send(c.UploadID, data, 0, len(data))
	res := e.complete(c.UploadID)
	backup, ok := res["backup"].(*saves.Backup)
	if !ok || backup == nil || backup.Reason != saves.ReasonBeforeReplace || backup.SizeBytes != int64(len(oldSave)) {
		t.Fatalf("save result %v", res)
	}
	if e.read("Saves/GBA/Starlight Courier.gba.sav") != "new save" {
		t.Fatal("save not replaced")
	}
	if kept := e.read(".userdata/shared/ReminisLink/backups/GBA/Starlight Courier.gba/saves/" + backup.ID + ".sav"); kept != oldSave {
		t.Fatal("old save lost")
	}
	if entries, _, _ := e.trash.List(); len(entries) != 0 {
		t.Fatalf("trash %+v", entries)
	}

	// state: slot 1 replaced (old one to the trash), slot 5 created.
	for _, slot := range []int{1, 5} {
		data := []byte("state")
		c := e.create(&Target{Kind: KindState, GameID: game, Slot: ptr(slot)}, data, "")
		e.send(c.UploadID, data, 0, len(data))
		res := e.complete(c.UploadID)
		if res["slot"] == nil {
			t.Fatalf("state result %v", res)
		}
	}
	if e.read(".userdata/shared/GBA-gpsp/Starlight Courier.gba.st5") != "state" {
		t.Fatal("state not written")
	}
	if entries, _, _ := e.trash.List(); len(entries) != 1 {
		t.Fatalf("trash %+v", entries)
	}

	// A locked slot is refused, at creation and at completion.
	// Another case of the ROM name where the file system ignores it (exFAT).
	lockName := "starlight courier.gba"
	if _, err := os.Lstat(filepath.Join(e.sd, "ROMS")); err != nil {
		lockName = "Starlight Courier.gba"
	}
	lockDir := filepath.Join(e.layout.LocksDir(gbaTag), lockName)
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	c = e.create(&Target{Kind: KindState, GameID: game, Slot: ptr(0)}, data, "")
	e.send(c.UploadID, data, 0, len(data))
	if err := os.WriteFile(filepath.Join(lockDir, "slot0.st"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, apiErr, _ := e.svc.CompleteUpload(context.Background(), c.UploadID); apiErr == nil || apiErr.Code != CodeStateLocked {
		t.Fatalf("locked at completion: %v", apiErr)
	}
	req := CreateUploadRequest{Target: &Target{Kind: KindState, GameID: game, Slot: ptr(0)}, SizeBytes: ptr(int64(1)), SHA256: ptr(sum([]byte{1}))}
	if _, apiErr, _ := e.svc.CreateUpload(req); apiErr == nil || apiErr.Code != CodeStateLocked {
		t.Fatalf("locked at creation: %v", apiErr)
	}

	// staging.
	data = []byte("patch")
	c = e.create(&Target{Kind: KindStaging, FileName: ptr("hack.bps")}, data, "")
	e.send(c.UploadID, data, 0, len(data))
	res = e.complete(c.UploadID)
	id, _ := res["stagingId"].(string)
	p, err := e.svc.StagingFile(id)
	if err != nil || filepath.Base(p) != "hack.bps" {
		t.Fatalf("staging %v %v", p, err)
	}
	if _, err := e.svc.StagingFile("st_../x"); !errors.Is(err, ErrStagingNotFound) {
		t.Fatal("bad staging id")
	}
}

func TestUploadCancelAndExpiry(t *testing.T) {
	e := newEnv(t)
	data := randomBytes(t, 2000)
	c := e.create(romTarget("Cancel.gba"), data, "")
	e.send(c.UploadID, data, 0, 1024)
	if apiErr := e.svc.CancelUpload(c.UploadID); apiErr != nil {
		t.Fatal(apiErr)
	}
	if _, err := os.Stat(filepath.Join(e.layout.UploadsDir(), c.UploadID+partExt)); !os.IsNotExist(err) {
		t.Fatal("partial file kept")
	}
	if apiErr := e.svc.CancelUpload(c.UploadID); apiErr == nil || apiErr.Code != CodeNotFound {
		t.Fatal("cancelled twice")
	}

	// Idle for 24 h: gone on next access.
	c = e.create(romTarget("Idle.gba"), data, "")
	e.clock.Advance(24*time.Hour + time.Second)
	if _, apiErr := e.svc.Status(c.UploadID); apiErr == nil || apiErr.Code != CodeNotFound {
		t.Fatalf("expired upload: %v", apiErr)
	}
	if des, _ := os.ReadDir(e.layout.UploadsDir()); len(des) != 0 {
		t.Fatalf("leftovers %v", des)
	}
}

func TestMaintainPurgesExpiredFiles(t *testing.T) {
	e := newEnv(t)
	data := randomBytes(t, 100)
	old := e.create(romTarget("Old.gba"), data, "")
	recent := e.create(romTarget("Recent.gba"), data, "")
	dir := e.layout.UploadsDir()
	past := e.clock.Now().Add(-25 * time.Hour)
	for _, ext := range []string{metaExt, partExt} {
		os.Chtimes(filepath.Join(dir, old.UploadID+ext), past, past)
	}
	os.WriteFile(filepath.Join(dir, "dl_0123.part"), []byte("partial download"), 0o644)
	stagingOld := filepath.Join(e.layout.StagingDir(), "st_"+past.UTC().Format(idTimeLayout)+"_00000000")
	stagingNew := filepath.Join(e.layout.StagingDir(), "st_"+e.clock.Now().UTC().Format(idTimeLayout)+"_00000001")
	for _, d := range []string{stagingOld, stagingNew} {
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "x.bps"), nil, 0o644)
	}
	e.svc.Close()
	e.svc = e.newService()
	if _, apiErr := e.svc.Status(old.UploadID); apiErr == nil {
		t.Error("expired upload kept")
	}
	if _, apiErr := e.svc.Status(recent.UploadID); apiErr != nil {
		t.Errorf("recent upload lost: %v", apiErr)
	}
	if _, err := os.Stat(filepath.Join(dir, "dl_0123.part")); !os.IsNotExist(err) {
		t.Error("partial download kept")
	}
	if _, err := os.Stat(stagingOld); !os.IsNotExist(err) {
		t.Error("old staging kept")
	}
	if _, err := os.Stat(stagingNew); err != nil {
		t.Error("recent staging lost")
	}
	if !strings.Contains(e.logs.String(), "deleted an expired upload") {
		t.Errorf("log: %s", e.logs.String())
	}
}

func TestLoadUploadsDropsBrokenOnes(t *testing.T) {
	e := newEnv(t)
	data := randomBytes(t, 100)
	a := e.create(romTarget("A.gba"), data, "")
	b := e.create(romTarget("B.gba"), data, "")
	dir := e.layout.UploadsDir()
	os.Remove(filepath.Join(dir, a.UploadID+partExt))                                // no partial file
	os.WriteFile(filepath.Join(dir, b.UploadID+partExt), randomBytes(t, 200), 0o644) // larger than announced
	os.WriteFile(filepath.Join(dir, "up_ffffffffffffffffffffffff.json"), []byte("{"), 0o644)
	e.svc.Close()
	e.svc = e.newService()
	for _, id := range []string{a.UploadID, b.UploadID} {
		if _, apiErr := e.svc.Status(id); apiErr == nil {
			t.Errorf("%s kept", id)
		}
	}
	if des, _ := os.ReadDir(dir); len(des) != 0 {
		t.Errorf("leftovers %v", des)
	}
}

// Codex review of M4: an overwrite whose new file cannot be renamed in
// place leaves neither an empty slot nor the old ROM purgeable: the old ROM
// goes back at once and the upload can be completed again.
func TestOverwriteRenameFailurePutsTheROMBack(t *testing.T) {
	e := newEnv(t)
	old := e.read("Roms/" + gbaSystem + "/Starlight Courier.gba")
	data := randomBytes(t, 3000)
	id := e.uploaded(romTarget("Starlight Courier.gba"), data, ConflictOverwrite)
	e.trash.SetRename(func(oldpath, newpath string) error {
		if strings.HasSuffix(oldpath, partExt) {
			return errors.New("simulated rename failure")
		}
		return os.Rename(oldpath, newpath)
	})
	if _, apiErr, err := e.svc.CompleteUpload(context.Background(), id); apiErr == nil && err == nil {
		t.Fatal("expected a failure")
	}
	if e.read("Roms/"+gbaSystem+"/Starlight Courier.gba") != old {
		t.Fatal("old ROM not put back")
	}
	if entries, _, _ := e.trash.List(); len(entries) != 0 {
		t.Fatalf("trash entry kept: %+v", entries)
	}
	e.trash.SetRename(os.Rename)
	e.complete(id)
	if e.read("Roms/"+gbaSystem+"/Starlight Courier.gba") != string(data) {
		t.Fatal("upload not completed on the second try")
	}
	if entries, _, _ := e.trash.List(); len(entries) != 1 {
		t.Fatalf("old ROM not in the trash: %+v", entries)
	}
}

// M8: a name sent in NFD is written in NFC.
func TestUploadNameIsNFC(t *testing.T) {
	e := newEnv(t)
	data := randomBytes(t, 100)
	id := e.uploaded(romTarget("Pétanque.gba"), data, "")
	res := e.complete(id)
	if res["gameId"] != gameID(gbaSystem+"/Pétanque.gba") || e.read("Roms/"+gbaSystem+"/Pétanque.gba") != string(data) {
		t.Fatalf("result %v", res)
	}
}
