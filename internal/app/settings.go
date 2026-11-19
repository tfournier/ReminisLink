package app

import (
	"errors"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tfournier/ReminisLink/internal/auth"
)

// The settings screen (docs/specs/05-console-pak.md §7.2).

// MaxDeviceNameBytes bounds the device name: it is the Bonjour instance
// name, one DNS label of at most 63 bytes.
const MaxDeviceNameBytes = 63

// ErrEmptyName is returned for a device name with nothing printable.
var ErrEmptyName = errors.New("empty device name")

// CleanDeviceName trims the spaces of a name typed on the keyboard, drops
// the control characters and cuts it to MaxDeviceNameBytes on a character
// boundary.
func CleanDeviceName(name string) (string, error) {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	for len(name) > MaxDeviceNameBytes {
		_, size := utf8.DecodeLastRuneInString(name)
		name = strings.TrimSpace(name[:len(name)-size])
	}
	if name == "" {
		return "", ErrEmptyName
	}
	return name, nil
}

// DeviceName returns the current name of the console.
func (a *App) DeviceName() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.name
}

// SetDeviceName renames the console: config.json (through the single
// writer of the file), the API (/hello, /pair, /device) and the Bonjour
// announcement, withdrawn and registered again. It returns the cleaned
// name.
func (a *App) SetDeviceName(raw string) (string, error) {
	name, err := CleanDeviceName(raw)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	conf := a.conf
	conf.DeviceName = name
	if err := a.config.Save(conf); err != nil {
		a.mu.Unlock()
		return "", err
	}
	a.conf = conf
	a.name = name
	a.mu.Unlock()
	a.log.Info("device renamed", "name", name)
	if a.srv != nil {
		a.srv.SetDeviceName(name)
		if err := a.reannounce(); err != nil {
			a.log.Error("cannot announce the new name", "error", err)
			return name, err
		}
	}
	return name, nil
}

// Language returns the UI language saved in config.json ("" when never
// chosen).
func (a *App) Language() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.conf.Language
}

// SetLanguage saves the UI language in config.json.
func (a *App) SetLanguage(code string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	conf := a.conf
	conf.Language = code
	if err := a.config.Save(conf); err != nil {
		return err
	}
	a.conf = conf
	a.log.Info("language selected", "language", code)
	return nil
}

// PairedClients returns the paired clients, the most recently paired
// first.
func (a *App) PairedClients() []auth.Client {
	list := a.clients.List()
	sort.SliceStable(list, func(i, j int) bool {
		if !list[i].PairedAt.Equal(list[j].PairedAt) {
			return list[i].PairedAt.After(list[j].PairedAt)
		}
		return list[i].Name < list[j].Name
	})
	return list
}

// ForgetClient removes a paired client: its next requests are refused.
func (a *App) ForgetClient(id string) error {
	ok, err := a.clients.Remove(id)
	if err == nil && ok {
		a.log.Info("client forgotten", "client", id)
	}
	a.syncPairing()
	return err
}

// ForgetAllClients removes every paired client and returns how many there
// were.
func (a *App) ForgetAllClients() (int, error) {
	n, err := a.clients.RemoveAll()
	if err == nil {
		a.log.Info("all clients forgotten", "count", n)
	}
	a.syncPairing()
	return n, err
}
