// Package ui is the gabagool interface of the pak
// (docs/specs/05-console-pak.md §7): the main screen, the screens without
// Wi-Fi and during the maintenance, the settings and the exit
// confirmation.
//
// The screens (files with the "sdl" build tag) need SDL and cgo; this file
// and the others without the tag build the texts of the screens from the
// state of the pak, in pure Go, so that they are tested with
// CGO_ENABLED=0. Every visible text goes through internal/l10n.
package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"

	"github.com/tfournier/ReminisLink/internal/app"
	"github.com/tfournier/ReminisLink/internal/l10n"
	"github.com/tfournier/ReminisLink/internal/server"
	"github.com/tfournier/ReminisLink/internal/transfer"
)

// AppName is the title of the screens (a brand name, not translated).
const AppName = "ReminisLink"

// QRSize is the side of the QR code image, in pixels (the Brick screen is
// 1024x768; the main screen also shows the PIN, the name and the
// transfers below it).
const QRSize = 320

// Item is a label and a value of the main screen.
type Item struct {
	Label string
	Value string
}

// MainView is the text of the main screen (05 §7.1). Two equal views
// draw the same screen: the UI redraws only when the view changes.
type MainView struct {
	State     string // top right: Ready, Connected or Transfer in progress
	Banner    string // a change of the SD card, "" when none
	ShowQR    bool
	QRLink    string // the pairing link the QR code encodes
	Hint      string // under the QR code
	Items     []Item
	Transfers []string
}

// Key identifies what is drawn, to compare two views.
func (v MainView) Key() string { return fmt.Sprintf("%#v", v) }

// BuildMain returns the main screen for the state of the pak at now.
func BuildMain(st app.Status, now time.Time, showQR bool) MainView {
	v := MainView{State: StateText(st.State), ShowQR: showQR, QRLink: st.PairingLink}
	if showQR {
		v.Hint = l10n.T(l10n.ScanHint)
	} else {
		v.Hint = l10n.T(l10n.QRHidden)
	}
	if st.Notice != nil {
		v.Banner = NoticeText(*st.Notice)
	}
	v.Items = append(v.Items, Item{l10n.T(l10n.LabelCode), PINText(st, now)})
	v.Items = append(v.Items, Item{l10n.T(l10n.LabelConsole), l10n.Tf(l10n.ConsoleValue, "Name", st.Name, "IP", st.IP)})
	connected := l10n.T(l10n.NoneConnected)
	if len(st.Connected) > 0 {
		connected = strings.Join(st.Connected, ", ")
	}
	v.Items = append(v.Items, Item{l10n.T(l10n.LabelConnected), connected})
	for _, a := range st.Activities {
		v.Transfers = append(v.Transfers, ActivityText(a))
	}
	return v
}

// StateText is the label of a state.
func StateText(s app.State) string {
	switch s {
	case app.StateConnected:
		return l10n.T(l10n.StateConnected)
	case app.StateTransfer:
		return l10n.T(l10n.StateTransfer)
	}
	return l10n.T(l10n.StateReady)
}

// PINText is the PIN ("482 913") and the minutes before it expires, or
// the remaining block after too many failed attempts.
func PINText(st app.Status, now time.Time) string {
	if st.BlockedUntil.After(now) {
		return l10n.Tn(l10n.PINBlocked, minutesLeft(st.BlockedUntil.Sub(now)))
	}
	pin := st.PIN
	if len(pin) == 6 {
		pin = pin[:3] + " " + pin[3:]
	}
	return l10n.Tn(l10n.PINExpires, minutesLeft(st.ExpiresAt.Sub(now)), "PIN", pin)
}

// minutesLeft rounds a duration up to whole minutes (at least 1): a PIN
// valid for 30 more seconds "expires in 1 min".
func minutesLeft(d time.Duration) int {
	m := int((d + time.Minute - time.Nanosecond) / time.Minute)
	return max(m, 1)
}

// ActivityText describes a transfer or a job with its progress.
func ActivityText(a transfer.Activity) string {
	var text string
	switch a.Kind {
	case transfer.JobDownload:
		text = named(a.Name, l10n.ActDownload, l10n.ActDownloadFile)
	case transfer.JobPatch:
		text = named(a.Name, l10n.ActPatch, l10n.ActPatchFile)
	default:
		text = named(a.Name, l10n.ActUpload, l10n.ActUploadFile)
	}
	if a.Queued {
		return l10n.Tf(l10n.ActQueued, "Text", text)
	}
	if a.Total > 0 {
		pct := int(a.Done * 100 / a.Total)
		return l10n.Tf(l10n.ActProgress, "Text", text, "Percent", min(max(pct, 0), 100))
	}
	if a.Done > 0 {
		return l10n.Tf(l10n.ActBytes, "Text", text, "Size", FormatSize(a.Done))
	}
	return text
}

func named(name string, withName, without *l10n.Message) string {
	if name == "" {
		return l10n.T(without)
	}
	return l10n.Tf(withName, "Name", name)
}

// FormatSize writes a size in megabytes with one decimal, with the decimal
// separator of the language ("12.3 MB", "12,3 Mo").
func FormatSize(bytes int64) string {
	tenths := (bytes*10 + (1<<20)/2) / (1 << 20)
	value := strconv.FormatInt(tenths/10, 10) + l10n.T(l10n.DecimalSeparator) + strconv.FormatInt(tenths%10, 10)
	return l10n.Tf(l10n.SizeMB, "Value", value)
}

// noticeMessages are the banner texts of the notice kinds.
var noticeMessages = map[string]*l10n.Message{
	server.ChangeROMAdded:      l10n.NoticeROMAdded,
	server.ChangeSaveReplaced:  l10n.NoticeSaveReplaced,
	server.ChangeSaveRestored:  l10n.NoticeSaveRestored,
	server.ChangeStateReplaced: l10n.NoticeStateReplaced,
	server.ChangeStateRestored: l10n.NoticeStateRestored,
	server.ChangeStateDeleted:  l10n.NoticeStateDeleted,
	server.ChangeGameDeleted:   l10n.NoticeGameDeleted,
	server.ChangeCheatsUpdated: l10n.NoticeCheatsUpdated,
	server.ChangeTrashEmptied:  l10n.NoticeTrashEmptied,
	app.NoticeDownloaded:       l10n.NoticeDownloaded,
	app.NoticeDownloadFailed:   l10n.NoticeDownloadFailed,
	app.NoticePatched:          l10n.NoticePatched,
	app.NoticePatchFailed:      l10n.NoticePatchFailed,
}

// NoticeText is the banner of a notice ("" for an unknown kind).
func NoticeText(n app.Notice) string {
	if n.Kind == app.NoticePaired {
		return l10n.Tf(l10n.NoticePaired, "Name", n.Name)
	}
	m, ok := noticeMessages[n.Kind]
	if !ok {
		return ""
	}
	text := l10n.T(m)
	if n.Name == "" {
		return text
	}
	return l10n.Tf(l10n.NoticeWithName, "Text", text, "Name", n.Name)
}

// QRPNG encodes the pairing link as a PNG image of QRSize pixels.
func QRPNG(link string) ([]byte, error) {
	return qrcode.Encode(link, qrcode.Medium, QRSize)
}

// PairedItem is a paired device in the settings list.
func PairedItem(name string, pairedAt time.Time) string {
	return l10n.Tf(l10n.PairedItem, "Name", name, "Date", pairedAt.Local().Format(l10n.T(l10n.DateLayout)))
}
