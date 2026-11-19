package server

import (
	"bytes"
	"context"
	"fmt"
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
	// Paths ServeMux would redirect (now 404 without any check, M8), and
	// unsigned routes.
	targets := []string{"/api/v1/systems", hugePath, "/api/v1//" + strings.Repeat("p", 400),
		"/api/v1/./systems", "/x/../api/v1/systems", "/api/v1/hello"}
	const requests = 300
	for i := 0; i < requests; i++ {
		target := targets[i%len(targets)]
		req, _ := http.NewRequest("GET", f.http.URL+target, nil)
		req.URL.Opaque = target // keep "//", "/./" and "/../" as sent
		req.Header.Set("X-RL-Client", hugeClient)
		f.client.HTTP = noRedirect
		f.client.Send(t, req)
	}
	logs := f.logs.String()
	for _, line := range strings.Split(logs, "\n") {
		if len(line) > 1500 {
			t.Fatalf("log line of %d bytes: %.200s", len(line), line)
		}
	}
	if strings.Contains(logs, "status=307") || strings.Contains(logs, "status=301") {
		t.Error("a path was answered with a redirection")
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

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func TestAcceptedRequestsAreNotSampled(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < RejectLogBurst+20; i++ {
		f.unsigned("GET", "/api/v1/nope", nil)
	}
	if r := f.client.Do(t, "GET", "/api/v1/systems", nil); r.Status != 200 {
		t.Fatal(r.Status)
	}
	if !strings.Contains(f.logs.String(), `route="GET /api/v1/systems" path=/api/v1/systems status=200`) {
		t.Errorf("accepted request not logged after the sample budget was spent:\n%s", f.logs.String())
	}
}

func TestSampledHandler(t *testing.T) {
	var buf bytes.Buffer
	c := &clock{t: time.Unix(0, 0)}
	h := newSampledHandler(slog.NewTextHandler(&buf, nil), 1, 2, c.Now, nil)
	log := slog.New(h).With("k", "v")
	for i := 0; i < 5; i++ {
		log.Info("line")
	}
	if n := strings.Count(buf.String(), "msg=line"); n != 2 {
		t.Fatalf("%d lines passed, want 2:\n%s", n, buf.String())
	}
	c.Advance(time.Second)
	log.Info("line")
	if !strings.Contains(buf.String(), "count=3 by.line=3") || strings.Count(buf.String(), "msg=line") != 3 {
		t.Fatalf("after refill:\n%s", buf.String())
	}
	if !h.Enabled(context.Background(), slog.LevelInfo) || h.WithGroup("g") == nil {
		t.Fatal("handler methods")
	}
}

func TestSampledHandlerCountsPerMessage(t *testing.T) {
	var buf bytes.Buffer
	c := &clock{t: time.Unix(0, 0)}
	log := slog.New(newSampledHandler(slog.NewTextHandler(&buf, nil), 1, 1, c.Now, nil))
	log.Info("request")
	log.Info("pairing failed")
	log.Info("pairing failed")
	log.Info("request")
	for i := 0; i < 30; i++ {
		log.Info(fmt.Sprint("varied message ", i))
	}
	c.Advance(time.Second)
	log.Info("request")
	out := buf.String()
	for _, want := range []string{`count=33`, `"by.pairing failed"=2`, `by.request=1`, `by.other=`} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %s:\n%s", want, out)
		}
	}
}

func TestSampledHandlerStopsWhenTheLogIsFull(t *testing.T) {
	var buf bytes.Buffer
	full := false
	c := &clock{t: time.Unix(0, 0)}
	log := slog.New(newSampledHandler(slog.NewTextHandler(&buf, nil), 100, 100, c.Now, func() bool { return full }))
	log.Info("before")
	full = true
	for i := 0; i < 10; i++ {
		log.Info("after")
	}
	out := buf.String()
	if strings.Count(out, "msg=before") != 1 || strings.Contains(out, "msg=after") || strings.Count(out, "log file full") != 1 {
		t.Fatalf("full log:\n%s", out)
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

func TestFullLogKeepsAcceptedRequests(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.LogFull = func() bool { return true } })
	f.unsigned("GET", "/api/v1/hello", nil)
	f.unsigned("GET", "/api/v1/nope", nil)
	if r := f.client.Do(t, "GET", "/api/v1/systems", nil); r.Status != 200 {
		t.Fatal(r.Status)
	}
	logs := f.logs.String()
	if strings.Contains(logs, "/api/v1/nope") || strings.Contains(logs, "path=/api/v1/hello") {
		t.Errorf("sampled lines written to a full log:\n%s", logs)
	}
	if !strings.Contains(logs, "path=/api/v1/systems status=200") || strings.Count(logs, "log file full") != 1 {
		t.Errorf("full log:\n%s", logs)
	}
}
