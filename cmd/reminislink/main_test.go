package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
)

func TestParseFlags(t *testing.T) {
	var stderr bytes.Buffer
	o, err := parseFlags([]string{"--headless", "--sd", "/tmp/sd", "--port", "48000", "--name", "Brick de test"}, &stderr)
	if err != nil || !o.headless || o.desktop || o.sd != "/tmp/sd" || o.port != 48000 || o.name != "Brick de test" {
		t.Fatalf("got %+v, %v", o, err)
	}
	o, err = parseFlags(nil, &stderr)
	if err != nil || o.headless || o.port != 0 {
		t.Fatalf("defaults: %+v, %v", o, err)
	}
	for _, args := range [][]string{{"--port", "70000"}, {"--port", "-1"}, {"extra"}, {"--nope"}} {
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
}

func TestOpenLog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	var also bytes.Buffer
	logger, lf, err := openLog(dir, &also)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("hello", "k", "v")
	if lf.size.Load() == 0 || lf.full() {
		t.Errorf("size %d", lf.size.Load())
	}
	lf.f.Close()
	data, err := os.ReadFile(filepath.Join(dir, LogFileName))
	if err != nil || !strings.Contains(string(data), "hello") || !strings.Contains(also.String(), "hello") {
		t.Fatalf("log file %q, also %q, %v", data, also.String(), err)
	}
}

func TestPairingDisplay(t *testing.T) {
	now := time.Date(2026, 11, 18, 17, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	s, err := auth.NewPairingSession("7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10", clock, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	d := newPairingDisplay(&out, s, "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10", "Brick test", "192.168.1.42", 47821)
	d.refresh()
	snap, _ := s.Snapshot()
	text := out.String()
	link := auth.PairingURL("7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10", "Brick test", snap.Secret, "192.168.1.42", 47821)
	if !strings.Contains(text, link) || !strings.Contains(text, snap.PIN[:3]+" "+snap.PIN[3:]) || !strings.Contains(text, "█") {
		t.Fatalf("display:\n%s", text)
	}
	// Nothing new until the secret changes.
	out.Reset()
	d.refresh()
	if out.Len() != 0 {
		t.Fatalf("printed again: %s", out.String())
	}
	d.paired(auth.Client{ID: "3b1c8d2a-5e6f-4a7b-8c9d-0e1f2a3b4c5d", Name: "iPhone"})
	if !strings.Contains(out.String(), `Paired with "iPhone"`) {
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
	if err == nil {
		t.Fatal("broken config.json accepted")
	}
	data, _ := os.ReadFile(filepath.Join(sd, ".userdata", "tg5040", "logs", LogFileName))
	if !strings.Contains(string(data), "level=ERROR msg=fatal") {
		t.Fatalf("fatal error not in the log file:\n%s", data)
	}
}

func TestOpenLogRotatesLargeFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, LogFileName)
	old := filepath.Join(dir, LogOldFileName)
	if err := os.WriteFile(old, []byte("older"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), LogRotateBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	_, lf, err := openLog(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lf.f.Close()
	if fi, err := os.Stat(old); err != nil || fi.Size() != LogRotateBytes+1 {
		t.Fatalf("old log: %v %v", fi, err)
	}
	if lf.size.Load() != 0 {
		t.Fatalf("new log size %d", lf.size.Load())
	}
	// A small file is kept and its size counted.
	lf.f.Close()
	if err := os.WriteFile(path, []byte("small"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, lf2, err := openLog(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lf2.f.Close()
	if lf2.size.Load() != 5 {
		t.Fatalf("small log size %d", lf2.size.Load())
	}
	lf2.size.Store(LogCapBytes)
	if !lf2.full() {
		t.Fatal("cap not reported")
	}
}
