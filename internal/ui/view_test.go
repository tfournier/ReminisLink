package ui

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/app"
	"github.com/tfournier/ReminisLink/internal/l10n"
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
		State: app.StateReady, Name: "Brick de Thomas", IP: "192.168.1.42", Port: 47821,
		PairingLink: "reminislink://pair?v=1", PIN: "482913", ExpiresAt: now.Add(9*time.Minute - 10*time.Second),
	}
}

func TestBuildMainFrench(t *testing.T) {
	setLang(t, "fr")
	st := status()
	st.State = app.StateTransfer
	st.Connected = []string{"iPhone de T."}
	st.Activities = []transfer.Activity{{Kind: transfer.ActivityUpload, Name: "Sky Garden Quest DX.gba", Done: 62, Total: 100}}
	st.Notice = &app.Notice{Kind: server.ChangeSaveRestored, Name: "Sky Garden Quest"}
	v := BuildMain(st, now, true)
	want := MainView{
		State: "Transfert en cours", Banner: "Save restaurée : Sky Garden Quest", ShowQR: true,
		QRLink: "reminislink://pair?v=1", Hint: "Scanne avec l'app ReminisLink",
		Items: []Item{
			{"Code", "482 913  (expire dans 9 min)"},
			{"Console", "Brick de Thomas · 192.168.1.42"},
			{"Appareils connectés", "iPhone de T."},
		},
		Transfers: []string{"Envoi : Sky Garden Quest DX.gba  62 %"},
	}
	if v.Key() != want.Key() {
		t.Fatalf("view\n got %#v\nwant %#v", v, want)
	}
	hidden := BuildMain(st, now, false)
	if hidden.ShowQR || !strings.Contains(hidden.Hint, "masqué") || hidden.Key() == v.Key() {
		t.Fatalf("hidden QR: %#v", hidden)
	}
}

func TestBuildMainEnglish(t *testing.T) {
	setLang(t, "en")
	v := BuildMain(status(), now, true)
	if v.State != "Ready" || v.Banner != "" || len(v.Transfers) != 0 ||
		v.Items[0].Value != "482 913  (expires in 9 min)" || v.Items[2].Value != "None" {
		t.Fatalf("view %#v", v)
	}
	// The view changes with the minute, not with every second.
	if BuildMain(status(), now.Add(time.Second), true).Key() != v.Key() {
		t.Fatal("view changed within the minute")
	}
}

func TestPINText(t *testing.T) {
	setLang(t, "en")
	st := status()
	st.ExpiresAt = now.Add(20 * time.Second)
	if got := PINText(st, now); got != "482 913  (expires in 1 min)" {
		t.Errorf("last minute: %q", got)
	}
	st.BlockedUntil = now.Add(4*time.Minute + time.Second)
	if got := PINText(st, now); !strings.Contains(got, "blocked for 5 min") {
		t.Errorf("blocked: %q", got)
	}
	st.BlockedUntil = now.Add(-time.Second)
	if got := PINText(st, now); strings.Contains(got, "blocked") {
		t.Errorf("block over: %q", got)
	}
}

func TestActivityText(t *testing.T) {
	setLang(t, "en")
	cases := map[string]transfer.Activity{
		"Receiving: Moon.gba  50%":      {Kind: transfer.ActivityUpload, Name: "Moon.gba", Done: 5, Total: 10},
		"Receiving a file":              {Kind: transfer.ActivityUpload, Total: 0},
		"Downloading: Hack.bps  1.5 MB": {Kind: transfer.JobDownload, Name: "Hack.bps", Done: 3 << 19, Total: -1},
		"Downloading a file  100%":      {Kind: transfer.JobDownload, Done: 20, Total: 10},
		"Patching: Moon (waiting)":      {Kind: transfer.JobPatch, Name: "Moon", Queued: true},
		"Applying a patch":              {Kind: transfer.JobPatch, Total: -1},
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
		server.ChangeCheatsUpdated, server.ChangeTrashEmptied, app.NoticeDownloaded, app.NoticeDownloadFailed,
		app.NoticePatched, app.NoticePatchFailed} {
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
