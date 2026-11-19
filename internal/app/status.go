package app

import (
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
	"github.com/tfournier/ReminisLink/internal/transfer"
)

// State is the state shown at the top right of the main screen
// (docs/specs/05-console-pak.md §7.1).
type State int

// States, by priority.
const (
	StateReady     State = iota // nothing going on
	StateConnected              // a signed request in the last 30 s
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
	// screen only, never to the log.
	PairingLink  string
	PIN          string
	ExpiresAt    time.Time
	BlockedUntil time.Time // zero when pairing is not blocked
	Generation   uint64    // changes with the secret

	Connected  []string // names of the connected clients, most recent first
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
		PIN: snap.PIN, ExpiresAt: snap.ExpiresAt, BlockedUntil: snap.BlockedUntil, Generation: snap.Generation,
		Activities: a.xfer.Activities(),
	}
	if wifi {
		st.PairingLink = auth.PairingURL(a.deviceID, name, snap.Secret, ip, a.port)
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
