package server

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// An unauthenticated client cannot make the log grow without bound: values
// from the network are cut and rejected requests are sampled.
func TestLogSizeIsBounded(t *testing.T) {
	f := newFixture(t)
	hugeClient := strings.Repeat("x", 8<<10)
	hugePath := "/api/v1/" + strings.Repeat("p", 4<<10)
	const requests = 300
	for i := 0; i < requests; i++ {
		target := "/api/v1/systems"
		if i%2 == 0 {
			target = hugePath
		}
		req, _ := http.NewRequest("GET", f.http.URL+target, nil)
		req.Header.Set("X-RL-Client", hugeClient)
		f.client.Send(t, req)
	}
	logs := f.logs.String()
	for _, line := range strings.Split(logs, "\n") {
		if len(line) > 1500 {
			t.Fatalf("log line of %d bytes: %.200s", len(line), line)
		}
	}
	if strings.Contains(logs, "xxxxxxxx") {
		t.Error("raw X-RL-Client logged")
	}
	if len(logs) > 64<<10 {
		t.Errorf("%d requests wrote %d bytes of log", requests, len(logs))
	}
	if n := strings.Count(logs, "\n"); n > RejectLogBurst+5 {
		t.Errorf("%d log lines for %d rejected requests", n, requests)
	}
	// Once the rate allows it again, the dropped lines are reported.
	f.clock.Advance(time.Second)
	f.unsigned("GET", "/api/v1/systems", nil)
	if !strings.Contains(f.logs.String(), "log lines dropped") {
		t.Error("dropped lines not reported")
	}
}

func TestSampledHandler(t *testing.T) {
	var buf bytes.Buffer
	c := &clock{t: time.Unix(0, 0)}
	h := newSampledHandler(slog.NewTextHandler(&buf, nil), 1, 2, c.Now)
	log := slog.New(h).With("k", "v")
	for i := 0; i < 5; i++ {
		log.Info("line")
	}
	if n := strings.Count(buf.String(), "msg=line"); n != 2 {
		t.Fatalf("%d lines passed, want 2:\n%s", n, buf.String())
	}
	c.Advance(time.Second)
	log.Info("line")
	if !strings.Contains(buf.String(), "count=3") || strings.Count(buf.String(), "msg=line") != 3 {
		t.Fatalf("after refill:\n%s", buf.String())
	}
	if !h.Enabled(context.Background(), slog.LevelInfo) || h.WithGroup("g") == nil {
		t.Fatal("handler methods")
	}
}

func TestMaxHeaderBytes(t *testing.T) {
	f := newFixture(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.server.Serve(ctx, ln) }()
	defer func() { cancel(); <-done }()
	req, _ := http.NewRequest("GET", "http://"+ln.Addr().String()+"/api/v1/hello", nil)
	req.Header.Set("X-Big", strings.Repeat("b", 32<<10))
	r := f.client.Send(t, req)
	if r.Status != http.StatusRequestHeaderFieldsTooLarge {
		t.Fatalf("32 KiB of headers: %d", r.Status)
	}
	small, _ := http.NewRequest("GET", "http://"+ln.Addr().String()+"/api/v1/hello", nil)
	if r := f.client.Send(t, small); r.Status != 200 {
		t.Fatalf("normal request: %d", r.Status)
	}
}
