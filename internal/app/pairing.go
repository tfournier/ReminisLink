package app

// Pairing on demand (docs/specs/05-console-pak.md §7.1, M8): pairing is
// only open while the console shows the QR code (no PIN since M13). The UI opens
// it while its pairing screen is visible (PairingManual); the headless
// mode has no screen and opens it on its own (PairingWhileUnpaired,
// PairingAlways).

// PairingMode says who opens pairing.
type PairingMode int

const (
	// PairingManual: the UI opens pairing with SetPairingVisible while it
	// shows the QR code.
	PairingManual PairingMode = iota
	// PairingWhileUnpaired: pairing is open while no device is paired
	// (headless mode, tests and development).
	PairingWhileUnpaired
	// PairingAlways: pairing stays open (headless --pairing-open).
	PairingAlways
)

// HasPairedClients reports whether at least one device is paired.
func (a *App) HasPairedClients() bool { return len(a.clients.List()) > 0 }

// SetPairingVisible opens pairing while the UI shows the QR code, and
// closes it (secret invalidated) when it goes away.
// Ignored in the automatic modes.
func (a *App) SetPairingVisible(visible bool) error {
	if a.opts.Pairing != PairingManual {
		return nil
	}
	return a.setPairingOpen(visible)
}

// syncPairing applies the automatic modes: called at Start, after a
// pairing, after a device is forgotten and on each network check (a
// device unpaired through the API).
func (a *App) syncPairing() {
	var open bool
	switch a.opts.Pairing {
	case PairingAlways:
		open = true
	case PairingWhileUnpaired:
		open = !a.HasPairedClients()
	default:
		return
	}
	if err := a.setPairingOpen(open); err != nil {
		a.log.Error("pairing", "error", err)
	}
}

func (a *App) setPairingOpen(open bool) error {
	if a.pairing.IsOpen() == open {
		return nil
	}
	if err := a.pairing.SetOpen(open); err != nil {
		return err
	}
	// Never the secret.
	if open {
		a.log.Info("pairing opened")
	} else {
		a.log.Info("pairing closed")
	}
	return nil
}
