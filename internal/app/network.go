package app

import "time"

// The Wi-Fi address can change while the pak runs: a short press on the
// power button puts the console to sleep through NextUI's suspend script,
// which stops the Wi-Fi; on wake-up DHCP may give another address. The
// pairing link (its h= parameter) and the Bonjour announcement follow it.

// watchNetwork checks the Wi-Fi address every checkEvery until stopWatch
// is closed.
func (a *App) watchNetwork() {
	defer close(a.watchDone)
	t := time.NewTicker(a.checkEvery)
	defer t.Stop()
	for {
		select {
		case <-a.stopWatch:
			return
		case <-t.C:
			a.checkNetwork()
		}
	}
}

// checkNetwork compares the Wi-Fi address with the one of the pairing
// link. A new address replaces it and the announcement is registered again
// (the QR code follows, its link being built from Status). Without an
// address, the pak is offline (Status.WiFi false: the UI says "No Wi-Fi"
// without quitting) and the announcement is withdrawn, until an address
// comes back. With Options.NoWiFiIP (headless mode), that address is used
// instead of going offline.
func (a *App) checkNetwork() {
	a.syncPairing() // a device may have unpaired itself through the API
	ip, ok := a.WiFiIPv4()
	if !ok && a.opts.NoWiFiIP != "" {
		ip, ok = a.opts.NoWiFiIP, true
	}
	a.mu.Lock()
	changed := ok != a.wifi || ip != a.ip
	if ok {
		a.ip, a.wifi = ip, true
	} else {
		a.ip, a.wifi = "", false
	}
	a.mu.Unlock()
	if !changed {
		return
	}
	if !ok {
		a.log.Warn("Wi-Fi address lost, waiting for it to come back")
		a.withdraw()
		return
	}
	a.log.Info("Wi-Fi address changed", "ip", ip)
	if err := a.reannounce(); err != nil {
		a.log.Error("cannot announce on the new address", "error", err)
	}
}

// WiFi reports whether the pak has a Wi-Fi address (after Start).
func (a *App) WiFi() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.wifi
}
