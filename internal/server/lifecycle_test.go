package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tfournier/ReminisLink/internal/auth"
)

func TestHTTPServerSettings(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := f.server.httpServer(ctx)
	if srv.ReadHeaderTimeout != 10*time.Second || srv.IdleTimeout != 60*time.Second || srv.ReadTimeout != 0 || srv.WriteTimeout != 0 || srv.MaxHeaderBytes != 16<<10 {
		t.Errorf("timeouts: header %v idle %v read %v write %v", srv.ReadHeaderTimeout, srv.IdleTimeout, srv.ReadTimeout, srv.WriteTimeout)
	}
	if srv.ErrorLog == nil {
		t.Error("ErrorLog not set")
	} else {
		srv.ErrorLog.Print("probe from net/http")
		if !strings.Contains(f.logs.String(), "probe from net/http") {
			t.Error("ErrorLog does not reach the log")
		}
	}
	if srv.BaseContext(nil) != ctx {
		t.Error("BaseContext does not return the shutdown context")
	}
	if BodyIdleTimeout != 60*time.Second || ShutdownTimeout != 10*time.Second {
		t.Error("spec timeouts changed")
	}
}

func TestListenTriesTheNextPorts(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port
	ln, got, err := Listen("127.0.0.1", port)
	if err != nil {
		t.Skipf("next port not free: %v", err)
	}
	defer ln.Close()
	if got <= port || got >= port+PortAttempts {
		t.Fatalf("got port %d after busy %d", got, port)
	}
	if ln.Addr().(*net.TCPAddr).Port != got {
		t.Fatal("listener on another port")
	}
}

func TestListenGivesUp(t *testing.T) {
	if _, _, err := Listen("203.0.113.1", 47821); err == nil {
		t.Fatal("listening on a foreign address worked")
	}
}

func TestShutdownGate(t *testing.T) {
	f := newFixture(t)
	f.server.BeginShutdown()
	for _, tc := range []struct{ method, target, path string }{
		{"GET", "/api/v1/hello", "/hello"},
		{"GET", "/api/v1/systems", "/systems"},
		{"POST", "/api/v1/pair", "/pair"},
	} {
		r := f.client.Do(t, tc.method, tc.target, nil)
		f.check(tc.method, tc.path, r)
		if r.Status != 503 || r.ErrorCode() != auth.CodeShuttingDown {
			t.Errorf("%s %s: %d %s", tc.method, tc.target, r.Status, r.Body)
		}
	}
}

func TestRecoverPanics(t *testing.T) {
	f := newFixture(t)
	h := f.server.recoverPanics(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/systems", nil))
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), `"internal"`) {
		t.Fatalf("panic: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(f.logs.String(), "panic in handler") {
		t.Error("panic not logged")
	}
}

func TestAccessLogHasNoSecrets(t *testing.T) {
	f := newFixture(t)
	req := f.client.Request(t, "GET", "/api/v1/systems/"+id(gba)+"/games", nil)
	if r := f.client.Send(t, req); r.Status != 200 {
		t.Fatal(r.Status)
	}
	logs := f.logs.String()
	if !strings.Contains(logs, "route=\"GET /api/v1/systems/{systemId}/games\"") || !strings.Contains(logs, "status=200") ||
		!strings.Contains(logs, "client="+f.client.ClientID) {
		t.Errorf("access log:\n%s", logs)
	}
	for _, h := range []string{auth.HeaderSignature, auth.HeaderNonce} {
		if strings.Contains(logs, req.Header.Get(h)) {
			t.Errorf("log contains %s:\n%s", h, logs)
		}
	}
	if strings.Contains(logs, auth.EncodeB64U(f.client.Key)) {
		t.Error("log contains the key")
	}
}

// A client that stops sending its body is cut off after the idle timeout.
func TestBodyIdleTimeout(t *testing.T) {
	f := newFixture(t)
	f.server.timeouts.bodyIdle = 200 * time.Millisecond
	conn, err := net.Dial("tcp", strings.TrimPrefix(f.http.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "POST /api/v1/pair HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"method\":")
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	start := time.Now()
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("no answer: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 || time.Since(start) > 3*time.Second {
		t.Fatalf("got %d after %v", resp.StatusCode, time.Since(start))
	}
}

// Serve stops on ctx: running requests finish, then the base context is
// cancelled, so a signed request waiting for the body memory budget
// answers 503 shutting_down.
func TestServeShutdown(t *testing.T) {
	f := newFixture(t, func(o *Options) {
		o.Verifier.BodyBudget = auth.BodyLimitJSON
	})
	f.server.timeouts.shutdown = 300 * time.Millisecond
	f.server.timeouts.grace = 300 * time.Millisecond
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.server.Serve(ctx, ln) }()
	f.client.BaseURL = "http://" + addr

	if r := f.client.Do(t, "GET", "/api/v1/systems", nil); r.Status != 200 {
		t.Fatalf("before shutdown: %d", r.Status)
	}

	// The holder takes the whole budget and never finishes its body.
	body := []byte(strings.Repeat(" ", auth.BodyLimitJSON))
	holder := signedRaw(t, f, "DELETE", "/api/v1/clients/me", body)
	hconn := dial(t, addr)
	defer hconn.Close()
	hconn.Write(holder[:len(holder)-10])
	time.Sleep(100 * time.Millisecond)

	// The waiter waits for the budget.
	waiter := signedRaw(t, f, "DELETE", "/api/v1/clients/me", []byte("{}"))
	wconn := dial(t, addr)
	defer wconn.Close()
	wconn.Write(waiter)
	time.Sleep(100 * time.Millisecond)

	start := time.Now()
	cancel()
	_ = wconn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(wconn), nil)
	if err != nil {
		t.Fatalf("waiter: %v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 503 || !strings.Contains(string(b), auth.CodeShuttingDown) {
		t.Fatalf("waiter: %d %s", resp.StatusCode, b)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return")
	}
	if d := time.Since(start); d < 300*time.Millisecond || d > 3*time.Second {
		t.Errorf("shutdown took %v", d)
	}
	if _, ok := f.clients.Lookup(f.client.ClientID); !ok {
		t.Error("the cancelled request was executed")
	}
	if _, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		t.Error("still listening after shutdown")
	}
}

func dial(t *testing.T, addr string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// signedRaw returns a raw HTTP/1.1 request signed by the fixture client.
func signedRaw(t *testing.T, f *fixture, method, target string, body []byte) []byte {
	t.Helper()
	req := f.client.Request(t, method, target, body)
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\nHost: x\r\nContent-Length: %d\r\n", method, target, len(body))
	for k, v := range req.Header {
		fmt.Fprintf(&b, "%s: %s\r\n", k, v[0])
	}
	b.WriteString("\r\n")
	b.Write(body)
	return []byte(b.String())
}
