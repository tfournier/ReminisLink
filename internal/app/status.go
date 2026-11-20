package app

import (
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/osadapter"
	"github.com/tfournier/ReminisLink/internal/transfer"
)

// BatteryReadEvery is how often Status reads the battery again.
const BatteryReadEvery = 30 * time.Second

// State is the state shown at the top right of the main screen
// (docs/specs/05-console-pak.md §7.1).
type State int

// States, by priority.
const (
	StateReady     State = iota // nothing going on
	StateConnected              // a signed request in the last 30 s, or an open event stream
	StateTransfer               // a transfer or a job in progress
	StateNoWiFi                 // the Wi-Fi address was lost (sleep, Wi-Fi off)
)

// Status is what the main screen shows. It is read from in-memory state
// only: building it never waits for a request.
type Status struct {
	State State
	Name  string
	IP    string // "" while offline
	Port  int
	WiFi  bool // false: no Wi-Fi address, the pairing link is empty

	// Pairing: the link of the QR code holds the secret; it goes to the
	// screen only, never to the log. PairingLink is empty while pairing is
	// closed (M8: pairing on demand). No PIN since M13.
	PairingOpen  bool
	Paired       bool // at least one device is paired
	PairingLink  string
	ExpiresAt    time.Time
	BlockedUntil time.Time // zero when pairing is not blocked
	Generation   uint64    // changes with the secret

	Battery    *osadapter.Battery // nil when unknown (desktop)
	Connected  []string           // names of the connected clients, most recent first
	Activities []transfer.Activity
	Notice     *Notice // the banner, nil when none
}

// Status returns the state of the pak. Call it after Start.
func (a *App) Status() (Status, error) {
	snap, err := a.pairing.Snapshot()
	if err != nil {
		return Status{}, err
	}
	a.mu.Lock()
	name, ip, wifi := a.name, a.ip, a.wifi
	a.mu.Unlock()
	st := Status{
		Name: name, IP: ip, Port: a.port, WiFi: wifi,
		PairingOpen: snap.Open, Paired: a.HasPairedClients(),
		BlockedUntil: snap.BlockedUntil, Generation: snap.Generation,
		Activities: a.xfer.Activities(),
		Battery:    a.battery(),
	}
	if snap.Open {
		st.ExpiresAt = snap.ExpiresAt
		if wifi {
			st.PairingLink = auth.PairingLink(a.deviceID, name, snap.Secret, ip, a.port, a.tls.Pin)
		}
	}
	if a.srv != nil {
		for _, c := range a.srv.ConnectedClients() {
			st.Connected = append(st.Connected, c.ClientName)
		}
	}
	switch {
	case !wifi:
		st.State = StateNoWiFi
	case len(st.Activities) > 0:
		st.State = StateTransfer
	case len(st.Connected) > 0:
		st.State = StateConnected
	}
	if n, ok := a.notices.current(); ok {
		st.Notice = &n
	}
	return st, nil
}

// battery returns the battery, read again every BatteryReadEvery.
func (a *App) battery() *osadapter.Battery {
	a.batMu.Lock()
	defer a.batMu.Unlock()
	if now := time.Now(); a.batAt.IsZero() || now.Sub(a.batAt) >= BatteryReadEvery {
		a.bat, a.batAt = a.adapter.Battery(), now
	}
	return a.bat
}
