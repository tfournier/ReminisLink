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

	// The QR code image holds the pairing secret: it stays in memory (see
	// qrimage.go) and is dropped at exit, the signal path included.
	screen := newMainScreen(a)
	defer screen.closeQR()

	// SIGTERM or SIGINT (NextUI's poweroff, or Ctrl-C in desktop mode),
	// from now on, the maintenance included: app.ExitOnSignal shuts the pak
	// down (data first, see app.Shutdown), drops the QR code image and
	// exits without waiting for the screen in front. The handler stays
	// registered until the process ends: a second signal during "Closing..."
	// does not kill the pak in the middle.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	stopSignals := make(chan struct{})
	go a.ExitOnSignal(sigs, stopSignals, screen.closeQR, os.Exit)

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
		close(stopSignals)
		return nil
	}

	if _, err := gaba.ProcessMessage(l10n.T(l10n.MsgPreparing), gaba.ProcessMessageOptions{ShowThemeBackground: true},
		func() (struct{}, error) { return struct{}{}, a.Maintain() }); err != nil {
		return fail(a, err)
	}
	if err := a.Start(ip); err != nil {
		return fail(a, err)
	}

	screen.loop()
	a.SetPairingVisible(false)

	_, err := gaba.ProcessMessage(l10n.T(l10n.MsgClosing), gaba.ProcessMessageOptions{ShowThemeBackground: true},
		func() (struct{}, error) { return struct{}{}, a.Quit() })
	close(stopSignals)
	return err
}

// fail shuts the pak down after a start error and shows it. ErrStopped
// means a signal is being handled: app.ExitOnSignal ends the process.
func fail(a *app.App, err error) error {
	if errors.Is(err, app.ErrStopped) {
		select {} // the signal path calls os.Exit
	}
	a.Logger().Error("fatal", "error", err)
	a.Shutdown()
	message(l10n.Tf(l10n.MsgStartError, "Error", err))
	return app.LoggedError{Err: err}
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

// mainScreen is the main screen (05 §7.1), and the pairing screen of the
// settings (05 §7.2), which is the same screen in pairing mode. gabagool
// screens are modal loops without a refresh hook: the screen is a
// DetailScreen rebuilt each time its view changes. A goroutine compares
// the view every refreshEvery and, when it changed, ends the DetailScreen
// with a pushed SDL quit event; the loop then rebuilds it. The goroutine
// reads the state of the pak from memory (app.Status), never waiting for
// a request.
//
// Pairing is on demand (M8): it is open only while the screen shows the
// QR code and the PIN, that is on the main screen while no device is
// paired (and the QR code is not hidden with A), or in pairing mode.
type mainScreen struct {
	a       *app.App
	showQR  bool
	pairing bool // pairing mode (Settings > Pair a new device)
	// pairedAtStart is the number of paired devices when the pairing mode
	// opened: a new one ends it.
	pairedAtStart int

	noQRLogged bool // the missing memory file was logged

	qrMu   sync.Mutex // guards qr (closeQR runs on the signal path)
	qr     *qrImage
	qrLink string // link encoded in qr

	mu        sync.Mutex // guards visible and refreshed with the pushes
	visible   bool       // a DetailScreen of this screen is running
	refreshed bool       // its end was asked by the refresher
}

const refreshEvery = 500 * time.Millisecond

// newMainScreen returns the main screen.
func newMainScreen(a *app.App) *mainScreen {
	return &mainScreen{a: a, showQR: true}
}

// view builds the current view, opening or closing pairing to match what
// it shows.
func (m *mainScreen) view() (MainView, error) {
	st, err := m.a.Status()
	if err != nil {
		return MainView{}, err
	}
	if want := WantPairing(st, m.showQR, m.pairing); want != st.PairingOpen {
		if err := m.a.SetPairingVisible(want); err != nil {
			return MainView{}, err
		}
		if st, err = m.a.Status(); err != nil {
			return MainView{}, err
		}
	}
	return BuildMain(st, time.Now(), m.showQR, m.pairing), nil
}

// qrPath returns a path to the QR image of link, drawn again when the
// link changed (a new secret).
func (m *mainScreen) qrPath(link string) (string, error) {
	m.qrMu.Lock()
	defer m.qrMu.Unlock()
	if m.qr == nil || link != m.qrLink {
		if m.qr != nil {
			m.qr.close()
			m.qr = nil
		}
		data, err := QRPNG(link)
		if err != nil {
			return "", err
		}
		m.qr, m.qrLink = &qrImage{data: data}, link
	}
	return m.qr.path()
}

// releaseQR is called once the screen loaded the image.
func (m *mainScreen) releaseQR() {
	m.qrMu.Lock()
	defer m.qrMu.Unlock()
	if m.qr != nil {
		m.qr.release()
	}
}

// closeQR drops the QR image (exit, signal path included).
func (m *mainScreen) closeQR() {
	m.qrMu.Lock()
	defer m.qrMu.Unlock()
	if m.qr != nil {
		m.qr.close()
		m.qr, m.qrLink = nil, ""
	}
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
		qrPath := ""
		if v.ShowQR {
			if qrPath, err = m.qrPath(v.QRLink); errors.Is(err, errNoMemoryFile) {
				if !m.noQRLogged {
					m.noQRLogged = true
					m.a.Logger().Info("no memory file for the QR code here: the PIN only is shown")
				}
			} else if err != nil {
				m.a.Logger().Error("cannot draw the QR code", "error", err)
			}
		} else {
			m.closeQR()
		}
		action := m.show(v, qrPath)
		select {
		case <-m.a.Stopped():
			return // the server stopped on its own: nothing left to show
		default:
		}
		switch action {
		case actionRefresh:
			if m.pairing && len(m.a.PairedClients()) > m.pairedAtStart {
				m.pairing = false // paired: back to the main screen, with the banner
			}
		case actionToggleQR:
			if !m.pairing {
				m.showQR = !m.showQR
			}
		case actionSettings:
			m.settings()
		case actionQuit:
			if m.pairing {
				// B leaves the pairing screen for the settings.
				m.pairing = false
				m.settings()
				continue
			}
			if !m.a.Busy() || confirm(l10n.T(l10n.MsgConfirmQuit), l10n.T(l10n.BtnQuit)) {
				return
			}
		}
	}
}

// settings shows the settings (pairing closed meanwhile: no QR code is
// shown); "Pair a new device" switches the screen to pairing mode.
func (m *mainScreen) settings() {
	if err := m.a.SetPairingVisible(false); err != nil {
		m.a.Logger().Error("pairing", "error", err)
	}
	m.closeQR()
	if settings(m.a) == settingsPair {
		m.pairing = true
		m.pairedAtStart = len(m.a.PairedClients())
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
func (m *mainScreen) show(v MainView, qrPath string) mainAction {
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
		released := false
		for {
			select {
			case <-stop:
				return
			case <-t.C:
			}
			if !released {
				// The DetailScreen loads its images before its first frame.
				m.releaseQR()
				released = true
			}
			next, err := m.view()
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

	res, err := gaba.DetailScreen(AppName, m.options(v, qrPath), m.footer(v))

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

// options lays out the view in a DetailScreen (05 §7.1): the state is the
// first line, on the left, under the title; the battery is in the status
// bar at the top right. The status bar of gabagool is short with the
// battery only, so it never overlaps the centered title (the long state
// "Transfer in progress" did). gabagool draws it in the theme colors: a
// low battery is told by its text ("Low: 12%"), not by a color.
func (m *mainScreen) options(v MainView, qrPath string) gaba.DetailScreenOptions {
	opts := gaba.DefaultInfoScreenOptions()
	opts.ShowThemeBackground = true
	opts.ShowScrollbar = true
	opts.ConfirmButton = constants.VirtualButtonA
	opts.ActionButton = constants.VirtualButtonY
	opts.AllowAction = !v.Pairing
	opts.MaxImageHeight = QRSize
	opts.MaxImageWidth = QRSize
	if v.Battery != "" {
		opts.StatusBar = gaba.StatusBarOptions{Enabled: true, Icons: []gaba.StatusBarIcon{{Text: v.Battery}}}
	}
	sections := []gaba.Section{gaba.NewInfoSection("", []gaba.MetadataItem{{Label: l10n.T(l10n.LabelState), Value: v.State}})}
	if v.Banner != "" {
		sections = append(sections, gaba.NewDescriptionSection("", v.Banner))
	}
	// The transfers come before the QR code: whoever sends files is
	// already paired, and the screen is too short for everything.
	if len(v.Transfers) > 0 {
		sections = append(sections, gaba.NewDescriptionSection(l10n.T(l10n.TitleTransfers), joinLines(v.Transfers)))
	}
	title := v.Hint
	if v.Pairing {
		title = l10n.T(l10n.ItemPairNew)
	}
	if v.ShowQR && qrPath != "" {
		// The hint is the title of the QR code section.
		if v.Pairing {
			sections = append(sections, gaba.NewDescriptionSection(title, v.Hint))
			title = ""
		}
		sections = append(sections, gaba.NewImageSection(title, qrPath, QRSize, QRSize, constants.TextAlignCenter))
	} else if v.ShowQR {
		// No QR image (no memory file here): the PIN below pairs.
		if !v.Pairing {
			title = ""
		}
		sections = append(sections, gaba.NewDescriptionSection(title, l10n.T(l10n.QRUnavailable)))
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

func (m *mainScreen) footer(v MainView) []gaba.FooterHelpItem {
	if v.Pairing {
		return footer("B", l10n.T(l10n.BtnBack))
	}
	items := footer("B", l10n.T(l10n.BtnQuit), "Y", l10n.T(l10n.BtnSettings))
	if v.ShowQR {
		items = append(items, footer("A", l10n.T(l10n.BtnHideQR))...)
	} else if !m.showQR && m.unpaired() {
		items = append(items, footer("A", l10n.T(l10n.BtnShowQR))...)
	}
	return items
}

// unpaired reports whether no device is paired (A shows the QR code).
func (m *mainScreen) unpaired() bool { return !m.a.HasPairedClients() }

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
