package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"

	"github.com/tfournier/ReminisLink/internal/app"
	"github.com/tfournier/ReminisLink/internal/auth"
)

// pairingDisplay prints the pairing link, the PIN and an ASCII QR code in
// the terminal (headless mode), and prints them again each time the
// secret changes (after a pairing, an expiry or too many failures) or the
// console is renamed. This goes to the terminal only, never to the log.
type pairingDisplay struct {
	mu     sync.Mutex
	out    io.Writer
	status func() (app.Status, error)
	shown  string // generation and name on screen
}

func newPairingDisplay(out io.Writer, status func() (app.Status, error)) *pairingDisplay {
	return &pairingDisplay{out: out, status: status}
}

// setStatus sets the source of the display (once the server is started).
func (d *pairingDisplay) setStatus(status func() (app.Status, error)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.status = status
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
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.status == nil {
		return
	}
	st, err := d.status()
	if err != nil {
		slog.Error("pairing secret", "error", err)
		return
	}
	key := fmt.Sprintf("%t/%d/%s/%s", st.PairingOpen, st.Generation, st.Name, st.IP)
	if key == d.shown {
		return
	}
	d.shown = key
	d.print(st)
}

func (d *pairingDisplay) print(st app.Status) {
	fmt.Fprintf(d.out, "\nReminisLink %q on %s:%d\n", st.Name, st.IP, st.Port)
	if !st.PairingOpen {
		fmt.Fprintln(d.out, "Pairing closed: a device is already paired (run with --pairing-open to pair another one)")
		return
	}
	if q, err := qrcode.New(st.PairingLink, qrcode.Medium); err == nil {
		fmt.Fprint(d.out, q.ToSmallString(false))
	}
	fmt.Fprintf(d.out, "Pairing link: %s\n", st.PairingLink)
	fmt.Fprintf(d.out, "PIN: %s %s (expires at %s)\n", st.PIN[:3], st.PIN[3:], st.ExpiresAt.Local().Format("15:04:05"))
	if st.BlockedUntil.After(time.Now()) {
		fmt.Fprintf(d.out, "Pairing blocked until %s (too many failed attempts)\n", st.BlockedUntil.Local().Format("15:04:05"))
	}
}

// paired is the OnPaired callback of the pak.
func (d *pairingDisplay) paired(c auth.Client) {
	d.mu.Lock()
	fmt.Fprintf(d.out, "\nPaired with %q (%s)\n", c.Name, c.ID)
	d.mu.Unlock()
	d.refresh()
}
