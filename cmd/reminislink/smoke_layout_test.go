package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/app"
	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/osadapter/knulli"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/store"
	"github.com/tfournier/ReminisLink/internal/testutil"
	"github.com/tfournier/ReminisLink/internal/tlsid"
)

// Environment of TestSmokeLayouts (milestone M20; scripts/emulation/run.sh
// runs it inside the rootfs of each platform).
const (
	// LayoutBinEnv is the pak binary to run (default: built from this
	// package with CGO_ENABLED=0).
	LayoutBinEnv = "RL_LAYOUT_BIN"
	// LayoutsEnv is the comma-separated list of layouts to run (default:
	// all of smokeLayouts).
	LayoutsEnv = "RL_LAYOUTS"
	// LayoutZipEnv, when set, is a release zip (the NextUI pak or the
	// Knulli zip): it is unzipped where the firmware keeps it and the pak
	// is started through its launcher, without --sd (Codex review of M20).
	LayoutZipEnv = "RL_LAYOUT_ZIP"
)

// smokeLayout is a card layout of a platform, rebuilt from the sources of
// its firmware (docs/specs/05-console-pak.md §3).
type smokeLayout struct {
	name     string
	os       string // nextui or knulli
	platform string // PLATFORM (nextui)
	device   string // DEVICE exported by the launcher (nextui)
	model    string // model the pak reports
	// symlinkRoot: the card is reached through a symbolic link, as
	// /mnt/SDCARD -> /mnt/sdcard on h700 (NextUI h700-init.sh).
	symlinkRoot bool
}

var smokeLayouts = []smokeLayout{
	{name: "tg5040", os: nextui.OSName, platform: "tg5040", device: "brick", model: "brick"},
	{name: "tg5050", os: nextui.OSName, platform: "tg5050", device: "smartpros", model: "smartpros"},
	{name: "h700", os: nextui.OSName, platform: "h700", device: "rg35xxplus", model: "h700", symlinkRoot: true},
	{name: "my355", os: nextui.OSName, platform: "my355", device: "my355", model: "my355"},
	{name: "knulli", os: knulli.OSName, model: "h700"},
}

// gba is where the layout keeps the GBA games and their saves.
func (l smokeLayout) gba() (system, saveName, gameName string) {
	if l.os == knulli.OSName {
		return "gba", "Starlight Courier.srm", "Starlight Courier.gba"
	}
	return "Game Boy Advance (GBA)", "Starlight Courier.gba.sav", "Starlight Courier.gba"
}

// card builds the card of the layout in a temporary folder and returns
// the folder to pass to --sd, the roots the pak will use and the
// environment of the launcher.
func (l smokeLayout) card(t *testing.T) (string, osadapter.Roots, []string) {
	t.Helper()
	dir := t.TempDir()
	if l.os == knulli.OSName {
		if err := testutil.CopyTree(filepath.Join(testutil.RepoRoot(), "testdata", "knulli"), dir); err != nil {
			t.Fatal(err)
		}
		sd := filepath.Join(dir, "userdata")
		dev := filepath.Join(dir, "device")
		return sd, knulli.RootsForSD(sd), []string{
			"REMINISLINK_OS=knulli",
			"KNULLI_BOARD_FILE=" + filepath.Join(dev, "knulli.board"),
			"KNULLI_VERSION_FILE=" + filepath.Join(dev, "knulli.version"),
			"POWER_SUPPLY_PATH=" + filepath.Join(dev, "power_supply"),
		}
	}
	real := filepath.Join(dir, "sdcard")
	if err := testutil.CopyTree(testutil.SDCardSource(), real); err != nil {
		t.Fatal(err)
	}
	// The platform folders of testdata/sdcard are those of tg5040: the
	// other platforms use the same paths under their own name
	// (MinUI.pak/launch.sh of each platform).
	if l.platform != nextui.DefaultPlatform {
		for _, parent := range []string{".system", ".userdata", "Emus", "Tools"} {
			from := filepath.Join(real, parent, nextui.DefaultPlatform)
			if _, err := os.Stat(from); err != nil {
				continue
			}
			if err := os.Rename(from, filepath.Join(real, parent, l.platform)); err != nil {
				t.Fatal(err)
			}
		}
	}
	sd := real
	if l.symlinkRoot {
		// Another name than "sdcard": the host may be case-insensitive.
		sd = filepath.Join(dir, "SDCARD-link")
		if err := os.Symlink("sdcard", sd); err != nil {
			t.Fatal(err)
		}
	}
	return sd, nextui.RootsForSD(sd, l.platform), []string{
		"PLATFORM=" + l.platform, "DEVICE=" + l.device, "IS_NEXT=yes", "REMINISLINK_OS=",
	}
}

// layoutPak is a headless pak whose standard output is read for the
// pairing link.
type layoutPak struct {
	t    *testing.T
	cmd  *exec.Cmd
	logs bytes.Buffer
	mu   sync.Mutex
	link chan string
	eof  chan struct{} // closed when the standard output closes (exit)
}

func startLayoutPak(t *testing.T, argv []string, env []string) *layoutPak {
	t.Helper()
	p := &layoutPak{t: t, link: make(chan string, 1), eof: make(chan struct{})}
	p.cmd = exec.Command(argv[0], argv[1:]...)
	p.cmd.Env = append(os.Environ(), env...)
	p.cmd.Stderr = &lockedWriter{mu: &p.mu, w: &p.logs}
	out, err := p.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		defer close(p.eof)
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			// The link holds the pairing secret: it is never logged.
			if l, ok := strings.CutPrefix(sc.Text(), "Pairing link: "); ok {
				select {
				case p.link <- l:
				default:
				}
			}
		}
	}()
	return p
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(b)
}

func (p *layoutPak) stderr() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.logs.String()
}

func (p *layoutPak) stop() {
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

// pairHeadless pairs like the app from the link the headless pak printed
// (pairing v2, docs/specs/03-protocole.md §3) and returns a signing client.
func pairHeadless(t *testing.T, link string, port int) *testutil.Client {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("pairing link: %v", err)
	}
	q := u.Query()
	secret, err := auth.DecodeB64U(q.Get("s"))
	if err != nil || q.Get("v") != "2" || q.Get("id") == "" || q.Get("k") == "" {
		t.Fatal("malformed pairing link")
	}
	if q.Get("p") != strconv.Itoa(port) {
		t.Fatalf("link port %s, want %d", q.Get("p"), port)
	}
	httpc := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsid.ClientConfig(q.Get("k"))}}
	base := "https://127.0.0.1:" + strconv.Itoa(port)
	key := testutil.NewKey(t)
	clientID := "6f1d2c3b-4a5e-4f60-8a7b-9c0d1e2f3a4b"
	pub := auth.EncodeB64U(key.Public().(ed25519.PublicKey))
	nonce := make([]byte, auth.PairingNonceSize)
	_, _ = rand.Read(nonce)
	cn := auth.EncodeB64U(nonce)
	body, _ := json.Marshal(map[string]string{
		"clientId": clientID, "clientName": "Smoke layout", "platform": "apple",
		"clientPublicKey": pub, "clientNonce": cn,
		"proof": auth.PairClientProof(secret, q.Get("id"), clientID, pub, cn),
	})
	resp, err := httpc.Post(base+"/api/v1/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /pair: %v", err)
	}
	defer resp.Body.Close()
	var pr struct {
		DeviceID    string `json:"deviceId"`
		DeviceNonce string `json:"deviceNonce"`
		Proof       string `json:"proof"`
	}
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || json.Unmarshal(raw, &pr) != nil {
		t.Fatalf("POST /pair: %d %s", resp.StatusCode, raw)
	}
	if pr.DeviceID != q.Get("id") || !auth.VerifyPairDeviceProof(secret, pr.DeviceID, clientID, pub, cn, pr.DeviceNonce, pr.Proof) {
		t.Fatal("POST /pair: the device proof does not verify")
	}
	return &testutil.Client{BaseURL: base, ClientID: clientID, Key: key, Platform: "iphone", HTTP: httpc}
}

// TestSmokeLayouts runs the pak headless on the card of each platform and
// of Knulli (milestone M20): headless pairing, the library, an upload and
// a save backup, checked on the card.
func TestSmokeLayouts(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the pak")
	}
	bin := os.Getenv(LayoutBinEnv)
	zipFile := os.Getenv(LayoutZipEnv)
	if bin == "" && zipFile == "" {
		bin = filepath.Join(t.TempDir(), "reminislink")
		build := exec.Command("go", "build", "-o", bin, ".")
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build: %v\n%s", err, out)
		}
	}
	var want []string
	if v := os.Getenv(LayoutsEnv); v != "" {
		want = strings.Split(v, ",")
	}
	ran := 0
	for _, l := range smokeLayouts {
		if want != nil && !slices.Contains(want, l.name) {
			continue
		}
		ran++
		t.Run(l.name, func(t *testing.T) { runLayoutSmoke(t, bin, zipFile, l) })
	}
	if ran == 0 {
		t.Fatalf("%s=%q names no layout", LayoutsEnv, os.Getenv(LayoutsEnv))
	}
}

func runLayoutSmoke(t *testing.T, bin, zipFile string, l smokeLayout) {
	sd, roots, env := l.card(t)
	port := freePort(t)
	args := []string{"--headless", "--port", strconv.Itoa(port), "--name", "Smoke layout"}
	argv := append([]string{bin, "--sd", sd}, args...)
	if zipFile != "" {
		launcher, launcherEnv := l.install(t, zipFile, sd)
		argv = append([]string{launcher}, args...)
		env = append(env, launcherEnv...)
		t.Logf("through the launcher %s", launcher)
	}
	p := startLayoutPak(t, argv, env)
	defer p.stop()
	var link string
	select {
	case link = <-p.link:
	case <-p.eof:
		t.Fatalf("the headless pak exited before printing a pairing link:\n%s%s", p.stderr(), launcherLogs(roots))
	case <-time.After(30 * time.Second):
		t.Fatalf("no pairing link from the headless pak:\n%s%s", p.stderr(), launcherLogs(roots))
	}
	c := pairHeadless(t, link, port)
	t.Log("paired (pairing v2, device proof verified)")

	// The console as the pak describes it.
	var hello struct {
		Model, Platform, OS string
	}
	call(t, c, http.MethodGet, "/api/v1/hello", nil, http.StatusOK, &hello)
	wantPlatform := l.platform
	if l.os == knulli.OSName {
		wantPlatform = "rg35xx-plus" // testdata/knulli/device/knulli.board
	}
	if hello.Model != l.model || hello.Platform != wantPlatform || hello.OS != l.os {
		t.Fatalf("hello = %+v", hello)
	}

	// The library.
	system, saveName, gameName := l.gba()
	sysID := library.EncodeID(system)
	var systems struct {
		Systems []struct{ ID string } `json:"systems"`
	}
	call(t, c, http.MethodGet, "/api/v1/systems", nil, http.StatusOK, &systems)
	if !slices.ContainsFunc(systems.Systems, func(s struct{ ID string }) bool { return s.ID == sysID }) {
		t.Fatalf("systems %+v: no %s", systems, system)
	}
	type game struct {
		ID, FileName string
		HasSave      bool `json:"hasSave"`
	}
	var listing struct{ Games []game }
	call(t, c, http.MethodGet, "/api/v1/systems/"+sysID+"/games", nil, http.StatusOK, &listing)
	i := slices.IndexFunc(listing.Games, func(g game) bool { return g.FileName == gameName })
	if i < 0 || !listing.Games[i].HasSave {
		t.Fatalf("games %+v: no %s with a save", listing.Games, gameName)
	}
	gameID := listing.Games[i].ID
	t.Logf("library: %d systems, %d games in %s", len(systems.Systems), len(listing.Games), system)

	// An upload lands in the system folder.
	data := make([]byte, 300<<10)
	rand.Read(data)
	sum := sha256.Sum256(data)
	var created struct {
		UploadID  string `json:"uploadId"`
		ChunkSize int    `json:"chunkSize"`
	}
	call(t, c, http.MethodPost, "/api/v1/uploads", map[string]any{
		"target":    map[string]any{"kind": "rom", "systemId": sysID, "fileName": "Smoke Layout.gba"},
		"sizeBytes": len(data), "sha256": hex.EncodeToString(sum[:]),
	}, http.StatusCreated, &created)
	for off := 0; off < len(data); off += created.ChunkSize {
		end := min(off+created.ChunkSize, len(data))
		req := c.RequestUnsigned(t, http.MethodPut, "/api/v1/uploads/"+created.UploadID, data[off:end])
		req.Header.Set("Upload-Offset", strconv.Itoa(off))
		if r := c.Send(t, req); r.Status != http.StatusNoContent {
			t.Fatalf("chunk at %d: %d %s", off, r.Status, r.Body)
		}
	}
	call(t, c, http.MethodPost, "/api/v1/uploads/"+created.UploadID+"/complete", nil, http.StatusOK, nil)
	installed, err := os.ReadFile(filepath.Join(roots.Roms, system, "Smoke Layout.gba"))
	if err != nil || sha256.Sum256(installed) != sum {
		t.Fatalf("uploaded game: wrong SHA-256 (%v)", err)
	}
	t.Log("upload: installed and verified on the card")

	// A save backup copies the save the OS wrote.
	var backup struct {
		ID        string `json:"id"`
		SizeBytes int64  `json:"sizeBytes"`
	}
	call(t, c, http.MethodPost, "/api/v1/games/"+gameID+"/save/backups", map[string]any{"label": "smoke " + l.name}, http.StatusCreated, &backup)
	save, err := os.ReadFile(filepath.Join(roots.Saves, sysTag(l, system), saveName))
	if err != nil || backup.SizeBytes != int64(len(save)) {
		t.Fatalf("backup %+v of %s (%d bytes, %v)", backup, saveName, len(save), err)
	}
	var backups struct {
		Backups []struct{ ID string } `json:"backups"`
	}
	call(t, c, http.MethodGet, "/api/v1/games/"+gameID+"/save/backups", nil, http.StatusOK, &backups)
	if !slices.ContainsFunc(backups.Backups, func(b struct{ ID string }) bool { return b.ID == backup.ID }) {
		t.Fatalf("backups %+v: no %s", backups, backup.ID)
	}
	// The copy is in the backup area of the tag, with the bytes of the save.
	backupsDir := store.Layout{Root: roots.Data}.BackupsDir(sysTag(l, system))
	found := false
	_ = filepath.WalkDir(backupsDir, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if b, err := os.ReadFile(path); err == nil && bytes.Equal(b, save) {
				found = true
			}
		}
		return nil
	})
	if !found {
		t.Fatalf("no copy of %s under %s", saveName, backupsDir)
	}
	t.Logf("save backup %s: %d bytes, copy under the ReminisLink data folder", backup.ID, backup.SizeBytes)

	call(t, c, http.MethodDelete, "/api/v1/clients/me", nil, http.StatusNoContent, nil)
	p.stop()
	if _, err := os.Stat(filepath.Join(roots.Logs, app.LogFileName)); err != nil {
		t.Errorf("log file: %v", err)
	}
}

// sysTag is the tag naming the save folder of the GBA system.
func sysTag(l smokeLayout, system string) string {
	if l.os == knulli.OSName {
		return system
	}
	return "GBA"
}

// install unzips a release zip where the firmware keeps the pak and
// returns its launcher with the environment the firmware gives it.
func (l smokeLayout) install(t *testing.T, zipFile, sd string) (string, []string) {
	t.Helper()
	if l.os == knulli.OSName {
		unzipInto(t, zipFile, filepath.Join(sd, "roms", "tools"), "")
		// Knulli exports nothing ReminisLink reads; SDCARD_PATH moves
		// /userdata to the test card. REMINISLINK_OS is left out: the
		// launcher passes --os knulli.
		return filepath.Join(sd, "roms", "tools", "ReminisLink.sh"), []string{"SDCARD_PATH=" + sd, "REMINISLINK_OS="}
	}
	dir := filepath.Join(sd, "Tools", l.platform, "ReminisLink.pak")
	// As the Pak Store does: Tools/<PLATFORM>/ReminisLink.pak/ created,
	// the zip extracted into it as is; launch.sh must be right there (a
	// zip nested in a ReminisLink.pak/ folder fails here).
	unzipInto(t, zipFile, dir, "")
	userdata := filepath.Join(sd, ".userdata", l.platform)
	env := []string{
		// As MinUI.pak/launch.sh exports them (my355 exports no ROMS_PATH).
		"SDCARD_PATH=" + sd, "SAVES_PATH=" + filepath.Join(sd, "Saves"), "CHEATS_PATH=" + filepath.Join(sd, "Cheats"),
		"BIOS_PATH=" + filepath.Join(sd, "Bios"), "SYSTEM_PATH=" + filepath.Join(sd, ".system", l.platform),
		"USERDATA_PATH=" + userdata, "SHARED_USERDATA_PATH=" + filepath.Join(sd, ".userdata", "shared"),
		"LOGS_PATH=" + filepath.Join(userdata, "logs"),
		// A REMINISLINK_OS left by another tool must not pick the Knulli
		// layout: launch.sh passes --os nextui.
		"REMINISLINK_OS=knulli",
	}
	if l.platform != "my355" {
		env = append(env, "ROMS_PATH="+filepath.Join(sd, "Roms"))
	}
	return filepath.Join(dir, "launch.sh"), env
}

// unzipInto extracts zipFile into dir, without the prefix strip of each
// entry, keeping the file modes; an entry that would leave dir fails.
func unzipInto(t *testing.T, zipFile, dir, strip string) {
	t.Helper()
	zr, err := zip.OpenReader(zipFile)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		name := strings.TrimPrefix(f.Name, strip)
		if name == "" || strings.HasSuffix(name, "/") {
			continue
		}
		if !filepath.IsLocal(name) {
			t.Fatalf("zip entry %q leaves its folder", f.Name)
		}
		dst := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			t.Fatal(err)
		}
		// The mode of the zip, even over a file already there.
		if err := os.Chmod(dst, f.Mode().Perm()|0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// launcherLogs returns the log files a launcher writes (its stderr goes
// there, not to the test), for a failure message.
func launcherLogs(roots osadapter.Roots) string {
	var b strings.Builder
	for _, dir := range []string{roots.Logs, "/userdata/system/logs"} {
		for _, n := range []string{"ReminisLink.stderr.txt", app.LogFileName} {
			if data, err := os.ReadFile(filepath.Join(dir, n)); err == nil {
				b.WriteString("--- " + filepath.Join(dir, n) + "\n" + string(data))
			}
		}
	}
	return b.String()
}
