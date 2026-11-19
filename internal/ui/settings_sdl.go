//go:build sdl

package ui

import (
	"errors"

	gaba "github.com/BrandonKowalski/gabagool/v2/pkg/gabagool"

	"github.com/tfournier/ReminisLink/internal/app"
	"github.com/tfournier/ReminisLink/internal/l10n"
)

// Settings items, in display order (05 §7.2).
const (
	itemPairNew = iota
	itemName
	itemPaired // Devices (M10)
	itemForgetAll
	itemLanguage
	itemAbout
)

// settingsResult says how the settings were left.
type settingsResult int

const (
	settingsBack settingsResult = iota // B
	settingsPair                       // Pair a new device: show the pairing screen
)

// settings shows the settings until B or "Pair a new device".
func settings(a *app.App) settingsResult {
	sel := 0
	for {
		lang := a.Language()
		if l, ok := l10n.Lookup(lang); ok {
			lang = l.Name
		}
		labels := []string{
			itemPairNew:   l10n.T(l10n.ItemPairNew),
			itemName:      l10n.Tf(l10n.ItemName, "Name", a.DeviceName()),
			itemPaired:    l10n.Tn(l10n.ItemPaired, len(a.PairedClients())),
			itemForgetAll: l10n.T(l10n.ItemForgetAll),
			itemLanguage:  l10n.Tf(l10n.ItemLanguage, "Name", lang),
			itemAbout:     l10n.T(l10n.ItemAbout),
		}
		i, err := showList(l10n.T(l10n.TitleSettings), labels, sel, "", l10n.T(l10n.BtnSelect))
		if err != nil {
			return settingsBack
		}
		sel = i
		switch i {
		case itemPairNew:
			return settingsPair
		case itemName:
			rename(a)
		case itemPaired:
			devices(a)
		case itemForgetAll:
			if len(a.PairedClients()) > 0 && confirm(l10n.T(l10n.MsgConfirmForgetAll), l10n.T(l10n.BtnForget)) {
				n, err := a.ForgetAllClients()
				if err != nil {
					message(l10n.Tf(l10n.MsgSaveError, "Error", err))
				} else {
					message(l10n.Tn(l10n.MsgForgotAll, n))
				}
			}
		case itemLanguage:
			chooseLanguage(a, false)
		case itemAbout:
			message(l10n.Tf(l10n.MsgAbout, "Version", a.Version(), "Path", a.LogPath()))
		}
	}
}

// rename asks the console name on the gabagool keyboard.
func rename(a *app.App) {
	r, err := gaba.Keyboard(a.DeviceName(), l10n.T(l10n.BtnCancel))
	if err != nil || r == nil {
		return
	}
	if _, err := a.SetDeviceName(r.Text); errors.Is(err, app.ErrEmptyName) {
		message(l10n.T(l10n.MsgNameEmpty))
	} else if err != nil {
		message(l10n.Tf(l10n.MsgSaveError, "Error", err))
	}
}

// devices is Settings > Devices (05 §7.2, M10): the paired devices with
// their state; A opens the actions of the selected one.
func devices(a *app.App) {
	sel := 0
	for {
		list := a.Devices()
		labels := make([]string, len(list))
		for i, d := range list {
			labels[i] = DeviceText(d)
		}
		i, err := showList(l10n.T(l10n.TitlePaired), labels, sel, l10n.T(l10n.MsgNoPaired), l10n.T(l10n.BtnSelect))
		if err != nil {
			return
		}
		sel = i
		deviceActions(a, list[i])
	}
}

// Actions of a device, in display order.
const (
	actionDisconnect = iota
	actionForget
)

// deviceActions offers Disconnect and Forget (with confirmation) for d.
func deviceActions(a *app.App, d app.Device) {
	labels := []string{
		actionDisconnect: l10n.T(l10n.BtnDisconnect),
		actionForget:     l10n.T(l10n.BtnForget),
	}
	i, err := showList(shown(d.Name), labels, 0, "", l10n.T(l10n.BtnSelect))
	if err != nil {
		return
	}
	switch i {
	case actionDisconnect:
		if err := a.DisconnectClient(d.ID); err != nil {
			message(l10n.Tf(l10n.MsgDeviceError, "Error", err))
			return
		}
		message(l10n.Tf(l10n.MsgDisconnected, "Name", shown(d.Name)))
	case actionForget:
		if confirm(l10n.Tf(l10n.MsgConfirmForget, "Name", shown(d.Name)), l10n.T(l10n.BtnForget)) {
			if err := a.ForgetClient(d.ID); err != nil {
				message(l10n.Tf(l10n.MsgSaveError, "Error", err))
			}
		}
	}
}

// showList displays a list and returns the selected index, or
// gaba.ErrCancelled when the user goes back.
func showList(title string, labels []string, selected int, empty, selectLabel string) (int, error) {
	items := make([]gaba.MenuItem, len(labels))
	for i, l := range labels {
		items[i] = gaba.MenuItem{Text: l}
	}
	opts := gaba.DefaultListOptions(title, items)
	if selected < len(items) {
		opts.SelectedIndex = selected
	}
	opts.EmptyMessage = empty
	opts.FooterHelpItems = footer("B", l10n.T(l10n.BtnBack), "A", selectLabel)
	r, err := gaba.List(opts)
	if err != nil {
		return -1, err
	}
	if r == nil || len(r.Selected) == 0 {
		return -1, gaba.ErrCancelled
	}
	return r.Selected[0], nil
}
