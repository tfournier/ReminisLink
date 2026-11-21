package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/app"
	"github.com/tfournier/ReminisLink/internal/auth"
)

func TestParseFlags(t *testing.T) {
	var stderr bytes.Buffer
	o, err := parseFlags([]string{"--headless", "--sd", "/tmp/sd", "--port", "48000", "--name", "Brick de test"}, &stderr)
	if err != nil || !o.headless || o.desktop || o.sd != "/tmp/sd" || o.port != 48000 || o.name != "Brick de test" {
		t.Fatalf("got %+v, %v", o, err)
	}
	if o.pairingOpen {
		t.Fatal("pairing open by default")
	}
	if o, err := parseFlags([]string{"--headless", "--pairing-open"}, &stderr); err != nil || !o.pairingOpen {
		t.Fatalf("--pairing-open: %+v, %v", o, err)
	}
	o, err = parseFlags(nil, &stderr)
	if err != nil || o.headless || o.port != 0 {
		t.Fatalf("defaults: %+v, %v", o, err)
	}
	o, err = parseFlags([]string{"--desktop", "--sd", "/tmp/sd"}, &stderr)
	if err != nil || !o.desktop || o.headless {
		t.Fatalf("desktop: %+v, %v", o, err)
	}
	if o.os != "nextui" {
		t.Fatalf("default OS %q", o.os)
	}
	if o, err := parseFlags([]string{"--os", "knulli"}, &stderr); err != nil || o.os != "knulli" {
		t.Fatalf("--os knulli: %+v, %v", o, err)
	}
	t.Setenv("REMINISLINK_OS", "knulli") // set by the Knulli launcher
	if o, err := parseFlags(nil, &stderr); err != nil || o.os != "knulli" {
		t.Fatalf("REMINISLINK_OS: %+v, %v", o, err)
	}
	// The launchers pass --os: it wins over the environment, and an --os
	// given after it (a manual launch through the script) wins over it.
	if o, err := parseFlags([]string{"--os", "nextui"}, &stderr); err != nil || o.os != "nextui" {
		t.Fatalf("launch.sh with REMINISLINK_OS=knulli: %+v, %v", o, err)
	}
	if o, err := parseFlags([]string{"--os", "nextui", "--os", "knulli"}, &stderr); err != nil || o.os != "knulli" {
		t.Fatalf("manual --os after the launcher's: %+v, %v", o, err)
	}
	t.Setenv("REMINISLINK_OS", "")
	for _, args := range [][]string{{"--os", "batocera"}, {"--port", "70000"}, {"--port", "-1"}, {"extra"}, {"--nope"},
		{"--desktop"}, {"--desktop", "--headless", "--sd", "/x"}, {"--pairing-open"}, {"--desktop", "--sd", "/x", "--pairing-open"}} {
		if _, err := parseFlags(args, &stderr); err == nil {
			t.Errorf("%q accepted", args)
		}
	}
}

func TestRoots(t *testing.T) {
	t.Setenv("ROMS_PATH", "/elsewhere/Roms")
	t.Setenv("PLATFORM", "tg5050")
	r := roots(options{sd: "/card"})
	if r.Roms != filepath.Join("/card", "Roms") || r.Userdata != filepath.Join("/card", ".userdata", "tg5050") {
		t.Errorf("--sd ignores the path variables: %+v", r)
	}
	if r := roots(options{}); r.Roms != "/elsewhere/Roms" {
		t.Errorf("environment: %+v", r)
	}
	if r := roots(options{sd: "/card", os: "knulli"}); r.Roms != filepath.Join("/card", "roms") || r.Data != filepath.Join("/card", "system", "ReminisLink") {
		t.Errorf("--os knulli --sd: %+v", r)
	}
}

// testPin is a certificate pin (b64u of 32 bytes).
const testPin = "dx55JGi5v9aA8sTa_6qpXzS1I0iDjz86z8UfYi-fGIw"

func TestPairingDisplay(t *testing.T) {
	now := time.Date(2026, 11, 18, 17, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	const id = "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10"
	s, err := auth.NewPairingSession(id, clock, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Open(); err != nil {
		t.Fatal(err)
	}
	name := "Brick test"
	status := func() (app.Status, error) {
		snap, err := s.Snapshot()
		return app.Status{
			Name: name, IP: "192.168.1.42", Port: 47821, PairingOpen: snap.Open,
			PairingLink: auth.PairingLink(id, name, snap.Secret, "192.168.1.42", 47821, testPin),
			ExpiresAt:   snap.ExpiresAt, Generation: snap.Generation,
		}, err
	}
	var out bytes.Buffer
	d := newPairingDisplay(&out, nil)
	d.refresh() // no status yet: nothing
	if out.Len() != 0 {
		t.Fatalf("printed without status: %s", out.String())
	}
	d.setStatus(status)
	d.refresh()
	snap, _ := s.Snapshot()
	text := out.String()
	link := auth.PairingLink(id, "Brick test", snap.Secret, "192.168.1.42", 47821, testPin)
	if !strings.Contains(text, link) || strings.Contains(text, "PIN:") || !strings.Contains(text, "█") {
		t.Fatalf("display:\n%s", text)
	}
	// Nothing new until the secret or the name changes.
	out.Reset()
	d.refresh()
	if out.Len() != 0 {
		t.Fatalf("printed again: %s", out.String())
	}
	name = "Renamed"
	d.refresh()
	if !strings.Contains(out.String(), `"Renamed"`) {
		t.Fatalf("rename not shown: %s", out.String())
	}
	out.Reset()
	s.Close() // the first device is paired: pairing closes
	d.paired(auth.Client{ID: "3b1c8d2a-5e6f-4a7b-8c9d-0e1f2a3b4c5d", Name: "iPhone"})
	if !strings.Contains(out.String(), `Paired with "iPhone"`) || !strings.Contains(out.String(), "Pairing closed") ||
		strings.Contains(out.String(), "PIN:") {
		t.Fatalf("paired: %s", out.String())
	}
	// run stops with its context.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not stop")
	}
}

func TestFatalErrorReachesTheLogFile(t *testing.T) {
	t.Setenv("PLATFORM", "")
	prev := slog.Default()
	defer slog.SetDefault(prev)
	sd := t.TempDir()
	conf := filepath.Join(sd, ".userdata", "shared", "ReminisLink", "config.json")
	if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conf, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runHeadlessTo(options{headless: true, sd: sd}, io.Discard)
	var logged app.LoggedError
	if err == nil || !errors.As(err, &logged) {
		t.Fatalf("broken config.json: %v (must be a loggedError, so main does not print it twice)", err)
	}
	data, _ := os.ReadFile(filepath.Join(sd, ".userdata", "tg5040", "logs", app.LogFileName))
	if !strings.Contains(string(data), "level=ERROR msg=fatal") {
		t.Fatalf("fatal error not in the log file:\n%s", data)
	}
}
