package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"

	"github.com/tfournier/ReminisLink/internal/auth"
)

// pairingDisplay prints the pairing link, the PIN and an ASCII QR code in
// the terminal (headless mode), and prints them again each time the
// secret changes (after a pairing, an expiry or too many failures). This
// goes to the terminal only, never to the log.
type pairingDisplay struct {
	mu       sync.Mutex
	out      io.Writer
	session  *auth.PairingSession
	deviceID string
	name     string
	host     string
	port     int
	shown    uint64 // generation on screen
}

func newPairingDisplay(out io.Writer, s *auth.PairingSession, deviceID, name, host string, port int) *pairingDisplay {
	return &pairingDisplay{out: out, session: s, deviceID: deviceID, name: name, host: host, port: port}
}

// run refreshes the display every second until ctx is done.
func (d *pairingDisplay) run(ctx context.Context) {
	d.refresh()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.refresh()
		}
	}
}

// refresh prints the pairing information if the secret changed.
func (d *pairingDisplay) refresh() {
	snap, err := d.session.Snapshot()
	if err != nil {
		slog.Error("pairing secret", "error", err)
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if snap.Generation == d.shown {
		return
	}
	d.shown = snap.Generation
	d.print(snap)
}

func (d *pairingDisplay) print(snap auth.PairingSnapshot) {
	link := auth.PairingURL(d.deviceID, d.name, snap.Secret, d.host, d.port)
	fmt.Fprintf(d.out, "\nReminisLink %q on %s:%d\n", d.name, d.host, d.port)
	if q, err := qrcode.New(link, qrcode.Medium); err == nil {
		fmt.Fprint(d.out, q.ToSmallString(false))
	}
	fmt.Fprintf(d.out, "Pairing link: %s\n", link)
	fmt.Fprintf(d.out, "PIN: %s %s (expires at %s)\n", snap.PIN[:3], snap.PIN[3:], snap.ExpiresAt.Local().Format("15:04:05"))
	if !snap.BlockedUntil.IsZero() {
		fmt.Fprintf(d.out, "Pairing blocked until %s (too many failed attempts)\n", snap.BlockedUntil.Local().Format("15:04:05"))
	}
}

// paired is the server's OnPaired callback.
func (d *pairingDisplay) paired(c auth.Client) {
	d.mu.Lock()
	fmt.Fprintf(d.out, "\nPaired with %q (%s)\n", c.Name, c.ID)
	d.mu.Unlock()
	d.refresh()
}
