package ui

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/app"
	"github.com/tfournier/ReminisLink/internal/l10n"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/server"
	"github.com/tfournier/ReminisLink/internal/transfer"
)

func setLang(t *testing.T, code string) {
	t.Helper()
	if err := l10n.Init(code); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l10n.SetLanguage(l10n.DefaultLanguage) })
}

var now = time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)

func status() app.Status {
	return app.Status{
		State: app.StateReady, Name: "Brick de Thomas", IP: "192.168.1.42", Port: 47821, WiFi: true,
		PairingOpen: true, PairingLink: "reminislink://pair?v=2", ExpiresAt: now.Add(9*time.Minute - 10*time.Second),
	}
}

func TestBuildMainFrench(t *testing.T) {
	setLang(t, "fr")
	st := status()
	st.State = app.StateTransfer
	st.Connected = []string{"iPhone de T."}
	st.Activities = []transfer.Activity{{Kind: transfer.ActivityUpload, Name: "Sky Garden Quest DX.gba", Done: 62, Total: 100}}
	st.Notice = &app.Notice{Kind: server.ChangeSaveRestored, Name: "Sky Garden Quest"}
	v := BuildMain(st, now, true, false)
	want := MainView{
		State: "Transfert en cours", Banner: "Sauvegarde restaurée\u00a0: Sky Garden Quest", ShowQR: true,
		QRLink: "reminislink://pair?v=2", Hint: "Scanne avec l'app ReminisLink",
		Items: []Item{
			{"QR code", "Expire dans 9 min"},
			{"Console", "Brick de Thomas · 192.168.1.42"},
			{"Appareils connectés", "iPhone de T."},
		},
		Transfers: []string{"Envoi\u00a0: Sky Garden Quest DX.gba  62\u00a0%"},
	}
	if v.Key() != want.Key() {
		t.Fatalf("view\n got %#v\nwant %#v", v, want)
	}
	hidden := BuildMain(st, now, false, false)
	if hidden.ShowQR || !strings.Contains(hidden.Hint, "masqué") || hidden.Key() == v.Key() {
		t.Fatalf("hidden QR: %#v", hidden)
	}
}

func TestBuildMainEnglish(t *testing.T) {
	setLang(t, "en")
	v := BuildMain(status(), now, true, false)
	if v.State != "Ready" || v.Banner != "" || len(v.Transfers) != 0 ||
		v.Items[0].Value != "Expires in 9 min" || v.Items[2].Value != "None" {
		t.Fatalf("view %#v", v)
	}
	// The view changes with the minute, not with every second.
	if BuildMain(status(), now.Add(time.Second), true, false).Key() != v.Key() {
		t.Fatal("view changed within the minute")
	}
}

func TestPairingExpiryText(t *testing.T) {
	setLang(t, "en")
	st := status()
	st.ExpiresAt = now.Add(20 * time.Second)
	if got := PairingExpiryText(st, now); got != "Expires in 1 min" {
		t.Errorf("last minute: %q", got)
	}
	st.BlockedUntil = now.Add(4*time.Minute + time.Second)
	if got := PairingExpiryText(st, now); !strings.Contains(got, "blocked for 5 min") {
		t.Errorf("blocked: %q", got)
	}
	st.BlockedUntil = now.Add(-time.Second)
	if got := PairingExpiryText(st, now); strings.Contains(got, "blocked") {
		t.Errorf("block over: %q", got)
	}
}

func TestActivityText(t *testing.T) {
	setLang(t, "en")
	cases := map[string]transfer.Activity{
		"Receiving: Moon.gba  50%": {Kind: transfer.ActivityUpload, Name: "Moon.gba", Done: 5, Total: 10},
		"Receiving a file":         {Kind: transfer.ActivityUpload, Total: 0},
		"Patching: Hack  1.5 MB":   {Kind: transfer.JobPatch, Name: "Hack", Done: 3 << 19, Total: -1},
		"Applying a patch  100%":   {Kind: transfer.JobPatch, Done: 20, Total: 10},
		"Patching: Moon (waiting)": {Kind: transfer.JobPatch, Name: "Moon", Queued: true},
		"Applying a patch":         {Kind: transfer.JobPatch, Total: -1},
	}
	for want, a := range cases {
		if got := ActivityText(a); got != want {
			t.Errorf("ActivityText(%+v) = %q, want %q", a, got, want)
		}
	}
	setLang(t, "fr")
	if got := FormatSize(3 << 19); got != "1,5 Mo" {
		t.Errorf("FormatSize fr = %q", got)
	}
}

func TestNoticeText(t *testing.T) {
	setLang(t, "en")
	if got := NoticeText(app.Notice{Kind: server.ChangeTrashEmptied}); got != "Trash emptied" {
		t.Errorf("no name: %q", got)
	}
	if got := NoticeText(app.Notice{Kind: app.NoticePaired, Name: "iPhone"}); got != "Paired with iPhone" {
		t.Errorf("paired: %q", got)
	}
	if got := NoticeText(app.Notice{Kind: "unknown"}); got != "" {
		t.Errorf("unknown: %q", got)
	}
	// Every kind has a text.
	for _, k := range []string{server.ChangeROMAdded, server.ChangeSaveReplaced, server.ChangeSaveRestored,
		server.ChangeStateReplaced, server.ChangeStateRestored, server.ChangeStateDeleted, server.ChangeGameDeleted,
		server.ChangeCheatsUpdated, server.ChangeTrashEmptied, server.ChangeTrashEntryDeleted, app.NoticePakInstalled, app.NoticePakInstallFailed,
		app.NoticePatched, app.NoticePatchFailed, server.ChangeArtSet, server.ChangeArtDeleted} {
		if NoticeText(app.Notice{Kind: k, Name: "X"}) == "" {
			t.Errorf("no text for %q", k)
		}
	}
}

func TestQRPNG(t *testing.T) {
	data, err := QRPNG("reminislink://pair?v=1&id=x")
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil || img.Bounds().Dx() != QRSize {
		t.Fatalf("QR image: %v %v", img.Bounds(), err)
	}
}

func TestPairedItem(t *testing.T) {
	setLang(t, "fr")
	at := time.Date(2026, 11, 18, 12, 0, 0, 0, time.Local)
	if got := PairedItem("iPhone", at); got != "iPhone (appairé le 18/11/2026)" {
		t.Errorf("PairedItem = %q", got)
	}
}

func TestBuildMainNoWiFi(t *testing.T) {
	setLang(t, "fr")
	st := status()
	st.State, st.WiFi, st.IP, st.PairingLink = app.StateNoWiFi, false, "", ""
	v := BuildMain(st, now, true, false)
	if v.State != "Pas de Wi-Fi" || v.ShowQR || !strings.Contains(v.Hint, "reprend dès son retour") ||
		v.Items[0].Value != "Brick de Thomas" || WantPairing(st, true, true) {
		t.Fatalf("offline view %#v", v)
	}
}

func TestBuildMainPairingOnDemand(t *testing.T) {
	setLang(t, "en")
	// No device paired: the main screen shows the QR code (pairing must be
	// open), A hides it (pairing closes). No PIN since M13.
	st := status()
	if !WantPairing(st, true, false) || WantPairing(st, false, false) {
		t.Fatal("unpaired main screen: pairing wanted only while the QR code is shown")
	}
	hidden := st
	hidden.PairingOpen, hidden.PairingLink = false, ""
	v := BuildMain(hidden, now, false, false)
	if v.ShowQR || v.QRLink != "" || !strings.Contains(v.Hint, "pairing is closed") {
		t.Fatalf("hidden QR: %#v", v)
	}
	for _, it := range v.Items {
		if it.Label == "QR code" {
			t.Fatalf("expiry shown while pairing is closed: %#v", v)
		}
	}
	// A device is paired: no QR code on the main screen, even if
	// pairing is still open for an instant; the hint points to the settings.
	paired := st
	paired.Paired = true
	if WantPairing(paired, true, false) {
		t.Fatal("pairing wanted on the main screen with a paired device")
	}
	v = BuildMain(paired, now, true, false)
	if v.ShowQR || v.QRLink != "" || v.Items[0].Label == "QR code" || !strings.Contains(v.Hint, "Pair a New Device") {
		t.Fatalf("paired main screen: %#v", v)
	}
	// Settings > Pair a new device: QR code only, pairing wanted.
	if !WantPairing(paired, true, true) || !WantPairing(paired, false, true) {
		t.Fatal("pairing screen: pairing not wanted")
	}
	v = BuildMain(paired, now, false, true)
	if !v.ShowQR || !v.Pairing || v.QRLink == "" || v.Items[0].Label != "QR code" {
		t.Fatalf("pairing screen: %#v", v)
	}
	// While pairing is not yet open, the screen shows no stale link.
	closed := paired
	closed.PairingOpen = false
	if v := BuildMain(closed, now, false, true); v.ShowQR || v.QRLink != "" {
		t.Fatalf("pairing screen before opening: %#v", v)
	}
}

// M8: names are drawn in NFC, even for a file whose name is in NFD on the
// card (it is not renamed).
func TestNamesShownInNFC(t *testing.T) {
	setLang(t, "en")
	nfd, nfc := "Cafe\u0301 Re\u0302verie", "Café Rêverie"
	if got := NoticeText(app.Notice{Kind: server.ChangeSaveRestored, Name: nfd}); got != "Save restored: "+nfc {
		t.Errorf("notice %q", got)
	}
	if got := ActivityText(transfer.Activity{Kind: transfer.ActivityUpload, Name: nfd + ".gba"}); got != "Receiving: "+nfc+".gba" {
		t.Errorf("activity %q", got)
	}
	st := status()
	st.Name, st.Connected = "Brick de Jose\u0301", []string{"iPhone de Ame\u0301lie"}
	v := BuildMain(st, now, true, false)
	if !strings.Contains(v.Key(), "Brick de José") || !strings.Contains(v.Key(), "iPhone de Amélie") || strings.Contains(v.Key(), "\u0301") {
		t.Errorf("main view %#v", v)
	}
}

func TestBatteryOnTheMainScreen(t *testing.T) {
	setLang(t, "fr")
	st := status()
	if v := BuildMain(st, now, true, false); v.Battery != "" || v.LowBattery {
		t.Fatalf("unknown battery: %#v", v)
	}
	st.Battery = &osadapter.Battery{Percent: 78}
	if v := BuildMain(st, now, true, false); v.Battery != "78\u00a0%" || v.LowBattery {
		t.Fatalf("78 %%: %#v", v)
	}
	st.Battery = &osadapter.Battery{Percent: 12, Charging: true}
	if v := BuildMain(st, now, true, false); v.Battery != "12\u00a0% (en charge)" || v.LowBattery {
		t.Fatalf("charging: %#v", v)
	}
	// Low and not charging: alert, and a banner only during a transfer.
	st.Battery = &osadapter.Battery{Percent: 14}
	v := BuildMain(st, now, true, false)
	if v.Battery != "Faible\u00a0: 14\u00a0%" || !v.LowBattery || v.Banner != "" {
		t.Fatalf("low: %#v", v)
	}
	st.Activities = []transfer.Activity{{Kind: transfer.ActivityUpload, Name: "x.gba", Done: 1, Total: 2}}
	st.Notice = &app.Notice{Kind: server.ChangeROMAdded, Name: "y.gba"}
	v = BuildMain(st, now, true, false)
	if !strings.HasPrefix(v.Banner, "Batterie faible\u00a0: branche la console\n") || !strings.Contains(v.Banner, "y.gba") {
		t.Fatalf("low during a transfer: %#v", v)
	}
	st.Battery.Percent = 15
	if v := BuildMain(st, now, true, false); v.LowBattery || strings.Contains(v.Banner, "faible") {
		t.Fatalf("15 %% is not low: %#v", v)
	}
}

func TestDeviceText(t *testing.T) {
	at := time.Date(2026, 11, 18, 12, 0, 0, 0, time.Local)
	cases := []struct {
		d      app.Device
		en, fr string
	}{
		{app.Device{Name: "Thomas", Platform: "apple", PairedAt: at}, "Thomas · Apple (paired 2026-11-18)", "Thomas · Apple (appairé le 18/11/2026)"},
		{app.Device{Name: "Thomas", Platform: "apple", Platforms: []string{"iphone", "ipad"}, Connected: true},
			"Thomas · iPhone, iPad (connected)", "Thomas · iPhone, iPad (connecté)"},
		{app.Device{Name: "Pixel", Platform: "android", Disconnected: true}, "Pixel · Android (disconnected)", "Pixel · Android (déconnecté)"},
		{app.Device{Name: "Amélie", Platform: "other", Connected: true}, "Amélie · Other (connected)", "Amélie · Autre (connecté)"},
		{app.Device{Name: "Nav", Platform: "web", Platforms: []string{"toaster"}, Connected: true}, "Nav · Web (connected)", "Nav · Web (connecté)"},
	}
	for _, lang := range []string{"en", "fr"} {
		setLang(t, lang)
		for _, c := range cases {
			want := c.en
			if lang == "fr" {
				want = c.fr
			}
			if got := DeviceText(c.d); got != want {
				t.Errorf("[%s] DeviceText(%+v) = %q, want %q", lang, c.d, got, want)
			}
		}
	}
}

// M13: no "write access" line any more (the lease is gone).
func TestBuildMainNoWriterLine(t *testing.T) {
	setLang(t, "fr")
	st := status()
	st.Paired, st.Connected = true, []string{"iPhone A", "iPhone B"}
	v := BuildMain(st, now, false, false)
	if len(v.Items) != 2 || v.Items[1] != (Item{"Appareils connectés", "iPhone A, iPhone B"}) {
		t.Fatalf("items %#v", v.Items)
	}
}
