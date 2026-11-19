package app

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/discovery"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/server"
	"github.com/tfournier/ReminisLink/internal/testutil"
	"github.com/tfournier/ReminisLink/internal/transfer"
)

// fakeBonjour records the announcements.
type fakeBonjour struct {
	mu     sync.Mutex
	active []string // names currently announced
	all    []string // every announcement, in order
	infos  []discovery.Info
}

type fakeAnn struct {
	b    *fakeBonjour
	name string
}

func (f *fakeAnn) Shutdown() {
	f.b.mu.Lock()
	defer f.b.mu.Unlock()
	for i, n := range f.b.active {
		if n == f.name {
			f.b.active = append(f.b.active[:i], f.b.active[i+1:]...)
			return
		}
	}
}

func (b *fakeBonjour) announce(name string, port int, info discovery.Info) (withdrawer, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.active = append(b.active, name)
	b.all = append(b.all, name)
	b.infos = append(b.infos, info)
	return &fakeAnn{b: b, name: name}, nil
}

func (b *fakeBonjour) state() ([]string, []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.active...), append([]string(nil), b.all...)
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

func openTest(t *testing.T, opts Options) (*App, string) {
	t.Helper()
	prev := slogDefault()
	t.Cleanup(func() { restoreSlog(prev) })
	sd := testutil.CopySDCard(t)
	if opts.Roots.SD == "" {
		opts.Roots = nextui.RootsForSD(sd, "tg5040")
	}
	if opts.Env == nil {
		opts.Env = func(k string) string { return map[string]string{"PLATFORM": "tg5040"}[k] }
	}
	if opts.Version == "" {
		opts.Version = "1.0.0-test"
	}
	a, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		a.Shutdown()
		a.Close()
	})
	return a, sd
}

func TestLifecycle(t *testing.T) {
	bj := &fakeBonjour{}
	a, sd := openTest(t, Options{Mode: "test", Port: freePort(t), Name: "Brick de test"})
	a.announce = bj.announce
	if err := a.Maintain(); err != nil {
		t.Fatal(err)
	}
	if err := a.Start("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if active, _ := bj.state(); len(active) != 1 || active[0] != "Brick de test" {
		t.Fatalf("announced %v", active)
	}
	if bj.infos[0].DeviceID != a.DeviceID() || bj.infos[0].PakVersion != "1.0.0-test" {
		t.Fatalf("TXT info %+v", bj.infos[0])
	}
	base := "http://127.0.0.1:" + itoa(a.Port())
	hello := func() map[string]any {
		t.Helper()
		resp, err := http.Get(base + "/api/v1/hello")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var h map[string]any
		json.NewDecoder(resp.Body).Decode(&h)
		return h
	}
	if h := hello(); h["deviceName"] != "Brick de test" {
		t.Fatalf("hello %v", h)
	}

	st, err := a.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateReady || st.Name != "Brick de test" || st.IP != "127.0.0.1" || st.Port != a.Port() ||
		len(st.PIN) != 6 || !strings.HasPrefix(st.PairingLink, auth.PairingURLScheme+"://pair?") ||
		!strings.Contains(st.PairingLink, "n=Brick%20de%20test") || st.Notice != nil || len(st.Connected) != 0 {
		t.Fatalf("status %+v", st)
	}

	// A signed request makes the state Connected.
	c := testutil.PairClient(t, a.clients, base)
	r := c.Do(t, "GET", "/api/v1/systems", nil)
	if r.Status != 200 {
		t.Fatalf("systems %d", r.Status)
	}
	if st, _ := a.Status(); st.State != StateConnected || len(st.Connected) != 1 {
		t.Fatalf("connected status %+v", st)
	}

	// Rename: config.json, /hello, Bonjour.
	name, err := a.SetDeviceName("  Brick du salon\n")
	if err != nil || name != "Brick du salon" {
		t.Fatalf("rename: %q %v", name, err)
	}
	if h := hello(); h["deviceName"] != "Brick du salon" {
		t.Fatalf("hello after rename %v", h)
	}
	if active, all := bj.state(); len(active) != 1 || active[0] != "Brick du salon" || len(all) != 2 {
		t.Fatalf("announcements after rename: active %v, all %v", active, all)
	}
	conf := readConfig(t, sd)
	if conf["deviceName"] != "Brick du salon" {
		t.Fatalf("config.json %v", conf)
	}
	if _, err := a.SetDeviceName(" \t"); !errors.Is(err, ErrEmptyName) {
		t.Fatalf("empty name: %v", err)
	}

	// Language.
	if a.Language() != "" {
		t.Fatalf("language %q", a.Language())
	}
	if err := a.SetLanguage("fr"); err != nil || a.Language() != "fr" || readConfig(t, sd)["language"] != "fr" {
		t.Fatalf("language: %v %v", err, readConfig(t, sd))
	}
	// The other keys of config.json are kept (credit clock).
	if _, ok := readConfig(t, sd)["creditSeconds"]; !ok {
		t.Fatalf("credit clock keys lost: %v", readConfig(t, sd))
	}

	// Paired clients.
	if list := a.PairedClients(); len(list) != 1 || list[0].ID != c.ClientID {
		t.Fatalf("paired %+v", list)
	}
	if err := a.ForgetClient(c.ClientID); err != nil || len(a.PairedClients()) != 0 {
		t.Fatalf("forget: %v", err)
	}
	if r := c.Do(t, "GET", "/api/v1/systems", nil); r.Status != http.StatusUnauthorized {
		t.Fatalf("forgotten client still accepted: %d", r.Status)
	}
	testutil.PairClient(t, a.clients, base)
	testutil.PairClient(t, a.clients, base)
	if n, err := a.ForgetAllClients(); n != 2 || err != nil {
		t.Fatalf("forget all: %d %v", n, err)
	}

	if a.Busy() {
		t.Fatal("busy without transfer")
	}
	if !strings.HasSuffix(a.LogPath(), LogFileName) || a.Version() != "1.0.0-test" {
		t.Fatalf("log path %q", a.LogPath())
	}

	// Shutdown: the announcement is withdrawn, the port closed.
	if err := a.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := a.Shutdown(); err != nil {
		t.Fatal("second shutdown", err)
	}
	if active, _ := bj.state(); len(active) != 0 {
		t.Fatalf("still announced: %v", active)
	}
	if _, err := http.Get(base + "/api/v1/hello"); err == nil {
		t.Fatal("server still listening")
	}
}

func TestStatusTransferAndNotice(t *testing.T) {
	a, _ := openTest(t, Options{Port: freePort(t)})
	a.announce = (&fakeBonjour{}).announce
	if err := a.Start("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	a.xfer.Jobs().SubmitLabeled(transfer.JobDownload, "Sky Hack.bps", func(ctx contextT, report func(int64, *int64)) (map[string]any, *transfer.Error) {
		<-release
		return nil, nil
	})
	if st, _ := a.Status(); st.State != StateTransfer || len(st.Activities) != 1 || !a.Busy() {
		t.Fatalf("transfer status %+v", st)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, _ := a.Status()
		if st.State == StateReady && st.Notice != nil && st.Notice.Kind == NoticeDownloaded && st.Notice.Name == "Sky Hack.bps" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status after the job %+v", st)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestFatalErrorIsLogged(t *testing.T) {
	prev := slogDefault()
	defer restoreSlog(prev)
	sd := t.TempDir()
	conf := filepath.Join(sd, ".userdata", "shared", "ReminisLink", "config.json")
	if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conf, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Open(Options{Roots: nextui.RootsForSD(sd, "tg5040")})
	var logged LoggedError
	if err == nil || !errors.As(err, &logged) {
		t.Fatalf("broken config.json: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(sd, ".userdata", "tg5040", "logs", LogFileName))
	if !strings.Contains(string(data), "level=ERROR msg=fatal") {
		t.Fatalf("fatal error not in the log file:\n%s", data)
	}
}

func TestWiFiIPv4(t *testing.T) {
	a, _ := openTest(t, Options{})
	var asked []string
	a.ipv4 = func(p string) (string, bool) { asked = append(asked, "any:"+p); return "10.0.0.2", true }
	a.ifaceIPv4 = func(n string) (string, bool) { asked = append(asked, "only:"+n); return "", false }
	// Off the console (no DEVICE): any interface.
	if ip, ok := a.WiFiIPv4(); !ok || ip != "10.0.0.2" || !a.IsDesktop() {
		t.Fatalf("desktop: %q %v", ip, ok)
	}
	// On the console: the Wi-Fi interface only.
	a.info.Model = "brick"
	if _, ok := a.WiFiIPv4(); ok {
		t.Fatal("console without wlan0 address")
	}
	if strings.Join(asked, ",") != "any:wlan0,only:wlan0" {
		t.Fatalf("lookups %v", asked)
	}
}

func TestCleanDeviceName(t *testing.T) {
	long := strings.Repeat("é", 40) // 80 bytes
	cases := map[string]string{
		"Brick":              "Brick",
		"  Brick du salon  ": "Brick du salon",
		"Bri\x00ck\n":        "Brick",
		long:                 strings.Repeat("é", 31),
		"Pixel 🌱":            "Pixel 🌱",
	}
	for in, want := range cases {
		if got, err := CleanDeviceName(in); err != nil || got != want {
			t.Errorf("CleanDeviceName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "   ", "\n\t"} {
		if _, err := CleanDeviceName(in); !errors.Is(err, ErrEmptyName) {
			t.Errorf("CleanDeviceName(%q): %v", in, err)
		}
	}
}

func TestNotices(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	n := newNotices(func() time.Time { return now })
	if _, ok := n.current(); ok {
		t.Fatal("notice at start")
	}
	n.change(server.Change{Kind: server.ChangeSaveRestored, Name: "Sky Garden Quest"})
	if x, ok := n.current(); !ok || x.Kind != server.ChangeSaveRestored || x.Name != "Sky Garden Quest" {
		t.Fatalf("current %+v", x)
	}
	now = now.Add(NoticeDuration)
	if _, ok := n.current(); ok {
		t.Fatal("notice kept too long")
	}
	for state, want := range map[string]string{
		transfer.JobSucceeded: NoticePatched, transfer.JobFailed: NoticePatchFailed, transfer.JobCancelled: "",
	} {
		n.last = Notice{}
		n.jobFinished(transfer.Job{Kind: transfer.JobPatch, State: state, Label: "Moon"})
		x, ok := n.current()
		if want == "" && ok || want != "" && (x.Kind != want || x.Name != "Moon") {
			t.Errorf("job %s: %+v %v", state, x, ok)
		}
	}
	n.jobFinished(transfer.Job{Kind: transfer.JobDownload, State: transfer.JobFailed})
	if x, _ := n.current(); x.Kind != NoticeDownloadFailed {
		t.Errorf("download failed: %+v", x)
	}
}

func readConfig(t *testing.T, sd string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sd, ".userdata", "shared", "ReminisLink", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
