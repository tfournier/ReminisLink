//go:build sdl

package ui

import (
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	gaba "github.com/BrandonKowalski/gabagool/v2/pkg/gabagool"
	"github.com/BrandonKowalski/gabagool/v2/pkg/gabagool/constants"

	"github.com/tfournier/ReminisLink/internal/app"
	"github.com/tfournier/ReminisLink/internal/l10n"
)

// SDL wants the thread that initialized it (the main thread on macOS):
// main runs on the main thread, keep it there.
func init() { runtime.LockOSThread() }

// UILogFileName is the log file of gabagool (errors of the UI framework),
// next to ReminisLink.txt.
const UILogFileName = "ReminisLink.ui.txt"

// Options configure the UI.
type Options struct {
	App     app.Options
	Desktop bool // a window on the Mac (keyboard), not the NextUI theme
}

// Run shows the UI until the user quits, running the pak behind it
// (docs/specs/05-console-pak.md §2): Wi-Fi check, maintenance under
// "Preparing...", server and Bonjour in goroutines, main screen.
func Run(o Options) error {
	if o.Desktop && os.Getenv(constants.EnvironmentEnvVar) == "" {
		// gabagool's development mode: a 1024x768 window and no power
		// button handling.
		os.Setenv(constants.EnvironmentEnvVar, constants.Development)
	}
	o.App.Mode = "ui"
	if o.Desktop {
		o.App.Mode = "desktop"
	}
	// The log and config.json first: the UI then knows the language.
	a, openErr := app.Open(o.App)

	gaba.Init(gaba.Options{
		WindowTitle:    AppName,
		ShowBackground: true,
		IsNextUI:       !o.Desktop,
		LogPath:        filepath.Join(o.App.Roots.Logs, UILogFileName),
	})
	defer gaba.Close()
	if err := l10n.Init(l10n.DefaultLanguage); err != nil {
		slog.Error("cannot load translations", "error", err)
	}
	if openErr != nil {
		message(l10n.Tf(l10n.MsgStartError, "Error", openErr))
		return openErr
	}
	defer a.Close()

	if l10n.IsSupported(a.Language()) {
		l10n.SetLanguage(a.Language())
	} else {
		chooseLanguage(a, true)
	}

	ip, ok := a.WiFiIPv4()
	if !ok {
		a.Logger().Warn("no Wi-Fi address, the pak does not start")
		gaba.ConfirmationMessage(l10n.T(l10n.MsgNoWiFi), footer("B", l10n.T(l10n.BtnQuit)), gaba.MessageOptions{})
		a.Shutdown()
		return nil
	}

	fail := func(err error) error {
		a.Logger().Error("fatal", "error", err)
		a.Shutdown()
		message(l10n.Tf(l10n.MsgStartError, "Error", err))
		return app.LoggedError{Err: err}
	}
	if _, err := gaba.ProcessMessage(l10n.T(l10n.MsgPreparing), gaba.ProcessMessageOptions{ShowThemeBackground: true},
		func() (struct{}, error) { return struct{}{}, a.Maintain() }); err != nil {
		return fail(err)
	}
	if err := a.Start(ip); err != nil {
		return fail(err)
	}

	// SIGTERM or SIGINT (NextUI, or Ctrl-C in desktop mode): clean
	// shutdown, then exit without waiting for the screen in front.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		if _, ok := <-sigs; ok {
			signal.Stop(sigs)
			a.Logger().Info("signal received, closing")
			a.Shutdown()
			a.Close()
			os.Exit(0)
		}
	}()

	screen := newMainScreen(a)
	defer screen.cleanup()
	screen.loop()

	signal.Stop(sigs)
	_, err := gaba.ProcessMessage(l10n.T(l10n.MsgClosing), gaba.ProcessMessageOptions{ShowThemeBackground: true},
		func() (struct{}, error) { return struct{}{}, a.Shutdown() })
	return err
}

// footer builds footer help items from button/text pairs.
func footer(items ...string) []gaba.FooterHelpItem {
	var out []gaba.FooterHelpItem
	for i := 0; i+1 < len(items); i += 2 {
		out = append(out, gaba.FooterHelpItem{ButtonName: items[i], HelpText: items[i+1]})
	}
	return out
}

// message shows a text until A or B.
func message(text string) {
	gaba.ConfirmationMessage(text, footer("A", l10n.T(l10n.BtnOK)), gaba.MessageOptions{})
}

// confirm asks a yes/no question; true when confirmed with A.
func confirm(text, okLabel string) bool {
	r, err := gaba.ConfirmationMessage(text, footer("B", l10n.T(l10n.BtnCancel), "A", okLabel), gaba.MessageOptions{})
	return err == nil && r != nil && r.Confirmed
}

// chooseLanguage shows the language picker, applies and saves the choice
// (as in Cheat Manager: on first launch it cannot be skipped).
func chooseLanguage(a *app.App, firstLaunch bool) {
	options := make([]gaba.SelectionOption, len(l10n.Languages))
	initial := 0
	for i, l := range l10n.Languages {
		options[i] = gaba.SelectionOption{DisplayName: l.Name, Description: l.Welcome, Value: l.Code}
		if l.Code == a.Language() {
			initial = i
		}
	}
	title := l10n.T(l10n.LanguagePrompt)
	help := footer(constants.LeftRight, l10n.T(l10n.BtnSelect), "A", l10n.T(l10n.BtnConfirm))
	if firstLaunch {
		// Nothing chosen yet: the welcome texts in their own language.
		title = l10n.Languages[0].Welcome
	} else {
		help = append(footer("B", l10n.T(l10n.BtnCancel)), help...)
	}
	r, err := gaba.SelectionMessage(title, options, help, gaba.SelectionMessageSettings{
		DisableBackButton: firstLaunch,
		InitialSelection:  initial,
	})
	if err != nil || r == nil {
		return
	}
	code, _ := r.SelectedValue.(string)
	l10n.SetLanguage(code)
	if err := a.SetLanguage(code); err != nil {
		a.Logger().Error("cannot save the language", "error", err)
		message(l10n.Tf(l10n.MsgSaveError, "Error", err))
	}
}

// mainScreen is the main screen (05 §7.1). gabagool screens are modal
// loops without a refresh hook: the screen is a DetailScreen rebuilt each
// time its view changes. A goroutine compares the view every
// refreshEvery and, when it changed, ends the DetailScreen with a pushed
// SDL quit event; the loop then rebuilds it. The goroutine reads the
// state of the pak from memory (app.Status), never waiting for a request.
type mainScreen struct {
	a      *app.App
	showQR bool
	qrDir  string // private temporary folder of the QR image
	qrPath string
	qrLink string // link encoded in qrPath

	mu        sync.Mutex // guards visible and refreshed with the pushes
	visible   bool       // a DetailScreen of this screen is running
	refreshed bool       // its end was asked by the refresher
}

const refreshEvery = 500 * time.Millisecond

func newMainScreen(a *app.App) *mainScreen { return &mainScreen{a: a, showQR: true} }

// cleanup deletes the QR image (it holds the pairing secret).
func (m *mainScreen) cleanup() {
	if m.qrDir != "" {
		os.RemoveAll(m.qrDir)
	}
}

// view builds the current view.
func (m *mainScreen) view() (MainView, error) {
	st, err := m.a.Status()
	if err != nil {
		return MainView{}, err
	}
	return BuildMain(st, time.Now(), m.showQR), nil
}

// writeQR writes the QR image of link. gabagool loads images from files:
// the file goes to a private folder of the temporary directory (/tmp, in
// memory on the console), never to the SD card, and is deleted when the
// secret changes and at exit.
// TODO(M0): check on the console that /tmp is a tmpfs (OpenWrt default).
func (m *mainScreen) writeQR(link string) error {
	if link == m.qrLink && m.qrPath != "" {
		return nil
	}
	if m.qrDir == "" {
		dir, err := os.MkdirTemp("", "reminislink-")
		if err != nil {
			return err
		}
		m.qrDir = dir
	}
	data, err := QRPNG(link)
	if err != nil {
		return err
	}
	if m.qrPath != "" {
		os.Remove(m.qrPath)
	}
	// A new name each time: the image cache of SDL never shows an old one.
	f, err := os.CreateTemp(m.qrDir, "qr-*.png")
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(f.Name())
		return werr
	}
	m.qrPath, m.qrLink = f.Name(), link
	return nil
}

// loop shows the main screen until the user quits.
func (m *mainScreen) loop() {
	for {
		v, err := m.view()
		if err != nil {
			m.a.Logger().Error("main screen", "error", err)
			message(l10n.Tf(l10n.MsgStartError, "Error", err))
			return
		}
		if v.ShowQR {
			if err := m.writeQR(v.QRLink); err != nil {
				m.a.Logger().Error("cannot draw the QR code", "error", err)
				v.ShowQR = false
			}
		}
		action := m.show(v)
		select {
		case <-m.a.Stopped():
			return // the server stopped on its own: nothing left to show
		default:
		}
		switch action {
		case actionRefresh:
		case actionToggleQR:
			m.showQR = !m.showQR
		case actionSettings:
			settings(m.a)
		case actionQuit:
			if !m.a.Busy() || confirm(l10n.T(l10n.MsgConfirmQuit), l10n.T(l10n.BtnQuit)) {
				return
			}
		}
	}
}

type mainAction int

const (
	actionRefresh mainAction = iota
	actionToggleQR
	actionSettings
	actionQuit
)

// show runs one DetailScreen of the view until a button or a refresh.
func (m *mainScreen) show(v MainView) mainAction {
	key := v.Key()
	stop := make(chan struct{})
	done := make(chan struct{})
	m.mu.Lock()
	m.visible, m.refreshed = true, false
	m.mu.Unlock()
	go func() {
		defer close(done)
		t := time.NewTicker(refreshEvery)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
			}
			next, err := m.view()
			if err == nil {
				next.ShowQR = v.ShowQR // a failed QR image does not loop
				next.QRLink = qrKey(next, v)
			}
			if err == nil && next.Key() == key {
				select {
				case <-m.a.Stopped():
				default:
					continue
				}
			}
			m.mu.Lock()
			if m.visible && !m.refreshed {
				m.refreshed = true
				pushQuit()
			}
			m.mu.Unlock()
			return
		}
	}()

	res, err := gaba.DetailScreen(AppName, m.options(v), m.footer())

	m.mu.Lock()
	m.visible = false
	refreshed := m.refreshed
	flushQuit() // a push that raced with a button
	m.mu.Unlock()
	close(stop)
	<-done

	switch {
	case refreshed:
		return actionRefresh
	case err != nil || res == nil:
		// B, or the window closed (desktop).
		if err != nil && !errors.Is(err, gaba.ErrCancelled) {
			m.a.Logger().Error("main screen", "error", err)
		}
		return actionQuit
	case res.Action == gaba.DetailActionTriggered:
		return actionSettings
	case res.Action == gaba.DetailActionConfirmed:
		return actionToggleQR
	}
	return actionRefresh
}

// qrKey keeps the link of the shown view when the QR code is hidden, so
// that a new secret does not redraw a screen that does not show it.
func qrKey(next, shown MainView) string {
	if !shown.ShowQR {
		return shown.QRLink
	}
	return next.QRLink
}

// options lays out the view in a DetailScreen.
func (m *mainScreen) options(v MainView) gaba.DetailScreenOptions {
	opts := gaba.DefaultInfoScreenOptions()
	opts.ShowThemeBackground = true
	opts.ShowScrollbar = true
	opts.ConfirmButton = constants.VirtualButtonA
	opts.ActionButton = constants.VirtualButtonY
	opts.AllowAction = true
	opts.MaxImageHeight = QRSize
	opts.MaxImageWidth = QRSize
	opts.StatusBar = gaba.StatusBarOptions{Enabled: true, Icons: []gaba.StatusBarIcon{{Text: v.State}}}
	var sections []gaba.Section
	if v.Banner != "" {
		sections = append(sections, gaba.NewDescriptionSection("", v.Banner))
	}
	// The transfers come before the QR code: whoever sends files is
	// already paired, and the screen is too short for everything.
	if len(v.Transfers) > 0 {
		sections = append(sections, gaba.NewDescriptionSection(l10n.T(l10n.TitleTransfers), joinLines(v.Transfers)))
	}
	if v.ShowQR && m.qrPath != "" {
		// The hint is the title of the QR code section.
		sections = append(sections, gaba.NewImageSection(v.Hint, m.qrPath, QRSize, QRSize, constants.TextAlignCenter))
	} else {
		sections = append(sections, gaba.NewDescriptionSection("", v.Hint))
	}
	meta := make([]gaba.MetadataItem, len(v.Items))
	for i, it := range v.Items {
		meta[i] = gaba.MetadataItem{Label: it.Label, Value: it.Value}
	}
	sections = append(sections, gaba.NewInfoSection("", meta))
	opts.Sections = sections
	return opts
}

func (m *mainScreen) footer() []gaba.FooterHelpItem {
	qr := l10n.T(l10n.BtnHideQR)
	if !m.showQR {
		qr = l10n.T(l10n.BtnShowQR)
	}
	return footer("B", l10n.T(l10n.BtnQuit), "Y", l10n.T(l10n.BtnSettings), "A", qr)
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}
