package app

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tfournier/ReminisLink/internal/testutil"
	"github.com/tfournier/ReminisLink/internal/tlsid"
)

type contextT = context.Context

func slogDefault() *slog.Logger  { return slog.Default() }
func restoreSlog(l *slog.Logger) { slog.SetDefault(l) }
func itoa(n int) string          { return strconv.Itoa(n) }

// httpsBase is the address of the running pak (TLS only, M13).
func httpsBase(a *App) string { return "https://127.0.0.1:" + itoa(a.Port()) }

// pinnedHTTP is an HTTP client that accepts only the pak's certificate.
func pinnedHTTP(a *App) *http.Client {
	return &http.Client{Transport: &http.Transport{TLSClientConfig: tlsid.ClientConfig(a.Pin())}}
}

// pairTLS pairs a new client and returns it, talking HTTPS with the pin.
func pairTLS(t *testing.T, a *App) *testutil.Client {
	t.Helper()
	c := testutil.PairClient(t, a.clients, httpsBase(a))
	c.HTTP = pinnedHTTP(a)
	return c
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
