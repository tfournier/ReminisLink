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
	"golang.org/x/text/unicode/norm"

	"github.com/tfournier/ReminisLink/internal/app"
	"github.com/tfournier/ReminisLink/internal/l10n"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/server"
	"github.com/tfournier/ReminisLink/internal/transfer"
)

// shown is a name as the screen draws it: in NFC (M8), whatever the
// spelling of the file on the card (an NFD name is never renamed, but its
// combining accents are drawn badly by the fonts of the pak).
func shown(name string) string { return norm.NFC.String(name) }

// AppName is the title of the screens (a brand name, not translated).
const AppName = "ReminisLink"

// QRSize is the side of the QR code image, in pixels (the Brick screen is
// 1024x768; the main screen also shows the expiry, the name and the
// transfers below it).
const QRSize = 260

// Item is a label and a value of the main screen.
type Item struct {
	Label string
	Value string
}

// MainView is the text of the main screen (05 §7.1). Two equal views
// draw the same screen: the UI redraws only when the view changes.
type MainView struct {
	State string // Ready, Connected or Transfer in progress, on its own line
	// Battery is the level shown at the top right ("" when unknown);
	// LowBattery: under LowBatteryPercent and not charging.
	Battery    string
	LowBattery bool
	// Pairing: the pairing screen opened from the settings (05 §7.2),
	// not the main screen.
	Pairing   bool
	Banner    string // a change of the SD card, "" when none
	ShowQR    bool   // the QR code is shown (pairing is open)
	QRLink    string // the pairing link the QR code encodes ("" when hidden)
	Hint      string // under the QR code
	Items     []Item
	Transfers []string
}

// Key identifies what is drawn, to compare two views.
func (v MainView) Key() string { return fmt.Sprintf("%#v", v) }

// LowBatteryPercent is the level under which the battery is shown as low
// when it is not charging (05 §7.1).
const LowBatteryPercent = 15

// BatteryText is the battery at the top right, and whether it is low.
func BatteryText(b *osadapter.Battery) (string, bool) {
	if b == nil {
		return "", false
	}
	switch {
	case b.Charging:
		return l10n.Tf(l10n.BatteryCharging, "Percent", b.Percent), false
	case b.Percent < LowBatteryPercent:
		return l10n.Tf(l10n.BatteryLow, "Percent", b.Percent), true
	}
	return l10n.Tf(l10n.BatteryLevel, "Percent", b.Percent), false
}

// WantPairing reports whether the screen shows the QR code,
// which is when pairing must be open (M8, 05 §7.1): on the pairing screen
// of the settings, or on the main screen while no device is paired and
// the QR code is not hidden with A. Never without Wi-Fi (no link).
func WantPairing(st app.Status, showQR, pairingScreen bool) bool {
	return st.WiFi && (pairingScreen || !st.Paired && showQR)
}

// BuildMain returns the main screen for the state of the pak at now
// (pairingScreen false), or the pairing screen of the settings (true).
// The QR code appears only while pairing is open.
func BuildMain(st app.Status, now time.Time, showQR, pairingScreen bool) MainView {
	v := MainView{State: StateText(st.State), Pairing: pairingScreen}
	v.Battery, v.LowBattery = BatteryText(st.Battery)
	if WantPairing(st, showQR, pairingScreen) && st.PairingOpen {
		v.ShowQR, v.QRLink = true, st.PairingLink
	}
	switch {
	case !st.WiFi:
		// The Wi-Fi was lost (sleep, Wi-Fi off): no link to show until
		// it comes back; the pak keeps running.
		v.Hint = l10n.T(l10n.HintNoWiFi)
	case v.ShowQR:
		v.Hint = l10n.T(l10n.ScanHint)
	case pairingScreen || !st.Paired:
		v.Hint = l10n.T(l10n.QRHidden)
	default:
		v.Hint = l10n.T(l10n.HintPairMore)
	}
	if st.Notice != nil {
		v.Banner = NoticeText(*st.Notice)
	}
	if v.LowBattery && len(st.Activities) > 0 {
		// A transfer cut by an empty battery resumes, but tell first.
		low := l10n.T(l10n.BatteryLowBanner)
		if v.Banner != "" {
			low += "\n" + v.Banner
		}
		v.Banner = low
	}
	if v.ShowQR {
		v.Items = append(v.Items, Item{l10n.T(l10n.LabelQR), PairingExpiryText(st, now)})
	}
	console := shown(st.Name)
	if st.IP != "" {
		console = l10n.Tf(l10n.ConsoleValue, "Name", shown(st.Name), "IP", st.IP)
	}
	v.Items = append(v.Items, Item{l10n.T(l10n.LabelConsole), console})
	connected := l10n.T(l10n.NoneConnected)
	if len(st.Connected) > 0 {
		connected = shown(strings.Join(st.Connected, ", "))
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
	case app.StateNoWiFi:
		return l10n.T(l10n.StateNoWiFi)
	}
	return l10n.T(l10n.StateReady)
}

// PairingExpiryText is the minutes before the QR code expires, or the
// remaining block after too many failed attempts. There is no PIN (M13).
func PairingExpiryText(st app.Status, now time.Time) string {
	if st.BlockedUntil.After(now) {
		return l10n.Tn(l10n.PairingBlocked, minutesLeft(st.BlockedUntil.Sub(now)))
	}
	return l10n.Tn(l10n.PairingExpires, minutesLeft(st.ExpiresAt.Sub(now)))
}

// minutesLeft rounds a duration up to whole minutes (at least 1): a QR
// code valid for 30 more seconds "expires in 1 min".
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
	case transfer.JobPakInstall:
		text = named(a.Name, l10n.ActPak, l10n.ActPakFile)
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
	return l10n.Tf(withName, "Name", shown(name))
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
	server.ChangeROMAdded:          l10n.NoticeROMAdded,
	server.ChangeSaveReplaced:      l10n.NoticeSaveReplaced,
	server.ChangeSaveRestored:      l10n.NoticeSaveRestored,
	server.ChangeStateReplaced:     l10n.NoticeStateReplaced,
	server.ChangeStateRestored:     l10n.NoticeStateRestored,
	server.ChangeStateDeleted:      l10n.NoticeStateDeleted,
	server.ChangeGameDeleted:       l10n.NoticeGameDeleted,
	server.ChangeCheatsUpdated:     l10n.NoticeCheatsUpdated,
	server.ChangeTrashEmptied:      l10n.NoticeTrashEmptied,
	server.ChangeTrashEntryDeleted: l10n.NoticeTrashEntryDeleted,
	server.ChangeArtSet:            l10n.NoticeArtSet,
	server.ChangeArtDeleted:        l10n.NoticeArtDeleted,
	server.ChangePakDeleted:        l10n.NoticePakDeleted,
	app.NoticePakInstalled:         l10n.NoticePakInstalled,
	app.NoticePakInstallFailed:     l10n.NoticePakInstallFailed,
	app.NoticeDownloaded:           l10n.NoticeDownloaded,
	app.NoticeDownloadFailed:       l10n.NoticeDownloadFailed,
	app.NoticePatched:              l10n.NoticePatched,
	app.NoticePatchFailed:          l10n.NoticePatchFailed,
}

// NoticeText is the banner of a notice ("" for an unknown kind).
func NoticeText(n app.Notice) string {
	if n.Kind == app.NoticePaired {
		return l10n.Tf(l10n.NoticePaired, "Name", shown(n.Name))
	}
	m, ok := noticeMessages[n.Kind]
	if !ok {
		return ""
	}
	text := l10n.T(m)
	if n.Name == "" {
		return text
	}
	return l10n.Tf(l10n.NoticeWithName, "Text", text, "Name", shown(n.Name))
}

// QRPNG encodes the pairing link as a PNG image of QRSize pixels.
func QRPNG(link string) ([]byte, error) {
	return qrcode.Encode(link, qrcode.Medium, QRSize)
}

// PairedItem is a paired device in the settings list.
func PairedItem(name string, pairedAt time.Time) string {
	return l10n.Tf(l10n.PairedItem, "Name", shown(name), "Date", pairedAt.Local().Format(l10n.T(l10n.DateLayout)))
}

// DeviceText is a device of Settings > Devices (M10): its name, its
// platform (M13) and its state (connected, disconnected), or its pairing
// date when it is idle.
func DeviceText(d app.Device) string {
	var states []string
	if d.Disconnected {
		states = append(states, l10n.T(l10n.DeviceStateDisconnected))
	}
	if d.Connected {
		states = append(states, l10n.T(l10n.DeviceStateConnected))
	}
	platform := PlatformText(d.Platform, d.Platforms)
	if len(states) == 0 {
		return l10n.Tf(l10n.DeviceIdleItem, "Name", shown(d.Name), "Platform", platform,
			"Date", d.PairedAt.Local().Format(l10n.T(l10n.DateLayout)))
	}
	return l10n.Tf(l10n.DeviceItem, "Name", shown(d.Name), "Platform", platform,
		"State", strings.Join(states, l10n.T(l10n.DeviceStateSeparator)))
}

// devicePlatformNames are the X-RL-Platform values as shown (product
// names, not translated).
var devicePlatformNames = map[string]string{
	"iphone": "iPhone", "ipad": "iPad", "mac": "Mac", "android": "Android", "web": "Web",
}

// PlatformText is the platform of a paired client: the devices seen
// recently ("iPhone, iPad") when known, otherwise its kind (Apple,
// Android, Web, Other).
func PlatformText(platform string, seen []string) string {
	var names []string
	for _, p := range seen {
		if n, ok := devicePlatformNames[p]; ok {
			names = append(names, n)
		}
	}
	if len(names) > 0 {
		return strings.Join(names, l10n.T(l10n.DeviceStateSeparator))
	}
	switch platform {
	case "apple":
		return l10n.T(l10n.PlatformApple)
	case "android":
		return l10n.T(l10n.PlatformAndroid)
	case "web":
		return l10n.T(l10n.PlatformWeb)
	}
	return l10n.T(l10n.PlatformOther)
}
