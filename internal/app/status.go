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
)

// Status is what the main screen shows. It is read from in-memory state
// only: building it never waits for a request.
type Status struct {
	State State
	Name  string
	IP    string
	Port  int

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
	name := a.DeviceName()
	st := Status{
		Name: name, IP: a.ip, Port: a.port,
		PairingLink: auth.PairingURL(a.conf.DeviceID, name, snap.Secret, a.ip, a.port),
		PIN:         snap.PIN, ExpiresAt: snap.ExpiresAt, BlockedUntil: snap.BlockedUntil, Generation: snap.Generation,
		Activities: a.xfer.Activities(),
	}
	if a.srv != nil {
		for _, c := range a.srv.ConnectedClients() {
			st.Connected = append(st.Connected, c.ClientName)
		}
	}
	switch {
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
