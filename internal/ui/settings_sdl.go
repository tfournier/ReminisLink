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
	itemName = iota
	itemPaired
	itemForgetAll
	itemLanguage
	itemAbout
)

// settings shows the settings until B.
func settings(a *app.App) {
	sel := 0
	for {
		lang := a.Language()
		if l, ok := l10n.Lookup(lang); ok {
			lang = l.Name
		}
		labels := []string{
			itemName:      l10n.Tf(l10n.ItemName, "Name", a.DeviceName()),
			itemPaired:    l10n.Tn(l10n.ItemPaired, len(a.PairedClients())),
			itemForgetAll: l10n.T(l10n.ItemForgetAll),
			itemLanguage:  l10n.Tf(l10n.ItemLanguage, "Name", lang),
			itemAbout:     l10n.T(l10n.ItemAbout),
		}
		i, err := showList(l10n.T(l10n.TitleSettings), labels, sel, "")
		if err != nil {
			return
		}
		sel = i
		switch i {
		case itemName:
			rename(a)
		case itemPaired:
			pairedDevices(a)
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

// pairedDevices lists the paired devices; A forgets the selected one.
func pairedDevices(a *app.App) {
	sel := 0
	for {
		clients := a.PairedClients()
		labels := make([]string, len(clients))
		for i, c := range clients {
			labels[i] = PairedItem(c.Name, c.PairedAt)
		}
		i, err := showList(l10n.T(l10n.TitlePaired), labels, sel, l10n.T(l10n.MsgNoPaired))
		if err != nil {
			return
		}
		sel = i
		c := clients[i]
		if confirm(l10n.Tf(l10n.MsgConfirmForget, "Name", c.Name), l10n.T(l10n.BtnForget)) {
			if err := a.ForgetClient(c.ID); err != nil {
				message(l10n.Tf(l10n.MsgSaveError, "Error", err))
			}
		}
	}
}

// showList displays a list and returns the selected index, or
// gaba.ErrCancelled when the user goes back.
func showList(title string, labels []string, selected int, empty string) (int, error) {
	items := make([]gaba.MenuItem, len(labels))
	for i, l := range labels {
		items[i] = gaba.MenuItem{Text: l}
	}
	opts := gaba.DefaultListOptions(title, items)
	if selected < len(items) {
		opts.SelectedIndex = selected
	}
	opts.EmptyMessage = empty
	opts.FooterHelpItems = footer("B", l10n.T(l10n.BtnBack), "A", l10n.T(l10n.BtnSelect))
	r, err := gaba.List(opts)
	if err != nil {
		return -1, err
	}
	if r == nil || len(r.Selected) == 0 {
		return -1, gaba.ErrCancelled
	}
	return r.Selected[0], nil
}
