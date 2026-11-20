package app

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/discovery"
	"github.com/tfournier/ReminisLink/internal/library"
	"github.com/tfournier/ReminisLink/internal/osadapter/nextui"
	"github.com/tfournier/ReminisLink/internal/server"
	"github.com/tfournier/ReminisLink/internal/testutil"
	"github.com/tfournier/ReminisLink/internal/tlsid"
	"github.com/tfournier/ReminisLink/internal/transfer"
)

// fakeBonjour records the announcements.
type fakeBonjour struct {
	mu     sync.Mutex
	fail   error    // returned by announce when set
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
	if b.fail != nil {
		return nil, b.fail
	}
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
	a.checkEvery = time.Hour // tests call checkNetwork themselves
	t.Cleanup(func() {
		a.Shutdown()
		a.Close()
	})
	return a, sd
}

func TestLifecycle(t *testing.T) {
	bj := &fakeBonjour{}
	a, sd := openTest(t, Options{Mode: "test", Port: freePort(t), Name: "Brick de test", Pairing: PairingAlways})
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
	base := httpsBase(a)
	hello := func() map[string]any {
		t.Helper()
		resp, err := pinnedHTTP(a).Get(base + "/api/v1/hello")
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
		!strings.HasPrefix(st.PairingLink, auth.PairingURLScheme+"://pair?v=2&") || !strings.HasSuffix(st.PairingLink, "&k="+a.Pin()) ||
		!strings.Contains(st.PairingLink, "n=Brick%20de%20test") || st.Notice != nil || len(st.Connected) != 0 {
		t.Fatalf("status %+v", st)
	}

	// A signed request makes the state Connected.
	c := pairTLS(t, a)
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
	pairTLS(t, a)
	pairTLS(t, a)
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
	if _, err := pinnedHTTP(a).Get(base + "/api/v1/hello"); err == nil {
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

func TestExitOnSignalDuringMaintenance(t *testing.T) {
	a, _ := openTest(t, Options{Port: freePort(t)})
	a.announce = (&fakeBonjour{}).announce
	inMaint := make(chan struct{})
	a.maintenance = func(ctx contextT) error {
		close(inMaint)
		<-ctx.Done() // a long maintenance, cancelled by the shutdown
		return nil
	}
	maintErr := make(chan error, 1)
	go func() { maintErr <- a.Maintain() }()
	<-inMaint

	sigs := make(chan os.Signal, 1)
	var cleaned, exited bool
	exitCode := -1
	done := make(chan struct{})
	go func() {
		a.ExitOnSignal(sigs, nil, func() { cleaned = true }, func(code int) { exited, exitCode = true, code })
		close(done)
	}()
	sigs <- syscall.SIGTERM
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ExitOnSignal did not finish")
	}
	if err := <-maintErr; !errors.Is(err, ErrStopped) {
		t.Fatalf("maintenance: %v", err)
	}
	if !cleaned || !exited || exitCode != 0 {
		t.Fatalf("cleanup %v exit %v code %d", cleaned, exited, exitCode)
	}
	// Nothing starts after the shutdown.
	if err := a.Start("127.0.0.1"); !errors.Is(err, ErrStopped) {
		t.Fatalf("Start after shutdown: %v", err)
	}
	if err := a.Maintain(); !errors.Is(err, ErrStopped) {
		t.Fatalf("Maintain after shutdown: %v", err)
	}
}

func TestExitOnSignalWhileServing(t *testing.T) {
	a, _ := openTest(t, Options{Port: freePort(t)})
	bj := &fakeBonjour{}
	a.announce = bj.announce
	if err := a.Start("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	sigs := make(chan os.Signal, 2)
	exited := make(chan int, 1)
	go a.ExitOnSignal(sigs, nil, nil, func(code int) { exited <- code })
	sigs <- syscall.SIGTERM
	sigs <- syscall.SIGTERM // a second signal is absorbed
	select {
	case code := <-exited:
		if code != 0 {
			t.Fatalf("exit code %d", code)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no exit")
	}
	if active, _ := bj.state(); len(active) != 0 {
		t.Fatalf("still announced %v", active)
	}
	select {
	case <-a.Stopped():
	default:
		t.Fatal("server still running")
	}
	// stop ends the wait without shutting down.
	b, _ := openTest(t, Options{})
	stop := make(chan struct{})
	close(stop)
	b.ExitOnSignal(make(chan os.Signal), stop, nil, func(int) { t.Fatal("exit called") })
}

func TestCheckNetwork(t *testing.T) {
	a, _ := openTest(t, Options{Port: freePort(t), Pairing: PairingAlways})
	bj := &fakeBonjour{}
	a.announce = bj.announce
	a.info.Model = "brick" // on the console: wlan0 only
	ip, ok := "192.168.1.42", true
	a.ifaceIPv4 = func(string) (string, bool) { return ip, ok }
	a.checkEvery = time.Hour // checkNetwork is called by hand
	if err := a.Start("192.168.1.42"); err != nil {
		t.Fatal(err)
	}
	link := func() string {
		st, err := a.Status()
		if err != nil {
			t.Fatal(err)
		}
		return st.PairingLink
	}
	a.checkNetwork() // unchanged: nothing
	if _, all := bj.state(); len(all) != 1 || !strings.Contains(link(), "h=192.168.1.42") {
		t.Fatalf("unchanged: %v %s", all, link())
	}
	// After a sleep, DHCP gives another address.
	ip = "192.168.1.77"
	a.checkNetwork()
	active, all := bj.state()
	if len(all) != 2 || len(active) != 1 || !strings.Contains(link(), "h=192.168.1.77") || a.IP() != "192.168.1.77" {
		t.Fatalf("new address: active %v all %v link %s", active, all, link())
	}
	// No address: offline, announcement withdrawn, no link.
	ip, ok = "", false
	a.checkNetwork()
	st, _ := a.Status()
	if st.State != StateNoWiFi || st.WiFi || st.PairingLink != "" || st.IP != "" || a.WiFi() {
		t.Fatalf("offline status %+v", st)
	}
	if active, _ := bj.state(); len(active) != 0 {
		t.Fatalf("announced while offline: %v", active)
	}
	// Back.
	ip, ok = "192.168.1.80", true
	a.checkNetwork()
	st, _ = a.Status()
	if st.State != StateReady || !strings.Contains(st.PairingLink, "h=192.168.1.80") {
		t.Fatalf("back online %+v", st)
	}
	if active, _ := bj.state(); len(active) != 1 {
		t.Fatalf("announcement after coming back: %v", active)
	}
}

func TestCheckNetworkFallback(t *testing.T) {
	a, _ := openTest(t, Options{Port: freePort(t), NoWiFiIP: "127.0.0.1"})
	a.announce = (&fakeBonjour{}).announce
	a.info.Model = "brick"
	a.ifaceIPv4 = func(string) (string, bool) { return "", false }
	a.checkEvery = time.Hour
	if err := a.Start("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	a.checkNetwork()
	if st, _ := a.Status(); !st.WiFi || st.IP != "127.0.0.1" || st.State == StateNoWiFi {
		t.Fatalf("headless fallback %+v", st)
	}
}

func TestCheckNetworkRetriesTheAnnouncement(t *testing.T) {
	a, _ := openTest(t, Options{Port: freePort(t)})
	bj := &fakeBonjour{}
	a.announce = bj.announce
	a.info.Model = "brick"
	ip := "192.168.1.42"
	a.ifaceIPv4 = func(string) (string, bool) { return ip, true }
	if err := a.Start(ip); err != nil {
		t.Fatal(err)
	}
	// At wake-up the address changed but the registration fails.
	bj.mu.Lock()
	bj.fail = errors.New("mdns: network is down")
	bj.mu.Unlock()
	ip = "192.168.1.77"
	a.checkNetwork()
	if active, _ := bj.state(); len(active) != 0 || a.announced() {
		t.Fatalf("announced despite the failure: %v", active)
	}
	// Same address, still failing: tried again, not announced.
	a.checkNetwork()
	// The network works again: the next check announces, with the address unchanged.
	bj.mu.Lock()
	bj.fail = nil
	bj.mu.Unlock()
	a.checkNetwork()
	if active, _ := bj.state(); len(active) != 1 || !a.announced() {
		t.Fatalf("announcement not retried: %v", active)
	}
	// Announced and unchanged: nothing more.
	a.checkNetwork()
	if _, all := bj.state(); len(all) != 2 {
		t.Fatalf("announced again without reason: %v", all)
	}
}

func TestPairingModes(t *testing.T) {
	// The UI opens pairing while it shows the QR code.
	a, _ := openTest(t, Options{Port: freePort(t)})
	a.announce = (&fakeBonjour{}).announce
	if err := a.Start("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	st, _ := a.Status()
	if st.PairingOpen || st.PairingLink != "" || st.Paired {
		t.Fatalf("manual mode opened pairing: %+v", st)
	}
	if err := a.SetPairingVisible(true); err != nil {
		t.Fatal(err)
	}
	st, _ = a.Status()
	if !st.PairingOpen || st.PairingLink == "" {
		t.Fatalf("visible pairing screen: %+v", st)
	}
	shown := st.PairingLink
	if err := a.SetPairingVisible(false); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Status(); st.PairingOpen || st.PairingLink != "" {
		t.Fatalf("hidden pairing screen: %+v", st)
	}
	a.SetPairingVisible(true)
	if st, _ := a.Status(); st.PairingLink == shown {
		t.Fatal("the secret was not drawn again")
	}

	// Headless: open while no device is paired.
	b, _ := openTest(t, Options{Port: freePort(t), Pairing: PairingWhileUnpaired})
	b.announce = (&fakeBonjour{}).announce
	if err := b.Start("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if !b.pairing.IsOpen() {
		t.Fatal("headless without client: pairing closed")
	}
	if err := b.SetPairingVisible(false); err != nil || !b.pairing.IsOpen() {
		t.Fatal("SetPairingVisible applies to the UI only")
	}
	c := pairTLS(t, b)
	b.paired(auth.Client{ID: c.ClientID, Name: "iPhone"})
	if b.pairing.IsOpen() {
		t.Fatal("still open with a paired client")
	}
	if st, _ := b.Status(); !st.Paired || st.PairingLink != "" {
		t.Fatalf("status with a client: %+v", st)
	}
	if err := b.ForgetClient(c.ClientID); err != nil || !b.pairing.IsOpen() {
		t.Fatalf("not reopened once no client is left: %v", err)
	}

	// --pairing-open: always open.
	d, _ := openTest(t, Options{Port: freePort(t), Pairing: PairingAlways})
	d.announce = (&fakeBonjour{}).announce
	if err := d.Start("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	pairTLS(t, d)
	d.checkNetwork()
	if !d.pairing.IsOpen() {
		t.Fatal("--pairing-open closed pairing")
	}
}

// TestDevicesScreen: the logic behind Settings > Devices (M10, M13:
// platforms), with two signed clients against the running server.
func TestDevicesScreen(t *testing.T) {
	a, _ := openTest(t, Options{Mode: "test", Port: freePort(t), Name: "Brick de test", Pairing: PairingAlways})
	a.announce = (&fakeBonjour{}).announce
	if err := a.Maintain(); err != nil {
		t.Fatal(err)
	}
	if err := a.Start("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	c1 := pairTLS(t, a)
	c2 := pairTLS(t, a)
	c1.Platform, c2.Platform = "iphone", "ipad"
	for _, c := range []*testutil.Client{c1, c2} {
		if r := c.Do(t, "GET", "/api/v1/systems", nil); r.Status != 200 {
			t.Fatal(r.Status)
		}
	}
	st, _ := a.Status()
	if len(st.Connected) != 2 {
		t.Fatalf("status %+v", st)
	}
	devs := a.Devices()
	if len(devs) != 2 {
		t.Fatalf("devices %+v", devs)
	}
	for _, d := range devs {
		want := map[string]string{c1.ClientID: "iphone", c2.ClientID: "ipad"}[d.ID]
		if !d.Connected || d.Disconnected || d.Platform != "apple" || len(d.Platforms) != 1 || d.Platforms[0] != want {
			t.Errorf("device %+v", d)
		}
	}

	// Disconnect: 401 disconnected, gone from the status.
	if err := a.DisconnectClient(c1.ClientID); err != nil {
		t.Fatal(err)
	}
	if r := c1.Do(t, "GET", "/api/v1/systems", nil); r.Status != 401 || r.ErrorCode() != "disconnected" {
		t.Fatalf("disconnected: %d %s", r.Status, r.Body)
	}
	if st, _ := a.Status(); len(st.Connected) != 1 {
		t.Fatalf("status after disconnect %+v", st)
	}
	for _, d := range a.Devices() {
		if d.ID == c1.ClientID && (!d.Disconnected || d.Connected) {
			t.Errorf("disconnected device %+v", d)
		}
	}
	if r := c1.Do(t, "POST", "/api/v1/clients/me/connect", nil); r.Status != 204 {
		t.Fatalf("connect: %d %s", r.Status, r.Body)
	}

	// Forget: removed, 401 unknown_client.
	if err := a.ForgetClient(c2.ClientID); err != nil {
		t.Fatal(err)
	}
	if r := c2.Do(t, "GET", "/api/v1/systems", nil); r.Status != 401 || r.ErrorCode() != "unknown_client" {
		t.Fatalf("forgotten: %d %s", r.Status, r.Body)
	}
	if devs := a.Devices(); len(devs) != 1 || devs[0].ID != c1.ClientID {
		t.Fatalf("devices after forget %+v", devs)
	}
	if err := a.DisconnectClient(c2.ClientID); err == nil {
		t.Fatal("disconnected a forgotten client")
	}
}

// Quitting from the console (Quit) and the system stopping the pak
// (Shutdown, the SIGTERM path) send the shutdown event to the open
// streams with their reason, then end them; the announcement is still
// withdrawn.
func TestShutdownEvent(t *testing.T) {
	for _, tc := range []struct {
		reason string
		stop   func(*App) error
	}{
		{"quit", (*App).Quit},
		{"stopped", (*App).Shutdown},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			bj := &fakeBonjour{}
			a, _ := openTest(t, Options{Mode: "test", Port: freePort(t), Name: "Brick", Pairing: PairingAlways})
			a.announce = bj.announce
			if err := a.Maintain(); err != nil {
				t.Fatal(err)
			}
			if err := a.Start("127.0.0.1"); err != nil {
				t.Fatal(err)
			}
			c := pairTLS(t, a)
			s := testutil.OpenSSE(t, c)
			if s.Resp.StatusCode != http.StatusOK {
				t.Fatalf("GET /events: %d", s.Resp.StatusCode)
			}
			if ev, err := s.NextEvent(5 * time.Second); err != nil || ev.Type != "hello" {
				t.Fatalf("hello: %+v %v", ev, err)
			}
			start := time.Now()
			stopped := make(chan error, 1)
			go func() { stopped <- tc.stop(a) }()
			ev, err := s.NextEvent(5 * time.Second)
			if err != nil || ev.Type != "shutdown" || ev.Data != `{"reason":"`+tc.reason+`"}` {
				t.Fatalf("shutdown event: %+v %v", ev, err)
			}
			if _, err := s.NextEvent(5 * time.Second); err == nil {
				t.Fatal("the stream goes on after the shutdown event")
			}
			if err := <-stopped; err != nil {
				t.Fatal(err)
			}
			if d := time.Since(start); d > 2*time.Second {
				t.Errorf("shutdown took %v", d)
			}
			if active, _ := bj.state(); len(active) != 0 {
				t.Fatalf("still announced: %v", active)
			}
		})
	}
}

// The Bonjour goodbye does not wait for the server: with a request stuck
// (its body never comes, the server would wait server.ShutdownTimeout),
// the announcement is withdrawn at once.
func TestShutdownWithdrawsBeforeTheServerStops(t *testing.T) {
	bj := &fakeBonjour{}
	a, _ := openTest(t, Options{Mode: "test", Port: freePort(t), Name: "Brick", Pairing: PairingAlways})
	a.announce = bj.announce
	if err := a.Maintain(); err != nil {
		t.Fatal(err)
	}
	if err := a.Start("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	addr := "127.0.0.1:" + itoa(a.Port())
	c := pairTLS(t, a)
	body := make([]byte, 1<<20)
	target := "/api/v1/games/" + library.EncodeID("Game Boy Advance (GBA)/Starlight Courier.gba") + "/save"
	req := c.Request(t, "PUT", target, body)
	conn, err := tls.Dial("tcp", addr, tlsid.ClientConfig(a.Pin()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var raw strings.Builder
	raw.WriteString("PUT " + target + " HTTP/1.1\r\nHost: x\r\nContent-Length: " + itoa(len(body)) + "\r\n")
	for k, v := range req.Header {
		raw.WriteString(k + ": " + v[0] + "\r\n")
	}
	raw.WriteString("\r\n")
	conn.Write([]byte(raw.String()))
	conn.Write(body[:1024]) // and the rest never comes
	time.Sleep(100 * time.Millisecond)

	stopped := make(chan error, 1)
	go func() { stopped <- a.Shutdown() }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if active, _ := bj.state(); len(active) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("still announced while the server waits for the stuck request")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-stopped:
		t.Fatal("the server did not wait for the stuck request (the test proves nothing)")
	default:
	}
	conn.Close() // the request fails, the shutdown ends
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
}

// M13: the certificate is created at the first launch, kept on the card
// and reused (same pin) at the next launches; RL1 pairings are erased at
// the first M13 launch (the count is logged, never the keys); the pak
// answers over TLS only.
func TestTLSIdentityAndRL1Migration(t *testing.T) {
	sd := testutil.CopySDCard(t)
	roots := nextui.RootsForSD(sd, "tg5040")
	env := func(k string) string { return map[string]string{"PLATFORM": "tg5040"}[k] }
	// An RL1 clients.json, as left by a pak before M13.
	key := auth.EncodeB64U([]byte("0123456789abcdef0123456789abcdef"))
	rl1 := `{"version":1,"clients":[{"clientId":"3b1c8d2a-5e6f-4a7b-8c9d-0e1f2a3b4c5d","clientName":"iPhone","key":"` + key + `","pairedAt":"2026-11-18T17:00:00Z"}]}`
	clients := filepath.Join(roots.Data, "clients.json")
	if err := os.MkdirAll(roots.Data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clients, []byte(rl1), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := slogDefault()
	t.Cleanup(func() { restoreSlog(prev) })
	open := func() *App {
		t.Helper()
		a, err := Open(Options{Roots: roots, Env: env, Version: "1.0.0-test", Port: freePort(t), Pairing: PairingAlways})
		if err != nil {
			t.Fatal(err)
		}
		a.checkEvery = time.Hour
		a.announce = (&fakeBonjour{}).announce
		return a
	}
	a := open()
	pin := a.Pin()
	if pin == "" || len(a.PairedClients()) != 0 {
		t.Fatalf("pin %q, clients %d", pin, len(a.PairedClients()))
	}
	if data, _ := os.ReadFile(clients); strings.Contains(string(data), key) {
		t.Fatal("RL1 key left on the card")
	}
	for _, f := range []string{tlsid.CertFile, tlsid.KeyFile} {
		if _, err := os.Stat(filepath.Join(roots.Data, "tls", f)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	if err := a.Start("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	st, _ := a.Status()
	if !strings.Contains(st.PairingLink, "?v=2&") || !strings.HasSuffix(st.PairingLink, "&k="+pin) {
		t.Fatalf("pairing link %q", st.PairingLink)
	}
	resp, err := pinnedHTTP(a).Get(httpsBase(a) + "/api/v1/hello")
	if err != nil {
		t.Fatal(err)
	}
	var h map[string]any
	json.NewDecoder(resp.Body).Decode(&h)
	resp.Body.Close()
	if h["apiVersion"] != float64(2) || resp.TLS == nil {
		t.Fatalf("hello %v", h)
	}
	if resp, err := http.Get("http://127.0.0.1:" + itoa(a.Port()) + "/api/v1/hello"); err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatal("plain HTTP served")
		}
	}
	logPath := a.LogPath()
	if err := a.Shutdown(); err != nil {
		t.Fatal(err)
	}
	logs, _ := os.ReadFile(logPath)
	if !strings.Contains(string(logs), "RL1 clients erased") || strings.Contains(string(logs), key) {
		t.Fatalf("migration log:\n%s", logs)
	}
	// Restart: same certificate, nothing erased again.
	b := open()
	defer b.Shutdown()
	if b.Pin() != pin {
		t.Fatal("the pin changed across restarts")
	}
}
